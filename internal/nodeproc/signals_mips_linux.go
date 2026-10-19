//go:build linux && (mips || mipsle || mips64 || mips64le)

package nodeproc

import "syscall"

// stkfltSignal and ioAliasSignals are the entries of Node's signo_string
// and os.constants.signals that exist only on some systems. MIPS has no
// SIGSTKFLT, and numbers SIGPOLL (= SIGIO, 22) and SIGPWR (19) its own way
// (arch/mips/include/uapi/asm/signal.h); Node lists neither SIGEMT nor
// SIGLOST there.
var (
	stkfltSignal   []namedSignal
	ioAliasSignals = []namedSignal{{"SIGPOLL", int(syscall.SIGPOLL)}, {"SIGPWR", int(syscall.SIGPWR)}}
)
