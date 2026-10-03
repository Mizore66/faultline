package nodefs

import (
	"encoding/binary"
	"io/fs"
	"strings"
	"syscall"
	"time"
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
	fileReadAttributes    = 0x80
	errSharingViolation   = syscall.Errno(32)  // ERROR_SHARING_VIOLATION
	errInvalidName        = syscall.Errno(123) // ERROR_INVALID_NAME
)

// statRaw is libuv's fs__stat_impl (1.51, Node 22.22.2) on a namespaced
// path: lstat (follow=false) opens the entry itself; a reparse point is a
// symbolic link when fs__readlink_handle accepts it (see classifyReparse),
// and otherwise lstat is retried as a following stat. When the open fails
// with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION the entry is read from
// its parent directory (fs__stat_directory), where any reparse point is a
// link to lstat and an error to stat.
func statRaw(raw string, follow bool) (fs.FileInfo, error) {
	raw = statPreparePath(raw)
	info, err := statImplFromPath(raw, !follow)
	if err != nil && !follow && (err == errSymlinkNotSupport || err == errNotAReparsePoint) {
		return statImplFromPath(raw, false)
	}
	return info, err
}

// statPreparePath is fs__stat_prepare_path: one trailing separator is
// dropped unless it follows a drive colon.
func statPreparePath(p string) string {
	if n := len(p); n > 1 && p[n-2] != ':' && (p[n-1] == '\\' || p[n-1] == '/') {
		return p[:n-1]
	}
	return p
}

type winStat struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
}

func (s winStat) Name() string       { return s.name }
func (s winStat) Size() int64        { return s.size }
func (s winStat) Mode() fs.FileMode  { return s.mode }
func (s winStat) ModTime() time.Time { return s.mod }
func (s winStat) IsDir() bool        { return s.mode.IsDir() }
func (s winStat) Sys() any           { return nil }

// assignStat is fs__stat_assign_statbuf's type and permission bits.
func assignStat(name string, attrs uint32, size int64, mtime syscall.Filetime, lstat bool) winStat {
	st := winStat{name: name, mod: time.Unix(0, mtime.Nanoseconds())}
	switch {
	case lstat && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		st.mode = fs.ModeSymlink
		st.size = size
	case attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0:
		st.mode = fs.ModeDir
	default:
		st.size = size
	}
	if attrs&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		st.mode |= 0o444
	} else {
		st.mode |= 0o666
	}
	return st
}

func baseName(p string) string {
	i := len(p)
	for i > 0 && p[i-1] != '\\' && p[i-1] != '/' && p[i-1] != ':' {
		i--
	}
	return p[i:]
}

// statImplFromPath is fs__stat_impl_from_path's handle path (the
// GetFileInformationByName fast path gives the same results).
func statImplFromPath(raw string, lstat bool) (fs.FileInfo, error) {
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return nil, err
	}
	flags := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	if lstat {
		flags |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := syscall.CreateFile(p, fileReadAttributes, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		if err != syscall.ERROR_ACCESS_DENIED && err != errSharingViolation {
			return nil, err
		}
		return statDirectory(raw, lstat, err)
	}
	defer syscall.CloseHandle(h)
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return nil, err
	}
	size := int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow)
	if lstat && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		if err := readlinkHandle(h); err != nil {
			return nil, err
		}
	}
	return assignStat(baseName(raw), d.FileAttributes, size, d.LastWriteTime, lstat), nil
}

// statDirectory is fs__stat_directory: the entry as its parent directory
// lists it. A reparse point cannot be stat'ed this way (the open error
// stands) but is a link to lstat.
func statDirectory(raw string, lstat bool, openErr error) (fs.FileInfo, error) {
	name := baseName(raw)
	if name == "" || strings.ContainsAny(name, "*?<>\"") {
		if name != "" {
			return nil, errInvalidName
		}
		return nil, openErr
	}
	p, err := syscall.UTF16PtrFromString(raw)
	if err != nil {
		return nil, err
	}
	var fd syscall.Win32finddata
	h, err := syscall.FindFirstFile(p, &fd)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND || err == syscall.ERROR_NO_MORE_FILES {
			return nil, syscall.ERROR_PATH_NOT_FOUND
		}
		return nil, err
	}
	syscall.FindClose(h)
	if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && !lstat {
		return nil, openErr
	}
	size := int64(fd.FileSizeHigh)<<32 | int64(fd.FileSizeLow)
	if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		size = 0
	}
	return assignStat(name, fd.FileAttributes, size, fd.LastWriteTime, lstat), nil
}

// readlinkHandle is fs__readlink_handle without building the target: nil
// when libuv would report a link.
func readlinkHandle(h syscall.Handle) error {
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

// exists is existsSync on Windows: uv_fs_access, then uv_fs_stat following
// links; the stat covers both.
func exists(raw string) bool {
	_, err := statRaw(raw, true)
	return err == nil
}
