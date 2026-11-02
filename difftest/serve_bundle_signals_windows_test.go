//go:build faultline_test && windows

package difftest

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// serveBundleConsoleHelper builds the test-only console emitter natively into a
// test-owned path. The target is launched with CREATE_NEW_CONSOLE and the
// helper delivers CTRL_C_EVENT to that console by PID.
func serveBundleConsoleHelper(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "console-helper.exe")
	// A test-only build deadline and an explicit repository cwd; not CLI behavior.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", out, "github.com/Mizore66/faultline/difftest/servefixture/console-helper")
	build.Dir = oracle.RepoRoot(t)
	build.WaitDelay = time.Second
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building console helper: %v\n%s", err, combined)
	}
	return out
}

// requireSecondInterruptDeath: Windows has no signal deaths; the raw exit
// status of the default action must be a completed nonzero status. The exact
// value is compared with the native Node oracle by the live signal test; it is
// recorded here in the failure text rather than assumed from POSIX 128+sig.
func requireSecondInterruptDeath(t *testing.T, r servefixture.ProcessResult) {
	t.Helper()
	if r.Signal != nil || r.ExitCode == nil || *r.ExitCode == 0 {
		t.Errorf("exit code=%v signal=%v, want a nonzero raw Windows exit status from the default Ctrl+C action", r.ExitCode, r.Signal)
	}
}

// requireTerminateStatus: Windows has no POSIX SIGTERM. TerminateProcess ends
// the owned handle with a completed raw exit status and no signal; its exact
// value is whatever the native Node comparison in the live test shows (this
// assumption is left to runtime, not asserted as a constant here).
func requireTerminateStatus(t *testing.T, r servefixture.ProcessResult) {
	t.Helper()
	if r.Signal != nil || r.ExitCode == nil {
		t.Errorf("exit code=%v signal=%v, want a completed raw Windows exit status from TerminateProcess", r.ExitCode, r.Signal)
	}
}
