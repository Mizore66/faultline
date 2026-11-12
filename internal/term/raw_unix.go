//go:build unix

package term

import (
	"os"
	"syscall"
	"unsafe"
)

// MakeRaw puts the terminal into the mode libuv's uv_tty_set_mode(RAW) uses
// for Node's setRawMode(true): no echo, no canonical editing, no signal keys
// (Ctrl+C and Ctrl+D arrive as bytes), no CR-to-NL input mapping, output
// post-processing kept. restore returns the original mode.
func MakeRaw(file *os.File) (restore func(), ok bool) {
	var orig syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&orig))); e != 0 {
		return nil, false
	}
	raw := orig
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag |= syscall.OPOST | syscall.ONLCR
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlSetTermios, uintptr(unsafe.Pointer(&raw))); e != 0 {
		return nil, false
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlSetTermios, uintptr(unsafe.Pointer(&orig)))
	}, true
}
