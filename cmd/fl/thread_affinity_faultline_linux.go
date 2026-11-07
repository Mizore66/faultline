//go:build faultline_test && linux

package main

import (
	"os"
	"runtime"
)

// Test-only: the writer-fault harness counts strace when= per thread, so it
// asks the CLI to stay on one OS thread. Never linked into release builds.
func init() {
	if os.Getenv("FAULTLINE_TEST_LOCK_OS_THREAD") == "1" {
		runtime.LockOSThread()
	}
}
