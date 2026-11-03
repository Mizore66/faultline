package nativefixture

import (
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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Capture is observed process evidence, not an accepted predicate or RunRecord.
// Zero timestamps/status/signal mean unavailable, not a fabricated observation.
type Capture struct {
	Config                    Materialized
	Executable                string
	Argv, Environment         []string
	Cwd                       string
	StartedAt, ExecutedAt     time.Time
	DurationMs                int64
	Status                    *int
	Signal                    string
	Stdout, Stderr            []byte
	Incomplete                bool
	FailureKind, CleanupError string
}

type ExecutionError struct {
	Kind string
	Err  error
}

func (e *ExecutionError) Error() string { return fmt.Sprintf("%s: %v", e.Kind, e.Err) }
func (e *ExecutionError) Unwrap() error { return e.Err }

type processDeps struct {
	executable func() (string, error)
	command    func(context.Context, string, ...string) *exec.Cmd
	mkTemp     func(string, string) (string, error)
	writeFile  func(string, []byte, fs.FileMode) error
	removeAll  func(string) error
}

type Executor struct {
	deps                         processDeps
	executable, executableSHA256 string
	environment                  []string
	environmentPreimage          jsjson.Value
}

// NewExecutor binds only the current executable and the reviewed private child.
// It neither accepts a program/path selector nor inherits caller credentials.
func NewExecutor() (*Executor, error) {
	return newExecutorWithDeps(processDeps{os.Executable, exec.CommandContext, os.MkdirTemp, os.WriteFile, os.RemoveAll})
}
func newExecutorWithDeps(d processDeps) (*Executor, error) {
	executable, err := d.executable()
	if err != nil {
		return nil, &ExecutionError{"ENVIRONMENT", err}
	}
	f, err := os.Open(executable)
	if err != nil {
		return nil, &ExecutionError{"ENVIRONMENT", err}
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, f)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return nil, &ExecutionError{"ENVIRONMENT", err}
	}
	environment := []string{ChildEnvName + "=" + ChildEnvValue}
	if runtime.GOOS == "windows" {
		root := os.Getenv("SYSTEMROOT")
		if root == "" {
			return nil, &ExecutionError{"ENVIRONMENT", errors.New("Windows SYSTEMROOT is required")}
		}
		environment = append(environment, "SYSTEMROOT="+root)
	}
	cmd := d.command(context.Background(), executable, ChildArg)
	effective, err := commandEnvironment(cmd, environment)
	if err != nil {
		return nil, &ExecutionError{"ENVIRONMENT", err}
	}
	e := &Executor{deps: d, executable: executable, executableSHA256: fmt.Sprintf("sha256:%x", hash.Sum(nil)), environment: slices.Clone(effective)}
	values := make([]jsjson.Value, len(effective))
	for i, value := range effective {
		values[i] = jsjson.MakeString(value)
	}
	e.environmentPreimage = jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-demo-environment.v1"),
		"runner", jsjson.MakeString(Runner),
		"runtime", jsjson.MakeString(runtime.Version()),
		"goos", jsjson.MakeString(runtime.GOOS),
		"goarch", jsjson.MakeString(runtime.GOARCH),
		"executableSHA256", jsjson.MakeString(e.executableSHA256),
		"environment", jsjson.MakeArray(values),
		"network", jsjson.MakeString("disabled"),
		"credentials", jsjson.MakeString("not-inherited"),
		"note", jsjson.MakeString("Reviewed fixed program behavior; not OS isolation or remote attestation."),
	)
	return e, nil
}

// Environment returns a fresh ordered object and fresh string-array storage.
func (e *Executor) Environment() jsjson.Value {
	out := jsjson.NewObj()
	for _, key := range e.environmentPreimage.Obj().Keys() {
		value := e.environmentPreimage.Get(key)
		if key == "environment" {
			value = jsjson.MakeArray(slices.Clone(value.Items()))
		}
		out.Set(key, value)
	}
	return jsjson.MakeObject(out)
}

