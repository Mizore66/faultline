//go:build faultline_test

package difftest

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/schema"
)

// bridgeDate is the author and committer date both sides set, so the
// synthetic commits' ids are deterministic. A hardened git (no inherited
// environment) would use the wall clock and give other ids.
const bridgeDate = "2026-07-16T10:00:00Z"

// bridgeRepo is one generated repository and the revisions a transition's
// treeDigest is drawn from.
type bridgeRepo struct {
	template string
	digests  []string
}

// bridgeGit runs the test's own git on dir with no user configuration and
// fixed dates.
func bridgeGit(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=FaultLine Test", "-c", "user.email=test@faultline.invalid",
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "gc.auto=0"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "LC_ALL=C",
		"GIT_AUTHOR_DATE="+bridgeDate, "GIT_COMMITTER_DATE="+bridgeDate)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %q: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// newBridgeRepo builds a repository with three trees, a commit, an
// annotated tag and a blob, and lists the revisions to draw from: the
// trees, the commit and the tag (both peel to a tree), the blob (does not),
// missing, abbreviated, upper-case and "-"-prefixed ids.
func newBridgeRepo(t *testing.T, objectFormat string) bridgeRepo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "template")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bridgeGit(t, dir, "", "init", "-q", "--object-format="+objectFormat)
	blob := func(content string) string { return bridgeGit(t, dir, content, "hash-object", "-w", "--stdin") }
	good, bad, other := blob("good\n"), blob("bad\n"), blob("other\n")
	treeOf := func(entries ...string) string { return bridgeGit(t, dir, strings.Join(entries, ""), "mktree") }
	goodTree := treeOf("100644 blob " + good + "\tstate.txt\n")
	badTree := treeOf("100644 blob " + bad + "\tstate.txt\n")
	bothTree := treeOf("100644 blob "+other+"\tother.txt\n", "100644 blob "+bad+"\tstate.txt\n")
	commit := bridgeGit(t, dir, "", "commit-tree", goodTree, "-m", "base")
	bridgeGit(t, dir, "", "tag", "-a", "v1", "-m", "tag", commit)
	tag := bridgeGit(t, dir, "", "rev-parse", "refs/tags/v1")
	missing := strings.Repeat("e", len(goodTree))
	other64 := strings.Repeat("d", 104-len(goodTree)) // the other hash length
	return bridgeRepo{template: dir, digests: []string{
		goodTree, badTree, bothTree, goodTree, badTree, bothTree, commit, tag, good, missing, other64,
		goodTree[:7], strings.ToUpper(goodTree), "-" + goodTree[1:],
	}}
}

// looseObjects lists the loose objects under repository/.git/objects.
func looseObjects(t *testing.T, repository string) []string {
	t.Helper()
	objects := filepath.Join(repository, ".git", "objects")
	dirs, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range dirs {
		if len(d.Name()) != 2 || strings.Trim(d.Name(), "0123456789abcdef") != "" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(objects, d.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			ids = append(ids, d.Name()+f.Name())
		}
	}
	return ids
}

// createdObjects is oracle-bridge.ts's created: the loose objects not in
// before, sorted, each with its inflated content.
func createdObjects(t *testing.T, repository string, before []string) jsjson.Value {
	t.Helper()
	var ids []string
	for _, id := range looseObjects(t, repository) {
		if !slices.Contains(before, id) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int { return slices.Compare(jsstr.ToUTF16(a), jsstr.ToUTF16(b)) })
	items := make([]jsjson.Value, len(ids))
	for i, id := range ids {
		raw, err := os.ReadFile(filepath.Join(repository, ".git", "objects", id[:2], id[2:]))
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		items[i] = jsjson.MustObject("id", jsjson.MakeString(id), "object", jsjson.MakeString(jsstr.DecodeUTF8(content)))
	}
	return jsjson.MakeArray(items)
}

