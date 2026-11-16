//go:build windows

package proofview

import (
	"math"
	"runtime"
	"syscall"
)

// sockFIONBIO is the Winsock ioctl _IOW('f', 126, u_long); syscall.SetNonblock
// is a no-op on Windows and is not used.
const sockFIONBIO = 0x8004667E

const sockWSAEWOULDBLOCK = syscall.Errno(10035)

type rawSocketTryWriter struct {
	raw syscall.RawConn
}

// newRawSocketTryWriter sets FIONBIO=1 once on the caller-owned socket. It is
// left on until the socket closes; it is never toggled or restored.
func newRawSocketTryWriter(raw syscall.RawConn) (socketTryWriter, error) {
	var ioerr error
	if err := raw.Control(func(fd uintptr) {
		in := [4]byte{1}
		var ret uint32
		ioerr = syscall.WSAIoctl(syscall.Handle(fd), sockFIONBIO, &in[0], 4, nil, 0, &ret, nil, 0)
		runtime.KeepAlive(&in)
	}); err != nil {
		return nil, err
	}
	if ioerr != nil {
		return nil, ioerr
	}
	return &rawSocketTryWriter{raw: raw}, nil
}

// Try issues one NULL-overlapped WSASend inside a RawConn.Write callback that
// always returns true. A part longer than a WSABuf can describe is split into
// consecutive WSABufs, and the attempt is limited to a MaxUint32-byte prefix
// (the DWORD byte count); the remainder is simply unaccepted, which the caller
// accounts for exactly like any partial send.
func (w *rawSocketTryWriter) Try(parts [][]byte) (int, bool, error) {
	const maxChunk = math.MaxUint32
	bufs := make([]syscall.WSABuf, 0, len(parts))
	budget := uint64(maxChunk)
build:
	for _, p := range parts {
		for len(p) > 0 && budget > 0 {
			c := uint64(len(p))
			if c > budget {
				c = budget
			}
			bufs = append(bufs, syscall.WSABuf{Len: uint32(c), Buf: &p[0]})
			budget -= c
			p = p[c:]
		}
		if budget == 0 {
			break build
		}
	}
	if len(bufs) == 0 {
		return 0, false, nil
	}
	var sent uint32
	var serr error
	werr := w.raw.Write(func(fd uintptr) bool {
		serr = syscall.WSASend(syscall.Handle(fd), &bufs[0], uint32(len(bufs)), &sent, 0, nil, nil)
		return true
	})
	runtime.KeepAlive(parts)
	runtime.KeepAlive(bufs)
	if werr != nil {
		return 0, false, werr
	}
	if serr == sockWSAEWOULDBLOCK {
		return 0, true, nil
	}
	if serr != nil {
		return 0, false, serr
	}
	return int(sent), false, nil
}
