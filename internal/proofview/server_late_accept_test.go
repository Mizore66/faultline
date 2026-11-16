package proofview

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// gatedListener's Accept announces that it is inside the raw accept, waits
// to be released, then returns a connection: the point where the kernel has
// already handed over a socket but nodeListener has not yet registered it.
// Close does not unblock Accept, as when the accept had already completed.
type gatedListener struct {
	entered chan struct{}
	release chan struct{}
	conn    net.Conn
}

func (l *gatedListener) Accept() (net.Conn, error) {
	close(l.entered)
	<-l.release
	return l.conn, nil
}

func (l *gatedListener) Close() error   { return nil }
func (l *gatedListener) Addr() net.Addr { return &net.TCPAddr{} }

// TestNodeListenerRejectsConnectionAcceptedDuringClose holds a raw accepted
// connection before nodeListener can register it, completes Server.Close
// (which sees no connection and reports drained), then releases the accept.
// The late socket must be closed and rejected, never tracked: tracking it
// would let its later destruction close drained a second time.
func TestNodeListenerRejectsConnectionAcceptedDuringClose(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	gl := &gatedListener{entered: make(chan struct{}), release: make(chan struct{}), conn: server}
	s := &Server{
		listener: gl,
		stop:     make(chan struct{}),
		conns:    map[*nodeConn]struct{}{},
		drained:  make(chan struct{}),
	}

	type accepted struct {
		conn net.Conn
		err  error
	}
	acceptResult := make(chan accepted, 1)
	go func() {
		conn, err := nodeListener{gl, s}.Accept()
		acceptResult <- accepted{conn, err}
	}()
	<-gl.entered

	closeResult := make(chan error, 1)
	go func() { closeResult <- s.Close() }()
	if err := <-closeResult; err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-s.drained:
	default:
		t.Fatal("Close returned without drained closed")
	}

	close(gl.release)
	got := <-acceptResult
	if got.err == nil {
		// Report the defect, and what the late connection then does.
		var panicked any
		func() {
			defer func() { panicked = recover() }()
			if nc, ok := got.conn.(*nodeConn); ok {
				nc.destroy()
			}
		}()
		t.Errorf("Accept after Close returned a connection (tracked: %d); closing it panicked: %v", len(s.conns), panicked)
		return
	}
	if !errors.Is(got.err, net.ErrClosed) {
		t.Errorf("Accept error = %v, want net.ErrClosed", got.err)
	}
	s.mu.Lock()
	tracked := len(s.conns)
	s.mu.Unlock()
	if tracked != 0 {
		t.Errorf("%d connections tracked after Close", tracked)
	}

	// The rejected socket must be closed: the peer sees EOF, not a hang.
	var wg sync.WaitGroup
	wg.Add(1)
	readErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := peer.Read(make([]byte, 1))
		readErr <- err
	}()
	select {
	case err := <-readErr:
		if err == nil {
			t.Error("peer read succeeded on a rejected connection")
		}
	case <-time.After(30 * time.Second):
		peer.Close()
		wg.Wait()
		t.Error("rejected connection was not closed")
	}
}
