//go:build unix

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const bundleWaitHelperEnv = "FAULTLINE_TEST_BUNDLE_WAIT_HELPER"

// TestServeBundleWaitHelper is not a test of its own: when the parent test
// re-executes this test binary with the helper variable it runs
// awaitBundleInterrupt in an isolated process. Mode "interrupt": the start
// callback delivers one REAL SIGINT to that process before returning its close.
// Mode "fail": the start callback fails. Without the variable it returns at
// once.
func TestServeBundleWaitHelper(t *testing.T) {
	mode := os.Getenv(bundleWaitHelperEnv)
	if mode == "" {
		return
	}
	err := awaitBundleInterrupt(func() (func() error, error) {
		if mode == "fail" {
			return nil, fmt.Errorf("start failed")
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			fmt.Println("kill failed:", err)
			os.Exit(3)
		}
		fmt.Println("ready")
		return func() error { fmt.Println("closed"); return nil }, nil
	})
	fmt.Printf("returned %v\n", err)
	os.Exit(0)
}

// runBundleWaitHelper runs the helper in a fresh process in the given mode.
func runBundleWaitHelper(t *testing.T, mode string) (*exec.Cmd, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeBundleWaitHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), bundleWaitHelperEnv+"="+mode)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("helper did not finish: stdout %q stderr %q", stdout.String(), stderr.String())
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return cmd, stdout.String(), stderr.String()
}

// A failed start returns its error at once: no wait for a signal and no close.
func TestServeBundleStartErrorReturnsWithoutWaiting(t *testing.T) {
	cmd, stdout, stderr := runBundleWaitHelper(t, "fail")
	if code := cmd.ProcessState.ExitCode(); code != 0 || stdout != "returned start failed\n" {
		t.Errorf("helper exit=%d stdout=%q stderr=%q, want exit 0 and %q", code, stdout, stderr, "returned start failed\n")
	}
}

// A SIGINT that arrives while the startup output is being written (inside the
// ready callback, before the command waits) must still be the one interrupt
// the command handles: close runs after the callback, the wait returns, and the
// process completes with exit 0. Catches a command that exposes its readiness
// before it is subscribed to SIGINT, where the same real signal takes the
// runtime default action and kills the process. The subprocess only isolates a
// genuine signal from the test runner; nothing aligns timing.
func TestServeBundleInterruptDuringReady(t *testing.T) {
	cmd, stdout, stderr := runBundleWaitHelper(t, "interrupt")
	status, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if status.Signaled() {
		t.Fatalf("helper died of signal %v, want a completed exit 0; stdout %q stderr %q", status.Signal(), stdout, stderr)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("helper exit = %d, want 0; stderr %q", code, stderr)
	}
	if want := "ready\nclosed\nreturned <nil>\n"; stdout != want {
		t.Errorf("helper stdout = %q, want %q", stdout, want)
	}
}
