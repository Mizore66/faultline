//go:build linux && !(mips || mipsle || mips64 || mips64le)

package boot

import "syscall"

// archDefault are the terminating signals whose number is not shared by
// every Linux architecture: SIGSTKFLT (16), which MIPS lacks.
var archDefault = []syscall.Signal{syscall.SIGSTKFLT}
