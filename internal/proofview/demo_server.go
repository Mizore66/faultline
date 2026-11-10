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
	closing    bool
	admitted   int            // RERUN handlers admitted before closing and not yet finished
	inflight   sync.WaitGroup // same set; Close waits so queued replies are written

	closeOnce sync.Once
	closeErr  error
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *PlainDemoServer) URL() string { return s.server.URL() }

// Close rejects new RERUNs (queued and later POSTs get a 500 without running
// the callback), waits for the active callback (a zero-body request counts
// as idle to the transport, so its drain does not cover it), then closes the
// transport.
func (s *PlainDemoServer) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		done := s.activeDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		s.inflight.Wait()
		s.closeErr = s.server.Close()
	})
	return s.closeErr
}

func (s *PlainDemoServer) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *PlainDemoServer) admittedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admitted
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

func (s *PlainDemoServer) rerunFailure(w http.ResponseWriter, r *http.Request, message string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	nodeHeaders(h, r)
	writeNodeBody(w, r, http.StatusInternalServerError,
		jsstr.ToUTF8(`{"ok":false,"error":`+jsjson.Stringify(jsjson.MakeString(message))+`}`))
}

func (s *PlainDemoServer) handleRerun(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		s.rerunFailure(w, r, "server is closing")
		return
	}
	s.admitted++
	s.inflight.Add(1)
	s.mu.Unlock()
	defer func() {
		// Flush before Close can proceed: the transport treats a finished
		// zero-body request as idle and would drop an unflushed reply.
		w.(http.Flusher).Flush()
		s.mu.Lock()
		s.admitted--
		s.mu.Unlock()
		s.inflight.Done()
	}()
	s.runMu.Lock()
	done := make(chan struct{})
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		s.runMu.Unlock()
		s.rerunFailure(w, r, "server is closing")
		return
	}
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
	if err != nil {
		s.rerunFailure(w, r, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	nodeHeaders(h, r)
	writeNodeBody(w, r, http.StatusOK,
		jsstr.ToUTF8(`{"ok":true,"mode":"RERUN","generatedAt":`+jsjson.Stringify(analysis.Get("generatedAt"))+`}`))
}
