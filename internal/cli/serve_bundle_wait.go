package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// awaitBundleInterrupt subscribes to SIGINT FIRST, so neither the listener
// nor the startup output can be exposed before the command handles the signal,
// then runs start (which opens the server, prints readiness and returns its
// close). A start error is returned at once without waiting or closing.
// Otherwise it waits for exactly one SIGINT, closes the server (its error
// ignored), and returns nil so the caller exits 0. A second SIGINT while Close
// is held takes the existing default-action helper. SIGTERM is not subscribed,
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
	go func() {
		<-signals
		sigexit.Die(syscall.SIGINT)
	}()
	_ = close()
	return nil
}
