//go:build linux || darwin

package boot

import "syscall"

// raiseNofile is the RLIMIT_NOFILE raise in Node's PlatformInit: the soft
// limit goes to the hard limit, or, when the hard limit is infinite, to the
// highest value setrlimit accepts below 2^20, found by bisection. Go's
// syscall package raises the soft limit too, but puts the original back in
// every process it starts; calling syscall.Setrlimit tells it not to, so
// git inherits the raised limit as it does under Node.
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
	lo, hi := lim.Cur, uint64(1)<<20
	for lo+1 < hi {
		lim.Cur = lo + (hi-lo)/2
		if syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim) != nil {
			hi = lim.Cur
		} else {
			lo = lim.Cur
		}
	}
	lim.Cur = lo
	syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
}

// rlimInfinity is RLIM_INFINITY, all ones on every Unix Go supports.
const rlimInfinity = ^uint64(0)
