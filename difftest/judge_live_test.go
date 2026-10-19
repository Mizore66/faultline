//go:build faultline_test && unix

package difftest

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// judgeWorkspace is a temporary workspace holding the committed COMMIT_PROOF
// sample where judge-proof looks for it.
func judgeWorkspace(t *testing.T, repo string) string {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(repo, "docs", "samples", "self-incident-commit-proof"), filepath.Join(ws, "docs", "samples", "self-incident-commit-proof"))
	return ws
}

// judgeSide is one side's judge-proof command line.
type judgeSide struct {
	name string
	argv []string
}

func judgeSides(repo string) []judgeSide {
	return []judgeSide{
		{"TS", []string{"node", filepath.Join(repo, "dist", "cli.js")}},
		{"Go", []string{flBinary}},
	}
}

// judgeEnv is the test's environment without the variables that skip the
// browser, plus extra.
func judgeEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != "CI" && name != "GITHUB_ACTIONS" && name != "FAULTLINE_NO_BROWSER" {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

type httpReply struct {
	status  int
	headers map[string]string
	body    string
}

// tsSetHeaders are the headers src/server.ts startGitProofServer sets
// itself; Node's http adds Date, Connection, Keep-Alive and
// Transfer-Encoding/Content-Length, which are compared too (all but Date's
// value; their order on the wire differs, KNOWN_DIFFERENCES.md).
var tsSetHeaders = []string{"Content-Type", "Cache-Control", "Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options",
	"Connection", "Keep-Alive", "Transfer-Encoding", "Content-Length"}

// judgeRequest is one raw request: method, request target, protocol
// version and an optional extra header line.
type judgeRequest struct{ method, target, proto, header string }

// fetch sends r on a fresh connection to address (host:port) and reads the
// reply with its raw framing (Transfer-Encoding and Content-Length as sent).
func fetch(t *testing.T, address string, r judgeRequest) httpReply {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw := r.method + " " + r.target + " " + r.proto + "\r\nHost: " + address + "\r\n"
	if r.header != "" {
		raw += r.header + "\r\n"
	}
	if _, err := io.WriteString(conn, raw+"\r\n"); err != nil {
		t.Fatal(err)
	}
	// The raw head too: ReadResponse drops "Connection: close" from an
	// HTTP/1.1 response's headers (it becomes resp.Close).
	var head bytes.Buffer
	resp, err := http.ReadResponse(bufio.NewReader(io.TeeReader(conn, &head)), &http.Request{Method: r.method})
	if err != nil {
		t.Fatalf("%v: %v", r, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	headers := map[string]string{}
	for _, name := range tsSetHeaders {
		headers[name] = resp.Header.Get(name)
	}
	headers["Connection"] = ""
	if end := bytes.Index(head.Bytes(), []byte("\r\n\r\n")); end >= 0 {
		for _, line := range strings.Split(string(head.Bytes()[:end]), "\r\n")[1:] {
			if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(name, "Connection") {
				headers["Connection"] = strings.TrimSpace(value)
			}
		}
	}
	if len(resp.TransferEncoding) > 0 {
		headers["Transfer-Encoding"] = strings.Join(resp.TransferEncoding, ",")
	}
	if resp.ContentLength >= 0 && r.method != "HEAD" {
		headers["Content-Length"] = itoa(resp.ContentLength)
	}
	if _, err := http.ParseTime(resp.Header.Get("Date")); err != nil {
		t.Errorf("%v: Date %q is not an HTTP date", r, resp.Header.Get("Date"))
	}
	return httpReply{resp.StatusCode, headers, string(body)}
}

// startJudge starts one side's `judge-proof` in a fresh workspace and reads
// its stdout up to the page URL.
func startJudge(t *testing.T, repo string, side judgeSide, env []string, args ...string) (cmd *exec.Cmd, url string, out *strings.Builder, rest *bufio.Reader, stderr *lockedBuffer) {
	cmd = exec.Command(side.argv[0], append(append(side.argv[1:], "judge-proof"), args...)...)
	cmd.Dir = judgeWorkspace(t, repo)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr = &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out = &strings.Builder{}
	rest = bufio.NewReader(stdout)
	for url == "" {
		line, err := rest.ReadString('\n')
		out.WriteString(line)
		if err != nil {
			t.Fatalf("%s: no page URL\n%s%s", side.name, out.String(), stderr.String())
		}
		if u, ok := strings.CutPrefix(line, "FaultLine COMMIT_PROOF page: "); ok {
			url = strings.TrimSuffix(u, "\n")
		}
	}
	return cmd, url, out, rest, stderr
}

// TestLiveJudgeServer serves the committed sample with TS and Go and
// compares status, headers and body for GET /, GET /x, POST /api/rerun,
// HEAD /, query, dot-segment (every percent-encoded spelling) and
// scheme-relative forms of /, `OPTIONS *`
// (which net/http would answer itself), HTTP/1.0 requests and a
// `Connection: close` request. The raw subtest compares whole exchanges.
func TestLiveJudgeServer(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	t.Run("raw", func(t *testing.T) { testJudgeRawExchanges(t, repo) })
	requests := []judgeRequest{
		{"GET", "/", "HTTP/1.1", ""}, {"GET", "/x", "HTTP/1.1", ""}, {"POST", "/api/rerun", "HTTP/1.1", "Content-Length: 0"},
		{"HEAD", "/", "HTTP/1.1", ""}, {"GET", "/?a=1", "HTTP/1.1", ""}, {"GET", "/x/..", "HTTP/1.1", ""}, {"GET", "/%2e", "HTTP/1.1", ""},
		// Every percent-encoded spelling of a dot segment (r10-3 P3): each
		// resolves /x/<dots> to /, and /x/<dots>/y to /y.
		{"GET", "/x/.%2e", "HTTP/1.1", ""}, {"GET", "/x/%2e.", "HTTP/1.1", ""}, {"GET", "/x/%2e%2e", "HTTP/1.1", ""},
		{"GET", "/x/.%2E", "HTTP/1.1", ""}, {"GET", "/x/%2E.", "HTTP/1.1", ""}, {"GET", "/x/%2e%2E", "HTTP/1.1", ""}, {"GET", "/x/%2E%2E", "HTTP/1.1", ""},
		{"GET", "/x/.%2e/", "HTTP/1.1", ""}, {"GET", "/x/%2E./y", "HTTP/1.1", ""}, {"GET", "/%2E", "HTTP/1.1", ""}, {"GET", "/x/%2e", "HTTP/1.1", ""},
		{"GET", "/x/%2e/", "HTTP/1.1", ""}, {"GET", "/x/.%2e%2e", "HTTP/1.1", ""},
		{"GET", "//host/", "HTTP/1.1", ""}, {"GET", "//host/x", "HTTP/1.1", ""}, {"OPTIONS", "*", "HTTP/1.1", ""}, {"OPTIONS", "/", "HTTP/1.1", ""},
		{"GET", "/", "HTTP/1.1", "Connection: close"}, {"GET", "/", "HTTP/1.0", ""}, {"GET", "/x", "HTTP/1.0", "Connection: keep-alive"}, {"HEAD", "/", "HTTP/1.0", ""},
	}
	replies := map[string][]httpReply{}
	outputs := map[string]string{}
	for _, side := range judgeSides(repo) {
		cmd, url, out, rest, stderr := startJudge(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "--port", "0", "--serve-ms", "3000")
		for _, r := range requests {
			replies[side.name] = append(replies[side.name], fetch(t, strings.TrimPrefix(url, "http://"), r))
		}
		tail, _ := io.ReadAll(rest)
		out.Write(tail)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("%s: %v\n%s", side.name, err, stderr.String())
		}
		outputs[side.name] = loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + stderr.String()
	}
	if outputs["Go"] != outputs["TS"] {
		t.Errorf("output differs\n--- Go\n%s--- TS\n%s", outputs["Go"], outputs["TS"])
	}
	for i, r := range requests {
		got, want := replies["Go"][i], replies["TS"][i]
		if got.status != want.status || got.body != want.body {
			t.Errorf("%v: Go %d (%d bytes), TS %d (%d bytes)", r, got.status, len(got.body), want.status, len(want.body))
		}
		for _, name := range tsSetHeaders {
			if got.headers[name] != want.headers[name] {
				t.Errorf("%v: %s: Go %q, TS %q", r, name, got.headers[name], want.headers[name])
			}
		}
	}
}

// testJudgeRawExchanges (TestLiveJudgeServer/raw) sends each request as raw
// bytes on a fresh connection to a TS and a Go judge-proof and compares
// every byte the client receives (Date masked, each response's header
// lines sorted: their order differs, KNOWN_DIFFERENCES.md), how the server
// ends the connection and when (within 1.5 s). These are the requests whose
// framing net/http would handle itself (round 11, r11-2):
//   - `Expect`: any value but 100-continue gets Node's empty 417 through
//     the normal keep-alive response (net/http answers it with its own 417
//     and closes);
func testJudgeRawExchanges(t *testing.T, repo string) {
	post := "POST /x HTTP/1.1\r\nHost: x\r\n"
	get := "GET /x HTTP/1.1\r\nHost: x\r\n\r\n"
	cases := []struct{ name, send string }{
		{"expect-other", "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nExpect: foo\r\n\r\n"},
		{"expect-other-get", "GET / HTTP/1.1\r\nHost: x\r\nExpect: foo\r\n\r\n"},
		{"expect-other-body", post + "Content-Length: 5\r\nExpect: foo\r\n\r\nabcde" + get},
		{"expect-other-close", "GET /x HTTP/1.1\r\nHost: x\r\nConnection: close\r\nExpect: foo\r\n\r\n"},
		{"expect-other-head", "HEAD / HTTP/1.1\r\nHost: x\r\nExpect: foo\r\n\r\n"},
		{"expect-empty", "GET /x HTTP/1.1\r\nHost: x\r\nExpect: \r\n\r\n"},
		{"expect-word", "GET /x HTTP/1.1\r\nHost: x\r\nExpect: x100-continue\r\n\r\n"},
	}
	type exchange struct {
		received string
		end      string // how the server ended the connection
		after    time.Duration
	}
	got := map[string][]exchange{}
	for _, side := range judgeSides(repo) {
		cmd, url, out, rest, stderr := startJudge(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "--port", "0")
		go io.Copy(out, rest)
		results := make([]exchange, len(cases))
		var wg sync.WaitGroup
		for i, tc := range cases {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				start := time.Now()
				go io.WriteString(conn, tc.send)
				conn.SetReadDeadline(start.Add(9 * time.Second))
				var received bytes.Buffer
				_, err = io.Copy(&received, conn)
				end := "closed"
				switch {
				case errors.Is(err, os.ErrDeadlineExceeded):
					end = "open after 9 s"
				case err != nil:
					end = "reset"
				}
				results[i] = exchange{received.String(), end, time.Since(start)}
			}()
		}
		wg.Wait()
		cmd.Process.Signal(syscall.SIGTERM)
		if err := cmd.Wait(); err != nil {
			t.Errorf("%s: %v\n%s", side.name, err, stderr.String())
		}
		got[side.name] = results
	}
	for i, tc := range cases {
		g, w := got["Go"][i], got["TS"][i]
		gotText, wantText := normalizeExchange(g.received), normalizeExchange(w.received)
		summary := fmt.Sprintf("Go: %s after %v; TS: %s after %v", g.end, g.after.Round(time.Millisecond), w.end, w.after.Round(time.Millisecond))
		if gotText != wantText || g.end != w.end {
			t.Errorf("%s: %s\n--- Go\n%q\n--- TS\n%q", tc.name, summary, gotText, wantText)
		}
		if d := g.after - w.after; d > 1500*time.Millisecond || d < -1500*time.Millisecond {
			t.Errorf("%s: close time differs; %s", tc.name, summary)
		}
	}
}

var responseStart = regexp.MustCompile(`HTTP/1\.1 \d{3} `)

// normalizeExchange masks each response's Date and sorts its header lines.
func normalizeExchange(received string) string {
	var out strings.Builder
	rest := received
	for {
		loc := responseStart.FindStringIndex(rest)
		if loc == nil {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:loc[0]])
		rest = rest[loc[0]:]
		end := strings.Index(rest, "\r\n\r\n")
		if end < 0 {
			out.WriteString(rest)
			return out.String()
		}
		lines := strings.Split(rest[:end], "\r\n")
		for j, line := range lines {
			if strings.HasPrefix(line, "Date: ") {
				lines[j] = "Date: <DATE>"
			}
		}
		sort.Strings(lines[1:])
		out.WriteString(strings.Join(lines, "\r\n") + "\r\n\r\n")
		rest = rest[end+4:]
	}
}

