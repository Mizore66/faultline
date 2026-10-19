//go:build !windows

package nodefs

import "syscall"

// renameRaw is rename(2) itself: os.Rename rewrites a failed rename onto a
// directory as EEXIST, where libuv reports the kernel's errno.
func renameRaw(from, to string) error {
	return retryEINTR(func() error { return syscall.Rename(from, to) })
}
