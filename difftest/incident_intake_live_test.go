//go:build faultline_test

package difftest

import (
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/incident"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// goSuggest runs incident.SuggestRanges like the oracle's
// suggestIncidentRanges op: run is the scripted replay of steps, or the
// default Node runner when steps is nil. Each call is recorded as
// [...argv, timeoutMs, maxOutputBytes].
func goSuggest(repository string, steps []runtimeStep) string {
	calls := []jsjson.Value{}
	node := incident.NodeIntakeRunner(repository)
	run := func(args []string) hostcmd.Result {
		entry := []jsjson.Value{}
		for _, a := range args {
			entry = append(entry, jsjson.MakeString(a))
		}
		entry = append(entry, jsjson.MakeNumber(5000), jsjson.MakeNumber(65536))
		calls = append(calls, jsjson.MakeArray(entry))
		if steps == nil {
			return node(args)
		}
		if len(steps) == 0 {
			jsexc.Throw(errors.New("script exhausted"))
		}
		step := steps[0]
		steps = steps[1:]
		if step.throw != "" {
			jsexc.Throw(errors.New(step.throw))
		}
		return step.result
	}
	v, err := incident.SuggestRanges(repository, run)
	outcome := ""
	if err != nil {
		code := "?"
		var ie *incident.IntakeError
		if errors.As(err, &ie) {
			code = ie.Code
		}
		outcome = "THREW:" + code + ":" + jsexc.Message(err)
	} else {
		outcome = canonicalOf(v)
	}
	return outcome + "|" + canonicalOf(jsjson.MakeArray(calls))
}

func suggestArgs(repository string, steps []runtimeStep) string {
	args := `{"repository":` + jsjson.Quote(repository)
	if steps != nil {
		items := []jsjson.Value{}
		for _, s := range steps {
			items = append(items, s.json())
		}
		args += `,"steps":` + jsjson.Stringify(jsjson.MakeArray(items))
	}
	return args + "}"
}

// tallySuggest counts the outcome kinds: THREW:<code> or the observation
// statuses.
func tallySuggest(reached map[string]int, got string) {
	head, _, _ := strings.Cut(got, "|")
	if strings.HasPrefix(head, "THREW:") {
		code, _, _ := strings.Cut(strings.TrimPrefix(head, "THREW:"), ":")
		reached[code]++
		return
	}
	v, err := jsjson.Parse(head)
	if err != nil {
		return
	}
	for _, o := range v.Get("observations").Items() {
		reached[o.Get("source").Str()+":"+o.Get("status").Str()]++
	}
}

func randomOID(r *rand.Rand) string {
	n := 40
	if r.IntN(6) == 0 {
		n = 64
	}
	var b strings.Builder
	for range n {
		b.WriteByte("0123456789abcdef"[r.IntN(16)])
	}
	return b.String()
}

var liveRefPieces = []string{"refs/", "heads/", "remotes/origin/", "main", "feat", "/", "//", ".", "..", "@", "@{", "{", "~", "^", ":", "?", "*", "[", "\\",
	" ", "\t", "\u00a0", "\u2028", "é", "😀", "-", "_", "#", "!", "$", "%", "&", "'", "(", ")", "+", ",", ";", "=", "a", "Z", "0", ".lock", "\"", "<", "|", "\xed\xa0\x80"}

func randomRefName(r *rand.Rand) string {
	switch r.IntN(10) {
	case 0, 1, 2, 3:
		return "refs/remotes/origin/main"
	case 4:
		return []string{"refs/heads/main", "refs/remotes/up/feat/x", "refs/remotes/origin/a+b#c", "--unsafe", "origin/main", "refs/", "refs/a.", "refs/a/", "refs/@"}[r.IntN(9)]
	}
	var b strings.Builder
	if r.IntN(6) != 0 {
		b.WriteString("refs/")
	}
	for n := 1 + r.IntN(6); n > 0; n-- {
		b.WriteString(liveRefPieces[r.IntN(len(liveRefPieces))])
	}
	return b.String()
}

// intakeDirs is the filesystem layout the scripted cases point at.
type intakeDirs struct {
	base, unresolved, sub, inner, unrelated, file, link string
}

func makeIntakeDirs(t *testing.T) intakeDirs {
	unresolved := t.TempDir()
	base, err := filepath.EvalSymlinks(unresolved)
	if err != nil {
		t.Fatal(err)
	}
	d := intakeDirs{base: base, unresolved: unresolved, sub: filepath.Join(base, "sub"), inner: filepath.Join(base, "sub", "inner"),
		unrelated: filepath.Join(base, "..", filepath.Base(base)+"-unrelated"), file: filepath.Join(base, "f"), link: filepath.Join(base, "link")}
	d.unrelated = filepath.Clean(d.unrelated)
	for _, dir := range []string{d.inner, d.unrelated} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.RemoveAll(d.unrelated) })
	if err := os.WriteFile(d.file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(d.sub, d.link); err != nil {
		t.Fatal(err)
	}
	return d
}