// TestLiveJudgeSignalStop serves without --serve-ms and stops each side with
// SIGINT and with SIGTERM once the page URL is printed: both print the
// stopped line and exit 0. Each signal is also sent to a judge-proof that
// inherited it as SIG_IGN (a shell traps it to nothing, then execs): Node
// resets it to SIG_DFL and its listener catches it, so fl must too (round 7
// #6: on Linux an ignored SIGTERM was left at SIG_DFL and killed fl). The
// held-connection subtests stop it while a client holds a connection open.
func TestLiveJudgeSignalStop(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		for _, inherited := range []string{"default", "ignored"} {
			t.Run(sig.String()+"/"+inherited, func(t *testing.T) {
				got := map[string]string{}
				for _, side := range judgeSides(repo) {
					if inherited == "ignored" {
						name := map[syscall.Signal]string{syscall.SIGINT: "INT", syscall.SIGTERM: "TERM"}[sig]
						side.argv = append([]string{"sh", "-c", "trap '' " + name + `; exec "$@"`, "sh"}, side.argv...)
					}
					cmd, _, out, rest, stderr := startJudge(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "--port", "0")
					// Two lines follow the URL ("Press Ctrl+C", the browser
					// line); the listeners come right after, so wait a moment.
					line, _ := rest.ReadString('\n')
					out.WriteString(line)
					line, _ = rest.ReadString('\n')
					out.WriteString(line)
					time.Sleep(300 * time.Millisecond)
					if err := cmd.Process.Signal(sig); err != nil {
						t.Fatal(err)
					}
					tail, _ := io.ReadAll(rest)
					out.Write(tail)
					cmd.Wait()
					got[side.name] = loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + "--- stderr\n" + stderr.String() + "--- " + cmd.ProcessState.String()
				}
				if got["Go"] != got["TS"] {
					t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
				}
				if !strings.Contains(got["Go"], "FaultLine judge-proof stopped.") || !strings.HasSuffix(got["Go"], "exit status 0") {
					t.Errorf("not a clean stop:\n%s", got["Go"])
				}
			})
		}
	}
	t.Run("held-connection", func(t *testing.T) { testJudgeHeldConnection(t, repo) })
}

