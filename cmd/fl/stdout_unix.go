//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const isWindows = false

func isWindowsPipeClosed(syscall.Errno) bool { return false }

// guessHandle is uv_guess_handle (src/unix/core.c).
func guessHandle(f *os.File) handleType {
	fd := int(f.Fd())
	if isatty(fd) {
		return handleTTY
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil {
		return handleUnknown
	}
	switch st.Mode & syscall.S_IFMT {
	case syscall.S_IFREG, syscall.S_IFCHR:
		return handleFile
	case syscall.S_IFIFO:
		return handlePipe
	case syscall.S_IFSOCK:
	default:
		return handleUnknown
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		return handleUnknown
	}
	typ, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil {
		return handleUnknown
	}
	inet := false
	switch sa.(type) {
	case *syscall.SockaddrInet4, *syscall.SockaddrInet6:
		inet = true
	}
	switch {
	case typ == syscall.SOCK_DGRAM && inet:
		return handleUDP
	case typ == syscall.SOCK_STREAM && inet:
		return handleTCP
	case typ == syscall.SOCK_STREAM:
		if _, ok := sa.(*syscall.SockaddrUnix); ok {
			return handlePipe
		}
	}
	return handleUnknown
}

func isatty(fd int) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// readOnly reports whether fd's access mode is O_RDONLY, which makes libuv
// open a terminal or pipe as a stream that cannot be written to.
func readOnly(f *os.File) bool {
	fl, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFL, 0)
	return e == 0 && fl&syscall.O_ACCMODE == syscall.O_RDONLY
}

// writeOnce is fs.writeSync: one write(2), whose count is not checked.
func writeOnce(f *os.File, p []byte) error {
	for {
		_, err := syscall.Write(int(f.Fd()), p)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		return nil
	}
}

// writeAll is a libuv stream write: everything is written, and while the
// descriptor would block the writer waits for it to become writable.
func writeAll(f *os.File, p []byte) error {
	fd := int(f.Fd())
	for len(p) > 0 {
		n, err := syscall.Write(fd, p)
		switch err {
		case nil:
			p = p[n:]
		case syscall.EINTR:
		case syscall.EAGAIN, syscall.ENOBUFS: // uv__try_write
			waitWritable(fd)
		default:
			return streamWriteErrno(err)
		}
	}
	return nil
}

// streamWriteErrno is uv__try_write's error mapping: on Apple systems a
// write to a socket being torn down fails with EPROTOTYPE, which libuv
// reports as ECONNRESET.
func streamWriteErrno(err error) error {
	if err == syscall.EPROTOTYPE && (runtime.GOOS == "darwin" || runtime.GOOS == "ios") {
		return syscall.ECONNRESET
	}
	return err
}

// writeTTY is writeAll: a terminal is a libuv stream on Unix.
func writeTTY(f *os.File, p []byte) error { return writeAll(f, p) }