// flipCase upper-cases the last path component: the same directory on a
// case-insensitive filesystem (macOS), so only the dev/ino walk of
// isInsideDirectory accepts it; a missing one elsewhere.
func flipCase(p string) string {
	return filepath.Join(filepath.Dir(p), strings.ToUpper(filepath.Base(p)))
}

func (d intakeDirs) randomRepository(r *rand.Rand) string {
	switch k := r.IntN(20); {
	case k < 12:
		return d.base
	case k < 14:
		return d.sub
	case k == 14:
		return d.inner
	case k == 15:
		return d.unresolved
	case k == 16:
		return d.link
	case k == 17:
		return d.file
	case k == 18:
		return []string{filepath.Join(d.base, "missing"), d.file + "/x"}[r.IntN(2)]
	}
	return d.link + "/inner"
}

func (d intakeDirs) randomRoot(r *rand.Rand) string {
	switch k := r.IntN(30); {
	case k < 16:
		return d.base
	case k == 16:
		return d.sub
	case k == 17:
		return "/"
	case k == 18:
		return d.unrelated
	case k == 19:
		return "relative/root"
	case k == 20:
		return filepath.Join(d.base, "missing")
	case k == 21:
		return d.file
	case k == 22:
		return d.file + "/x"
	case k == 23:
		return d.link
	case k == 24:
		return flipCase(d.base)
	case k == 25:
		return d.base + "/"
	case k == 26:
		return d.base + "/sub/.."
	case k == 27:
		return d.unresolved
	case k == 28:
		return flipCase(d.sub)
	}
	return d.inner
}

// randomIntakeStep perturbs the response good would be: every exit,
// timeout and cap combination, a runner rejection, and malformed output.
func randomIntakeStep(r *rand.Rand, good string) runtimeStep {
	if r.IntN(12) == 0 {
		return runtimeStep{throw: []string{"spawn git ENOENT", "boom", " "}[r.IntN(3)]}
	}
	res := hostcmd.Result{ExitCode: new(0), Stdout: good}
	switch r.IntN(5) {
	case 0:
		res.ExitCode = nil
	case 1:
		res.ExitCode = new([]int{1, 128, 129, -1, 255}[r.IntN(5)])
	}
	res.TimedOut = r.IntN(5) == 0
	res.OutputLimitExceeded = r.IntN(5) == 0
	trimmed := strings.TrimRight(good, "\n")
	switch r.IntN(16) {
	case 0:
		res.Stdout = ""
	case 1:
		res.Stdout = " \n\t"
	case 2:
		res.Stdout = good + good
	case 3:
		res.Stdout = strings.ToUpper(good)
	case 4:
		res.Stdout = trimmed[:r.IntN(len(trimmed)+1)]
	case 5:
		// Whitespace JS trim removes (and a line terminator inside).
		res.Stdout = []string{"\ufeff\u00a0" + trimmed + "\u2028\u3000", trimmed + "\r\n", "\r" + trimmed, trimmed + "\u2028x", trimmed + "\u0085"}[r.IntN(5)]
	case 6:
		// Past the byte cap on bytes but not on UTF-16 units, or right at it.
		n := []int{32767, 32768, 32769, 21845, 21846, 65536, 65537}[r.IntN(7)]
		piece := []string{"é", "x", "😀", "\xed\xa0\x80"}[r.IntN(4)]
		if r.IntN(2) == 0 {
			res.Stdout = good
			res.Stderr = strings.Repeat(piece, n/len(piece)+r.IntN(2))
		} else {
			res.Stdout = good + strings.Repeat(piece, n/len(piece))
		}
	case 7:
		res.Stdout = good + "\xed\xa0\x80"
	case 8:
		res.Stderr = "fatal: something"
	}
	return runtimeStep{result: res}
}

