package proofview

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

type incidentAsyncReply struct {
	raw string
	err error
}

func incidentAsyncExchange(url, request string) <-chan incidentAsyncReply {
	result := make(chan incidentAsyncReply, 1)
	go func() {
		raw, err := rawExchangeErr(url, request)
		result <- incidentAsyncReply{raw: raw, err: err}
	}()
	return result
}

func incidentRawReply(t *testing.T, result incidentAsyncReply) incidentReply {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	head, body, ok := strings.Cut(result.raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("malformed response %q", result.raw)
	}
	return incidentReply{head: head, body: body}
}

func incidentRaceRequest(s *IncidentServer, method, path string, authorized bool) string {
	request := method + " " + path + " HTTP/1.1\r\nHost: " + strings.TrimPrefix(s.URL(), "http://") + "\r\nConnection: close\r\n"
	if authorized {
		request += "X-FaultLine-Native-RERUN: " + testCredential + "\r\n"
	}
	return request + "\r\n"
}

func incidentRaceServer(t *testing.T, analysis jsjson.Value, rerun NativeRerunFunc) *IncidentServer {
	t.Helper()
	s, err := startIncidentServer(IncidentServerOptions{
		Analysis:     analysis,
		RerunEnabled: true,
		Rerun:        rerun,
	}, testCredential, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func incidentActiveDone(s *IncidentServer) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeDone
}

func TestIncidentServerSingleFlight(t *testing.T) {
	before := incidentServerAnalysis(t, "2026-01-01T00:00:00.000Z")
	after := incidentServerAnalysis(t, "2026-11-08T00:00:00.000Z")
	started := make(chan struct{})
	release := make(chan jsjson.Value, 1)
	returned := make(chan struct{})
	var calls atomic.Int32
	s := incidentRaceServer(t, before, func(ctx context.Context) (jsjson.Value, error) {
		if calls.Add(1) != 1 {
			return jsjson.Value{}, errors.New("concurrent callback")
		}
		close(started)
		defer close(returned)
		select {
		case value := <-release:
			return value, nil
		case <-ctx.Done():
			return jsjson.Value{}, ctx.Err()
		}
	})

	first := incidentAsyncExchange(s.URL(), incidentRaceRequest(s, "POST", "/api/rerun", true))
	<-started
	second := incidentAsyncExchange(s.URL(), incidentRaceRequest(s, "POST", "/api/rerun", true))
	reply := incidentRawReply(t, <-second)
	if reply.status() != "HTTP/1.1 409 Conflict" || reply.decoded() != incidentDiagnostic(t, "server-busy") {
		t.Fatalf("second POST:\n%s\n%s", reply.head, reply.body)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("callbacks while first is blocked: %d", got)
	}
	get := incidentExchange(t, s, incidentRaceRequest(s, "GET", "/api/analysis", false))
	if get.status() != "HTTP/1.1 200 OK" || get.decoded() != jsjson.Stringify(before) {
		t.Fatalf("snapshot while busy:\n%s\n%s", get.head, get.body)
	}

	release <- after
	reply = incidentRawReply(t, <-first)
	<-returned
	const success = `{"ok":true,"mode":"RERUN","generatedAt":"2026-11-08T00:00:00.000Z"}`
	if reply.status() != "HTTP/1.1 200 OK" || reply.decoded() != success {
		t.Fatalf("first POST:\n%s\n%s", reply.head, reply.body)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("callbacks after both POSTs: %d", got)
	}
	get = incidentExchange(t, s, incidentRaceRequest(s, "GET", "/api/analysis", false))
	if get.decoded() != jsjson.Stringify(after) {
		t.Fatalf("published snapshot:\n%s\n%s", get.head, get.body)
	}
}

func TestIncidentServerDisconnect(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	returned := make(chan struct{})
	releaseSecond := make(chan struct{})
	var calls atomic.Int32
	analysis := incidentServerAnalysis(t, "")
	s := incidentRaceServer(t, analysis, func(ctx context.Context) (jsjson.Value, error) {
		if calls.Add(1) == 1 {
			close(started)
			defer close(returned)
			<-ctx.Done()
			cancelled <- ctx.Err()
			return jsjson.Value{}, ctx.Err()
		}
		<-releaseSecond
		return analysis, nil
	})

	conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, incidentRaceRequest(s, "POST", "/api/rerun", true)); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	<-started
	done := incidentActiveDone(s)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("disconnect context error: %v", err)
	}
	<-returned
	<-done

	// A completed disconnected run is reaped and does not block a later run.
	second := incidentAsyncExchange(s.URL(), incidentRaceRequest(s, "POST", "/api/rerun", true))
	close(releaseSecond)
	reply := incidentRawReply(t, <-second)
	if reply.status() != "HTTP/1.1 200 OK" {
		t.Fatalf("POST after disconnect:\n%s\n%s", reply.head, reply.body)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("callbacks: %d", got)
	}
}

