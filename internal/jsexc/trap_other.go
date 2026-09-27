//go:build !unix

package jsexc

import "os"

// trap exits with STATUS_BREAKPOINT, the code a V8 fatal error leaves on
// Windows.
func trap() { os.Exit(int(int32(-0x7ffffffd))) } // 0x80000003

// abort exits with 134, the status Node's abort() leaves in a shell.
func abort() { os.Exit(134) }
