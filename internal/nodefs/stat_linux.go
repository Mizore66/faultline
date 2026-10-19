package nodefs

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// statRaw is uv_fs_lstat (follow=false) or uv_fs_stat on a syscall path:
// libuv 1.51's uv__fs_statx, statx(AT_FDCWD, path, AT_STATX_SYNC_AS_STAT
// [| AT_SYMLINK_NOFOLLOW], STATX_BASIC_STATS | STATX_BTIME), falling back
// to stat/lstat once statx is found missing. Go's os.Stat makes a
// newfstatat call instead, so a fault injected on one syscall would land on
// a different step of Node's sequence (round 7 #12: MKDirpSync's stat).
func statRaw(raw string, follow bool) (fs.FileInfo, error) {
	flags := 0
	if !follow {
		flags = _AT_SYMLINK_NOFOLLOW
	}
	var info fs.FileInfo
	err := retryEINTR(func() (e error) { info, e = statx(_AT_FDCWD, raw, flags); return })
	if err != errNoStatx {
		return info, err
	}
	if follow {
		return os.Stat(raw)
	}
	return os.Lstat(raw)
}

// fstatFile is uv_fs_fstat: statx(fd, "", AT_EMPTY_PATH), or fstat(2)
// without statx.
func fstatFile(f *os.File) (fs.FileInfo, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return f.Stat()
	}
	var info fs.FileInfo
	rc.Control(func(fd uintptr) {
		err = retryEINTR(func() (e error) { info, e = statx(int(fd), "", _AT_EMPTY_PATH); return })
	})
	if err != errNoStatx {
		if info != nil {
			info.(*statInfo).name = filepath.Base(f.Name())
		}
		return info, err
	}
	return f.Stat()
}

// exists is uv_fs_access(raw, F_OK): access(2), which libuv retries on
// EINTR. It differs from stat(2) where a filesystem answers the two
// differently, and checks search permission with the real ids.
func exists(raw string) bool {
	return retryEINTR(func() error { return syscall.Access(raw, 0) }) == nil
}

// fstatDir is glibc 2.36's __fstat64 as opendir_tail calls it:
// __fstatat64_time64(fd, "", AT_EMPTY_PATH). On 64-bit Linux that is
// fstatat(fd, "", &st, AT_EMPTY_PATH) (newfstatat on most architectures);
// where glibc defines FSTATAT_USE_STATX (fstatatUsesStatx) it is statx(fd, "",
// AT_NO_AUTOMOUNT | AT_EMPTY_PATH, STATX_BASIC_STATS), falling back to
// fstatat64 on ENOSYS. It returns the file type bits.
func fstatDir(fd int) (uint32, error) {
	empty := [1]byte{}
	if fstatatUsesStatx[runtime.GOARCH] {
		var buf statxBuf
		_, _, e := syscall.Syscall6(sysStatx[runtime.GOARCH], uintptr(fd), uintptr(unsafe.Pointer(&empty[0])), _AT_NO_AUTOMOUNT|_AT_EMPTY_PATH, _STATX_BASIC_STATS, uintptr(unsafe.Pointer(&buf)), 0)
		if e != syscall.ENOSYS {
			if e != 0 {
				return 0, e
			}
			return uint32(buf.Mode) & syscall.S_IFMT, nil
		}
	}
	nr := sysFstatat[runtime.GOARCH]
	if nr == 0 {
		var st syscall.Stat_t
		err := syscall.Fstat(fd, &st)
		return st.Mode & syscall.S_IFMT, err
	}
	var st syscall.Stat_t
	_, _, e := syscall.Syscall6(nr, uintptr(fd), uintptr(unsafe.Pointer(&empty[0])), uintptr(unsafe.Pointer(&st)), _AT_EMPTY_PATH, 0, 0)
	if e != 0 {
		return 0, e
	}
	return st.Mode & syscall.S_IFMT, nil
}

// fstatatUsesStatx is glibc 2.36's FSTATAT_USE_STATX by GOARCH, from its
// sysdeps: a 32-bit word and syscall size (386, arm, mips o32), a kernel
// stat with 32-bit timestamps (STAT_HAS_TIME32: mips64 n64), or neither
// newfstatat nor fstatat64 in the syscall table (loongarch64). amd64,
// arm64, riscv64, ppc64 and s390x have newfstatat and use it.
var fstatatUsesStatx = map[string]bool{
	"386": true, "arm": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "loong64": true,
}

