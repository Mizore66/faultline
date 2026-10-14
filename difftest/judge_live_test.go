//go:build unix

package difftest

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
// HEAD /, query, dot-segment and scheme-relative forms of /, HTTP/1.0
// requests and a `Connection: close` request.
func TestLiveJudgeServer(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	requests := []judgeRequest{
		{"GET", "/", "HTTP/1.1", ""}, {"GET", "/x", "HTTP/1.1", ""}, {"POST", "/api/rerun", "HTTP/1.1", "Content-Length: 0"},
		{"HEAD", "/", "HTTP/1.1", ""}, {"GET", "/?a=1", "HTTP/1.1", ""}, {"GET", "/x/..", "HTTP/1.1", ""}, {"GET", "/%2e", "HTTP/1.1", ""},
		{"GET", "//host/", "HTTP/1.1", ""}, {"GET", "//host/x", "HTTP/1.1", ""},
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

// TestLiveJudgeSignalStop serves without --serve-ms and stops each side with
// SIGINT and with SIGTERM once the page URL is printed: both print the
// stopped line and exit 0.
func TestLiveJudgeSignalStop(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
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
