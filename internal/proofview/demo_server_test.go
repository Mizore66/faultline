package proofview

import (
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func plainDemoExchange(t *testing.T, s *PlainDemoServer, request string) incidentReply {
	t.Helper()
	raw := rawExchange(t, s.URL(), request)
	head, body, _ := strings.Cut(raw, "\r\n\r\n")
	return incidentReply{head: head, body: body}
}

func plainGet(path string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: example.invalid\r\nConnection: close\r\n\r\n"
}

const plainPost = "POST /api/rerun HTTP/1.1\r\nHost: example.invalid\r\nConnection: close\r\n\r\n"

func headerNames(r incidentReply) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(r.head, "\r\n")[1:] {
		name, _, _ := strings.Cut(line, ":")
		names[strings.ToLower(name)] = true
	}
	return names
}

func TestPlainDemoServerHTTP(t *testing.T) {
	first := incidentServerAnalysis(t, "2026-11-09T00:00:00.000Z")
	second := incidentServerAnalysis(t, "2026-11-10T00:00:00.000Z")
	fail := false
	s, err := StartPlainDemoServer(PlainDemoServerOptions{Analysis: first, Rerun: func() (jsjson.Value, error) {
		if fail {
			return jsjson.Value{}, errors.New(`boom "quoted"`)
		}
		return second, nil
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	page := plainDemoExchange(t, s, plainGet("/?x=1"))
	if page.status() != "HTTP/1.1 200 OK" || page.decoded() != RenderIncidentPage(first) ||
		!page.has("Content-Type: text/html; charset=utf-8") || !page.has("Cache-Control: no-store") || !page.has("X-Content-Type-Options: nosniff") {
		t.Fatalf("%s", page.head)
	}
	for _, n := range []string{"content-security-policy", "referrer-policy"} {
		if headerNames(page)[n] {
			t.Fatalf("unexpected %s", n)
		}
	}
	api := plainDemoExchange(t, s, plainGet("/api/analysis"))
	if api.status() != "HTTP/1.1 200 OK" || api.decoded() != jsjson.Stringify(first) ||
		!api.has("Content-Type: application/json; charset=utf-8") || !api.has("Cache-Control: no-store") {
		t.Fatalf("%s\n%s", api.head, api.body)
	}

	for _, req := range []string{plainGet("/nope"), "POST /other HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"} {
		nf := plainDemoExchange(t, s, req)
		if nf.status() != "HTTP/1.1 404 Not Found" || nf.decoded() != "Not found" || !nf.has("Content-Type: text/plain; charset=utf-8") ||
			headerNames(nf)["cache-control"] || headerNames(nf)["x-content-type-options"] {
			t.Fatalf("%s\n%s", nf.head, nf.body)
		}
	}
	head := plainDemoExchange(t, s, "HEAD /nope HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	if head.status() != "HTTP/1.1 404 Not Found" || head.body != "" {
		t.Fatalf("%s\n%s", head.head, head.body)
	}

	fail = true
	bad := plainDemoExchange(t, s, plainPost)
	if bad.status() != "HTTP/1.1 500 Internal Server Error" || bad.decoded() != `{"ok":false,"error":"boom \"quoted\""}` ||
		!bad.has("Content-Type: application/json; charset=utf-8") || headerNames(bad)["cache-control"] || headerNames(bad)["x-content-type-options"] {
		t.Fatalf("%s\n%s", bad.head, bad.body)
	}
	if got := plainDemoExchange(t, s, plainGet("/api/analysis")).decoded(); got != jsjson.Stringify(first) {
		t.Fatalf("analysis changed after failure: %s", got)
	}

	fail = false
	ok := plainDemoExchange(t, s, plainPost)
	if ok.status() != "HTTP/1.1 200 OK" || ok.decoded() != `{"ok":true,"mode":"RERUN","generatedAt":"2026-11-10T00:00:00.000Z"}` ||
		!ok.has("Content-Type: application/json; charset=utf-8") || !ok.has("Cache-Control: no-store") {
		t.Fatalf("%s\n%s", ok.head, ok.body)
	}
	if got := plainDemoExchange(t, s, plainGet("/api/analysis")).decoded(); got != jsjson.Stringify(second) {
		t.Fatalf("api not updated: %s", got)
	}
	if got := plainDemoExchange(t, s, plainGet("/")).decoded(); got != RenderIncidentPage(second) {
		t.Fatal("page not updated")
	}
}

// waitFor spins on cond (no sleeps), failing at the deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		runtime.Gosched()
	}
}

func TestPlainDemoServerConcurrency(t *testing.T) {
	first := incidentServerAnalysis(t, "2026-11-09T00:00:00.000Z")
	second := incidentServerAnalysis(t, "2026-11-10T00:00:00.000Z")
	var calls, running, overlap atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var firstReturned, startedAfterFirst atomic.Bool
	s, err := StartPlainDemoServer(PlainDemoServerOptions{Analysis: first, Rerun: func() (jsjson.Value, error) {
		n := calls.Add(1)
		if running.Add(1) > 1 {
			overlap.Add(1)
		}
		if n == 2 {
			startedAfterFirst.Store(firstReturned.Load())
		}
		entered <- struct{}{}
		if n == 1 {
			<-release
			firstReturned.Store(true)
		}
		running.Add(-1)
		return second, nil
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replies := make(chan string, 2)
	post := func() {
		r, _ := rawExchangeErr(s.URL(), plainPost)
		replies <- r
	}
	go post()
	<-entered
	go post()
	// The second POST is admitted and waiting on the first callback.
	waitFor(t, "second admitted", func() bool { return s.admittedCount() >= 2 })
	if calls.Load() != 1 {
		t.Fatalf("calls %d while first is blocked", calls.Load())
	}
	// A GET is served while the callback is blocked, with the prior analysis.
	if got := plainDemoExchange(t, s, plainGet("/api/analysis")).decoded(); got != jsjson.Stringify(first) {
		t.Fatalf("GET during callback: %s", got)
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case r := <-replies:
			if !strings.HasPrefix(r, "HTTP/1.1 200 OK") {
				t.Fatalf("reply: %s", r)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout")
		}
	}
	if calls.Load() != 2 || overlap.Load() != 0 || !startedAfterFirst.Load() {
		t.Fatalf("calls %d overlap %d startedAfterFirst %v", calls.Load(), overlap.Load(), startedAfterFirst.Load())
	}
}

func TestPlainDemoServerClose(t *testing.T) {
	first := incidentServerAnalysis(t, "2026-11-09T00:00:00.000Z")
	entered := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	var calls atomic.Int32
	s, err := StartPlainDemoServer(PlainDemoServerOptions{Analysis: first, Rerun: func() (jsjson.Value, error) {
		calls.Add(1)
		close(entered)
		<-release
		finished.Store(true)
		return first, nil
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	active := make(chan string, 1)
	go func() { r, _ := rawExchangeErr(s.URL(), plainPost); active <- r }()
	<-entered
	queued := make(chan string, 1)
	go func() { r, _ := rawExchangeErr(s.URL(), plainPost); queued <- r }()
	waitFor(t, "queued admitted", func() bool { return s.admittedCount() >= 2 })
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	waitFor(t, "closing state", s.isClosing)
	// The listener stays open until the active callback drains, so a late
	// POST reaches the handler and is refused without a callback.
	const closingBody = `{"ok":false,"error":"server is closing"}`
	late := plainDemoExchange(t, s, plainPost)
	if late.status() != "HTTP/1.1 500 Internal Server Error" || late.decoded() != closingBody ||
		!late.has("Content-Type: application/json; charset=utf-8") || headerNames(late)["cache-control"] || headerNames(late)["x-content-type-options"] {
		t.Fatalf("late POST: %s\n%s", late.head, late.body)
	}
	select {
	case <-closed:
		t.Fatal("Close returned during callback")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if !finished.Load() {
		t.Fatal("callback unfinished at Close return")
	}
	if r := <-active; !strings.HasPrefix(r, "HTTP/1.1 200 OK") {
		t.Fatalf("active reply: %s", r)
	}
	r := <-queued
	head, _, _ := strings.Cut(r, "\r\n\r\n")
	rep := incidentReply{head: head, body: strings.TrimPrefix(r, head+"\r\n\r\n")}
	if rep.status() != "HTTP/1.1 500 Internal Server Error" || rep.decoded() != closingBody ||
		!rep.has("Content-Type: application/json; charset=utf-8") || headerNames(rep)["cache-control"] || headerNames(rep)["x-content-type-options"] {
		t.Fatalf("queued reply: %s", r)
	}
	if calls.Load() != 1 {
		t.Fatalf("callbacks %d, want 1", calls.Load())
	}
}
