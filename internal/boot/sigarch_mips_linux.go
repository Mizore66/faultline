//go:build linux && (mips || mipsle || mips64 || mips64le)

package boot

import "syscall"

// archDefault are the terminating signals whose number is not shared by
// every Linux architecture. MIPS has no SIGSTKFLT; its SIGEMT (7), which
// Node resets to SIG_DFL like any signal below 32, ends the process with a
// core dump.
var archDefault = []syscall.Signal{syscall.SIGEMT}
