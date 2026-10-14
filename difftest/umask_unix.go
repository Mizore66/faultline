//go:build unix

package difftest

import "syscall"

// setWriterUmask pins the umask the writer replay and the fl processes it
// starts create files under; gen-writers.ts pins the same one.
func setWriterUmask() { syscall.Umask(0o022) }
