//go:build !windows

package nodefs

import (
	"os"
	"syscall"
)

// readRaw is one read(2) on f, as libuv's uv__fs_read makes it: an EINTR is
// returned, not retried (Go's File.Read would retry it).
func readRaw(f *os.File, b []byte) (int, error) {
	n, err := syscall.Read(int(f.Fd()), b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// checkReadable is nothing on Unix: a directory fails with EISDIR from
// read(2) itself, and where the kernel lets a directory be read (macOS
// devfs) Node reads it too.
func checkReadable(*os.File) error { return nil }
