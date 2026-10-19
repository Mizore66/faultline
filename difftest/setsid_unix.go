//go:build faultline_test && unix

package difftest

import "syscall"

// noTerminal detaches a child from the controlling terminal (setsid).
func noTerminal() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
