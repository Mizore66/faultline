//go:build faultline_test && unix

package difftest

import (
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// investigateCase is one generated investigation: its request (the JSON
// form of GitInvestigationRequest without a runner) and the fake Docker.
type investigateCase struct {
	label   string
	request jsjson.Value
	fake    string
}

// packageJSONFingerprint is the environment fingerprint digest of a tree
// whose only descriptor file is package.json with the given content.
func packageJSONFingerprint(t *testing.T, content string) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := gitinv.ComputeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fp.Get("digest").Str()
}

// randomInvestigateCase builds a repository and a request. The range has
// 2 to 20 states (sometimes one), with a PASS to FAIL change at a random
// point, a non-monotonic history or no change; some ranges change
// package.json (a heterogeneous fingerprint) with a complete, partial,
// invalid or no runtime mapping; some repositories use the sha256 object
// format, live under CJK or spaced directory names, track files the
// witness overlays replace, track a symlink an overlay targets, or declare
// a Git filter. A few requests are invalid or name a bad range.
func randomInvestigateCase(t *testing.T, r *rand.Rand, witnesses []jsjson.Value) investigateCase {
	n := 2 + r.IntN(19)
	if r.IntN(20) == 0 {
		n = 1
	}
	prefix := r.IntN(3)
	verdicts := make([]string, n)
	shape := r.IntN(10)
	flip := 1 + r.IntN(max(1, n-1))
	for i := range verdicts {
		switch {
		case shape < 6: // PASS to FAIL at a random point
			verdicts[i] = map[bool]string{true: "bad", false: "good"}[i >= flip]
		case shape < 8: // non-monotonic
			verdicts[i] = pickOf(r, []string{"good", "bad"})
		case shape < 9: // FAIL to PASS
			verdicts[i] = map[bool]string{true: "good", false: "bad"}[i >= flip]
		default:
			verdicts[i] = "good"
		}
	}
	heterogeneous := n > 1 && r.IntN(3) == 0
	changeAt := 1 + r.IntN(max(1, n-1))
	symlinkTarget := r.IntN(20) == 0
	filter := r.IntN(25) == 0
	var commits []gitinv.RepoCommitForTest
	for i := 0; i < prefix; i++ {
		commits = append(commits, gitinv.RepoCommitForTest{Files: map[string]string{"state.txt": "good\n", "prefix.txt": strconv.Itoa(i)}})
	}
	packageJSON := func(i int) string {
		if heterogeneous && i >= changeAt {
			return `{"name":"fixture","dependencies":{"left-pad":"1.3.0"}}` + "\n"
		}
		return `{"name":"fixture"}` + "\n"
	}
	for i := 0; i < n; i++ {
		c := gitinv.RepoCommitForTest{Files: map[string]string{
			"state.txt":    verdicts[i] + "\n",
			"package.json": packageJSON(i),
			"step.txt":     strconv.Itoa(i) + "\n",
		}}
		if i == 0 {
			c.Files["中文 目录/说明.txt"] = "tracked\n"
			if symlinkTarget {
				c.Symlinks = map[string]string{"notes.txt": "state.txt"}
			} else {
				c.Files["notes.txt"] = "tracked notes\n"
			}
			if filter {
				c.Files[".gitattributes"] = "*.bin filter=lfs diff=lfs merge=lfs -text\n"
			}
		}
		commits = append(commits, c)
	}
	format := pickOf(r, []string{"sha1", "sha1", "sha1", "sha256"})
	name := pickOf(r, []string{"", "", "repo with space", "仓库", "dépôt é"})
	repo, ids := gitinv.BuildRepoForTest(t, format, name, commits)
	ancestor, descendant := ids[prefix], ids[prefix+n-1]
	label := format + " n=" + strconv.Itoa(n) + " " + strings.Join(verdicts, ",")
	switch r.IntN(8) {
	case 0:
		descendant = "HEAD"
	case 1:
		if prefix > 0 {
			ancestor = "HEAD~" + strconv.Itoa(n-1)
		}
	}
	switch r.IntN(30) {
	case 0:
		ancestor, descendant = descendant, ancestor
		label += " swapped"
	case 1:
		ancestor = "-bad"
		label += " unsafe revision"
	case 2:
		descendant = "no-such-revision"
		label += " unknown revision"
	}

	w := pickOf(r, witnesses)
	expected := w.Get("frozenDigest")
	if r.IntN(25) == 0 {
		expected = witnesses[(indexOf(witnesses, w)+1)%len(witnesses)].Get("frozenDigest")
		label += " foreign digest"
	}
	sb := jsjson.NewObj()
	if r.IntN(4) != 0 {
		sb.Set("mode", jsjson.MakeString("DOCKER_ISOLATED"))
	}
	if r.IntN(20) != 0 {
		sb.Set("image", jsjson.MakeString(gitinv.PinnedImageForTest))
	} else {
		label += " no image"
	}
	if r.IntN(4) == 0 {
		sb.Set("environment", jsjson.MustObject("FOO", jsjson.MakeString("bar"), "API_TOKEN", jsjson.MakeString("secret")))
		sb.Set("allowedEnvironment", jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("FOO")}))
	}
	switch r.IntN(16) {
	case 0:
		sb.Set("allowedEnvironment", jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("API_TOKEN")}))
		label += " secret allowlist"
	case 1:
		sb.Set("limits", jsjson.MustObject("timeoutMs", jsjson.MakeNumber(300_001)))
		label += " limit too high"
	}
	if r.IntN(4) == 0 {
		sb.Set("limits", jsjson.MustObject("timeoutMs", jsjson.MakeNumber(60_000), "cpuCount", jsjson.MakeNumber(2)))
	}
	if r.IntN(40) == 0 {
		sb.Set("mode", jsjson.MakeString("UNSAFE_LOCAL"))
		label += " unsafe mode"
	}
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(repo))
	request.Set("range", jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant)))
	request.Set("frozenWitness", w)
	request.Set("expectedFrozenDigest", expected)
	request.Set("sandbox", jsjson.MakeObject(sb))
	switch r.IntN(8) {
	case 0:
		request.Set("maxStates", jsjson.MakeNumber(float64(max(2, n))))
	case 1:
		request.Set("maxStates", jsjson.MakeNumber(float64(max(2, n-1))))
		label += " maxStates=n-1"
	}
	if heterogeneous {
		before, after := packageJSONFingerprint(t, packageJSON(0)), packageJSONFingerprint(t, packageJSON(n-1))
		other := "ghcr.io/faultline/other@sha256:" + strings.Repeat("c", 64)
		switch r.IntN(5) {
		case 0, 1:
			request.Set("runtimeMapping", jsjson.MustObject(after, jsjson.MakeString(other), before, jsjson.MakeString(gitinv.PinnedImageForTest)))
			label += " heterogeneous mapped"
		case 2:
			request.Set("runtimeMapping", jsjson.MustObject(before, jsjson.MakeString(gitinv.PinnedImageForTest)))
			label += " heterogeneous partial"
		case 3:
			request.Set("runtimeMapping", jsjson.MustObject(after, jsjson.MakeString("node:22"), before, jsjson.MakeString(gitinv.PinnedImageForTest)))
			label += " heterogeneous invalid image"
		default:
			label += " heterogeneous unmapped"
		}
	}
	if symlinkTarget {
		label += " symlink"
	}
	if filter {
		label += " filter"
	}
	fake := "pass-fail"
	if r.IntN(8) == 0 {
		fake = pickOf(r, []string{"daemon-down", "compile-error", "forged-pass", "setup-127", "crash", "missing-image"})
	}
	return investigateCase{label: label + " " + fake, request: jsjson.MakeObject(request), fake: fake}
}

