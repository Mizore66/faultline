//go:build linux && (mips || mipsle || mips64 || mips64le)

package sigexit

// The MIPS signal ABI (arch/mips/include/uapi/asm/signal.h): _NSIG 128,
// SIG_BLOCK 1, SIG_UNBLOCK 2, SIG_SETMASK 3.
const (
	MaxSignal  = 128
	sigUnblock = 2
	sigSetmask = 3
)

// sigaction is MIPS's struct sigaction: sa_flags (unsigned int) comes
// first, and there is no sa_restorer.
type sigaction struct {
	flags   uint32
	handler uintptr
	mask    Set
}
