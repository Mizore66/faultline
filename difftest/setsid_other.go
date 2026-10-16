//go:build faultline_test && !unix

package difftest

import "syscall"

// noTerminal: Windows has no controlling terminal to leave.
func noTerminal() *syscall.SysProcAttr { return nil }
