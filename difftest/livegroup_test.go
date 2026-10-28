//go:build faultline_test

package difftest

import (
	"bytes"
	"strings"
	"testing"
)

func resetLiveGroups() {
	liveGroupState.Lock()
	liveGroupState.outputs, liveGroupState.done, liveGroupState.checked = map[string]string{}, map[string]map[string]bool{}, map[string]bool{}
	liveGroupState.Unlock()
}

func groupReport() string {
	var b bytes.Buffer
	reportUnverifiedGroups(&b)
	return b.String()
}

// An intermediate partial state followed by completion leaves no terminal
// failure; the completing call gets every test's outputs (so the reach check
// has real input).
func TestLiveGroupPartialThenComplete(t *testing.T) {
	resetLiveGroups()
	defer resetLiveGroups()
	if complete, _ := noteGroup("f.go", "TestA", false, "out-a", 2); complete {
		t.Fatal("one of two is not complete")
	}
	complete, outputs := noteGroup("f.go", "TestB", false, "out-b", 2)
	if !complete || !strings.Contains(outputs, "out-a") || !strings.Contains(outputs, "out-b") {
		t.Fatalf("complete=%v outputs=%q", complete, outputs)
	}
	if got := groupReport(); got != "" {
		t.Fatalf("completed group reported: %q", got)
	}
}

// A partial-only final state and a narrowed group are reported, even a
// narrowed group that saw every test.
func TestLiveGroupPartialOrNarrowedIsReported(t *testing.T) {
	resetLiveGroups()
	defer resetLiveGroups()
	noteGroup("partial.go", "TestA", false, "x", 2)
	noteGroup("narrow.go", "TestA", true, "x", 1)
	got := groupReport()
	if !strings.Contains(got, "LIVE-GROUP-UNVERIFIED partial.go") || !strings.Contains(got, "LIVE-GROUP-UNVERIFIED narrow.go") {
		t.Fatalf("report: %q", got)
	}
}

// A completed unnarrowed group is checked for real: with no compared
// outputs the attest literals stay unreached, so the check has teeth
// (requireReached fails a live test on exactly this).
func TestLiveGroupCompletedRunsTheReachCheck(t *testing.T) {
	resetLiveGroups()
	defer resetLiveGroups()
	file := "internal/attest/attest.go"
	noteGroup(file, "TestLiveAttestationCreate", false, "", 2)
	complete, outputs := noteGroup(file, "TestLiveAttestationStore", false, "", 2)
	if !complete {
		t.Fatal("not complete")
	}
	if missing := unreachedLiterals(t, file, outputs); len(missing) == 0 {
		t.Fatal("empty outputs must leave attest literals unreached")
	}
}
