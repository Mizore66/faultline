package ledger

import (
	"errors"
	"runtime"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
)

// ErrDirtyWorktree is the message isDirtyCheckpointError compares
// (codex-sidecar.ts:811).
const ErrDirtyWorktree = "Git checkpoint capture requires a clean worktree."

// GitRunner ports GitCommandRunner (src/ledger.ts:201): trimmed stdout or
// an error.
type GitRunner func(repository string, args []string) (string, error)

// CaptureOptions ports CaptureGitCheckpointOptions (src/ledger.ts:203).
type CaptureOptions struct {
	Now    func() time.Time // nil: clock.Now
	RunGit GitRunner        // nil: DefaultHardenedGitRunner
}

// checkpointTimeout is defaultHardenedGitRunner's spawnSync timeout
// (src/ledger.ts:279).
const checkpointTimeout = 60 * time.Second

// DefaultHardenedGitRunner ports defaultHardenedGitRunner (src/ledger.ts:274):
// spawnSync git with HOST_COMMAND_SAFE_GIT_CONFIG, -C repository, the
// hardenedCheckpointGitEnvironment (src/ledger.ts:257, the same pairs as
// gitinv.HardenedGitEnvironment) and a 60 s timeout. A spawn error or a
// non-zero status is a plain Error (not a LedgerIntegrityError).
func DefaultHardenedGitRunner(repository string, args []string) (string, error) {
	argv := append(safegit.HostCommand(), "-C", repository)
	argv = append(argv, args...)
	env := gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})
	if runtime.GOOS != "windows" {
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{Env: env, Timeout: checkpointTimeout})
	if r.Err != nil || r.Status == nil || *r.Status != 0 {
		detail := jsstr.DecodeUTF8(r.Stderr)
		if r.Err != nil {
			detail = jsexc.Concat(detail, r.Err.Error())
		}
		detail = jsstr.Trim(detail)
		if detail == "" {
			status := "unknown"
			if r.Status != nil {
				status = jsjson.FormatNumber(float64(*r.Status))
			}
			detail = "exit " + status
		}
		return "", errors.New(jsexc.Concat("Git checkpoint capture failed for ", jsexc.Join(args, " "), ": ", detail))
	}
	return jsstr.Trim(jsstr.DecodeUTF8(r.Stdout)), nil
}

// SignGitCheckpoint ports signGitCheckpoint (src/ledger.ts:238): parse the
// unsigned checkpoint, then parse {...unsigned, digest}. Either parse fails
// with a ZodError.
func SignGitCheckpoint(unsigned jsjson.Value) (signed jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { signed, err = jsjson.Value{}, e })
	parsed := bundle.Must(bundle.ParseValue(unsigned, gitproof.UnsignedGitCheckpointSchema))
	digest := bundle.Must(canonical.DigestJSON(parsed))
	out := jsjson.NewObj()
	for _, k := range parsed.Obj().Keys() {
		out.Set(k, parsed.Obj().Field(k))
	}
	out.Set("digest", jsjson.MakeString(digest))
	return bundle.ParseValue(jsjson.MakeObject(out), gitproof.GitCheckpointSchema)
}

// CaptureGitCleanCheckpoint ports captureGitCleanCheckpoint (src/ledger.ts:293):
// the top level from rev-parse, then diff-index --ignore-cr-at-eol HEAD and
// ls-files --others --exclude-standard (either non-empty is a dirty
// worktree, an *IntegrityError), then HEAD and HEAD^{tree}; the clock is
// read once, after every Git call. The runner's own errors pass through.
func CaptureGitCleanCheckpoint(repository string, o CaptureOptions) (checkpoint jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { checkpoint, err = jsjson.Value{}, e })
	requestedRoot := nodefs.Resolve(repository)
	runGit := o.RunGit
	if runGit == nil {
		runGit = DefaultHardenedGitRunner
	}
	run := func(root string, args ...string) string { return bundle.Must(runGit(root, args)) }
	repositoryRoot := nodefs.Resolve(run(requestedRoot, "rev-parse", "--show-toplevel"))
	// Nulling GIT_CONFIG_GLOBAL drops a Windows user's autocrlf=true, which
	// makes porcelain status report CRLF-only "modifications". diff-index
	// with --ignore-cr-at-eol still catches real content drift; ls-files
	// catches extras.
	trackedDrift := run(repositoryRoot, "diff-index", "--ignore-cr-at-eol", "HEAD")
	untracked := run(repositoryRoot, "ls-files", "--others", "--exclude-standard")
	if trackedDrift != "" || untracked != "" {
		return jsjson.Value{}, &IntegrityError{Message: ErrDirtyWorktree}
	}
	headCommit := run(repositoryRoot, "rev-parse", "HEAD")
	treeDigest := run(repositoryRoot, "rev-parse", "HEAD^{tree}")
	now := o.Now
	if now == nil {
		now = clock.Now
	}
	capturedAt := clock.ISO(now())
	return SignGitCheckpoint(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(gitproof.GitCheckpointVersion),
		"repositoryRoot", jsjson.MakeString(repositoryRoot),
		"headCommit", jsjson.MakeString(headCommit),
		"treeDigest", jsjson.MakeString(treeDigest),
		"capturedAt", jsjson.MakeString(capturedAt),
		"clean", jsjson.MakeBool(true),
	))
}
