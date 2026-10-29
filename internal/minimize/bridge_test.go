package minimize

import (
	"math"
	"os/exec"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func bridgeRepo(t *testing.T) (dir, goodTree, badTree string) {
	t.Helper()
	dir = t.TempDir()
	run := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "-q")
	good := run("good\n", "hash-object", "-w", "--stdin")
	bad := run("bad\n", "hash-object", "-w", "--stdin")
	return dir, run("100644 blob "+good+"\tstate.txt\n", "mktree"), run("100644 blob "+bad+"\tstate.txt\n", "mktree")
}

func transition(kind, beforeVerdict, afterVerdict, beforeTree, afterTree string) jsjson.Value {
	state := func(i int, verdict, tree string) jsjson.Value {
		ids := jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("sha256:" + strings.Repeat("1", 64)), jsjson.MakeString("sha256:" + strings.Repeat("2", 64)), jsjson.MakeString("sha256:" + strings.Repeat("3", 64))})
		return jsjson.MustObject("stateIndex", jsjson.MakeNumber(float64(i)), "turnId", jsjson.MakeString("turn"), "turnOrdinal", jsjson.MakeNumber(float64(i)),
			"role", jsjson.MakeString("TURN"), "treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat("4", 64)),
			"verdict", jsjson.MakeString(verdict), "executionIds", ids, "runIds", ids)
	}
	return jsjson.MustObject("kind", jsjson.MakeString(kind), "before", state(0, beforeVerdict, beforeTree), "after", state(1, afterVerdict, afterTree))
}

func TestBridgeCommitsBothTrees(t *testing.T) {
	t.Setenv("GIT_AUTHOR_DATE", "2026-07-16T10:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-07-16T10:00:00Z")
	dir, good, bad := bridgeRepo(t)
	b, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "PASS", "FAIL", good, bad)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.BeforeTree != good || b.AfterTree != bad || len(b.BeforeCommit) != 40 || b.BeforeCommit == b.AfterCommit || b.Note != BridgeNote {
		t.Fatalf("%+v", b)
	}
	out, _ := exec.Command("git", "-C", dir, "cat-file", "-p", b.AfterCommit).Output()
	if !strings.Contains(string(out), "committer FaultLine <faultline@example.invalid>") || !strings.HasSuffix(string(out), "faultline-turn-min after t1 ("+bad[:12]+")\n") {
		t.Fatalf("%s", out)
	}
}

func TestBridgeRefusals(t *testing.T) {
	dir, good, bad := bridgeRepo(t)
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("FAIL_TO_PASS", "FAIL", "PASS", good, bad)}, 0); err == nil ||
		err.Error() != "Counterfactual minimization from a turn package currently requires PASS→FAIL (got FAIL_TO_PASS)." {
		t.Fatal(err)
	}
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "FAIL", "FAIL", good, bad)}, 0); err == nil ||
		err.Error() != "Turn transition endpoints are not a stable PASS→FAIL pair." {
		t.Fatal(err)
	}
	if _, err := BridgeTurnTransition(dir, nil, 3); err == nil || err.Error() != "Turn investigation has no transition at index 3 (0 available)." {
		t.Fatal(err)
	}
	missing := strings.Repeat("e", 40)
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "PASS", "FAIL", missing, bad)}, 0); err == nil || !strings.HasPrefix(err.Error(), "fatal: ") {
		t.Fatal(err)
	}
}

// TestSelectTurnTransition covers what fl cannot reach: a non-integer or
// negative index (the CLI refuses it first) and a transition the stable
// schema rejects (a verified investigation never holds one); -0 selects
// element 0.
func TestSelectTurnTransition(t *testing.T) {
	_, good, bad := bridgeRepo(t)
	valid := transition("PASS_TO_FAIL", "PASS", "FAIL", good, bad)
	for _, index := range []float64{-1, 1.5, math.NaN(), math.Inf(1)} {
		want := "Transition index must be a non-negative integer (got " + jsjson.FormatNumber(index) + ")."
		if _, err := SelectTurnTransition([]jsjson.Value{valid}, index); err == nil || err.Error() != want {
			t.Errorf("%v: %v", index, err)
		}
	}
	if got, err := SelectTurnTransition([]jsjson.Value{valid}, math.Copysign(0, -1)); err != nil || got.Get("before", "treeDigest").Str() != good {
		t.Fatalf("-0: %v", err)
	}
	invalid := transition("PASS_TO_FAIL", "PASS", "FAIL", "not-a-tree", bad)
	_, err := SelectTurnTransition([]jsjson.Value{invalid}, 0)
	if zodErr, ok := err.(*schema.Error); !ok || !strings.Contains(zodErr.Message(), `"before",`) {
		t.Fatalf("%v", err)
	}
}

// TestBridgeRefusesSha256Trees: GitObjectIdSchema is 40 hex digits, so a
// SHA-256 repository's tree fails it with zod's "Invalid".
func TestBridgeRefusesSha256Trees(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "--object-format=sha256").CombinedOutput(); err != nil {
		t.Skipf("no sha256 repositories: %s", out)
	}
	cmd := exec.Command("git", "-C", dir, "mktree")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(out))
	_, err = BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "PASS", "FAIL", tree, tree)}, 0)
	if zodErr, ok := err.(*schema.Error); !ok || !strings.Contains(zodErr.Message(), `"message": "Invalid"`) {
		t.Fatalf("%v", err)
	}
}
