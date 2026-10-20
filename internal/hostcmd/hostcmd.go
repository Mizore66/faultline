// Package hostcmd is the bounded host-command runner that src/doctor.ts
// (createNodeDoctorRunner) and src/runtime.ts (createNodeDockerImageRunner)
// each define. The two TS runners are the same apart from the output cap
// and how an 'error' event's message joins stderr, which the caller picks
// through Options. (Their catch blocks also differ in the fallback message
// for a thrown non-Error, which cannot happen: Node's spawn, and
// nodeproc.Spawn, only throw Errors.)
package hostcmd

import (
	"sync"
	"time"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// Options are the parts of the TS runners that differ by call site.
type Options struct {
	// Timeout is command.timeoutMs: once it passes, TimedOut is set and
	// the child is sent SIGKILL.
	Timeout time.Duration
	// MaxOutputBytes caps the UTF-8 length of stdout plus stderr
	// (DOCTOR_MAX_OUTPUT_BYTES, or runtime's per-command
	// DOCKER_IMAGE_*_MAX_OUTPUT_BYTES).
	MaxOutputBytes int
	// ErrorJoin goes between stderr and an 'error' event's message when
	// stderr is not empty. Runtime uses "\n"
	// (`${stderr}${stderr ? "\n" : ""}${error.message}`); doctor uses ""
	// (`stderr += error.message`).
	ErrorJoin string
}

// Result is DoctorCommandResult / DockerImageInspectResult.
type Result struct {
	ExitCode            *int // nil is JS null: killed by a signal, or never started
	Stdout, Stderr      string
	TimedOut            bool
	OutputLimitExceeded bool
}

// Run ports the run method of src/doctor.ts:767 createNodeDoctorRunner and
// of src/runtime.ts:917 createNodeDockerImageRunner: spawn(file, args,
// { shell: false, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] })
// with the inherited environment and cwd. Each chunk is decoded on its own
// (a code point split across chunks becomes U+FFFD) and kept up to what is
// left of MaxOutputBytes, measured on the decoded strings; a chunk that
// does not fit sets OutputLimitExceeded and SIGKILLs the child. A spawn
// that throws replaces stderr with the message; an 'error' event appends
// its message (ErrorJoin); either finishes with a nil ExitCode. Otherwise
// 'close' finishes with the exit code (nil after a signal). The error is
// non-nil only where the TS promise would reject, which it never does.
func Run(file string, args []string, o Options) (Result, error) {
	var (
		mu       sync.Mutex
		settled  bool
		timedOut bool
		overflow bool
		stdout   string
		stderr   string
		child    *nodeproc.Child
		timer    *time.Timer
	)
	// finish is the TS finish: only the first call counts. Stopping the
	// timer under mu is clearTimeout: a callback already waiting on mu
	// sees settled and does nothing, as a cleared Node timer never fires.
	var result Result
	finish := func(exitCode *int) {
		if settled {
			return
		}
		settled = true
		if timer != nil {
			timer.Stop()
		}
		result = Result{ExitCode: exitCode, Stdout: stdout, Stderr: stderr, TimedOut: timedOut, OutputLimitExceeded: overflow}
	}
	appendOutput := func(toStdout bool, chunk []byte) {
		mu.Lock()
		defer mu.Unlock()
		used := len(stdout) + len(stderr) // Buffer.byteLength of DecodeUTF8 output
		remaining := o.MaxOutputBytes - used
		if remaining <= 0 {
			overflow = true
			child.Kill("SIGKILL")
			return
		}
		retained := jsstr.DecodeUTF8(chunk[:min(len(chunk), remaining)])
		if toStdout {
			stdout += retained
		} else {
			stderr += retained
		}
		if len(chunk) > remaining {
			overflow = true
			child.Kill("SIGKILL")
		}
	}

	thrown := jsexc.Try(func() {
		// The listeners run on one goroutine, but the first chunk can
		// arrive before Spawn returns: hold the lock until child and the
		// timer are set, as Node runs listeners only after this tick.
		mu.Lock()
		defer mu.Unlock()
		child = nodeproc.Spawn(file, args, nodeproc.SpawnOptions{
			Stdout:   nodeproc.StdioPipe,
			Stderr:   nodeproc.StdioPipe,
			OnStdout: func(chunk []byte) { appendOutput(true, chunk) },
			OnStderr: func(chunk []byte) { appendOutput(false, chunk) },
		})
		timer = time.AfterFunc(o.Timeout, func() {
			mu.Lock()
			defer mu.Unlock()
			if settled {
				return // cleared
			}
			timedOut = true
			child.Kill("SIGKILL")
		})
	})
	if thrown != nil {
		mu.Lock()
		defer mu.Unlock()
		stderr = jsexc.Message(thrown) // error instanceof Error
		finish(nil)
		return result, nil
	}
	exit := child.Wait()
	mu.Lock()
	defer mu.Unlock()
	if exit.Err != nil {
		if stderr != "" {
			stderr += o.ErrorJoin
		}
		stderr += exit.Err.Error()
		finish(nil)
		return result, nil
	}
	finish(exit.Code)
	return result, nil
}
