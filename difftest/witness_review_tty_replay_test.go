//go:build faultline_test && (linux || darwin)

package difftest

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWitnessReviewTTYReplay runs the covered fl on a real pseudo-terminal
// (stdin and stdout) and answers the prompt, so the coverage gate sees the TTY
// decline path of internal/cli/witness_review_tty.go execute and the
// committed declined diagnostic is compared with what fl printed.
func TestWitnessReviewTTYReplay(t *testing.T) {
	texts := witnessReviewDiagnostics(t)
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "proposal.json"), []byte(witnessReviewReplayProposal), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runFl(t, work, nil, "witness", "propose", "--input", "proposal.json"); code != 0 {
		t.Fatalf("propose: %d %q", code, stderr)
	}
	run := func(args []string, answer string) (output, stderr string, code int) {
		t.Helper()
		master, slave := openReplayPTY(t)
		defer master.Close()
		cmd := exec.Command(flBinary, args...)
		cmd.Dir, cmd.Env = work, append(os.Environ(), "FAULTLINE_NO_BROWSER=1")
		cmd.Stdin, cmd.Stdout = slave, slave
		var errb bytes.Buffer
		cmd.Stderr = &errb
		var out bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(&out, master) // ends with EIO once the slave closes
		}()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if _, err := master.WriteString(answer); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("fl did not exit; output %q", out.String())
		}
		_ = slave.Close()
		wg.Wait()
		return out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("decline writes nothing", func(t *testing.T) {
		out, stderr, code := run([]string{"witness", "review", "p1"}, "n\r")
		want := "FaultLine error: " + texts["witness-review-tty-declined"] + "\nNext: pnpm fl help\n"
		if code != 1 || stderr != want || !strings.Contains(out, "[y/N] ") {
			t.Fatalf("exit=%d stderr=%q output=%q", code, stderr, out)
		}
		if _, err := os.Stat(filepath.Join(work, ".faultline", "witnesses", "approvals", "p1.json")); err == nil {
			t.Fatal("a declined review wrote an approval")
		}
	})

	for _, c := range []struct{ name, key, answer string }{
		{"Ctrl+D aborts", "witness-review-tty-aborted-ctrl-d", "\x04"},
		{"Ctrl+C aborts", "witness-review-tty-aborted-ctrl-c", "\x03"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, stderr, code := run([]string{"witness", "review", "p1"}, c.answer)
			want := "FaultLine error: " + texts[c.key] + "\nNext: pnpm fl help\n"
			if texts[c.key] == "" || code != 1 || stderr != want || !strings.Contains(out, "[y/N] ") {
				t.Fatalf("exit=%d stderr=%q output=%q, want stderr %q", code, stderr, out, want)
			}
			if _, err := os.Stat(filepath.Join(work, ".faultline", "witnesses", "approvals", "p1.json")); err == nil {
				t.Fatal("an aborted review wrote an approval")
			}
		})
	}

	t.Run("yes approves and freezes", func(t *testing.T) {
		out, stderr, code := run([]string{"witness", "review", "p1", "--tty"}, "yes\r")
		if code != 0 || stderr != "" || !strings.Contains(out, "Frozen p1 · approvedBy ") {
			t.Fatalf("exit=%d stderr=%q output=%q", code, stderr, out)
		}
		for _, rel := range []string{"approvals/p1.json", "frozen/p1.json"} {
			if _, err := os.Stat(filepath.Join(work, ".faultline", "witnesses", filepath.FromSlash(rel))); err != nil {
				t.Fatalf("missing %s: %v", rel, err)
			}
		}
	})
}
