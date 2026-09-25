package gitproof

import (
	"bytes"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

const maxGitOutputBytes = 4 * 1024 * 1024

type gitResult struct {
	status         *int
	stdout, stderr []byte
	err            error
}

// limitedBuffer fails the write once maxGitOutputBytes is exceeded (spawnSync maxBuffer).
type limitedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxGitOutputBytes {
		b.exceeded = true
		return 0, errors.New("maxBuffer exceeded")
	}
	return b.Buffer.Write(p)
}

func toGitPath(path string) string { return strings.ReplaceAll(nodefs.Resolve(path), `\`, "/") }

// runGit ports runGit (git-proof-bundle.ts:380).
func runGit(repository string, args ...string) gitResult {
	if repository != "" {
		args = append([]string{"-C", toGitPath(repository)}, args...)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	result := gitResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	switch {
	case stdout.exceeded || stderr.exceeded:
		result.err = errors.New("spawnSync git ENOBUFS")
	case errors.Is(runErr, exec.ErrNotFound):
		result.err = errors.New("spawnSync git ENOENT")
	default:
		code := cmd.ProcessState.ExitCode()
		result.status = &code
	}
	return result
}

func statusText(status *int) string {
	if status == nil {
		return "null"
	}
	return strconv.Itoa(*status)
}

// gitBytes ports gitBytes (git-proof-bundle.ts:398).
func gitBytes(repository, label string, args ...string) []byte {
	r := runGit(repository, args...)
	if r.err != nil || r.status == nil || *r.status != 0 {
		var parts []string
		if s := jsstr.Trim(nodefs.DecodeUTF8(r.stderr)); s != "" {
			parts = append(parts, s)
		}
		if r.err != nil {
			parts = append(parts, r.err.Error())
		}
		detail := strings.Join(parts, "; ")
		if detail == "" {
			detail = "exit " + statusText(r.status)
		}
		bundle.Throw(errors.New(label + " failed: " + detail))
	}
	return r.stdout
}

func gitText(repository, label string, args ...string) string {
	return jsstr.Trim(nodefs.DecodeUTF8(gitBytes(repository, label, args...)))
}

var gitObjectIDPattern = regexp.MustCompile(gitObjectIDSource)

// resolveGitState ports resolveGitState (git-proof-bundle.ts:411).
func resolveGitState(repository, commit string) (string, string) {
	resolved := gitText(repository, "Git commit resolution", "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	tree := gitText(repository, "Git tree resolution", "rev-parse", "--verify", "--end-of-options", resolved+"^{tree}")
	if !gitObjectIDPattern.MatchString(resolved) || !gitObjectIDPattern.MatchString(tree) {
		bundle.Throw(errors.New("Git returned an invalid object identifier while writing a proof bundle."))
	}
	return resolved, tree
}

func gitStateValue(commit, tree string) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("commit", jsjson.MakeString(commit))
	o.Set("tree", jsjson.MakeString(tree))
	return jsjson.MakeObject(o)
}

// splitLines is text.split(/\r?\n/).filter(Boolean).
func splitLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// matchBundleHead is /^([a-f0-9]{40}|[a-f0-9]{64})\s+(\S+)$/ with JS \s.
func matchBundleHead(line string) (commit, ref string, ok bool) {
	units := jsstr.ToUTF16(line)
	ws := func(i int) bool { return i < len(units) && jsstr.IsWhitespace(units[i]) }
	n := 0
	switch {
	case len(line) >= 40 && isHex(line[:40]) && ws(40):
		n = 40
	case len(line) >= 64 && isHex(line[:64]) && ws(64):
		n = 64
	default:
		return "", "", false
	}
	i := n
	for ws(i) {
		i++
	}
	if i == len(units) {
		return "", "", false
	}
	for j := i; j < len(units); j++ {
		if jsstr.IsWhitespace(units[j]) {
			return "", "", false
		}
	}
	return line[:n], jsstr.FromUTF16(units[i:]), true
}

type bundleHead struct{ commit, ref string }

// parseBundleHeads ports parseBundleHeads (git-proof-bundle.ts:451).
func parseBundleHeads(b []byte) []bundleHead {
	var heads []bundleHead
	for _, line := range splitLines(nodefs.DecodeUTF8(b)) {
		commit, ref, ok := matchBundleHead(jsstr.Trim(line))
		if !ok {
			bundle.Throw(errors.New("Git bundle returned an invalid head line: " + line))
		}
		heads = append(heads, bundleHead{commit, ref})
	}
	return heads
}

// binaryRangePatch ports binaryRangePatch (git-proof-bundle.ts:486).
func binaryRangePatch(repository, ancestor, descendant string) []byte {
	patch := gitBytes(repository, "Git binary range patch creation", "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-renames", "--end-of-options", ancestor, descendant)
	if len(patch) > maxSourceArtifactBytes {
		bundle.Throw(errors.New("Git range patch exceeds FaultLine's portable artifact limit."))
	}
	return patch
}

// sourceMetadataFromArtifacts ports git-proof-bundle.ts:890.
func sourceMetadataFromArtifacts(root string, metadata, result, manifest jsjson.Value, errs *[]string) {
	resolved := result.Get("resolvedRange")
	if resolved.Kind() == jsjson.Null {
		*errs = append(*errs, "investigation has no resolved range for source metadata cross-check")
		return
	}
	if !bundle.SameCanonical(metadata.Get("ancestor"), resolved.Get("ancestor")) || !bundle.SameCanonical(metadata.Get("descendant"), resolved.Get("descendant")) {
		*errs = append(*errs, "source metadata endpoints do not match the investigation range")
	}
	if metadata.Get("bundle", "path").Str() != manifest.Get("artifacts", "gitBundle").Str() || metadata.Get("rangePatch", "path").Str() != manifest.Get("artifacts", "rangePatch").Str() {
		*errs = append(*errs, "source metadata artifact paths do not match the manifest")
	}
	gitBundle := readBoundedFile(safeArtifactPath(root, metadata.Get("bundle", "path").Str()), "source Git bundle", maxSourceArtifactBytes)
	patch := readBoundedFile(safeArtifactPath(root, metadata.Get("rangePatch", "path").Str()), "source range patch", maxSourceArtifactBytes)
	if metadata.Get("bundle", "digest").Str() != digestBytes(gitBundle) || metadata.Get("bundle", "bytes").Num() != float64(len(gitBundle)) {
		*errs = append(*errs, "source metadata Git bundle digest or byte count is invalid")
	}
	if metadata.Get("rangePatch", "digest").Str() != digestBytes(patch) || metadata.Get("rangePatch", "bytes").Num() != float64(len(patch)) {
		*errs = append(*errs, "source metadata range patch digest or byte count is invalid")
	}
}

// verifyPortableGitSource ports git-proof-bundle.ts:913.
func verifyPortableGitSource(root string, metadata, result jsjson.Value, errs *[]string) {
	bundlePath := safeArtifactPath(root, metadata.Get("bundle", "path").Str())
	patchPath := safeArtifactPath(root, metadata.Get("rangePatch", "path").Str())
	assertRegularFile(bundlePath, "portable Git bundle", maxSourceArtifactBytes)
	assertRegularFile(patchPath, "portable Git range patch", maxSourceArtifactBytes)
	temporaryBare := bundle.Must(nodefs.MkdirTemp("faultline-git-proof-verify-"))
	defer nodefs.RemoveAll(temporaryBare)
	if err := bundle.Try(func() {
		gitBundlePath := toGitPath(bundlePath)
		heads := parseBundleHeads(gitBytes("", "Git bundle head listing", "bundle", "list-heads", gitBundlePath))
		descendantCommit := metadata.Get("descendant", "commit").Str()
		if len(heads) != 1 || heads[0].ref != metadata.Get("bundle", "headRef").Str() || heads[0].commit != metadata.Get("bundle", "headCommit").Str() || heads[0].commit != descendantCommit {
			*errs = append(*errs, "Git bundle heads do not match source metadata descendant")
		}
		gitBytes("", "Temporary Git verifier initialization", "init", "--bare", "--quiet", toGitPath(temporaryBare))
		gitBytes(temporaryBare, "Git bundle verification", "bundle", "verify", gitBundlePath)
		gitBytes(temporaryBare, "Git bundle extraction", "fetch", "--quiet", gitBundlePath, BundleHeadRef+":refs/heads/faultline-descendant")
		ancestorCommit := metadata.Get("ancestor", "commit").Str()
		dc, dt := resolveGitState(temporaryBare, "refs/heads/faultline-descendant")
		ac, at := resolveGitState(temporaryBare, ancestorCommit)
		if !bundle.SameCanonical(gitStateValue(dc, dt), gitStateValue(descendantCommit, metadata.Get("descendant", "tree").Str())) {
			*errs = append(*errs, "Git bundle descendant object does not match source metadata")
		}
		if !bundle.SameCanonical(gitStateValue(ac, at), gitStateValue(ancestorCommit, metadata.Get("ancestor", "tree").Str())) {
			*errs = append(*errs, "Git bundle ancestor object does not match source metadata")
		}
		if ancestry := runGit(temporaryBare, "merge-base", "--is-ancestor", ancestorCommit, descendantCommit); ancestry.err != nil || ancestry.status == nil || *ancestry.status != 0 {
			*errs = append(*errs, "Git bundle does not preserve ancestor-to-descendant ancestry")
		}
		listed := gitText(temporaryBare, "Git bundle range enumeration", "rev-list", "--reverse", "--ancestry-path", "--end-of-options", ancestorCommit+".."+descendantCommit)
		commits := []string{ancestorCommit}
		if ancestorCommit != descendantCommit {
			commits = append(commits, splitLines(listed)...)
		}
		states := make([]jsjson.Value, len(commits))
		for i, commit := range commits {
			c, tree := resolveGitState(temporaryBare, commit)
			o := jsjson.NewObj()
			o.Set("index", jsjson.MakeNumber(float64(i)))
			o.Set("commit", jsjson.MakeString(c))
			o.Set("tree", jsjson.MakeString(tree))
			states[i] = jsjson.MakeObject(o)
		}
		if !bundle.SameCanonical(jsjson.MakeArray(states), result.Get("states")) {
			*errs = append(*errs, "investigation state sequence does not exactly match the bundled ancestor-to-descendant Git path")
		}
		generated := binaryRangePatch(temporaryBare, ancestorCommit, descendantCommit)
		stored := readBoundedFile(patchPath, "portable Git range patch", maxSourceArtifactBytes)
		if !bytes.Equal(generated, stored) {
			*errs = append(*errs, "binary range patch does not reproduce the bundled Git endpoint diff")
		}
		if gitText(temporaryBare, "Git bundle object-format resolution", "rev-parse", "--show-object-format") != metadata.Get("objectFormat").Str() {
			*errs = append(*errs, "Git bundle object format does not match source metadata")
		}
	}); err != nil {
		*errs = append(*errs, "portable Git source verification failed: "+err.Error())
	}
}
