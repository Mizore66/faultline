//go:build unix && !darwin

package nodeproc

// closeInheritedFds is a no-op: libuv's fork+exec on Linux leaves inherited
// descriptors open in the child, as Go does.
func closeInheritedFds() {}