// testJudgeHeldConnection (TestLiveJudgeSignalStop/held-connection) stops judge-proof while a client
// holds a connection open, as Node's server.close() treats each kind
// (round 10, r10-2): close destroys only idle connections and completes once
// the others have closed.
//   - stall: a POST whose body never completes is answered at once (Node
//     calls the listener on the headers), then destroyed by the keep-alive
//     socket timeout 6 s after the response; fl exits then.
//   - served-partial: a served keep-alive connection that started a second
//     request is not idle either; the same timeout ends it.
//   - silent / partial-headers: a connection that sent nothing (a browser
//     preconnect) or half a header is never idle and has no timeout after
//     close (the 30 s request-timeout check stops with it), so fl keeps
//     serving until the client gives up; here the client closes after 8 s.
//   - a second identical signal ends the process by that signal (Node's
//     process.once listener is gone); a different second signal is
//     consumed, a third (repeating it) kills.
//   - served-idle: a served keep-alive connection is idle and destroyed at
//     once; mid-response: a slow reader's pipelined responses, still being
//     written, do not keep it either.
//   - expect-other*: an Expect other than 100-continue, answered 417 by
//     Node's normal response path (round 11, r11-2).
//
// Each case compares the stopped line, the exit status, what the client
// received and when the server closed the connection (within 1.5 s).
func testJudgeHeldConnection(t *testing.T, repo string) {
	type heldCase struct {
		name     string
		send     []string // written in turn, 0.5 s apart
		signals  []syscall.Signal
		clientMs int // the client closes after this long (0: never)
		// slowReader: the client's receive buffer is 4 KiB and it reads
		// nothing until the signals are sent, so the responses to its
		// pipelined requests are still being written when they arrive.
		slowReader bool
	}
	cases := []heldCase{
		{"stall", []string{"POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 1000\r\n\r\na=1"}, []syscall.Signal{syscall.SIGTERM}, 0, false},
		{"stall-sigint", []string{"POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n\r\nab"}, []syscall.Signal{syscall.SIGINT}, 0, false},
		{"served-partial", []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", "GET / HT"}, []syscall.Signal{syscall.SIGTERM}, 0, false},
		{"silent", nil, []syscall.Signal{syscall.SIGTERM}, 8000, false},
		{"partial-headers", []string{"GET / HTTP/1.1\r\nHost:"}, []syscall.Signal{syscall.SIGINT}, 8000, false},
		{"silent-sigint-twice", nil, []syscall.Signal{syscall.SIGINT, syscall.SIGINT}, 0, false},
		{"silent-sigterm-twice", nil, []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM}, 0, false},
		{"silent-sigint-sigterm", nil, []syscall.Signal{syscall.SIGINT, syscall.SIGTERM}, 6000, false},
		{"silent-int-term-term", nil, []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGTERM}, 0, false},
		{"stall-sigterm-twice", []string{"POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n\r\nab"}, []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM}, 0, false},
		// A browser's connection after it loaded the page: served, kept
		// alive, idle. close() destroys it at once (r10-3 verify F6).
		{"served-idle", []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\n"}, []syscall.Signal{syscall.SIGTERM}, 0, false},
		{"served-idle-sigint", []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\nGET /x HTTP/1.1\r\nHost: x\r\n\r\n"}, []syscall.Signal{syscall.SIGINT}, 0, false},
		// Responses still being written at the signal (r10-3 verify F7):
		// Node's handler has ended each response, so the connection counts
		// as idle and is destroyed with its unsent bytes.
		{"mid-response", []string{strings.Repeat("GET / HTTP/1.1\r\nHost: x\r\n\r\n", 40)}, []syscall.Signal{syscall.SIGTERM}, 0, true},
		// An Expect other than 100-continue (round 11, r11-2 P2): Node's
		// 417 is a normal keep-alive response and the body that never
		// comes ends with the 6 s timeout. net/http answered it itself, so
		// the connection never went idle and fl never exited.
		{"expect-other", []string{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nExpect: foo\r\n\r\n"}, []syscall.Signal{syscall.SIGTERM}, 0, false},
		{"expect-other-sigint", []string{"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nExpect: foo\r\n\r\n"}, []syscall.Signal{syscall.SIGINT}, 0, false},
		{"expect-other-served", []string{"GET /x HTTP/1.1\r\nHost: x\r\nExpect: foo\r\n\r\n"}, []syscall.Signal{syscall.SIGTERM}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			type outcome struct {
				text   string
				closed time.Duration // when the server closed the connection, after the first signal
				exited time.Duration // when fl exited, after the first signal
				bytes  int           // what the client could read
			}
			got := map[string]outcome{}
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, side := range judgeSides(repo) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cmd, url, out, rest, stderr := startJudge(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "--port", "0")
					line, _ := rest.ReadString('\n')
					out.WriteString(line)
					line, _ = rest.ReadString('\n')
					out.WriteString(line)
					var dialer net.Dialer
					if tc.slowReader {
						dialer.Control = func(_, _ string, rc syscall.RawConn) error {
							return rc.Control(func(fd uintptr) { syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096) })
						}
					}
					conn, err := dialer.Dial("tcp", strings.TrimPrefix(url, "http://"))
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					var received bytes.Buffer
					closedAt := make(chan time.Time, 1)
					read := func() {
						io.Copy(&received, conn)
						closedAt <- time.Now()
					}
					if !tc.slowReader {
						go read()
					}
					for _, chunk := range tc.send {
						io.WriteString(conn, chunk)
						time.Sleep(500 * time.Millisecond)
					}
					time.Sleep(300 * time.Millisecond)
					start := time.Now()
					for _, sig := range tc.signals {
						cmd.Process.Signal(sig)
						time.Sleep(300 * time.Millisecond)
					}
					if tc.slowReader {
						go read()
					}
					if tc.clientMs > 0 {
						time.AfterFunc(time.Duration(tc.clientMs)*time.Millisecond, func() { conn.Close() })
					}
					exited := make(chan struct{})
					var exitedAfter time.Duration
					go func() {
						tail, _ := io.ReadAll(rest)
						out.Write(tail)
						cmd.Wait()
						exitedAfter = time.Since(start)
						close(exited)
					}()
					select {
					case <-exited:
					case <-time.After(30 * time.Second):
						cmd.Process.Kill()
						<-exited
						out.WriteString("--- still running 30 s after the first signal\n")
					}
					conn.Close()
					closed := time.Duration(-1)
					select {
					case at := <-closedAt:
						closed = at.Sub(start)
					case <-time.After(time.Second):
					}
					head, _, _ := strings.Cut(received.String(), "\r\n")
					text := loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + "--- stderr\n" + stderr.String() +
						"--- " + cmd.ProcessState.String()
					// A slow reader's connection is destroyed with pipelined
					// requests still unread, so the server's kernel resets it,
					// and whether the client still reads the bytes already in
					// its buffer depends on that reset's timing, run by run on
					// either side (macOS often drops them). Only the timing is
					// compared then.
					if !tc.slowReader {
						text += "\n--- client got " + head
					}
					mu.Lock()
					got[side.name] = outcome{text, closed, exitedAfter, received.Len()}
					mu.Unlock()
				}()
			}
			wg.Wait()
			// Every failure line carries the whole comparison: CI
			// annotations show only the failure message.
			summary := fmt.Sprintf("Go: closed %v, exited %v, client read %d bytes; TS: closed %v, exited %v, client read %d bytes (after the first signal)",
				got["Go"].closed, got["Go"].exited, got["Go"].bytes, got["TS"].closed, got["TS"].exited, got["TS"].bytes)
			if got["Go"].text != got["TS"].text {
				t.Errorf("%s: output differs; %s\n--- Go\n%s\n--- TS\n%s", tc.name, summary, got["Go"].text, got["TS"].text)
			}
			if d := got["Go"].closed - got["TS"].closed; d > 1500*time.Millisecond || d < -1500*time.Millisecond {
				t.Errorf("%s: connection close time differs; %s", tc.name, summary)
			}
			if d := got["Go"].exited - got["TS"].exited; d > 1500*time.Millisecond || d < -1500*time.Millisecond {
				t.Errorf("%s: exit time differs; %s", tc.name, summary)
			}
			t.Logf("%s: %s", tc.name, summary)
		})
	}
}

