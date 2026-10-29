//go:build !linux

package nodefs

import (
	"os"
	"syscall"
)

// scandir is libuv's uv_fs_scandir: every entry but "." and "..", in
// directory order (the caller sorts), with each entry's type from the
// directory record as libuv reads it (direntTypeOf), as Linux d_type
// values. On Unix the directory is opened with O_DIRECTORY, as opendir
// does.
func scandir(path string) (names []string, types []uint8, err error) {
	// An EINTR anywhere re-runs the whole scandir, as uv__fs_work re-runs
	// uv__fs_scandir (glibc's scandir fails with the errno opendir or
	// readdir set).
	err = retryEINTR(func() (e error) { names, types, e = scandirOnce(path); return })
	return
}

func scandirOnce(path string) ([]string, []uint8, error) {
	f, err := openDir(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	// os.File.ReadDir takes each type from the record (readdir's d_type on
	// darwin, getdirentries' on the BSDs, FILE_*_DIR_INFO on Windows), so
	// a readable directory without search permission keeps its types, as
	// in libuv.
	entries, err := f.ReadDir(-1)
	names := make([]string, len(entries))
	types := make([]uint8, len(entries))
	for i, e := range entries {
		names[i], types[i] = e.Name(), direntTypeOf(e)
	}
	return names, types, err
}

// scandirNames is scandir without types, for readdirSync without
// withFileTypes (rimraf): os.File.ReadDir lstats an entry whose d_type is
// DT_UNKNOWN and can fail there, where libuv's scandir never stats.
func scandirNames(path string) ([]string, error) {
	var names []string
	err := retryEINTR(func() error {
		f, err := openDir(path)
		if err != nil {
			return err
		}
		defer f.Close()
		names, err = f.Readdirnames(-1)
		return err
	})
	return names, err
}

func openDir(path string) (*os.File, error) {
	if isWindows {
		// libuv's fs__scandir opens anything with
		// FILE_FLAG_BACKUP_SEMANTICS, and NtQueryDirectoryFile on a
		// non-directory gives STATUS_INVALID_PARAMETER, which it reports
		// as UV_ENOTDIR itself (src/win/fs.c, not_a_directory_error).
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if info, err := f.Stat(); err == nil && !info.IsDir() {
			f.Close()
			return nil, uvCode("ENOTDIR")
		}
		return f, nil
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|oDirectory|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
