//go:build linux

package main

// Process-group pidfds: the only way this helper ever signals a process.
// No numeric pid or tid is ever the target of a signal. A pidfd names one
// process incarnation: a signal through it reaches that incarnation or fails
// with ESRCH, never a process that later reused the number.
//
// ACQUISITION RULES (ownership first; procfs is never an ownership proof):
//  1. The root: CLONE_PIDFD through SysProcAttr.PidFD at the ForkExec itself.
//  2. A new process-group LEADER: pidfd_open(tid, 0) at the tracee's FIRST
//     observed stop, before the helper ever resumes it. Success is the only
//     outcome that creates a handle.
//  3. pidfd_open(tid, 0) fails for a non-leader thread (ENOENT observed). That
//     proves nothing by itself; it only prompts a LOOKUP: accepted only if
//     /proc/<tid>/status yields a parsed Tgid != tid AND a handle for exactly
//     that Tgid is already held with live observed members; otherwise
//     group_unknown (unhealthy).
//
// A pidfd is never opened from an event's child id, from a reaped entry, or
// from a number the helper has not just seen stop unreaped.
//
// Non-leader execve is an UNSUPPORTED case (see execEvent in main_linux.go):
// it is reconciled without a phantom but HEALTH stays false. A group whose
// pidfd_send_signal failed with anything but ESRCH is marked `unsignalled`:
// never waited for, left to PTRACE_O_EXITKILL, failure loud.
//
// Requirements, checked at run time before any tracee is launched
// (probePidfd): Linux 5.3 (pidfd_open), 5.1 (pidfd_send_signal), and Linux 5.2
// with Go 1.23 for CLONE_PIDFD through SysProcAttr.PidFD. No thread pidfd (6.9)
// is used or required. There is no wait-only fallback.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	sysPidfdSendSignal = 424 // same number on arm64 and amd64
	sysPidfdOpen       = 434
)

type group struct {
	tgid        int
	fd          int
	members     int  // live observed tracees of this thread group
	signalled   bool // the group SIGKILL was delivered (or the incarnation was already gone: ESRCH)
	unsignalled bool // the group signal FAILED (not ESRCH): never wait for this group
}

func pidfdOpen(pid int) (int, error) {
	fd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if e != 0 {
		return -1, e
	}
	return int(fd), nil
}

func pidfdSendSignal(fd int, sig syscall.Signal) error {
	_, _, e := syscall.Syscall6(sysPidfdSendSignal, uintptr(fd), uintptr(sig), 0, 0, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

// probePidfd proves the pidfd facility works on this host by opening a pidfd
// for this very process and sending it signal 0 (nothing is delivered).
func probePidfd() error {
	fd, err := pidfdOpen(os.Getpid())
	if err != nil {
		return fmt.Errorf("pidfd_open: %w", err)
	}
	serr := pidfdSendSignal(fd, 0)
	if cerr := syscall.Close(fd); cerr != nil {
		return fmt.Errorf("closing the probe pidfd: %w", cerr)
	}
	if serr != nil {
		return fmt.Errorf("pidfd_send_signal(0): %w", serr)
	}
	return nil
}

// needsGroupLookup: pidfd_open(tid) answered ENOENT, the observed answer for a
// non-leader. It is a prompt for a procfs LOOKUP into held handles, nothing more.
func needsGroupLookup(err error) bool { return errors.Is(err, syscall.ENOENT) }

// readTgid reads a stopped, unreaped thread's group id. It is a lookup key
// into already-held handles ONLY, never an ownership proof.
func readTgid(tid int) (int, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(tid) + "/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Tgid:"); ok {
			n, perr := strconv.Atoi(strings.TrimSpace(v))
			if perr != nil || n <= 0 {
				return 0, fmt.Errorf("malformed Tgid line %q in /proc/%d/status", line, tid)
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("no Tgid line in /proc/%d/status", tid)
}
