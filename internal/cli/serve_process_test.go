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
	"github.com/Mizore66/faultline/internal/bundle/demo"
)

// TestPlainServeBuiltBinaryProcess drives the actual `fl serve` process,
// started outside the repository with an empty PATH (no Node, no Go).
func TestPlainServeBuiltBinaryProcess(t *testing.T) {
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

	t.Run("startup rerun twice and interrupt", func(t *testing.T) {
		work := newWork(t, "serve")
		output := filepath.Join(work, ".faultline", "bundles", "judge-demo")
		urlPattern := regexp.MustCompile(`^FaultLine incident page: (http://127\.0\.0\.1:[1-9][0-9]*)\nPress Ctrl\+C to stop\.\n$`)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		started, err := servefixture.StartWithOptions(ctx, binary, nil, []string{"serve", "--port", "0"}, builtBinaryEnv(), work,
			servefixture.StartOptions{
				StartupLines: 2, StartupDescription: "serve startup lines",
				ValidateStartup: func(out []byte) (string, error) {
					m := urlPattern.FindStringSubmatch(string(out))
					if m == nil {
						return "", os.ErrInvalid
					}
					return m[1], nil
				},
			})
		if err != nil {
			t.Fatalf("start serve: %v", err)
		}
		t.Cleanup(func() {
			if err := started.Dispose(); err != nil {
				t.Errorf("serve teardown: %v", err)
			}
		})
		if _, err := os.Lstat(output); err == nil {
			t.Fatal("startup wrote a bundle")
		}
		if _, err := os.Lstat(output + ".native-execution.json"); err == nil {
			t.Fatal("startup wrote a sidecar")
		}
		p := &judgeDemoProcess{Started: started, url: started.URL, consoleHelper: consoleHelper}

		get := judgeDemoRequest("GET", p.url+"/api/analysis", "")
		if get.err != nil || get.status != 200 || !strings.Contains(get.body, `"mode":"REPLAY"`) {
			t.Fatalf("initial analysis: %+v", get)
		}
		var roots, generated []string
		for i := 0; i < 2; i++ {
			post := judgeDemoRequest("POST", p.url+"/api/rerun", "")
			if post.err != nil || post.status != 200 || !strings.Contains(post.body, `"ok":true`) || !strings.Contains(post.body, `"mode":"RERUN"`) {
				t.Fatalf("rerun %d: %+v", i, post)
			}
			after := judgeDemoRequest("GET", p.url+"/api/analysis", "")
			if after.err != nil || after.status != 200 || !strings.Contains(after.body, `"mode":"RERUN"`) {
				t.Fatalf("analysis after rerun %d: %+v", i, after)
			}
			verification := demo.Verify(output, "", false)
			if !verification.Valid || verification.RootDigest == nil {
				t.Fatalf("bundle after rerun %d invalid: %+v", i, verification)
			}
			analysis := readValue(t, filepath.Join(output, "analysis.json"))
			requireString(t, "bundle analysis.mode", analysis.Get("mode"), "RERUN")
			roots = append(roots, *verification.RootDigest)
			generated = append(generated, analysis.Get("generatedAt").Str())
			if i == 0 {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if roots[0] == roots[1] || generated[0] == generated[1] {
			t.Fatalf("second rerun did not carry fresh evidence: roots=%v generatedAt=%v", roots, generated)
		}
		p.interrupt(t)
		result := p.wait(t)
		if result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != nil || result.Stderr != "" ||
			!urlPattern.MatchString(result.Stdout) {
			t.Fatalf("after SIGINT: %+v", result)
		}
		t.Logf("PLAIN-SERVE-EVIDENCE binary=%s root1=%s root2=%s generatedAt=%v", binary, roots[0], roots[1], generated)
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
		stdout, stderr, code := failing(t, newWork(t, "occupied"), "serve", "--port", port)
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: ") || !strings.Contains(stderr, "address already in use") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("output refusal", func(t *testing.T) {
		work := newWork(t, "refused")
		stdout, stderr, code := failing(t, work, "serve", "--output", filepath.Join(work, "outside"), "--port", "0")
		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "FaultLine error: Proof bundle output must be a child directory of ") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if _, err := os.Lstat(filepath.Join(work, "outside")); err == nil {
			t.Fatal("refused output was created")
		}
	})
}
