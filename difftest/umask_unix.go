//go:build unix

package difftest

import (
	"syscall"
	"testing"
)

// setWriterUmask sets umask 0 for the rest of t, restoring the old one when
// t ends: fl inherits it, so the mode fl asks for is the mode on disk and a
// 0o666 does not collapse into a 0o644 (gen-writers.ts sets the same). The
// umask is process-wide, which is safe here: TestWriterReplay and its
// subtests are not parallel, and parallel top-level tests only start after
// every sequential one has finished. Setting it around each child instead
// would need the same process-wide call (Go has no per-child umask).
func setWriterUmask(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
}
