package cli

import (
	"bytes"
	"context"
	"io"
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
	"github.com/Mizore66/faultline/internal/jsjson"
)

const reviewProcessProposal = `{"proposalId":"p1","incidentPacket":{"symptom":"refund currency","ciLog":"Expected USD; received EUR.",
"repositoryLanguage":"JavaScript","repositorySummary":"A Node service that completes refunds."},
"witness":{"behavior":"b","command":"node witness.mjs","overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`

// TestWitnessReviewProcess drives the actual fl binary outside the repository
// with an empty PATH (no Node, no Go, no browser), stdin not a terminal.
func TestWitnessReviewProcess(t *testing.T) {
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
		if err := os.WriteFile(filepath.Join(resolved, "proposal.json"), []byte(reviewProcessProposal), 0o600); err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	runFlOnce := func(t *testing.T, work string, args ...string) (string, string, int) {
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
	propose := func(t *testing.T, work string) {
		t.Helper()
		if _, stderr, code := runFlOnce(t, work, "witness", "propose", "--input", "proposal.json"); code != 0 {
			t.Fatalf("propose: %d %q", code, stderr)
		}
	}
	start := func(t *testing.T, work string, lines int, pattern *regexp.Regexp, args ...string) *judgeDemoProcess {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		started, err := servefixture.StartWithOptions(ctx, binary, nil, append([]string{"witness", "review", "p1"}, args...), builtBinaryEnv(), work,
			servefixture.StartOptions{
				StartupLines: lines, StartupDescription: "witness review startup",
				ValidateStartup: func(out []byte) (string, error) {
					m := pattern.FindStringSubmatch(string(out))
					if m == nil {
						return "", os.ErrInvalid
					}
					return m[1], nil
				},
			})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(func() {
			if err := started.Dispose(); err != nil {
				t.Errorf("teardown: %v", err)
			}
		})
		return &judgeDemoProcess{Started: started, url: started.URL, consoleHelper: consoleHelper}
	}
	readRecord := func(t *testing.T, work, rel string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(work, ".faultline", "witnesses", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("json output", func(t *testing.T) {
		work := newWork(t, "json")
		propose(t, work)
		stdout, stderr, code := runFlOnce(t, work, "witness", "review", "p1", "--json", "--port", "bad")
		if code != 0 || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		v, err := jsjson.Parse(stdout)
		if err != nil {
			t.Fatal(err)
		}
		stored := string(readRecord(t, work, "proposals/p1.json"))
		if !strings.HasPrefix(stdout, "{\n  \"status\": \"LOCAL_READ_ONLY_REVIEW\",\n  \"review\": {\n") || !strings.HasSuffix(stdout, "\n}\n") ||
			v.Get("review", "proposal", "proposalId").Str() != "p1" ||
			!strings.Contains(stdout, `"sensitivity": "This export contains the exact command and base64 overlay bytes. Treat it as sensitive incident material and do not publish it."`) ||
			!strings.Contains(stored, "Expected USD; received EUR.") || !strings.Contains(stdout, "Expected USD; received EUR.") {
			t.Fatalf("stdout=%q", stdout)
		}
	})

	atomicPattern := regexp.MustCompile(`^FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\nApprove & freeze is one action\. Press Ctrl\+C to stop\.\n$`)
	t.Run("noninteractive server approve and interrupt", func(t *testing.T) {
		work := newWork(t, "server")
		propose(t, work)
		p := start(t, work, 2, atomicPattern)
		page := judgeDemoRequest("GET", p.url+"/", "")
		if page.err != nil || page.status != 200 || !strings.Contains(page.body, "/approve-freeze") {
			t.Fatalf("page: %+v", page)
		}
		token := regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`).FindStringSubmatch(page.body)
		digest := regexp.MustCompile(`name="reviewDigest" value="(sha256:[0-9a-f]{64})"`).FindStringSubmatch(page.body)
		if token == nil || digest == nil {
			t.Fatalf("form fields missing: %s", page.body)
		}
		form := url.Values{"token": {token[1]}, "reviewDigest": {digest[1]}, "approvedBy": {"Process Reviewer"}, "acknowledged": {"exact-witness-reviewed"}}
		resp, err := http.Post(p.url+"/approve-freeze", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("approve: %d %s", resp.StatusCode, body)
		}
		approval, frozen := readRecord(t, work, "approvals/p1.json"), readRecord(t, work, "frozen/p1.json")
		if !bytes.Contains(approval, []byte("Process Reviewer")) || !bytes.Contains(frozen, []byte("frozenDigest")) {
			t.Fatalf("records: %s / %s", approval, frozen)
		}
		p.interrupt(t)
		result := p.wait(t)
		if result.ExitCode == nil || *result.ExitCode != 0 || result.Signal != nil || result.Stderr != "" || !atomicPattern.MatchString(result.Stdout) {
			t.Fatalf("after SIGINT: %+v", result)
		}
	})

	t.Run("forced tty fallback exits 1 after interrupt", func(t *testing.T) {
		work := newWork(t, "forced")
		propose(t, work)
		pattern := regexp.MustCompile(`^FaultLine local witness review: (http://127\.0\.0\.1:[1-9][0-9]*)\nPress Ctrl\+C to stop\.\n$`)
		p := start(t, work, 2, pattern, "--tty")
		p.interrupt(t)
		result := p.wait(t)
		want := "Refusing non-interactive approval. Open the local review URL: " + p.url + " FaultLine never auto-approves under yes| piping.\n"
		if result.ExitCode == nil || *result.ExitCode != 1 || result.Signal != nil || result.Stderr != want || !pattern.MatchString(result.Stdout) {
			t.Fatalf("after SIGINT: %+v", result)
		}
		if _, err := os.Stat(filepath.Join(work, ".faultline", "witnesses", "approvals", "p1.json")); err == nil {
			t.Fatal("forced fallback wrote an approval")
		}
	})

	t.Run("invalid port and unknown proposal", func(t *testing.T) {
		work := newWork(t, "invalid")
		propose(t, work)
		stdout, stderr, code := runFlOnce(t, work, "witness", "review", "p1", "--port", "bad")
		if code != 1 || stdout != "" || stderr != "FaultLine error: "+reviewDiagnostic(t, "witness-review-invalid-port")+"\nNext: pnpm fl help\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}
