//go:build faultline_test

package difftest

import (
	"os"
	"syscall"
	"time"
	_ "unsafe" // go:linkname
)

// utimensat is syscall's own (syscall_darwin.go marks it for linkname).
//
//go:linkname utimensat syscall.utimensat
func utimensat(dirfd int, path string, times *[2]syscall.Timespec, flags int) error

// lutimes sets a path's atime and mtime to at without following a final
// symbolic link: Node's lutimesSync (utimensat with AT_SYMLINK_NOFOLLOW),
// for the set-mtime mutation (cases.ts applyMutation).
func lutimes(path string, at time.Time) error {
	ts := [2]syscall.Timespec{syscall.NsecToTimespec(at.UnixNano()), syscall.NsecToTimespec(at.UnixNano())}
	const atFDCWD, atSymlinkNofollow = -2, 0x20
	if err := utimensat(atFDCWD, path, &ts, atSymlinkNofollow); err != nil {
		return &os.PathError{Op: "lutimes", Path: path, Err: err}
	}
	return nil
}
