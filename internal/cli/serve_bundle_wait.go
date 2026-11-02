package cli

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// awaitBundleInterrupt subscribes to SIGINT FIRST, so neither the listener
// nor the startup output can be exposed before the command handles the signal,
// then runs start (which opens the server, prints readiness and returns its
// close). A start error is returned at once without waiting or closing.
// Otherwise it waits for exactly one SIGINT, closes the server (its error
// ignored), and returns nil so the caller exits 0. A second SIGINT while Close
// is held takes the existing default-action helper (Unix) or, on Windows, the
// console's real default action after the subscription is reset. SIGTERM is not subscribed,
// there is no timer, browser or stop line.
func awaitBundleInterrupt(start func() (func() error, error)) error {
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, os.Interrupt)
	sigexit.Intercept(syscall.SIGINT)
	close, err := start()
	if err != nil {
		return err
	}
	<-signals
	if runtime.GOOS == "windows" {
		// Node's listener is process.once("SIGINT"): after it has run, the next
		// Ctrl+C reaches the console's default handler, which ends the process
		// with the system's own status. Release the subscription before Close
		// so a second event during Close takes that real default action; the
		// numeric-exit helper below would report a different status there.
		signal.Reset(os.Interrupt)
	} else {
		go func() {
			<-signals
			sigexit.Die(syscall.SIGINT)
		}()
	}
	_ = close()
	return nil
}
