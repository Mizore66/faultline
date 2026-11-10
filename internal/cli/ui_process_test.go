package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
)

// uiProcessTail is the rest of stdout: with FAULTLINE_NO_BROWSER=1 the opener
// prints its skip line after the three readiness lines.
const uiProcessTail = ` local artifact\(s\) under \.faultline\nPress Ctrl\+C to stop\.\nBrowser auto-open skipped in CI\. Open http://127\.0\.0\.1:[1-9][0-9]* manually if needed\.\n$`

// TestUIProcess drives the actual `fl ui` process, started outside the
// repository with an empty PATH (no Node, no Go) and FAULTLINE_NO_BROWSER=1.
func TestUIProcess(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fresh := t.TempDir()
	name := "fl"
	if runtime.GOOS == "windows" {
		name = "fl.exe"
	}
	binary := filepath.Join(fresh, name)
	build := exec.Command("go", "build", "-o", binary, "./cmd/fl")
	build.Dir = moduleRoot
	build.Env = deterministicBuildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	consoleHelper := buildJudgeDemoConsoleHelper(t, moduleRoot, fresh)
	newWork := func(t *testing.T, label string) string {
		t.Helper()
		work := filepath.Join(fresh, label)
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(work)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}

	startUI := func(t *testing.T, work, count string, args ...string) *judgeDemoProcess {
		t.Helper()
		pattern := regexp.MustCompile(`^FaultLine UI hub: (http://127\.0\.0\.1:[1-9][0-9]*)\nDiscovered ` + count + uiProcessTail)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		started, err := servefixture.StartWithOptions(ctx, binary, nil, append([]string{"ui"}, args...), builtBinaryEnv(), work,
			servefixture.StartOptions{
				StartupLines: 4, StartupDescription: "ui startup lines",
				ValidateStartup: func(out []byte) (string, error) {
					m := pattern.FindStringSubmatch(string(out))
					if m == nil {
						return "", os.ErrInvalid
					}
					return m[1], nil
				},
			})
		if err != nil {
			t.Fatalf("start ui: %v", err)
		}
		t.Cleanup(func() {
			if err := started.Dispose(); err != nil {
				t.Errorf("ui teardown: %v", err)
			}
		})
		return &judgeDemoProcess{Started: started, url: started.URL, consoleHelper: consoleHelper}
	}
	interruptClean := func(t *testing.T, p *judgeDemoProcess, count, next string) {
		t.Helper()
		p.interrupt(t)
		result := p.wait(t)
		stdoutPattern := regexp.MustCompile(`^FaultLine UI hub: http://127\.0\.0\.1:[1-9][0-9]*\nDiscovered ` + count + uiProcessTail)
		if result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != nil ||
			!stdoutPattern.MatchString(result.Stdout) || result.Stderr != next {
			t.Fatalf("after SIGINT: %+v", result)
		}
	}

	t.Run("empty startup and interrupt", func(t *testing.T) {
		work := newWork(t, "empty")
		p := startUI(t, work, "0", "--port", "0")
		r := judgeDemoRequest("GET", p.url+"/", "")
		if r.err != nil || r.status != 200 || !strings.Contains(r.body, "No local artifacts found") {
			t.Fatalf("index: %+v", r)
		}
		interruptClean(t, p, "0", "Next: fl quickstart\n")
	})

	t.Run("draft raw request and interrupt", func(t *testing.T) {
		work := newWork(t, "draft")
		drafts := filepath.Join(work, ".faultline", "incidents", "drafts")
		if err := os.MkdirAll(drafts, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(drafts, "draft-1.json"), []byte(`{"id":"draft-1"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		p := startUI(t, fresh, "1", "--repo", work, "--port", "0")
		r := judgeDemoRequest("GET", p.url+"/raw/0", "")
		if r.err != nil || r.status != 200 || !strings.Contains(r.body, "draft-1") {
			t.Fatalf("raw: %+v", r)
		}
		miss := judgeDemoRequest("GET", p.url+"/raw/7", "")
		if miss.err != nil || miss.status != 404 {
			t.Fatalf("missing artifact: %+v", miss)
		}
		interruptClean(t, p, "1", "Next: fl ui --repo "+work+"\n")
	})

	failing := func(t *testing.T, work string, args ...string) (string, string, int) {
		t.Helper()
		var out, errb bytes.Buffer
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Env = work, builtBinaryEnv()
		cmd.Stdout, cmd.Stderr = &out, &errb
		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("%v did not exit: stdout=%q", args, out.String())
		}
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("occupied port", func(t *testing.T) {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		stdout, stderr, code := failing(t, newWork(t, "occupied"), "ui", "--port", port)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: listen EADDRINUSE: address already in use 127.0.0.1:"+port) {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("invalid ports", func(t *testing.T) {
		for _, port := range []string{"abc", "-1", "65536", "1.5"} {
			stdout, stderr, code := failing(t, newWork(t, "invalid"+port), "ui", "--port", port)
			if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: --port must be an integer from 0 through 65535.\n") {
				t.Fatalf("port %q: exit=%d stdout=%q stderr=%q", port, code, stdout, stderr)
			}
		}
	})
}
