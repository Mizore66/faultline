//go:build unix

package nodefs

import (
	"runtime"
	"syscall"
)

// pathMaxBytes is Node's buffer for process.cwd() (PATH_MAX_BYTES):
// PATH_MAX, 1024 on darwin and 4096 on Linux.
var pathMaxBytes = map[bool]int{true: 1024, false: 4096}[runtime.GOOS == "darwin"]

// Cwd is process.cwd(): libuv's uv_cwd into a PATH_MAX_BYTES buffer, the
// bytes decoded as UTF-8 with replacement. uv_cwd calls libc getcwd (see
// libcGetcwd) and, when that fails with ERANGE, again with a buffer one byte
// larger, turning success into ENOBUFS; any other errno is the error Node
// throws.
func Cwd() (string, error) {
	cwd, errno := libcGetcwd(pathMaxBytes)
	if errno == syscall.ERANGE {
		if _, errno = libcGetcwd(pathMaxBytes + 1); errno == 0 {
			errno = syscall.ENOBUFS
		}
	}
	if errno != 0 {
		return "", &Error{Code: codeOf(errno), Syscall: "uv_cwd", NoPath: true}
	}
	if n := len(cwd); n > 1 && cwd[n-1] == '/' {
		cwd = cwd[:n-1]
	}
	return DecodeUTF8([]byte(cwd)), nil
}
