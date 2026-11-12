//go:build unix

package term

import (
	"os"
	"syscall"
	"unsafe"
)

type winsize struct{ Row, Col, X, Y uint16 }

// Columns reports the terminal's column count (TIOCGWINSZ), or 0 when it is
// unknown, which Node's readline treats as an infinite width.
func Columns(file *os.File) int {
	if file == nil {
		return 0
	}
	var ws winsize
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); e != 0 {
		return 0
	}
	return int(ws.Col)
}
