package nativefixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/jsjson"
)

const processTestArg = "--nativefixture-owned-process-test-v1"

// Only the test executable recognizes these argv, before testing flag parsing.
// Production has no fault-control environment or argument selector.
func dispatchProcessTestHelper(args []string) (int, bool) {
	if len(args) < 2 || args[0] != processTestArg {
		return 0, false
	}
	switch args[1] {
	case "stdin":
		b, err := io.ReadAll(os.Stdin)
		if err != nil || len(b) != 0 {
			return 9, true
		}
		fmt.Fprint(os.Stdout, "stdin EOF\n")
		return 0, true
	case "environment":
		env := os.Environ()
		sort.Strings(env)
		fmt.Fprintln(os.Stdout, strings.Join(env, "\n"))
		return 0, true
	case "abnormal":
		fmt.Fprint(os.Stdout, "partial stdout\n")
		fmt.Fprint(os.Stderr, "partial stderr\n")
		return 7, true
	case "overflow":
		a, b := bytes.Repeat([]byte{'A'}, 1024), bytes.Repeat([]byte{'B'}, 1024)
		for {
			if _, err := os.Stdout.Write(a); err != nil {
				return 9, true
			}
			if _, err := os.Stderr.Write(b); err != nil {
				return 9, true
			}
		}
	case "block":
		if len(args) != 4 {
			return 9, true
		}
		ready, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return 9, true
		}
		block, err := strconv.ParseUint(args[3], 10, 64)
		if err != nil {
			return 9, true
		}
		readyFile, blockFile := os.NewFile(uintptr(ready), "owned-ready"), os.NewFile(uintptr(block), "owned-block")
		if readyFile == nil || blockFile == nil {
			return 9, true
		}
		fmt.Fprint(os.Stdout, "partial stdout\n")
		fmt.Fprint(os.Stderr, "partial stderr\n")
		if _, err := readyFile.Write([]byte{'R'}); err != nil {
			return 9, true
		}
		readyFile.Close()
		var one [1]byte
		_, _ = blockFile.Read(one[:])
		blockFile.Close()
		return 9, true
	case "pipe-drain":
		if len(args) < 4 {
			return 9, true
		}
		status, err := strconv.Atoi(args[len(args)-1])
		if err != nil || (status != 0 && status != 1 && status != 3) {
			return 9, true
		}
		fmt.Fprint(os.Stdout, "before drain\n")
		fmt.Fprint(os.Stderr, "drain stderr\n")
		if code := processTestDrainHelper(args[2 : len(args)-1]); code != 0 {
			return code, true
		}
		return status, true
	case "signal":
		fmt.Fprint(os.Stdout, "before signal\n")
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = p.Kill()
		}
		fmt.Fprintln(os.Stderr, err)
		return 9, true
	}
	return 9, true
}

