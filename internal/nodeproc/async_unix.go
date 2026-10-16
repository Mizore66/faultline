//go:build unix

package nodeproc

import (
	"os"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// newAsyncStdio is uv__process_init_stdio for spawn's stdio array: a
// socketpair for each "pipe" slot, and for "ignore" /dev/null, which the
// child opens read-only for stdin and read-write otherwise.
func newAsyncStdio(o SpawnOptions) (*asyncStdio, error) {
	s := &asyncStdio{child: make([]*os.File, 0, 3)}
	parents := []**os.File{&s.stdin, &s.stdout, &s.stderr}
	for i, piped := range []bool{o.StdinPipe, o.Stdout == StdioPipe, o.Stderr == StdioPipe} {
		var c *os.File
		var err error
		if piped {
			*parents[i], c, err = socketPair()
		} else {
			flag := os.O_RDWR
			if i == 0 {
				flag = os.O_RDONLY
			}
			c, err = os.OpenFile(os.DevNull, flag, 0)
		}
		if err != nil {
			s.closeChild()
			s.closeParent()
			return nil, err
		}
		s.child = append(s.child, c)
	}
	return s, nil
}

// envPairs is the envPairs normalizeSpawnArguments builds from an env
// object: one "key=value" per own property, in order.
func envPairs(env []string) []string { return env }

// killProcess is uv_process_kill: kill(pid, sig).
func killProcess(p *os.Process, sig int) bool {
	return p.Signal(syscall.Signal(sig)) == nil
}

// exitFrom is ChildProcess's onexit for a reaped child: an exit code, or
// the name of the signal that killed it (signo_string). A signal
// signo_string has no name for (a real-time signal, SIGEMT on macOS) gives
// "", which onexit takes as no signal, so the child reports libuv's
// exit_status, 0. A child killed by a signal fl did not send is reported to
// sigexit, as SpawnSync does.
func exitFrom(state *os.ProcessState, sent bool, _ string) Exit {
	if state == nil {
		return Exit{}
	}
	ws, ok := state.Sys().(syscall.WaitStatus)
	if ok && ws.Signaled() {
		if !sent {
			sigexit.ChildKilled(ws.Signal())
		}
		if name := signalName(int(ws.Signal())); name != "" {
			return Exit{Signal: name}
		}
		zero := 0
		return Exit{Code: &zero}
	}
	code := state.ExitCode()
	return Exit{Code: &code}
}

// signalName is node's signo_string: the first name in its case list for
// the number, "" when none matches.
func signalName(sig int) string {
	for _, s := range signalList {
		if s.num == sig {
			return s.name
		}
	}
	return ""
}

type namedSignal struct {
	name string
	num  int
}

// signalList is signo_string's order (aliases such as SIGIOT come after
// the name they share a number with); signalNumbers is
// os.constants.signals, which child.kill looks names up in.
var signalList, signalNumbers = func() ([]namedSignal, map[string]int) {
	list := []namedSignal{
		{"SIGHUP", int(syscall.SIGHUP)}, {"SIGINT", int(syscall.SIGINT)}, {"SIGQUIT", int(syscall.SIGQUIT)},
		{"SIGILL", int(syscall.SIGILL)}, {"SIGTRAP", int(syscall.SIGTRAP)}, {"SIGABRT", int(syscall.SIGABRT)},
		{"SIGIOT", int(syscall.SIGIOT)}, {"SIGBUS", int(syscall.SIGBUS)}, {"SIGFPE", int(syscall.SIGFPE)},
		{"SIGKILL", int(syscall.SIGKILL)}, {"SIGUSR1", int(syscall.SIGUSR1)}, {"SIGSEGV", int(syscall.SIGSEGV)},
		{"SIGUSR2", int(syscall.SIGUSR2)}, {"SIGPIPE", int(syscall.SIGPIPE)}, {"SIGALRM", int(syscall.SIGALRM)},
		{"SIGTERM", int(syscall.SIGTERM)}, {"SIGCHLD", int(syscall.SIGCHLD)},
	}
	list = append(list, stkfltSignal...)
	list = append(list,
		namedSignal{"SIGCONT", int(syscall.SIGCONT)}, namedSignal{"SIGSTOP", int(syscall.SIGSTOP)},
		namedSignal{"SIGTSTP", int(syscall.SIGTSTP)}, namedSignal{"SIGTTIN", int(syscall.SIGTTIN)},
		namedSignal{"SIGTTOU", int(syscall.SIGTTOU)}, namedSignal{"SIGURG", int(syscall.SIGURG)},
		namedSignal{"SIGXCPU", int(syscall.SIGXCPU)}, namedSignal{"SIGXFSZ", int(syscall.SIGXFSZ)},
		namedSignal{"SIGVTALRM", int(syscall.SIGVTALRM)}, namedSignal{"SIGPROF", int(syscall.SIGPROF)},
		namedSignal{"SIGWINCH", int(syscall.SIGWINCH)}, namedSignal{"SIGIO", int(syscall.SIGIO)},
	)
	list = append(list, ioAliasSignals...)
	list = append(list, namedSignal{"SIGSYS", int(syscall.SIGSYS)})
	numbers := make(map[string]int, len(list))
	for _, s := range list {
		numbers[s.name] = s.num
	}
	return list, numbers
}()

// closeRaw is only needed on Windows (raw pipe handles).
func closeRaw(uintptr) {}
