package nodefs

import (
	"encoding/binary"
	"syscall"
)

// direntReader is glibc's readdir over getdents64 as Node's official Linux
// builds see it on glibc 2.28 to 2.36 (Debian 11 and 12, RHEL 8 and 9,
// Ubuntu 22.04): records whose d_ino is 0 are skipped ("deleted files"),
// which some FUSE and old XFS filesystems report for live entries, and a
// directory removed while it is read ends like EOF. glibc 2.37 dropped the
// skip, and musl never had it (KNOWN_DIFFERENCES.md).
type direntReader struct {
	fd      int
	buf     []byte
	pending []byte
}

// next returns the next entry's name, inode and d_type; ok is false at
// the end.
func (d *direntReader) next() (name string, ino uint64, typ uint8, ok bool, errno syscall.Errno) {
	if d.buf == nil {
		d.buf = make([]byte, 32*1024)
	}
	for {
		if len(d.pending) == 0 {
			n, err := syscall.Getdents(d.fd, d.buf)
			if err != nil {
				if err == syscall.ENOENT {
					return "", 0, 0, false, 0
				}
				return "", 0, 0, false, err.(syscall.Errno)
			}
			if n <= 0 {
				return "", 0, 0, false, 0
			}
			d.pending = d.buf[:n]
		}
		// struct linux_dirent64: d_ino, d_off, d_reclen, d_type, d_name
		ino = binary.NativeEndian.Uint64(d.pending[0:])
		reclen := int(binary.NativeEndian.Uint16(d.pending[16:]))
		typ = d.pending[18]
		nameBytes := d.pending[19:reclen]
		d.pending = d.pending[reclen:]
		if ino == 0 {
			continue
		}
		for i, c := range nameBytes {
			if c == 0 {
				nameBytes = nameBytes[:i]
				break
			}
		}
		return string(nameBytes), ino, typ, true, 0
	}
}

// scandir is libuv's uv_fs_scandir over glibc: opendir (O_DIRECTORY, so a
// non-directory fails with ENOTDIR and a FIFO does not block, then fstat),
// every entry
// but "." and "..", with d_type (0 is DT_UNKNOWN); the caller sorts.
func scandir(path string) (names []string, types []uint8, err error) {
	// An EINTR anywhere re-runs the whole scandir, as uv__fs_work re-runs
	// uv__fs_scandir (glibc's scandir fails with the errno opendir or
	// readdir set).
	err = retryEINTR(func() (e error) { names, types, e = scandirOnce(path); return })
	return
}

func scandirOnce(path string) ([]string, []uint8, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer syscall.Close(fd)
	// glibc's opendir_tail fstats the new descriptor (for st_blksize) and
	// checks S_ISDIR; a failing fstat fails the scandir with its errno.
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, nil, err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, nil, syscall.ENOTDIR
	}
	d := direntReader{fd: fd}
	var ents []rawDirent
	for {
		name, _, typ, ok, errno := d.next()
		if errno != 0 {
			return nil, nil, errno
		}
		if !ok {
			break
		}
		if name != "." && name != ".." {
			ents = append(ents, rawDirent{name, typ})
		}
	}
	names := make([]string, len(ents))
	types := make([]uint8, len(ents))
	for i, e := range ents {
		names[i], types[i] = e.name, e.typ
	}
	return names, types, nil
}

type rawDirent struct {
	name string
	typ  uint8
}