// bridgeOutcome renders BridgeTurnTransition's result as oracle-bridge.ts
// does.
func bridgeOutcome(c minimize.SyntheticTurnCommits, err error, created jsjson.Value) string {
	if err != nil {
		var zodErr *schema.Error
		return jsjson.Stringify(jsjson.MustObject("threw", jsjson.MakeString(jsexc.Message(err)), "zod", jsjson.MakeBool(errors.As(err, &zodErr)),
			"created", created))
	}
	return jsjson.Stringify(jsjson.MustObject("value", jsjson.MustObject(
		"beforeCommit", jsjson.MakeString(c.BeforeCommit),
		"afterCommit", jsjson.MakeString(c.AfterCommit),
		"beforeTree", jsjson.MakeString(c.BeforeTree),
		"afterTree", jsjson.MakeString(c.AfterTree),
		"transitionIndex", jsjson.MakeNumber(c.TransitionIndex),
		"transition", c.Transition,
		"note", jsjson.MakeString(c.Note),
	), "created", created))
}

// randomBridgeTransition is a stable transition over two of repo's
// digests: PASS_TO_FAIL, FAIL_TO_PASS, or PASS_TO_FAIL with the verdicts
// swapped, with ordinals that interpolate unusually (-0, 1e21).
func randomBridgeTransition(r *rand.Rand, repo bridgeRepo) jsjson.Value {
	kind, beforeVerdict, afterVerdict := "PASS_TO_FAIL", "PASS", "FAIL"
	switch n := r.IntN(10); {
	case n == 0:
		kind, beforeVerdict, afterVerdict = "FAIL_TO_PASS", "FAIL", "PASS"
	case n == 1:
		beforeVerdict, afterVerdict = "FAIL", "PASS"
	case n == 2:
		afterVerdict = "PASS"
	}
	digest := func() string {
		if r.IntN(3) != 0 {
			return repo.digests[r.IntN(6)] // the trees
		}
		return repo.digests[r.IntN(len(repo.digests))]
	}
	ordinals := []string{"0", "1", "2", "7", "-0", "12345", "1e21", "9007199254740993"}
	state := func(index int, verdict, tree string) jsjson.Value {
		ids := make([]jsjson.Value, 3)
		for i := range ids {
			ids[i] = jsjson.MakeString("sha256:" + strings.Repeat(strconv.Itoa(index*3+i+1), 64))
		}
		return jsjson.MustObject("stateIndex", jsjson.MakeNumber(float64(index)), "turnId", jsjson.MakeString("turn-"+strconv.Itoa(index)),
			"turnOrdinal", numberSentinel(ordinals[r.IntN(len(ordinals))]), "role", jsjson.MakeString([]string{"SESSION_BASELINE", "TURN"}[min(index, 1)]),
			"treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat("a", 64)),
			"verdict", jsjson.MakeString(verdict), "executionIds", jsjson.MakeArray(ids), "runIds", jsjson.MakeArray(ids))
	}
	return jsjson.MustObject("kind", jsjson.MakeString(kind), "before", state(0, beforeVerdict, digest()), "after", state(1, afterVerdict, digest()))
}

// bridgeTransition is a PASS_TO_FAIL transition from tree before (turn 0)
// to tree after (turn 1).
func bridgeTransition(before, after string) jsjson.Value {
	state := func(index int, verdict, tree string) jsjson.Value {
		ids := make([]jsjson.Value, 3)
		for i := range ids {
			ids[i] = jsjson.MakeString("sha256:" + strings.Repeat(strconv.Itoa(index*3+i+1), 64))
		}
		return jsjson.MustObject("stateIndex", jsjson.MakeNumber(float64(index)), "turnId", jsjson.MakeString("turn-"+strconv.Itoa(index)),
			"turnOrdinal", jsjson.MakeNumber(float64(index)), "role", jsjson.MakeString("TURN"),
			"treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat("a", 64)),
			"verdict", jsjson.MakeString(verdict), "executionIds", jsjson.MakeArray(ids), "runIds", jsjson.MakeArray(ids))
	}
	return jsjson.MustObject("kind", jsjson.MakeString("PASS_TO_FAIL"), "before", state(0, "PASS", before), "after", state(1, "FAIL", after))
}

