package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const helperEnv = "GUIDED_STEP_RUNNER_TEST_CHILD"

// TestMain doubles as the observed child: a stand-in for the Node CLI that prints the exact five review
// readiness stderr lines and then blocks (or exits) as selected. It is a test fixture, not an oracle.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "block", "exit":
		os.Stderr.WriteString("FaultLine guided investigate: PREFLIGHT\n" +
			"FaultLine guided investigate: INTAKE (ci-log)\n" +
			"FaultLine guided investigate: WITNESS_REVIEW\n" +
			"FaultLine local witness review: http://127.0.0.1:43210\n" +
			"Approve, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n")
		if os.Getenv(helperEnv) == "exit" {
			os.Stdout.WriteString("out\n")
			os.Exit(7)
		}
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	os.Exit(99)
}

func testConfig(mode string, limit time.Duration) config {
	return config{
		exe: os.Args[0], args: []string{"-test.run=^$"}, cwd: ".", limit: limit,
		env: append(os.Environ(), helperEnv+"="+mode),
	}
}

// alive reports whether the started command has not been reaped. run() returns only after Dispose joined the
// child, so a non-nil ProcessState proves the owned Node stand-in is gone.
func requireReaped(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("the child was never started")
	}
	if cmd.ProcessState == nil {
		t.Fatalf("child %d was left running (not reaped) after run returned", cmd.Process.Pid)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe (test)") }

func TestRunStdinEOFStopsAndReapsChild(t *testing.T) {
	var cmd *exec.Cmd
	started := time.Now()
	err := run(testConfig("block", time.Minute), strings.NewReader(""), io.Discard, func(c *exec.Cmd) { cmd = c })
	if err == nil || !strings.Contains(err.Error(), "control pipe closed") {
		t.Fatalf("err = %v, want a control pipe closed error", err)
	}
	requireReaped(t, cmd)
	if time.Since(started) > 30*time.Second {
		t.Fatalf("EOF did not stop the step promptly: %v", time.Since(started))
	}
}

func TestRunDeadlineReapsChild(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close() // stdin stays open: only the deadline can end the step
	var cmd *exec.Cmd
	err = run(testConfig("block", 1500*time.Millisecond), pr, io.Discard, func(c *exec.Cmd) { cmd = c })
	if err == nil {
		t.Fatal("a step past its deadline must fail")
	}
	requireReaped(t, cmd)
}

func TestRunWriteFailureReapsChild(t *testing.T) {
	var cmd *exec.Cmd
	err := run(testConfig("block", time.Minute), strings.NewReader("stay open\n"), failWriter{}, func(c *exec.Cmd) { cmd = c })
	if err == nil || !strings.Contains(err.Error(), "writing READY") {
		t.Fatalf("err = %v, want a READY write error", err)
	}
	requireReaped(t, cmd)
}

func TestRunStartupFailureReportsStreams(t *testing.T) {
	cfg := testConfig("block", time.Minute)
	cfg.exe = os.Args[0]
	cfg.env = append(os.Environ(), helperEnv+"=nonsense") // exits 99 without readiness
	err := run(cfg, strings.NewReader(""), io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "before printing") {
		t.Fatalf("err = %v, want a startup failure carrying the child's exit", err)
	}
}

func TestRunNormalExitRelaysRawStatus(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	var out strings.Builder
	if err := run(testConfig("exit", time.Minute), pr, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "READY ") || !strings.Contains(out.String(), `"exit":7`) || !strings.Contains(out.String(), "DONE ") {
		t.Fatalf("output %q lacks the READY/DONE lines with the raw exit 7", out.String())
	}
}

func TestMainBadUsageExitsTwo(t *testing.T) {
	if _, err := loadConfig("", "", "", "", time.Second, nil); err == nil {
		t.Fatal("missing arguments accepted")
	} else {
		var u usageError
		if !errors.As(err, &u) {
			t.Fatalf("err %v is not a usage error", err)
		}
	}
}
