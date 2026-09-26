package nodefs

import (
	"encoding/binary"
	"io/fs"
	"os"
	"syscall"
)

const (
	fsctlGetReparsePoint  = 0x000900A8
	tagSymlink            = 0xA000000C
	tagMountPoint         = 0xA0000003
	tagAppExecLink        = 0x8000001B
	tagLxSymlink          = 0xA000001D
	errSymlinkNotSupport  = syscall.Errno(1464) // ERROR_SYMLINK_NOT_SUPPORTED
	errNotAReparsePoint   = syscall.Errno(4390) // ERROR_NOT_A_REPARSE_POINT
	maxReparseDataBufSize = 16 * 1024
)

// lstatReparse classifies a reparse point the way libuv's lstat does
// (fs__stat_handle with fs__readlink_handle, libuv 1.52): SYMLINK and
// LX_SYMLINK tags, junctions whose target is a drive path (\??\X:\), and
// AppExecLinks with an absolute third string are symbolic links. For any
// other reparse point libuv retries with a following stat and takes the
// directory bit from the target's attributes.
func lstatReparse(raw string, info fs.FileInfo) (fs.FileInfo, error) {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return info, nil
	}
	perm := info.Mode().Perm()
	switch err := readlinkHandle(raw); err {
	case nil:
		return libuvInfo{info, fs.ModeSymlink | perm}, nil
	case errSymlinkNotSupport, errNotAReparsePoint:
	default:
		return nil, err
	}
	target, err := os.Stat(raw)
	if err != nil {
		return nil, err
	}
	mode := perm
	if t, ok := target.Sys().(*syscall.Win32FileAttributeData); ok && t.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= fs.ModeDir
	}
	return libuvInfo{target, mode}, nil
}

// readlinkHandle is fs__readlink_handle without building the target: nil
// when libuv would report a link.
func readlinkHandle(raw string) error {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, 0x80 /* FILE_READ_ATTRIBUTES */, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	buf := make([]byte, maxReparseDataBufSize)
	var n uint32
	if err := syscall.DeviceIoControl(h, fsctlGetReparsePoint, nil, 0, &buf[0], uint32(len(buf)), &n, nil); err != nil {
		return err
	}
	return classifyReparse(buf[:n])
}

// classifyReparse applies fs__readlink_handle's tag rules to a
// REPARSE_DATA_BUFFER.
func classifyReparse(buf []byte) error {
	if len(buf) < 8 {
		return errSymlinkNotSupport
	}
	le := binary.LittleEndian
	data := buf[8:]
	units := func(b []byte) []uint16 {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = le.Uint16(b[2*i:])
		}
		return u
	}
	substitute := func(header int) []uint16 {
		if len(data) < header {
			return nil
		}
		off, size := int(le.Uint16(data[0:])), int(le.Uint16(data[2:]))
		if header+off+size > len(data) {
			return nil
		}
		return units(data[header+off : header+off+size])
	}
	isLetter := func(c uint16) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
	switch le.Uint32(buf[0:]) {
	case tagSymlink, tagLxSymlink:
		return nil
	case tagMountPoint:
		t := substitute(8)
		if len(t) >= 6 && t[0] == '\\' && t[1] == '?' && t[2] == '?' && t[3] == '\\' && isLetter(t[4]) && t[5] == ':' && (len(t) == 6 || t[6] == '\\') {
			return nil
		}
	case tagAppExecLink:
		if len(data) < 4 || le.Uint32(data) < 3 {
			return errSymlinkNotSupport
		}
		list := units(data[4:])
		for range 2 {
			end := 0
			for end < len(list) && list[end] != 0 {
				end++
			}
			if end == 0 || end == len(list) {
				return errSymlinkNotSupport
			}
			list = list[end+1:]
		}
		end := 0
		for end < len(list) && list[end] != 0 {
			end++
		}
		t := list[:end]
		if len(t) >= 3 && isLetter(t[0]) && t[1] == ':' && t[2] == '\\' {
			return nil
		}
	}
	return errSymlinkNotSupport
}