// TestLiveBrowserOpen runs judge-proof with the browser enabled and the fake
// openers (difftest/testdata/fakebrowser) first on PATH, succeeding and
// failing, and compares what they were run with and what fl printed. exec's
// callback only reports while fl runs, so each side serves until the opener
// has logged (and, when it fails, until the failure is reported), then gets
// SIGTERM.
func TestLiveBrowserOpen(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fake := filepath.Join(repo, "difftest", "testdata", "fakebrowser")
	for _, exit := range []string{"0", "1"} {
		t.Run("exit-"+exit, func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
				log := filepath.Join(t.TempDir(), "browser.log")
				env := judgeEnv("PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "FAULTLINE_BROWSER_LOG="+log, "FAULTLINE_BROWSER_EXIT="+exit)
				cmd, _, out, rest, stderr := startJudge(t, repo, side, env, "--port", "0")
				done := make(chan struct{})
				go func() {
					tail, _ := io.ReadAll(rest)
					out.Write(tail)
					close(done)
				}()
				deadline := time.Now().Add(60 * time.Second)
				for {
					_, err := os.Stat(log)
					if err == nil && (exit == "0" || strings.Contains(stderr.String(), "could not open a browser")) {
						break
					}
					if time.Now().After(deadline) {
						cmd.Process.Kill()
						t.Fatalf("%s: the opener did not run or report\n%s", side.name, stderr.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
				// A successful opener prints nothing; leave its callback a
				// moment to (not) report.
				time.Sleep(200 * time.Millisecond)
				cmd.Process.Signal(syscall.SIGTERM)
				<-done
				if err := cmd.Wait(); err != nil {
					t.Fatalf("%s: %v\n%s", side.name, err, stderr.String())
				}
				logged, _ := os.ReadFile(log)
				got[side.name] = loopbackPort.ReplaceAllString("--- stdout\n"+out.String()+"--- stderr\n"+stderr.String()+"--- opener\n"+string(logged), "127.0.0.1:<PORT>")
			}
			if got["Go"] != got["TS"] {
				t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
			}
			if (exit == "1") != strings.Contains(got["Go"], "could not open a browser") {
				t.Errorf("exit %s: unexpected browser failure report\n%s", exit, got["Go"])
			}
		})
	}
}

// lockedBuffer is a bytes.Buffer a test may read while a child writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestLiveSelfIncidentMatch runs `judge-proof --export-only` on the
// committed sample with the recorded self-incident root replaced by the
// sample's own root on both sides (TS: the difftest/gen/selfroot.mjs
// preload; Go: FAULTLINE_TEST_SELF_INCIDENT_ROOT), so the "Matched
// historical self-incident root." branch is compared end to end (round 7
// #14, mutant P3).
func TestLiveSelfIncidentMatch(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	const sampleRoot = "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"
	got := map[string]string{}
	for _, side := range []judgeSide{
		{"TS", []string{"node", "--import", "file://" + filepath.Join(repo, "difftest", "gen", "selfroot.mjs"), filepath.Join(repo, "dist", "cli.js")}},
		{"Go", []string{flBinary}},
	} {
		cmd := exec.Command(side.argv[0], append(side.argv[1:], "judge-proof", "--export-only")...)
		cmd.Dir = judgeWorkspace(t, repo)
		cmd.Env = judgeEnv("FAULTLINE_TEST_SELF_INCIDENT_ROOT=" + sampleRoot)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", side.name, err, out)
		}
		got[side.name] = string(out)
	}
	if got["Go"] != got["TS"] {
		t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
	}
	if !strings.Contains(got["TS"], "\nMatched historical self-incident root.\n") {
		t.Errorf("TS did not take the match branch:\n%s", got["TS"])
	}
}
