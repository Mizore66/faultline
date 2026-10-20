//go:build faultline_test

package difftest

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

// A capture case is a template work directory (repo/ plus room for an
// outside session cache) and the steps run on a copy of it: each step
// applies its operations, then captures.
type captureCase struct {
	label string
	setup []captureOp // applied to the template after the first commit
	steps []captureStep
}

type captureStep struct {
	ops          []captureOp
	cache        string // "", "repo" or "outside"
	trackedOnly  bool
	subdirectory string // captured from repo/<subdirectory>
}

// captureOp is one deterministic change to a work directory.
type captureOp struct {
	kind          string // write, remove, chmod, symlink, mkdir, git, rename, cache, alternates
	path, content string
	mode          os.FileMode
	args          []string
}

// captureGit runs the test's own git (not FaultLine's runner) with fixed
// identity, dates and no user configuration, so both copies get the same
// commits.
func captureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := captureGitCmd(dir, args...).CombinedOutput(); err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
}

func captureGitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine Test", "-c", "user.email=test@faultline.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "-c", "init.defaultBranch=main",
		// No detached auto gc or maintenance: it would change .git while a
		// capture or the manifest walk runs.
		"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_DATE=2026-07-16T12:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T12:00:00Z")
	return cmd
}

func applyCaptureOps(t *testing.T, work string, ops []captureOp) {
	t.Helper()
	repo := filepath.Join(work, "repo")
	for _, op := range ops {
		path := filepath.Join(repo, filepath.FromSlash(op.path))
		switch op.kind {
		case "write":
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.RemoveAll(path)
			if err := os.WriteFile(path, []byte(op.content), op.mode); err != nil {
				t.Error(err)
			}
			os.Chmod(path, op.mode)
		case "remove":
			os.RemoveAll(path)
		case "chmod":
			os.Chmod(path, op.mode)
		case "symlink":
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.RemoveAll(path)
			os.Symlink(op.content, path)
		case "mkdir":
			os.RemoveAll(path)
			os.MkdirAll(filepath.Join(path, "inner"), 0o755)
			os.WriteFile(filepath.Join(path, "inner", "f.txt"), []byte("inner\n"), 0o644)
		case "rename":
			os.MkdirAll(filepath.Dir(filepath.Join(repo, filepath.FromSlash(op.content))), 0o755)
			os.Rename(path, filepath.Join(repo, filepath.FromSlash(op.content)))
		case "git": // may fail (no repository, nothing to move): alike on both copies
			captureGitCmd(repo, op.args...).Run()
		case "cache": // op.path names the cache ("repo" or "outside")
			cache := filepath.Join(work, "outside.turn-snapshot-cache.json")
			if op.path == "repo" {
				cache = filepath.Join(repo, ".git", "faultline", "turn-snapshot-cache.json")
			}
			os.MkdirAll(filepath.Dir(cache), 0o755)
			if op.content == "\x00truncate" {
				b, _ := os.ReadFile(cache)
				os.WriteFile(cache, b[:len(b)/2], 0o644)
			} else {
				os.WriteFile(cache, []byte(op.content), 0o644)
			}
		case "alternates":
			alternates := filepath.Join(repo, ".git", "objects", "info", "alternates")
			os.MkdirAll(filepath.Dir(alternates), 0o755)
			extra := filepath.Join(work, "extra-objects")
			os.MkdirAll(filepath.Join(extra, "info"), 0o755)
			os.MkdirAll(filepath.Join(extra, "pack"), 0o755)
			os.WriteFile(alternates, []byte(strings.ReplaceAll(op.content, "<EXTRA>", extra)), 0o644)
		default:
			t.Errorf("unknown op %q", op.kind)
		}
	}
}

// copyWork copies a work directory, keeping modes and symbolic links.
func copyWork(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dest)
		case d.IsDir():
			return os.MkdirAll(dest, info.Mode().Perm()|0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, b, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(dest, info.Mode().Perm())
	})
	if err != nil {
		t.Error(err)
	}
}

// workManifest lists every entry under dir: path, kind, permission bits
// and content digest or link target. The index git keeps stat data in is
// left out (both copies refresh nothing, but its stat fields name each
// copy's inodes).
func workManifest(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			lines = append(lines, "ERR "+err.Error())
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			lines = append(lines, strconv.Quote(rel)+" ERR "+err.Error())
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, strconv.Quote(rel)+" L "+target)
		case d.IsDir():
			lines = append(lines, strconv.Quote(rel)+" D "+info.Mode().Perm().String())
		default:
			b, _ := os.ReadFile(path)
			sum := sha256.Sum256(b)
			if rel == filepath.Join("repo", ".git", "index") {
				sum = [32]byte{}
			}
			lines = append(lines, strconv.Quote(rel)+" F "+info.Mode().Perm().String()+" "+hex.EncodeToString(sum[:]))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// captureArgs are the oracle op's arguments for one step.