// fakeGitScript is the git a case's PATH may name instead of the real one:
// FAULTLINE_FAKE_GIT picks a silent failure, a kill by signal (null
// status) or output that is not an object id.
const fakeGitScript = `#!/bin/sh
case "$FAULTLINE_FAKE_GIT" in
silent) exit 3 ;;
signal) kill -9 $$ ;;
junk) echo "not an object id"; exit 0 ;;
esac
exit 1
`

// TestLiveTurnBridge runs bridgeTurnTransitionToMinimizationCommits and
// BridgeTurnTransition on two copies of the same generated repositories
// (SHA-1 and SHA-256), each case with the same environment changes on both
// sides, and compares the results and the loose objects each call created.
func TestLiveTurnBridge(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(97, 4))
	work := t.TempDir()
	emptyConfig := filepath.Join(work, "empty.gitconfig")
	gpgConfig := filepath.Join(work, "gpg.gitconfig")
	encodingConfig := filepath.Join(work, "encoding.gitconfig")
	fakeDir := filepath.Join(work, "fakegit")
	if err := os.WriteFile(emptyConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gpgConfig, []byte("[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// commit-tree ignores commit.gpgSign (git 2.39 and 2.54 both commit
	// unsigned), so the gpgSign cases match without a failure; an
	// i18n.commitEncoding header does change the commit ids, which shows
	// GIT_CONFIG_GLOBAL reaches the bridge's git.
	if err := os.WriteFile(encodingConfig, []byte("[i18n]\n\tcommitEncoding = ISO-8859-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeDir, "git"), []byte(fakeGitScript), 0o755); err != nil {
		t.Fatal(err)
	}

	type copies struct{ goDir, tsDir string }
	var repos []bridgeRepo
	var dirs []copies
	for _, format := range []string{"sha1", "sha256"} {
		repo := newBridgeRepo(t, format)
		pair := copies{filepath.Join(work, format+"-go"), filepath.Join(work, format+"-ts")}
		copyWork(t, repo.template, pair.goDir)
		copyWork(t, repo.template, pair.tsDir)
		repos, dirs = append(repos, repo), append(dirs, pair)
	}

	indices := []string{"0", "0", "0", "0", "1", "1", "-0", "2.5", "99"}
	succeeded, threw, zod, gitFailed, created, encoded := 0, 0, 0, 0, 0, 0
	labels := []string{"plain", "gpgSign", "encoding", "badDate", "noGit", "silent", "signal", "junk"}
	if runtime.GOOS == "windows" {
		labels = labels[:5] // the fake git is a shell script
	}
	for i := 0; i < 300; i++ {
		k := 0
		if i >= len(labels) && r.IntN(5) == 0 {
			k = 1 // the SHA-256 repository
		}
		repo, pair := repos[k], dirs[k]
		indexText := indices[r.IntN(len(indices))]
		n := r.IntN(4)
		if i < len(labels) {
			indexText, n = "0", 1
		}
		index := jsjson.ToNumber(indexText)
		if indexText == "-0" {
			index = math.Copysign(0, -1)
		}
		if i >= len(labels) && !math.IsNaN(index) && index == math.Trunc(index) && index < 2 && r.IntN(4) != 0 {
			n = int(index) + 1 + r.IntN(2) // usually the selected transition exists
		}
		transitions := make([]jsjson.Value, n)
		for j := range transitions {
			switch r.IntN(25) {
			case 0:
				transitions[j] = jsjson.MakeNull()
			case 1:
				transitions[j] = jsjson.MakeNumber(0)
			case 2:
				transitions[j] = jsjson.MustObject("kind", jsjson.MakeString("PASS_TO_FAIL"))
			default:
				transitions[j] = randomBridgeTransition(r, repo)
			}
		}
		// The first cases run each environment on a transition that
		// bridges in the plain one.
		label := "plain"
		if i < len(labels) {
			label = labels[i]
			transitions[0] = bridgeTransition(repo.digests[0], repo.digests[1])
		} else if v := r.IntN(40); v < 11 && v-3 < len(labels)-1 {
			label = labels[1+max(0, v-3)] // gpgSign 4 in 40, each other environment 1 in 40
		}
		transitionsText := withNumberForms(jsjson.Stringify(jsjson.MakeArray(transitions)))

		env := [][2]string{{"LC_ALL", "C"}, {"GIT_CONFIG_NOSYSTEM", "1"}, {"GIT_CONFIG_GLOBAL", emptyConfig},
			{"GIT_AUTHOR_DATE", bridgeDate}, {"GIT_COMMITTER_DATE", bridgeDate}}
		switch label {
		case "gpgSign":
			env[2][1] = gpgConfig
		case "encoding":
			env[2][1] = encodingConfig
		case "badDate":
			env[4][1] = "not a date"
		case "noGit":
			env = append(env, [2]string{"PATH", filepath.Join(work, "no-such-directory")})
		case "silent", "signal", "junk":
			env = append(env, [2]string{"PATH", fakeDir}, [2]string{"FAULTLINE_FAKE_GIT", label})
		}
		envObj := jsjson.NewObj()
		for _, kv := range env {
			envObj.Set(kv[0], jsjson.MakeString(kv[1]))
		}
		args := `{"repository":` + jsjson.Quote(pair.tsDir) + `,"transitions":` + transitionsText +
			`,"transitionIndex":` + indexText + `,"env":` + jsjson.Stringify(jsjson.MakeObject(envObj)) + `}`

		t.Run(strconv.Itoa(i)+"-"+label, func(t *testing.T) {
			want := oracleText(t, c, "bridgeTurnTransition", args)
			for _, kv := range env {
				t.Setenv(kv[0], kv[1])
			}
			before := looseObjects(t, pair.goDir)
			result, err := minimize.BridgeTurnTransition(pair.goDir, mustParseText(t, transitionsText).Items(), index)
			got := bridgeOutcome(result, err, createdObjects(t, pair.goDir, before))
			// The copies differ only in their paths.
			want = strings.ReplaceAll(want, pair.tsDir, pair.goDir)
			if got != want {
				t.Fatalf("bridgeTurnTransition(%s):\ngot  %s\nwant %s", args, got, want)
			}
			recordOutput(t, want)
			var zodErr *schema.Error
			switch {
			case err == nil:
				succeeded++
			case errors.As(err, &zodErr):
				zod++
			default:
				threw++
				if strings.HasPrefix(err.Error(), "fatal: ") || strings.HasPrefix(err.Error(), "error: ") || strings.HasPrefix(err.Error(), "git ") || strings.HasPrefix(err.Error(), "spawnSync") {
					gitFailed++
				}
			}
			created += len(createdObjects(t, pair.goDir, before).Items())
			if strings.Contains(want, `\nencoding ISO-8859-1\n`) {
				encoded++
			}
		})
	}
	if succeeded < 30 || zod < 10 || gitFailed < 10 || encoded == 0 {
		t.Errorf("too few of a kind: %d bridged, %d ZodErrors, %d git failures, %d with a global commitEncoding", succeeded, zod, gitFailed, encoded)
	}
	liveSummary(t, "300 cases: %d bridged (%d with a global commitEncoding), %d threw (%d git or spawn failures), %d ZodErrors; %d loose objects created per side",
		succeeded, encoded, threw, gitFailed, zod, created)
	requireReached(t, "internal/minimize/bridge.go", recordedOutputs(t))
}
