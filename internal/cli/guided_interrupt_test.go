package cli

import (
	"context"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/sigexit"
)

const (
	guidedInterruptModeEnv    = "FAULTLINE_TEST_GUIDED_INTERRUPT_MODE"
	guidedInterruptBarrierEnv = "FAULTLINE_TEST_GUIDED_INTERRUPT_BARRIER"
)

// TestGuidedInterruptHelper is not a test of its own: the parent re-executes
// this test binary with the mode variable. The child installs the real helper,
// prints "installed", waits for the context, reports over the parent's loopback
// barrier only after the context observed the first SIGINT, and then blocks
// until a second SIGINT ends it. Mode "boot" first installs the same SIGINT
// filter as internal/boot, so the helper runs under fl's real startup handler.
func TestGuidedInterruptHelper(t *testing.T) {
	mode := os.Getenv(guidedInterruptModeEnv)
	if mode == "" {
		return
	}
	if mode == "boot" {
		installBootLikeSIGINT()
	}
	ctx, cleanup := guidedInterruptSignal()
	_ = cleanup // retained, as the wrapper's defer does, until the process ends
	os.Stdout.WriteString("installed\n")
	select {
	case <-ctx.Done():
	case <-time.After(60 * time.Second):
		os.Exit(9)
	}
	conn, err := net.Dial("tcp4", os.Getenv(guidedInterruptBarrierEnv))
	if err != nil {
		os.Exit(8)
	}
	conn.Write([]byte("consumed\n"))
	conn.Close()
	time.Sleep(60 * time.Second)
	os.Exit(7)
}

// installBootLikeSIGINT mirrors internal/boot/boot_unix.go: a permanent SIGINT
// registration that dies of the signal unless a command intercepted it.
func installBootLikeSIGINT() {
	if runtime.GOOS == "windows" {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	go func() {
		for sig := range ch {
			if !sigexit.Intercepted(sig.(syscall.Signal)) {
				sigexit.Die(sig.(syscall.Signal))
			}
		}
	}()
}

func startGuidedInterruptChild(t *testing.T, mode, barrier string) *servefixture.Started {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := append(builtBinaryEnv(), guidedInterruptModeEnv+"="+mode, guidedInterruptBarrierEnv+"="+barrier)
	started, err := servefixture.StartWithOptions(ctx, os.Args[0], []string{"-test.run=^TestGuidedInterruptHelper$", "-test.count=1"}, nil, env, t.TempDir(),
		servefixture.StartOptions{
			StartupLines: 1, StartupDescription: "installed line",
			ValidateStartup: func(out []byte) (string, error) {
				if string(out) != "installed\n" {
					return "", os.ErrInvalid
				}
				return "", nil
			},
		})
	if err != nil {
		t.Fatalf("start helper child: %v", err)
	}
	t.Cleanup(func() { _ = started.Dispose() })
	return started
}

func TestGuidedInterruptFirstConsumedSecondTerminal(t *testing.T) {
	consoleHelper := ""
	if runtime.GOOS == "windows" {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		consoleHelper = buildJudgeDemoConsoleHelper(t, root, t.TempDir())
	}
	for _, mode := range []string{"plain", "boot"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			child := startGuidedInterruptChild(t, mode, ln.Addr().String())
			ictx, icancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer icancel()
			if err := child.Interrupt(ictx, consoleHelper); err != nil {
				t.Fatalf("first interrupt: %v", err)
			}
			// Barrier: reported only after the child's context observed the
			// first interrupt, so the second one is a distinct, later signal.
			ln.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second))
			conn, err := ln.Accept()
			if err != nil {
				t.Fatalf("first SIGINT was not consumed (no barrier report): %v", err)
			}
			buf := make([]byte, 16)
			n, _ := conn.Read(buf)
			conn.Close()
			if string(buf[:n]) != "consumed\n" {
				t.Fatalf("barrier = %q", buf[:n])
			}
			if child.Exited() {
				t.Fatal("child exited on the first SIGINT")
			}
			if err := child.Interrupt(ictx, consoleHelper); err != nil {
				t.Fatalf("second interrupt: %v", err)
			}
			wctx, wcancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer wcancel()
			result, err := child.Wait(wctx)
			if err != nil {
				t.Fatalf("second SIGINT did not end the child: %v", err)
			}
			if runtime.GOOS == "windows" {
				if result.ExitCode == nil || *result.ExitCode == 0 || *result.ExitCode == 7 {
					t.Fatalf("second Ctrl+C result %+v", result)
				}
			} else if result.Signal == nil || *result.Signal != "SIGINT" {
				t.Fatalf("second SIGINT must terminate by SIGINT; code %v signal %v stderr %q", result.ExitCode, result.Signal, result.Stderr)
			}
			if result.Stdout != "installed\n" {
				t.Fatalf("stdout = %q", result.Stdout)
			}
		})
	}
}

func TestGuidedInterruptCleanupIdempotentJoinsGoroutine(t *testing.T) {
	// Warm os/signal's own loop goroutine so it is part of the baseline.
	warm := make(chan os.Signal, 1)
	signal.Notify(warm, os.Interrupt)
	signal.Stop(warm)
	base := runtime.NumGoroutine()
	ctx, cleanup := guidedInterruptSignal()
	if ctx.Err() != nil {
		t.Fatal("context cancelled before any signal")
	}
	cleanup()
	cleanup()
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines after cleanup = %d, baseline %d (cleanup must join synchronously)", n, base)
	}
}
