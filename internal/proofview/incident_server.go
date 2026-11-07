package proofview

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// NativeRerunFunc runs one native RERUN and returns the newly published
// analysis. Its context is cancelled when the client disconnects or the
// server closes.
type NativeRerunFunc func(context.Context) (jsjson.Value, error)

// IncidentServerOptions configures StartIncidentServer. RERUN is available
// only when RerunEnabled is set and Rerun is non-nil.
type IncidentServerOptions struct {
	Analysis     jsjson.Value
	RerunEnabled bool
	Rerun        NativeRerunFunc
}

// IncidentServer is the loopback interactive incident server.
type IncidentServer struct {
	server *Server

	mu       sync.Mutex
	analysis jsjson.Value
	running  bool
	closing  bool
	cancel   context.CancelFunc
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *IncidentServer) URL() string { return s.server.URL() }

// Close rejects new runs, cancels the active one and closes the transport,
// which completes once every connection (and so every handler) is done.
func (s *IncidentServer) Close() error {
	s.mu.Lock()
	s.closing = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	return s.server.Close()
}

const rerunTokenHeader = "X-FaultLine-Native-RERUN"

// StartIncidentServer serves the incident page with a fresh random
// credential used as both the script nonce and the RERUN header value.
func StartIncidentServer(options IncidentServerOptions, port float64) (*IncidentServer, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return startIncidentServer(options, base64.RawURLEncoding.EncodeToString(raw), port)
}

func startIncidentServer(options IncidentServerOptions, credential string, port float64) (*IncidentServer, error) {
	is := &IncidentServer{analysis: options.Analysis}
	enabled := options.RerunEnabled && options.Rerun != nil
	csp := "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-" + credential +
		"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	server, err := startNodeServer(port, func(url string) http.HandlerFunc {
		authority := url[len("http://"):]
		origin := url
		plain := func(w http.ResponseWriter, r *http.Request, status int, body string) {
			h := w.Header()
			h.Set("Content-Type", "text/plain; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			h.Set("X-Content-Type-Options", "nosniff")
			nodeHeaders(h, r)
			writeNodeBody(w, r, status, body)
		}
		api := func(w http.ResponseWriter, r *http.Request, status int, contentType, body string) {
			h := w.Header()
			h.Set("Content-Type", contentType)
			h.Set("Cache-Control", "no-store")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			nodeHeaders(h, r)
			writeNodeBody(w, r, status, jsstr.ToUTF8(body))
		}
		const jsonType = "application/json; charset=utf-8"
		failure := func(w http.ResponseWriter, r *http.Request, status int, message string) {
			api(w, r, status, jsonType, `{"ok":false,"error":`+jsjson.Stringify(jsjson.MakeString(message))+`}`)
		}
		single := func(r *http.Request, name string) (string, bool) {
			v := r.Header.Values(name)
			if len(v) != 1 {
				return "", false
			}
			return v[0], true
		}
		authorized := func(r *http.Request) bool {
			token, ok := single(r, rerunTokenHeader)
			if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(credential)) != 1 {
				return false
			}
			if body, ok := single(r, bodyMarker); !ok || body != "zero" {
				return false
			}
			if len(r.Header.Values("Origin")) > 0 {
				if o, ok := single(r, "Origin"); !ok || o != origin {
					return false
				}
			}
			if len(r.Header.Values("Sec-Fetch-Site")) > 0 {
				if f, ok := single(r, "Sec-Fetch-Site"); !ok || f != "same-origin" {
					return false
				}
			}
			return true
		}
		snapshot := func() jsjson.Value {
			is.mu.Lock()
			defer is.mu.Unlock()
			return is.analysis
		}
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Host != authority {
				plain(w, r, http.StatusForbidden, "Forbidden")
				return
			}
			path := requestPathname(r.RequestURI)
			switch {
			case r.Method == "GET" && path == "/":
				h := w.Header()
				h.Set("Content-Type", "text/html; charset=utf-8")
				h.Set("Cache-Control", "no-store")
				h.Set("Content-Security-Policy", csp)
				h.Set("Referrer-Policy", "no-referrer")
				h.Set("X-Content-Type-Options", "nosniff")
				nodeHeaders(h, r)
				page := renderInteractiveIncidentPage(snapshot(), incidentInteraction{credential: credential, enabled: enabled})
				writeNodeBody(w, r, http.StatusOK, jsstr.ToUTF8(page))
			case r.Method == "GET" && path == "/api/analysis":
				api(w, r, http.StatusOK, jsonType, jsjson.Stringify(snapshot()))
			case r.Method == "POST" && path == "/api/rerun":
				is.rerun(w, r, enabled, options.Rerun, authorized, failure, api)
			default:
				plain(w, r, http.StatusNotFound, "Not found")
			}
		}
	})
	if err != nil {
		return nil, err
	}
	is.server = server
	return is, nil
}

// rerun evaluates security, then approval, then single-flight, then runs.
func (s *IncidentServer) rerun(w http.ResponseWriter, r *http.Request, enabled bool, run NativeRerunFunc,
	authorized func(*http.Request) bool,
	failure func(http.ResponseWriter, *http.Request, int, string),
	api func(http.ResponseWriter, *http.Request, int, string, string)) {
	if !authorized(r) {
		failure(w, r, http.StatusForbidden, "Forbidden")
		return
	}
	if !enabled {
		failure(w, r, http.StatusForbidden, "Native RERUN requires --approve-native <proposal-digest> at server startup.")
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		failure(w, r, http.StatusConflict, "native RERUN server is closing")
		return
	}
	if s.running {
		s.mu.Unlock()
		failure(w, r, http.StatusConflict, "native RERUN already running")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	s.running, s.cancel = true, cancel
	s.mu.Unlock()
	analysis, err := run(ctx)
	cancel()
	s.mu.Lock()
	s.running, s.cancel = false, nil
	if err == nil {
		s.analysis = analysis
	}
	s.mu.Unlock()
	if err != nil {
		failure(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	api(w, r, http.StatusOK, "application/json; charset=utf-8",
		`{"ok":true,"mode":"RERUN","generatedAt":`+jsjson.Stringify(analysis.Get("generatedAt"))+`}`)
}
