//go:build unix

package boot

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// Node's PlatformInit resets every signal to SIG_DFL except SIGPIPE and
// SIGXFSZ, which it ignores, and clears the signal mask. Go instead keeps
// an inherited mask, keeps inherited SIG_IGN for HUP, INT and the stop
// signals, and handles the rest itself: it ignores signals nobody asked for
// (ALRM, USR2, IO, real-time signals, ...) and dumps goroutines for QUIT,
// ABRT and externally sent SEGV, BUS, TRAP and the like. So: on Linux, a
// blocked inherited mask is cleared by re-executing fl once and inherited
// SIG_IGN on the stop signals is reset in the kernel; SIGPIPE and SIGXFSZ
// are caught and dropped (a caught signal is SIG_DFL again in git, as
// libuv resets every signal in its children); and every signal whose
// default action ends the process ends fl by that action (sigexit.Die).
// SIGUSR1 (Node's inspector) and SIGPROF (the runtime's) are not emulated
// (KNOWN_DIFFERENCES.md).
//
// The order matters. The handlers are in place before the mask is cleared,
// so a signal left pending under the inherited mask gets Node's treatment,
// and the re-exec happens before the standard descriptors are marked
// close-on-exec, which would leave the new image with /dev/null on 0 to 2.
func init() {
	dieOfPending()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, terminating...)
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE, syscall.SIGXFSZ)
	go func() {
		sigexit.Die((<-ch).(syscall.Signal))
	}()
	resetInherited()
	disableStdioInheritance()
	raiseNofile()
}

// disableStdioInheritance is libuv's uv_disable_stdio_inheritance, which
// Node calls at startup: descriptors 0 to 15 are marked close-on-exec
// unconditionally, then each one above until the first that is not open,
// so descriptors fl inherited do not reach git. Node holds no descriptors
// of its own by then; the Go runtime does (a cgroup file, netpoll's epoll
// and eventfd), and they could fill the gap where libuv's scan stops. They
// are close-on-exec already, which an inherited descriptor never is, so the
// scan above 15 also stops at one.
func disableStdioInheritance() {
	for fd := 0; ; fd++ {
		if fd > 15 {
			if flags, ok := fdFlags(fd); !ok || flags&syscall.FD_CLOEXEC != 0 {
				break
			}
		}
		if !setCloexec(fd) && fd > 15 {
			break
		}
	}
}
