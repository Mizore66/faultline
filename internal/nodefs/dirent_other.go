//go:build !linux

package nodefs

import (
	"os"
	"syscall"
)

// scandir is libuv's uv_fs_scandir: every entry but "." and "..", in
// directory order (the caller sorts). On Unix the directory is opened with
// O_DIRECTORY, as opendir does. d_type is not read here: nil types.
func scandir(path string) ([]string, []uint8, error) {
	f, err := openDir(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	return names, nil, err
}

func openDir(path string) (*os.File, error) {
	if isWindows {
		return os.Open(path)
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|oDirectory|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
