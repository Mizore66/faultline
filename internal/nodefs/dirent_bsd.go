//go:build unix && !linux

package nodefs

import (
	"io/fs"
	"syscall"
)

const oDirectory = syscall.O_DIRECTORY

// direntMode is the type libuv's scandir gives an entry where d_type is
// not read here (macOS and the BSDs, whose d_type always names the type):
// lstat's. A failure, a race only, gives no type.
func direntMode(raw string) (fs.FileMode, bool) {
	info, err := statRaw(raw, false)
	if err != nil {
		return 0, false
	}
	return info.Mode(), true
}
