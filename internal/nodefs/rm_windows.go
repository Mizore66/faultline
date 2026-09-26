package nodefs

import "syscall"

// unlinkRaw follows libuv's fs__unlink: a real directory is refused with
// ERROR_ACCESS_DENIED, a read-only file has the attribute cleared first, and
// a directory symlink or junction is removed as a directory.
func unlinkRaw(raw string) error {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		return err
	}
	const reparse = syscall.FILE_ATTRIBUTE_REPARSE_POINT
	if attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		if attrs&reparse == 0 {
			return syscall.ERROR_ACCESS_DENIED
		}
		return syscall.RemoveDirectory(p)
	}
	if attrs&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		if err := syscall.SetFileAttributes(p, attrs&^syscall.FILE_ATTRIBUTE_READONLY); err != nil {
			return err
		}
	}
	return syscall.DeleteFile(p)
}

func rmdirRaw(raw string) error {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	return syscall.RemoveDirectory(p)
}
