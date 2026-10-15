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

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/sandbox"
)

// minimizeCase is one generated minimization: the repository's commits (the
// first is `before`, the last is `after` unless overridden), the fake
// Docker and the optional budget.
type minimizeCase struct {
	label          string
	fake           string
	commits        []gitinv.RepoCommitForTest
	before, after  string // "" takes the first and last commit
	budget         int    // 0 keeps the schema default
	expectedDigest string // "" takes the witness's own frozen digest
	notesOverlay   bool   // the witness also overlays notes.txt
}

func lines(count int, bad int) string {
	var b strings.Builder
	for i := 0; i < count; i++ {
		if i == bad {
			b.WriteString("bad\n")
			continue
		}
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

func manyFiles(count int, state string) map[string]string {
	files := map[string]string{"state.txt": state + "\n"}
	for i := 1; i < count; i++ {
		files["file-"+strconv.Itoa(i)+".txt"] = state + " " + strconv.Itoa(i) + "\n"
	}
	return files
}

// minimizeCases are the repositories TestLiveMinimize minimizes: 1 to 12
// changed files, binary content, a rename (add plus delete, since the
// recorder derives units with --no-renames), a mode change, a type change,
// a two-hunk modified file, a non-monotonic interaction (the odd-bad fake
// Docker fails on an odd number of bad state files), and the failure
// statuses of the search.
func minimizeCases() []minimizeCase {
	binaryBefore := string([]byte{0, 1, 2, 3, 4, 5, 6, 7, 0, 10})
	binaryAfter := string([]byte{0, 9, 8, 7, 6, 5, 4, 3, 0, 11})
	return []minimizeCase{
		{label: "one-file", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n"}},
		}},
		{label: "two-files", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "other.txt": "a\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "other.txt": "b\n"}},
		}},
		{label: "non-monotonic", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "state2.txt": "good\n", "state3.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "state2.txt": "bad\n", "state3.txt": "bad\n"}},
		}},
		{label: "binary", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "data.bin": binaryBefore, "notes.txt": "a\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "data.bin": binaryAfter, "notes.txt": "b\n"}},
		}},
		{label: "rename", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "old.txt": "moved\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "new.txt": "moved\n"}, Remove: []string{"old.txt"}},
		}},
		{label: "mode-change", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "script.sh": "#!/bin/sh\necho hi\n"}},
			{Files: map[string]string{"state.txt": "bad\n"}, Modes: map[string]int{"script.sh": 0o755}},
		}},
		// The symlink is removed in its own commit: writing link.txt while
		// it is still a link would follow it into state.txt.
		{label: "typechange", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}, Symlinks: map[string]string{"link.txt": "state.txt"}},
			{Remove: []string{"link.txt"}},
			{Files: map[string]string{"state.txt": "bad\n", "link.txt": "a regular file now\n"}},
		}},
		{label: "hunks", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": lines(24, -1)}},
			{Files: map[string]string{"state.txt": strings.Replace(lines(24, 2), "line 20", "tail 20", 1)}},
		}},
		{label: "eleven-files", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: manyFiles(11, "good")},
			{Files: manyFiles(11, "bad")},
		}},
		{label: "twelve-files", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: manyFiles(12, "good")},
			{Files: manyFiles(12, "bad")},
		}},
		{label: "budget", fake: "odd-bad", budget: 5, commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n", "state2.txt": "good\n", "state3.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "state2.txt": "bad\n", "state3.txt": "bad\n"}},
		}},
		{label: "baseline-not-pass", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "bad\n"}},
			{Files: map[string]string{"state.txt": "bad\n", "other.txt": "b\n"}},
		}},
		{label: "full-patch-not-fail", fake: "odd-bad", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "good\n", "other.txt": "b\n"}},
		}},
		{label: "no-patches", fake: "odd-bad", before: "HEAD", after: "HEAD", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n"}},
		}},
		{label: "daemon-down", fake: "daemon-down", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n"}},
		}},
		// notes.txt is a tracked symlink in the base commit, so materializing
		// the witness's notes.txt overlay fails: an unresolved run with [] overlays.
		{label: "overlay-symlink-target", fake: "odd-bad", notesOverlay: true, commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}, Symlinks: map[string]string{"notes.txt": "state.txt"}},
			{Files: map[string]string{"state.txt": "bad\n"}},
		}},
		{label: "bad-range", fake: "odd-bad", before: "no-such-revision", commits: []gitinv.RepoCommitForTest{
			{Files: map[string]string{"state.txt": "good\n"}},
			{Files: map[string]string{"state.txt": "bad\n"}},
		}},
		{label: "invalid-witness", fake: "odd-bad", expectedDigest: "sha256:" + strings.Repeat("0", 64),
			commits: []gitinv.RepoCommitForTest{
				{Files: map[string]string{"state.txt": "good\n"}},
				{Files: map[string]string{"state.txt": "bad\n"}},
			}},
	}
}

