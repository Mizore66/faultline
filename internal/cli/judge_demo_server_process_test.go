package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// judgeDemoStream is an append-only process stream whose readers are woken by
// writes rather than by timing sleeps.
type judgeDemoStream struct {
	mu      sync.Mutex
	buf     strings.Builder
	changed chan struct{}
}

func newJudgeDemoStream() *judgeDemoStream { return &judgeDemoStream{changed: make(chan struct{})} }

func (s *judgeDemoStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	close(s.changed)
	s.changed = make(chan struct{})
	return n, err
}

func (s *judgeDemoStream) snapshot() (string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String(), s.changed
}

func (s *judgeDemoStream) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

type judgeDemoProcess struct {
	cmd           *exec.Cmd
	stdout        *judgeDemoStream
	stderr        *judgeDemoStream
	done          chan struct{}
	waitErr       error
	url           string
	startup       string
	output        string
	consoleHelper string
}

func (p *judgeDemoProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *judgeDemoProcess) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.done:
		return p.waitErr
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		t.Fatal("judge-demo did not exit after its signal")
		return nil
	}
}

func (p *judgeDemoProcess) interrupt(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatalf("SIGINT: %v", err)
		}
		return
	}
	if p.consoleHelper == "" {
		t.Fatal("Windows console helper was not built")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.consoleHelper, strconv.Itoa(p.cmd.Process.Pid)).CombinedOutput()
	if err != nil {
		t.Fatalf("CTRL_C_EVENT: %v\n%s", err, out)
	}
}

func (p *judgeDemoProcess) terminate(t *testing.T) {
	t.Helper()
	var err error
	if runtime.GOOS == "windows" {
		err = p.cmd.Process.Kill()
	} else {
		err = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	if err != nil {
		t.Fatalf("default termination: %v", err)
	}
}

// startJudgeDemoProcess launches the actual binary outside the repository and
// waits for the complete publication summary followed by the two readiness
// lines. Its environment contains neither Node nor Go.
func startJudgeDemoProcess(t *testing.T, binary, work, output, displayedOutput, consoleHelper string, initialNative bool, args ...string) *judgeDemoProcess {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = work
	cmd.Env = builtBinaryEnv()
	p := &judgeDemoProcess{
		cmd: cmd, stdout: newJudgeDemoStream(), stderr: newJudgeDemoStream(), done: make(chan struct{}),
		output: output, consoleHelper: consoleHelper,
	}
	cmd.Stdout, cmd.Stderr = p.stdout, p.stderr
	if runtime.GOOS == "windows" {
		// Match servefixture's isolated console without introducing a second
		// platform-specific test file. This field exists only on Windows.
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		field := reflect.ValueOf(cmd.SysProcAttr).Elem().FieldByName("CreationFlags")
		if !field.IsValid() || !field.CanSet() {
			t.Fatal("Windows SysProcAttr.CreationFlags is unavailable")
		}
		field.SetUint(0x10) // CREATE_NEW_CONSOLE
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start judge-demo: %v", err)
	}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		if !p.exited() {
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Errorf("judge-demo was still running at teardown")
		}
	})

	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	const stopLine = "Press Ctrl+C to stop.\n"
	for {
		out, changed := p.stdout.snapshot()
		if strings.HasSuffix(out, stopLine) {
			p.startup = out
			_, rest, ok := strings.Cut(out, "🚀 Launching FaultLine Judge Demo at ")
			url, after, found := strings.Cut(rest, "...\n")
			if !ok || !found || after != stopLine || !regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]*$`).MatchString(url) {
				t.Fatalf("invalid startup output %q", out)
			}
			p.url = url
			requireJudgeDemoStartup(t, p, displayedOutput, initialNative)
			return p
		}
		select {
		case <-changed:
		case <-p.done:
			t.Fatalf("judge-demo exited before readiness: %v stdout=%q stderr=%q", p.waitErr, p.stdout.String(), p.stderr.String())
		case <-deadline.C:
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Fatalf("judge-demo startup deadline; stdout=%q stderr=%q", p.stdout.String(), p.stderr.String())
		}
	}
}

func requireJudgeDemoStartup(t *testing.T, p *judgeDemoProcess, displayedOutput string, initialNative bool) {
	t.Helper()
	verification := demo.Verify(p.output, "", false)
	if !verification.Valid || verification.RootDigest == nil {
		t.Fatalf("startup bundle invalid: %+v", verification)
	}
	analysis := readValue(t, filepath.Join(p.output, "analysis.json"))
	mode, modeText := "REPLAY", "cached replay"
	if initialNative {
		mode, modeText = "RERUN", "executed"
	}
	requireString(t, "startup analysis.mode", analysis.Get("mode"), mode)
	boundary := "not certified from cached replay"
	for _, transition := range analysis.Get("transitions").Items() {
		if transition.Get("kind").Str() == "PASS_TO_FAIL" && transition.Get("stable").Bool() {
			boundary = transition.Get("beforeStateId").Str() + " → " + transition.Get("afterStateId").Str()
			break
		}
	}
	want := "FaultLine " + modeText + " investigation prepared.\n" +
		"Stable sample boundary: " + boundary + "\n" +
		"Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n" +
		"Bundle root: " + *verification.RootDigest + "\n" +
		"Proof bundle: " + displayedOutput + "\n"
	if initialNative {
		want += "Native execution evidence: " + sidecarOf(p.output) + "\n"
	}
	want += "🚀 Launching FaultLine Judge Demo at " + p.url + "...\nPress Ctrl+C to stop.\n"
	if p.startup != want {
		t.Fatalf("startup stdout=%q want=%q", p.startup, want)
	}
	if p.stderr.String() != "" {
		t.Fatalf("startup stderr=%q", p.stderr.String())
	}
}

type judgeDemoResponse struct {
	status int
	body   string
	err    error
}

func judgeDemoRequest(method, url, nonce string) judgeDemoResponse {
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		return judgeDemoResponse{err: err}
	}
	if nonce != "" {
		request.Header.Set("X-FaultLine-Native-RERUN", nonce)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return judgeDemoResponse{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return judgeDemoResponse{status: response.StatusCode, body: string(body), err: err}
}

func judgeDemoPageAndNonce(t *testing.T, p *judgeDemoProcess) (string, string) {
	t.Helper()
	response := judgeDemoRequest(http.MethodGet, p.url+"/", "")
	if response.err != nil || response.status != http.StatusOK {
		t.Fatalf("GET page: %+v", response)
	}
	match := regexp.MustCompile(`<script nonce="([^"]+)">`).FindStringSubmatch(response.body)
	if len(match) != 2 || len(match[1]) != 43 {
		t.Fatalf("script nonce missing from HTML: %q", match)
	}
	return response.body, match[1]
}

