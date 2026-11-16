//go:build linux || darwin

package proofview

import (
	"runtime"
	"syscall"
	"unsafe"
)

type rawSocketTryWriter struct {
	raw syscall.RawConn
}

// newRawSocketTryWriter needs no socket mode initialization: writev on a Go
// net socket is already nonblocking.
func newRawSocketTryWriter(raw syscall.RawConn) (socketTryWriter, error) {
	return &rawSocketTryWriter{raw: raw}, nil
}

// Try issues exactly one real writev inside a single RawConn.Write callback
// that always returns true (so the runtime never polls). Only EINTR is retried.
//
// unsafe: iovs points into the parts backing arrays; both stay referenced
// through runtime.KeepAlive after the callback, and the iovec array pointer is
// converted to uintptr directly in the Syscall argument list.
func (w *rawSocketTryWriter) Try(parts [][]byte) (int, bool, error) {
	iovs := make([]syscall.Iovec, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		iov := syscall.Iovec{Base: &p[0]}
		iov.SetLen(len(p))
		iovs = append(iovs, iov)
	}
	if len(iovs) == 0 {
		return 0, false, nil
	}
	var n int
	var errno syscall.Errno
	werr := w.raw.Write(func(fd uintptr) bool {
		for {
			r, _, e := syscall.Syscall(syscall.SYS_WRITEV, fd, uintptr(unsafe.Pointer(&iovs[0])), uintptr(len(iovs)))
			if e == syscall.EINTR {
				continue
			}
			if e != 0 {
				errno = e
			} else {
				n = int(r)
			}
			return true
		}
	})
	runtime.KeepAlive(parts)
	runtime.KeepAlive(iovs)
	if werr != nil {
		return 0, false, werr
	}
	if errno == syscall.EAGAIN {
		return 0, true, nil
	}
	if errno != 0 {
		return 0, false, errno
	}
	return n, false, nil
}