// liveMinimizeWitnesses are the plain witness and one that also overlays
// notes.txt; they read the clock, so build them before a compared sequence.
func liveMinimizeWitnesses(t *testing.T) [2]jsjson.Value {
	return [2]jsjson.Value{gitinv.FrozenWitnessForTest(t),
		gitinv.FrozenWitnessWithOverlaysForTest(t, []string{"notes.txt"}, []string{"overlay notes\n"})}
}

// minimizeRequest builds a case's repository and its request.
func minimizeRequest(t *testing.T, c minimizeCase, witnesses [2]jsjson.Value) jsjson.Value {
	t.Helper()
	frozen := witnesses[0]
	if c.notesOverlay {
		frozen = witnesses[1]
	}
	repo, ids := gitinv.BuildRepoForTest(t, "sha1", "", c.commits)
	before, after := ids[0], ids[len(ids)-1]
	if c.before != "" {
		before = c.before
	}
	if c.after != "" {
		after = c.after
	}
	expected := frozen.Get("frozenDigest")
	if c.expectedDigest != "" {
		expected = jsjson.MakeString(c.expectedDigest)
	}
	request := jsjson.NewObj()
	request.Set("repository", jsjson.MakeString(repo))
	request.Set("before", jsjson.MakeString(before))
	request.Set("after", jsjson.MakeString(after))
	request.Set("frozenWitness", frozen)
	request.Set("expectedFrozenDigest", expected)
	request.Set("sandbox", jsjson.MustObject("mode", jsjson.MakeString("DOCKER_ISOLATED"),
		"image", jsjson.MakeString(gitinv.PinnedImageForTest)))
	if c.budget > 0 {
		request.Set("budget", jsjson.MustObject("maxExecutions", jsjson.MakeNumber(float64(c.budget))))
	}
	return jsjson.MakeObject(request)
}