func captureArgs(work string, s captureStep, path string) (repository, cachePath, args string) {
	repository = filepath.Join(work, "repo")
	if s.subdirectory != "" {
		repository = filepath.Join(repository, filepath.FromSlash(s.subdirectory))
	}
	switch s.cache {
	case "outside":
		cachePath = filepath.Join(work, "outside.turn-snapshot-cache.json")
	case "repo":
		cachePath = "repo"
	}
	args = canonicalOf(jsjson.MustObject("repository", jsjson.MakeString(repository), "sessionCachePath", jsjson.MakeString(cachePath),
		"trackedFilesOnly", jsjson.MakeBool(s.trackedOnly), "path", jsjson.MakeString(path)))
	return repository, cachePath, args
}

// goCapture is the oracle op on the Go side, in the same shape.
func goCapture(repository, cachePath string, s captureStep) string {
	var onWarning, calls []jsjson.Value
	record := func(root string, args, env []string, input []byte, hasInput bool) {
		digest := jsjson.MakeNull()
		if hasInput {
			sum := sha256.Sum256(input)
			digest = jsjson.MakeString(hex.EncodeToString(sum[:]))
		}
		calls = append(calls, jsjson.MakeArray([]jsjson.Value{jsjson.MakeString(root), stringValues(args), stringValues(env), digest}))
	}
	cacheValue := jsjson.MakeNull()
	var outcome jsjson.Value
	err := jsexc.Try(func() {
		if cachePath == "repo" {
			resolved, err := turnsnap.RepoCachePath(repository)
			if err != nil {
				jsexc.Throw(err)
			}
			cachePath = resolved
		}
		cacheValue = jsjson.MakeString(cachePath)
		snapshot, warnings, err := turnsnap.CaptureForTest(repository, turnsnap.CaptureOptions{
			SessionCachePath: cachePath,
			TrackedFilesOnly: s.trackedOnly,
			Sleep:            func(time.Duration) {},
			OnWarning:        func(w string) { onWarning = append(onWarning, jsjson.MakeString(w)) },
		}, record)
		if err != nil {
			jsexc.Throw(err)
		}
		outcome = jsjson.MustObject("snapshot", snapshot, "warnings", stringValues(warnings))
	})
	if err != nil {
		outcome = jsjson.MustObject("threw", jsjson.MakeString(jsexc.Message(err)))
	}
	cache := jsjson.MakeNull()
	if cacheValue.Kind() == jsjson.String && cacheValue.Str() != "" {
		if info, err := os.Stat(cacheValue.Str()); err == nil && info.Mode().IsRegular() {
			b, _ := os.ReadFile(cacheValue.Str())
			cache = jsjson.MakeString(string(b))
		}
	}
	if onWarning == nil {
		onWarning = []jsjson.Value{}
	}
	if calls == nil {
		calls = []jsjson.Value{}
	}
	return jsjson.Stringify(jsjson.MustObject("cachePath", cacheValue, "outcome", outcome, "onWarning", jsjson.MakeArray(onWarning),
		"cache", cache, "calls", jsjson.MakeArray(calls)))
}

// Paths for generated repositories: separators, control characters,
// non-ASCII, integer-like names (which JS objects order first), protected
// descriptors and default-ignored trees.
var capturePaths = []string{
	"a.txt", "b.txt", "src/main.go", "src/lib/util.js", "dir with space/file name.txt", "tab\there.txt", "new\nline.txt",
	"cr\rname.txt", "é/ü.txt", "中文.md", "\U0001F600.txt", "0", "12", "007", "4294967294", "4294967295", "docs/10", "1.txt",
	"package.json", "pnpm-lock.yaml", "Dockerfile", "go.sum", "build/out.js", "node_modules/x/index.js", ".env", "notes.log",
	"-dash", "#hash", "back\\slash.txt", "scratch/tmp.txt", "keep.log", "Zeta.txt", "alpha.txt", "Ä.txt", "z.tmp",
}

var captureIgnoreLines = []string{
	"*.log", "!keep.log", "scratch/", "package.json", "pnpm-lock.yaml", "Dockerfile", "/0", "12", "docs/", "*.tmp",
	".faultline-secret-allowlist.json", "# comment", "src/lib/", "**/x/", "中文.md", "", "tab*",
}

