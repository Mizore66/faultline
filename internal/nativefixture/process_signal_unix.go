//go:build !windows

package nativefixture

import (
	"os"
	"syscall"
)

func observedSignal(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}