// TestLiveMinimize runs minimizeGitDiff (native runner) and
// MinimizeGitDiff(NativeRunner()) on every generated repository with the
// same seed and the same fake Docker behind the docker recorder, and
// compares the whole results (execution nonces, timestamps and durations
// included: both draw them from the seeded sequence) and the recorded git
// and docker invocations.
func TestLiveMinimize(t *testing.T) {
	root := oracle.RepoRoot(t)
	oracle.Skip(t)
	// The witness reads the clock, so it is built before the compared sequence.
	clock.ResetForTest("live-minimize-witness")
	frozen := liveMinimizeWitnesses(t)
	cases := minimizeCases()
	requests := make([]jsjson.Value, len(cases))
	for i, c := range cases {
		requests[i] = minimizeRequest(t, c, frozen)
	}

	const seed = "live-minimize"
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
	for i, tc := range cases {
		path := strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"), filepath.Join(root, "difftest/testdata/fakedocker", tc.fake),
			filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":")
		tsLog, goLog := filepath.Join(logs, strconv.Itoa(i)+"-ts.log"), filepath.Join(logs, strconv.Itoa(i)+"-go.log")
		// Both clocks must still be in step: each side reads one tick here.
		if ts, goTick := oracleCall(t, c, "seededTick", "null"), clock.NowMillis()-seededEpochMillis; ts != strconv.FormatInt(goTick, 10) {
			t.Fatalf("case %d: oracle clock at %s, Go clock at %d", i, ts, goTick)
		}
		requestText := jsjson.Stringify(requests[i])
		want := oracleText(t, c, "minimizeGitDiff", `{"request":`+requestText+`,"path":`+jsjson.Quote(path)+`,"argvLog":`+jsjson.Quote(tsLog)+`}`)
		t.Setenv("PATH", path)
		t.Setenv("FAULTLINE_ARGV_LOG", goLog)
		result, err := minimize.MinimizeGitDiff(requests[i], sandbox.NativeRunner(), false)
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
			t.Fatalf("case %d (%s): git/docker argv differ:\ngo %q\nts %q", i, tc.label, goRecords, tsRecords)
		}
		if err != nil {
			statuses["THREW"]++
			continue
		}
		status := result.Get("status").Str()
		statuses[status]++
		hunkRefined := false
		for _, attempt := range result.Get("attempts").Items() {
			if attempt.Get("phase").Str() == "HUNK_ONE_MINIMAL" {
				hunkRefined = true
			}
		}
		if tc.label == "hunks" && !hunkRefined {
			t.Errorf("case %d (%s): no hunk refinement ran", i, tc.label)
		}
		if tc.label == "non-monotonic" && result.Get("minimality", "nonMonotonicInteraction", "verdict").Str() != "NON_MONOTONIC_INTERACTION" {
			t.Errorf("case %d (%s): %s", i, tc.label, jsjson.Stringify(result.Get("minimality", "nonMonotonicInteraction")))
		}
		t.Logf("case %d (%s): %s, %d units, %d runs, candidate %d, proof %v, hunks %v", i, tc.label, status,
			len(result.Get("patchUnits").Items()), len(result.Get("runs").Items()),
			len(result.Get("candidateUnitIds").Items()), result.Get("proof", "isProof").Bool(), hunkRefined)
		// Every recorded result verifies offline, in Go and in frozen TS.
		verification := minimize.Verify(result, "", false)
		wantVerification := oracleText(t, c, "verifyGitMinimizationResult", `{"text":`+jsjson.Quote(got)+`}`)
		if canonicalOf(verification.JSON()) != wantVerification {
			t.Fatalf("case %d (%s) verification:\ngot  %s\nwant %s", i, tc.label, canonicalOf(verification.JSON()), wantVerification)
		}
	}
	liveSummary(t, "%d cases compared, statuses %v", len(cases), statuses)
	for _, status := range []string{"COMPLETED", "EXECUTION_ERROR", "BUDGET_EXHAUSTED", "BASELINE_NOT_PASS", "FULL_PATCH_NOT_FAIL", "NO_PATCHES",
		"SANDBOX_UNAVAILABLE", "RANGE_ERROR", "INVALID_WITNESS"} {
		if statuses[status] == 0 {
			t.Errorf("no case reached %s", status)
		}
	}
}

// minimizationSeeds are complete minimization records (one per repository),
// recorded with the fake Docker behind the docker recorder.
func minimizationSeeds(t *testing.T) []string {
	t.Helper()
	root := oracle.RepoRoot(t)
	clock.ResetForTest("live-minimization-verify-seeds")
	frozen := liveMinimizeWitnesses(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	var seeds []string
	for i, c := range minimizeCases() {
		if i >= 10 {
			break
		}
		request := minimizeRequest(t, c, frozen)
		// Built from a fixed list: a PATH that names the recorder twice
		// would make it exec itself (it skips only its first occurrence).
		t.Setenv("PATH", strings.Join([]string{filepath.Join(root, "difftest/testdata/recorder"),
			filepath.Join(root, "difftest/testdata/fakedocker", c.fake), filepath.Dir(gitPath), "/usr/bin", "/bin"}, ":"))
		t.Setenv("FAULTLINE_ARGV_LOG", filepath.Join(t.TempDir(), "argv.log"))
		result, err := minimize.MinimizeGitDiff(request, sandbox.NativeRunner(), false)
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, jsjson.Stringify(result))
	}
	return seeds
}

