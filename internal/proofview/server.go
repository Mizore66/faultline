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
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Server is the FaultLineServer startGitProofServer returns.
type Server struct {
	url string
	srv *http.Server
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *Server) URL() string { return s.url }

// Close is server.close(): stop accepting, close idle connections (Node 19+
// closes them on close) and wait for in-flight requests.
func (s *Server) Close() error { return s.srv.Shutdown(context.Background()) }

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
		h := w.Header()
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
	// Node's keepAliveTimeout is 5 s. Node passes `OPTIONS *` to the
	// handler like any other request (a 404 here); net/http would answer it
	// with 200 itself.
	srv := &http.Server{Handler: handler, IdleTimeout: 5 * time.Second, DisableGeneralOptionsHandler: true}
	go srv.Serve(listener)
	// A TCP listener always reports a TCP address, so the TS "did not
	// report a TCP address" branch is unreachable and not ported.
	return &Server{url: "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), srv: srv}, nil
}
