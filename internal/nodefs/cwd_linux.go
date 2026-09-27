package nodefs

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// libcGetcwd is glibc's getcwd(buf, size) (sysdeps/unix/sysv/linux/getcwd.c,
// glibc 2.36): the getcwd syscall, falling back to the generic walk up
// through ".." when the kernel cannot return the path (ENAMETOOLONG past a
// page) or returns one that is not absolute (a cwd outside the process root,
// "(unreachable)/..."). musl has no fallback (KNOWN_DIFFERENCES.md).
func libcGetcwd(size int) (string, syscall.Errno) {
	buf := make([]byte, size)
	r, _, errno := syscall.Syscall(syscall.SYS_GETCWD, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0)
	if errno == 0 && r > 0 && buf[0] == '/' {
		return string(buf[:r-1]), 0
	}
	if errno == 0 || errno == syscall.ENAMETOOLONG {
		return getcwdGeneric(size)
	}
	return "", errno
}

// getcwdGeneric is glibc's __getcwd_generic (sysdeps/posix/getcwd.c) with
// openat support: from ".", open each "..", find the entry whose d_ino
// matches (every entry at a mount point, and a second fstatat pass when no
// d_ino matches), and build the path backwards into size bytes.
func getcwdGeneric(size int) (string, syscall.Errno) {
	if size == 1 {
		return "", syscall.ERANGE
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(".", &st); err != nil {
		return "", err.(syscall.Errno)
	}
	thisDev, thisIno := st.Dev, st.Ino
	if err := syscall.Lstat("/", &st); err != nil {
		return "", err.(syscall.Errno)
	}
	rootDev, rootIno := st.Dev, st.Ino
	path := []byte{} // built backwards; its length plus the NUL must fit in size
	fd := _AT_FDCWD
	defer func() {
		if fd != _AT_FDCWD {
			syscall.Close(fd)
		}
	}()
	for thisDev != rootDev || thisIno != rootIno {
		parent, err := syscall.Openat(fd, "..", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if fd != _AT_FDCWD {
			syscall.Close(fd)
		}
		fd = _AT_FDCWD
		if err != nil {
			return "", err.(syscall.Errno)
		}
		fd = parent
		if err := syscall.Fstat(fd, &st); err != nil {
			return "", err.(syscall.Errno)
		}
		dotDev, dotIno := st.Dev, st.Ino
		mountPoint := dotDev != thisDev
		name, errno := findEntry(fd, thisDev, thisIno, mountPoint)
		if errno != 0 {
			return "", errno
		}
		if size-1-len(path) <= len(name) {
			return "", syscall.ERANGE
		}
		path = append(append([]byte{'/'}, name...), path...)
		thisDev, thisIno = dotDev, dotIno
	}
	if len(path) == 0 {
		return "/", 0
	}
	return string(path), 0
}

const _AT_FDCWD = -0x64

// findEntry is the readdir loop of __getcwd_generic.
func findEntry(fd int, dev, ino uint64, mountPoint bool) (string, syscall.Errno) {
	useIno := true
	buf := make([]byte, 32*1024)
	var pending []byte
	next := func() (string, uint64, bool, syscall.Errno) {
		for {
			if len(pending) == 0 {
				n, err := syscall.Getdents(fd, buf)
				if err != nil {
					if err == syscall.ENOENT { // glibc readdir: a removed directory is EOF
						return "", 0, false, 0
					}
					return "", 0, false, err.(syscall.Errno)
				}
				if n <= 0 {
					return "", 0, false, 0
				}
				pending = buf[:n]
			}
			// struct linux_dirent64: d_ino, d_off, d_reclen, d_type, d_name
			entIno := binary.NativeEndian.Uint64(pending[0:])
			reclen := int(binary.NativeEndian.Uint16(pending[16:]))
			nameBytes := pending[19:reclen]
			pending = pending[reclen:]
			for i, c := range nameBytes {
				if c == 0 {
					nameBytes = nameBytes[:i]
					break
				}
			}
			return string(nameBytes), entIno, true, 0
		}
	}
	for {
		name, entIno, ok, errno := next()
		if errno != 0 {
			return "", errno
		}
		if !ok && useIno {
			useIno = false
			if _, err := syscall.Seek(fd, 0, 0); err != nil {
				return "", err.(syscall.Errno)
			}
			pending = nil
			name, entIno, ok, errno = next()
			if errno != 0 {
				return "", errno
			}
		}
		if !ok {
			return "", syscall.ENOENT
		}
		if name == "." || name == ".." {
			continue
		}
		if useIno && entIno != ino && !mountPoint {
			continue
		}
		if st, ok := lstatAt(fd, name); ok && st.Mode&syscall.S_IFMT == syscall.S_IFDIR && st.Dev == dev && st.Ino == ino {
			return name, 0
		}
	}
}

// lstatAt is fstatat(fd, name, AT_SYMLINK_NOFOLLOW), whose errors glibc
// ignores here: an O_PATH open does not follow the last component and needs
// no permission on the entry, like fstatat.
func lstatAt(fd int, name string) (syscall.Stat_t, bool) {
	var st syscall.Stat_t
	f, err := syscall.Openat(fd, name, _O_PATH|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return st, false
	}
	defer syscall.Close(f)
	return st, syscall.Fstat(f, &st) == nil
}

const _O_PATH = 0x200000
