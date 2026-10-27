package prevention

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
)

// RepairedState is commitRepairedWorktreeState's { commit, tree }.
type RepairedState struct {
	Commit, Tree string
}

// gitObjectIDPattern is the object-id regex commitRepairedWorktreeState
// checks hash-object's and commit-tree's output against
// (src/prevention-from-artifacts.ts:420, :471): TS's group captures, which
// .test() never reads, and jsre refuses a capture under a quantifier.
var gitObjectIDPattern = jsre.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`, "")

// executionIDPattern is collectRepairedPreventionRuns' content-bound
// execution id (src/prevention-from-artifacts.ts:507).
var executionIDPattern = jsre.MustCompile(`^sha256:[a-f0-9]{64}$`, "")

// repairGitTimeout and repairGitMaxBuffer are the spawnSync options of
// commitRepairedWorktreeState's git calls.
const (
	repairGitTimeout   = 120_000 * time.Millisecond
	repairGitMaxBuffer = 32 * 1024 * 1024
)

// repairedHardenedEnv ports commitRepairedWorktreeState's hardenedEnv
// (src/prevention-from-artifacts.ts:347): the hardened variables, then
// GIT_INDEX_FILE as the raw path (no toGitPath), then on Windows
// SystemRoot, ComSpec and PATHEXT when set and non-empty. Node encodes the
// env object's JS strings as UTF-8.
func repairedHardenedEnv(indexPath string) []string {
	path, _ := nodeproc.Getenv("PATH")
	nullDevice := "/dev/null"
	if runtime.GOOS == "windows" {
		nullDevice = "NUL"
	}
	env := []string{
		"PATH=" + path,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + nullDevice,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_ALLOW_PROTOCOL=none",
		"GIT_INDEX_FILE=" + indexPath,
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT"} {
			if v, _ := nodeproc.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
		return env
	}
	for i := range env {
		env[i] = jsstr.ToUTF8(env[i])
	}
	return env
}

// repairedGit is one of commitRepairedWorktreeState's spawnSync("git",
// [...DOCTOR_SAFE_GIT_CONFIG, -c core.attributesFile=…, -C, root, ...args],
// { encoding: "utf8", shell: false, windowsHide: true, timeout: 120_000,
// maxBuffer: 32 MiB, env, input }) calls: the decoded stdout, or the
// thrown text (stderr || stdout || error.message || fallback, untrimmed).
func repairedGit(root string, args []string, env []string, input []byte, hasInput bool, fallback string) (string, error) {
	argv := append(safegit.TurnSnapshot(), "-C", root)
	argv = append(argv, args...)
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{
		Env: env, Input: input, HasInput: hasInput, MaxBuffer: repairGitMaxBuffer, Timeout: repairGitTimeout,
	})
	stdout, stderr := jsstr.DecodeUTF8(r.Stdout), jsstr.DecodeUTF8(r.Stderr)
	if r.Err != nil || r.Status == nil || *r.Status != 0 {
		message := stderr
		if message == "" {
			message = stdout
		}
		if message == "" && r.Err != nil {
			message = r.Err.Error()
		}
		if message == "" {
			message = fallback
		}
		return "", errors.New(message)
	}
	return stdout, nil
}

// CommitRepairedWorktreeState ports commitRepairedWorktreeState
// (src/prevention-from-artifacts.ts:342): an orphan commit-tree of the
// worktree's files on disk on top of baseCommit, built in a temporary index
// (<tmpdir>/faultline-repair-<uuid>.index, one clock.NewUUID read before
// any git call) under hardened Git config and environment. Every regular
// file is committed except under entries named .git, the worktree's
// .faultline-repair/ and materialized overlays included; symlinks and
// other non-files are skipped. The error is the TS throw; the index's
// removal (rmSync force) runs last and its failure wins.
func CommitRepairedWorktreeState(worktreePath, baseCommit string) (state RepairedState, err error) {
	root := nodefs.Resolve(worktreePath)
	temporaryIndexPath := nodefs.Join(nodefs.Tmpdir(), "faultline-repair-"+clock.NewUUID()+".index")
	defer func() {
		// finally: rmSync(temporaryIndexPath, { force: true }).
		if rmErr := gitinv.RemoveForce(temporaryIndexPath); rmErr != nil {
			state, err = RepairedState{}, rmErr
		}
	}()
	env := repairedHardenedEnv(temporaryIndexPath)
	run := func(args ...string) (string, error) {
		out, err := repairedGit(root, args, env, nil, false, jsexc.Concat("git ", strings.Join(args, " "), " failed"))
		return jsstr.Trim(out), err
	}
	hashBlob := func(absolutePath string) (string, error) {
		content, err := nodefs.ReadBytes(absolutePath)
		if err != nil {
			return "", err
		}
		out, err := repairedGit(root, []string{"hash-object", "-w", "--stdin"}, env, content, true,
			jsexc.Concat("git hash-object --stdin failed for ", absolutePath))
		if err != nil {
			return "", err
		}
		blob := jsstr.Trim(out)
		if !gitObjectIDPattern.Test(blob) {
			return "", errors.New(jsexc.Concat("git hash-object --stdin did not return a blob id for ", absolutePath))
		}
		return blob, nil
	}

	if _, err := run("read-tree", baseCommit); err != nil {
		return RepairedState{}, err
	}
	listed, err := run("ls-files", "-z")
	if err != nil {
		return RepairedState{}, err
	}
	// new Set(split("\0").filter(Boolean).map(replace(/\\/g, "/"))).
	var indexed []string
	indexedSet := map[string]bool{}
	for _, path := range strings.Split(listed, "\x00") {
		if path == "" {
			continue
		}
		path = strings.ReplaceAll(path, `\`, "/")
		if !indexedSet[path] {
			indexedSet[path] = true
			indexed = append(indexed, path)
		}
	}
	var absoluteFiles []string
	if err := listWorktreeFiles(root, &absoluteFiles); err != nil {
		return RepairedState{}, err
	}
	seen := map[string]bool{}
	for _, absolutePath := range absoluteFiles {
		relativePath := nodefs.Relative(root, absolutePath)
		if runtime.GOOS == "windows" {
			relativePath = strings.ReplaceAll(relativePath, `\`, "/")
		}
		if relativePath == "" || strings.HasPrefix(relativePath, "..") {
			continue // unreachable: the walk only lists paths below root
		}
		stat, err := nodefs.Lstat(absolutePath)
		if err != nil {
			return RepairedState{}, err
		}
		if !stat.Mode().IsRegular() {
			return RepairedState{}, errors.New(jsexc.Concat("Repaired worktree path is not a regular file: ", relativePath)) // coverage gate: race only (the walk keeps only isFile() entries)
		}
		mode := "100644"
		if stat.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		blob, err := hashBlob(absolutePath)
		if err != nil {
			return RepairedState{}, err
		}
		if _, err := run("update-index", "--add", "--cacheinfo", mode+","+blob+","+relativePath); err != nil {
			return RepairedState{}, err
		}
		seen[relativePath] = true
	}
	for _, indexedPath := range indexed {
		if !seen[indexedPath] {
			if _, err := run("update-index", "--force-remove", "--", indexedPath); err != nil {
				return RepairedState{}, err
			}
		}
	}

	tree, err := run("write-tree")
	if err != nil {
		return RepairedState{}, err
	}
	commit, err := run("commit-tree", tree, "-p", baseCommit, "-m", "faultline: repaired prevention state")
	if err != nil {
		return RepairedState{}, err
	}
	if !gitObjectIDPattern.Test(commit) {
		return RepairedState{}, errors.New("git commit-tree did not return a Git object id for the repaired state.")
	}
	return RepairedState{Commit: commit, Tree: tree}, nil
}

// listWorktreeFiles ports commitRepairedWorktreeState's listWorktreeFiles
// (src/prevention-from-artifacts.ts:425): readdirSync withFileTypes in
// byte order, skipping entries named .git, recursing into directories and
// keeping only isFile() entries.
func listWorktreeFiles(directory string, out *[]string) error {
	entries, err := nodefs.ReadDirTypes(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name == ".git" {
			continue
		}
		absolute := nodefs.Join(directory, entry.Name)
		if entry.IsDir {
			if err := listWorktreeFiles(absolute, out); err != nil {
				return err
			}
			continue
		}
		if !entry.IsFile {
			continue
		}
		*out = append(*out, absolute)
	}
	return nil
}

// Verdict is the verifyOnce result collectRepairedPreventionRuns consumes:
// { ok, detail, executionId? }; ExecutionID is set only when
// HasExecutionID.
type Verdict struct {
	OK             bool
	Detail         string
	ExecutionID    string
	HasExecutionID bool
}

// JSON is the verdict as a JS object, keys in TS order: ok, detail,
// executionId when present.
func (v Verdict) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("ok", jsjson.MakeBool(v.OK))
	o.Set("detail", jsjson.MakeString(v.Detail))
	if v.HasExecutionID {
		o.Set("executionId", jsjson.MakeString(v.ExecutionID))
	}
	return jsjson.MakeObject(o)
}

// CollectRepairedRuns ports collectRepairedPreventionRuns
// (src/prevention-from-artifacts.ts:486): witness/frozen.json and then
// investigation.json are parsed (a failure is err, the TS throw), the
// environment must be HOMOGENEOUS with one digest, and verifyOnce runs up
// to three times, each PASS with a content-bound execution id becoming a
// run binding (keys runId, executionId, commit, tree, verdict,
// witnessDigest, environmentDigest, executionTrust, executionKind). A
// refusal is reasons (TS { ok: false }); a verifyOnce error is err.
// worktreePath is accepted and unused, as in TS.
func CollectRepairedRuns(worktreePath, bundleDirectory, repairedCommit, repairedTree string, verifyOnce func() (Verdict, error)) (runs jsjson.Value, reasons []string, err error) {
	_ = worktreePath
	frozen, err := bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "witness", "frozen.json"), gitproof.FrozenWitnessSchema)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	investigation, err := bundle.ParseFile(nodefs.Join(nodefs.Resolve(bundleDirectory), "investigation.json"), gitproof.GitInvestigationResultSchema)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	environmentDigest, ok := requireHomogeneousEnvironmentDigest(investigation)
	if !ok {
		return jsjson.Value{}, []string{"Cannot collect repaired runs without a homogeneous environment digest."}, nil
	}

	witnessDigest := frozen.Get("witnessDigest")
	count := strconv.Itoa(stableExecutionCount)
	var collected []jsjson.Value
	var runIDs []string
	for attempt := 1; attempt <= stableExecutionCount; attempt++ {
		verdict, err := verifyOnce()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		n := strconv.Itoa(attempt)
		if !verdict.OK {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired-state execution ", n, "/", count, " failed: ", verdict.Detail)}, nil
		}
		if !verdict.HasExecutionID || !executionIDPattern.Test(verdict.ExecutionID) {
			return jsjson.Value{}, []string{jsexc.Concat("Repaired-state execution ", n, "/", count, " omitted a content-bound executionId; refusing PREVENTION_VERIFIED placeholder synthesis.")}, nil
		}
		executionID := jsjson.MakeString(verdict.ExecutionID)
		unsigned := jsjson.NewObj()
		unsigned.Set("schemaVersion", jsjson.MakeString("faultline.prevention-repaired-run.v1"))
		unsigned.Set("attempt", jsjson.MakeNumber(float64(attempt)))
		unsigned.Set("commit", jsjson.MakeString(repairedCommit))
		unsigned.Set("tree", jsjson.MakeString(repairedTree))
		unsigned.Set("witnessDigest", witnessDigest)
		unsigned.Set("environmentDigest", jsjson.MakeString(environmentDigest))
		unsigned.Set("executionId", executionID)
		unsigned.Set("detail", jsjson.MakeString(verdict.Detail))
		runID, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		run := jsjson.NewObj()
		run.Set("runId", jsjson.MakeString(runID))
		run.Set("executionId", executionID)
		run.Set("commit", jsjson.MakeString(repairedCommit))
		run.Set("tree", jsjson.MakeString(repairedTree))
		run.Set("verdict", jsjson.MakeString("PASS"))
		run.Set("witnessDigest", witnessDigest)
		run.Set("environmentDigest", jsjson.MakeString(environmentDigest))
		run.Set("executionTrust", jsjson.MakeString("NATIVE_DOCKER"))
		run.Set("executionKind", jsjson.MakeString("EXECUTED"))
		collected = append(collected, jsjson.MakeObject(run))
		runIDs = append(runIDs, runID)
	}
	// src/prevention-from-artifacts.ts:535: cannot fire, because attempt is
	// part of each digested object, so the three run ids always differ.
	distinct := map[string]bool{}
	for _, id := range runIDs {
		distinct[id] = true
	}
	if len(distinct) != stableExecutionCount {
		return jsjson.Value{}, []string{"Collected repaired run digests were not distinct."}, nil
	}
	return jsjson.MakeArray(collected), nil, nil
}
