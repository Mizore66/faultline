//go:build !windows

package nodefs

import (
	"io/fs"
	"os"
	"syscall"
)

// statRaw is uv_fs_lstat (follow=false) or uv_fs_stat on a syscall path.
func statRaw(raw string, follow bool) (fs.FileInfo, error) {
	if follow {
		return os.Stat(raw)
	}
	return os.Lstat(raw)
}

// exists is uv_fs_access(raw, F_OK): access(2), which libuv retries on
// EINTR. It differs from stat(2) where a filesystem answers the two
// differently, and checks search permission with the real ids.
func exists(raw string) bool {
	return retryEINTR(func() error { return syscall.Access(raw, 0) }) == nil
}
