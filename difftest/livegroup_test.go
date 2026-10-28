//go:build faultline_test

package difftest

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// These are unit tests of the group state, named so that CI's
// `-list TestLive` does not pick them up as live tests.

// isolateLiveGroups swaps in empty group state and restores the real
// accumulated state afterwards, so no earlier group is wiped.
func isolateLiveGroups(t *testing.T) {
	t.Helper()
	liveGroupState.Lock()
	saved := struct {
		outputs map[string]string
		done    map[string]map[string]bool
		checked map[string]bool
	}{liveGroupState.outputs, liveGroupState.done, liveGroupState.checked}
	liveGroupState.outputs, liveGroupState.done, liveGroupState.checked = map[string]string{}, map[string]map[string]bool{}, map[string]bool{}
	liveGroupState.Unlock()
	t.Cleanup(func() {
		liveGroupState.Lock()
		liveGroupState.outputs, liveGroupState.done, liveGroupState.checked = saved.outputs, saved.done, saved.checked
		liveGroupState.Unlock()
	})
}

func groupReport() string {
	var b bytes.Buffer
	reportUnverifiedGroups(&b)
	return b.String()
}

// An intermediate partial state followed by completion leaves no terminal
// failure; the completing call gets every test's outputs.
func TestGroupStatePartialThenComplete(t *testing.T) {
	isolateLiveGroups(t)
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
func TestGroupStatePartialOrNarrowedIsReported(t *testing.T) {
	isolateLiveGroups(t)
	noteGroup("partial.go", "TestA", false, "x", 2)
	noteGroup("narrow.go", "TestA", true, "x", 1)
	got := groupReport()
	if !strings.Contains(got, "LIVE-GROUP-UNVERIFIED partial.go") || !strings.Contains(got, "LIVE-GROUP-UNVERIFIED narrow.go") {
		t.Fatalf("report: %q", got)
	}
}

const groupChildEnv = "FAULTLINE_GROUP_WRAPPER_CHILD"

// groupChildFile is gated, in the child only, by the child test itself; its
// error literal "attestation receipt does not exist" is in no golden and not
// waived, so only a real requireReached call can report it unreached.
const groupChildFile = "internal/attest/attest.go"

// TestGroupWrapperChild is the child of TestGroupWrapperRunsTheReachCheck.
// Run by itself it does nothing.
func TestGroupWrapperChild(t *testing.T) {
	if os.Getenv(groupChildEnv) != "1" {
		return
	}
	old, had := liveGatedFiles[groupChildFile]
	liveGatedFiles[groupChildFile] = "TestGroupWrapperChild"
	defer func() {
		if had {
			liveGatedFiles[groupChildFile] = old
		} else {
			delete(liveGatedFiles, groupChildFile)
		}
	}()
	isolateLiveGroups(t)
	// A one-test group, no compared outputs: complete and unnarrowed, so the
	// wrapper must call requireReached, which fails on every unreached literal.
	requireReachedGroup(t, groupChildFile)
}

// The actual wrapper performs the reach check on a completed unnarrowed
// group: the child's failure must be requireReached's "no generated input
// reached" for a specific literal, not a setup error.
func TestGroupWrapperRunsTheReachCheck(t *testing.T) {
	if os.Getenv(groupChildEnv) == "1" {
		return // inside the child process: only the child test runs there
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestGroupWrapperChild$", "-test.count=1", "-test.v")
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "FAULTLINE_REPAIR_ONLY", "FAULTLINE_REPAIR_SKIP", "FAULTLINE_REPAIR_WT_CASES", "FAULTLINE_DUMP_LIVE_OUTPUTS", "FAULTLINE_COVERAGE_GATE", "GOCOVERDIR", groupChildEnv:
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, groupChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err == nil {
		t.Fatalf("the child passed, so the wrapper never ran the reach check:\n%s", text)
	}
	for _, want := range []string{"--- FAIL: TestGroupWrapperChild", "no generated input reached \"attestation receipt does not exist\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("child output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "LIVE-GROUP-PENDING") || strings.Contains(text, "is not in liveGatedFiles") || strings.Contains(text, "names \"") {
		t.Fatalf("the child failed for a setup reason:\n%s", text)
	}
}
