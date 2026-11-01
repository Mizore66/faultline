//go:build faultline_test

package difftest

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/servefixture"
)

// signalCase is the retained serving row every signal case launches.
const signalCase = "cli-serve-111"

// signalPair is the concrete TS and Go processes on independent copies of the
// identical retained fixture, both proven ready by the exact retained page.
type signalPair struct {
	sides []liveSide
	procs []*servefixture.Started
	roots []string
	page  []byte
}

// startSignalPair launches both sides (optionally behind a wrapper that execs
// the real command, keeping the same PID) and proves readiness by fetching the
// full retained page from each. wrap may be nil.
func startSignalPair(t *testing.T, corpus *servefixture.Corpus, node, cli string, wrap func(liveSide) liveSide) *signalPair {
	t.Helper()
	c := serveBundleFindCase(t, corpus, signalCase)
	page, err := corpus.Page(signalCase)
	if err != nil {
		t.Fatal(err)
	}
	pair := &signalPair{page: page}
	for _, side := range []liveSide{{"TS", node, []string{cli}}, {"Go", flBinary, nil}} {
		root := liveCopy(t, corpus, c)
		argv, err := corpus.ResolveArgv(signalCase, root, 0, c.Argv)
		if err != nil {
			t.Fatal(err)
		}
		launched := side
		if wrap != nil {
			launched = wrap(side)
		}
		ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
		proc, err := servefixture.Start(ctx, launched.exe, launched.prefix, argv, serveBundleChildEnv(root, "FAULTLINE_NO_BROWSER=1"), root)
		cancel()
		if err != nil {
			t.Fatalf("%s: starting %s: %v", signalPlatform(), side.name, err)
		}
		t.Cleanup(func() {
			if err := proc.Dispose(); err != nil {
				t.Errorf("teardown of %s: %v", side.name, err)
			}
		})
		_, body, err := fetchServeBundlePage(stepContext(t), proc)
		if err != nil || !bytes.Equal(body, page) {
			t.Fatalf("%s: %s not ready with the exact retained page (err=%v)", signalPlatform(), side.name, err)
		}
		pair.sides, pair.procs, pair.roots = append(pair.sides, side), append(pair.procs, proc), append(pair.roots, root)
	}
	return pair
}

func signalPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// signalStatus is one side's complete outcome with the declared normalization.
type signalStatus struct {
	stdout, stderr string
	code           *int
	signal         *string
}

func (s signalStatus) String() string {
	code, sig := "nil", "nil"
	if s.code != nil {
		code = fmt.Sprint(*s.code)
	}
	if s.signal != nil {
		sig = *s.signal
	}
	return fmt.Sprintf("code=%s signal=%s stdout=%q stderr=%q", code, sig, s.stdout, s.stderr)
}

func (p *signalPair) wait(t *testing.T, i int) (signalStatus, servefixture.ProcessResult) {
	t.Helper()
	res, err := p.procs[i].Wait(stepContext(t))
	if err != nil {
		t.Fatalf("%s: %s did not end: %v", signalPlatform(), p.sides[i].name, err)
	}
	stdout := serveBundleNormalize(strings.Replace(res.Stdout, p.procs[i].URL, "http://127.0.0.1:{PORT}", 1), p.roots[i])
	return signalStatus{stdout, serveBundleNormalize(res.Stderr, p.roots[i]), res.ExitCode, res.Signal}, res
}

// requireAgree compares the two sides' raw native status and complete streams,
// and that both streams are exactly the two startup lines (no stop line).
func (p *signalPair) requireAgree(t *testing.T, ts, goSide signalStatus) {
	t.Helper()
	wantStdout := "FaultLine read-only Git proof page: http://127.0.0.1:{PORT}\nPress Ctrl+C to stop.\n"
	for i, s := range []signalStatus{ts, goSide} {
		if s.stdout != wantStdout || s.stderr != "" {
			t.Errorf("%s %s streams: %s", signalPlatform(), p.sides[i].name, s)
		}
	}
	same := ts.stdout == goSide.stdout && ts.stderr == goSide.stderr && equalInt(ts.code, goSide.code) && equalString(ts.signal, goSide.signal)
	if !same {
		t.Errorf("%s Go differs from TS\nTS: %s\nGo: %s", signalPlatform(), ts, goSide)
	}
}