// scriptedSuggestSteps builds the nine-call script for a run that would
// succeed, then perturbs one call (and occasionally others).
func scriptedSuggestSteps(r *rand.Rand, d intakeDirs, i int) (string, []runtimeStep) {
	repository := d.randomRepository(r)
	root := d.base
	if r.IntN(3) == 0 {
		root = d.randomRoot(r)
	}
	head, upstreamCommit, mergeBase, parent := randomOID(r), randomOID(r), randomOID(r), randomOID(r)
	switch r.IntN(8) {
	case 0:
		mergeBase = head // NO_RANGE: no --is-ancestor call
	case 1:
		parent = mergeBase // DUPLICATE_RANGE
	case 2:
		parent = head
	}
	ref := randomRefName(r)
	insideOut := "true\n"
	if r.IntN(10) == 0 {
		insideOut = []string{"true", " true \n", "TRUE\n", "false\n", "true\ntrue\n"}[r.IntN(5)]
	}
	type call struct {
		index int
		good  string
	}
	calls := []call{{0, insideOut}, {1, root + "\n"}, {2, head + "\n"}, {3, ref + "\n"}}
	if incident.SafeRefName(ref) {
		calls = append(calls, call{4, upstreamCommit + "\n"}, call{5, mergeBase + "\n"})
		if mergeBase != head {
			calls = append(calls, call{6, ""})
		}
	}
	calls = append(calls, call{7, parent + "\n"}, call{8, ""})
	target := i % 9
	steps := []runtimeStep{}
	for _, c := range calls {
		if c.index == target || r.IntN(20) == 0 {
			steps = append(steps, randomIntakeStep(r, c.good))
		} else {
			steps = append(steps, runtimeStep{result: hostcmd.Result{ExitCode: new(0), Stdout: c.good}})
		}
	}
	if r.IntN(25) == 0 {
		steps = steps[:r.IntN(len(steps)+1)] // exhausted: the runner throws
	}
	return repository, steps
}

// intakeGit runs the test's own git with fixed identity and dates and no
// user configuration.
func intakeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine Test", "-c", "user.email=test@faultline.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "-c", "init.defaultBranch=main", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_DATE=2026-07-16T12:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T12:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func intakeCommit(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "state.txt"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	intakeGit(t, dir, "add", "state.txt")
	intakeGit(t, dir, "commit", "-q", "-m", content)
}

// trackUpstream points branch main's upstream at origin/main = rev,
// without a reachable remote.
func trackUpstream(t *testing.T, dir, rev string) {
	t.Helper()
	intakeGit(t, dir, "remote", "add", "origin", "https://127.0.0.1:1/never-contacted.git")
	intakeGit(t, dir, "update-ref", "refs/remotes/origin/main", intakeGit(t, dir, "rev-parse", rev))
	intakeGit(t, dir, "config", "branch.main.remote", "origin")
	intakeGit(t, dir, "config", "branch.main.merge", "refs/heads/main")
}