// Inspect the effective vector, including os/exec additions. Windows key
// normalization is applied to the command itself, then observed again; no
// sorted/intended-only substitute is recorded as the actual environment.
func commandEnvironment(cmd *exec.Cmd, intended []string) ([]string, error) {
	cmd.Env = append([]string{}, intended...)
	actual := cmd.Environ()
	out := make([]string, 0, len(actual))
	seen := make(map[string]int)
	for _, entry := range actual {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return actual, errors.New("malformed effective environment entry")
		}
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		switch key {
		case ChildEnvName:
			if value != ChildEnvValue {
				return actual, errors.New("incorrect child protocol environment")
			}
		case "SYSTEMROOT":
			if runtime.GOOS != "windows" || value == "" {
				return actual, errors.New("unexpected or empty SYSTEMROOT")
			}
		default:
			return actual, fmt.Errorf("unexpected effective environment key %q", key)
		}
		normalized := key + "=" + value
		if index, exists := seen[key]; exists {
			if runtime.GOOS != "windows" {
				return actual, fmt.Errorf("duplicate effective environment key %q", key)
			}
			out[index] = normalized
		} else {
			seen[key] = len(out)
			out = append(out, normalized)
		}
	}
	expected := 1
	if runtime.GOOS == "windows" {
		expected = 2
	}
	if len(out) != expected {
		return actual, errors.New("incomplete effective environment")
	}
	cmd.Env = slices.Clone(out)
	if observed := cmd.Environ(); !slices.Equal(observed, out) {
		return observed, errors.New("effective environment changed during observation")
	}
	return slices.Clone(out), nil
}

func (e *Executor) Run(ctx context.Context, input Materialized) (capture Capture, err error) {
	capture.Config = Materialized{Data: slices.Clone(input.Data), Partial: input.Partial}
	var cmd *exec.Cmd
	var output *processOutput
	var timer clock.EvidenceTimer
	waited := false
	fail := func(kind string, cause error) {
		if capture.FailureKind == "" {
			capture.FailureKind = kind
		}
		err = errors.Join(err, &ExecutionError{kind, cause})
	}
	observeWait := func() {
		capture.ExecutedAt = clock.Now()
		capture.DurationMs = timer.Milliseconds()
		if cmd.ProcessState != nil {
			if status := cmd.ProcessState.ExitCode(); status >= 0 {
				capture.Status = &status
			}
			capture.Signal = observedSignal(cmd.ProcessState)
		}
	}
	defer func() {
		recovered := recover()
		if recovered != nil {
			fail("PANIC", fmt.Errorf("native process panic: %v", recovered))
		}
		if cmd != nil && cmd.Process != nil && !waited {
			killErr := cmd.Process.Kill()
			// The invocation owns this exact child. Wait once even on panic; no
			// process-tree enumeration, broad cleanup, or termination retry.
			waitErr := cmd.Wait()
			waited = true
			observeWait()
			if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				fail("CLEANUP", killErr)
			}
			if waitErr != nil {
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) {
					fail("CLEANUP", waitErr)
				}
			}
		}
		if output != nil {
			if waited {
				output.completion()
			}
			capture.Stdout, capture.Stderr, capture.Incomplete = output.snapshot()
		}
		if capture.Cwd != "" {
			cleanupErr, panicValue := removeProcessDirectory(e.deps.removeAll, capture.Cwd)
			if cleanupErr != nil {
				capture.CleanupError = cleanupErr.Error()
				kind := "CLEANUP"
				if panicValue != nil {
					kind = "PANIC"
				}
				fail(kind, cleanupErr)
			}
			if _, fatal := panicValue.(runtime.Error); fatal {
				panic(panicValue)
			}
		}
		if _, fatal := recovered.(runtime.Error); fatal {
			panic(recovered)
		}
	}()
	if ctx == nil {
		fail("CANCELLED", errors.New("nil execution context"))
		return
	}
	if cause := ctx.Err(); cause != nil {
		kind := "CANCELLED"
		if errors.Is(cause, context.DeadlineExceeded) {
			kind = "DEADLINE"
		}
		fail(kind, cause)
		return
	}
	runContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output = &processOutput{cancel: cancel}
	cmd = e.deps.command(runContext, e.executable, ChildArg)
	capture.Executable = cmd.Path
	capture.Argv = slices.Clone(cmd.Args)
	capture.Cwd, err = e.deps.mkTemp("", "faultline-native-demo-")
	if err != nil {
		cause := err
		err = nil
		fail("PREPARE", cause)
		return
	}
	if capture.Cwd == "" {
		fail("PREPARE", errors.New("empty owned temporary directory"))
		return
	}
	if cause := e.deps.writeFile(filepath.Join(capture.Cwd, ConfigFile), slices.Clone(capture.Config.Data), 0600); cause != nil {
		fail("PREPARE", cause)
		return
	}
	cmd.Dir = capture.Cwd
	cmd.Stdin = nil
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	capture.Environment, err = commandEnvironment(cmd, e.environment)
	if err != nil {
		cause := err
		err = nil
		fail("ENVIRONMENT", cause)
		return
	}
	frozen := e.environmentPreimage.Get("environment").Items()
	if len(frozen) != len(capture.Environment) {
		fail("ENVIRONMENT", errors.New("effective environment differs from frozen preimage"))
		return
	}
	for i, value := range frozen {
		if value.Str() != capture.Environment[i] {
			fail("ENVIRONMENT", errors.New("effective environment differs from frozen preimage"))
			return
		}
	}
	cmd.Stdout = processStream{output: output}
	cmd.Stderr = processStream{output: output, stderr: true}
	if cause := runContext.Err(); cause != nil {
		kind := "CANCELLED"
		if errors.Is(cause, context.DeadlineExceeded) {
			kind = "DEADLINE"
		}
		fail(kind, cause)
		return
	}
	timer = clock.StartEvidenceTimer()
	capture.StartedAt = clock.Now()
	if cause := cmd.Start(); cause != nil {
		capture.StartedAt = time.Time{}
		kind := "SPAWN"
		if runContext.Err() != nil {
			cause = errors.Join(cause, runContext.Err())
			kind = "CANCELLED"
			if errors.Is(runContext.Err(), context.DeadlineExceeded) {
				kind = "DEADLINE"
			}
		}
		fail(kind, cause)
		return
	}
	waitErr := cmd.Wait()
	waited = true
	observeWait()
	limited, drainErr := output.completion()
	switch {
	case limited:
		fail("OUTPUT_LIMIT", errors.New("combined stdout/stderr reached one MiB capture ceiling"))
	case runContext.Err() != nil:
		kind := "CANCELLED"
		if errors.Is(runContext.Err(), context.DeadlineExceeded) {
			kind = "DEADLINE"
		}
		fail(kind, errors.Join(runContext.Err(), waitErr))
	case drainErr != nil || errors.Is(waitErr, exec.ErrWaitDelay):
		fail("PIPE_DRAIN", errors.Join(drainErr, waitErr))
	case capture.Signal != "":
		fail("SIGNAL", waitErr)
	case capture.Status == nil:
		fail("PROTOCOL", errors.Join(errors.New("no observed ordinary exit status"), waitErr))
	case *capture.Status != 0 && *capture.Status != 1 && *capture.Status != 3:
		fail("PROTOCOL", errors.Join(fmt.Errorf("unexpected child exit status %d", *capture.Status), waitErr))
	case waitErr != nil:
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			fail("PROTOCOL", waitErr)
		}
	}
	return
}

