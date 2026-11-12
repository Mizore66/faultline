//go:build linux || darwin

package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/term"
)

const ttyHelperEnv = "FAULTLINE_TEST_WITNESS_REVIEW_TTY_HELPER"

// TestWitnessReviewTTYHelper is inert unless re-executed with the helper
// variable (store directory). It uses the real process stdin/stdout and the
// real internal/term detection.
func TestWitnessReviewTTYHelper(t *testing.T) {
	store := os.Getenv(ttyHelperEnv)
	if store == "" {
		return
	}
	name := "PTY Reviewer"
	_, _, _, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
		StoreDirectory: store, ProposalID: "p1", ApprovedBy: &name,
		Stdin: os.Stdin, Stdout: os.Stdout,
		StdinIsTTY: term.IsTerminal(os.Stdin), StdoutIsTTY: term.IsTerminal(os.Stdout),
	})
	if err != nil {
		os.Stderr.WriteString("ERR " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(0)
}

type helperRun struct {
	output   string
	stderr   string
	exitCode int
}

// runHelper starts the helper with the given stdin/stdout files, optionally
// feeding input through the PTY master, and waits for it to exit.
func runHelper(t *testing.T, store string, stdin, stdout *os.File, master *os.File, input string) helperRun {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWitnessReviewTTYHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), ttyHelperEnv+"="+store)
	cmd.Stdin, cmd.Stdout = stdin, stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var out bytes.Buffer
	var wg sync.WaitGroup
	if master != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(&out, master) // ends with EIO when the slave closes
		}()
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if input != "" {
		time.Sleep(300 * time.Millisecond)
		if _, err := master.WriteString(input); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("helper timed out")
	}
	code := 0
	if ee, ok := waitErr.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if waitErr != nil {
		t.Fatal(waitErr)
	}
	if master != nil {
		// Close the parent's slave so the reader sees EIO, then drain.
		_ = stdin.Close()
		_ = stdout.Close()
		wg.Wait()
	}
	return helperRun{out.String(), stderr.String(), code}
}

func runWitnessReviewTTYProcessTests(t *testing.T) {
	t.Run("RealPTY", witnessReviewTTYRealPTY)
	t.Run("PipedYesNeverApproves", witnessReviewTTYPipedYesNeverApproves)
	t.Run("PipedStdoutRefuses", witnessReviewTTYPipedStdoutRefuses)
}

func witnessReviewTTYRealPTY(t *testing.T) {
	for _, c := range []struct {
		name, input string
		approve     bool
	}{
		{"approve", "yes\r", true},
		{"decline", "n\r", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := ttyProposal(t, "p1")
			master, slave := openTestPTY(t)
			defer master.Close()
			run := runHelper(t, store, slave, slave, master, c.input)
			if !strings.Contains(run.output, `Approve & freeze as "PTY Reviewer"? [y/N] `) {
				t.Fatalf("output %q stderr %q", run.output, run.stderr)
			}
			if c.approve {
				if run.exitCode != 0 || !strings.Contains(run.output, "Frozen p1 · approvedBy PTY Reviewer · frozenDigest sha256:") || recordCount(t, store) == 0 {
					t.Fatalf("exit %d output %q stderr %q", run.exitCode, run.output, run.stderr)
				}
				return
			}
			if run.exitCode != 1 || !strings.Contains(run.stderr, "Approval declined; no records were written.") || recordCount(t, store) != 0 {
				t.Fatalf("exit %d stderr %q", run.exitCode, run.stderr)
			}
		})
	}
}

func witnessReviewTTYPipedYesNeverApproves(t *testing.T) {
	store := ttyProposal(t, "p1")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.WriteString("yes\nyes\n"); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	master, slave := openTestPTY(t)
	defer master.Close()
	defer pr.Close()
	// stdin is a pipe, stdout a real PTY: stdin check refuses.
	run := runHelper(t, store, pr, slave, master, "")
	if run.exitCode != 1 || !strings.Contains(run.stderr, "requires an interactive terminal") ||
		run.output != "" || recordCount(t, store) != 0 {
		t.Fatalf("exit %d out %q stderr %q", run.exitCode, run.output, run.stderr)
	}
}

func witnessReviewTTYPipedStdoutRefuses(t *testing.T) {
	store := ttyProposal(t, "p1")
	master, slave := openTestPTY(t)
	defer master.Close()
	defer slave.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	run := runHelper(t, store, slave, pw, nil, "")
	pw.Close()
	rest, _ := io.ReadAll(pr)
	if run.exitCode != 1 || !strings.Contains(run.stderr, "requires an interactive stdout") ||
		len(rest) != 0 || recordCount(t, store) != 0 {
		t.Fatalf("exit %d out %q stderr %q", run.exitCode, rest, run.stderr)
	}
}
