package proofview

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestNodeServerCloseWaitsForActiveResponse drives a real loopback POST whose
// body the framing has fully consumed while the handler has not yet answered.
// Node's closeIdleConnections spares such a connection (its response is still
// attached); Close must neither classify it idle nor destroy it before the
// response is delivered.
func TestNodeServerCloseWaitsForActiveResponse(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s, err := startNodeServer(0, func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "done")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var released bool
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}

	type reply struct {
		status int
		body   string
		err    error
	}
	replied := make(chan reply, 1)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	go func() {
		res, err := client.Post(s.URL()+"/", "application/x-www-form-urlencoded", strings.NewReader("a=b"))
		if err != nil {
			replied <- reply{err: err}
			return
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		replied <- reply{res.StatusCode, string(b), err}
	}()

	closed := make(chan error, 1)
	closeStarted, closeJoined, replyJoined := false, false, false
	// Whatever happens below, the handler is released and every goroutine is
	// joined before the test returns.
	defer func() {
		releaseOnce()
		if !closeStarted {
			go func() { closed <- s.Close() }()
		}
		if !closeJoined {
			<-closed
		}
		if !replyJoined {
			<-replied
		}
	}()

	select {
	case <-entered:
	case r := <-replied:
		t.Fatalf("request ended before the handler ran: %+v", r)
	}

	// The handler is blocked: the POST body was read to the end (the handler
	// is only entered after the head, and the 3 byte body is in the same
	// segment), no response byte exists yet.
	s.mu.Lock()
	var conns []*nodeConn
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	if len(conns) != 1 {
		t.Fatalf("tracked connections = %d, want 1", len(conns))
	}
	if conns[0].idle() {
		t.Errorf("active-response/close defect: a connection with a request in its handler and no response written is classified idle, so Close destroys it")
	}

	closeStarted = true
	go func() { closed <- s.Close() }()
	releaseOnce()
	r := <-replied
	replyJoined = true
	if r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Errorf("active-response/close defect: reply = status %d body %q err %v, want 200 \"done\"", r.status, r.body, r.err)
	}
	closeErr := <-closed
	closeJoined = true
	if closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}
}

// awaitClosing returns once Close has marked the server closing (the point
// after which closeIdleConnections has been decided). It yields instead of
// sleeping and fails the test at the deadline.
func awaitClosing(t *testing.T, s *Server) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing {
			return true
		}
		if time.Now().After(deadline) {
			t.Errorf("Close did not start closing")
			return false
		}
		runtime.Gosched()
	}
}

// expectEOF fails unless the peer closes conn well inside the 6 s keep-alive
// socket timeout, which would otherwise be the only thing closing it.
func expectEOF(t *testing.T, conn net.Conn, what string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	if n, err := conn.Read(b[:]); n != 0 || err != io.EOF {
		t.Errorf("%s: read = %d, %v; want EOF before the keep-alive timeout", what, n, err)
	}
}

// TestNodeServerCloseKeepAliveResponseClosesPromptly: a keep-alive request
// whose handler is still working when Close starts is spared, and its
// connection is closed as soon as the response is complete, not at the 6 s
// keep-alive timeout.
func TestNodeServerCloseKeepAliveResponseClosesPromptly(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s, err := startNodeServer(0, func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			nodeHeaders(w.Header(), r)
			writeNodeBody(w, r, http.StatusOK, "done")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp4", s.listener.Addr().String())
	if err != nil {
		close(release)
		s.Close()
		t.Fatal(err)
	}
	defer conn.Close()
	closed := make(chan error, 1)
	closeStarted := false
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if !closeStarted {
			go func() { closed <- s.Close() }()
		}
		<-closed
	}()
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\n\r\na=b"); err != nil {
		t.Fatal(err)
	}
	<-entered
	closeStarted = true
	go func() { closed <- s.Close() }()
	if !awaitClosing(t, s) {
		return
	}
	close(release)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || string(b) != "done" {
		t.Fatalf("reply = %d %q %v, want 200 \"done\"", res.StatusCode, b, err)
	}
	if res.Header.Get("Connection") != "keep-alive" {
		t.Fatalf("Connection = %q, want keep-alive", res.Header.Get("Connection"))
	}
	expectEOF(t, conn, "keep-alive connection after the response during Close")
}

// TestNodeServerCloseDestroysIdleKeepAliveConnection: a connection that has
// finished its response before Close is still destroyed at once.
func TestNodeServerCloseDestroysIdleKeepAliveConnection(t *testing.T) {
	s, err := startNodeServer(0, func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			nodeHeaders(w.Header(), r)
			writeNodeBody(w, r, http.StatusOK, "done")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp4", s.listener.Addr().String())
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	defer conn.Close()
	closed := make(chan error, 1)
	closeStarted := false
	defer func() {
		if !closeStarted {
			go func() { closed <- s.Close() }()
		}
		<-closed
	}()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	closeStarted = true
	go func() { closed <- s.Close() }()
	expectEOF(t, conn, "idle keep-alive connection at Close")
}
