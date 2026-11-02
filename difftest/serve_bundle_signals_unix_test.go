//go:build faultline_test && unix

package difftest

import (
	"testing"

	"github.com/Mizore66/faultline/difftest/servefixture"
)

// serveBundleConsoleHelper: POSIX signals need no console emitter.
func serveBundleConsoleHelper(t *testing.T) string { return "" }

// requireSecondInterruptDeath: the second SIGINT during a held Close takes the
// default action, a raw SIGINT death with no exit code (never 130 by shell
// arithmetic).
func requireSecondInterruptDeath(t *testing.T, r servefixture.ProcessResult) {
	t.Helper()
	if r.Signal == nil || *r.Signal != "SIGINT" || r.ExitCode != nil {
		t.Errorf("exit code=%v signal=%v, want raw SIGINT death and no exit code", r.ExitCode, r.Signal)
	}
}

// requireTerminateStatus: SIGTERM with no listener takes the default action, a
// raw SIGTERM death with no exit code.
func requireTerminateStatus(t *testing.T, r servefixture.ProcessResult) {
	t.Helper()
	if r.Signal == nil || *r.Signal != "SIGTERM" || r.ExitCode != nil {
		t.Errorf("exit code=%v signal=%v, want raw SIGTERM death and no exit code", r.ExitCode, r.Signal)
	}
}
