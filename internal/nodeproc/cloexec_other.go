//go:build unix && !darwin

package nodeproc

// closeInheritedFds is a no-op: on Linux, libuv's fork+exec passes every
// descriptor without FD_CLOEXEC, and the ones Node inherited were marked at
// startup (uv_disable_stdio_inheritance, done by internal/boot).
func closeInheritedFds() {}
