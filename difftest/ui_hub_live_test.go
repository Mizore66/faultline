//go:build faultline_test && unix

package difftest

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// uiHubFixture builds a repository whose .faultline holds one of each
// artifact kind, in discovery order: a Git proof bundle (0), a turn proof
// bundle (1), an incident draft (2) and a recording ledger (3). Both sides
// share the path because the index page and the "Next" line print it.
func uiHubFixture(t *testing.T, repo string) string {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fl := filepath.Join(dir, ".faultline")
	copyTree(t, filepath.Join(repo, "difftest", "testdata", "serve-bundle", "inputs", "coherent", "bundle"), filepath.Join(fl, "git-proof-bundles", "git"))
	copyTree(t, filepath.Join(repo, "difftest", "testdata", "bases", "turn-two-states"), filepath.Join(fl, "turn-proof-bundles", "turn"))
	for path, text := range map[string]string{
		filepath.Join(fl, "incidents", "drafts", "draft-1.json"): `{"id":"<draft-1>","q":"a&b"}`,
		filepath.Join(fl, "recordings", "ledger-1.json"):         `{"ledger":1}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLiveUIHubRoutes compares status, headers and body of the frozen
// TypeScript hub and fl ui for the index, Git artifact, raw routes and every
// invalid index, plus startup output. Frozen TS serves a turn bundle's raw
// JSON on /artifact/<n> and 404s /raw/<n> for it; Go serves the raw page on
// /raw/<n> and its dedicated dashboard on /artifact/<n>, so the turn probe
// compares TS /artifact/1 with Go /raw/1, and the dashboard itself is not
// compared with TS (it is golden-tested in internal/proofview).
func TestLiveUIHubRoutes(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fixture := uiHubFixture(t, repo)
	empty, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	same := func(method, target, header string) [2]judgeRequest {
		r := judgeRequest{method, target, "HTTP/1.1", header}
		return [2]judgeRequest{r, r}
	}
	get := func(target string) [2]judgeRequest { return same("GET", target, "") }
	// requests[0] is sent to TS, requests[1] to Go.
	probes := []struct {
		name     string
		repo     string
		requests [2]judgeRequest
	}{
		{"empty index", empty, get("/")},
		{"empty artifact 0", empty, get("/artifact/0")},
		{"empty raw 0", empty, get("/raw/0")},
		{"index", fixture, get("/")},
		{"index query", fixture, get("/?x=1")},
		{"git artifact", fixture, get("/artifact/0")},
		{"turn raw JSON", fixture, [2]judgeRequest{{"GET", "/artifact/1", "HTTP/1.1", ""}, {"GET", "/raw/1", "HTTP/1.1", ""}}},
		{"draft raw", fixture, get("/raw/2")},
		{"ledger raw", fixture, get("/raw/3")},
		{"raw of git bundle", fixture, get("/raw/0")},
		{"artifact of draft", fixture, get("/artifact/2")},
		{"artifact of ledger", fixture, get("/artifact/3")},
		{"artifact past end", fixture, get("/artifact/4")},
		{"raw past end", fixture, get("/raw/4")},
		{"artifact huge", fixture, get("/artifact/99999999999999999999")},
		{"artifact non-numeric", fixture, get("/artifact/x")},
		{"artifact negative", fixture, get("/artifact/-1")},
		{"artifact empty", fixture, get("/artifact/")},
		{"artifact leading zero", fixture, get("/artifact/00")},
		{"artifact trailing slash", fixture, get("/artifact/0/")},
		{"unknown", fixture, get("/nope")},
		{"post index", fixture, same("POST", "/", "Content-Length: 0")},
		{"head index", fixture, same("HEAD", "/", "")},
	}
	sides := judgeSides(repo)
	replies := map[string]httpReply{}
	var outputs [2]map[string]string
	for i := range outputs {
		outputs[i] = map[string]string{}
	}
	for s, side := range sides {
		for i, hub := range []string{empty, fixture} {
			cmd, url, out, rest, stderr := startJudgeCommand(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "ui", "FaultLine UI hub: ", "--repo", hub, "--port", "0")
			for _, p := range probes {
				if p.repo == hub {
					replies[side.name+"/"+p.name] = fetch(t, strings.TrimPrefix(url, "http://"), p.requests[s])
				}
			}
			time.Sleep(200 * time.Millisecond)
			cmd.Process.Signal(syscall.SIGINT)
			tail, _ := io.ReadAll(rest)
			out.Write(tail)
			cmd.Wait()
			outputs[i][side.name] = loopbackPort.ReplaceAllString(out.String(), "127.0.0.1:<PORT>") + "--- stderr\n" + stderr.String()
		}
	}
	for i, count := range []string{"0", "4"} {
		if outputs[i]["Go"] != outputs[i]["TS"] {
			t.Errorf("startup output %d differs\n--- Go\n%s\n--- TS\n%s", i, outputs[i]["Go"], outputs[i]["TS"])
		}
		if !strings.Contains(outputs[i]["Go"], "Discovered "+count+" local artifact(s)") {
			t.Errorf("expected %s artifacts:\n%s", count, outputs[i]["Go"])
		}
	}
	for _, p := range probes {
		got, want := replies["Go/"+p.name], replies["TS/"+p.name]
		if got.status != want.status || got.body != want.body {
			t.Errorf("%s: Go %d (%d bytes), TS %d (%d bytes)\n--- Go\n%.400s\n--- TS\n%.400s", p.name, got.status, len(got.body), want.status, len(want.body), got.body, want.body)
		}
		for _, name := range tsSetHeaders {
			if got.headers[name] != want.headers[name] {
				t.Errorf("%s: %s: Go %q, TS %q", p.name, name, got.headers[name], want.headers[name])
			}
		}
	}

	// The Go-only dashboard replaces TS's raw JSON page on /artifact/<turn>.
	cmd, url, _, rest, _ := startJudgeCommand(t, repo, sides[1], judgeEnv("FAULTLINE_NO_BROWSER=1"), "ui", "FaultLine UI hub: ", "--repo", fixture, "--port", "0")
	address := strings.TrimPrefix(url, "http://")
	dash := fetch(t, address, judgeRequest{"GET", "/artifact/1", "HTTP/1.1", ""})
	raw := fetch(t, address, judgeRequest{"GET", "/raw/1", "HTTP/1.1", ""})
	cmd.Process.Signal(syscall.SIGINT)
	io.Copy(io.Discard, rest)
	cmd.Wait()
	if dash.status != 200 || dash.body == raw.body || !strings.Contains(dash.body, "FaultLine turn dashboard") {
		t.Errorf("Go turn dashboard is not distinct from the raw page: %d %.200s", dash.status, dash.body)
	}
}

// TestLiveUIHubInvalidStart compares the --port refusals and the
// occupied-port failure.
func TestLiveUIHubInvalidStart(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	for _, port := range []string{"abc", "-1", "65536", "1.5", " 7 ", "1e2x", "0x10000", occupied} {
		got := map[string]string{}
		for _, side := range judgeSides(repo) {
			args := append(append([]string{}, side.argv[1:]...), "ui", "--port", port)
			cmd := exec.Command(side.argv[0], args...)
			cmd.Dir = judgeWorkspace(t, repo)
			cmd.Env = judgeEnv("FAULTLINE_NO_BROWSER=1")
			var stdout, stderr lockedBuffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
				<-done
				t.Errorf("%s port %q: did not exit; stdout %q", side.name, port, stdout.String())
			}
			got[side.name] = "--- stdout\n" + stdout.String() + "--- stderr\n" + stderr.String() + "--- " + cmd.ProcessState.String()
		}
		got["Go"] = strings.ReplaceAll(got["Go"], occupied, "<PORT>")
		got["TS"] = strings.ReplaceAll(got["TS"], occupied, "<PORT>")
		if got["Go"] != got["TS"] {
			t.Errorf("port %q\n--- Go\n%s\n--- TS\n%s", port, got["Go"], got["TS"])
		}
	}
}

// TestLiveUIHubSignalStop stops each side with SIGINT once the readiness
// lines are printed: no stop summary, exit 0, same output.
func TestLiveUIHubSignalStop(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fixture := uiHubFixture(t, repo)
	got := map[string]string{}
	for _, side := range judgeSides(repo) {
		cmd, _, out, rest, stderr := startJudgeCommand(t, repo, side, judgeEnv("FAULTLINE_NO_BROWSER=1"), "ui", "FaultLine UI hub: ", "--repo", fixture, "--port", "0")
		for i := 0; i < 2; i++ {
			line, _ := rest.ReadString('\n')
			out.WriteString(line)
		}
		time.Sleep(300 * time.Millisecond)
		if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
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
	if !strings.HasSuffix(got["Go"], "exit status 0") || strings.Contains(got["Go"], "stopped") {
		t.Errorf("not a clean stop:\n%s", got["Go"])
	}
}

// TestLiveUIHubBrowser compares the browser lifecycle: suppression by
// FAULTLINE_NO_BROWSER, and the fake opener succeeding and failing.
func TestLiveUIHubBrowser(t *testing.T) {
	oracle.Skip(t)
	repo := repoRoot(t)
	fixture := uiHubFixture(t, repo)
	fake := filepath.Join(repo, "difftest", "testdata", "fakebrowser")
	for _, mode := range []string{"suppressed", "exit-0", "exit-1"} {
		t.Run(mode, func(t *testing.T) {
			got := map[string]string{}
			for _, side := range judgeSides(repo) {
				log := filepath.Join(t.TempDir(), "browser.log")
				path := "PATH=" + fake + string(os.PathListSeparator) + os.Getenv("PATH")
				var env []string
				if mode == "suppressed" {
					env = judgeEnv("FAULTLINE_NO_BROWSER=1", path, "FAULTLINE_BROWSER_LOG="+log)
				} else {
					env = judgeEnv(path, "FAULTLINE_BROWSER_LOG="+log, "FAULTLINE_BROWSER_EXIT="+strings.TrimPrefix(mode, "exit-"))
				}
				cmd, _, out, rest, stderr := startJudgeCommand(t, repo, side, env, "ui", "FaultLine UI hub: ", "--repo", fixture, "--port", "0")
				done := make(chan struct{})
				go func() {
					tail, _ := io.ReadAll(rest)
					out.Write(tail)
					close(done)
				}()
				if mode == "suppressed" {
					time.Sleep(1500 * time.Millisecond)
				} else {
					deadline := time.Now().Add(60 * time.Second)
					for {
						_, err := os.Stat(log)
						if err == nil && (mode == "exit-0" || strings.Contains(stderr.String(), "could not open a browser")) {
							break
						}
						if time.Now().After(deadline) {
							cmd.Process.Kill()
							t.Fatalf("%s: the opener did not run or report\n%s", side.name, stderr.String())
						}
						time.Sleep(20 * time.Millisecond)
					}
					time.Sleep(200 * time.Millisecond)
				}
				cmd.Process.Signal(syscall.SIGINT)
				<-done
				cmd.Wait()
				logged, _ := os.ReadFile(log)
				got[side.name] = loopbackPort.ReplaceAllString("--- stdout\n"+out.String()+"--- stderr\n"+stderr.String()+"--- opener\n"+string(logged)+"--- "+cmd.ProcessState.String(), "127.0.0.1:<PORT>")
				if mode == "suppressed" && len(logged) != 0 {
					t.Errorf("%s: opener ran while suppressed: %q", side.name, logged)
				}
			}
			if got["Go"] != got["TS"] {
				t.Errorf("Go\n%s\nTS\n%s", got["Go"], got["TS"])
			}
			if (mode == "exit-1") != strings.Contains(got["Go"], "could not open a browser") {
				t.Errorf("%s: unexpected browser failure report\n%s", mode, got["Go"])
			}
		})
	}
}
