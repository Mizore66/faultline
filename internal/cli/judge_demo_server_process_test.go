package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
)

type judgeDemoProcess struct {
	*servefixture.Started
	url           string
	startup       string
	output        string
	consoleHelper string
	root          string
}

func (p *judgeDemoProcess) wait(t *testing.T) servefixture.ProcessResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := p.Started.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (p *judgeDemoProcess) interrupt(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Started.Interrupt(ctx, p.consoleHelper); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
}

func (p *judgeDemoProcess) terminate(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Started.Terminate(ctx); err != nil {
		t.Fatalf("default termination: %v", err)
	}
}

// startJudgeDemoProcess launches the actual binary outside the repository and
// waits for the complete publication summary followed by the two readiness
// lines. Its environment contains neither Node nor Go.
func startJudgeDemoProcess(t *testing.T, binary, work, output, displayedOutput, consoleHelper string, initialNative bool, args ...string) *judgeDemoProcess {
	t.Helper()
	return startJudgeDemoProcessWithEnv(t, binary, work, output, displayedOutput, consoleHelper, builtBinaryEnv(), initialNative, args...)
}

func startJudgeDemoProcessWithEnv(t *testing.T, binary, work, output, displayedOutput, consoleHelper string, env []string, initialNative bool, args ...string) *judgeDemoProcess {
	t.Helper()
	startupLines := 8
	if initialNative {
		startupLines = 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var startup string
	started, err := servefixture.StartWithOptions(ctx, binary, nil, args, env, work, servefixture.StartOptions{
		StartupLines: startupLines, StartupDescription: "judge-demo startup lines",
		ValidateStartup: func(out []byte) (string, error) {
			text := string(out)
			_, rest, ok := strings.Cut(text, "🚀 Launching FaultLine Judge Demo at ")
			url, after, found := strings.Cut(rest, "...\n")
			if !ok || !found || !strings.HasPrefix(after, "Press Ctrl+C to stop.\nBrowser auto-open skipped in CI. Open ") ||
				!regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]*$`).MatchString(url) {
				return "", fmt.Errorf("invalid judge-demo startup output %q", text)
			}
			startup = text
			return url, nil
		},
	})
	if err != nil {
		t.Fatalf("start judge-demo: %v", err)
	}
	p := &judgeDemoProcess{Started: started, url: started.URL, startup: startup, output: output, consoleHelper: consoleHelper}
	t.Cleanup(func() {
		if err := started.Dispose(); err != nil {
			t.Errorf("judge-demo teardown: %v", err)
		}
	})
	requireJudgeDemoStartup(t, p, displayedOutput, initialNative)
	return p
}

func judgeDemoEnvironmentWithTemp(directory string) []string {
	env := builtBinaryEnv()
	kept := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "TMPDIR") || strings.EqualFold(key, "TMP") || strings.EqualFold(key, "TEMP") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, "TMPDIR="+directory, "TMP="+directory, "TEMP="+directory)
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
	want += "Browser auto-open skipped in CI. Open " + p.url + " manually if needed.\n"
	p.root = *verification.RootDigest
	if p.startup != want {
		t.Fatalf("startup stdout=%q want=%q", p.startup, want)
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

// requireJudgeDemoCleanStop requires a graceful exit 0 whose stdout is the
// startup text plus the stop summary naming the initial verified root.
func requireJudgeDemoCleanStop(t *testing.T, p *judgeDemoProcess, label string) {
	t.Helper()
	result := p.wait(t)
	want := p.startup + "FaultLine judge-demo stopped. Bundle root: " + p.root + "\n"
	if result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != nil || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("%s result: code=%v signal=%v stdout=%q want=%q stderr=%q", label, result.ExitCode, result.Signal, result.Stdout, want, result.Stderr)
	}
}

func requireJudgeDemoCleanInterrupt(t *testing.T, p *judgeDemoProcess) {
	t.Helper()
	p.interrupt(t)
	requireJudgeDemoCleanStop(t, p, "SIGINT")
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

// awaitJudgeDemoNativeConfig observes the real native executor's per-child
// directory. The config is written before the private child starts and removed
// by the executor's cleanup, so its existence is an execution-phase barrier,
// not an inference from HTTP single-flight or publication state.
func awaitJudgeDemoNativeConfig(t *testing.T, p *judgeDemoProcess, temp string, response <-chan judgeDemoResponse) string {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entries, err := os.ReadDir(temp)
		if err != nil {
			t.Fatalf("read controlled native TMPDIR: %v", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "faultline-native-demo-") {
				continue
			}
			config := filepath.Join(temp, entry.Name(), "native-config.json")
			info, err := os.Lstat(config)
			if err == nil && info.Mode().IsRegular() {
				return config
			}
		}
		select {
		case result := <-response:
			t.Fatalf("native POST completed before the execution-phase barrier: %+v", result)
		case <-ticker.C:
			if p.Exited() {
				t.Fatal("judge-demo exited before the execution-phase barrier")
			}
		case <-deadline.C:
			t.Fatal("native-config.json did not appear under controlled TMPDIR")
		}
	}
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
		if p.root == first.root || p.root == second.root {
			t.Fatalf("initial root %s equals a rerun root", p.root)
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

	activeNative := func(t *testing.T, label string, stop func(*judgeDemoProcess, *testing.T)) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "active")
		nativeTemp := filepath.Join(work, "native-tmp")
		if err := os.Mkdir(nativeTemp, 0o700); err != nil {
			t.Fatal(err)
		}
		p := startJudgeDemoProcessWithEnv(t, binary, work, output, filepath.Join(".faultline", "bundles", "active"), consoleHelper,
			judgeDemoEnvironmentWithTemp(nativeTemp), false,
			"judge-demo", "--approve-native", proposalDigest, "--port", "0", "--output", output)
		_, nonce := judgeDemoPageAndNonce(t, p)
		before := tree(t, work)
		response := make(chan judgeDemoResponse, 1)
		go func() { response <- judgeDemoRequest(http.MethodPost, p.url+"/api/rerun", nonce) }()
		config := awaitJudgeDemoNativeConfig(t, p, nativeTemp, response)
		// The real per-child config exists: native execution, not bundle or
		// sidecar publication, is the phase interrupted here.
		stop(p, t)
		active := <-response
		if active.err == nil && active.status == http.StatusOK {
			t.Fatalf("cancelled native POST returned success: %+v", active)
		}
		if runtime.GOOS == "windows" && label == "active SIGTERM" {
			// TerminateProcess ends the process outright: no graceful stop
			// summary, and no cleanup or tree guarantee to assert.
			result := p.wait(t)
			if result.Signal != nil || result.ExitCode == nil || *result.ExitCode == 0 || result.Stdout != p.startup || result.Stderr != "" {
				t.Fatalf("Windows active termination: code=%v signal=%v stdout=%q stderr=%q", result.ExitCode, result.Signal, result.Stdout, result.Stderr)
			}
			return
		}
		requireJudgeDemoCleanStop(t, p, label)
		if _, err := os.Lstat(config); !os.IsNotExist(err) {
			t.Fatalf("native child config survived cancellation cleanup: %v", err)
		}
		sameTree(t, "cancelled native publication", tree(t, work), before)
	}
	t.Run("active native rerun interrupt", func(t *testing.T) {
		activeNative(t, "active SIGINT", (*judgeDemoProcess).interrupt)
	})
	t.Run("active native rerun terminate", func(t *testing.T) {
		activeNative(t, "active SIGTERM", (*judgeDemoProcess).terminate)
	})

	t.Run("default terminate", func(t *testing.T) {
		work := newWork(t)
		output := filepath.Join(work, ".faultline", "bundles", "terminate")
		p := start(t, work, output, filepath.Join(".faultline", "bundles", "terminate"), false, "judge-demo", "--port", "0", "--output", output)
		p.terminate(t)
		if runtime.GOOS != "windows" {
			// POSIX SIGTERM is handled gracefully like SIGINT.
			requireJudgeDemoCleanStop(t, p, "SIGTERM")
			return
		}
		// Windows terminates the process outright (TerminateProcess): no
		// graceful stop and no stop summary.
		result := p.wait(t)
		if result.Signal != nil || result.ExitCode == nil || *result.ExitCode == 0 {
			t.Fatalf("Windows termination result: code=%v signal=%v", result.ExitCode, result.Signal)
		}
		if result.Stdout != p.startup || result.Stderr != "" {
			t.Fatalf("termination output: stdout=%q stderr=%q", result.Stdout, result.Stderr)
		}
	})
}
