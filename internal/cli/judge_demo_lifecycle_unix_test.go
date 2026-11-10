//go:build unix

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
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

// TestJudgeDemoServeSignalHelper is a re-executed helper, inert without its
// variable. Its start prints "ready"; its close prints "close-started" and
// then blocks until the parent closes stdin (the release channel), so a
// second signal can be delivered while close is in progress. The mixed mode
// also reports "second-seen" once its own subscription has received the
// second (different) signal, proving the runtime delivered it.
func TestJudgeDemoServeSignalHelper(t *testing.T) {
	mode := os.Getenv("FAULTLINE_TEST_JUDGE_DEMO_SIGNAL_HELPER")
	if mode == "" {
		return
	}
	var seen chan os.Signal
	if mode == "mixed" {
		seen = make(chan os.Signal, 8)
		signal.Notify(seen, syscall.SIGTERM)
	}
	err := awaitJudgeDemoServeInterrupt(func() (func() error, error) {
		fmt.Println("ready")
		return func() error {
			fmt.Println("close-started")
			if seen != nil {
				<-seen
				fmt.Println("second-seen")
			}
			_, _ = io.Copy(io.Discard, os.Stdin) // releaseClose: parent closes stdin
			return nil
		}, nil
	}, func() { fmt.Println("stopped") })
	fmt.Printf("returned %v\n", err)
}

func TestJudgeDemoServeRepeatedSignal(t *testing.T) {
	cases := []struct {
		name, mode    string
		first, second syscall.Signal
		dies          bool
	}{
		{"SIGINT then SIGINT", "same", syscall.SIGINT, syscall.SIGINT, true},
		{"SIGTERM then SIGTERM", "same", syscall.SIGTERM, syscall.SIGTERM, true},
		{"SIGINT then SIGTERM", "mixed", syscall.SIGINT, syscall.SIGTERM, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestJudgeDemoServeSignalHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "FAULTLINE_TEST_JUDGE_DEMO_SIGNAL_HELPER="+tc.mode)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			lines := make(chan string, 16)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
				close(lines)
			}()
			var all []string
			await := func(want string) {
				t.Helper()
				deadline := time.After(30 * time.Second)
				for {
					select {
					case line, ok := <-lines:
						if !ok {
							t.Fatalf("helper output ended before %q: %q", want, all)
						}
						all = append(all, line)
						if line == want {
							return
						}
					case <-deadline:
						t.Fatalf("timed out awaiting %q: %q", want, all)
					}
				}
			}
			await("ready")
			if err := cmd.Process.Signal(tc.first); err != nil {
				t.Fatal(err)
			}
			await("close-started") // closeStarted
			if err := cmd.Process.Signal(tc.second); err != nil {
				t.Fatal(err)
			}
			if !tc.dies {
				await("second-seen")
				_ = stdin.Close() // releaseClose
			}
			for line := range lines {
				all = append(all, line)
			}
			err = cmd.Wait()
			joined := strings.Join(all, "\n")
			if tc.dies {
				status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != tc.second {
					t.Fatalf("want raw %v death, got %v state=%v output=%q", tc.second, err, cmd.ProcessState, joined)
				}
				if strings.Contains(joined, "stopped") || strings.Contains(joined, "returned") {
					t.Fatalf("stop summary printed despite repeated signal: %q", joined)
				}
				return
			}
			if err != nil {
				t.Fatalf("mixed signals: %v output=%q", err, joined)
			}
			if got, want := joined, "ready\nclose-started\nsecond-seen\nstopped\nreturned <nil>\nPASS"; got != want {
				t.Fatalf("output %q want %q", got, want)
			}
		})
	}
}
