package nodeproc

import "syscall"

// epoller is libuv's Linux backend: a level-triggered epoll set. The kernel
// queues a file on its ready list when it becomes readable and re-queues a
// still-readable one at the tail after reporting it, so epoll_wait returns
// the streams in the order their data arrived.
type epoller struct {
	fd     int
	events []syscall.EpollEvent
}

func newPoller() (poller, error) {
	fd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return &epoller{fd: fd, events: make([]syscall.EpollEvent, 1024)}, nil
}

func (p *epoller) add(fd, token int) error {
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(token)}
	return syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_ADD, fd, &ev)
}

func (p *epoller) remove(fd int) { syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_DEL, fd, nil) }

func (p *epoller) wait(tokens []int) (int, error) {
	n, err := syscall.EpollWait(p.fd, p.events[:len(tokens)], -1)
	if err != nil {
		return 0, err
	}
	for i := range n {
		tokens[i] = int(p.events[i].Fd)
	}
	return n, nil
}

func (p *epoller) close() { syscall.Close(p.fd) }