// seededEpochMillis is the seeded clock's epoch (difftest/gen/seed.mjs).
const seededEpochMillis = 1784203200000

// unstableInvestigateCase investigates a repository with one state per
// verdict (state.txt holds it) through the broken-state fake Docker, under
// which "broken" fails like a witness that does not parse.
func unstableInvestigateCase(t *testing.T, w jsjson.Value, verdicts ...string) investigateCase {
	var commits []gitinv.RepoCommitForTest
	for i, v := range verdicts {
		commits = append(commits, gitinv.RepoCommitForTest{Files: map[string]string{"state.txt": v + "\n", "step.txt": strconv.Itoa(i) + "\n"}})
	}
	repo, ids := gitinv.BuildRepoForTest(t, "sha1", "", commits)
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(repo))
	request.Set("range", jsjson.MustObject("ancestor", jsjson.MakeString(ids[0]), "descendant", jsjson.MakeString(ids[len(ids)-1])))
	request.Set("frozenWitness", w)
	request.Set("expectedFrozenDigest", w.Get("frozenDigest"))
	request.Set("sandbox", jsjson.MustObject("mode", jsjson.MakeString("DOCKER_ISOLATED"), "image", jsjson.MakeString(gitinv.PinnedImageForTest)))
	return investigateCase{label: "sha1 n=" + strconv.Itoa(len(verdicts)) + " " + strings.Join(verdicts, ",") + " broken-state", request: jsjson.MakeObject(request), fake: "broken-state"}
}

