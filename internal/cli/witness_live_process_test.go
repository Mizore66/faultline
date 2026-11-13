//go:build faultline_test

package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// TestWitnessLiveProcess drives the actual fl binary (built with the tagged
// loopback override) outside the repository with an empty PATH, against a
// scripted local provider: one persisted proposal, then the write-once
// refusal that repeats the provider call.
func TestWitnessLiveProcess(t *testing.T) {
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
	build := exec.Command("go", "build", "-tags", "faultline_test", "-o", binary, "./cmd/fl")
	build.Dir = moduleRoot
	build.Env = deterministicBuildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	requests := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		requests++
		bodies = append(bodies, string(b))
		io.WriteString(w, outputEnvelope(`{"behavior":"b","command":"node witness.mjs","overlayFiles":["witness.mjs"],"expectedExitCode":7,"timeoutSeconds":30,"safetyNotes":["n"]}`))
	}))
	defer srv.Close()

	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"incident.json": liveIncidentJSON, "overlays/witness.mjs": "console.log(1);\r\n"} {
		path := filepath.Join(work, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, string, int) {
		t.Helper()
		var out, errb bytes.Buffer
		cmd := exec.Command(binary, args...)
		cmd.Dir = work
		cmd.Env = append(builtBinaryEnv(), "FAULTLINE_TEST_OPENAI_URL="+srv.URL+"/v1/responses", "OPENAI_API_KEY=process-key")
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
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
	args := []string{"witness", "propose", "--live", "--incident", "incident.json", "--proposal-id", "p1", "--overlay-root", "overlays"}

	stdout, stderr, code := run(args...)
	if code != 0 || stderr != "" || requests != 1 {
		t.Fatalf("first: code=%d stderr=%q requests=%d", code, stderr, requests)
	}
	v, err := jsjson.Parse(stdout)
	if err != nil || strings.Join(v.Obj().Keys(), ",") != "status,source,store,proposalId,proposalDigest,incidentPacketDigest,privacy" ||
		v.Get("source").Str() != "GPT-6.1" || v.Get("status").Str() != "PROPOSED" || strings.Contains(stdout, "expectedExitCode") || strings.Contains(stdout, "safetyNotes") {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
	if !strings.HasPrefix(bodies[0], `{"model":"gpt-6.1","instructions":`) {
		t.Fatalf("request=%s", bodies[0])
	}
	recordPath := filepath.Join(work, ".faultline", "witnesses", "proposals", "p1.json")
	first, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(recordPath); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if !strings.Contains(string(first), `"bytesBase64":"Y29uc29sZS5sb2coMSk7DQo="`) || strings.Contains(string(first), "expectedExitCode") {
		t.Fatalf("record=%s", first)
	}

	stdout, stderr, code = run(args...)
	after, _ := os.ReadFile(recordPath)
	if code != 1 || stdout != "" || requests != 2 || !bytes.Equal(first, after) ||
		!strings.HasPrefix(stderr, "FaultLine error: Immutable witness lock record already exists: ") || !strings.HasSuffix(stderr, "\nNext: pnpm fl help\n") {
		t.Fatalf("second: code=%d stdout=%q stderr=%q requests=%d", code, stdout, stderr, requests)
	}
}
