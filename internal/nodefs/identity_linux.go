package nodefs

import (
	"io/fs"
	"syscall"
)

// StatIdentity is a Stats object's dev and ino as the JS numbers Node gives
// them (uv_stat_t's uint64 fields converted to double), for an info from
// Stat or Lstat. ok is false for any other fs.FileInfo. libuv's statx path
// builds st_dev with glibc's makedev(stx_dev_major, stx_dev_minor).
func StatIdentity(info fs.FileInfo) (dev, ino float64, ok bool) {
	switch s := info.Sys().(type) {
	case *statxBuf:
		major, minor := uint64(s.DevMajor), uint64(s.DevMinor)
		d := (major&0x00000fff)<<8 | (major&0xfffff000)<<32 | minor&0x000000ff | (minor&0xffffff00)<<12
		return float64(d), float64(s.Ino), true
	case *syscall.Stat_t:
		return float64(uint64(s.Dev)), float64(uint64(s.Ino)), true
	}
	return 0, 0, false
}
