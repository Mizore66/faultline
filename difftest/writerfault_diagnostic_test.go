//go:build faultline_test && linux

package difftest

import "testing"

// When the injected-call counts of the two sides differ, the failure message
// is the only evidence a hosted run leaves. It must keep the original count
// sentence and then both raw, root-masked strace logs, TS first, byte for byte.
// A formatter that drops, reorders, parses or reinterprets the traces (for
// example by treating their text as a format string) hides which tid and which
// syscall number each injection was.
func TestFaultCountMismatchMessageRetainsBothTraces(t *testing.T) {
	t.Run("both traces appear in order, unmodified", func(t *testing.T) {
		const (
			tsTrace = "4101  openat(AT_FDCWD, \"<R>/ws/.faultline/prevention-proofs/.p.tmp\", O_RDONLY|O_CLOEXEC) = -1 EACCES (Permission denied) (INJECTED)\n" +
				"4101  openat(AT_FDCWD, \"<R>/ws/.faultline/prevention-proofs/.p.tmp\", O_RDONLY|O_NONBLOCK|O_CLOEXEC|O_DIRECTORY) = 4\n"
			goTrace = "5202  openat(AT_FDCWD, \"<R>/ws/.faultline/prevention-proofs/.p.tmp\", O_RDONLY|O_CLOEXEC) = -1 EACCES (Permission denied) (INJECTED)\n" +
				"5203  openat(AT_FDCWD, \"<R>/ws/odd %s %d %!(x) 100%% dir/.p.tmp\", O_RDONLY|O_NONBLOCK|O_CLOEXEC|O_DIRECTORY) = -1 EACCES (Permission denied) (INJECTED)\n"
		)
		ts := faultResult{injected: 1, trace: tsTrace}
		goRes := faultResult{injected: 2, trace: goTrace}
		const rel = "ws/.faultline/prevention-proofs/.p.tmp"
		want := "open on " + rel + " injected 1 times in TS, 2 in Go" +
			"\n--- TS\n" + tsTrace +
			"--- Go\n" + goTrace
		if got := faultCountMismatchMessage("open", rel, ts, goRes); got != want {
			t.Fatalf("message differs\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("both labels appear even when the traces are empty", func(t *testing.T) {
		want := "close on ws/x injected 0 times in TS, 3 in Go" + "\n--- TS\n" + "--- Go\n"
		got := faultCountMismatchMessage("close", "ws/x", faultResult{injected: 0}, faultResult{injected: 3})
		if got != want {
			t.Fatalf("message differs\n got: %q\nwant: %q", got, want)
		}
	})
}
