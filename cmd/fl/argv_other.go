//go:build !windows

package main

import (
	"os"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// nodeArgv is process.argv.slice(2): the raw bytes decoded as UTF-8 with
// replacement.
func nodeArgv() []string {
	args := make([]string, len(os.Args)-1)
	for i, a := range os.Args[1:] {
		args[i] = nodefs.DecodeUTF8([]byte(a))
	}
	return args
}