func oracleCall(t *testing.T, c *oracle.Client, op, args string) string {
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func indexOf(values []jsjson.Value, v jsjson.Value) int {
	for i, x := range values {
		if jsjson.Stringify(x) == jsjson.Stringify(v) {
			return i
		}
	}
	return -1
}

// TestLiveInvestigate runs investigateGitRange (native runner) and
// InvestigateGitRange(NativeRunner()) on 3 fixed repositories with an
// unstable state and 40 generated ones with
// the same seed and the same fake Docker behind the docker recorder, and
// compares the whole results (executionNonce, timestamps and durations
// included: both draw them from the seeded sequence) and the recorded
// docker invocations, whose bind-mount paths carry the seeded mkdtemp name.
func TestLiveInvestigate(t *testing.T) {
	root := oracle.RepoRoot(t)
	oracle.Skip(t)
	// The witnesses read the clock, so they are built before the compared sequence.
	clock.ResetForTest("live-investigate-witnesses")
	witnesses := []jsjson.Value{
		gitinv.FrozenWitnessForTest(t),
		gitinv.FrozenWitnessWithOverlaysForTest(t, []string{"notes.txt", "中文 目录/新 文件.txt", "dir with space/é.txt"},
			[]string{"overlay notes\n", "叠加\n", "é\n"}),
	}
	r := rand.New(rand.NewPCG(13, 1))
	// The fixed cases run first: a state the witness cannot run at (fake
	// Docker broken-state) between a PASS and a FAIL state, so the stable
	// states around it are not adjacent and no transition may be claimed
	// (round 10, r10-3 G3); and the same unstable state at either end.
	cases := []investigateCase{
		unstableInvestigateCase(t, witnesses[0], "good", "broken", "bad"),
		unstableInvestigateCase(t, witnesses[1], "good", "broken", "broken", "bad", "good"),
		unstableInvestigateCase(t, witnesses[0], "broken", "good", "bad", "broken"),
	}
	for i := 0; i < 40; i++ {
		cases = append(cases, randomInvestigateCase(t, r, witnesses))
	}

	const seed = "live-investigate"
	c := oracle.StartSeeded(t, seed)
	defer c.Close()
	clock.ResetForTest(seed)
	// tsx reads the seeded Date once while it starts, from an immediate
	// (its disk-cache expiry), so let it run before syncing the Go clock
	// with the oracle's; seededTick's own read is counted.
	oracleCall(t, c, "echo", "null")
	time.Sleep(time.Second)
	tick, err := strconv.ParseInt(oracleCall(t, c, "seededTick", "null"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for range tick + 1 {
		clock.NowMillis()
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int{}
	transitions := 0
	for i, tc := range cases {
		path := strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"), filepath.Join(root, "difftest/testdata/fakedocker", tc.fake),
			filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":")
		tsLog, goLog := filepath.Join(logs, strconv.Itoa(i)+"-ts.log"), filepath.Join(logs, strconv.Itoa(i)+"-go.log")
		// Both clocks must still be in step: each side reads one tick here.
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("case %d: oracle clock at %s, Go clock at %d", i, ts, goTick)
		}
		requestText := jsjson.Stringify(tc.request)
		want := oracleText(t, c, "investigateGitRange", `{"request":`+requestText+`,"path":`+jsjson.Quote(path)+`,"argvLog":`+jsjson.Quote(tsLog)+`}`)
		t.Setenv("PATH", path)
		t.Setenv("FAULTLINE_ARGV_LOG", goLog)
		result, err := gitinv.InvestigateGitRange(tc.request, sandbox.NativeRunner(), false)
		got := ""
		if err != nil {
			got = "THREW:" + err.Error()
		} else {
			got = jsjson.Stringify(result)
		}
		if got != want {
			t.Fatalf("case %d (%s):\ngot  %s\nwant %s", i, tc.label, got, want)
		}
		tsRecords, _ := os.ReadFile(tsLog)
		goRecords, _ := os.ReadFile(goLog)
		if string(tsRecords) != string(goRecords) {
			t.Fatalf("case %d (%s): docker argv differ:\ngo %q\nts %q", i, tc.label, goRecords, tsRecords)
		}
		if err != nil {
			statuses["THREW"]++
			t.Logf("case %d (%s): %s", i, tc.label, got)
			continue
		}
		status := result.Get("status").Str()
		statuses[status]++
		transitions += len(result.Get("transitions").Items())
		t.Logf("case %d (%s): %s, %d runs, %d transitions, proof %v", i, tc.label, status, len(result.Get("runs").Items()),
			len(result.Get("transitions").Items()), result.Get("proof", "isProof").Bool())
		for _, run := range result.Get("runs").Items() {
			if run.Get("executionId").Str() != gitproof.ExpectedExecutionID(run) || run.Get("runId").Str() != gitproof.ExpectedRunID(run) {
				t.Fatalf("case %d: run ids do not recompute: %s", i, jsjson.Stringify(run))
			}
		}
	}
	liveSummary(t, "%d generated repositories compared, statuses %v, %d transitions", len(cases), statuses, transitions)
	for _, status := range []string{"COMPLETED", "RANGE_ERROR", "CONFIGURATION_ERROR", "EXECUTION_ERROR", "INVALID_WITNESS", "SANDBOX_UNAVAILABLE", "THREW"} {
		if statuses[status] == 0 {
			t.Errorf("no case reached %s", status)
		}
	}
}
