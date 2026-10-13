package nodefs

import (
	"syscall"
	"unsafe"
)

// The libc wrappers the syscall package exports for the standard library
// (syscall/linkname_darwin.go). Go's own getcwd wrapper cannot be used: it
// reads errno only when libc returns -1, but getcwd returns NULL.

//go:linkname sysOpenat syscall.openat
func sysOpenat(fd int, path string, flags int, perm uint32) (int, error)

//go:linkname sysFstatat syscall.fstatat
func sysFstatat(fd int, path string, stat *syscall.Stat_t, flags int) error

//go:linkname sysFcntl syscall.fcntl
func sysFcntl(fd int, cmd int, arg int) (int, error)

//go:linkname sysFdopendir syscall.fdopendir
func sysFdopendir(fd int) (uintptr, error)

//go:linkname sysReaddirR syscall.readdir_r
func sysReaddirR(dir uintptr, entry *syscall.Dirent, result **syscall.Dirent) syscall.Errno

//go:linkname sysClosedir syscall.closedir
func sysClosedir(dir uintptr) error

const (
	darwinAtFdcwd          = -2
	darwinAtSymlinkNofollw = 0x20
	darwinMaxPathLen       = 1024
	darwinDTDir            = 4
)

func errnoOf(err error) syscall.Errno {
	if e, ok := err.(syscall.Errno); ok {
		return e
	}
	return syscall.EINVAL
}

// libcGetcwd is Apple Libc's getcwd(buf, size) (gen/FreeBSD/getcwd.c):
// the F_GETPATH fast path, checked against "." by device and inode, then
// (unless the fast path failed with ERANGE) the walk up through "..".
func libcGetcwd(size int) (string, syscall.Errno) {
	if size == 1 {
		return "", syscall.ERANGE
	}
	path, errno := darwinGetcwdFast(size)
	if errno == 0 {
		return path, 0
	}
	if errno == syscall.ERANGE {
		return "", errno
	}
	return darwinGetcwdWalk(size)
}

// darwinGetcwdFast is the __getcwd workaround in Apple's getcwd.c.
func darwinGetcwdFast(size int) (string, syscall.Errno) {
	fd, err := syscall.Open(".", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", errnoOf(err)
	}
	var dot, pt syscall.Stat_t
	if err := syscall.Fstat(fd, &dot); err != nil {
		syscall.Close(fd)
		return "", errnoOf(err)
	}
	if dot.Dev == 0 || dot.Ino == 0 {
		syscall.Close(fd)
		return "", syscall.EINVAL
	}
	buf := make([]byte, max(size, darwinMaxPathLen))
	_, err = sysFcntl(fd, syscall.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0]))))
	syscall.Close(fd)
	if err != nil {
		return "", errnoOf(err)
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	b := string(buf[:n])
	if err := syscall.Stat(b, &pt); err != nil {
		return "", errnoOf(err)
	}
	if dot.Dev != pt.Dev || dot.Ino != pt.Ino {
		return "", syscall.EINVAL
	}
	if size < darwinMaxPathLen && n >= size {
		return "", syscall.ERANGE
	}
	return b, 0
}

// darwinGetcwdWalk is the portable part of Apple's getcwd: stat "/", then
// from "." open each "..", find the entry by d_fileno (with an fstatat of
// every directory entry for firmlinks, and of every entry at a mount
// point), and build the path backwards into size bytes. A failed fstatat is
// remembered and reported if the entry is not found.
func darwinGetcwdWalk(size int) (string, syscall.Errno) {
	var s syscall.Stat_t
	if err := syscall.Stat("/", &s); err != nil {
		return "", errnoOf(err)
	}
	rootDev, rootIno := s.Dev, s.Ino
	room := size - 1 // bytes before the NUL
	path := []byte{}
	var dir uintptr
	dirFd := -1 // dirfd(dir): fdopendir owns the descriptor it was given
	defer func() {
		if dir != 0 {
			sysClosedir(dir)
		}
	}()
	for first := true; ; first = false {
		var err error
		if dir != 0 {
			err = syscall.Fstat(dirFd, &s)
		} else {
			err = syscall.Lstat(".", &s)
		}
		if err != nil {
			return "", errnoOf(err)
		}
		ino, dev := s.Ino, s.Dev
		if rootDev == dev && rootIno == ino {
			return "/" + string(path), 0
		}
		at := darwinAtFdcwd
		if dir != 0 {
			at = dirFd
		}
		fd, err := sysOpenat(at, "..", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return "", errnoOf(err)
		}
		if dir != 0 {
			sysClosedir(dir)
			dir = 0
		}
		if dir, err = sysFdopendir(fd); err != nil {
			dir = 0
			syscall.Close(fd)
			return "", errnoOf(err)
		}
		dirFd = fd
		if err := syscall.Fstat(dirFd, &s); err != nil {
			return "", errnoOf(err)
		}
		var saveErrno syscall.Errno
		var ent syscall.Dirent
		var name string
		found := false
		sameDev := s.Dev == dev
		for !found {
			var res *syscall.Dirent
			if e := sysReaddirR(dir, &ent, &res); e != 0 {
				return "", e // readdir set errno: it wins over a saved one
			}
			if res == nil {
				if saveErrno != 0 {
					return "", saveErrno
				}
				return "", syscall.ENOENT
			}
			nb := make([]byte, ent.Namlen)
			for i := range nb {
				nb[i] = byte(ent.Name[i])
			}
			entName := string(nb)
			isDot := entName == "." || entName == ".."
			if sameDev {
				if ent.Ino == ino {
					name, found = entName, true
					continue
				}
				if isDot || ent.Type != darwinDTDir {
					continue
				}
			} else if isDot {
				continue
			}
			var st syscall.Stat_t
			if err := sysFstatat(dirFd, entName, &st, darwinAtSymlinkNofollw); err != nil {
				if saveErrno == 0 {
					saveErrno = errnoOf(err)
				}
				continue
			}
			if st.Dev == dev && st.Ino == ino {
				name, found = entName, true
			}
		}
		need := len(name) + 2
		if first {
			need = len(name) + 1
		}
		if room-len(path) < need {
			return "", syscall.ERANGE
		}
		if !first {
			path = append([]byte{'/'}, path...)
		}
		path = append([]byte(name), path...)
	}
}
