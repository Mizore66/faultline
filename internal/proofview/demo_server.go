package proofview

import (
	"net/http"
	"sync"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// PlainDemoRerunFunc creates and publishes one demo RERUN analysis. The
// analysis replaces the served one only when it returns without error.
type PlainDemoRerunFunc func() (jsjson.Value, error)

// PlainDemoServerOptions configures StartPlainDemoServer.
type PlainDemoServerOptions struct {
	Analysis jsjson.Value
	Rerun    PlainDemoRerunFunc
}

// PlainDemoServer is the legacy plain `serve` server: frozen-TypeScript
// routes and headers, with no Host, Origin, token, or approval checks.
type PlainDemoServer struct {
	server *Server
	rerun  PlainDemoRerunFunc

	// runMu serializes RERUN callbacks; readers never take it.
	runMu sync.Mutex
	// mu guards analysis and activeDone and is not held during a callback.
	mu         sync.Mutex
	analysis   jsjson.Value
	activeDone chan struct{}
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *PlainDemoServer) URL() string { return s.server.URL() }

// Close waits for the active RERUN callback (a zero-body request counts as
// idle to the transport, so its drain does not cover it), then closes the
// transport.
func (s *PlainDemoServer) Close() error {
	s.mu.Lock()
	done := s.activeDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return s.server.Close()
}

// StartPlainDemoServer serves the plain demo analysis on 127.0.0.1:port.
func StartPlainDemoServer(options PlainDemoServerOptions, port float64) (*PlainDemoServer, error) {
	s := &PlainDemoServer{analysis: options.Analysis, rerun: options.Rerun}
	snapshot := func() jsjson.Value {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.analysis
	}
	server, err := startNodeServer(port, func(string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			path := requestPathname(r.RequestURI)
			switch {
			case r.Method == "GET" && path == "/":
				h.Set("Content-Type", "text/html; charset=utf-8")
				h.Set("Cache-Control", "no-store")
				h.Set("X-Content-Type-Options", "nosniff")
				nodeHeaders(h, r)
				writeNodeBody(w, r, http.StatusOK, jsstr.ToUTF8(RenderIncidentPage(snapshot())))
			case r.Method == "GET" && path == "/api/analysis":
				h.Set("Content-Type", "application/json; charset=utf-8")
				h.Set("Cache-Control", "no-store")
				nodeHeaders(h, r)
				writeNodeBody(w, r, http.StatusOK, jsstr.ToUTF8(jsjson.Stringify(snapshot())))
			case r.Method == "POST" && path == "/api/rerun":
				s.handleRerun(w, r)
			default:
				h.Set("Content-Type", "text/plain; charset=utf-8")
				nodeHeaders(h, r)
				writeNodeBody(w, r, http.StatusNotFound, "Not found")
			}
		}
	})
	if err != nil {
		return nil, err
	}
	s.server = server
	return s, nil
}

func (s *PlainDemoServer) handleRerun(w http.ResponseWriter, r *http.Request) {
	s.runMu.Lock()
	done := make(chan struct{})
	s.mu.Lock()
	s.activeDone = done
	s.mu.Unlock()
	var analysis jsjson.Value
	var err error
	func() {
		defer func() {
			s.mu.Lock()
			s.activeDone = nil
			s.mu.Unlock()
			close(done)
			s.runMu.Unlock()
		}()
		analysis, err = s.rerun()
		if err == nil {
			s.mu.Lock()
			s.analysis = analysis
			s.mu.Unlock()
		}
	}()
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		nodeHeaders(h, r)
		writeNodeBody(w, r, http.StatusInternalServerError,
			jsstr.ToUTF8(`{"ok":false,"error":`+jsjson.Stringify(jsjson.MakeString(err.Error()))+`}`))
		return
	}
	h.Set("Cache-Control", "no-store")
	nodeHeaders(h, r)
	writeNodeBody(w, r, http.StatusOK,
		jsstr.ToUTF8(`{"ok":true,"mode":"RERUN","generatedAt":`+jsjson.Stringify(analysis.Get("generatedAt"))+`}`))
}