func testProcessDeps() processDeps {
	return processDeps{executable: os.Executable, command: exec.CommandContext, mkTemp: os.MkdirTemp, writeFile: os.WriteFile, removeAll: os.RemoveAll}
}
func testMaterialized(t *testing.T) Materialized {
	t.Helper()
	m, err := Materialize(nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func testExecutor(t *testing.T, d processDeps) *Executor {
	t.Helper()
	e, err := newExecutorWithDeps(d)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func requireProcessError(t *testing.T, c Capture, err error, kind string) {
	t.Helper()
	var typed *ExecutionError
	if err == nil || !errors.As(err, &typed) || typed.Kind != kind || c.FailureKind != kind {
		t.Fatalf("failure kind=%q error=%v want=%s", c.FailureKind, err, kind)
	}
	if c.Cwd != "" {
		if _, err := os.Stat(c.Cwd); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("owned directory retained: %v", err)
		}
	}
}
func requireReaped(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil || cmd.ProcessState == nil {
		t.Fatal("no observed owned process/reap")
	}
	// Windows Wait releases the process handle rather than retaining a zombie;
	// a subsequent Kill therefore reports EINVAL instead of ErrProcessDone.
	var want error = os.ErrProcessDone
	if runtime.GOOS == "windows" {
		if !cmd.ProcessState.Exited() {
			t.Fatal("owned Windows process has not exited")
		}
		want = syscall.EINVAL
	}
	if err := cmd.Process.Kill(); !errors.Is(err, want) {
		t.Fatalf("owned process not already reaped: %v", err)
	}
}

// Omitting self-exec, changing transcript/status, leaking caller data, aliasing
// input, or deleting a caller-owned directory must fail this test.
func TestNativeProcessRealConfigurations(t *testing.T) {
	sentinel := t.TempDir()
	file := filepath.Join(sentinel, "caller.txt")
	if err := os.WriteFile(file, []byte("untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		hunks    []string
		status   int
		out, err string
	}{
		{"base", nil, 0, wantPassOut, ""},
		{"display", []string{"settlement-display-default"}, 0, wantPassOut, ""},
		{"boundary", []string{"settlement-display-boundary"}, 0, wantPassOut, ""},
		{"interaction", []string{"settlement-display-default", "settlement-display-boundary"}, 1, wantFailOut, wantFailErr},
		{"partial", []string{"partial-renderer-hunk"}, 3, "", "native fixture config: " + tokenSyntaxCause(t, partialConfig) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewExecutor()
			if err != nil {
				t.Fatal(err)
			}
			input, err := Materialize(tc.hunks)
			if err != nil {
				t.Fatal(err)
			}
			original := append([]byte(nil), input.Data...)
			c, err := e.Run(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if c.Status == nil || *c.Status != tc.status || string(c.Stdout) != tc.out || string(c.Stderr) != tc.err {
				t.Fatalf("status=%v stdout=%q stderr=%q", c.Status, c.Stdout, c.Stderr)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if c.Executable != exe || strings.Join(c.Argv, "\x00") != exe+"\x00"+ChildArg || c.Signal != "" || c.Incomplete || c.FailureKind != "" || c.CleanupError != "" || c.StartedAt.IsZero() || c.ExecutedAt.IsZero() || c.DurationMs < 0 {
				t.Fatalf("incomplete process facts: %+v", c)
			}
			if c.Cwd == "" || c.Cwd == sentinel {
				t.Fatal("not a fresh owned cwd")
			}
			if _, err := os.Stat(c.Cwd); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("cleanup: %v", err)
			}
			input.Data[0] ^= 1
			if !bytes.Equal(c.Config.Data, original) || c.Config.Partial != input.Partial {
				t.Fatal("configuration not snapshotted")
			}
			b, err := os.ReadFile(file)
			if err != nil || string(b) != "untouched\n" {
				t.Fatalf("caller sentinel changed: %q %v", b, err)
			}
		})
	}
}

func TestNativeProcessEnvironmentObservedAndFresh(t *testing.T) {
	for key, value := range map[string]string{"PATH": "caller-path", "HOME": "caller-home", "OPENAI_API_KEY": "fl-test-key", "FAULTLINE_TEST_SEED": "caller-seed"} {
		t.Setenv(key, value)
	}
	d := testProcessDeps()
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, exe, processTestArg, "environment")
	}
	e := testExecutor(t, d)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	env := []string{"FAULTLINE_NATIVE_DEMO_CHILD=v1"}
	envJSON := `["FAULTLINE_NATIVE_DEMO_CHILD=v1"]`
	if runtime.GOOS == "windows" {
		root := os.Getenv("SYSTEMROOT")
		if root == "" {
			t.Fatal("actual Windows SYSTEMROOT missing")
		}
		env = append(env, "SYSTEMROOT="+root)
		envJSON = `["FAULTLINE_NATIVE_DEMO_CHILD=v1",` + jsjson.Stringify(jsjson.MakeString("SYSTEMROOT="+root)) + `]`
	}
	want := fmt.Sprintf(`{"schemaVersion":"faultline.native-demo-environment.v1","runner":"native-go-config-evaluator-v1","runtime":%q,"goos":%q,"goarch":%q,"executableSHA256":"sha256:%x","environment":%s,"network":"disabled","credentials":"not-inherited","note":"Reviewed fixed program behavior; not OS isolation or remote attestation."}`, runtime.Version(), runtime.GOOS, runtime.GOARCH, sum, envJSON)
	got := e.Environment()
	if text := jsjson.Stringify(got); text != want {
		t.Fatalf("environment=%s want=%s", text, want)
	}
	got.Obj().Set("runner", jsjson.MakeString("changed"))
	if jsjson.Stringify(e.Environment()) != want {
		t.Fatal("environment preimage aliased")
	}
	c, err := e.Run(context.Background(), testMaterialized(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Environment, "\x00") != strings.Join(env, "\x00") {
		t.Fatalf("effective environment=%q want=%q", c.Environment, env)
	}
	childEnv := append([]string(nil), env...)
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		// Windows on ARM64 adds this exact architecture entry even when the
		// parent supplies an explicit minimal environment block.
		childEnv = append(childEnv, "PROCESSOR_ARCHITECTURE=ARM64")
	}
	sort.Strings(childEnv)
	if string(c.Stdout) != strings.Join(childEnv, "\n")+"\n" || len(c.Stderr) != 0 {
		t.Fatalf("actual child environment=%q stderr=%q", c.Stdout, c.Stderr)
	}
	c.Environment[0] = "changed"
	if jsjson.Stringify(e.Environment()) != want {
		t.Fatal("capture aliases executor environment")
	}
}

func TestNativeProcessModesAndOwnedCleanup(t *testing.T) {
	d := testProcessDeps()
	removed := 0
	d.removeAll = func(dir string) error {
		removed++
		for _, tc := range []struct {
			p    string
			mode fs.FileMode
		}{{dir, 0700}, {filepath.Join(dir, ConfigFile), 0600}} {
			info, err := os.Stat(tc.p)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != tc.mode {
				t.Errorf("%s mode=%o want=%o", tc.p, info.Mode().Perm(), tc.mode)
			}
		}
		return os.RemoveAll(dir)
	}
	c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || c.Cwd == "" {
		t.Fatal("owned cleanup not invoked once")
	}
}
func TestNativeProcessStdinEOF(t *testing.T) {
	d := testProcessDeps()
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, exe, processTestArg, "stdin")
	}
	c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Stdout) != "stdin EOF\n" || len(c.Stderr) != 0 || c.Status == nil || *c.Status != 0 {
		t.Fatalf("not stdin EOF: %+v", c)
	}
}
func TestNativeProcessCancelledBeforePreparation(t *testing.T) {
	d := testProcessDeps()
	commands, dirs := 0, 0
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		commands++
		return exec.CommandContext(ctx, exe, args...)
	}
	d.mkTemp = func(a, b string) (string, error) { dirs++; return os.MkdirTemp(a, b) }
	e := testExecutor(t, d)
	commandsBefore := commands
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := e.Run(ctx, testMaterialized(t))
	requireProcessError(t, c, err, "CANCELLED")
	if !errors.Is(err, context.Canceled) || commands != commandsBefore || dirs != 0 || c.Status != nil || !c.StartedAt.IsZero() {
		t.Fatalf("cancelled call attempted preparation: %+v %v", c, err)
	}
}
func TestNativeProcessPrepareSpawnAndPanicFailures(t *testing.T) {
	primary := errors.New("owned primary fault")
	cleanup := errors.New("owned cleanup fault")
	for _, kind := range []string{"temp", "write", "spawn", "panic", "joined"} {
		t.Run(kind, func(t *testing.T) {
			d := testProcessDeps()
			want := "PREPARE"
			owned := ""
			d.mkTemp = func(a, b string) (string, error) {
				if kind == "temp" {
					return "", primary
				}
				p, err := os.MkdirTemp(a, b)
				owned = p
				return p, err
			}
			t.Cleanup(func() {
				if owned != "" {
					if err := os.RemoveAll(owned); err != nil {
						t.Error(err)
					}
				}
			})
			if kind == "write" || kind == "joined" {
				d.writeFile = func(string, []byte, fs.FileMode) error { return primary }
			}
			if kind == "spawn" {
				want = "SPAWN"
				d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
					return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "absent-owned-executable"), args...)
				}
			}
			if kind == "panic" {
				want = "PANIC"
				d.writeFile = func(string, []byte, fs.FileMode) error { panic("owned preparation panic") }
			}
			if kind == "joined" {
				d.removeAll = func(string) error { return cleanup }
			}
			c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
			if kind == "joined" {
				if !errors.Is(err, primary) || !errors.Is(err, cleanup) || c.CleanupError != cleanup.Error() {
					t.Fatalf("lost simultaneous failures: %+v %v", c, err)
				}
				if removeErr := os.RemoveAll(owned); removeErr != nil {
					t.Fatal(removeErr)
				}
			}
			requireProcessError(t, c, err, want)
			if c.Status != nil {
				t.Fatal("fabricated exit status")
			}
			if !c.StartedAt.IsZero() || !c.ExecutedAt.IsZero() || c.DurationMs != 0 {
				t.Fatal("unstarted child has execution timestamps")
			}
			if kind == "temp" || kind == "write" {
				if !errors.Is(err, primary) {
					t.Fatal("lost primary cause")
				}
			}
		})
	}
}
func TestNativeProcessExecutableObservationFailure(t *testing.T) {
	cause := errors.New("executable observation failed")
	d := testProcessDeps()
	d.executable = func() (string, error) { return "", cause }
	e, err := newExecutorWithDeps(d)
	var typed *ExecutionError
	if e != nil || !errors.As(err, &typed) || typed.Kind != "ENVIRONMENT" || !errors.Is(err, cause) {
		t.Fatalf("factory=%v error=%v", e, err)
	}
	d = testProcessDeps()
	d.executable = func() (string, error) { return filepath.Join(t.TempDir(), "absent"), nil }
	e, err = newExecutorWithDeps(d)
	if e != nil || !errors.As(err, &typed) || typed.Kind != "ENVIRONMENT" || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("hash failure factory=%v error=%v", e, err)
	}
}
func TestNativeProcessAbnormalExit(t *testing.T) {
	d := testProcessDeps()
	var cmd *exec.Cmd
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		cmd = exec.CommandContext(ctx, exe, processTestArg, "abnormal")
		return cmd
	}
	c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
	requireProcessError(t, c, err, "PROTOCOL")
	requireReaped(t, cmd)
	if c.Status == nil || *c.Status != 7 || string(c.Stdout) != "partial stdout\n" || string(c.Stderr) != "partial stderr\n" || c.Incomplete {
		t.Fatalf("lost abnormal evidence: %+v", c)
	}
}
func TestNativeProcessCombinedOutputLimit(t *testing.T) {
	d := testProcessDeps()
	var cmd *exec.Cmd
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		cmd = exec.CommandContext(ctx, exe, processTestArg, "overflow")
		return cmd
	}
	c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
	requireProcessError(t, c, err, "OUTPUT_LIMIT")
	requireReaped(t, cmd)
	if !c.Incomplete || len(c.Stdout)+len(c.Stderr) != 1<<20 || len(c.Stdout) == 0 || len(c.Stderr) == 0 {
		t.Fatalf("not combined capped prefixes: stdout=%d stderr=%d incomplete=%v", len(c.Stdout), len(c.Stderr), c.Incomplete)
	}
	if !bytes.Equal(c.Stdout, bytes.Repeat([]byte{'A'}, len(c.Stdout))) || !bytes.Equal(c.Stderr, bytes.Repeat([]byte{'B'}, len(c.Stderr))) {
		t.Fatal("raw prefix bytes changed")
	}
}
func TestNativeProcessCleanupFailureAndPanicAfterWait(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(strconv.FormatBool(panics), func(t *testing.T) {
			d := testProcessDeps()
			var cmd *exec.Cmd
			owned := ""
			cause := errors.New("owned cleanup rejected")
			d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
				cmd = exec.CommandContext(ctx, exe, args...)
				return cmd
			}
			d.removeAll = func(dir string) error {
				owned = dir
				if panics {
					panic("owned cleanup panic")
				}
				return cause
			}
			t.Cleanup(func() {
				if owned != "" {
					if err := os.RemoveAll(owned); err != nil {
						t.Error(err)
					}
				}
			})
			c, err := testExecutor(t, d).Run(context.Background(), testMaterialized(t))
			kind := "CLEANUP"
			if panics {
				kind = "PANIC"
			}
			var typed *ExecutionError
			if !errors.As(err, &typed) || typed.Kind != kind || c.FailureKind != kind || c.CleanupError == "" {
				t.Fatalf("cleanup evidence=%+v error=%v", c, err)
			}
			requireReaped(t, cmd)
			if c.Status == nil || *c.Status != 0 || string(c.Stdout) != wantPassOut {
				t.Fatal("cleanup failure discarded observed successful process")
			}
			if !panics && !errors.Is(err, cause) {
				t.Fatal("cleanup cause discarded")
			}
		})
	}
}
func TestNativeProcessRejectsChangedEffectiveEnvironment(t *testing.T) {
	d := testProcessDeps()
	var cmd *exec.Cmd
	d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
		cmd = exec.CommandContext(ctx, exe, args...)
		return cmd
	}
	e := testExecutor(t, d)
	e.environment = append(e.environment, "PATH=caller-path")
	c, err := e.Run(context.Background(), testMaterialized(t))
	requireProcessError(t, c, err, "ENVIRONMENT")
	if cmd.Process != nil || c.Status != nil {
		t.Fatal("changed environment spawned")
	}
}