func removeProcessDirectory(remove func(string) error, dir string) (err error, panicValue any) {
	defer func() {
		if p := recover(); p != nil {
			panicValue = p
			err = fmt.Errorf("native cleanup panic: %v", p)
		}
	}()
	err = remove(dir)
	return
}

const processOutputLimit = 1 << 20

type processOutput struct {
	mu             sync.Mutex
	stdout, stderr []byte
	size           int
	incomplete     bool
	limited        bool
	stdoutEOF      bool
	stderrEOF      bool
	readErr        error
	cancel         context.CancelFunc
}
type processStream struct {
	output *processOutput
	stderr bool
}

func (s processStream) Write(data []byte) (int, error) {
	o := s.output
	o.mu.Lock()
	remaining := processOutputLimit - o.size
	keep := min(len(data), remaining)
	if s.stderr {
		o.stderr = append(o.stderr, data[:keep]...)
	} else {
		o.stdout = append(o.stdout, data[:keep]...)
	}
	o.size += keep
	limit := len(data) > 0 && len(data) >= remaining
	if limit {
		o.incomplete = true
		o.limited = true
	}
	o.mu.Unlock()
	if limit {
		o.cancel()
	}
	// Consumed, including discarded suffix; cancellation terminates the child.
	return len(data), nil
}

// os/exec copies each pipe with io.Copy. Observe the read boundary ourselves:
// Wait can hide a forced pipe closure behind an ordinary nonzero ExitError.
func (s processStream) ReadFrom(r io.Reader) (total int64, err error) {
	var buffer [32 * 1024]byte
	for {
		n, readErr := r.Read(buffer[:])
		if n > 0 {
			s.Write(buffer[:n])
			total += int64(n)
		}
		if readErr == nil {
			continue
		}
		o := s.output
		o.mu.Lock()
		if readErr == io.EOF {
			if s.stderr {
				o.stderrEOF = true
			} else {
				o.stdoutEOF = true
			}
		} else {
			o.incomplete = true
			o.readErr = errors.Join(o.readErr, readErr)
		}
		o.mu.Unlock()
		if readErr == io.EOF {
			return total, nil
		}
		return total, readErr
	}
}

func (o *processOutput) completion() (limited bool, drainErr error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.stdoutEOF || !o.stderrEOF {
		o.incomplete = true
		drainErr = errors.Join(errors.New("stdout/stderr EOF not observed"), o.readErr)
	}
	return o.limited, drainErr
}

func (o *processOutput) snapshot() ([]byte, []byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.stdout), slices.Clone(o.stderr), o.incomplete
}
