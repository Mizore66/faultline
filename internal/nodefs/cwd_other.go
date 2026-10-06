//go:build unix && !linux && !darwin

package nodefs

import "syscall"

// libcGetcwd on other Unix systems is Go's getcwd, which has not been
// compared with their libc.
func libcGetcwd(size int) (string, syscall.Errno) {
	cwd, err := syscall.Getwd()
	if err != nil {
		if e, ok := err.(syscall.Errno); ok {
			return "", e
		}
		return "", syscall.ENOENT
	}
	if len(cwd) >= size {
		return "", syscall.ERANGE
	}
	return cwd, 0
}