// intakeRepoShapes build a work directory and return the --repo path.
var intakeRepoShapes = map[string]func(t *testing.T, work, format string) string{
	"no-upstream": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		return repo
	},
	"upstream-behind": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		trackUpstream(t, repo, "HEAD~2")
		return repo
	},
	"upstream-behind-one": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		trackUpstream(t, repo, "HEAD~1") // the parent: DUPLICATE_RANGE
		return repo
	},
	"upstream-equal": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		trackUpstream(t, repo, "HEAD")
		return repo
	},
	"upstream-ahead": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		intakeGit(t, repo, "branch", "ahead")
		intakeGit(t, repo, "reset", "-q", "--hard", "HEAD~2")
		intakeGit(t, repo, "remote", "add", "origin", "https://127.0.0.1:1/x.git")
		intakeGit(t, repo, "update-ref", "refs/remotes/origin/main", intakeGit(t, repo, "rev-parse", "ahead"))
		intakeGit(t, repo, "config", "branch.main.remote", "origin")
		intakeGit(t, repo, "config", "branch.main.merge", "refs/heads/main")
		return repo
	},
	"upstream-diverged": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		intakeGit(t, repo, "checkout", "-q", "-b", "other", "HEAD~1")
		intakeCommit(t, repo, "other")
		intakeGit(t, repo, "checkout", "-q", "main")
		intakeCommit(t, repo, "main-2")
		trackUpstream(t, repo, "other")
		return repo
	},
	"upstream-local-branch": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		intakeGit(t, repo, "branch", "base", "HEAD~2")
		intakeGit(t, repo, "branch", "--set-upstream-to=base")
		return repo
	},
	"upstream-missing-ref": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		intakeGit(t, repo, "config", "branch.main.remote", "origin")
		intakeGit(t, repo, "config", "branch.main.merge", "refs/heads/main")
		return repo
	},
	"upstream-unrelated": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		intakeGit(t, repo, "checkout", "-q", "--orphan", "orphan")
		intakeCommit(t, repo, "orphan")
		intakeGit(t, repo, "checkout", "-q", "main")
		trackUpstream(t, repo, "orphan")
		return repo
	},
	"upstream-non-ascii": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		intakeGit(t, repo, "branch", "bäse", "HEAD~1")
		intakeGit(t, repo, "branch", "--set-upstream-to=bäse")
		return repo
	},
	"upstream-punctuation": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		intakeGit(t, repo, "branch", "feat/a+b#c=d", "HEAD~2")
		intakeGit(t, repo, "branch", "--set-upstream-to=feat/a+b#c=d")
		return repo
	},
	"upstream-malformed-merge": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		intakeGit(t, repo, "config", "branch.main.remote", ".")
		intakeGit(t, repo, "config", "branch.main.merge", "refs/heads/a..b")
		return repo
	},
	"detached": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		trackUpstream(t, repo, "HEAD~2")
		intakeGit(t, repo, "checkout", "-q", "--detach", "HEAD~1")
		return repo
	},
	"root": func(t *testing.T, work, format string) string {
		return initIntakeRepo(t, work, format, 1)
	},
	"root-upstream": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 1)
		trackUpstream(t, repo, "HEAD")
		return repo
	},
	"merge": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		intakeGit(t, repo, "checkout", "-q", "-b", "topic", "HEAD~1")
		if err := os.WriteFile(filepath.Join(repo, "topic.txt"), []byte("topic\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		intakeGit(t, repo, "add", "topic.txt")
		intakeGit(t, repo, "commit", "-q", "-m", "topic")
		intakeGit(t, repo, "checkout", "-q", "main")
		intakeGit(t, repo, "merge", "-q", "--no-ff", "-m", "merge", "topic")
		trackUpstream(t, repo, "HEAD~1")
		return repo
	},
	"empty": func(t *testing.T, work, format string) string {
		return initIntakeRepo(t, work, format, 0)
	},
	"subdirectory": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		trackUpstream(t, repo, "HEAD~2")
		sub := filepath.Join(repo, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		return sub
	},
	"symlinked-repo": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		link := filepath.Join(work, "link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		return link
	},
	"through-symlinked-parent": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 2)
		if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(work, "link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(link, "sub")
	},
	"inside-git-dir": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 1)
		return filepath.Join(repo, ".git")
	},
	"bare": func(t *testing.T, work, format string) string {
		bare := filepath.Join(work, "bare.git")
		intakeGit(t, work, "init", "-q", "--bare", "--object-format="+format, bare)
		return bare
	},
	"linked-worktree": func(t *testing.T, work, format string) string {
		repo := initIntakeRepo(t, work, format, 3)
		linked := filepath.Join(work, "linked")
		intakeGit(t, repo, "worktree", "add", "-q", "-b", "wt", linked, "HEAD~1")
		intakeGit(t, linked, "branch", "--set-upstream-to=main")
		return linked
	},
	"not-a-repo": func(t *testing.T, work, format string) string {
		dir := filepath.Join(work, "plain")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	},
	"missing": func(t *testing.T, work, format string) string {
		return filepath.Join(work, "missing")
	},
	"file": func(t *testing.T, work, format string) string {
		file := filepath.Join(work, "file")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return file
	},
}