type judgeDemoRunObservation struct {
	root, nonce, approvedAt string
	runIDs                  map[string]bool
}

func requireJudgeDemoNativePost(t *testing.T, p *judgeDemoProcess, binary, goVersion, proposalDigest, nonce, notBefore string) judgeDemoRunObservation {
	t.Helper()
	response := judgeDemoRequest(http.MethodPost, p.url+"/api/rerun", nonce)
	if response.err != nil || response.status != http.StatusOK {
		t.Fatalf("native POST: %+v", response)
	}
	analysis := readValue(t, filepath.Join(p.output, "analysis.json"))
	wantBody := `{"ok":true,"mode":"RERUN","generatedAt":` + jsjson.Stringify(analysis.Get("generatedAt")) + `}`
	if response.body != wantBody {
		t.Fatalf("native POST body=%q want frozen envelope %q", response.body, wantBody)
	}
	verification := demo.Verify(p.output, "", false)
	if !verification.Valid || verification.RootDigest == nil {
		t.Fatalf("native bundle invalid: %+v", verification)
	}
	sidecar := sidecarOf(p.output)
	auditNativeSidecar(t, nativeAudit{
		sidecar: sidecar, directory: p.output, executable: binary, goVersion: goVersion,
		proposalDigest: proposalDigest, notBefore: notBefore, notAfter: clock.ISO(time.Now()),
	})
	if err := sidecarLinkage(sidecar, p.output); err != nil {
		t.Fatalf("sidecar linkage: %v", err)
	}
	evidence := readValue(t, sidecar)
	provenance := evidence.Get("provenance")
	observation := judgeDemoRunObservation{
		root: *verification.RootDigest, nonce: provenance.Get("nonce").Str(),
		approvedAt: provenance.Get("approval").Get("approvedAt").Str(), runIDs: map[string]bool{},
	}
	for _, record := range analysis.Get("runCatalog").Items() {
		observation.runIDs[record.Get("id").Str()] = true
	}
	if len(observation.runIDs) != 39 {
		t.Fatalf("native analysis has %d unique run IDs", len(observation.runIDs))
	}
	return observation
}

