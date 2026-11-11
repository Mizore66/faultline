//go:build darwin

package cli

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// openTestPTY returns a pseudo-terminal master and slave.
func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCPTYGRANT, 0); e != 0 {
		t.Fatal(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCPTYUNLK, 0); e != 0 {
		t.Fatal(e)
	}
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		t.Fatal(e)
	}
	n := 0
	for name[n] != 0 {
		n++
	}
	s, err := os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return m, s
}
