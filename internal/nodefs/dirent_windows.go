package nodefs

import (
	"io/fs"
	"syscall"
)

const oDirectory = 0

// direntMode is the type libuv's fs__scandir gives an entry from its
// FILE_DIRECTORY_INFORMATION attributes: any reparse point is a link,
// then a directory, else a file. The attributes are read again here
// (GetFileAttributesW); a failure, a race only, gives no type.
func direntMode(raw string) (fs.FileMode, bool) {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return 0, false
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		return 0, false
	}
	switch {
	case attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return fs.ModeSymlink, true
	case attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0:
		return fs.ModeDir, true
	}
	return 0, true
}
