//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestJudgeDemoServeWaitHelper is not a test of its own: when the parent test
// re-executes this test binary with the helper variable it runs
// awaitJudgeDemoServeInterrupt; without the variable it returns at once.
func TestJudgeDemoServeWaitHelper(t *testing.T) {
	mode := os.Getenv("FAULTLINE_TEST_JUDGE_DEMO_WAIT_HELPER")
	if mode == "" {
		return
	}
	err := awaitJudgeDemoServeInterrupt(func() (func() error, error) {
		if mode == "fail" {
			return nil, fmt.Errorf("start failed")
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			return nil, err
		}
		fmt.Println("ready")
		if mode == "close-error" {
			return func() error { fmt.Println("closed"); return fmt.Errorf("close failed") }, nil
		}
		return func() error { fmt.Println("closed"); return nil }, nil
	}, func() { fmt.Println("stopped") })
	fmt.Printf("returned %v\n", err)
}

func TestJudgeDemoServeStartAndInterrupt(t *testing.T) {
	for _, mode := range []string{"fail", "interrupt", "close-error"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestJudgeDemoServeWaitHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "FAULTLINE_TEST_JUDGE_DEMO_WAIT_HELPER="+mode)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("mode %s: %v\n%s", mode, err, out)
		}
		// The test binary appends its own "PASS" line after the helper output.
		got := strings.TrimSuffix(string(out), "PASS\n")
		if mode == "fail" && got != "returned start failed\n" {
			t.Fatalf("fail output %q", out)
		}
		if mode != "fail" && got != "ready\nclosed\nstopped\nreturned <nil>\n" {
			t.Fatalf("%s output %q", mode, out)
		}
	}
}
