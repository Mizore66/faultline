//go:build windows

// Command console-helper is test-only Windows support: it delivers CTRL_C_EVENT
// to the new console of a target process. It is not part of fl.
package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func check(api string, ok uintptr, err error) {
	if ok == 0 {
		fmt.Fprintf(os.Stderr, "console-helper: %s failed: %v\n", api, err)
		os.Exit(2)
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: console-helper <pid>")
		os.Exit(2)
	}
	pid, err := strconv.ParseUint(os.Args[1], 10, 32)
	if err != nil || pid == 0 || strconv.FormatUint(pid, 10) != os.Args[1] {
		fmt.Fprintln(os.Stderr, "console-helper: pid must be a positive decimal")
		os.Exit(2)
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	free := kernel.NewProc("FreeConsole")
	attach := kernel.NewProc("AttachConsole")
	ignore := kernel.NewProc("SetConsoleCtrlHandler")
	emit := kernel.NewProc("GenerateConsoleCtrlEvent")
	// ERROR_INVALID_HANDLE: the emitter had no console.
	if ok, _, err := free.Call(); ok == 0 && err != syscall.Errno(6) {
		check("FreeConsole", ok, err)
	}
	ok, _, err := attach.Call(uintptr(pid))
	check("AttachConsole", ok, err)
	ok, _, err = ignore.Call(0, 1)
	check("SetConsoleCtrlHandler", ok, err)
	ok, _, err = emit.Call(0, 0)
	check("GenerateConsoleCtrlEvent", ok, err)
}
