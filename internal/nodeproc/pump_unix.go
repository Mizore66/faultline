//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package nodeproc

import (
	"os"
	"syscall"
)

// poller is the kernel's readiness queue libuv's uv__io_poll waits on:
// epoll on Linux, kqueue on the BSDs. wait reports the ready streams'
// tokens in the order the kernel returns them, which is the order they
// became readable.
type poller interface {
	add(fd, token int) error
	remove(fd int)
	wait(tokens []int) (int, error)
	close()
}

// pumpPolled is libuv's loop over the piped streams: wait for readiness,
// then for each ready stream, in the kernel's order, uv__read it. It
// returns the streams still open should the poller fail (none normally).
func pumpPolled(streams []pipedStream, _ uintptr) []pipedStream {
	p, err := newPoller()
	if err != nil {
		return streams
	}
	defer p.close()
	fds := make([]int, len(streams))
	open := make([]bool, len(streams))
	for i, s := range streams {
		fds[i] = rawFd(s.f)
		if fds[i] < 0 || p.add(fds[i], i) != nil {
			return remaining(streams, open, i)
		}
		open[i] = true
	}
	left := len(streams)
	tokens := make([]int, 1024) // libuv's epoll and kqueue batch
	for left > 0 {
		n, err := p.wait(tokens)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return remaining(streams, open, len(streams))
		}
		for _, i := range tokens[:n] {
			if i < 0 || i >= len(streams) || !open[i] {
				continue
			}
			if readStream(streams[i]) {
				p.remove(fds[i])
				streams[i].f.Close()
				open[i] = false
				left--
			}
		}
	}
	return nil
}

// remaining is the streams still open, plus those from index from on that
// were never added.
func remaining(streams []pipedStream, open []bool, from int) []pipedStream {
	var out []pipedStream
	for i, s := range streams {
		if open[i] || i >= from {
			out = append(out, s)
		}
	}
	return out
}

// rawFd is f's descriptor, without File.Fd's switch to blocking mode.
func rawFd(f *os.File) int {
	rc, err := f.SyscallConn()
	if err != nil {
		return -1
	}
	fd := -1
	rc.Control(func(raw uintptr) { fd = int(raw) })
	return fd
}

// readStream is libuv's uv__read on a stream the poller reported: up to 32
// reads of 64 KiB, each handed to the listener at once (Node emits 'data'
// from the read callback), stopping at a read that comes back short or
// would block. It reports whether the stream ended: EOF, or an error,
// which for a socket the child holds ends the stream as well.
func readStream(s pipedStream) bool {
	rc, err := s.f.SyscallConn()
	if err != nil {
		return true
	}
	var buf []byte
	for count := 32; count > 0; count-- {
		// A read that returns no data leaves the buffer for the next one.
		if buf == nil {
			buf = make([]byte, segment)
		}
		var n int
		var rerr error
		rc.Read(func(fd uintptr) bool {
			for {
				n, rerr = syscall.Read(int(fd), buf)
				if rerr != syscall.EINTR {
					return true
				}
			}
		})
		switch {
		case rerr == syscall.EAGAIN:
			return false
		case rerr != nil || n <= 0:
			return true
		}
		data := buf[:n]
		buf = nil // the listener owns data
		if s.listen != nil {
			s.listen(data)
		}
		if n < segment {
			return false
		}
	}
	return false
}