func equalInt(a, b *int) bool       { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func equalString(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// each applies an action to both sides in order (TS then Go).
func (p *signalPair) each(t *testing.T, phase string, f func(i int, proc *servefixture.Started) error) {
	t.Helper()
	for i, proc := range p.procs {
		if err := f(i, proc); err != nil {
			t.Fatalf("%s %s %s: %v", signalPlatform(), p.sides[i].name, phase, err)
		}
	}
}

// holdAndFirstInterrupt runs, for ONE side, the accepted-held-connection
// barrier (complete headers, incomplete body, complete response), the first
// interrupt, the listener refusal and the check that the process is still
// running because of the held client. The server arms an inactivity timer once
// a response is written, so each side's whole held sequence must run
// back-to-back, never with the other side's setup or signals in between.
func (p *signalPair) holdAndFirstInterrupt(t *testing.T, i int, helper string) {
	t.Helper()
	proc := p.procs[i]
	fail := func(phase string, err error) {
		t.Helper()
		t.Fatalf("%s %s %s: %v", signalPlatform(), p.sides[i].name, phase, err)
	}
	if _, err := holdServeBundleConnection(stepContext(t), proc); err != nil {
		fail("hold", err)
	}
	if err := proc.Interrupt(stepContext(t), helper); err != nil {
		fail("first interrupt", err)
	}
	if err := waitListenerClosed(stepContext(t), proc); err != nil {
		fail("listener refusal", err)
	}
	if proc.Exited() {
		fail("still running", fmt.Errorf("exited after the first interrupt while a tracked client was held"))
	}
}

// heldSequence runs one side's entire held sequence, then the final signal
// (second interrupt or terminate), and returns that side's outcome before the
// other side's client is held.
func (p *signalPair) heldSequence(t *testing.T, i int, helper string, final func(*servefixture.Started) error) (signalStatus, servefixture.ProcessResult) {
	t.Helper()
	p.holdAndFirstInterrupt(t, i, helper)
	if err := final(p.procs[i]); err != nil {
		t.Fatalf("%s %s final signal: %v", signalPlatform(), p.sides[i].name, err)
	}
	return p.wait(t, i)
}

// runSignalMatrix runs the four named cases against the pair factory.
func runSignalMatrix(t *testing.T, corpus *servefixture.Corpus, node, cli, helper string, wrap func(liveSide) liveSide) {
	t.Run("ordinary-interrupt", func(t *testing.T) {
		p := startSignalPair(t, corpus, node, cli, wrap)
		p.each(t, "interrupt", func(i int, proc *servefixture.Started) error { return proc.Interrupt(stepContext(t), helper) })
		ts, tsRaw := p.wait(t, 0)
		goSide, goRaw := p.wait(t, 1)
		for i, r := range []servefixture.ProcessResult{tsRaw, goRaw} {
			if r.Signal != nil || r.ExitCode == nil || *r.ExitCode != 0 {
				t.Errorf("%s %s: code=%v signal=%v, want completed exit 0", signalPlatform(), p.sides[i].name, r.ExitCode, r.Signal)
			}
		}
		p.requireAgree(t, ts, goSide)
	})
	t.Run("held-second-interrupt", func(t *testing.T) {
		p := startSignalPair(t, corpus, node, cli, wrap)
		second := func(proc *servefixture.Started) error { return proc.Interrupt(stepContext(t), helper) }
		ts, tsRaw := p.heldSequence(t, 0, helper, second)
		goSide, goRaw := p.heldSequence(t, 1, helper, second)
		requireSecondInterruptDeath(t, tsRaw)
		requireSecondInterruptDeath(t, goRaw)
		p.requireAgree(t, ts, goSide)
	})
	t.Run("default-terminate", func(t *testing.T) {
		p := startSignalPair(t, corpus, node, cli, wrap)
		p.each(t, "terminate", func(i int, proc *servefixture.Started) error { return proc.Terminate(stepContext(t)) })
		ts, tsRaw := p.wait(t, 0)
		goSide, goRaw := p.wait(t, 1)
		requireTerminateStatus(t, tsRaw)
		requireTerminateStatus(t, goRaw)
		p.requireAgree(t, ts, goSide)
	})
	t.Run("held-then-terminate", func(t *testing.T) {
		p := startSignalPair(t, corpus, node, cli, wrap)
		terminate := func(proc *servefixture.Started) error { return proc.Terminate(stepContext(t)) }
		ts, tsRaw := p.heldSequence(t, 0, helper, terminate)
		goSide, goRaw := p.heldSequence(t, 1, helper, terminate)
		requireTerminateStatus(t, tsRaw)
		requireTerminateStatus(t, goRaw)
		p.requireAgree(t, ts, goSide)
	})
}

// TestLiveServeBundleSignals compares native Node and Go for ordinary and
// repeated interrupts and for default termination, with and without a held
// accepted client. A Go server that swallows the second interrupt, hangs, runs
// the default action with a different raw status than Node, prints a stop line
// or subscribes to SIGTERM fails here. Barriers are explicit: complete retained
// page (readiness), a complete response on an incomplete-body connection
// (accepted), a refused fresh dial (listener closed) and a still-running
// process; no sleep or timer alignment. Windows runs in an isolated new console
// with CTRL_C_EVENT; its raw statuses are compared natively, never assumed.
func TestLiveServeBundleSignals(t *testing.T) {
	oracle.Skip(t)
	node, cli := pinServeBundleOracle(t)
	corpus := loadServeBundleCorpus(t)
	helper := serveBundleConsoleHelper(t)
	runSignalMatrix(t, corpus, node, cli, helper, nil)
}
