package incident

import (
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/safegit"
)

func code(n int) *int { return &n }

func TestSuggestRefusesNonWorktree(t *testing.T) {
	run := func(args []string) hostcmd.Result {
		return hostcmd.Result{ExitCode: code(0), Stdout: "false\n"}
	}
	_, err := SuggestRanges(t.TempDir(), run)
	if err == nil || err.Error() != "The requested directory is not inside a non-bare Git worktree." {
		t.Fatalf("%v", err)
	}
}

func TestSuggestUnavailableGit(t *testing.T) {
	run := func(args []string) hostcmd.Result { return hostcmd.Result{TimedOut: true} }
	_, err := SuggestRanges(t.TempDir(), run)
	if err == nil || !strings.HasPrefix(err.Error(), "FaultLine could not safely inspect local Git state while ") {
		t.Fatalf("%v", err)
	}
}

// The fixtures of tests/incident-intake.test.ts.
var (
	fakeHead           = strings.Repeat("a", 40)
	fakeMergeBase      = strings.Repeat("b", 40)
	fakeParent         = strings.Repeat("c", 40)
	fakeUpstreamCommit = strings.Repeat("d", 40)
)

type fakeIntake struct {
	calls [][]string
}

func ok(stdout string) hostcmd.Result { return hostcmd.Result{ExitCode: code(0), Stdout: stdout} }

// commandTail drops DOCTOR_SAFE_GIT_CONFIG and -C <repository>.
func commandTail(args []string) []string { return args[len(safegit.Doctor())+2:] }

func (f *fakeIntake) runner(respond func(tail string) hostcmd.Result) IntakeRunner {
	return func(args []string) hostcmd.Result {
		f.calls = append(f.calls, slices.Clone(args))
		return respond(strings.Join(commandTail(args), " "))
	}
}

