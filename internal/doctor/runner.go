package doctor

import (
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
)

// Runner runs one fixed doctor command (src/doctor.ts DoctorCommandRunner):
// the executable and every argument come from this package, never from the
// user. Tests inject one; NodeRunner is the production runner.
type Runner func(exe string, args []string, timeout time.Duration, maxBytes int) hostcmd.Result

// NodeRunner ports src/doctor.ts:767 createNodeDoctorRunner: hostcmd.Run with
// the inherited environment and cwd, the caller's timeout and output cap,
// and an 'error' event's message appended to stderr with nothing between
// (`stderr += error.message`).
func NodeRunner() Runner {
	return func(exe string, args []string, timeout time.Duration, maxBytes int) hostcmd.Result {
		r, err := hostcmd.Run(exe, args, hostcmd.Options{Timeout: timeout, MaxOutputBytes: maxBytes, ErrorJoin: ""})
		if err != nil {
			return hostcmd.Result{} // a rejection, which runCommand turns into this
		}
		return r
	}
}

// runCommand ports src/doctor.ts:199 runCommand: a runner that throws gives
// a result with no exit code and no output, so nothing rejects.
func runCommand(run Runner, exe string, args []string) hostcmd.Result {
	var r hostcmd.Result
	if err := jsexc.Try(func() { r = run(exe, args, CommandTimeout, MaxOutputBytes) }); err != nil {
		return hostcmd.Result{}
	}
	return r
}
