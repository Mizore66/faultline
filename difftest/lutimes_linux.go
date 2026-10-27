//go:build faultline_test

package difftest

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

// lutimes sets a path's atime and mtime to at without following a final
// symbolic link: Node's lutimesSync (utimensat with AT_SYMLINK_NOFOLLOW),
// for the set-mtime mutation (cases.ts applyMutation).
func lutimes(path string, at time.Time) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	ts := [2]syscall.Timespec{syscall.NsecToTimespec(at.UnixNano()), syscall.NsecToTimespec(at.UnixNano())}
	const atFDCWD, atSymlinkNofollow = -100, 0x100
	fd := atFDCWD
	if _, _, errno := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(fd), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&ts)), atSymlinkNofollow, 0, 0); errno != 0 {
		return &os.PathError{Op: "lutimes", Path: path, Err: errno}
	}
	return nil
}