type processDrainFixture struct {
	command func(context.Context, string, int) *exec.Cmd
	receive func() (*os.File, error)
}

// The parent owns a duplicate of the child's stdout write descriptor. No
// descendant is needed: hold that descriptor until WaitDelay has fired.
// ExitError must not hide forced closure for ordinary nonzero statuses.
func TestNativeProcessPipeDrainBound(t *testing.T) {
	for _, status := range []int{0, 1, 3} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fixture := newProcessDrainFixture(t)
			d := testProcessDeps()
			var cmd *exec.Cmd
			d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
				cmd = fixture.command(ctx, exe, status)
				return cmd
			}
			e := testExecutor(t, d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				c   Capture
				err error
			}
			done := make(chan result, 1)
			input := testMaterialized(t)
			go func() { c, err := e.Run(ctx, input); done <- result{c, err} }()
			type descriptor struct {
				f   *os.File
				err error
			}
			received := make(chan descriptor)
			go func() {
				f, err := fixture.receive()
				select {
				case received <- descriptor{f, err}:
				case <-ctx.Done():
					if f != nil {
						f.Close()
					}
				}
			}()
			select {
			case held := <-received:
				if held.err != nil {
					t.Error(held.err)
					cancel()
				}
				if held.f != nil {
					defer held.f.Close()
				}
			case <-time.After(5 * time.Second):
				t.Error("owned descriptor transfer timed out")
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("owned pipe drain did not terminate")
			}
			requireProcessError(t, got.c, got.err, "PIPE_DRAIN")
			requireReaped(t, cmd)
			if got.c.Status == nil || *got.c.Status != status || got.c.Signal != "" || string(got.c.Stdout) != "before drain\n" || string(got.c.Stderr) != "drain stderr\n" {
				t.Fatalf("lost pipe-drain process evidence: %+v %v", got.c, got.err)
			}
			if status == 0 && !errors.Is(got.err, exec.ErrWaitDelay) {
				t.Fatal("lost observed WaitDelay error")
			}
			if status != 0 {
				var exit *exec.ExitError
				if !errors.As(got.err, &exit) || exit.ExitCode() != status {
					t.Fatal("lost observed nonzero Wait error")
				}
			}
			if !got.c.Incomplete {
				t.Fatal("forced pipe closure falsely marked streams complete")
			}
		})
	}
}

