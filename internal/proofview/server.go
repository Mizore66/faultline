package proofview

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Server is the FaultLineServer startGitProofServer returns. Its
// connections follow Node's http.Server (lib/_http_server.js, Node 22's
// defaults) rather than net/http's own lifecycle:
//   - a connection is "idle" once its last request message is complete
//     (body included) and no byte of the next has arrived (the parser's
//     last_message_start_ is 0; a response still being written to a slow
//     reader does not count, as Node ends it at once); a new
//     connection is not idle until it has sent a whole request;
//   - after a keep-alive response, the socket gets an inactivity timeout of
//     keepAliveTimeout + keepAliveTimeoutBuffer (6 s), refreshed by every
//     read and write and cleared when the next request's headers arrive;
//     it is destroyed when the timeout fires, also mid-body;
//   - every 30 s (connectionsCheckingInterval, from listen until close) a
//     connection inside a message whose headers are not complete after
//     headersTimeout (60 s), or whose message is older than requestTimeout
//     (300 s), gets `408 Request Timeout` and is destroyed;
//   - close() destroys the idle connections, stops the 30 s check and the
//     listener, and completes once every other connection has closed, by
//     its timeout or by the client, however long that takes.
//
// The response is written as soon as the headers are in (Node calls the
// listener once the headers are parsed, and dumps an unread body after the
// response): the request framing in framing.go reads the bodies itself and
// hands net/http only the heads.
type Server struct {
	url      string
	srv      *http.Server
	listener net.Listener
	stop     chan struct{} // closed by Close: ends the 30 s check

	mu      sync.Mutex
	conns   map[*nodeConn]struct{}
	closing bool
	drained chan struct{} // closed once closing and no connection is left
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *Server) URL() string { return s.url }

// Node's http.Server timing defaults (lib/_http_server.js storeHTTPOptions).
const (
	keepAliveSocketTimeout      = 5*time.Second + time.Second // keepAliveTimeout + keepAliveTimeoutBuffer
	headersTimeout              = 60 * time.Second
	requestTimeout              = 300 * time.Second
	connectionsCheckingInterval = 30 * time.Second
)

// requestTimeoutResponse is what Node writes before destroying an expired
// connection (socketOnError, ERR_HTTP_REQUEST_TIMEOUT).
const requestTimeoutResponse = "HTTP/1.1 408 Request Timeout\r\nConnection: close\r\n\r\n"

// Close is server.close(): httpServerPreClose (closeIdleConnections, then
// the connections check stops), net.Server.close (the listener), and the
// callback once the last connection has closed.
func (s *Server) Close() error {
	close(s.stop)
	err := s.listener.Close()
	s.mu.Lock()
	s.closing = true
	var idle []*nodeConn
	for c := range s.conns {
		if c.idle() {
			idle = append(idle, c)
		}
	}
	if len(s.conns) == 0 {
		close(s.drained)
	}
	s.mu.Unlock()
	for _, c := range idle {
		c.destroy()
	}
	<-s.drained
	return err
}

// untrack forgets a closed connection and completes a pending Close when
// it was the last.
func (s *Server) untrack(c *nodeConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conns[c]; !ok {
		return
	}
	delete(s.conns, c)
	if s.closing && len(s.conns) == 0 {
		close(s.drained)
	}
}

// checkConnections is Node's checkConnections, every
// connectionsCheckingInterval until Close.
func (s *Server) checkConnections() {
	ticker := time.NewTicker(connectionsCheckingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			var expired []*nodeConn
			for c := range s.conns {
				if c.expired(now) {
					expired = append(expired, c)
				}
			}
			s.mu.Unlock()
			for _, c := range expired {
				c.requestTimedOut()
			}
		}
	}
}

// nodeListener tracks every accepted connection as a nodeConn.
type nodeListener struct {
	net.Listener
	s *Server
}

func (l nodeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	now := clock.Monotonic()
	c := &nodeConn{Conn: conn, s: l.s, inMessage: true, messageStart: now, lastActivity: now}
	c.cond = sync.NewCond(&c.mu)
	c.buf = make([]byte, 64<<10)
	l.s.mu.Lock()
	l.s.conns[c] = struct{}{}
	l.s.mu.Unlock()
	return c, nil
}

