//go:build unix && !linux

package nodeproc

// stkfltSignal and ioAliasSignals are the entries of Node's signo_string
// and os.constants.signals that exist only on some systems: on macOS and
// the BSDs, SIGINFO (29) after SIGIO. Neither lists SIGEMT (7):
// child.kill("SIGEMT") throws ERR_UNKNOWN_SIGNAL, and a child that dies of
// it exits with code 0 and no signal (exitFrom).
var (
	stkfltSignal   []namedSignal
	ioAliasSignals = []namedSignal{{"SIGINFO", 29}}
)
