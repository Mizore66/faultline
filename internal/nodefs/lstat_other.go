//go:build !windows

package nodefs

import (
	"io/fs"
	"os"
)

// statRaw is uv_fs_lstat (follow=false) or uv_fs_stat on a syscall path.
func statRaw(raw string, follow bool) (fs.FileInfo, error) {
	if follow {
		return os.Stat(raw)
	}
	return os.Lstat(raw)
}
