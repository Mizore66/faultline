// Package runtimeimage ports src/runtime.ts: the curated runtime catalog,
// the fixed Docker inspect/pull/build commands, RepoDigest resolution and
// project image preparation. Docker runs only through a Runner, so tests and
// the live oracle can script its results.
package runtimeimage

import (
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// Runtime is one CURATED_RUNTIME_CATALOG entry (src/runtime.ts:11).
type Runtime struct{ Alias, DisplayName, Tag string }

// JSON is the catalog entry as TS holds it: { alias, displayName, tag }.
func (r Runtime) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("alias", jsjson.MakeString(r.Alias))
	o.Set("displayName", jsjson.MakeString(r.DisplayName))
	o.Set("tag", jsjson.MakeString(r.Tag))
	return jsjson.MakeObject(o)
}

// Port of src/runtime.ts:11-27 CURATED_RUNTIME_CATALOG, in Object.values
// order.
var catalog = [...]Runtime{
	{Alias: "node", DisplayName: "Node.js 22 (Alpine)", Tag: "node:22-alpine"},
	{Alias: "python", DisplayName: "Python 3.13 (Alpine)", Tag: "python:3.13-alpine"},
	{Alias: "go", DisplayName: "Go 1.24 (Alpine)", Tag: "golang:1.24-alpine"},
}

// Catalog ports src/runtime.ts:281 catalogEntries. The slice is a copy.
func Catalog() []Runtime { return append([]Runtime(nil), catalog[:]...) }

// Port of src/runtime.ts:32-44.
const (
	DockerRepoDigestsFormat               = "{{json .RepoDigests}}"
	DockerImageInspectTimeout             = 5_000 * time.Millisecond
	DockerImageInspectMaxOutputBytes      = 64 * 1024
	DockerImagePullTimeout                = 5 * 60_000 * time.Millisecond
	DockerImagePullMaxOutputBytes         = 64 * 1024
	DockerProjectImageBuildTimeout        = 10 * 60_000 * time.Millisecond
	DockerProjectImageBuildMaxOutputBytes = 128 * 1024
)

