//go:build linux || darwin || windows

package proofview

import "net"

// socketTryWriter makes one physical, nonblocking transfer attempt of the
// ordered byte vectors in parts. It is a bytes-only backend: it knows nothing
// about HTTP framing, queues or logical watermarks.
//
// n is the exact accepted prefix of the concatenated parts (it may be partial).
// wouldBlock is true only for a native EAGAIN/WSAEWOULDBLOCK, and n is then 0.
// Every other native or RawConn error is returned unchanged. There is no
// hidden retry (other than Unix EINTR), polling, deadline or goroutine.
//
// The caller must own the connection exclusively and must never call Try while
// another Go Write or pending native write completion owns the descriptor. The
// parts are read synchronously and must not be mutated during the call.
type socketTryWriter interface {
	Try(parts [][]byte) (n int, wouldBlock bool, err error)
}

// newSocketTryWriter prepares conn, which the caller exclusively owns, for
// Try. Platform specifics live in newRawSocketTryWriter.
func newSocketTryWriter(conn *net.TCPConn) (socketTryWriter, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	return newRawSocketTryWriter(raw)
}
