package sigexit

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Set is the kernel's sigset_t for this architecture: the kernel accepts
// its size (rt_sigprocmask refuses any other), and a signal blocked
// through Set reads back from the kernel's own mask.
func TestSetMatchesKernelSigset(t *testing.T) {
	if got := int(unsafe.Sizeof(Set{})) * 8; got != MaxSignal {
		t.Fatalf("Set holds %d bits, want %d", got, MaxSignal)
	}
	var s Set
	s.Add(syscall.SIGUSR2)
	s.Add(MaxSignal)
	if !s.Has(syscall.SIGUSR2) || !s.Has(MaxSignal) || s.Has(syscall.SIGUSR1) || s.Has(0) || s.Has(MaxSignal+1) {
		t.Fatalf("Add/Has: %v", s)
	}
	// Block SIGUSR2 on a locked thread and read it back through Mask.
	done := make(chan Set)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var block Set
		block.Add(syscall.SIGUSR2)
		old := Mask()
		var with Set
		for i := range with {
			with[i] = old[i] | block[i]
		}
		_, _, e := syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, sigSetmask, uintptr(unsafe.Pointer(&with)), 0, sigsetSize, 0, 0)
		if e != 0 {
			t.Errorf("rt_sigprocmask(SIG_SETMASK) with sigsetsize %d: %v", sigsetSize, e)
		}
		got := Mask()
		SetMask(old)
		done <- got
	}()
	if got := <-done; !got.Has(syscall.SIGUSR2) {
		t.Fatalf("SIGUSR2 not in the kernel's mask: %v", got)
	}
	var old sigaction
	if _, _, e := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(syscall.SIGUSR2), 0, uintptr(unsafe.Pointer(&old)), sigsetSize, 0, 0); e != 0 {
		t.Fatalf("rt_sigaction with sigsetsize %d: %v", sigsetSize, e)
	}
}
