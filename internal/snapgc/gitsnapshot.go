package snapgc

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// runGit ports runGit (src/git-snapshot.ts:17): spawnSync("git", ["-C",
// repository, ...args], { encoding: "utf8" }) with the inherited
// environment and working directory, Node's default maxBuffer and
// windowsHide false. It is not hardened: the user's Git configuration
// applies. The result is the trimmed stdout.
func runGit(repository string, args ...string) (string, error) {
	r := nodeproc.SpawnSyncWith("git", append([]string{"-C", repository}, args...), nodeproc.SpawnSyncOptions{WindowsShow: true})
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
		return "", errors.New(jsexc.Concat("Git sidecar capture failed for ", jsexc.Join(args, " "), ": ", detail))
	}
	return jsstr.Trim(jsstr.DecodeUTF8(r.Stdout)), nil
}

// CaptureCleanGitSnapshot ports captureCleanGitSnapshot
// (src/git-snapshot.ts:26): the Git top level of resolve(repository) (git's
// own text, not resolved again), refused when `git status` reports
// anything, then HEAD and its tree, digested in the TS literal's order.
func CaptureCleanGitSnapshot(repository string) (jsjson.Value, error) {
	requestedRoot := nodefs.Resolve(repository)
	repoRoot, err := runGit(requestedRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return jsjson.Value{}, err
	}
	status, err := runGit(repoRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return jsjson.Value{}, err
	}
	if status != "" {
		return jsjson.Value{}, errors.New("Git sidecar capture requires a clean worktree. Commit, stash, or explicitly model changes before recording evidence.")
	}
	headCommit, err := runGit(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return jsjson.Value{}, err
	}
	treeDigest, err := runGit(repoRoot, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return jsjson.Value{}, err
	}
	unsigned := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.git-sidecar.v1"),
		"recorder", jsjson.MakeString("git-sidecar"),
		"repositoryName", jsjson.MakeString(nodefs.Basename(repoRoot)),
		"headCommit", jsjson.MakeString(headCommit),
		"treeDigest", jsjson.MakeString(treeDigest),
		"capturedAt", jsjson.MakeString(clock.ISO(clock.Now())),
		"clean", jsjson.MakeBool(true),
	)
	digest, err := canonical.DigestJSON(unsigned)
	if err != nil {
		return jsjson.Value{}, err
	}
	snapshot := jsjson.NewObj()
	for _, k := range unsigned.Obj().Keys() {
		snapshot.Set(k, unsigned.Get(k))
	}
	snapshot.Set("digest", jsjson.MakeString(digest))
	return jsjson.MakeObject(snapshot), nil
}

// WriteGitSidecarSnapshot ports writeGitSidecarSnapshot
// (src/git-snapshot.ts:47): <resolve(repository)>/.faultline/recordings
// (made recursively), the file named by the digest's hex, written as
// JSON.stringify(snapshot, null, 2) + "\n" with flag w. It returns the
// file's path.
func WriteGitSidecarSnapshot(repository string, snapshot jsjson.Value) (string, error) {
	outputDirectory := nodefs.Join(nodefs.Resolve(repository), ".faultline", "recordings")
	if err := nodefs.MkdirAll(outputDirectory, 0o777); err != nil {
		return "", err
	}
	file := nodefs.Join(outputDirectory, strings.Replace(snapshot.Get("digest").Str(), "sha256:", "", 1)+".json")
	if err := nodefs.WriteFileUTF8(file, jsexc.Concat(jsjson.StringifyIndent(snapshot, "  "), "\n"), 0o666, false); err != nil {
		return "", err
	}
	return file, nil
}
