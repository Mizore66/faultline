//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package nodeproc

// pumpPolled leaves every stream to pumpEach. On Windows libuv reads pipes
// with overlapped I/O and runs the callbacks in completion order, which
// the per-stream readers approximate.
func pumpPolled(streams []pipedStream) []pipedStream { return streams }
