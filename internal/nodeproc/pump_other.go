//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package nodeproc

// pumpPolled leaves every stream to pumpEach (Solaris, illumos, AIX: Node's
// official builds do not cover them).
func pumpPolled(streams []pipedStream, _ uintptr) []pipedStream { return streams }
