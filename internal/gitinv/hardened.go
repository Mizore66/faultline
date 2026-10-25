package gitinv

import (
	"errors"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// Port of src/git-materialization.ts:6-8.
const (
	GitMaterializationMaxOutputBytes = 64 * 1024 * 1024
	gitSymlinkMode                   = "120000"
)

// gitMaterializationTimeoutMs is GIT_MATERIALIZATION_TIMEOUT_MS, apart for
// SetGitTimeoutForTest.
var gitMaterializationTimeoutMs = 30_000

// toGitPath ports src/git-materialization.ts:15 toGitPath.
func toGitPath(path string) string { return strings.ReplaceAll(nodefs.Resolve(path), `\`, "/") }

// materializationSafeGitConfig ports src/git-materialization.ts:19
// MATERIALIZATION_SAFE_GIT_CONFIG, in source order. TS freezes it; it stays
// unexported here, and callers copy it before appending.
var materializationSafeGitConfig = []string{
	"-c", "core.hooksPath=/nonexistent/faultline-hooks",
	"-c", "core.fsmonitor=false",
	"-c", "core.useBuiltinFSMonitor=false",
	"-c", "core.untrackedCache=false",
	"-c", "core.preloadIndex=false",
	"-c", "core.autocrlf=false",
	"-c", "core.symlinks=false",
	"-c", "core.sparseCheckout=false",
	"-c", "core.sparseCheckoutCone=false",
	"-c", "filter.lfs.process=",
	"-c", "filter.lfs.smudge=",
	"-c", "filter.lfs.clean=",
	"-c", "filter.lfs.required=false",
	"-c", "diff.external=",
	"-c", "submodule.recurse=false",
	"-c", "fetch.recurseSubmodules=false",
	"-c", "protocol.allow=never",
	"-c", "protocol.file.allow=never",
	"-c", "protocol.ext.allow=never",
	"-c", "protocol.git.allow=never",
	"-c", "protocol.ssh.allow=never",
	"-c", "protocol.http.allow=never",
	"-c", "protocol.https.allow=never",
}

// GitRun is HardenedGitResult (src/git-materialization.ts:47). ExitCode is
// nil for JS null (killed by a signal, or never started); Error is "" when
// the TS result has no error property.
type GitRun struct {
	ExitCode       *int
	Stdout, Stderr []byte
	Error          string
}

// HardenedOptions are runHardenedGit's repository argument and
// HardenedGitOptions (src/git-materialization.ts:54); "" is undefined.
// HasStdin distinguishes an empty stdin Buffer from no stdin.
type HardenedOptions struct {
	Repository, GitDir, WorkTree, IndexFile string
	Stdin                                   []byte
	HasStdin                                bool
}

// failed is `exitCode !== 0 || error !== undefined`, as at
// src/git-materialization.ts:199, 222, 264 and 305-319.
func (r GitRun) failed() bool { return r.ExitCode == nil || *r.ExitCode != 0 || r.Error != "" }

// detail is `stderr.toString("utf8").trim() || error || fallback`, as at
// src/git-materialization.ts:224, 246, 265 and 306-320.
func (r GitRun) detail(fallback string) string {
	if s := jsstr.Trim(jsstr.DecodeUTF8(r.Stderr)); s != "" {
		return s
	}
	if r.Error != "" {
		return r.Error
	}
	return fallback
}

// HardenedGitEnvironment ports src/git-materialization.ts:72
// hardenedGitEnvironment: "KEY=value" pairs in object order, values as JS
// strings.
func HardenedGitEnvironment(o HardenedOptions) []string {
	path, _ := nodeproc.Getenv("PATH")
	global := "/dev/null"
	if runtime.GOOS == "windows" {
		global = "NUL"
	}
	env := []string{
		"PATH=" + path,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + global,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_ALLOW_PROTOCOL=none",
	}
	if o.GitDir != "" {
		env = append(env, "GIT_DIR="+toGitPath(o.GitDir))
	}
	if o.WorkTree != "" {
		env = append(env, "GIT_WORK_TREE="+toGitPath(o.WorkTree))
	}
	if o.IndexFile != "" {
		env = append(env, "GIT_INDEX_FILE="+toGitPath(o.IndexFile))
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT"} {
			if v, _ := nodeproc.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	}
	return env
}

// boundedAppend ports src/git-materialization.ts:97 boundedAppend.
func boundedAppend(current, next []byte) []byte {
	if len(current) >= GitMaterializationMaxOutputBytes {
		return current
	}
	room := GitMaterializationMaxOutputBytes - len(current)
	return append(current, next[:min(room, len(next))]...)
}

// RunHardenedGit ports src/git-materialization.ts:107 runHardenedGit. The
// timer and the output cap stop Git with child.kill() (SIGTERM). Node
// unref()s the timer, which changes nothing here: the child's handle keeps
// the event loop alive until 'close', and settling clears the timer.
func RunHardenedGit(args []string, o HardenedOptions) GitRun {
	argv := append([]string{}, materializationSafeGitConfig...)
	if o.GitDir != "" {
		argv = append(argv, "--git-dir="+toGitPath(o.GitDir))
	}
	if o.WorkTree != "" {
		argv = append(argv, "--work-tree="+toGitPath(o.WorkTree))
	}
	argv = append(argv, "-C", toGitPath(o.Repository))
	argv = append(argv, args...)

	env := HardenedGitEnvironment(o)
	if runtime.GOOS != "windows" {
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}

	var (
		mu                       sync.Mutex
		stdout, stderr           []byte
		outputExceeded, timedOut bool
		settled                  bool
		child                    *nodeproc.Child
	)
	capture := func(chunk []byte, isStdout bool) {
		mu.Lock()
		defer mu.Unlock()
		if isStdout {
			stdout = boundedAppend(stdout, chunk)
		} else {
			stderr = boundedAppend(stderr, chunk)
		}
		if len(stdout)+len(stderr) >= GitMaterializationMaxOutputBytes && !outputExceeded {
			outputExceeded = true
			child.Kill("")
		}
	}
	// The listeners wait on mu until child is set, as Node's run after spawn returns.
	mu.Lock()
	thrown := jsexc.Try(func() {
		child = nodeproc.Spawn("git", argv, nodeproc.SpawnOptions{
			Env:       env,
			Stdin:     o.Stdin,
			StdinPipe: o.HasStdin,
			Stdout:    nodeproc.StdioPipe,
			Stderr:    nodeproc.StdioPipe,
			OnStdout:  func(b []byte) { capture(b, true) },
			OnStderr:  func(b []byte) { capture(b, false) },
		})
	})
	mu.Unlock()
	if thrown != nil {
		return GitRun{Error: jsexc.Message(thrown)}
	}
	timer := time.AfterFunc(time.Duration(gitMaterializationTimeoutMs)*time.Millisecond, func() {
		mu.Lock()
		defer mu.Unlock()
		if settled {
			return
		}
		timedOut = true
		child.Kill("")
	})
	exit := child.Wait()
	mu.Lock()
	defer mu.Unlock()
	settled = true
	timer.Stop()
	if exit.Err != nil {
		return GitRun{Stdout: stdout, Stderr: stderr, Error: exit.Err.Error()}
	}
	reason := ""
	if timedOut {
		reason = "Git command exceeded bounded execution time"
	} else if outputExceeded {
		reason = "Git command exceeded bounded output limit"
	}
	if reason != "" {
		stderr = boundedAppend(stderr, []byte("\nFaultLine stopped Git: "+reason+"."))
	}
	return GitRun{ExitCode: exit.Code, Stdout: stdout, Stderr: stderr, Error: reason}
}

// HardenedGitText ports src/git-materialization.ts:197 hardenedGitText.
func HardenedGitText(repository string, args []string) (string, error) {
	result := RunHardenedGit(args, HardenedOptions{Repository: repository})
	if result.failed() {
		var parts []string
		if s := jsstr.Trim(jsstr.DecodeUTF8(result.Stderr)); s != "" {
			parts = append(parts, s)
		}
		if result.Error != "" {
			parts = append(parts, result.Error)
		}
		detail := strings.Join(parts, "; ")
		if detail == "" {
			code := "unknown"
			if result.ExitCode != nil {
				code = strconv.Itoa(*result.ExitCode)
			}
			detail = "exit " + code
		}
		return "", errors.New(jsexc.Concat("Git ", jsexc.Join(args, " "), " failed: ", detail))
	}
	return jsstr.Trim(jsstr.DecodeUTF8(result.Stdout)), nil
}

// OverlayIndexInspector is OverlayIndexInspector (src/safe-overlay.ts:82):
// Git bound to a materialized tree's external index.
type OverlayIndexInspector interface {
	RunGit(args []string) GitRun
}

var lineBreak = regexp.MustCompile(`\r?\n`)

// AssertOverlayTargetsNotGitSymlinks ports src/git-materialization.ts:213
// assertOverlayTargetsNotGitSymlinks.
func AssertOverlayTargetsNotGitSymlinks(tree OverlayIndexInspector, overlayPaths []string) error {
	for _, overlayPath := range overlayPaths {
		if overlayPath == "" || strings.Contains(overlayPath, `\`) || strings.Contains(overlayPath, "\x00") || strings.HasPrefix(overlayPath, "/") {
			return errors.New(jsexc.Concat("Frozen overlay path is unsafe: ", overlayPath))
		}
		listed := tree.RunGit([]string{"ls-files", "--stage", "--", overlayPath})
		if listed.failed() {
			return errors.New(jsexc.Concat("Could not inspect overlay target mode for ", overlayPath, ": ", listed.detail("Git failed.")))
		}
		line := ""
		for _, entry := range lineBreak.Split(jsstr.DecodeUTF8(listed.Stdout), -1) {
			if jsstr.Trim(entry) != "" {
				line = entry
				break
			}
		}
		if line == "" {
			continue
		}
		if firstWhitespaceField(line) == gitSymlinkMode {
			return errors.New(jsexc.Concat("Frozen overlay target is a symbolic link: ", overlayPath))
		}
	}
	return nil
}

// firstWhitespaceField ports src/git-materialization.ts:229
// line.split(/\s+/, 1)[0]: the text before the first JS whitespace character.
func firstWhitespaceField(line string) string {
	units := jsstr.ToUTF16(line)
	for i, u := range units {
		if jsstr.IsWhitespace(u) {
			return jsstr.FromUTF16(units[:i])
		}
	}
	return line
}

// AssertSafeGitMaterialization ports src/git-materialization.ts:240
// assertSafeGitMaterialization.
func AssertSafeGitMaterialization(repository string, commits []string) error {
	o := HardenedOptions{Repository: repository}
	localFilters := RunHardenedGit([]string{"config", "--local", "--get-regexp", `^filter\.`}, o)
	code := -1
	if localFilters.ExitCode != nil {
		code = *localFilters.ExitCode
	}
	if code == 0 && jsstr.Trim(jsstr.DecodeUTF8(localFilters.Stdout)) != "" {
		return errors.New("Refusing host tree materialization: repository local Git filter configuration is present.")
	}
	if code != 0 && code != 1 {
		return errors.New(jsexc.Concat("Could not inspect repository Git filters: ", localFilters.detail("Git failed.")))
	}
	seen := map[string]bool{}
	for _, commit := range commits {
		if seen[commit] {
			continue
		}
		seen[commit] = true
		attributes := RunHardenedGit([]string{
			"grep", "-I", "-n", "-E", "(^|[[:space:]])filter(=|[[:space:]]|$)", commit, "--",
			":(literal).gitattributes", ":(glob)**/.gitattributes",
		}, o)
		if attributes.ExitCode != nil && *attributes.ExitCode == 0 {
			return errors.New(jsexc.Concat("Refusing host tree materialization: ", commit, " declares a Git filter attribute."))
		}
		if attributes.ExitCode == nil || *attributes.ExitCode != 1 || attributes.Error != "" {
			return errors.New(jsexc.Concat("Could not inspect Git attributes for ", commit, ": ", attributes.detail("Git failed.")))
		}
	}
	return nil
}

// MaterializedTree is MaterializedGitTree (src/git-materialization.ts:61).
// Cleanup never fails the caller's flow: its error is the message TS
// cleanup() returns (nil for null).
type MaterializedTree struct {
	Worktree, IndexFile string
	Cleanup             func() error
	session             HardenedOptions
}

// RunGit ports src/git-materialization.ts:325 runGit, without stdin.
func (t MaterializedTree) RunGit(args []string) GitRun { return RunHardenedGit(args, t.session) }

// RunGitStdin ports src/git-materialization.ts:325 runGit, with stdin.
func (t MaterializedTree) RunGitStdin(args []string, stdin []byte) GitRun {
	o := t.session
	o.Stdin, o.HasStdin = stdin, true
	return RunHardenedGit(args, o)
}

// MaterializeGitTree ports src/git-materialization.ts:274 materializeGitTree.
func MaterializeGitTree(repository, commit, tempRoot, name string) (MaterializedTree, error) {
	if err := AssertSafeGitMaterialization(repository, []string{commit}); err != nil {
		return MaterializedTree{}, err
	}
	worktrees := nodefs.Join(tempRoot, "worktrees")
	indexes := nodefs.Join(tempRoot, "indexes")
	gitDir, err := HardenedGitText(repository, []string{"rev-parse", "--absolute-git-dir"})
	if err != nil {
		return MaterializedTree{}, err
	}
	if !nodefs.IsAbsolute(gitDir) {
		return MaterializedTree{}, errors.New("Git did not return an absolute repository metadata directory.")
	}
	if err := nodefs.MkdirAll(worktrees, 0o700); err != nil {
		return MaterializedTree{}, err
	}
	if err := nodefs.MkdirAll(indexes, 0o700); err != nil {
		return MaterializedTree{}, err
	}
	worktree := nodefs.Join(worktrees, name)
	indexFile := nodefs.Join(indexes, clock.NewUUID()+".index")
	// The sandbox runs as an unprivileged uid against a read-only bind mount.
	if err := nodefs.Mkdir(worktree, 0o755); err != nil {
		return MaterializedTree{}, err
	}
	session := HardenedOptions{Repository: worktree, GitDir: gitDir, WorkTree: worktree, IndexFile: indexFile}
	cleanup := func() error {
		err := nodefs.RemoveAllAsync(worktree)
		if err == nil {
			err = RemoveForce(indexFile) // rm(indexFile, { force: true }) is not recursive
		}
		if err != nil {
			return errors.New(jsexc.Concat("Could not remove temporary materialized tree: ", jsexc.Message(err)))
		}
		return nil
	}
	fail := func(err error) (MaterializedTree, error) {
		cleanup()
		return MaterializedTree{}, err
	}
	readTree := RunHardenedGit([]string{"read-tree", "--reset", commit}, session)
	if readTree.failed() {
		return fail(errors.New(jsexc.Concat("Could not prepare external Git index for ", commit, ": ", readTree.detail("Git failed."))))
	}
	checkout := RunHardenedGit([]string{"checkout-index", "--all", "--force"}, session)
	if checkout.failed() {
		return fail(errors.New(jsexc.Concat("Could not materialize Git tree ", commit, ": ", checkout.detail("Git failed."))))
	}
	// read-tree creates entries without worktree stat data. Refresh the
	// external index so later `git apply --index` can require an exact match.
	refresh := RunHardenedGit([]string{"update-index", "--refresh"}, session)
	if refresh.failed() {
		return fail(errors.New(jsexc.Concat("Could not verify materialized Git tree ", commit, ": ", refresh.detail("Git failed."))))
	}
	verification := RunHardenedGit([]string{"diff-files", "--quiet", "--"}, session)
	if verification.failed() {
		return fail(errors.New(jsexc.Concat("Materialized Git tree does not match its external index for ", commit, ": ", verification.detail("content mismatch"))))
	}
	return MaterializedTree{Worktree: worktree, IndexFile: indexFile, Cleanup: cleanup, session: session}, nil
}

// Detail is the `stderr.trim() || error || fallback` detail of a failed
// run, for ported callers outside this package.
func (r GitRun) Detail(fallback string) string { return r.detail(fallback) }
