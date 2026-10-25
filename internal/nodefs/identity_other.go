//go:build !windows && !linux

package nodefs

import (
	"io/fs"
	"syscall"
)

// StatIdentity is a Stats object's dev and ino as the JS numbers Node gives
// them (uv_stat_t's uint64 fields, assigned from the platform's dev_t and
// ino_t, converted to double), for an info from Stat or Lstat. ok is false
// for any other fs.FileInfo.
func StatIdentity(info fs.FileInfo) (dev, ino float64, ok bool) {
	if s, isStat := info.Sys().(*syscall.Stat_t); isStat {
		return float64(uint64(int64(s.Dev))), float64(uint64(s.Ino)), true
	}
	return 0, 0, false
}
