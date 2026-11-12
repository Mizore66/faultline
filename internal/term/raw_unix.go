//go:build unix

package term

import (
	"os"
	"syscall"
	"unsafe"
)

func rawTermios(orig syscall.Termios) syscall.Termios {
	raw := orig
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag |= syscall.OPOST | syscall.ONLCR
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	return raw
}

func setTermios(file *os.File, t *syscall.Termios) bool {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlSetTermios, uintptr(unsafe.Pointer(t)))
	return e == 0
}

// MakeRaw puts the terminal into the mode libuv's uv_tty_set_mode(RAW) uses
// for Node's setRawMode(true): no echo, no canonical editing, no signal keys
// (Ctrl+C and Ctrl+D arrive as bytes), no CR-to-NL input mapping, output
// post-processing kept. restore returns the original mode.
func MakeRaw(file *os.File) (restore func(), ok bool) {
	var orig syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&orig))); e != 0 {
		return nil, false
	}
	raw := rawTermios(orig)
	if !setTermios(file, &raw) {
		return nil, false
	}
	return func() { setTermios(file, &orig) }, true
}

// MakeRawAgain re-enters raw mode after a restore (Ctrl+Z / SIGCONT).
func MakeRawAgain(file *os.File) {
	var cur syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&cur))); e != 0 {
		return
	}
	raw := rawTermios(cur)
	setTermios(file, &raw)
}
