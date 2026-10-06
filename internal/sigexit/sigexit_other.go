//go:build !unix

package sigexit

import (
	"os"
	"syscall"
)

// Die exits with 128+sig; Windows has no signal deaths.
func Die(sig syscall.Signal) {
	dying.Store(true)
	os.Exit(128 + int(sig))
}

// RecordIgnored is only needed where Die resets dispositions through
// os/signal.
func RecordIgnored() {}
