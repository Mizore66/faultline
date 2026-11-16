//go:build linux || darwin

package proofview

import (
	"net"
	"runtime"
	"syscall"
	"unsafe"
)

// newUnixSocketRemainder borrows an exclusively owned, deadline-free TCP socket.
// The controller owns prepared RawConn.Write cycles and closes this connection
// on Close. This adapter makes no HTTP, logical-retirement or loop-turn decision.
func newUnixSocketRemainder(conn *net.TCPConn) (*unixRemainderWriter, error) {
	if conn == nil {
		return nil, net.ErrClosed
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	return newUnixRemainderWithDeps(unixRemainderDeps{
		Raw: raw, Writev: unixRemainderWritev, Close: conn.Close,
	}), nil
}

// unixRemainderWritev is called only inside an already prepared RawConn.Write
// callback, with the controller's bounded, nonempty vectors. Never nest another
// RawConn.Write here: prepareWrite must precede this attempt, not follow it.
func unixRemainderWritev(fd uintptr, parts [][]byte) (int, bool, error) {
	iovs := make([]syscall.Iovec, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		iov := syscall.Iovec{Base: &part[0]}
		iov.SetLen(len(part))
		iovs = append(iovs, iov)
	}
	if len(iovs) == 0 {
		return 0, false, nil
	}
	var n int
	var errno syscall.Errno
	for {
		r, _, e := syscall.Syscall(syscall.SYS_WRITEV, fd, uintptr(unsafe.Pointer(&iovs[0])), uintptr(len(iovs)))
		if e == syscall.EINTR {
			continue
		}
		errno = e
		if e == 0 {
			n = int(r)
		}
		break
	}
	runtime.KeepAlive(parts)
	runtime.KeepAlive(iovs)
	if errno == syscall.EAGAIN {
		return 0, true, nil
	}
	if errno != 0 {
		return 0, false, errno
	}
	return n, false, nil
}
