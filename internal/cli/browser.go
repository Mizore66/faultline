package cli

import (
	"io"
	"math"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/sigexit"
)

// shouldAutoOpenBrowser ports src/cli-app.ts:879.
func shouldAutoOpenBrowser(getenv func(string) string) bool {
	if getenv("FAULTLINE_NO_BROWSER") == "1" {
		return false
	}
	if ci := getenv("CI"); ci == "true" || ci == "1" {
		return false
	}
	return getenv("GITHUB_ACTIONS") != "true"
}

// processEnv is process.env[name] (undefined compares like "").
func processEnv(name string) string {
	v, _ := nodeproc.Getenv(name)
	return v
}

// openLocalDemoURL ports src/cli-app.ts:886 openLocalDemoUrl. exec() runs
// the command through the shell asynchronously; its callback reports a
// failure (a spawn error, a non-zero exit or a signal) on stderr, if fl is
// still running then.
func openLocalDemoURL(e *env, url string) {
	if !shouldAutoOpenBrowser(processEnv) {
		e.out("Browser auto-open skipped in CI. Open " + url + " manually if needed.\n")
		return
	}
	var command string
	switch runtime.GOOS {
	case "windows":
		command = `cmd /c start "" "` + url + `"`
	case "darwin":
		command = `open "` + url + `"`
	default:
		command = `xdg-open "` + url + `"`
	}
	failed := func() {
		io.WriteString(e.stderr, jsstr.ToUTF8("FaultLine could not open a browser automatically. Open "+url+" manually.\n"))
	}
	var wait func() bool
	if err := jsexc.Try(func() { wait = startShell(command) }); err != nil {
		// exec() reports a synchronous spawn failure through its callback too.
		go failed()
		return
	}
	go func() {
		if wait() {
			failed()
		}
	}()
}

// awaitServeInterrupt ports src/cli-app.ts:904: wait for SIGINT, SIGTERM or
// the --serve-ms timer, then close (its error ignored) and onStop (a panic
// swallowed); the caller then exits 0. Node's process.once listeners stay
// until fl exits here: a second SIGINT or SIGTERM while closing is ignored
// (in Node a second identical signal would take the default action;
// KNOWN_DIFFERENCES.md).
func awaitServeInterrupt(close func() error, serveMs *float64, onStop func()) {
	signals := make(chan os.Signal, 2)
	sigexit.Intercept(syscall.SIGINT)
	sigexit.Intercept(syscall.SIGTERM)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var timer <-chan time.Time
	if serveMs != nil && !math.IsInf(*serveMs, 0) && !math.IsNaN(*serveMs) && *serveMs >= 0 {
		timer = time.After(timeoutDuration(*serveMs))
	}
	select {
	case <-signals:
	case <-timer:
	}
	close()
	func() {
		defer func() { recover() }() // Keep the verified summary path fail-open on stop.
		onStop()
	}()
}

// timeoutDuration is setTimeout's delay: below 1 ms or above 2^31-1 ms it
// is 1 ms (Node also prints a TimeoutOverflowWarning for the latter, with
// its pid; not ported, KNOWN_DIFFERENCES.md).
func timeoutDuration(ms float64) time.Duration {
	if !(ms >= 1 && ms <= math.MaxInt32) {
		ms = 1
	}
	return time.Duration(ms * float64(time.Millisecond))
}
