package servefixture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const readyStreamHelperEnv = "FAULTLINE_TEST_READY_STREAM_HELPER"

// TestReadyStreamHelper is not a test of its own: the parent tests re-execute
// this test binary with the helper variable. Mode "stderr" writes its
// readiness line to stderr only; "stdout" writes the readiness line to stdout
// and noise to stderr; "exit" exits before any readiness; "hang" writes
// nothing and waits to be killed.
func TestReadyStreamHelper(t *testing.T) {
	switch os.Getenv(readyStreamHelperEnv) {
	case "":
		return
	case "stderr":
		os.Stderr.WriteString("ready-on-stderr\n")
		time.Sleep(time.Hour)
	case "stdout":
		os.Stdout.WriteString("ready-on-stdout\n")
		os.Stderr.WriteString("noise\n")
		time.Sleep(time.Hour)
	case "exit":
		os.Exit(3)
	case "ready-then-exit":
		os.Stderr.WriteString("ready-on-stderr\n")
		os.Exit(4)
	case "extra":
		os.Stderr.WriteString("ready-on-stderr\nextra\n")
		time.Sleep(time.Hour)
	case "stdout-noise":
		os.Stdout.WriteString("noise-on-stdout\n")
		os.Stderr.WriteString("ready-on-stderr\n")
		time.Sleep(time.Hour)
	case "hang":
		time.Sleep(time.Hour)
	}
}

func startReadyStreamHelper(t *testing.T, ctx context.Context, mode string, options StartOptions) (*Started, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return StartWithOptions(ctx, exe, nil, []string{"-test.run=^TestReadyStreamHelper$", "-test.count=1"},
		append(os.Environ(), readyStreamHelperEnv+"="+mode), t.TempDir(), options)
}

func lineValidator(want string) func([]byte) (string, error) {
	return func(out []byte) (string, error) {
		if !strings.HasPrefix(string(out), want+"\n") {
			return "", os.ErrInvalid
		}
		return want, nil
	}
}

func TestReadyStreamStderrReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := startReadyStreamHelper(t, ctx, "stderr", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: lineValidator("ready-on-stderr"), ReadyStream: "stderr"})
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "ready-on-stderr" {
		t.Fatalf("URL %q", p.URL)
	}
	if err := p.Dispose(); err == nil {
		t.Fatal("a still-running child must be reported as force-terminated")
	}
	res, err := p.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stderr != "ready-on-stderr\n" || res.Stdout != "" {
		t.Fatalf("captured %+v", res)
	}
}

func TestReadyStreamDefaultsToStdout(t *testing.T) {
	for _, stream := range []string{"", "stdout"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		p, err := startReadyStreamHelper(t, ctx, "stdout", StartOptions{StartupLines: 1, StartupDescription: "stdout line",
			ValidateStartup: lineValidator("ready-on-stdout"), ReadyStream: stream})
		if err != nil {
			cancel()
			t.Fatalf("%q: %v", stream, err)
		}
		_ = p.Dispose()
		res, _ := p.Wait(ctx)
		cancel()
		if !strings.HasPrefix(res.Stdout, "ready-on-stdout\n") {
			t.Fatalf("%q: %+v", stream, res)
		}
	}
}

// An invalid ReadyStream is refused before any child exists: the executable
// here is the test binary running a mode that would create a marker.
func TestReadyStreamInvalidValueDoesNotLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"STDERR", "both", " stderr"} {
		p, err := StartWithOptions(ctx, exe, nil, []string{"-test.run=^TestReadyStreamMarkerHelper$", "-test.count=1"},
			append(os.Environ(), "FAULTLINE_TEST_READY_MARKER="+marker), t.TempDir(),
			StartOptions{StartupLines: 1, StartupDescription: "x", ValidateStartup: lineValidator("x"), ReadyStream: bad})
		if err == nil || p != nil || !strings.Contains(err.Error(), "ReadyStream") {
			t.Fatalf("%q: p=%v err=%v", bad, p, err)
		}
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("an invalid ReadyStream launched the child")
	}
}

// TestReadyStreamMarkerHelper creates the marker file named by the helper
// variable; it is a no-op otherwise.
func TestReadyStreamMarkerHelper(t *testing.T) {
	if marker := os.Getenv("FAULTLINE_TEST_READY_MARKER"); marker != "" {
		_ = os.WriteFile(marker, nil, 0o600)
	}
}

func TestReadyStreamEarlyExitAndDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := startReadyStreamHelper(t, ctx, "exit", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: lineValidator("x"), ReadyStream: "stderr"})
	if err == nil || !strings.Contains(err.Error(), "fl exited before printing its stderr line") || !strings.Contains(err.Error(), "code 3") {
		t.Fatalf("early exit: %v", err)
	}
	short, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	_, err = startReadyStreamHelper(t, short, "hang", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: lineValidator("x"), ReadyStream: "stderr"})
	if err == nil || !strings.Contains(err.Error(), "startup deadline") {
		t.Fatalf("deadline: %v", err)
	}
}

// exactLineValidator accepts only a stream that is exactly the ready line.
func exactLineValidator(want string) func([]byte) (string, error) {
	return func(out []byte) (string, error) {
		if string(out) != want+"\n" {
			return "", os.ErrInvalid
		}
		return want, nil
	}
}

func TestReadyStreamStderrExtraBytesAreRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The validator runs once the first line is complete; the second line may
	// already be present, in which case the strict validator rejects it.
	// Either way readiness must not be accepted with "extra" on stderr.
	p, err := startReadyStreamHelper(t, ctx, "extra", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: exactLineValidator("ready-on-stderr"), ReadyStream: "stderr"})
	if err == nil {
		// The first line was observed before "extra" arrived (a single write
		// makes that impossible, but stay precise): the extra bytes must be
		// visible in the captured stream.
		defer p.Dispose()
		t.Fatalf("readiness accepted despite extra stderr bytes")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("rejection does not retain the extra stderr bytes: %v", err)
	}
}

func TestReadyStreamStdoutNoiseIsRetainedButIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := startReadyStreamHelper(t, ctx, "stdout-noise", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: exactLineValidator("ready-on-stderr"), ReadyStream: "stderr"})
	if err != nil {
		t.Fatalf("stdout noise affected stderr readiness: %v", err)
	}
	_ = p.Dispose()
	res, err := p.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "noise-on-stdout\n" || res.Stderr != "ready-on-stderr\n" {
		t.Fatalf("captured %+v", res)
	}
}

// A complete ready line followed immediately by a nonzero exit must be
// accepted whatever the scheduling: StartWithOptions rechecks the selected
// collector after Cmd.Wait has returned. The exit status and both streams stay
// readable afterwards.
func TestReadyStreamReadyLineThenNonzeroExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := startReadyStreamHelper(t, ctx, "ready-then-exit", StartOptions{StartupLines: 1, StartupDescription: "stderr line",
		ValidateStartup: exactLineValidator("ready-on-stderr"), ReadyStream: "stderr"})
	if err != nil {
		t.Fatalf("a complete ready line before exit must be accepted: %v", err)
	}
	if p.URL != "ready-on-stderr" {
		t.Fatalf("URL %q", p.URL)
	}
	res, err := p.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == nil || *res.ExitCode != 4 || res.Stderr != "ready-on-stderr\n" || res.Stdout != "" {
		t.Fatalf("result %+v", res)
	}
	_ = p.Dispose()
}
