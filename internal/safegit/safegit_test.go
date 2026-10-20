package safegit

import (
	"slices"
	"testing"
)

// HOST_COMMAND_SAFE_GIT_CONFIG has 18 "-c" pairs (36 entries) in the frozen
// TS; TestLiveSafeGitLists compares the lists with dist/doctor.js.
func TestListsExtendHostCommand(t *testing.T) {
	h := HostCommand()
	if len(h) != 36 || h[0] != "-c" || h[1] != "core.hooksPath=/nonexistent/faultline-hooks" || h[35] != "protocol.https.allow=never" {
		t.Fatalf("%q", h)
	}
	if d := Doctor(); len(d) != 38 || !slices.Equal(d[:36], h) || !slices.Equal(d[36:], []string{"-c", "core.autocrlf=false"}) {
		t.Fatalf("%q", d)
	}
	ts := TurnSnapshot()
	if len(ts) != 40 || !slices.Equal(ts[:38], Doctor()) || !slices.Equal(ts[38:], []string{"-c", "core.attributesFile=/nonexistent/faultline-attributes"}) {
		t.Fatalf("%q", ts)
	}
	h[1] = "mutated"
	if HostCommand()[1] == "mutated" {
		t.Fatal("HostCommand must return a fresh slice")
	}
	d := Doctor()
	d[1] = "mutated"
	if Doctor()[1] == "mutated" || TurnSnapshot()[1] == "mutated" {
		t.Fatal("Doctor must return a fresh slice")
	}
}