// nodeConn is one connection with the state Node's HTTP parser and socket
// keep for it.
type nodeConn struct {
	net.Conn
	s *Server

	mu   sync.Mutex
	cond *sync.Cond // signalled on idled, destroy and SetReadDeadline
	framer
	// inMessage is last_message_start_ != 0: true from the connection (the
	// parser is initialized with the time) and from the first byte of each
	// message until the message is complete (the framing sets it).
	inMessage       bool
	messageStart    time.Time
	headersComplete bool
	keepAlive       bool // the keep-alive socket timeout is armed
	lastActivity    time.Time
	timer           *time.Timer
	destroyed       bool
}

func (c *nodeConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.mu.Lock()
		c.lastActivity = clock.Monotonic()
		c.mu.Unlock()
	}
	return n, err
}

// idle is closeIdleConnections' test. Its other condition, a response
// that is not finished (_httpMessage && !finished), never holds when Node
// looks: the request listener ends its response synchronously, so by the
// time a signal or timer runs every response has been ended, sent or not.
// A connection whose slow reader holds queued responses is destroyed like
// any idle one, so a response still being written does not count here.
func (c *nodeConn) idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.inMessage
}

// expired is ConnectionsList::Expired's test for a connection.
func (c *nodeConn) expired(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.inMessage || c.destroyed {
		return false
	}
	age := now.Sub(c.messageStart)
	return (!c.headersComplete && age > headersTimeout) || age > requestTimeout
}

// requestTimedOut is onRequestTimeout: socketOnError writes the 408 when
// the socket has no response attached, which is always the case here (the
// listener ends each response at once and resOnFinish detaches it), then
// destroys the socket.
func (c *nodeConn) requestTimedOut() {
	c.Conn.SetWriteDeadline(clock.Monotonic().Add(time.Second))
	io.WriteString(c.Conn, requestTimeoutResponse)
	c.destroy()
}

// responseFinished is resOnFinish: a keep-alive response arms the socket
// timeout.
func (c *nodeConn) responseFinished(keepAlive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !keepAlive || c.destroyed {
		return
	}
	c.keepAlive = true
	c.lastActivity = clock.Monotonic()
	if c.timer == nil {
		c.timer = time.AfterFunc(keepAliveSocketTimeout, c.socketTimeout)
	} else {
		c.timer.Reset(keepAliveSocketTimeout)
	}
}

// socketTimeout is socketOnTimeout with no listeners: destroy, unless
// there was activity since (the timeout counts from the last read or
// write).
func (c *nodeConn) socketTimeout() {
	c.mu.Lock()
	if !c.keepAlive || c.destroyed {
		c.mu.Unlock()
		return
	}
	if rest := keepAliveSocketTimeout - clock.Monotonic().Sub(c.lastActivity); rest > 0 {
		c.timer.Reset(rest)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.destroy()
}

// destroy is socket.destroy(). The connection stops counting for Close at
// once, as Node's does: net/http may still linger on it (closeWriteAndWait
// sleeps 500 ms after an unread body) before reporting StateClosed.
func (c *nodeConn) destroy() {
	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		return
	}
	c.destroyed = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	c.Conn.Close()
	c.s.untrack(c)
}

type connKey struct{}

const contentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// requestPathname approximates `new URL(request.url, "http://127.0.0.1").pathname`
// for an origin-form request target: the query and fragment are dropped,
// "\" counts as "/" (a special scheme), and "." / ".." segments (also
// percent-encoded as %2e) are removed. It is only compared with "/".
func requestPathname(target string) string {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	target = strings.ReplaceAll(target, `\`, "/")
	// A scheme-relative target ("//host/path") names a host: the pathname
	// is what follows the authority.
	if rest, ok := strings.CutPrefix(target, "//"); ok {
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			return "/"
		}
		target = rest[i:]
	}
	segments := strings.Split(strings.TrimPrefix(target, "/"), "/")
	var out []string
	for i, segment := range segments {
		last := i == len(segments)-1
		switch strings.ToLower(segment) {
		case ".", "%2e":
			if last {
				out = append(out, "")
			}
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, segment)
		}
	}
	return "/" + strings.Join(out, "/")
}

// nodeHeaders sets the framing headers Node's http adds (Date comes from
// Go). An HTTP/1.1 request that does not ask to close gets
// `Connection: keep-alive` and `Keep-Alive: timeout=5`; otherwise
// `Connection: close`, including every HTTP/1.0 request, since Node cannot
// keep a 1.0 connection alive without a Content-Length. response.end(body)
// after writeHead sends an HTTP/1.1 body chunked; a 1.0 body is ended by
// closing, and a HEAD response has neither Transfer-Encoding nor
// Content-Length.
func nodeHeaders(h http.Header, r *http.Request) {
	http11 := r.ProtoAtLeast(1, 1)
	if http11 && !r.Close {
		h.Set("Connection", "keep-alive")
		h.Set("Keep-Alive", "timeout=5")
	} else {
		h.Set("Connection", "close")
	}
	if http11 && r.Method != "HEAD" {
		h.Set("Transfer-Encoding", "chunked")
	}
}

// writeNodeBody writes the status and body without a Content-Length: Go
// would compute one for a small HTTP/1.0 body (flushing the headers first
// prevents it) or a HEAD body (which is not written).
func writeNodeBody(w http.ResponseWriter, r *http.Request, status int, body string) {
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	if !r.ProtoAtLeast(1, 1) {
		w.(http.Flusher).Flush()
	}
	io.WriteString(w, body)
}

// Port of src/server.ts:65 startGitProofServer. The page is rendered once;
// GET / serves it and every other request is a 404.
func StartGitProofServer(v View, port float64) (*Server, error) {
	page := jsstr.ToUTF8(RenderGitProofIncidentPage(v))
	// net.Server.listen validates the port (validatePort, allowZero).
	if math.IsNaN(port) || port != math.Trunc(port) || port < 0 || port > 0xFFFF {
		return nil, errors.New("options.port should be >= 0 and < 65536. Received type number (" + jsjson.FormatNumber(port) + ").")
	}
	address := "127.0.0.1:" + strconv.Itoa(int(port))
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		code := nodefs.ErrnoCode(err)
		return nil, errors.New("listen " + code + ": " + nodefs.Describe(code) + " " + address)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := r.Context().Value(connKey{}).(*nodeConn)
		defer func() { c.responseFinished(w.Header().Get("Connection") == "keep-alive") }()
		h := w.Header()
		// parserOnIncoming's expectation check (HTTP/1.1 only): a value
		// matching continueExpression gets `100 Continue` before the
		// response, any other the empty 417 (no checkContinue or
		// checkExpectation listener).
		switch r.Header.Get(expectMarker) {
		case "fail":
			nodeHeaders(h, r)
			writeNodeBody(w, r, http.StatusExpectationFailed, "")
			return
		}
		if r.Method == "GET" && requestPathname(r.RequestURI) == "/" {
			h.Set("Content-Type", "text/html; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			nodeHeaders(h, r)
			writeNodeBody(w, r, 200, page)
			return
		}
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		nodeHeaders(h, r)
		writeNodeBody(w, r, 404, "Not found")
	})
	s := &Server{
		url:      "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
		listener: listener,
		stop:     make(chan struct{}),
		conns:    map[*nodeConn]struct{}{},
		drained:  make(chan struct{}),
	}
	// Node passes `OPTIONS *` to the handler like any other request (a 404
	// here); net/http would answer it with 200 itself. Timeouts are the
	// nodeConn's (no IdleTimeout: the keep-alive socket timeout closes an
	// idle connection after 6 s, as Node does). The head's size is limited
	// by the framing, Node's way, and Node 22.22.2 has no limit on the
	// number of header lines.
	s.srv = &http.Server{
		Handler:                      handler,
		DisableGeneralOptionsHandler: true,
		MaxHeaderBytes:               maxHeadBytes + 4096,
		MaxHeaderValueCount:          maxHeadBytes,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, conn)
		},
		ConnState: func(conn net.Conn, state http.ConnState) {
			c := conn.(*nodeConn)
			switch state {
			case http.StateIdle:
				c.idled()
			case http.StateClosed, http.StateHijacked:
				c.destroy()
			}
		},
	}
	go s.srv.Serve(nodeListener{listener, s})
	go s.checkConnections()
	// A TCP listener always reports a TCP address, so the TS "did not
	// report a TCP address" branch is unreachable and not ported.
	return s, nil
}
