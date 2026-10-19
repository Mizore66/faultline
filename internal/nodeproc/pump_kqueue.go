//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package nodeproc

import "syscall"

// kqueuer is libuv's BSD and macOS backend: EVFILT_READ filters without
// EV_CLEAR. The kernel queues a knote at the tail when it fires and queues
// a still-active one again after reporting it, so kevent returns the
// streams in the order their data arrived.
type kqueuer struct {
	fd     int
	tokens map[int]int // descriptor -> token
	events []syscall.Kevent_t
}

func newPoller() (poller, error) {
	syscall.ForkLock.RLock()
	fd, err := syscall.Kqueue()
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	return &kqueuer{fd: fd, tokens: map[int]int{}, events: make([]syscall.Kevent_t, 1024)}, nil
}

func (p *kqueuer) change(fd, flags int) error {
	var ev syscall.Kevent_t
	syscall.SetKevent(&ev, fd, syscall.EVFILT_READ, flags)
	_, err := syscall.Kevent(p.fd, []syscall.Kevent_t{ev}, nil, nil)
	return err
}

func (p *kqueuer) add(fd, token int) error {
	p.tokens[fd] = token
	return p.change(fd, syscall.EV_ADD)
}

func (p *kqueuer) remove(fd int) {
	p.change(fd, syscall.EV_DELETE)
	delete(p.tokens, fd)
}

func (p *kqueuer) wait(tokens []int) (int, error) {
	n, err := syscall.Kevent(p.fd, nil, p.events[:len(tokens)], nil)
	if err != nil {
		return 0, err
	}
	for i := range n {
		token, ok := p.tokens[int(p.events[i].Ident)]
		if !ok {
			token = -1
		}
		tokens[i] = token
	}
	return n, nil
}

func (p *kqueuer) close() { syscall.Close(p.fd) }
