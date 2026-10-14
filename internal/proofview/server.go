package proofview

import (
	"context"
	"errors"
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

// nodeHeaders sets the headers Node's http adds to a keep-alive response
// that Go's net/http does not (Date comes from Go). response.end(body)
// after writeHead sends the body chunked; a HEAD response has neither
// Transfer-Encoding nor Content-Length.
func nodeHeaders(h http.Header, method string) {
	h.Set("Connection", "keep-alive")
	h.Set("Keep-Alive", "timeout=5")
	if method != "HEAD" {
		h.Set("Transfer-Encoding", "chunked")
	}
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
			nodeHeaders(h, r.Method)
			w.WriteHeader(200)
			w.Write([]byte(page))
			return
		}
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		nodeHeaders(h, r.Method)
		w.WriteHeader(404)
		if r.Method != "HEAD" { // Go would add a Content-Length for a HEAD body
			w.Write([]byte("Not found"))
		}
	})
	// Node's keepAliveTimeout is 5 s.
	srv := &http.Server{Handler: handler, IdleTimeout: 5 * time.Second}
	go srv.Serve(listener)
	// A TCP listener always reports a TCP address, so the TS "did not
	// report a TCP address" branch is unreachable and not ported.
	return &Server{url: "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), srv: srv}, nil
}