func TestIncidentServerClose(t *testing.T) {
	t.Run("active run", func(t *testing.T) {
		started := make(chan struct{})
		cancelled := make(chan error, 1)
		release := make(chan struct{})
		returned := make(chan struct{})
		var calls atomic.Int32
		s := incidentRaceServer(t, incidentServerAnalysis(t, ""), func(ctx context.Context) (jsjson.Value, error) {
			calls.Add(1)
			close(started)
			defer close(returned)
			<-ctx.Done()
			cancelled <- ctx.Err()
			<-release
			return jsjson.Value{}, ctx.Err()
		})
		post := incidentAsyncExchange(s.URL(), incidentRaceRequest(s, "POST", "/api/rerun", true))
		<-started
		done := incidentActiveDone(s)

		const closers = 8
		ready := make(chan struct{})
		results := make(chan error, closers)
		var wg sync.WaitGroup
		wg.Add(closers)
		for range closers {
			go func() {
				defer wg.Done()
				<-ready
				results <- s.Close()
			}()
		}
		close(ready)
		if err := <-cancelled; !errors.Is(err, context.Canceled) {
			t.Fatalf("Close context error: %v", err)
		}
		for range closers {
			select {
			case err := <-results:
				t.Fatalf("Close returned before callback release: %v", err)
			default:
			}
		}
		// The listener is still open and the run still active: a second
		// authorized POST reaches the closing 409.
		closing := incidentRawReply(t, <-incidentAsyncExchange(s.URL(), incidentRaceRequest(s, "POST", "/api/rerun", true)))
		if closing.status() != "HTTP/1.1 409 Conflict" || closing.decoded() != incidentDiagnostic(t, "server-busy") {
			t.Fatalf("closing-window POST:\n%s\n%s", closing.head, closing.body)
		}
		close(release)
		<-returned
		<-done
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		}
		conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL(), "http://"))
		if err == nil {
			conn.Close()
			t.Fatal("listener accepted a connection after Close")
		}
		if result := <-post; result.err != nil {
			t.Fatal(result.err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("callbacks: %d", got)
		}

		// The active request's socket and all test goroutines have terminated;
		// the transport's own connection map is drained as well.
		s.server.mu.Lock()
		remaining := len(s.server.conns)
		s.server.mu.Unlock()
		if remaining != 0 {
			t.Fatalf("transport connections after Close: %d", remaining)
		}
	})

	t.Run("idle rejects later run", func(t *testing.T) {
		var calls atomic.Int32
		s := incidentRaceServer(t, incidentServerAnalysis(t, ""), func(context.Context) (jsjson.Value, error) {
			calls.Add(1)
			return jsjson.Value{}, nil
		})
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}

		conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL(), "http://"))
		if err == nil {
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			request := incidentRaceRequest(s, "POST", "/api/rerun", true)
			if _, err := io.WriteString(conn, request); err != nil {
				conn.Close()
				t.Fatal(err)
			}
			raw, err := io.ReadAll(conn)
			conn.Close()
			if err != nil {
				t.Fatal(err)
			}
			reply := incidentRawReply(t, incidentAsyncReply{raw: string(raw)})
			if reply.status() != "HTTP/1.1 409 Conflict" || reply.decoded() != incidentDiagnostic(t, "server-busy") {
				t.Fatalf("POST after Close:\n%s\n%s", reply.head, reply.body)
			}
		}
		if got := calls.Load(); got != 0 {
			t.Fatalf("callbacks after Close: %d", got)
		}
	})
}
