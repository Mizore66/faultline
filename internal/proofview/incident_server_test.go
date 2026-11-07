package proofview

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

const testCredential = "test_nonce-ABC123"

func incidentServerAnalysis(t *testing.T, generatedAt string) jsjson.Value {
	t.Helper()
	corpus := incidentTestLoad(t)
	analysis, err := jsjson.Parse(corpus.rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	if generatedAt != "" {
		analysis.Obj().Set("generatedAt", jsjson.MakeString(generatedAt))
	}
	return analysis
}

type incidentReply struct{ head, body string }

func (r incidentReply) status() string {
	line, _, _ := strings.Cut(r.head, "\r\n")
	return line
}

func (r incidentReply) has(h string) bool { return strings.Contains(r.head+"\r\n", "\r\n"+h+"\r\n") }

// decoded returns the body with chunked framing removed.
func (r incidentReply) decoded() string {
	if !r.has("Transfer-Encoding: chunked") {
		return r.body
	}
	var out strings.Builder
	rest := r.body
	for {
		size, after, ok := strings.Cut(rest, "\r\n")
		if !ok || size == "0" {
			return out.String()
		}
		n := 0
		for _, c := range size {
			n = n*16 + strings.IndexRune("0123456789abcdef", c)
		}
		out.WriteString(after[:n])
		rest = after[n+2:]
	}
}

func incidentExchange(t *testing.T, s *IncidentServer, request string) incidentReply {
	t.Helper()
	raw := rawExchange(t, s.URL(), request)
	head, body, _ := strings.Cut(raw, "\r\n\r\n")
	return incidentReply{head: head, body: body}
}

func TestIncidentServerHTTP(t *testing.T) {
	var calls atomic.Int32
	rerunAnalysis := incidentServerAnalysis(t, "2026-11-08T00:00:00.000Z")
	start := func(t *testing.T, enabled bool) *IncidentServer {
		t.Helper()
		s, err := startIncidentServer(IncidentServerOptions{
			Analysis:     incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z"),
			RerunEnabled: enabled,
			Rerun: func(context.Context) (jsjson.Value, error) {
				calls.Add(1)
				return rerunAnalysis, nil
			},
		}, testCredential, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	hostOf := func(s *IncidentServer) string { return strings.TrimPrefix(s.URL(), "http://") }
	post := func(s *IncidentServer, extra string) string {
		return "POST /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + extra + "\r\n"
	}
	const forbidden = `{"ok":false,"error":"Forbidden"}`
	token := "X-FaultLine-Native-RERUN: " + testCredential + "\r\n"

	t.Run("GET / page", func(t *testing.T) {
		s := start(t, true)
		reply := incidentExchange(t, s, "GET /?x=1 HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		if reply.status() != "HTTP/1.1 200 OK" {
			t.Fatal(reply.head)
		}
		for _, h := range []string{
			"Content-Type: text/html; charset=utf-8", "Cache-Control: no-store", "X-Content-Type-Options: nosniff",
			"Referrer-Policy: no-referrer",
			"Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-" + testCredential + "'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		} {
			if !reply.has(h) {
				t.Errorf("missing %q in\n%s", h, reply.head)
			}
		}
		want := renderInteractiveIncidentPage(incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z"), incidentInteraction{credential: testCredential, enabled: true})
		if reply.decoded() != want {
			t.Fatal("page differs from renderer")
		}
		if reply.has("Access-Control-Allow-Origin: *") || strings.Contains(reply.head, "Access-Control") {
			t.Fatal("CORS header")
		}
	})
	t.Run("GET / disabled page", func(t *testing.T) {
		s := start(t, false)
		reply := incidentExchange(t, s, "GET / HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		want := renderInteractiveIncidentPage(incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z"), incidentInteraction{credential: testCredential})
		if reply.decoded() != want || strings.Contains(reply.body, "/api/rerun") {
			t.Fatal("disabled page differs")
		}
	})
	t.Run("GET /api/analysis", func(t *testing.T) {
		s := start(t, true)
		reply := incidentExchange(t, s, "GET /api/analysis?q HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		if reply.status() != "HTTP/1.1 200 OK" || !reply.has("Content-Type: application/json; charset=utf-8") ||
			!reply.has("Cache-Control: no-store") || !reply.has("X-Content-Type-Options: nosniff") || !reply.has("Referrer-Policy: no-referrer") {
			t.Fatal(reply.head)
		}
		if reply.decoded() != jsjson.Stringify(incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z")) {
			t.Fatal("analysis bytes differ")
		}
	})
	t.Run("host policy", func(t *testing.T) {
		s := start(t, true)
		for name, request := range map[string]string{
			"dns":       "GET / HTTP/1.1\r\nHost: localhost:" + strings.TrimPrefix(hostOf(s), "127.0.0.1:") + "\r\nConnection: close\r\n\r\n",
			"wrong":     "GET / HTTP/1.1\r\nHost: 127.0.0.1:1\r\nConnection: close\r\n\r\n",
			"no port":   "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n",
			"http1.0":   "GET / HTTP/1.0\r\n\r\n",
			"post dns":  "POST /api/rerun HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n" + token + "\r\n",
			"unknown":   "GET /nope HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n\r\n",
			"http1.0 p": "POST /api/rerun HTTP/1.0\r\n" + token + "\r\n",
		} {
			reply := incidentExchange(t, s, request)
			if !strings.HasSuffix(reply.status(), " 403 Forbidden") || !reply.has("Content-Type: text/plain; charset=utf-8") ||
				!reply.has("Cache-Control: no-store") || !reply.has("X-Content-Type-Options: nosniff") || reply.decoded() != "Forbidden" {
				t.Errorf("%s:\n%s\n\n%s", name, reply.head, reply.body)
			}
		}
		reply := incidentExchange(t, s, "GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
		if !strings.HasPrefix(reply.status(), "HTTP/1.1 400 Bad Request") {
			t.Errorf("malformed missing Host: %s", reply.head)
		}
		if calls.Load() != 0 {
			t.Fatal("callback ran")
		}
	})
	t.Run("unknown routes and methods", func(t *testing.T) {
		s := start(t, true)
		for name, request := range map[string]string{
			"route":       "GET /missing HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n\r\n",
			"post root":   "POST / HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + token + "\r\n",
			"get rerun":   "GET /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n\r\n",
			"put rerun":   "PUT /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + token + "\r\n",
			"options":     "OPTIONS /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\nOrigin: http://evil.example\r\n\r\n",
			"artifact":    "GET /artifacts/x HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n\r\n",
			"diagnostics": "GET /api/diagnostics HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n\r\n",
			"post analys": "POST /api/analysis HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + token + "\r\n",
		} {
			reply := incidentExchange(t, s, request)
			if reply.status() != "HTTP/1.1 404 Not Found" || !reply.has("Content-Type: text/plain; charset=utf-8") ||
				!reply.has("Cache-Control: no-store") || !reply.has("X-Content-Type-Options: nosniff") || reply.decoded() != "Not found" ||
				strings.Contains(reply.head, "Access-Control") {
				t.Errorf("%s:\n%s\n\n%s", name, reply.head, reply.body)
			}
		}
		head := incidentExchange(t, s, "HEAD /missing HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		if head.status() != "HTTP/1.1 404 Not Found" || head.body != "" {
			t.Errorf("HEAD:\n%s\n%q", head.head, head.body)
		}
		if incidentExchange(t, s, "HEAD / HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n").status() != "HTTP/1.1 404 Not Found" {
			t.Error("HEAD / is not 404")
		}
	})
	t.Run("rerun security", func(t *testing.T) {
		s := start(t, true)
		origin := "Origin: " + s.URL() + "\r\n"
		for name, request := range map[string]string{
			"no token":        post(s, ""),
			"bad token":       post(s, "X-FaultLine-Native-RERUN: nope\r\n"),
			"prefix token":    post(s, "X-FaultLine-Native-RERUN: "+testCredential+"x\r\n"),
			"empty token":     post(s, "X-FaultLine-Native-RERUN:\r\n"),
			"duplicate token": post(s, token+token),
			"comma token":     post(s, "X-FaultLine-Native-RERUN: "+testCredential+", "+testCredential+"\r\n"),
			"cross origin":    post(s, token+"Origin: http://evil.example\r\n"),
			"origin slash":    post(s, token+"Origin: "+s.URL()+"/\r\n"),
			"null origin":     post(s, token+"Origin: null\r\n"),
			"duplicate orig":  post(s, token+origin+origin),
			"cross site":      post(s, token+"Sec-Fetch-Site: cross-site\r\n"),
			"same site":       post(s, token+"Sec-Fetch-Site: same-site\r\n"),
			"none fetch":      post(s, token+"Sec-Fetch-Site: none\r\n"),
			"positive length": post(s, token+"Content-Length: 2\r\n\r\n{}")[:0] + "POST /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + token + "Content-Length: 2\r\n\r\n{}",
			"chunked":         post(s, token+"Transfer-Encoding: chunked\r\n\r\n0\r\n\r\n"),
			"forged marker":   "POST /api/rerun HTTP/1.1\r\nHost: " + hostOf(s) + "\r\nConnection: close\r\n" + token + "Faultline-Node-Body: zero\r\nContent-Length: 2\r\n\r\n{}",
		} {
			reply := incidentExchange(t, s, request)
			if reply.status() != "HTTP/1.1 403 Forbidden" || !reply.has("Content-Type: application/json; charset=utf-8") ||
				!reply.has("Cache-Control: no-store") || !reply.has("X-Content-Type-Options: nosniff") || !reply.has("Referrer-Policy: no-referrer") ||
				reply.decoded() != forbidden {
				t.Errorf("%s:\n%s\n\n%s", name, reply.head, reply.body)
			}
		}
		if calls.Load() != 0 {
			t.Fatal("callback ran for a refused request")
		}
	})
	t.Run("no approval", func(t *testing.T) {
		s := start(t, false)
		reply := incidentExchange(t, s, post(s, token))
		const body = `{"ok":false,"error":"Native RERUN requires --approve-native <proposal-digest> at server startup."}`
		if reply.status() != "HTTP/1.1 403 Forbidden" || !reply.has("Content-Type: application/json; charset=utf-8") || reply.decoded() != body {
			t.Fatalf("%s\n%s", reply.head, reply.body)
		}
		// An invalid caller learns nothing about approval.
		if got := incidentExchange(t, s, post(s, "")).decoded(); got != forbidden {
			t.Fatalf("unauthenticated: %s", got)
		}
		if calls.Load() != 0 {
			t.Fatal("callback ran")
		}
	})
	t.Run("successful rerun", func(t *testing.T) {
		s := start(t, true)
		before := calls.Load()
		for _, extra := range []string{token, token + "Origin: " + s.URL() + "\r\nSec-Fetch-Site: same-origin\r\n", token + "Content-Length: 0\r\n"} {
			reply := incidentExchange(t, s, post(s, extra))
			const body = `{"ok":true,"mode":"RERUN","generatedAt":"2026-11-08T00:00:00.000Z"}`
			if reply.status() != "HTTP/1.1 200 OK" || !reply.has("Content-Type: application/json; charset=utf-8") ||
				!reply.has("Cache-Control: no-store") || !reply.has("X-Content-Type-Options: nosniff") || !reply.has("Referrer-Policy: no-referrer") ||
				reply.decoded() != body {
				t.Fatalf("%s\n%s", reply.head, reply.body)
			}
		}
		if calls.Load()-before != 3 {
			t.Fatalf("callbacks %d", calls.Load()-before)
		}
		// The published analysis replaces the snapshot.
		get := incidentExchange(t, s, "GET /api/analysis HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		if get.decoded() != jsjson.Stringify(rerunAnalysis) {
			t.Fatal("snapshot not replaced")
		}
	})
	t.Run("failed rerun keeps snapshot", func(t *testing.T) {
		s, err := startIncidentServer(IncidentServerOptions{
			Analysis:     incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z"),
			RerunEnabled: true,
			Rerun:        func(context.Context) (jsjson.Value, error) { return jsjson.Value{}, errors.New("boom \"quoted\"") },
		}, testCredential, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		reply := incidentExchange(t, s, post(s, token))
		if reply.status() != "HTTP/1.1 500 Internal Server Error" || reply.decoded() != `{"ok":false,"error":"boom \"quoted\""}` {
			t.Fatalf("%s\n%s", reply.head, reply.body)
		}
		get := incidentExchange(t, s, "GET /api/analysis HTTP/1.1\r\nHost: "+hostOf(s)+"\r\nConnection: close\r\n\r\n")
		if get.decoded() != jsjson.Stringify(incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z")) {
			t.Fatal("snapshot changed")
		}
	})
	t.Run("single flight and close", func(t *testing.T) {
		started, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		s, err := startIncidentServer(IncidentServerOptions{
			Analysis:     incidentServerAnalysis(t, ""),
			RerunEnabled: true,
			Rerun: func(ctx context.Context) (jsjson.Value, error) {
				close(started)
				select {
				case <-release:
					return rerunAnalysis, nil
				case <-ctx.Done():
					cancelled <- ctx.Err()
					return jsjson.Value{}, ctx.Err()
				}
			},
		}, testCredential, 0)
		if err != nil {
			t.Fatal(err)
		}
		first := make(chan incidentReply)
		go func() { first <- incidentExchange(t, s, post(s, token)) }()
		<-started
		reply := incidentExchange(t, s, post(s, token))
		if reply.status() != "HTTP/1.1 409 Conflict" || reply.decoded() != `{"ok":false,"error":"native RERUN already running"}` {
			t.Fatalf("%s\n%s", reply.head, reply.body)
		}
		s.Close() // cancels the active run
		// Node's close() destroys the idle connection of a request whose
		// handler is still running, so the reply may be lost; the run must
		// still have been cancelled and finished by the time Close returns.
		if err := <-cancelled; !errors.Is(err, context.Canceled) {
			t.Fatalf("run context: %v", err)
		}
		<-first
	})
}

func TestStartIncidentServerCredential(t *testing.T) {
	s, err := StartIncidentServer(IncidentServerOptions{Analysis: incidentServerAnalysis(t, "")}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page := incidentExchange(t, s, "GET / HTTP/1.1\r\nHost: "+strings.TrimPrefix(s.URL(), "http://")+"\r\nConnection: close\r\n\r\n")
	_, rest, ok := strings.Cut(page.head, "script-src 'nonce-")
	nonce, _, _ := strings.Cut(rest, "'")
	if !ok || len(nonce) != 43 || strings.ContainsAny(nonce, "+/=") {
		t.Fatalf("nonce %q", nonce)
	}
	if _, err := StartIncidentServer(IncidentServerOptions{}, -1); err == nil {
		t.Fatal("bad port accepted")
	}
}
