package cli

import "testing"

func TestProveUsage(t *testing.T) {
	for _, args := range [][]string{{"prove"}, {"prove", "nope"}, {"prove", "transition"}, {"prove", "transition", ""}} {
		_, stderr, code := run(args...)
		if code != 1 || stderr != "FaultLine error: "+proveUsage+"\nNext: pnpm fl help\n" {
			t.Errorf("%q: %d %q", args, code, stderr)
		}
	}
}

func TestProveMissingDirectory(t *testing.T) {
	_, stderr, code := run("prove", "transition", t.TempDir()+"/nowhere")
	if code != 1 || stderr != "FaultLine error: Turn proof bundle failed verification: Turn proof bundle directory does not exist\nNext: pnpm fl help\n" {
		t.Fatalf("%d %q", code, stderr)
	}
}