// fullCandidateResponse is the TS test's fullCandidateResponse; upstream
// is "", "missing", "head" or "invalid", parent "", "missing" or "invalid".
func fullCandidateResponse(repository, upstream, parent string) func(string) hostcmd.Result {
	return func(tail string) hostcmd.Result {
		switch tail {
		case "rev-parse --is-inside-work-tree":
			return ok("true\n")
		case "rev-parse --show-toplevel":
			return ok(repository + "\n")
		case "rev-parse --verify --end-of-options HEAD^{commit}":
			return ok(fakeHead + "\n")
		case "rev-parse --symbolic-full-name @{upstream}":
			switch upstream {
			case "missing":
				return hostcmd.Result{ExitCode: code(128), Stderr: "no upstream"}
			case "invalid":
				return ok("--unsafe\n")
			}
			return ok("refs/remotes/origin/main\n")
		case "rev-parse --verify --end-of-options refs/remotes/origin/main^{commit}":
			return ok(fakeUpstreamCommit + "\n")
		case "merge-base " + fakeHead + " " + fakeUpstreamCommit:
			if upstream == "head" {
				return ok(fakeHead + "\n")
			}
			return ok(fakeMergeBase + "\n")
		case "merge-base --is-ancestor " + fakeMergeBase + " " + fakeHead:
			return ok("")
		case "rev-parse --verify --end-of-options HEAD^":
			switch parent {
			case "missing":
				return hostcmd.Result{ExitCode: code(128), Stderr: "root commit"}
			case "invalid":
				return ok("not-an-object\n")
			}
			return ok(fakeParent + "\n")
		case "merge-base --is-ancestor " + fakeParent + " " + fakeHead:
			return ok("")
		}
		jsexc.Throw(errors.New("Unexpected Git command: " + tail))
		return hostcmd.Result{}
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func statuses(v jsjson.Value) []string {
	var out []string
	for _, o := range v.Get("observations").Items() {
		out = append(out, o.Get("source").Str()+":"+o.Get("status").Str())
	}
	return out
}

func candidateIDs(v jsjson.Value) []string {
	var out []string
	for _, c := range v.Get("candidates").Items() {
		out = append(out, c.Get("id").Str()+":"+c.Get("from").Str()+".."+c.Get("to").Str())
	}
	return out
}

func TestSuggestFullCandidates(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	report, err := SuggestRanges(repository, f.runner(fullCandidateResponse(repository, "", "")))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"schemaVersion": IntakeSchemaVersion, "repository": repository, "repositoryRoot": repository, "head": fakeHead, "automaticSelection": "NONE"} {
		if got := report.Get(key).Str(); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if want := []string{"LOCAL_UPSTREAM_MERGE_BASE:" + fakeMergeBase + ".." + fakeHead, "HEAD_PARENT:" + fakeParent + ".." + fakeHead}; !slices.Equal(candidateIDs(report), want) {
		t.Errorf("candidates %v", candidateIDs(report))
	}
	up := report.Get("candidates").Items()[0]
	if up.Get("confidence").Str() != "MEDIUM" || up.Get("evidence", "upstreamRef").Str() != "refs/remotes/origin/main" ||
		up.Get("evidence", "upstreamCommit").Str() != fakeUpstreamCommit || up.Get("evidence", "remoteContacted").Kind() != jsjson.Bool {
		t.Errorf("upstream candidate %s", jsjson.Stringify(up))
	}
	if want := []string{"LOCAL_UPSTREAM_MERGE_BASE:CANDIDATE_AVAILABLE", "HEAD_PARENT:CANDIDATE_AVAILABLE"}; !slices.Equal(statuses(report), want) {
		t.Errorf("observations %v", statuses(report))
	}
	safe := safegit.Doctor()
	for _, args := range f.calls {
		if !slices.Equal(args[:len(safe)], safe) || args[len(safe)] != "-C" || args[len(safe)+1] != repository {
			t.Errorf("argv %q", args)
		}
		if slices.Contains(args, "fetch") || slices.Contains(args, "ls-remote") {
			t.Errorf("argv %q", args)
		}
	}
	if len(f.calls) != 9 {
		t.Errorf("%d calls", len(f.calls))
	}
}

func TestSuggestParentFallback(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	report, err := SuggestRanges(repository, f.runner(fullCandidateResponse(repository, "missing", "")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"HEAD_PARENT:" + fakeParent + ".." + fakeHead}; !slices.Equal(candidateIDs(report), want) {
		t.Errorf("candidates %v", candidateIDs(report))
	}
	if want := []string{"LOCAL_UPSTREAM_MERGE_BASE:NOT_AVAILABLE", "HEAD_PARENT:CANDIDATE_AVAILABLE"}; !slices.Equal(statuses(report), want) {
		t.Errorf("observations %v", statuses(report))
	}
	if got := report.Get("observations").Items()[0].Get("reason").Str(); got != "No resolvable local upstream reference was configured for the current HEAD; no upstream range was proposed." {
		t.Errorf("reason %q", got)
	}
	for _, args := range f.calls {
		for _, a := range commandTail(args) {
			if strings.Contains(a, "refs/remotes/origin/main") {
				t.Errorf("argv %q", args)
			}
		}
	}
}

func TestSuggestNoRange(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	report, err := SuggestRanges(repository, f.runner(fullCandidateResponse(repository, "head", "missing")))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Get("candidates").Items()) != 0 || report.Get("automaticSelection").Str() != "NONE" {
		t.Errorf("%s", jsjson.Stringify(report))
	}
	if want := []string{"LOCAL_UPSTREAM_MERGE_BASE:NO_RANGE", "HEAD_PARENT:NO_RANGE"}; !slices.Equal(statuses(report), want) {
		t.Errorf("observations %v", statuses(report))
	}
	// The merge base is HEAD: no --is-ancestor call for it.
	if len(f.calls) != 7 {
		t.Errorf("%d calls", len(f.calls))
	}
}

func intakeCode(err error) string {
	var ie *IntakeError
	if errors.As(err, &ie) {
		return ie.Code
	}
	return "?"
}

func TestSuggestRefusals(t *testing.T) {
	root := realTempDir(t)
	unrelated := realTempDir(t)
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	var never fakeIntake
	_, err := SuggestRanges(file, never.runner(func(string) hostcmd.Result {
		jsexc.Throw(errors.New("runner should not be called"))
		return hostcmd.Result{}
	}))
	if intakeCode(err) != "UNSAFE_REPOSITORY_PATH" || err.Error() != "Requested repository directory must be a real directory, not a symlink or file." || len(never.calls) != 0 {
		t.Errorf("file: %v, %d calls", err, len(never.calls))
	}
	if _, err := SuggestRanges(filepath.Join(root, "missing"), nil); err == nil || err.Error() != "Requested repository directory does not exist." {
		t.Errorf("missing: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err == nil {
		if _, err := SuggestRanges(link, nil); intakeCode(err) != "UNSAFE_REPOSITORY_PATH" {
			t.Errorf("symlink: %v", err)
		}
	}

	var nonGit fakeIntake
	_, err = SuggestRanges(root, nonGit.runner(func(tail string) hostcmd.Result {
		if tail == "rev-parse --is-inside-work-tree" {
			return ok("false\n")
		}
		jsexc.Throw(errors.New("Unexpected Git command: " + tail))
		return hostcmd.Result{}
	}))
	if intakeCode(err) != "NOT_A_GIT_WORKTREE" {
		t.Errorf("non-git: %v", err)
	}

	var escaped fakeIntake
	_, err = SuggestRanges(root, escaped.runner(func(tail string) hostcmd.Result {
		switch tail {
		case "rev-parse --is-inside-work-tree":
			return ok("true\n")
		case "rev-parse --show-toplevel":
			return ok(unrelated + "\n")
		}
		jsexc.Throw(errors.New("Unexpected Git command: " + tail))
		return hostcmd.Result{}
	}))
	if intakeCode(err) != "UNSAFE_REPOSITORY_PATH" || err.Error() != "The requested directory falls outside Git's reported worktree root." {
		t.Errorf("escaped root: %v", err)
	}

	var relativeRoot fakeIntake
	_, err = SuggestRanges(root, relativeRoot.runner(func(tail string) hostcmd.Result {
		if tail == "rev-parse --is-inside-work-tree" {
			return ok("true")
		}
		return ok("relative/root\n")
	}))
	if intakeCode(err) != "INVALID_GIT_RESPONSE" || err.Error() != "Git did not return an absolute worktree root." {
		t.Errorf("relative root: %v", err)
	}
}

func TestSuggestOversizedResponse(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	_, err := SuggestRanges(repository, f.runner(func(string) hostcmd.Result {
		return hostcmd.Result{ExitCode: code(0), OutputLimitExceeded: true, Stdout: strings.Repeat("x", 70*1024)}
	}))
	if intakeCode(err) != "GIT_UNAVAILABLE" {
		t.Fatalf("%v", err)
	}
	// normalizeRunnerResult re-checks the cap on the UTF-8 byte lengths.
	_, err = SuggestRanges(repository, f.runner(func(string) hostcmd.Result {
		return hostcmd.Result{ExitCode: code(0), Stdout: "true\n", Stderr: strings.Repeat("é", 32*1024)}
	}))
	if intakeCode(err) != "GIT_UNAVAILABLE" || err.Error() != "FaultLine could not safely inspect local Git state while checking whether the requested directory is a Git worktree." {
		t.Fatalf("%v", err)
	}
	// A runner that throws is a null exit: unavailable.
	_, err = SuggestRanges(repository, f.runner(func(string) hostcmd.Result {
		jsexc.Throw(errors.New("boom"))
		return hostcmd.Result{}
	}))
	if intakeCode(err) != "GIT_UNAVAILABLE" {
		t.Fatalf("%v", err)
	}
	// A plain failure keeps the operation's code.
	_, err = SuggestRanges(repository, f.runner(func(string) hostcmd.Result { return hostcmd.Result{ExitCode: code(128)} }))
	if intakeCode(err) != "NOT_A_GIT_WORKTREE" {
		t.Fatalf("%v", err)
	}
}

func TestSuggestUnsafeUpstreamKeptOutOfArgv(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	report, err := SuggestRanges(repository, f.runner(fullCandidateResponse(repository, "invalid", "")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"HEAD_PARENT:" + fakeParent + ".." + fakeHead}; !slices.Equal(candidateIDs(report), want) {
		t.Errorf("candidates %v", candidateIDs(report))
	}
	if statuses(report)[0] != "LOCAL_UPSTREAM_MERGE_BASE:NOT_AVAILABLE" ||
		report.Get("observations").Items()[0].Get("reason").Str() != "Git returned an unsafe local upstream reference; FaultLine refused to use it." {
		t.Errorf("%s", jsjson.Stringify(report))
	}
	for _, args := range f.calls {
		if slices.Contains(args, "--unsafe^{commit}") {
			t.Errorf("argv %q", args)
		}
	}
}

func TestSuggestDuplicateRange(t *testing.T) {
	repository := realTempDir(t)
	var f fakeIntake
	base := fullCandidateResponse(repository, "", "")
	report, err := SuggestRanges(repository, f.runner(func(tail string) hostcmd.Result {
		if tail == "rev-parse --verify --end-of-options HEAD^" {
			return ok(fakeMergeBase + "\n")
		}
		return base(tail)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"LOCAL_UPSTREAM_MERGE_BASE:CANDIDATE_AVAILABLE", "HEAD_PARENT:DUPLICATE_RANGE"}; !slices.Equal(statuses(report), want) {
		t.Errorf("observations %v", statuses(report))
	}
}

// realGit runs the test's own git with no user configuration.
func realGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine intake test", "-c", "user.email=faultline@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func realCommit(t *testing.T, dir, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "state.txt"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, dir, "add", "state.txt")
	realGit(t, dir, "commit", "-m", message)
	return realGit(t, dir, "rev-parse", "HEAD")
}

func TestSuggestRealGitLocalTrackingRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repository := realTempDir(t)
	realGit(t, repository, "init")
	base := realCommit(t, repository, "good", "base")
	current := realCommit(t, repository, "bad", "current")
	branch := realGit(t, repository, "branch", "--show-current")
	realGit(t, repository, "remote", "add", "origin", "https://127.0.0.1:1/never-contacted.git")
	realGit(t, repository, "update-ref", "refs/remotes/origin/"+branch, base)
	realGit(t, repository, "config", "branch."+branch+".remote", "origin")
	realGit(t, repository, "config", "branch."+branch+".merge", "refs/heads/"+branch)

	report, err := SuggestRanges(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Get("head").Str() != current {
		t.Errorf("head %s", report.Get("head").Str())
	}
	want := "LOCAL_UPSTREAM_MERGE_BASE:" + base + ".." + current
	if ids := candidateIDs(report); !slices.Contains(ids, want) {
		t.Errorf("candidates %v", ids)
	}
	if up := report.Get("candidates").Items()[0]; up.Get("evidence", "remoteContacted").Kind() != jsjson.Bool || up.Get("evidence", "upstreamRef").Str() != "refs/remotes/origin/"+branch {
		t.Errorf("%s", jsjson.Stringify(up))
	}

	// From a subdirectory: repository is the subdirectory, the root the repo.
	sub := filepath.Join(repository, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	report, err = SuggestRanges(sub, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Get("repository").Str() != sub || report.Get("repositoryRoot").Str() != repository {
		t.Errorf("%s", jsjson.Stringify(report))
	}
}

func TestSuggestRealGitMalformedUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repository := realTempDir(t)
	realGit(t, repository, "init")
	realCommit(t, repository, "good", "base")
	realCommit(t, repository, "bad", "current")
	branch := realGit(t, repository, "branch", "--show-current")
	// A local branch upstream whose full name has a "..": refused.
	realGit(t, repository, "config", "branch."+branch+".remote", ".")
	realGit(t, repository, "config", "branch."+branch+".merge", "refs/heads/a..b")
	var calls [][]string
	node := NodeIntakeRunner(repository)
	report, err := SuggestRanges(repository, func(args []string) hostcmd.Result {
		calls = append(calls, args)
		return node(args)
	})
	if err != nil {
		t.Fatal(err)
	}
	if statuses(report)[0] != "LOCAL_UPSTREAM_MERGE_BASE:NOT_AVAILABLE" || len(report.Get("candidates").Items()) != 1 {
		t.Errorf("%s", jsjson.Stringify(report))
	}
	for _, args := range calls[4:] {
		for _, a := range args {
			if strings.Contains(a, "a..b") {
				t.Errorf("argv %q", args)
			}
		}
	}
}

func TestIntakeGitEnvironment(t *testing.T) {
	env := intakeGitEnvironment()
	i := slices.Index(env, "GIT_ALLOW_PROTOCOL=none")
	if i < 0 || i+1 >= len(env) || env[i+1] != "GIT_PROTOCOL_FROM_USER=0" {
		t.Fatalf("%q", env)
	}
	if !strings.HasPrefix(env[0], "PATH=") || env[1] != "GIT_CONFIG_NOSYSTEM=1" {
		t.Fatalf("%q", env)
	}
}

func TestNodeIntakeRunnerBoundsOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	env := intakeGitEnvironment()
	// A chunk split inside a code point is decoded once at the end.
	res := runIntakeCommand("sh", []string{"-c", `printf '\303'; sleep 0.1; printf '\251\n'`}, env, IntakeCommandTimeout, IntakeMaxOutputBytes)
	if res.Stdout != "é\n" || res.ExitCode == nil || *res.ExitCode != 0 || res.TimedOut || res.OutputLimitExceeded {
		t.Errorf("split: %+v", res)
	}
	res = runIntakeCommand("sh", []string{"-c", "head -c 70000 /dev/zero; sleep 5"}, env, IntakeCommandTimeout, IntakeMaxOutputBytes)
	if !res.OutputLimitExceeded || res.ExitCode != nil || len(res.Stdout) != IntakeMaxOutputBytes {
		t.Errorf("flood: exit %v overflow %v len %d", res.ExitCode, res.OutputLimitExceeded, len(res.Stdout))
	}
	res = runIntakeCommand("sh", []string{"-c", "echo x >&2; exec sleep 5"}, env, 200_000_000, IntakeMaxOutputBytes)
	if !res.TimedOut || res.ExitCode != nil || res.Stderr != "x\n" {
		t.Errorf("slow: %+v", res)
	}
	res = runIntakeCommand("faultline-no-such-git", nil, env, IntakeCommandTimeout, IntakeMaxOutputBytes)
	if res.ExitCode != nil || res.Stderr != "" || res.TimedOut {
		t.Errorf("enoent: %+v", res)
	}
}

// safeRefNameByDefinition evaluates SAFE_REF_NAME's lookahead literally:
// at every position the lookahead's .* can reach (it cannot consume a line
// terminator), try each alternative; then match the final class with Go's
// regexp. (TestLiveIncidentSuggest compares safeRefName with the TS regex.)
func safeRefNameByDefinition(s string) bool {
	rest, found := strings.CutPrefix(s, "refs/")
	if !found {
		return false
	}
	for i, at := 0, rest; ; {
		if strings.HasPrefix(at, "..") || strings.HasPrefix(at, "@{") || strings.HasPrefix(at, "//") || at == "/" || at == "." {
			return false
		}
		if at != "" && (strings.ContainsRune("~^:?*[\\", rune(at[0])) || jsWhitespacePrefix(at)) {
			return false
		}
		if at == "" || at[0] == '\n' || at[0] == '\r' || strings.HasPrefix(at, "\u2028") || strings.HasPrefix(at, "\u2029") {
			break
		}
		_, size := utf8.DecodeRuneInString(at)
		i += size
		at = rest[i:]
	}
	return regexp.MustCompile(`^refs/[A-Za-z0-9!#$%&'()+,./;=@_-]+$`).MatchString(s)
}

// jsWhitespacePrefix reports whether s starts with a character of JS \s.
func jsWhitespacePrefix(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff", r) || r >= 0x2000 && r <= 0x200a
}

func TestSafeRefName(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 17))
	pieces := []string{"refs/", "refs", "heads", "remotes/origin/", "main", "/", "//", ".", "..", "@", "@{", "{", "}", "~", "^", ":", "?", "*", "[", "\\",
		" ", "\t", "\n", "\r", "\u00a0", "\u2028", "\u3000", "\ufeff", "é", "-", "_", "#", "!", "$", "%", "&", "'", "(", ")", "+", ",", ";", "=", "a", "Z", "0", ".lock", "\x00", "\"", "<", "|"}
	for _, s := range []string{"refs/remotes/origin/main", "refs/", "refs/a.", "refs/a/", "refs/a..b", "refs/a@{b", "refs/a//b", "refs/@", "refs/.a", "refs/a@b", "--unsafe", "refs/a\nb"} {
		if got, want := safeRefName(s), safeRefNameByDefinition(s); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		if r.IntN(5) != 0 {
			b.WriteString("refs/")
		}
		for n := r.IntN(8); n > 0; n-- {
			b.WriteString(pieces[r.IntN(len(pieces))])
		}
		s := b.String()
		if got, want := safeRefName(s), safeRefNameByDefinition(s); got != want {
			t.Fatalf("%q: %v, want %v", s, got, want)
		}
	}
}
