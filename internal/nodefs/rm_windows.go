package nodefs

import (
	"syscall"
	"unsafe"
)

var procSetFileInformationByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("SetFileInformationByHandle")

const (
	fileWriteAttributes      = 0x100
	accessDelete             = 0x10000
	fileBasicInfoClass       = 0
	fileDispositionInfoClass = 4
	fileDispositionInfoEx    = 21

	fileDispositionDelete         = 0x1
	fileDispositionPosixSemantics = 0x2
	fileDispositionIgnoreReadonly = 0x10
	errInvalidFunction            = syscall.Errno(1)
	errNotSupported               = syscall.Errno(50)
	errInvalidParameter           = syscall.Errno(87)
	fileAttributeArchive          = 0x20
)

func setFileInformation(h syscall.Handle, class uint32, buf unsafe.Pointer, size uintptr) error {
	r, _, err := procSetFileInformationByHandle.Call(uintptr(h), uintptr(class), uintptr(buf), size)
	if r == 0 {
		return err
	}
	return nil
}

func unlinkRaw(raw string) error { return unlinkRmdir(raw, false) }
func rmdirRaw(raw string) error  { return unlinkRmdir(raw, true) }

// unlinkRmdir is libuv's fs__unlink_rmdir (1.51): open the entry itself
// with DELETE access, refuse a non-directory for rmdir (ENOENT) and a real
// directory or a reparse point libuv does not read as a link for unlink
// (EPERM), then delete with POSIX semantics, ignoring the read-only
// attribute, and fall back to a legacy delete only where POSIX deletion is
// unsupported.
func unlinkRmdir(raw string, isRmdir bool) error {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, fileReadAttributes|fileWriteAttributes|accessDelete,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	attrs := info.FileAttributes
	isDir := attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0
	if isRmdir && !isDir {
		return syscall.ERROR_FILE_NOT_FOUND // UV_ENOENT (ERROR_DIRECTORY)
	}
	if !isRmdir && isDir {
		if attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			return syscall.ERROR_ACCESS_DENIED
		}
		if err := readlinkHandle(h); err != nil {
			if err == errSymlinkNotSupport {
				return syscall.ERROR_ACCESS_DENIED
			}
			return err
		}
	}
	flags := uint32(fileDispositionDelete | fileDispositionPosixSemantics | fileDispositionIgnoreReadonly)
	err = setFileInformation(h, fileDispositionInfoEx, unsafe.Pointer(&flags), unsafe.Sizeof(flags))
	if err == nil {
		return nil
	}
	if err != errNotSupported && err != errInvalidParameter && err != errInvalidFunction {
		return err
	}
	if attrs&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		basic := struct {
			creation, access, write, change int64
			attributes                      uint32
			_                               uint32
		}{attributes: attrs&^syscall.FILE_ATTRIBUTE_READONLY | fileAttributeArchive}
		if err := setFileInformation(h, fileBasicInfoClass, unsafe.Pointer(&basic), unsafe.Sizeof(basic)); err != nil {
			return err
		}
	}
	del := uint32(1) // FILE_DISPOSITION_INFO.DeleteFile (a BOOLEAN, padded)
	return setFileInformation(h, fileDispositionInfoClass, unsafe.Pointer(&del), unsafe.Sizeof(del))
}