// Port of src/runtime.ts:55 REPO_DIGEST.
var repoDigest = jsre.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`, "")

// ResolutionError ports src/runtime.ts:135 CuratedRuntimeResolutionError;
// Code is its .code.
type ResolutionError struct{ Code, Message string }

func (e *ResolutionError) Error() string { return e.Message }

func resolutionError(code string, parts ...string) *ResolutionError {
	return &ResolutionError{Code: code, Message: jsexc.Concat(parts...)}
}

// SelectCuratedRuntime ports src/runtime.ts:291 selectCuratedRuntime: the
// alias or the exact catalog tag.
func SelectCuratedRuntime(requested string) (Runtime, error) {
	for _, entry := range catalog {
		if entry.Alias == requested || entry.Tag == requested {
			return entry, nil
		}
	}
	return Runtime{}, resolutionError("UNKNOWN_RUNTIME", "Unknown curated runtime ", jsjson.Quote(requested), ". Choose node, python, or go.")
}

// inspectArgs ports src/runtime.ts:308
// createDockerImageInspectCommandForReference (its arguments).
func inspectArgs(reference string) []string {
	return []string{"image", "inspect", "--format", DockerRepoDigestsFormat, reference}
}

// pullArgs ports src/runtime.ts:326 createDockerImagePullCommand.
func pullArgs(rt Runtime) []string { return []string{"pull", rt.Tag} }

// Port of src/runtime.ts:598 repositoryFromTaggedReference. References are
// ASCII here (catalog tags, validated project tags), so byte and UTF-16
// indexes agree.
func repositoryFromTaggedReference(reference string) string {
	slash := strings.LastIndexByte(reference, '/')
	colon := strings.LastIndexByte(reference, ':')
	if colon > slash {
		return reference[:colon]
	}
	return reference
}

// Port of src/runtime.ts:604 repositoryFromRepoDigest. Only called on values
// REPO_DIGEST accepted (ASCII).
func repositoryFromRepoDigest(value string) string {
	at := strings.IndexByte(value, '@')
	if at < 0 {
		return ""
	}
	return value[:at]
}

// Port of src/runtime.ts:610 isDigestPinnedRepoDigest.
func isDigestPinnedRepoDigest(value string) bool {
	if !repoDigest.Test(value) {
		return false
	}
	repository := repositoryFromRepoDigest(value)
	return strings.LastIndexByte(repository, ':') <= strings.LastIndexByte(repository, '/')
}

// Port of src/runtime.ts:621 normalizeRepository.
func normalizeRepository(repository string) string {
	without := strings.TrimPrefix(repository, "index.docker.io/")
	without = strings.TrimPrefix(without, "docker.io/")
	if !strings.Contains(without, "/") {
		return "library/" + without
	}
	return without
}

// Port of src/runtime.ts:629 isExpectedRepository.
func isExpectedRepository(rt Runtime, digest string) bool {
	return normalizeRepository(repositoryFromTaggedReference(rt.Tag)) == normalizeRepository(repositoryFromRepoDigest(digest))
}

// Port of src/runtime.ts:634 isExpectedProjectImageRepository.
func isExpectedProjectImageRepository(imageTag, digest string) bool {
	return normalizeRepository(repositoryFromTaggedReference(imageTag)) == normalizeRepository(repositoryFromRepoDigest(digest))
}

// Port of src/runtime.ts:639 parseRepoDigests: anything but a JSON array of
// strings is [].
func parseRepoDigests(stdout string) []string {
	trimmed := jsstr.Trim(stdout)
	if trimmed == "" {
		return nil
	}
	var parsed jsjson.Value
	var perr error
	if thrown := jsexc.Try(func() { parsed, perr = jsjson.Parse(trimmed) }); thrown != nil || perr != nil {
		return nil
	}
	if parsed.Kind() != jsjson.Array {
		return nil
	}
	items := parsed.Items()
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind() != jsjson.String {
			return nil
		}
		out = append(out, item.Str())
	}
	return out
}

// Port of src/runtime.ts:651 dockerFailureDetail:
// `stderr.replace(/[\r\n]+/g, " ").trim()`, then ": " and its first 320
// UTF-16 units, or "" when blank.
func dockerFailureDetail(r hostcmd.Result) string {
	var b strings.Builder
	b.Grow(len(r.Stderr))
	inRun := false
	for i := 0; i < len(r.Stderr); i++ {
		c := r.Stderr[i]
		if c == '\r' || c == '\n' {
			if !inRun {
				b.WriteByte(' ')
			}
			inRun = true
			continue
		}
		inRun = false
		b.WriteByte(c)
	}
	detail := jsstr.Trim(b.String())
	if detail == "" {
		return ""
	}
	units := jsstr.ToUTF16(detail)
	return ": " + jsstr.FromUTF16(units[:min(len(units), 320)])
}

// call runs one Docker command through run with the cap the TS runner picks
// for it. thrown is the runner's rejection (a jsexc.Throw inside run).
func call(run Runner, args []string, timeout time.Duration) (result hostcmd.Result, thrown error) {
	thrown = jsexc.Try(func() { result = run(args, timeout, maxOutputBytesFor(args)) })
	return result, thrown
}

// ResolveCuratedRuntime ports src/runtime.ts:720 resolveCuratedRuntime. The
// result is { runtime, requested, image }.
func ResolveCuratedRuntime(requested string, run Runner) (jsjson.Value, error) {
	rt, err := SelectCuratedRuntime(requested)
	if err != nil {
		return jsjson.Value{}, err
	}
	result, thrown := call(run, inspectArgs(rt.Tag), DockerImageInspectTimeout)
	if thrown != nil {
		return jsjson.Value{}, resolutionError("DOCKER_INSPECT_FAILED", "FaultLine could not inspect the local ", rt.Tag, " image: ", jsexc.Message(thrown))
	}
	if result.TimedOut {
		return jsjson.Value{}, resolutionError("DOCKER_INSPECT_TIMED_OUT", "Docker image inspection timed out for ", rt.Tag, ".")
	}
	if result.OutputLimitExceeded {
		return jsjson.Value{}, resolutionError("DOCKER_INSPECT_OUTPUT_LIMIT", "Docker image inspection exceeded FaultLine's 65536-byte output limit for ", rt.Tag, ".")
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		return jsjson.Value{}, resolutionError("DOCKER_INSPECT_FAILED", "Docker could not inspect the local ", rt.Tag, " image", dockerFailureDetail(result))
	}

	var pinned []string
	for _, d := range parseRepoDigests(result.Stdout) {
		if isDigestPinnedRepoDigest(d) {
			pinned = append(pinned, d)
		}
	}
	if len(pinned) == 0 {
		return jsjson.Value{}, resolutionError("UNRESOLVED_REPO_DIGEST", "Docker did not return a digest-pinned RepoDigest for ", rt.Tag, ". Pull it explicitly, then try again.")
	}
	expected, found := "", false
	for _, d := range pinned {
		if isExpectedRepository(rt, d) {
			expected, found = d, true
			break
		}
	}
	if !found {
		return jsjson.Value{}, resolutionError("UNEXPECTED_REPOSITORY", "Docker returned a RepoDigest for a repository other than curated ", rt.Tag, ".")
	}

	o := jsjson.NewObj()
	o.Set("runtime", rt.JSON())
	o.Set("requested", jsjson.MakeString(requested))
	o.Set("image", jsjson.MakeString(expected))
	return jsjson.MakeObject(o), nil
}

// PrepareCuratedRuntime ports src/runtime.ts:785 prepareCuratedRuntime: an
// explicit `docker pull` of the catalog tag, then ResolveCuratedRuntime with
// the same runner. The result is { ...resolved, pull: { tag,
// resolvedRepoDigest } }.
func PrepareCuratedRuntime(requested string, run Runner) (jsjson.Value, error) {
	rt, err := SelectCuratedRuntime(requested)
	if err != nil {
		return jsjson.Value{}, err
	}
	result, thrown := call(run, pullArgs(rt), DockerImagePullTimeout)
	if thrown != nil {
		return jsjson.Value{}, resolutionError("DOCKER_PULL_FAILED", "FaultLine could not pull the reviewed ", rt.Tag, " image: ", jsexc.Message(thrown))
	}
	if result.TimedOut {
		return jsjson.Value{}, resolutionError("DOCKER_PULL_TIMED_OUT", "Docker image pull timed out for ", rt.Tag, ".")
	}
	if result.OutputLimitExceeded {
		return jsjson.Value{}, resolutionError("DOCKER_PULL_OUTPUT_LIMIT", "Docker image pull exceeded FaultLine's 65536-byte output limit for ", rt.Tag, ".")
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		return jsjson.Value{}, resolutionError("DOCKER_PULL_FAILED", "Docker could not pull the reviewed ", rt.Tag, " image", dockerFailureDetail(result))
	}

	resolved, err := ResolveCuratedRuntime(requested, run)
	if err != nil {
		return jsjson.Value{}, err
	}
	pull := jsjson.NewObj()
	pull.Set("tag", jsjson.MakeString(rt.Tag))
	pull.Set("resolvedRepoDigest", resolved.Get("image"))
	o := jsjson.NewObj()
	for _, k := range resolved.Obj().Keys() {
		o.Set(k, resolved.Obj().Field(k))
	}
	o.Set("pull", jsjson.MakeObject(pull))
	return jsjson.MakeObject(o), nil
}