func requireJudgeDemoCleanInterrupt(t *testing.T, p *judgeDemoProcess) {
	t.Helper()
	p.interrupt(t)
	if err := p.wait(t); err != nil {
		t.Fatalf("SIGINT exit: %v stdout=%q stderr=%q", err, p.stdout.String(), p.stderr.String())
	}
	if p.stdout.String() != p.startup || p.stderr.String() != "" {
		t.Fatalf("output after SIGINT: stdout=%q stderr=%q", p.stdout.String(), p.stderr.String())
	}
}

func buildJudgeDemoConsoleHelper(t *testing.T, moduleRoot, directory string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return ""
	}
	helper := filepath.Join(directory, "console-helper.exe")
	cmd := exec.Command("go", "build", "-o", helper, "./difftest/servefixture/console-helper")
	cmd.Dir = moduleRoot
	cmd.Env = deterministicBuildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build console helper: %v\n%s", err, out)
	}
	return helper
}

func TestJudgeDemoInteractiveProcess(t *testing.T) {
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
	goVersion := goVersionOfBinary(t, binary)
	proposalDigest := demofixture.NativeWitnessProposal().Get("digest").Str()

	newWork := func(t *testing.T) string {
		t.Helper()
		work := filepath.Join(fresh, strings.NewReplacer("/", "-", "\\", "-").Replace(t.Name()))
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(work)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	start := func(t *testing.T, work, output, displayedOutput string, initialNative bool, args ...string) *judgeDemoProcess {
		t.Helper()
		return startJudgeDemoProcess(t, binary, work, output, displayedOutput, consoleHelper, initialNative, args...)
	}

	t.Run("read-only replay and idle interrupt", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "custom", "replay")
		p := start(t, work, output, filepath.Join(".faultline", "bundles", "custom", "replay"), false, "judge-demo", "--port", "0", "--output", output)
		page, nonce := judgeDemoPageAndNonce(t, p)
		if strings.Contains(page, "/api/rerun") {
			t.Fatal("read-only page exposes native RERUN")
		}
		before := tree(t, work)
		response := judgeDemoRequest(http.MethodPost, p.url+"/api/rerun", nonce)
		const forbidden = `{"ok":false,"error":"Native RERUN requires --approve-native <proposal-digest> at server startup."}`
		if response.err != nil || response.status != http.StatusForbidden || response.body != forbidden {
			t.Fatalf("read-only POST: %+v want exact 403 %q", response, forbidden)
		}
		sameTree(t, "read-only POST", tree(t, work), before)
		requireJudgeDemoCleanInterrupt(t, p)
	})

	t.Run("approved replay performs two fresh native reruns", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "custom", "native")
		notBefore := clock.ISO(time.Now())
		p := start(t, work, output, filepath.Join(".faultline", "bundles", "custom", "native"), false,
			"judge-demo", "--replay", "--approve-native", proposalDigest, "--port", "0", "--output", output)
		page, nonce := judgeDemoPageAndNonce(t, p)
		if !strings.Contains(page, "/api/rerun") {
			t.Fatal("approved page does not expose native RERUN")
		}
		first := requireJudgeDemoNativePost(t, p, binary, goVersion, proposalDigest, nonce, notBefore)
		second := requireJudgeDemoNativePost(t, p, binary, goVersion, proposalDigest, nonce, notBefore)
		if first.approvedAt == "" || first.approvedAt != second.approvedAt {
			t.Fatalf("approval was not captured once and reused: %q then %q", first.approvedAt, second.approvedAt)
		}
		if first.nonce == second.nonce || first.root == second.root {
			t.Fatalf("second evidence is not fresh: nonce %q/%q root %q/%q", first.nonce, second.nonce, first.root, second.root)
		}
		for id := range second.runIDs {
			if first.runIDs[id] {
				t.Fatalf("second native run reused run ID %s", id)
			}
		}
		snapshot := judgeDemoRequest(http.MethodGet, p.url+"/api/analysis", "")
		analysis := readValue(t, filepath.Join(output, "analysis.json"))
		if snapshot.err != nil || snapshot.status != http.StatusOK || snapshot.body != jsjson.Stringify(analysis) {
			t.Fatalf("updated analysis snapshot: %+v", snapshot)
		}
		requireJudgeDemoCleanInterrupt(t, p)
	})

	t.Run("initial native rerun with default output", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "judge-demo")
		display := filepath.Join(".faultline", "bundles", "judge-demo")
		notBefore := clock.ISO(time.Now())
		p := start(t, work, output, display, true,
			"judge-demo", "--replay", "--rerun-all", "--approve-native", proposalDigest, "--port", "0")
		auditNativeSidecar(t, nativeAudit{
			sidecar: sidecarOf(output), directory: output, executable: binary, goVersion: goVersion,
			proposalDigest: proposalDigest, notBefore: notBefore, notAfter: clock.ISO(time.Now()),
		})
		requireJudgeDemoCleanInterrupt(t, p)
	})

	t.Run("active native rerun interrupt", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "active")
		p := start(t, work, output, filepath.Join(".faultline", "bundles", "active"), false,
			"judge-demo", "--approve-native", proposalDigest, "--port", "0", "--output", output)
		_, nonce := judgeDemoPageAndNonce(t, p)
		var before map[string]string
		interrupted := false
		for attempt := 0; attempt < 5 && !interrupted; attempt++ {
			before = tree(t, work)
			responses := make(chan judgeDemoResponse, 2)
			go func() { responses <- judgeDemoRequest(http.MethodPost, p.url+"/api/rerun", nonce) }()
			go func() { responses <- judgeDemoRequest(http.MethodPost, p.url+"/api/rerun", nonce) }()
			first := <-responses
			if first.err != nil || first.status != http.StatusConflict {
				second := <-responses
				if first.err != nil || second.err != nil || first.status != http.StatusOK && second.status != http.StatusOK {
					t.Fatalf("native requests failed before the active barrier: first=%+v second=%+v", first, second)
				}
				continue
			}
			if first.body != `{"ok":false,"error":"native RERUN already running"}` {
				t.Fatalf("single-flight barrier: %+v", first)
			}
			// The conflict response arrived while the other request was active.
			// Signal before waiting for that request, rather than accidentally
			// observing the active request's success and interrupting an idle server.
			p.interrupt(t)
			active := <-responses
			if active.err == nil && active.status == http.StatusOK {
				t.Fatalf("cancelled native POST returned success: %+v", active)
			}
			interrupted = true
		}
		if !interrupted {
			t.Fatal("could not observe an active native RERUN")
		}
		if err := p.wait(t); err != nil {
			t.Fatalf("active SIGINT exit: %v", err)
		}
		if p.stdout.String() != p.startup || p.stderr.String() != "" {
			t.Fatalf("active SIGINT output: stdout=%q stderr=%q", p.stdout.String(), p.stderr.String())
		}
		sameTree(t, "cancelled native publication", tree(t, work), before)
	})

	t.Run("default terminate", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "terminate")
		p := start(t, work, output, filepath.Join(".faultline", "bundles", "terminate"), false, "judge-demo", "--port", "0", "--output", output)
		p.terminate(t)
		err := p.wait(t)
		if runtime.GOOS == "windows" {
			if err == nil || p.cmd.ProcessState.ExitCode() == 0 {
				t.Fatalf("Windows termination status: %v", err)
			}
		} else {
			status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
				t.Fatalf("termination did not take the default SIGTERM action: %v", err)
			}
		}
		if p.stdout.String() != p.startup || p.stderr.String() != "" {
			t.Fatalf("termination output: stdout=%q stderr=%q", p.stdout.String(), p.stderr.String())
		}
	})
}
