//go:build unix && !linux

package nodefs

import (
	"io/fs"
	"syscall"
)

const oDirectory = syscall.O_DIRECTORY

// direntTypeOf is the d_type of an entry os.File.ReadDir read: its
// FileMode type, which on darwin and the BSDs comes from the record's
// d_type (os lstats only an entry whose d_type is DT_UNKNOWN, where libuv
// would report UV_DIRENT_UNKNOWN and Node would lstat it by its decoded
// name; KNOWN_DIFFERENCES: neither APFS nor UFS nor ZFS reports
// DT_UNKNOWN).
func direntTypeOf(e fs.DirEntry) uint8 {
	switch t := e.Type(); {
	case t&fs.ModeSymlink != 0:
		return dtLNK
	case t.IsDir():
		return dtDIR
	case t&fs.ModeNamedPipe != 0:
		return dtFIFO
	case t&fs.ModeSocket != 0:
		return dtSOCK
	case t&fs.ModeCharDevice != 0:
		return dtCHR
	case t&fs.ModeDevice != 0:
		return dtBLK
	case t.IsRegular():
		return dtREG
	}
	return 0
}