// TestLiveMinimizationVerify compares verifyGitMinimizationResult with
// minimize.Verify over recorded minimization records and deeply corrupted
// copies of them, with and without an external digest.
func TestLiveMinimizationVerify(t *testing.T) {
	oracle.Skip(t)
	// The oracle starts first: recording the seeds replaces PATH, and pnpm
	// would no longer be on it.
	c := oracle.Start(t)
	defer c.Close()
	seeds := minimizationSeeds(t)
	r := rand.New(rand.NewPCG(31, 7))
	seen := map[string]bool{}
	valid := 0
	var outputs strings.Builder
	targeted := targetedMinimizationEdits(t, seeds)
	for i := 0; i < len(targeted)+6000; i++ {
		var text string
		if i < len(targeted) {
			text = targeted[i]
		} else if text = seeds[r.IntN(len(seeds))]; i-len(targeted) >= len(seeds) {
			// Schema-breaking corruption, and kind-preserving edits that
			// keep the schema and reach the cross-field checks.
			switch r.IntN(3) {
			case 0:
				text = gen.CorruptDeep(r, text)
			case 1:
				for n := 1 + r.IntN(2); n > 0; n-- {
					text = gen.CorruptLeaf(r, text)
				}
			default:
				for n := 1 + r.IntN(2); n > 0; n-- {
					text = gen.SwapLeaf(r, text)
				}
			}
		}
		value, err := jsjson.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		expected, provided := "", false
		switch r.IntN(4) {
		case 1:
			expected, provided = minimize.Verify(value, "", false).ExternalDigestStatus, true // not a digest
		case 2:
			expected, provided = "sha256:"+strings.Repeat("0", 64), true
		case 3:
			if digest := minimize.Verify(value, "", false).ResultDigest; digest != nil {
				expected, provided = *digest, true
			}
		}
		args := `{"text":` + jsjson.Quote(text)
		if provided {
			args += `,"expected":` + jsjson.Quote(expected)
		}
		args += "}"
		if seen[args] {
			continue
		}
		seen[args] = true
		got := canonicalOf(minimize.Verify(value, expected, provided).JSON())
		if want := oracleText(t, c, "verifyGitMinimizationResult", args); got != want {
			t.Fatalf("case %d:\ngot  %s\nwant %s", i, got, want)
		}
		outputs.WriteString(got) // equal to TS's result
		if minimize.Verify(value, expected, provided).Valid {
			valid++
		}
	}
	liveSummary(t, "%d distinct records compared (%d targeted edits, %d valid)", len(seen), len(targeted), valid)
	if valid == 0 {
		t.Error("no generated record was valid")
	}
	requireReached(t, "internal/minimize/verify.go", outputs.String())
}

