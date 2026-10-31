//go:build linux && !globalopen_hooks

package main

// Default (untagged) indirections: wait4(-1, __WALL), pidfd_send_signal
// (SIGKILL), os.Exit, and empty seams. The build tag globalopen_hooks selects
// hooks_regression.go instead (test builds only). Default behavior never
// depends on a hook, and a default `go build` of this package contains none
// of the regression instrumentation.

import (
	"os"
	"syscall"
)

func rawWait4(ws *syscall.WaitStatus) (int, error) {
	return syscall.Wait4(-1, ws, syscall.WALL, nil)
}

// signalGroup sends SIGKILL through a group pidfd (never by number).
func signalGroup(fd, tgid int) error { return pidfdSendSignal(fd, syscall.SIGKILL) }

func exitHook(code int) { os.Exit(code) }

func afterInfo(tid int, info syscallInfo) {}

func beforeSuppress(tid int) {}

func afterLogOpen(f *os.File) {}

func beforeLogClose(f *os.File) {}
