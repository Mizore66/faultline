package main

import (
	"syscall"
	"unsafe"

	"github.com/Mizore66/faultline/internal/cliargs"
)

// nodeArgv is process.argv.slice(2) on Windows: the UCRT split of
// GetCommandLineW, each argument converted like WideCharToMultiByte.
func nodeArgv() []string {
	p := syscall.GetCommandLine()
	var cmd []uint16
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; ptr = unsafe.Add(ptr, 2) {
		cmd = append(cmd, *(*uint16)(ptr))
	}
	var args []string
	for _, a := range cliargs.SplitUCRT(cmd) {
		args = append(args, cliargs.FromWide(a))
	}
	return args
}
