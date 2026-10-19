//go:build linux && (amd64 || 386 || arm || arm64 || ppc64 || ppc64le || s390x)

package sigexit

// The generic signal ABI (include/uapi/asm-generic/signal.h): 64 signals,
// SIG_BLOCK 0, SIG_UNBLOCK 1, SIG_SETMASK 2.
const (
	MaxSignal  = 64
	sigUnblock = 1
	sigSetmask = 2
)

// sigaction is the kernel's struct sigaction where the architecture has
// sa_restorer (__ARCH_HAS_SA_RESTORER).
type sigaction struct {
	handler  uintptr
	flags    uintptr // unsigned long
	restorer uintptr
	mask     Set
}