type processTestPipes struct{ readyRead, readyWrite, blockRead, blockWrite *os.File }

func newProcessTestPipes(t *testing.T) processTestPipes {
	t.Helper()
	var p processTestPipes
	var err error
	p.readyRead, p.readyWrite, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.readyRead.Close(); p.readyWrite.Close() })
	p.blockRead, p.blockWrite, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.blockRead.Close(); p.blockWrite.Close() })
	return p
}
func TestNativeProcessReadinessCancellationAndDeadline(t *testing.T) {
	for _, cancels := range []bool{true, false} {
		t.Run(strconv.FormatBool(cancels), func(t *testing.T) {
			pipes := newProcessTestPipes(t)
			d := testProcessDeps()
			var cmd *exec.Cmd
			d.command = func(ctx context.Context, exe string, args ...string) *exec.Cmd {
				cmd = processTestBlockingCommand(t, ctx, exe, pipes)
				return cmd
			}
			e := testExecutor(t, d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				c   Capture
				err error
			}
			done := make(chan result, 1)
			input := testMaterialized(t)
			started := time.Now()
			go func() { c, err := e.Run(ctx, input); done <- result{c, err} }()
			ready := make(chan error, 1)
			go func() {
				var b [1]byte
				_, err := io.ReadFull(pipes.readyRead, b[:])
				if err == nil && b[0] != 'R' {
					err = fmt.Errorf("readiness=%q", b)
				}
				ready <- err
			}()
			select {
			case err := <-ready:
				if err != nil {
					t.Error(err)
					cancel()
				}
			case <-time.After(5 * time.Second):
				t.Error("owned helper readiness timed out")
				cancel()
				pipes.readyRead.Close()
			}
			if cancels {
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("owned process did not terminate within observation allowance")
			}
			want := "DEADLINE"
			cause := context.DeadlineExceeded
			if cancels {
				want = "CANCELLED"
				cause = context.Canceled
			}
			requireProcessError(t, got.c, got.err, want)
			requireReaped(t, cmd)
			if runtime.GOOS != "windows" && (got.c.Signal != "killed" || got.c.Status != nil) {
				t.Fatalf("cancellation did not observe direct hard kill: %+v", got.c)
			}
			if !errors.Is(got.err, cause) {
				t.Fatalf("context cause lost: %v", got.err)
			}
			if string(got.c.Stdout) != "partial stdout\n" || string(got.c.Stderr) != "partial stderr\n" || got.c.DurationMs < 0 || got.c.ExecutedAt.IsZero() {
				t.Fatal("partial output/timing not retained")
			}
			if !cancels && time.Since(started) < 2*time.Second {
				t.Fatal("default two-second execution deadline shortened")
			}
			if cmd.WaitDelay != 250*time.Millisecond {
				t.Fatalf("pipe drain bound=%v", cmd.WaitDelay)
			}
		})
	}
}
