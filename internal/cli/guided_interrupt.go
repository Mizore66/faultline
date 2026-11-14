package cli

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// guidedInterruptSignal is the one-shot SIGINT lifecycle of guided
// investigate (Node's process.once("SIGINT") that aborts only the freeze
// waiter). The returned context is cancelled by the first delivered SIGINT,
// which first unregisters this listener, so a later SIGINT has its ordinary
// disposition. It never handles SIGTERM and adds no timeout. cleanup is
// idempotent: it stops the listener and joins the owned goroutine. The caller
// defers it, which keeps the registration through binding and output.
//
// Boot's permanent SIGINT filter leaves an intercepted signal to the command
// and sigexit cannot withdraw that, so on Unix the next SIGINT after the first
// takes the default action (death by SIGINT) here, as awaitBundleInterrupt
// does. This helper's own channel is released before it, so it is notified
// only once. On Windows the unregistered listener leaves the console's real
// default handler, and no process-wide reset is done.
func guidedInterruptSignal() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	stop := make(chan struct{})
	joined := make(chan struct{})
	signal.Notify(signals, os.Interrupt)
	sigexit.Intercept(syscall.SIGINT)
	go func() {
		defer close(joined)
		select {
		case <-signals:
			var second chan os.Signal
			if runtime.GOOS != "windows" {
				// Registered before the first listener is released: no gap.
				second = make(chan os.Signal, 1)
				signal.Notify(second, os.Interrupt)
			}
			signal.Stop(signals)
			cancel()
			if second == nil {
				return
			}
			defer signal.Stop(second)
			select {
			case <-second:
				sigexit.Die(syscall.SIGINT)
			case <-stop:
			}
		case <-stop:
			signal.Stop(signals)
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			close(stop)
			<-joined
			cancel()
		})
	}
}
