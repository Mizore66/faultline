//go:build faultline_test && windows

package difftest

import (
	"os"
	"syscall"
	"time"
)

// lutimes sets a path's atime and mtime to at without following a final
// symbolic link: Node's lutimesSync. libuv 1.51 src/win/fs.c fs__lutime
// (fs__utime_impl_from_path with do_lutime) opens the path with
// CreateFileW(FILE_WRITE_ATTRIBUTES, share read|write|delete, OPEN_EXISTING,
// FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT), so a link's own
// times are set, and then SetFileTime(handle, NULL, atime, mtime). A path
// that is not a link is set the same way.
func lutimes(path string, at time.Time) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	const fileWriteAttributes = 0x100
	handle, err := syscall.CreateFile(name, fileWriteAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "lutimes", Path: path, Err: err}
	}
	defer syscall.CloseHandle(handle)
	ft := syscall.NsecToFiletime(at.UnixNano())
	if err := syscall.SetFileTime(handle, nil, &ft, &ft); err != nil {
		return &os.PathError{Op: "lutimes", Path: path, Err: err}
	}
	return nil
}
