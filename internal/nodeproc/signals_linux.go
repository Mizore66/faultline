//go:build linux && !(mips || mipsle || mips64 || mips64le)

package nodeproc

import "syscall"

// stkfltSignal and ioAliasSignals are the entries of Node's signo_string
// and os.constants.signals that exist only on some systems, at their place
// in its order: SIGSTKFLT after SIGCHLD; SIGPOLL (an alias of SIGIO, so
// only in os.constants.signals) and SIGPWR after SIGIO.
var (
	stkfltSignal   = []namedSignal{{"SIGSTKFLT", int(syscall.SIGSTKFLT)}}
	ioAliasSignals = []namedSignal{{"SIGPOLL", int(syscall.SIGPOLL)}, {"SIGPWR", int(syscall.SIGPWR)}}
)
