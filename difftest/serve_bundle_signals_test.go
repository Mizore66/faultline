//go:build faultline_test

package difftest

import (
	"bytes"
	"testing"
)

// Actual-process startup and interrupt tests for `fl serve --bundle`. The
// selector `^TestServeBundleStartup(Ordinary|Repeated)Interrupt$` runs them
// alone. Both use the retained cli-serve-000 argv and page of the Task 1
// corpus against the real tagged flBinary (TestMain's, or FAULTLINE_FL_BINARY).

const startupCase = "cli-serve-000"

// TestServeBundleStartupOrdinaryInterrupt catches a binary that never starts
// the bundle server (the not-ported refusal exits before printing a banner),
// serves different bytes than the frozen TS page, prints a stop line or other
// stderr, or does not return exit 0 after one real SIGINT.
func TestServeBundleStartupOrdinaryInterrupt(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	launch := startServeBundleCase(t, corpus, startupCase)
	helper := serveBundleConsoleHelper(t)

	res, body, err := fetchServeBundlePage(stepContext(t), launch.proc)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("GET / status %d", res.StatusCode)
	}
	if !bytes.Equal(body, launch.page) {
		t.Fatal("served page differs from the retained frozen-TS page: " + serveBundleDiff(body, launch.page))
	}

	if err := launch.proc.Interrupt(stepContext(t), helper); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	result, err := launch.proc.Wait(stepContext(t))
	if err != nil {
		t.Fatalf("fl did not return after the interrupt: %v", err)
	}
	if result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Errorf("exit code=%v signal=%v, want completed exit 0", result.ExitCode, result.Signal)
	}
	requireStartupOnly(t, launch.proc, result.Stdout, result.Stderr)
}

// TestServeBundleStartupRepeatedInterrupt catches a server that treats a
// second SIGINT during a held graceful Close like the first (swallowing it or
// finishing cleanly) instead of taking the source default action. The barrier
// is explicit: an owned connection with complete headers and an unfinished
// body is answered (so it is accepted and tracked), the first interrupt
// closes the listener (observed by refusal), the process is still running
// because Close is held by that connection, and only then the second
// interrupt is sent.
func TestServeBundleStartupRepeatedInterrupt(t *testing.T) {
	corpus := loadServeBundleCorpus(t)
	launch := startServeBundleCase(t, corpus, startupCase)
	helper := serveBundleConsoleHelper(t)

	if _, err := holdServeBundleConnection(stepContext(t), launch.proc); err != nil {
		t.Fatalf("holding a tracked connection: %v", err)
	}
	if err := launch.proc.Interrupt(stepContext(t), helper); err != nil {
		t.Fatalf("first interrupt: %v", err)
	}
	if err := waitListenerClosed(stepContext(t), launch.proc); err != nil {
		t.Fatal(err)
	}
	if launch.proc.Exited() {
		t.Fatal("fl exited after the first interrupt although a tracked connection was held open")
	}
	if err := launch.proc.Interrupt(stepContext(t), helper); err != nil {
		t.Fatalf("second interrupt: %v", err)
	}
	result, err := launch.proc.Wait(stepContext(t))
	if err != nil {
		t.Fatalf("fl did not end after the second interrupt: %v", err)
	}
	requireSecondInterruptDeath(t, result)
	requireStartupOnly(t, launch.proc, result.Stdout, result.Stderr)
}
