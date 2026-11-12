//go:build faultline_test && windows

package difftest

import "syscall"

// newConsoleAttr is CREATE_NEW_CONSOLE: the hosted program gets a console of
// its own, so typed key events and the screen buffer belong to it alone.
func newConsoleAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x10}
}
