// Package term holds the shared terminal check used by cmd/fl and the CLI.
package term

import (
	"os"
	"syscall"
)

// IsTerminal reports whether file is a console: GetConsoleMode succeeds.
func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}
