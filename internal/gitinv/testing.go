//go:build faultline_test

package gitinv

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

// SetGitTimeoutForTest swaps GIT_MATERIALIZATION_TIMEOUT_MS (in ms); the
// returned func restores it.
func SetGitTimeoutForTest(ms int) (restore func()) {
	old := gitMaterializationTimeoutMs
	gitMaterializationTimeoutMs = ms
	return func() { gitMaterializationTimeoutMs = old }
}

// PinnedImageForTest is a digest-pinned image the fake Dockers accept.
const PinnedImageForTest = "node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// WitnessScriptForTest is the witness.mjs overlay: it reports
// PREDICATE_FAIL and exits 1 when state.txt says bad, PREDICATE_PASS otherwise.
const WitnessScriptForTest = `import { readFileSync } from "node:fs";
const state = readFileSync("state.txt", "utf8").trim();
const outcome = state === "bad" ? "PREDICATE_FAIL" : "PREDICATE_PASS";
console.log("FAULTLINE_WITNESS_RESULT=" + JSON.stringify({ protocol: "faultline.witness-result.v1", outcome }));
process.exit(state === "bad" ? 1 : 0);
`

func repoRoot() string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..")
}

// FakeDockerDirForTest resolves difftest/testdata/fakedocker/<name>.
func FakeDockerDirForTest(t testing.TB, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join(repoRoot(), "difftest", "testdata", "fakedocker", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// RepoCommitForTest is one commit of BuildRepoForTest: files to write
// (slash-separated path to content), symbolic links to create and paths to
// delete, then `git add -A`.
type RepoCommitForTest struct {
	Files    map[string]string
	Symlinks map[string]string // path to link target
	Remove   []string
}

func gitForTest(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine Test", "-c", "user.email=test@faultline.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_DATE=2026-07-16T12:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T12:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// BuildRepoForTest initializes a repository named name ("" for the
// directory itself) in a new physical temporary directory, with the given
// object format ("sha1" or "sha256"), and makes one commit per entry; it
// returns the repository directory and the commit ids.
func BuildRepoForTest(t testing.TB, objectFormat, name string, commits []RepoCommitForTest) (string, []string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		dir = filepath.Join(dir, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitForTest(t, dir, "init", "-q", "--object-format="+objectFormat)
	var ids []string
	for i, c := range commits {
		for path, content := range c.Files {
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for path, target := range c.Symlinks {
			if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(path))); err != nil {
				t.Fatal(err)
			}
		}
		for _, path := range c.Remove {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(path))); err != nil {
				t.Fatal(err)
			}
		}
		gitForTest(t, dir, "add", "-A")
		gitForTest(t, dir, "commit", "-q", "--allow-empty", "-m", "commit "+string(rune('a'+i%26)))
		ids = append(ids, gitForTest(t, dir, "rev-parse", "HEAD"))
	}
	return dir, ids
}

// GoodThenBadRepoForTest is a repository whose state.txt is "good" at the
// ancestor commit and "bad" at its child, the descendant.
func GoodThenBadRepoForTest(t testing.TB) (repo, ancestor, descendant string) {
	t.Helper()
	dir, ids := BuildRepoForTest(t, "sha1", "", []RepoCommitForTest{
		{Files: map[string]string{"state.txt": "good\n", "package.json": "{}\n"}},
		{Files: map[string]string{"state.txt": "bad\n"}},
	})
	return dir, ids[0], ids[1]
}

// FrozenWitnessWithOverlaysForTest proposes, approves and freezes a witness
// running `node witness.mjs` in a temporary store; overlays maps paths to
// contents and always includes witness.mjs (WitnessScriptForTest) first.
// It reads the clock, so call it before seeding a compared sequence.
func FrozenWitnessWithOverlaysForTest(t testing.TB, paths []string, contents []string) jsjson.Value {
	t.Helper()
	overlays := []jsjson.Value{jsjson.MustObject("path", jsjson.MakeString("witness.mjs"),
		"bytesBase64", jsjson.MakeString(base64.StdEncoding.EncodeToString([]byte(WitnessScriptForTest))))}
	for i, path := range paths {
		overlays = append(overlays, jsjson.MustObject("path", jsjson.MakeString(path),
			"bytesBase64", jsjson.MakeString(base64.StdEncoding.EncodeToString([]byte(contents[i])))))
	}
	input := jsjson.MustObject(
		"proposalId", jsjson.MakeString("investigate-1"),
		"incidentPacket", jsjson.MustObject(
			"symptom", jsjson.MakeString("state flips to bad"),
			"ciLog", jsjson.MakeString("state=bad"),
			"repositoryLanguage", jsjson.MakeString("JavaScript"),
			"repositorySummary", jsjson.MakeString("A fixture whose state.txt regresses."),
		),
		"witness", jsjson.MustObject(
			"behavior", jsjson.MakeString("state.txt must not say bad"),
			"command", jsjson.MakeString("node witness.mjs"),
			"overlays", jsjson.MakeArray(overlays),
			"policy", jsjson.MustObject("network", jsjson.MakeString("disabled"), "credentials", jsjson.MakeString("redacted"),
				"timeoutSeconds", jsjson.MakeNumber(30)),
		),
	)
	store := t.TempDir()
	if _, err := witness.Propose(store, input); err != nil {
		t.Fatal(err)
	}
	if _, err := witness.Approve(store, "investigate-1", jsjson.MustObject("approvedBy", jsjson.MakeString("reviewer@example.test"))); err != nil {
		t.Fatal(err)
	}
	frozen, err := witness.Freeze(store, "investigate-1", witness.FreezeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

// FrozenWitnessForTest is FrozenWitnessWithOverlaysForTest with only witness.mjs.
func FrozenWitnessForTest(t testing.TB) jsjson.Value {
	return FrozenWitnessWithOverlaysForTest(t, nil, nil)
}

// The names the package's own tests use.
var (
	fakeDockerDir    = FakeDockerDirForTest
	goodThenBadRepo  = GoodThenBadRepoForTest
	frozenWitnessFor = FrozenWitnessForTest
)

const pinnedImage = PinnedImageForTest

// TwoStateRequestForTest is the two-state investigation request over
// GoodThenBadRepoForTest (DOCKER_ISOLATED, PinnedImageForTest) with the
// fake Docker fake first on PATH (t.Setenv), and the frozen witness it
// names. It reads the clock (the witness), like FrozenWitnessForTest.
func TwoStateRequestForTest(t *testing.T, fake string) (request, frozen jsjson.Value) {
	t.Helper()
	t.Setenv("PATH", FakeDockerDirForTest(t, fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo, ancestor, descendant := GoodThenBadRepoForTest(t)
	frozen = FrozenWitnessForTest(t)
	request = jsjson.MustObject(
		"repository", jsjson.MakeString(repo),
		"range", jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant)),
		"frozenWitness", frozen,
		"expectedFrozenDigest", frozen.Get("frozenDigest"),
		"sandbox", jsjson.MustObject("mode", jsjson.MakeString("DOCKER_ISOLATED"), "image", jsjson.MakeString(PinnedImageForTest)),
	)
	return request, frozen
}
