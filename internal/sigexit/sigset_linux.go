package sigexit

import (
	"syscall"
	"unsafe"
)

// wordBits is the width of the kernel's sigset_t word (unsigned long).
const wordBits = 8 * unsafe.Sizeof(uintptr(0))

// Set is the kernel's sigset_t: unsigned long[_NSIG / bits per long], so
// the bit order matches on big-endian architectures too. Signal n is bit
// n-1.
type Set [MaxSignal / wordBits]uintptr

// sigsetSize is the sigsetsize rt_sigprocmask, rt_sigpending and
// rt_sigaction require: exactly sizeof(sigset_t).
const sigsetSize = unsafe.Sizeof(Set{})

// Add adds sig to s.
func (s *Set) Add(sig syscall.Signal) {
	if sig > 0 && int(sig) <= MaxSignal {
		s[uintptr(sig-1)/wordBits] |= 1 << (uintptr(sig-1) % wordBits)
	}
}

// Has reports whether sig is in s.
func (s Set) Has(sig syscall.Signal) bool {
	return sig > 0 && int(sig) <= MaxSignal && s[uintptr(sig-1)/wordBits]&(1<<(uintptr(sig-1)%wordBits)) != 0
}

// Empty reports whether s holds no signal.
func (s Set) Empty() bool { return s == Set{} }

// Mask is this thread's signal mask (rt_sigprocmask with no new set).
func Mask() Set {
	var old Set
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 0, 0, uintptr(unsafe.Pointer(&old)), sigsetSize, 0, 0)
	return old
}

// SetMask replaces this thread's signal mask (SIG_SETMASK).
func SetMask(s Set) {
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, sigSetmask, uintptr(unsafe.Pointer(&s)), 0, sigsetSize, 0, 0)
}

// Pending is the set of signals pending for this thread or the process.
func Pending() Set {
	var s Set
	syscall.RawSyscall(syscall.SYS_RT_SIGPENDING, uintptr(unsafe.Pointer(&s)), sigsetSize, 0)
	return s
}

// unblock removes sig from this thread's mask (SIG_UNBLOCK).
func unblock(sig syscall.Signal) {
	var s Set
	s.Add(sig)
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, sigUnblock, uintptr(unsafe.Pointer(&s)), 0, sigsetSize, 0, 0)
}
