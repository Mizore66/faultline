package nodeproc

import (
	"errors"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/jsexc"
)

// newAsyncStdio is uv__stdio_create for spawn's stdio array: a pipe with
// 64 KiB buffers for each "pipe" slot, and for "ignore" the NUL device,
// which libuv opens for the first three slots whatever is asked (stdin
// readable, stdout and stderr writable).
func newAsyncStdio(o SpawnOptions) (*asyncStdio, error) {
	s := &asyncStdio{child: make([]*os.File, 0, 3)}
	parents := []**os.File{&s.stdin, &s.stdout, &s.stderr}
	for i, piped := range []bool{o.StdinPipe, o.Stdout == StdioPipe, o.Stderr == StdioPipe} {
		var c *os.File
		var err error
		switch {
		case piped && i == 0:
			c, *parents[i], err = pipe64k()
		case piped:
			// libuv's uv__create_stdio_pipe_pair: an overlapped named
			// pipe server for fl, read through the loop's completion
			// port (pump_windows.go), and a blocking client for the child.
			var server uintptr
			server, c, err = overlappedPipe()
			if err == nil {
				if i == 1 {
					s.rawStdout = server
				} else {
					s.rawStderr = server
				}
			}
		case i == 0:
			c, err = os.OpenFile(os.DevNull, os.O_RDONLY, 0)
		default:
			c, err = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		}
		if err != nil {
			s.closeChild()
			s.closeParent()
			return nil, err
		}
		s.child = append(s.child, c)
	}
	if s.rawStdout != 0 || s.rawStderr != 0 {
		port, err := newCompletionPort(s.rawStdout, s.rawStderr)
		if err != nil {
			s.closeChild()
			s.closeParent()
			return nil, err
		}
		s.port = port
	}
	return s, nil
}

// envPairs is normalizeSpawnArguments on Windows: the env object's keys
// sorted, those that uppercase alike reduced to the first, each with its
// value.
func envPairs(env []string) []string {
	values := make(map[string]string, len(env))
	names := make([]string, 0, len(env))
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, ok := values[key]; !ok {
			names = append(names, key)
		}
		values[key] = value
	}
	pairs := make([]string, 0, len(names))
	for _, name := range windowsEnvDedupe(names) {
		pairs = append(pairs, name+"="+values[name])
	}
	return pairs
}

// killProcess is uv_process_kill on Windows: SIGQUIT, SIGTERM, SIGKILL and
// SIGINT terminate the process (exit code 1, reported as the signal);
// anything else is UV_ENOSYS, which child.kill throws.
func killProcess(p *os.Process, sig int) bool {
	switch sig {
	case 2, 3, 9, 15:
		return p.Kill() == nil
	}
	jsexc.Throw(errors.New("kill ENOSYS"))
	return false
}

// exitFrom is ChildProcess's onexit: libuv reports the signal a successful
// kill sent, in place of the exit code.
func exitFrom(state *os.ProcessState, sent bool, killed string) Exit {
	if state == nil {
		return Exit{}
	}
	if sent {
		return Exit{Signal: killed}
	}
	code := state.ExitCode()
	return Exit{Code: &code}
}

// signalNumbers is os.constants.signals on Windows.
var signalNumbers = map[string]int{
	"SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, "SIGILL": 4, "SIGABRT": 22, "SIGFPE": 8, "SIGKILL": 9,
	"SIGSEGV": 11, "SIGTERM": 15, "SIGBREAK": 21, "SIGWINCH": 28,
}