// targetedMinimizationEdits applies, to every seed record where it fits, one
// edit aimed at each cross-field check the random corruption rarely reaches
// (a certificate whose ids disagree, a run whose sandbox and result
// disagree, an attempt without a run, interaction evidence, the status of a
// proof).
func targetedMinimizationEdits(t *testing.T, seeds []string) []string {
	t.Helper()
	at := func(v jsjson.Value, path ...any) jsjson.Value {
		for _, p := range path {
			switch p := p.(type) {
			case string:
				v = v.Get(p)
			case int:
				if v.Kind() != jsjson.Array || p >= len(v.Items()) {
					return jsjson.Value{}
				}
				v = v.Items()[p]
			}
		}
		return v
	}
	str := jsjson.MakeString
	var evidence jsjson.Value // a seed's non-monotonic interaction evidence
	for _, seed := range seeds {
		v, _ := jsjson.Parse(seed)
		if e := at(v, "minimality", "nonMonotonicInteraction", "evidence"); e.Kind() == jsjson.Object {
			evidence = e
		}
	}
	edits := []func(v jsjson.Value) bool{
		func(v jsjson.Value) bool { // run and execution id counts differ
			ids := at(v, "certification", "sufficiency", "runIds")
			if len(ids.Items()) == 0 {
				return false
			}
			v.Get("certification", "sufficiency").Obj().Set("runIds", jsjson.MakeArray(ids.Items()[:len(ids.Items())-1]))
			return true
		},
		func(v jsjson.Value) bool { // NOT_RUN with execution references
			if len(at(v, "certification", "sufficiency", "runIds").Items()) == 0 {
				return false
			}
			v.Get("certification", "sufficiency").Obj().Set("status", str("NOT_RUN"))
			return true
		},
		func(v jsjson.Value) bool { // a repeated run id
			ids := at(v, "certification", "necessity", "runIds").Items()
			if len(ids) < 2 {
				return false
			}
			ids[1] = ids[0]
			return true
		},
		func(v jsjson.Value) bool { // complete evidence but not CERTIFIED
			if at(v, "certification", "necessity", "status").Str() != "CERTIFIED" {
				return false
			}
			v.Get("certification", "necessity").Obj().Set("status", str("NOT_CERTIFIED"))
			return true
		},
		func(v jsjson.Value) bool { // a result without its sandbox
			run := at(v, "runs", 0)
			if run.Kind() != jsjson.Object || run.Get("result").Kind() != jsjson.Object {
				return false
			}
			run.Obj().Set("sandbox", jsjson.MakeNull())
			return true
		},
		func(v jsjson.Value) bool { // no result, yet resolved
			run := at(v, "runs", 0)
			if run.Kind() != jsjson.Object || run.Get("outcome").Str() == "UNRESOLVED" {
				return false
			}
			run.Obj().Set("sandbox", jsjson.MakeNull())
			run.Obj().Set("result", jsjson.MakeNull())
			return true
		},
		func(v jsjson.Value) bool { // sandbox kind against result kind
			sandbox := at(v, "runs", 0, "sandbox")
			if sandbox.Kind() != jsjson.Object {
				return false
			}
			sandbox.Obj().Set("kind", str("UNSAFE_LOCAL"))
			return true
		},
		func(v jsjson.Value) bool { // unsafe-local executor, Docker result
			result := at(v, "runs", 0, "result")
			if result.Kind() != jsjson.Object {
				return false
			}
			result.Obj().Set("executor", str("UNSAFE_LOCAL"))
			return true
		},
		func(v jsjson.Value) bool { // Docker executor, unsafe-local result
			result := at(v, "runs", 0, "result")
			if result.Kind() != jsjson.Object {
				return false
			}
			result.Obj().Set("kind", str("UNSAFE_LOCAL"))
			return true
		},
		func(v jsjson.Value) bool { // an attempt that ran loses its run
			attempt := at(v, "attempts", 0)
			if attempt.Kind() != jsjson.Object || attempt.Get("runId").Kind() != jsjson.String {
				return false
			}
			attempt.Obj().Set("runId", jsjson.MakeNull())
			return true
		},
		func(v jsjson.Value) bool { // a non-monotonic verdict without evidence
			interaction := at(v, "minimality", "nonMonotonicInteraction")
			if interaction.Kind() != jsjson.Object || interaction.Get("evidence").Kind() != jsjson.Null {
				return false
			}
			interaction.Obj().Set("verdict", str("NON_MONOTONIC_INTERACTION"))
			return true
		},
		func(v jsjson.Value) bool { // evidence under another verdict
			interaction := at(v, "minimality", "nonMonotonicInteraction")
			if interaction.Kind() != jsjson.Object || evidence.Kind() != jsjson.Object ||
				interaction.Get("verdict").Str() == "NON_MONOTONIC_INTERACTION" {
				return false
			}
			copied, _ := jsjson.Parse(jsjson.Stringify(evidence))
			interaction.Obj().Set("evidence", copied)
			return true
		},
		func(v jsjson.Value) bool { // proof evidence under another status
			if !at(v, "proof", "isProof").Bool() {
				return false
			}
			v.Obj().Set("status", str("BUDGET_EXHAUSTED"))
			return true
		},
	}
	var out []string
	for i, edit := range edits {
		applied := false
		for _, seed := range seeds {
			v, err := jsjson.Parse(seed)
			if err != nil {
				t.Fatal(err)
			}
			if edit(v) {
				out = append(out, jsjson.Stringify(v))
				applied = true
			}
		}
		if !applied {
			t.Errorf("targeted edit %d fits no seed", i)
		}
	}
	return out
}