const (
	_AT_SYMLINK_NOFOLLOW = 0x100
	_AT_EMPTY_PATH       = 0x1000
	_AT_NO_AUTOMOUNT     = 0x800
	_STATX_MASK          = 0xFFF // STATX_BASIC_STATS + STATX_BTIME
	_STATX_BASIC_STATS   = 0x7FF
)

// sysStatx and sysFstatat are the system call numbers (syscall's
// zsysnum_linux_*.go and the kernel's tables), by GOARCH. sysFstatat lists
// only the architectures where syscall.Stat_t is the struct that call
// fills; elsewhere fstatDir uses fstat.
var (
	sysStatx = map[string]uintptr{
		"amd64": 332, "arm64": 291, "riscv64": 291, "loong64": 291, "386": 383, "arm": 397,
		"ppc64": 383, "ppc64le": 383, "s390x": 379, "mips": 4366, "mipsle": 4366, "mips64": 5326, "mips64le": 5326,
	}
	sysFstatat = map[string]uintptr{
		"amd64": 262, "arm64": 79, "riscv64": 79, "ppc64": 291, "ppc64le": 291, "s390x": 293, "386": 300, "arm": 327,
	}
)

// noStatx is libuv's sticky no_statx: once statx fails with EINVAL, EPERM
// (a seccomp filter), ENOSYS or EOPNOTSUPP, every later stat uses the old
// calls.
var noStatx atomic.Bool

var errNoStatx = syscall.ENOSYS

// statxBuf is struct statx (include/uapi/linux/stat.h), the same on every
// architecture.
type statxBuf struct {
	Mask, Blksize               uint32
	Attributes                  uint64
	Nlink, UID, GID             uint32
	Mode                        uint16
	_                           uint16
	Ino, Size, Blocks, AttrMask uint64
	Atime, Btime, Ctime, Mtime  statxTime
	RdevMajor, RdevMinor        uint32
	DevMajor, DevMinor          uint32
	_                           [14]uint64
}

type statxTime struct {
	Sec  int64
	Nsec uint32
	_    int32
}

// statx is uv__fs_statx; errNoStatx means the caller falls back.
func statx(dirfd int, path string, flags int) (fs.FileInfo, error) {
	nr := sysStatx[runtime.GOARCH]
	if nr == 0 || noStatx.Load() {
		return nil, errNoStatx
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	var buf statxBuf
	r, _, e := syscall.Syscall6(nr, uintptr(dirfd), uintptr(unsafe.Pointer(p)), uintptr(flags), _STATX_MASK, uintptr(unsafe.Pointer(&buf)), 0)
	switch {
	case e == syscall.EINVAL || e == syscall.EPERM || e == syscall.ENOSYS || e == syscall.EOPNOTSUPP || e == 0 && r != 0:
		noStatx.Store(true)
		return nil, errNoStatx
	case e != 0:
		return nil, e
	}
	return &statInfo{name: filepath.Base(path), buf: buf}, nil
}

// statInfo is a statx result as an fs.FileInfo, with os's mode mapping.
type statInfo struct {
	name string
	buf  statxBuf
}

func (s *statInfo) Name() string { return s.name }
func (s *statInfo) Size() int64  { return int64(s.buf.Size) }
func (s *statInfo) ModTime() time.Time {
	return time.Unix(s.buf.Mtime.Sec, int64(s.buf.Mtime.Nsec))
}
func (s *statInfo) IsDir() bool { return s.Mode().IsDir() }
func (s *statInfo) Sys() any    { return &s.buf }
func (s *statInfo) Mode() fs.FileMode {
	m := uint32(s.buf.Mode)
	mode := fs.FileMode(m & 0o777)
	switch m & syscall.S_IFMT {
	case syscall.S_IFBLK:
		mode |= fs.ModeDevice
	case syscall.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case syscall.S_IFDIR:
		mode |= fs.ModeDir
	case syscall.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= fs.ModeSymlink
	case syscall.S_IFSOCK:
		mode |= fs.ModeSocket
	}
	if m&syscall.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if m&syscall.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if m&syscall.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}