// initIntakeRepo makes work/repo with n commits on main.
func initIntakeRepo(t *testing.T, work, format string, n int) string {
	t.Helper()
	repo := filepath.Join(work, "repo")
	intakeGit(t, work, "init", "-q", "--object-format="+format, repo)
	for k := range n {
		intakeCommit(t, repo, "state-"+string(rune('a'+k)))
	}
	return repo
}

// TestLiveIncidentSuggest compares suggestIncidentRanges with
// incident.SuggestRanges: scripted runner results at each of the nine Git
// calls, then real repositories through both default runners. Each case
// compares the canonical intake (or THREW:<code>:<message>) and every
// call's argv, timeout and output cap. SAFE_REF_NAME is compared on
// generated refs.
func TestLiveIncidentSuggest(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(33, 3))
	reached := map[string]int{}

	t.Run("safe-ref-name", func(t *testing.T) {
		refs := []jsjson.Value{}
		var want strings.Builder
		for range 20000 {
			ref := randomRefName(r)
			refs = append(refs, jsjson.MakeString(ref))
			if incident.SafeRefName(ref) {
				want.WriteByte('1')
			} else {
				want.WriteByte('0')
			}
		}
		liveCheck(t, c, "safeRefNames", `{"refs":`+jsjson.Stringify(jsjson.MakeArray(refs))+"}", want.String())
	})

	t.Run("scripted", func(t *testing.T) {
		d := makeIntakeDirs(t)
		for i := range 1000 {
			repository, steps := scriptedSuggestSteps(r, d, i)
			got := goSuggest(repository, steps)
			liveCheck(t, c, "suggestIncidentRanges", suggestArgs(repository, steps), got)
			tallySuggest(reached, got)
		}
	})

	t.Run("real", func(t *testing.T) {
		n := 0
		names := []string{}
		for name := range intakeRepoShapes {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, format := range []string{"sha1", "sha256", "sha1-unresolved"} {
			for _, name := range names {
				work := t.TempDir()
				if format != "sha1-unresolved" {
					resolved, err := filepath.EvalSymlinks(work)
					if err != nil {
						t.Fatal(err)
					}
					work = resolved
				}
				objectFormat := strings.TrimSuffix(format, "-unresolved")
				repository := intakeRepoShapes[name](t, work, objectFormat)
				got := goSuggest(repository, nil)
				liveCheck(t, c, "suggestIncidentRanges", suggestArgs(repository, nil), got)
				tallySuggest(reached, got)
				n++
			}
		}
		t.Logf("%d real repositories", n)
	})

	t.Logf("outcomes: %v", reached)
	for _, want := range []string{"UNSAFE_REPOSITORY_PATH", "NOT_A_GIT_WORKTREE", "GIT_UNAVAILABLE", "INVALID_GIT_RESPONSE", "HEAD_UNAVAILABLE",
		"LOCAL_UPSTREAM_MERGE_BASE:CANDIDATE_AVAILABLE", "LOCAL_UPSTREAM_MERGE_BASE:NOT_AVAILABLE", "LOCAL_UPSTREAM_MERGE_BASE:NO_RANGE",
		"HEAD_PARENT:CANDIDATE_AVAILABLE", "HEAD_PARENT:NOT_AVAILABLE", "HEAD_PARENT:NO_RANGE", "HEAD_PARENT:DUPLICATE_RANGE"} {
		if reached[want] == 0 {
			t.Errorf("no case reached %s", want)
		}
	}
	// Every message literal of intake.go was produced by some case.
	outputs := recordedOutputs(t)
	for lit, pos := range errorLiterals(t, filepath.Join(oracle.RepoRoot(t), "internal", "incident", "intake.go")) {
		for _, frag := range messageFragments(lit) {
			if q := jsjson.Quote(frag); !strings.Contains(outputs, q[1:len(q)-1]) {
				t.Errorf("%s: no case reached %q", pos, lit)
			}
		}
	}
	for _, msg := range []string{"Requested repository directory does not exist.", "Requested repository directory is unavailable.",
		"Requested repository directory must be a real directory, not a symlink or file.", "Git worktree root does not exist.",
		"Git worktree root is unavailable.", "Git worktree root must be a real directory, not a symlink or file."} {
		if !strings.Contains(outputs, msg) {
			t.Errorf("no case reached %q", msg)
		}
	}
}
