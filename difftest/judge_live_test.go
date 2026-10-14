//go:build unix

package difftest

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func fetch(t *testing.T, method, url string) httpReply {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ask for the raw framing: the client would otherwise strip
	// Transfer-Encoding into resp.TransferEncoding.
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	headers := map[string]string{}
	for _, name := range tsSetHeaders {
		headers[name] = resp.Header.Get(name)
	}
	if len(resp.TransferEncoding) > 0 {
		headers["Transfer-Encoding"] = strings.Join(resp.TransferEncoding, ",")
	}
	if resp.ContentLength >= 0 && method != "HEAD" {
		headers["Content-Length"] = itoa(resp.ContentLength)
	}
	if _, err := http.ParseTime(resp.Header.Get("Date")); err != nil {
		t.Errorf("%s %s: Date %q is not an HTTP date", method, url, resp.Header.Get("Date"))
	}
	return httpReply{resp.StatusCode, headers, string(body)}
}

// TestLiveJudgeServer serves the committed sample with TS and Go and
// compares GET /, GET /x, POST /api/rerun, HEAD / and a query and
// dot-segment form of /: status, headers and body.
func TestLiveJudgeServer(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	requests := [][2]string{{"GET", "/"}, {"GET", "/x"}, {"POST", "/api/rerun"}, {"HEAD", "/"}, {"GET", "/?a=1"}, {"GET", "/x/.."}, {"GET", "/%2e"}}
	replies := map[string][]httpReply{}
	outputs := map[string]string{}
	for _, side := range judgeSides(repo) {
		ws := judgeWorkspace(t, repo)
		cmd := exec.Command(side.argv[0], append(side.argv[1:], "judge-proof", "--port", "0", "--serve-ms", "3000")...)
		cmd.Dir = ws
		cmd.Env = judgeEnv("FAULTLINE_NO_BROWSER=1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		reader := bufio.NewReader(stdout)
		url := ""
		for url == "" {
			line, err := reader.ReadString('\n')
			out.WriteString(line)
			if err != nil {
				t.Fatalf("%s: no page URL\n%s%s", side.name, out.String(), stderr.String())
			}
			if rest, ok := strings.CutPrefix(line, "FaultLine COMMIT_PROOF page: "); ok {
				url = strings.TrimSuffix(rest, "\n")
			}
		}
		for _, r := range requests {
			replies[side.name] = append(replies[side.name], fetch(t, r[0], url+r[1]))
		}
		rest, _ := io.ReadAll(reader)
		out.Write(rest)
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
			t.Errorf("%s %s: Go %d (%d bytes), TS %d (%d bytes)", r[0], r[1], got.status, len(got.body), want.status, len(want.body))
		}
		for _, name := range tsSetHeaders {
			if got.headers[name] != want.headers[name] {
				t.Errorf("%s %s: %s: Go %q, TS %q", r[0], r[1], name, got.headers[name], want.headers[name])
			}
		}
	}
}

// TestLiveBrowserOpen runs judge-proof with the browser enabled and the fake
// openers (difftest/testdata/fakebrowser) first on PATH, succeeding and
// failing, and compares what they were run with and what fl printed.
func TestLiveBrowserOpen(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fake := filepath.Join(repo, "difftest", "testdata", "fakebrowser")
	for _, exit := range []string{"0", "1"} {
		t.Run("exit-"+exit, func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
				ws := judgeWorkspace(t, repo)
				log := filepath.Join(ws, "browser.log")
				// --serve-ms leaves the opener time to finish: exec's
				// callback only reports while fl runs.
				cmd := exec.Command(side.argv[0], append(side.argv[1:], "judge-proof", "--port", "0", "--serve-ms", "5000")...)
				cmd.Dir = ws
				cmd.Env = judgeEnv("PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "FAULTLINE_BROWSER_LOG="+log, "FAULTLINE_BROWSER_EXIT="+exit)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				start := time.Now()
				if err := cmd.Run(); err != nil {
					t.Fatalf("%s: %v\n%s", side.name, err, stderr.String())
				}
				if time.Since(start) < 5*time.Second {
					t.Errorf("%s returned before --serve-ms", side.name)
				}
				logged, err := os.ReadFile(log)
				if err != nil {
					t.Fatalf("%s: the opener did not run: %v\n%s%s", side.name, err, stdout.String(), stderr.String())
				}
				got[side.name] = loopbackPort.ReplaceAllString("--- stdout\n"+stdout.String()+"--- stderr\n"+stderr.String()+"--- opener\n"+string(logged), "127.0.0.1:<PORT>")
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
