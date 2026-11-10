package proofview

import (
	"errors"
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

func TestPlainDemoServerConcurrency(t *testing.T) {
	first := incidentServerAnalysis(t, "2026-11-09T00:00:00.000Z")
	second := incidentServerAnalysis(t, "2026-11-10T00:00:00.000Z")
	var calls atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	s, err := StartPlainDemoServer(PlainDemoServerOptions{Analysis: first, Rerun: func() (jsjson.Value, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
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
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("calls %d before release", calls.Load())
	}
	select {
	case r := <-replies:
		t.Fatalf("early reply (409?): %s", r)
	default:
	}
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
	if n := calls.Load(); n != 2 {
		t.Fatalf("serialized calls %d, want 2 (one per POST)", n)
	}
}

func TestPlainDemoServerClose(t *testing.T) {
	first := incidentServerAnalysis(t, "2026-11-09T00:00:00.000Z")
	entered := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	s, err := StartPlainDemoServer(PlainDemoServerOptions{Analysis: first, Rerun: func() (jsjson.Value, error) {
		close(entered)
		<-release
		finished.Store(true)
		return first, nil
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	go rawExchangeErr(s.URL(), plainPost)
	<-entered
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned during callback")
	case <-time.After(150 * time.Millisecond):
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
}
