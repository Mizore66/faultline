//go:build faultline_test && windows

package difftest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// consoleProbeWait is the bounded observation deadline for the target's exit
// after one CTRL_C_EVENT. It is an observation bound, not a delivery rule: a
// timeout is a failure and is never counted as delivery or a pass.
const consoleProbeWait = 5 * time.Second

// TestServeFixtureWindowsConsoleInterrupt is an OBSERVATIONAL diagnostic for the
// hosted Windows failure where the real console helper returns success yet
// every Go serve process stays alive. It runs three sequential variants of the
// same tiny target (difftest/servefixture/console-target) through the same
// launch path (servefixture.Start in its own new console, the existing console
// helper, Wait, Dispose):
//
//   - baseline: ordinary signal.Notify(os.Interrupt), launched before anything
//     in this test touches the test process's console state;
//   - clear-ignore: the identical target with ONLY SetConsoleCtrlHandler(NULL,
//     FALSE) added by the target itself before the subscription;
//   - clear-in-launcher (LAST): the baseline target again, with NO target
//     argument, launched after the TEST PROCESS itself called
//     SetConsoleCtrlHandler(NULL, FALSE). Per the documented semantics the call
//     restores normal Ctrl+C processing for the caller, the attribute is
//     inherited by processes created afterwards and existing processes are not
//     affected.
//
// clear-in-launcher MUTATES THE TEST PROCESS and is process-wide and test-only:
// it must stay last, nothing here is parallel, and no attempt is made to restore
// the prior state (there is no documented getter). The baseline therefore always
// runs before the mutation and its failure is neither masked nor suppressed.
// Tests that run later in the same test binary observe the cleared state, so
// after a failing baseline they cannot independently qualify readiness.
// This is instrumentation to locate where the ignore attribute comes from, not
// the proposed launcher fix and not an acceptance of anything.
//
// Reading the native results (hypotheses, not conclusions): a baseline failure
// with a clear-ignore pass is consistent with an inherited "ignore Ctrl+C"
// attribute but does not by itself prove where it was inherited from. If
// clear-in-launcher then passes, the attribute was held by the test process at
// the time its children were created (it does not say which ancestor set it);
// if it still fails, the attribute is not simply inherited from the test
// process's own state. If baseline and clear-ignore both pass, the symptom did
// not reproduce; fl behavior remains unproven. Each variant must start, deliver the existing
// emitter successfully, exit within the bound with exactly the two startup
// lines, empty stderr, raw exit 0 and no signal. Nothing here skips, accepts
// either outcome, touches the helper, or sends anything but the real
// CTRL_C_EVENT.
func TestServeFixtureWindowsConsoleInterrupt(t *testing.T) {
	helper := serveBundleConsoleHelper(t)
	target := buildConsoleTarget(t)
	targetBytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	helperBytes, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native %s/%s target %s sha256 %s helper %s sha256 %s", runtime.GOOS, runtime.GOARCH,
		target, consoleProbeSHA256Hex(targetBytes), helper, consoleProbeSHA256Hex(helperBytes))
	for _, variant := range []struct {
		name string
		args []string
	}{{"baseline", nil}, {"clear-ignore", []string{"--clear-ignore"}}} {
		// Independent subtests: a failure of the first does not stop the second.
		t.Run(variant.name, func(t *testing.T) { runConsoleProbe(t, variant.name, target, helper, variant.args) })
	}
	// LAST and sequential: it mutates the test process (see the comment above).
	t.Run("clear-in-launcher", func(t *testing.T) {
		setHandler := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
		if ok, _, err := setHandler.Call(0, 0); ok == 0 {
			t.Fatalf("clear-in-launcher: SetConsoleCtrlHandler(NULL, FALSE) failed in the test process: %v", err)
		}
		t.Logf("clear-in-launcher: the TEST PROCESS cleared its own Ctrl+C-ignore attribute (SetConsoleCtrlHandler(NULL, FALSE)) before launching the ordinary baseline target; no --clear-ignore argument")
		runConsoleProbe(t, "clear-in-launcher", target, helper, nil)
	})
}

func consoleProbeSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildConsoleTarget builds the target natively into a test-owned path, with a
// bounded build context like serveBundleConsoleHelper.
func buildConsoleTarget(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "console-target.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", out, "github.com/Mizore66/faultline/difftest/servefixture/console-target")
	build.Dir = oracle.RepoRoot(t)
	build.WaitDelay = time.Second
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building console target: %v\n%s", err, combined)
	}
	return out
}

func runConsoleProbe(t *testing.T, variant, target, helper string, args []string) {
	root := t.TempDir()
	startCtx, cancelStart := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	proc, err := servefixture.Start(startCtx, target, nil, args, serveBundleChildEnv(root), root)
	cancelStart()
	if err != nil {
		t.Fatalf("%s: startup: %v", variant, err)
	}
	// Cleanup kills and joins only this owned child; a forced termination is
	// retained as a failure of the variant.
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("%s: teardown: %v", variant, err)
		}
	})
	t.Logf("%s: started %s", variant, proc.URL)

	interruptCtx, cancelInterrupt := context.WithTimeout(context.Background(), serveBundleStepTimeout)
	err = proc.Interrupt(interruptCtx, helper)
	cancelInterrupt()
	if err != nil {
		t.Fatalf("%s: the console emitter failed: %v", variant, err)
	}
	t.Logf("%s: the console emitter reported success", variant)

	waitCtx, cancelWait := context.WithTimeout(context.Background(), consoleProbeWait)
	defer cancelWait()
	result, err := proc.Wait(waitCtx)
	if err != nil {
		t.Fatalf("%s: the target did not exit within %s of the emitter's success (a timeout is a failure, not delivery): %v", variant, consoleProbeWait, err)
	}
	code, signal := "nil", "nil"
	if result.ExitCode != nil {
		code = strconv.Itoa(*result.ExitCode)
	}
	if result.Signal != nil {
		signal = *result.Signal
	}
	t.Logf("%s: completed raw status code=%s signal=%s stdout=%q stderr=%q", variant, code, signal, result.Stdout, result.Stderr)
	wantStdout := "FaultLine read-only Git proof page: " + proc.URL + "\nPress Ctrl+C to stop.\n"
	if result.Stdout != wantStdout || result.Stderr != "" || result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Errorf("%s: want exactly the two startup lines, empty stderr, raw exit 0 and no signal", variant)
	}
}