func captureContent(r *rand.Rand) string {
	switch r.IntN(14) {
	case 0:
		return ""
	case 1:
		return secretText(r) + "\n"
	case 2:
		return "value: " + nearEntropyToken(r) + "\n"
	case 3:
		return "bin\x00ary " + randomRun(r, alnum, 20)
	case 4:
		return "line one\r\nline two\r\n"
	}
	return pick(r, "hello", "world", "const x = 1;", "é ü 中文", "lockfileVersion: '9.0'") + " " + randomRun(r, alnum, r.IntN(30)) + "\n"
}

func randomCaptureCase(t *testing.T, r *rand.Rand, i int) (captureCase, string) {
	tmpl := filepath.Join(t.TempDir(), "w")
	repo := filepath.Join(tmpl, "repo")
	os.MkdirAll(repo, 0o755)
	format := "sha1"
	if r.IntN(10) == 0 {
		format = "sha256"
	}
	captureGit(t, repo, "init", "-q", "--object-format="+format)
	c := captureCase{label: "case " + strconv.Itoa(i) + " " + format}

	// The first commit: some tracked files.
	tracked := map[string]bool{}
	for n := 1 + r.IntN(6); n > 0; n-- {
		p := capturePaths[r.IntN(len(capturePaths))]
		if strings.Contains(p, "\\") {
			continue // a backslash path is checked out as itself but normalized to a/b
		}
		mode := os.FileMode(0o644)
		if r.IntN(6) == 0 {
			mode = 0o755
		}
		applyCaptureOps(t, tmpl, []captureOp{{kind: "write", path: p, content: captureContent(r), mode: mode}})
		tracked[p] = true
	}
	if r.IntN(5) == 0 {
		applyCaptureOps(t, tmpl, []captureOp{{kind: "write", path: ".faultlineignore", content: randomIgnore(r), mode: 0o644}})
	}
	if r.IntN(15) == 0 {
		applyCaptureOps(t, tmpl, []captureOp{{kind: "symlink", path: "link", content: "a.txt"}})
		c.label += " tracked-symlink"
	}
	captureGit(t, repo, "add", "-A", "-f")
	captureGit(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	var trackedList []string
	for p := range tracked {
		trackedList = append(trackedList, p)
	}
	sort.Strings(trackedList)

	// Setup on the template: pre-existing caches and alternates.
	switch r.IntN(12) {
	case 0:
		c.setup = append(c.setup, captureOp{kind: "alternates", content: pick(r, "  ../faultline/objects\r\n", "<EXTRA>\n", "<EXTRA>", "\n\n<EXTRA>\r\n  \n")})
		c.label += " alternates"
	case 1:
		c.setup = append(c.setup, captureOp{kind: "cache", path: pick(r, "repo", "outside"),
			content: pick(r, "{", `{"schemaVersion":"faultline.turn-snapshot-cache.v1"}`, "[]", "null", `{"schemaVersion":"faultline.turn-snapshot-cache.v2"}`)})
		c.label += " corrupt-cache"
	}
	applyCaptureOps(t, tmpl, c.setup)

	cache := pick(r, "", "repo", "outside", "outside")
	trackedOnly := r.IntN(5) == 0
	for n := 1 + r.IntN(3); n > 0; n-- {
		s := captureStep{cache: cache, trackedOnly: trackedOnly}
		if r.IntN(10) == 0 {
			s.trackedOnly = !s.trackedOnly
		}
		if r.IntN(15) == 0 && tracked["src/main.go"] {
			s.subdirectory = "src"
		}
		s.ops = randomCaptureOps(r, trackedList, len(c.steps))
		if len(c.steps) > 0 && r.IntN(6) == 0 {
			s.ops = append(s.ops, captureOp{kind: "cache", path: cache, content: "\x00truncate"})
		}
		c.steps = append(c.steps, s)
	}
	if r.IntN(40) == 0 {
		os.RemoveAll(filepath.Join(repo, ".git"))
		c.label += " not-a-repository"
	}
	for _, s := range c.steps {
		c.label += " [" + s.cache + " tracked-only=" + strconv.FormatBool(s.trackedOnly) + " " + strconv.Itoa(len(s.ops)) + " ops]"
	}
	return c, tmpl
}

func randomIgnore(r *rand.Rand) string {
	var lines []string
	for n := 1 + r.IntN(5); n > 0; n-- {
		lines = append(lines, captureIgnoreLines[r.IntN(len(captureIgnoreLines))])
	}
	return strings.Join(lines, pick(r, "\n", "\r\n")) + "\n"
}

// randomCaptureOps changes the worktree before a step: edits, deletions,
// renames (staged or not), mode changes, untracked files, ignore rules,
// allowlists, symbolic links, directories, commits. Some steps change
// nothing, so a session cache can match.
func randomCaptureOps(r *rand.Rand, tracked []string, step int) []captureOp {
	var ops []captureOp
	if step > 0 && r.IntN(3) == 0 {
		return nil
	}
	for n := r.IntN(5); n > 0; n-- {
		p := capturePaths[r.IntN(len(capturePaths))]
		if len(tracked) > 0 && r.IntN(2) == 0 {
			p = tracked[r.IntN(len(tracked))]
		}
		switch r.IntN(14) {
		case 0, 1, 2, 3:
			ops = append(ops, captureOp{kind: "write", path: p, content: captureContent(r), mode: pickMode(r)})
		case 4:
			ops = append(ops, captureOp{kind: "remove", path: p})
		case 5:
			ops = append(ops, captureOp{kind: "chmod", path: p, mode: pickMode(r)})
		case 6:
			ops = append(ops, captureOp{kind: "rename", path: p, content: "renamed/" + strconv.Itoa(r.IntN(3)) + ".txt"})
		case 7:
			if len(tracked) > 0 {
				ops = append(ops, captureOp{kind: "git", args: []string{"mv", "-k", tracked[r.IntN(len(tracked))], "moved-" + strconv.Itoa(r.IntN(3)) + ".txt"}})
			}
		case 8:
			ops = append(ops, captureOp{kind: "write", path: ".faultlineignore", content: randomIgnore(r), mode: 0o644})
		case 9:
			ops = append(ops, captureOp{kind: "git", args: []string{"add", "-A"}})
		case 10:
			ops = append(ops, captureOp{kind: "git", args: []string{"commit", "-q", "-a", "--allow-empty", "-m", "step"}})
		case 11:
			if r.IntN(4) == 0 {
				ops = append(ops, captureOp{kind: "symlink", path: "link-" + strconv.Itoa(r.IntN(2)), content: p})
			} else {
				ops = append(ops, captureOp{kind: "mkdir", path: p})
			}
		case 12:
			ops = append(ops, captureOp{kind: "allowlist"})
		case 13:
			if r.IntN(8) == 0 {
				ops = append(ops, captureOp{kind: "write", path: "big.bin", content: strings.Repeat("x", turnsnap.MaxFileBytes+r.IntN(2)), mode: 0o644})
			} else {
				ops = append(ops, captureOp{kind: "write", path: ".faultline/recording.json", content: "{}", mode: 0o644})
			}
		}
	}
	return ops
}

func pickMode(r *rand.Rand) os.FileMode {
	if r.IntN(4) == 0 {
		return 0o755
	}
	return 0o644
}

// TestLiveTurnSnapshotCapture runs captureTurnTreeSnapshot and
// turnsnap.Capture under the same seed on two copies of each of 150
// generated repositories, one after the other at the same path, and
// compares after every step the result (snapshot, warnings, onWarning
// calls, the session cache's bytes), every Git call (arguments, env
// additions, stdin digest) and the whole work directory: the quarantine
// object store, alternates and caches.
func TestLiveTurnSnapshotCapture(t *testing.T) {
	oracle.Skip(t)
	gitDir, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	// The git binary itself first: /usr/bin/git on macOS is an xcrun shim.
	path := strings.TrimSpace(string(gitDir)) + ":/usr/bin:/bin"
	r := rand.New(rand.NewPCG(73, 10))
	// FAULTLINE_TURNSNAP_CASES=first:end compares only cases [first, end)
	// (all are still generated up to end, so each keeps its repository), for
	// local runs in pieces; CI runs all 150.
	n, first := 150, 0
	if window := os.Getenv("FAULTLINE_TURNSNAP_CASES"); window != "" {
		a, b, _ := strings.Cut(window, ":")
		first, _ = strconv.Atoi(a)
		n, _ = strconv.Atoi(b)
	}
	type prepared struct {
		c    captureCase
		tmpl string
	}
	var cases []prepared
	for i := 0; i < n; i++ {
		c, tmpl := randomCaptureCase(t, r, i)
		// Allowlist operations are resolved now, against the template's
		// files, so both copies get the same document.
		for si := range c.steps {
			for oi, op := range c.steps[si].ops {
				if op.kind == "allowlist" {
					c.steps[si].ops[oi] = captureOp{kind: "write", path: ".faultline-secret-allowlist.json", mode: 0o644,
						content: scanAllowlist(r, filepath.Join(tmpl, "repo"), capturePaths)}
				}
			}
		}
		cases = append(cases, prepared{c, tmpl})
	}

	const seed = "live-turnsnap-capture"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	t.Setenv("PATH", path)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Case i runs at root/w<i>: TS first, then (once the TS copy has moved
	// aside) Go. A worker runs the Go sides in order while the oracle runs
	// the next case's TS side; each side's clock and UUID sequence stays
	// sequential, so the seeded values still line up.
	type sideResult struct{ result, manifest string }
	run := func(i int, pc prepared, side string) []sideResult {
		work := filepath.Join(root, "w"+strconv.Itoa(i))
		copyWork(t, pc.tmpl, work)
		var out []sideResult
		for _, s := range pc.c.steps {
			applyCaptureOps(t, work, s.ops)
			repository, cachePath, args := captureArgs(work, s, path)
			var result string
			if side == "ts" {
				raw, err := c.Call("captureTurnTreeSnapshot", args)
				if err != nil {
					t.Errorf("oracle: %v", err)
					return out
				}
				v, _ := jsjson.Parse(raw)
				result = v.Str()
			} else {
				result = goCapture(repository, cachePath, s)
			}
			out = append(out, sideResult{result, workManifest(t, work)})
		}
		if err := os.Rename(work, filepath.Join(root, side+"-"+strconv.Itoa(i))); err != nil {
			t.Error(err)
		}
		return out
	}
	goResults := make([][]sideResult, len(cases))
	jobs := make(chan int, len(cases))
	done := make([]chan struct{}, len(cases))
	for i := range done {
		done[i] = make(chan struct{})
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for i := range jobs {
			goResults[i] = run(i, cases[i], "go")
			close(done[i])
		}
	}()
	// On any exit, let the worker finish before the temporary directories go.
	defer func() { close(jobs); <-workerDone }()
	tsResults := make([][]sideResult, len(cases))
	outcomes := map[string]int{}
	steps := 0
	compare := func(i int) {
		<-done[i]
		pc, ts, got := cases[i], tsResults[i], goResults[i]
		if len(got) != len(ts) {
			t.Fatalf("%s: %d Go steps, %d TS steps", pc.c.label, len(got), len(ts))
		}
		for si := range ts {
			steps++
			if got[si].result != ts[si].result {
				t.Fatalf("%s step %d:\ngot  %s\nwant %s", pc.c.label, si, got[si].result, ts[si].result)
			}
			if got[si].manifest != ts[si].manifest {
				t.Fatalf("%s step %d: work directories differ:\ngo\n%s\nts\n%s", pc.c.label, si, got[si].manifest, ts[si].manifest)
			}
			v, _ := jsjson.Parse(ts[si].result)
			kind := "accepted"
			if threw := v.Get("outcome", "threw"); threw.Kind() == jsjson.String {
				kind = "threw: " + strings.SplitN(threw.Str(), ":", 2)[0]
			} else if v.Get("outcome", "snapshot", "dirty").Bool() {
				kind = "accepted dirty"
			}
			outcomes[kind]++
			recordOutput(t, ts[si].result)
		}
		os.RemoveAll(filepath.Join(root, "ts-"+strconv.Itoa(i)))
		os.RemoveAll(filepath.Join(root, "go-"+strconv.Itoa(i)))
		if t.Failed() {
			t.FailNow()
		}
	}
	for i := first; i < len(cases); i++ {
		tsResults[i] = run(i, cases[i], "ts")
		if t.Failed() {
			t.FailNow()
		}
		jobs <- i
		if i > first {
			compare(i - 1)
		}
	}
	if len(cases) > first {
		compare(len(cases) - 1)
	}
	if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
		t.Fatalf("oracle clock at %s, Go clock at %d", ts, goTick)
	}
	outputs := recordedOutputs(t)
	reached := map[string]int{}
	for _, marker := range []string{
		`"dirty":true`, `"dirty":false`, "Reused HEAD^{tree} for clean", "Including previously untracked file",
		"Protected environment descriptor remains", "symbolic link is not snapshot-eligible", "secret material detected",
		"high-entropy secret-like token", `"read-tree"`, `"cat-file"`, `"hash-object"`, `"--force-remove"`, "GIT_INDEX_FILE=",
		`"cache":"{\n  \"schemaVersion\"`, "not a git repository", "per file",
	} {
		reached[marker] = strings.Count(outputs, marker)
		if reached[marker] == 0 && first == 0 && n == 150 { // checked over the full run only
			t.Errorf("no generated step reached %q", marker)
		}
	}
	liveSummary(t, "%d repositories, %d steps compared; outcomes %v; markers %v", n-first, steps, outcomes, reached)
}
