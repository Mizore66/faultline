package nodefs

import (
	"syscall"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// pathMaxBytes is Node's process.cwd() buffer on Windows: MAX_PATH * 4.
const pathMaxBytes = 260 * 4

// Cwd is process.cwd(): libuv's uv_cwd converts GetCurrentDirectoryW's
// UTF-16 to WTF-8 (a lone surrogate stays a 3-byte sequence), drops a
// trailing backslash except at a drive root, fails with ENOBUFS when that
// does not fit PATH_MAX_BYTES, and Node then decodes the bytes as UTF-8, so
// each lone surrogate becomes three U+FFFD.
func Cwd() (string, error) {
	b := make([]uint16, 300)
	for {
		n, err := syscall.GetCurrentDirectory(uint32(len(b)), &b[0])
		if err != nil {
			return "", &Error{Code: codeOf(err), Syscall: "uv_cwd", NoPath: true}
		}
		if int(n) <= len(b) {
			b = b[:n]
			break
		}
		b = make([]uint16, n)
	}
	if len(b) > 1 && b[len(b)-1] == '\\' && !(len(b) == 3 && b[1] == ':') {
		b = b[:len(b)-1]
	}
	wtf8 := jsstr.FromUTF16(b)
	if len(wtf8)+1 > pathMaxBytes {
		return "", &Error{Code: "ENOBUFS", Syscall: "uv_cwd", NoPath: true}
	}
	return DecodeUTF8([]byte(wtf8)), nil
}
