//go:build linux || darwin

package boot

import (
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// raiseNofile is the RLIMIT_NOFILE raise in Node's PlatformInit: the soft
// limit goes to the hard limit, or, when the hard limit is infinite, to the
// value Node's bisection settles on. Go's syscall package raises the soft
// limit too, but puts the original back in every process it starts; calling
// syscall.Setrlimit tells it not to, so git inherits the raised limit as it
// does under Node.
//
// Node starts its bisection at the soft limit fl inherited, which the Go
// runtime has already replaced (with kern.maxfilesperproc on macOS) and keeps
// out of reach. With an infinite hard limit the raise therefore waits for
// the first child process: until Setrlimit is called, Go still restores the
// original limit in children, so a shell started first reports it (round 6:
// an inherited soft limit of 2^20 or more, the default in some macOS shells,
// gave git 1,048,575 where Node gives 1,048,576 or no limit).
func raiseNofile() {
	var lim syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim) != nil || lim.Cur == lim.Max {
		return
	}
	if lim.Max != rlimInfinity {
		lim.Cur = lim.Max
		syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
		return
	}
	deferredNofile = true
}

// deferredNofile is set when the raise waits for BeforeSpawn.
var deferredNofile bool

var spawnOnce sync.Once

// BeforeSpawn must run before fl starts any child process; it completes a
// deferred RLIMIT_NOFILE raise.
func BeforeSpawn() {
	spawnOnce.Do(func() {
		if deferredNofile {
			bisectNofile(inheritedSoftNofile())
		}
	})
}

// inheritedSoftNofile asks a shell, which Go starts with the limit fl
// inherited, for its soft RLIMIT_NOFILE. If that fails, the current soft
// limit stands in (the bisection then gives 1,048,575, as it did before).
func inheritedSoftNofile() uint64 {
	var lim syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim)
	// syscall.ForkExec rather than os/exec, which boot must not import
	// (it initializes first); both restore the inherited limit.
	var p [2]int
	if syscall.Pipe(p[:]) != nil {
		return lim.Cur
	}
	pid, err := syscall.ForkExec("/bin/sh", []string{"sh", "-c", "ulimit -Sn"}, &syscall.ProcAttr{Files: []uintptr{0, uintptr(p[1]), 2}})
	syscall.Close(p[1])
	var out []byte
	buf := make([]byte, 64)
	for err == nil {
		n, rerr := syscall.Read(p[0], buf)
		if n <= 0 || rerr != nil {
			break
		}
		out = append(out, buf[:n]...)
	}
	syscall.Close(p[0])
	if err != nil {
		return lim.Cur
	}
	var ws syscall.WaitStatus
	for {
		if _, werr := syscall.Wait4(pid, &ws, 0, nil); werr != syscall.EINTR {
			break
		}
	}
	s := strings.TrimSpace(string(out))
	if s == "unlimited" {
		return rlimInfinity
	}
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n
	}
	return lim.Cur
}

// bisectNofile is Node's loop (src/node.cc PlatformInit) with rlim_t's
// unsigned arithmetic, starting from the inherited soft limit: a do-while,
// so it tries at least once, and the midpoint wraps when the start is above
// 2^20. The kernel keeps the last value it accepted, as it does for Node.
func bisectNofile(soft uint64) {
	lim := syscall.Rlimit{Cur: soft, Max: rlimInfinity}
	syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim) // Node's starting state
	lo, hi := soft, uint64(1)<<20
	for {
		lim.Cur = lo + (hi-lo)/2
		if syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim) != nil {
			hi = lim.Cur
		} else {
			lo = lim.Cur
		}
		if lo+1 >= hi {
			break
		}
	}
}
