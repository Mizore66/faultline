package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
)

const guidedProcessNodeSource = `package main

import "os"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		os.Stdout.WriteString("v22.22.2\n")
		return
	}
	os.Exit(9)
}
`

const (
	guidedProcessPhasePreflight = "FaultLine guided investigate: PREFLIGHT\n"
	guidedProcessPhaseReview    = "FaultLine guided investigate: WITNESS_REVIEW\n"
	guidedProcessPhaseRuntime   = "FaultLine guided investigate: RUNTIME_SELECTION\n"
	guidedProcessReadyText      = "\nApprove, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n"
	guidedProcessNoRuntime      = "FaultLine error: Guided investigate needs a digest-pinned runtime before localization. Pass --runtime <node|python|go> after pulling the curated tag, or --image <digest-pinned-image>.\nNext: pnpm fl help\n"
)

// guidedProcessEnv has an owned PATH: a fake `node` that only answers
// --version (so the real doctor reports READY for Node) followed by the
// directory of the real Git. There is no Docker, so no runtime is ever started.
func guidedProcessEnv(t *testing.T, moduleRoot, fresh string) []string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required for the guided process tests: %v", err)
	}
	toolDir := filepath.Join(fresh, "tools")
	if err := os.Mkdir(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(fresh, "fakenode.go")
	if err := os.WriteFile(source, []byte(guidedProcessNodeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	build := exec.Command("go", "build", "-o", filepath.Join(toolDir, name), source)
	build.Dir = fresh
	build.Env = deterministicBuildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake node: %v\n%s", err, out)
	}
	var env []string
	for _, kv := range builtBinaryEnv() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return append(env, "PATH="+toolDir+string(os.PathListSeparator)+filepath.Dir(gitPath))
}

func guidedProcessGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestGuidedInvestigateProcess drives the actual fl binary, built outside the
// repository, against owned Git fixtures: real review readiness, HTTP
// approval and freeze, first-SIGINT cancellation with retained state, resume
// over the same immutable records, default SIGTERM, and no stdin/browser
// approval. The runtime stage is never reached past its refusal, so no
// provider, Docker or target-repository command ever runs.
func TestGuidedInvestigateProcess(t *testing.T) {
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
	env := guidedProcessEnv(t, moduleRoot, fresh)

	newRepo := func(t *testing.T, label string) string {
		t.Helper()
		dir := filepath.Join(fresh, label)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		guidedProcessGit(t, resolved, "init", "-q")
		for i, content := range []string{"a", "b"} {
			if err := os.WriteFile(filepath.Join(resolved, "f.txt"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			guidedProcessGit(t, resolved, "add", "f.txt")
			guidedProcessGit(t, resolved, "commit", "-q", "-m", []string{"one", "two"}[i])
		}
		if err := os.WriteFile(filepath.Join(fresh, label+".ci.log"), []byte("FAIL expected USD received EUR\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	logPath := func(label string) string { return filepath.Join(fresh, label+".ci.log") }

	// start launches fl with the shared Windows-prepared, process-owned
	// launcher and returns once the complete readiness stderr has appeared
	// (barrier: observed bytes, not a delay).
	start := func(t *testing.T, repo string, lines int, startup *regexp.Regexp, args ...string) *judgeDemoProcess {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		started, err := servefixture.StartWithOptions(ctx, binary, nil, args, env, repo, servefixture.StartOptions{
			StartupLines: lines, StartupDescription: "guided review readiness", ReadyStream: "stderr",
			ValidateStartup: func(out []byte) (string, error) {
				m := startup.FindStringSubmatch(string(out))
				if m == nil {
					return "", os.ErrInvalid
				}
				return m[1], nil
			},
		})
		if err != nil {
			t.Fatalf("start %v: %v", args, err)
		}
		t.Cleanup(func() {
			if err := started.Dispose(); err != nil {
				t.Errorf("teardown: %v", err)
			}
		})
		return &judgeDemoProcess{Started: started, url: started.URL, consoleHelper: consoleHelper}
	}
	runOnce := func(t *testing.T, repo string, args ...string) (string, string, int) {
		t.Helper()
		var out, errb bytes.Buffer
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Env = repo, env
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("%v did not exit: stdout=%q stderr=%q", args, out.String(), errb.String())
		}
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}
	read := func(t *testing.T, repo, rel string) ([]byte, bool) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(repo, ".faultline", filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			return nil, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return b, true
	}
	readyPattern := func(intake string) *regexp.Regexp {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(guidedProcessPhasePreflight+"FaultLine guided investigate: INTAKE ("+intake+")\n"+guidedProcessPhaseReview) +
			`FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)` + regexp.QuoteMeta(guidedProcessReadyText) + `$`)
	}
	cancelled := "FaultLine error: Guided investigate cancelled before freeze.\nNext: pnpm fl help\n"

	t.Run("first SIGINT cancels the wait and resume freezes by HTTP", func(t *testing.T) {
		repo := newRepo(t, "interrupt")
		p := start(t, repo, 5, readyPattern("ci-log"), "investigate", "--ci-log", logPath("interrupt"), "--repo", repo,
			"--id", "inc-1", "--port", "0", "--command", "echo ignored", "--json", "--two-step", "--tty", "--browser")
		// Readiness: draft and proposal exist, nothing is approved.
		draftBefore, ok := read(t, repo, "incidents/drafts/inc-1.json")
		proposalBefore, ok2 := read(t, repo, "witnesses/proposals/inc-1.json")
		if !ok || !ok2 {
			t.Fatalf("draft/proposal missing: %v %v", ok, ok2)
		}
		for _, rel := range []string{"witnesses/approvals/inc-1.json", "witnesses/frozen/inc-1.json"} {
			if _, there := read(t, repo, rel); there {
				t.Fatalf("%s exists before human action", rel)
			}
		}
		p.interrupt(t)
		result := p.wait(t)
		wantErr := guidedProcessPhasePreflight + "FaultLine guided investigate: INTAKE (ci-log)\n" + guidedProcessPhaseReview +
			"FaultLine local witness review: " + p.url + guidedProcessReadyText + cancelled
		if result.ExitCode == nil || *result.ExitCode != 1 || result.Signal != nil || result.Stdout != "" || result.Stderr != wantErr {
			t.Fatalf("after SIGINT: %+v", result)
		}
		// State is retained unchanged and still unfrozen.
		if got, _ := read(t, repo, "incidents/drafts/inc-1.json"); !bytes.Equal(got, draftBefore) {
			t.Fatal("draft changed by cancellation")
		}
		if got, _ := read(t, repo, "witnesses/proposals/inc-1.json"); !bytes.Equal(got, proposalBefore) {
			t.Fatal("proposal changed by cancellation")
		}
		if _, there := read(t, repo, "witnesses/frozen/inc-1.json"); there {
			t.Fatal("cancellation froze the witness")
		}
		if _, there := read(t, repo, "incidents/bindings/inc-1.json"); there {
			t.Fatal("cancellation wrote a session binding")
		}

		// Resume over the same immutable records; only the HTTP form freezes.
		r := start(t, repo, 5, readyPattern("resume inc-1"), "investigate", "--resume", "inc-1", "--repo", repo, "--port", "0")
		page := judgeDemoRequest(http.MethodGet, r.url+"/", "")
		if page.err != nil || page.status != 200 || !strings.Contains(page.body, "/approve-freeze") {
			t.Fatalf("page: %+v", page)
		}
		token := regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`).FindStringSubmatch(page.body)
		digest := regexp.MustCompile(`name="reviewDigest" value="(sha256:[0-9a-f]{64})"`).FindStringSubmatch(page.body)
		if token == nil || digest == nil {
			t.Fatalf("form fields missing: %s", page.body)
		}
		form := url.Values{"token": {token[1]}, "reviewDigest": {digest[1]}, "approvedBy": {"Process Reviewer"}, "acknowledged": {"exact-witness-reviewed"}}
		resp, err := http.Post(r.url+"/approve-freeze", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("approve-freeze: %d", resp.StatusCode)
		}
		// The flow continues by itself and stops at the runtime refusal.
		final := r.wait(t)
		wantResume := guidedProcessPhasePreflight + "FaultLine guided investigate: INTAKE (resume inc-1)\n" + guidedProcessPhaseReview +
			"FaultLine local witness review: " + r.url + guidedProcessReadyText + guidedProcessPhaseRuntime + guidedProcessNoRuntime
		if final.ExitCode == nil || *final.ExitCode != 1 || final.Signal != nil || final.Stdout != "" || final.Stderr != wantResume {
			t.Fatalf("resume result: %+v", final)
		}
		if got, _ := read(t, repo, "incidents/drafts/inc-1.json"); !bytes.Equal(got, draftBefore) {
			t.Fatal("draft changed by resume")
		}
		if got, _ := read(t, repo, "witnesses/proposals/inc-1.json"); !bytes.Equal(got, proposalBefore) {
			t.Fatal("proposal changed by resume")
		}
		approval, a := read(t, repo, "witnesses/approvals/inc-1.json")
		_, f := read(t, repo, "witnesses/frozen/inc-1.json")
		if !a || !f || !bytes.Contains(approval, []byte("Process Reviewer")) {
			t.Fatalf("freeze records missing: approval=%v frozen=%v", a, f)
		}

		// A third run finds the freeze before any server: no readiness, no URL.
		stdout, stderr, code := runOnce(t, repo, "investigate", "--resume", "inc-1", "--repo", repo, "--port", "not-a-port")
		wantThird := guidedProcessPhasePreflight + "FaultLine guided investigate: INTAKE (resume inc-1)\n" + guidedProcessPhaseReview +
			guidedProcessPhaseRuntime + guidedProcessNoRuntime
		if code != 1 || stdout != "" || stderr != wantThird {
			t.Fatalf("already frozen: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("default SIGTERM and no stdin approval", func(t *testing.T) {
		repo := newRepo(t, "term")
		p := start(t, repo, 5, readyPattern("ci-log"), "investigate", "--ci-log", logPath("term"), "--repo", repo, "--id", "inc-t", "--port", "0")
		p.terminate(t)
		result := p.wait(t)
		if runtime.GOOS == "windows" {
			// TerminateProcess: an abrupt nonzero exit.
			if result.Signal != nil || result.ExitCode == nil || *result.ExitCode == 0 {
				t.Fatalf("Windows termination: %+v", result)
			}
		} else if result.Signal == nil || *result.Signal != "SIGTERM" || result.ExitCode != nil {
			t.Fatalf("SIGTERM must keep its default death: %+v", result)
		}
		if result.Stdout != "" || strings.Contains(result.Stderr, "cancelled") || strings.Contains(result.Stderr, "FaultLine error") {
			t.Fatalf("termination output: %+v", result)
		}
		if _, there := read(t, repo, "witnesses/approvals/inc-t.json"); there {
			t.Fatal("approval written without a human action")
		}
	})

	t.Run("second SIGINT during server drain takes the default action", func(t *testing.T) {
		repo := newRepo(t, "second")
		p := start(t, repo, 5, readyPattern("ci-log"), "investigate", "--ci-log", logPath("second"), "--repo", repo, "--id", "inc-2", "--port", "0")
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		// A tracked raw client opens a POST whose body never completes. The
		// server's real "100 Continue" proves the connection is in an active
		// body wait (not idle), so Close cannot finish draining it.
		conn, err := p.Dial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		host := strings.TrimPrefix(p.url, "http://")
		request := "POST /approve HTTP/1.1\r\nHost: " + host + "\r\nContent-Type: application/x-www-form-urlencoded\r\n" +
			"Content-Length: 1000\r\nExpect: 100-continue\r\n\r\n"
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := conn.Write([]byte(request)); err != nil {
			t.Fatal(err)
		}
		want := "HTTP/1.1 100 Continue\r\n\r\n"
		got := make([]byte, len(want))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != want {
			t.Fatalf("expected 100 Continue, got %q (%v)", got, err)
		}

		// First SIGINT: the wait is cancelled and Close closes the listener,
		// then blocks on the body connection.
		p.interrupt(t)
		refused := false
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			c, err := net.DialTimeout("tcp4", host, time.Second)
			if err != nil {
				refused = true
				break
			}
			c.Close()
			if p.Exited() {
				break
			}
		}
		if !refused {
			t.Fatalf("listener never refused connections; exited=%v", p.Exited())
		}
		if p.Exited() {
			t.Fatal("process exited during drain after the first SIGINT")
		}

		// Second SIGINT: ordinary default disposition, not a cancel-error exit.
		p.interrupt(t)
		result := p.wait(t)
		if runtime.GOOS == "windows" {
			if result.Signal != nil || result.ExitCode == nil || uint32(*result.ExitCode) != 0xC000013A {
				t.Fatalf("Windows default Ctrl+C status expected: %+v", result)
			}
		} else if result.Signal == nil || *result.Signal != "SIGINT" || result.ExitCode != nil {
			t.Fatalf("second SIGINT must kill by SIGINT: %+v", result)
		}
		wantErr := guidedProcessPhasePreflight + "FaultLine guided investigate: INTAKE (ci-log)\n" + guidedProcessPhaseReview +
			"FaultLine local witness review: " + p.url + guidedProcessReadyText
		if result.Stdout != "" || result.Stderr != wantErr {
			t.Fatalf("streams after second SIGINT: %+v", result)
		}
		for _, rel := range []string{"witnesses/approvals/inc-2.json", "witnesses/frozen/inc-2.json", "incidents/bindings/inc-2.json"} {
			if _, there := read(t, repo, rel); there {
				t.Fatalf("%s written", rel)
			}
		}
		if err := p.CloseClients(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("blocked preflight and refusals keep exact streams", func(t *testing.T) {
		repo := newRepo(t, "refuse")
		// No node on PATH: the real doctor blocks and the result is JSON, exit 1.
		var blockedEnv []string
		for _, kv := range builtBinaryEnv() { // already has an empty PATH
			blockedEnv = append(blockedEnv, kv)
		}
		cmd := exec.Command(binary, "investigate", "--ci-log", logPath("refuse"), "--repo", repo)
		cmd.Dir, cmd.Env = repo, blockedEnv
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		if cmd.ProcessState.ExitCode() != 1 || errb.String() != guidedProcessPhasePreflight ||
			!strings.HasPrefix(out.String(), "{\n  \"status\": \"PREFLIGHT_BLOCKED\",\n  \"doctor\": {\n") ||
			!strings.HasSuffix(out.String(), "\"note\": \"Local CLI preflight failed; fix Node before continuing the guided investigate flow.\"\n}\n") {
			t.Fatalf("blocked: exit=%d stdout=%q stderr=%q", cmd.ProcessState.ExitCode(), out.String(), errb.String())
		}
		for _, dir := range []string{"incidents", "witnesses"} {
			if _, err := os.Lstat(filepath.Join(repo, ".faultline", dir)); err == nil {
				t.Fatalf("blocked preflight created .faultline/%s", dir)
			}
		}
		stdout, stderr, code := runOnce(t, repo, "investigate", "git", "--resume", "x")
		if code != 1 || stdout != "" || stderr != "FaultLine error: Guided investigate (--ci-log / --resume) cannot be combined with `fl investigate git` or `fl investigate turns`.\nNext: pnpm fl help\n" {
			t.Fatalf("combined: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}
