//go:build faultline_test && (linux || darwin)

package difftest

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// setPTYColumns sets the pseudo-terminal's window size (TIOCSWINSZ on the
// master), so a program reading the terminal width sees cols columns.
func setPTYColumns(t *testing.T, master *os.File, cols int) {
	t.Helper()
	ws := struct{ Row, Col, X, Y uint16 }{Row: 24, Col: uint16(cols)}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws))); e != 0 {
		t.Fatal(e)
	}
}
