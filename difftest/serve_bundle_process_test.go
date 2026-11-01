//go:build faultline_test

package difftest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/servefixture"
	"github.com/Mizore66/faultline/internal/proofview"
)

// Actual-process support for the serve-bundle tests. Everything runs the real
// tagged flBinary against a fresh copy of a retained fixture and compares with
// the retained frozen-TS page. Context timeouts are test deadlines only.

const (
	serveBundleStartupTimeout = 30 * time.Second
	serveBundleStepTimeout    = 30 * time.Second
	serveBundleHTTPTimeout    = 120 * time.Second
	serveBundleBanner         = "FaultLine read-only Git proof page: "
	serveBundleStop           = "Press Ctrl+C to stop.\n"

	// Row counts of the retained corpus, restated independently of it.
	serveBundleCLIErrorRows  = 46
	serveBundleCLIServerRows = 22
)

// serveBundleLaunch is one launched serving case with its retained page.
type serveBundleLaunch struct {
	proc *servefixture.Started
	page []byte
	root string
}

func serveBundleFindCase(t *testing.T, corpus *servefixture.Corpus, id string) servefixture.Case {
	t.Helper()
	for _, candidate := range corpus.Cases {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("no retained case %s", id)
	return servefixture.Case{}
}

// startServeBundleCase copies the case's fixture, resolves its retained argv
// against the copy and launches the real fl. Teardown is registered here and
// reports a still-running child; assertions run before it.
func startServeBundleCase(t *testing.T, corpus *servefixture.Corpus, id string) serveBundleLaunch {
	t.Helper()
	return startServeBundleCaseEnv(t, corpus, id, nil, nil)
}

// startServeBundleCaseEnv is startServeBundleCase with the child environment
// and a hook that edits the copied fixture before launch (nil: defaults).
func startServeBundleCaseEnv(t *testing.T, corpus *servefixture.Corpus, id string, env []string, prepare func(root string)) serveBundleLaunch {
	t.Helper()
	c := serveBundleFindCase(t, corpus, id)
	if c.Kind != "cli-serve" {
		t.Fatalf("%s is not a retained cli-serve case", id)
	}
	page, err := corpus.Page(id)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "fixture")
	if err := corpus.CopyFixture(c.Fixture, root); err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(root)
	}
	argv, err := corpus.ResolveArgv(id, root, 0, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	if env == nil {
		env = serveBundleChildEnv(root, "FAULTLINE_NO_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	defer cancel()
	proc, err := servefixture.Start(ctx, flBinary, nil, argv, env, root)
	if err != nil {
		t.Fatalf("starting %s: %v", id, err)
	}
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("teardown of %s: %v", id, err)
		}
	})
	return serveBundleLaunch{proc: proc, page: page, root: root}
}

// serveBundleEnv is the test process environment, which TestMain has already
// made hermetic (HOME, Git configuration).
func serveBundleEnv() []string { return os.Environ() }

// serveBundleChildEnv is the hermetic test environment with PWD pinned to the
// child's directory (so a relative path resolves the way the case declares)
// and extra assignments; it never inherits a stale browser switch.
func serveBundleChildEnv(root string, extra ...string) []string {
	var env []string
	for _, kv := range serveBundleEnv() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "PWD" || name == "FAULTLINE_NO_BROWSER" || name == "CI" || name == "GITHUB_ACTIONS" {
			continue
		}
		env = append(env, kv)
	}
	return append(append(env, "PWD="+root), extra...)
}

// isConnRefused recognizes a refused dial portably: Windows reports
// WSAECONNREFUSED, which is not syscall.ECONNREFUSED, with the text "actively
// refused it".
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused")
}

func stepContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStepTimeout)
	t.Cleanup(cancel)
	return ctx
}

// fetchServeBundlePage issues one GET / with Connection: close over an owned
// raw client and returns the complete response body. Closing the client first
// keeps it from holding the later graceful close.
func fetchServeBundlePage(ctx context.Context, p *servefixture.Started) (*http.Response, []byte, error) {
	conn, err := p.Dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"); err != nil {
		return nil, nil, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	return res, body, err
}

// holdServeBundleConnection proves a connection is ACCEPTED and tracked while
// its request body stays incomplete: it sends complete headers declaring a
// 10-byte body but only 3 bytes, then reads the full response (the server
// answers once the headers are in). A bare Dial would not prove acceptance,
// since the kernel completes the handshake before Accept. The connection is
// owned by p and stays open until released.
func holdServeBundleConnection(ctx context.Context, p *servefixture.Started) (net.Conn, error) {
	conn, err := p.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n\r\nabc"); err != nil {
		return nil, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if err != nil {
		return nil, fmt.Errorf("no complete response on the held connection: %w", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("incomplete response body on the held connection: %w", err)
	}
	if res.StatusCode != 404 || string(body) != "Not found" {
		return nil, fmt.Errorf("held connection answered %d %q, want the source 404 Not found", res.StatusCode, body)
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// holdServeBundlePartial opens an owned client that has sent nothing or only
// part of a request head. Acceptance cannot be read from such a client, so it
// is proven by order instead: the kernel queues connections FIFO and the
// server tracks each one inside Accept, so once a LATER probe connection has
// received a full response, this earlier one has been accepted and tracked.
func holdServeBundlePartial(ctx context.Context, p *servefixture.Started, sent string) (net.Conn, error) {
	conn, err := p.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if sent != "" {
		if _, err := io.WriteString(conn, sent); err != nil {
			return nil, err
		}
	}
	res, body, err := fetchServeBundlePage(ctx, p)
	if err != nil || res.StatusCode != 200 || len(body) == 0 {
		return nil, fmt.Errorf("probe connection after the held client failed: status=%v err=%v", res, err)
	}
	return conn, nil
}

// waitListenerClosed returns once a fresh connection to the bound address is
// refused, the externally observable "listener closed" barrier. Each probe is
// an immediate dial; a probe that connects (the close has not happened yet) is
// dropped at once. The loop is bounded by ctx only.
func waitListenerClosed(ctx context.Context, p *servefixture.Started) error {
	var d net.Dialer
	return probeListenerClosed(ctx, d.DialContext, strings.TrimPrefix(p.URL, "http://"))
}

// probeListenerClosed is waitListenerClosed's loop with the dial injected, so
// the harness policy itself can be tested deterministically. It is test-only
// and never stands in for the server oracle or the held HTTP barrier.
func probeListenerClosed(ctx context.Context, dial func(ctx context.Context, network, address string) (net.Conn, error), address string) error {
	for {
		conn, err := dial(ctx, "tcp4", address)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("listener never closed: %w", ctx.Err())
			}
			if isConnRefused(err) {
				return nil
			}
			// A dial that races the listener's close can fail with ECONNRESET
			// (golang/go#65240). A reset never proves refusal: keep observing
			// on the same bounded cadence until an actual refusal or ctx ends.
			if !errors.Is(err, syscall.ECONNRESET) {
				return fmt.Errorf("unexpected probe failure: %w", err)
			}
		} else {
			conn.Close()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("listener never closed: %w", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// scriptedDial returns a dial function that plays one scripted result per call
// and records the call count. A nil error result hands out an owned net.Pipe
// end whose peer is tracked so the test can prove the probe closed it.
type scriptedDial struct {
	steps []func(cancel context.CancelFunc) (net.Conn, error)
	calls int
	peers []net.Conn
}

func (s *scriptedDial) dial(cancel context.CancelFunc) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, _, _ string) (net.Conn, error) {
		if s.calls >= len(s.steps) {
			return nil, fmt.Errorf("scripted dial called %d times, script has %d steps", s.calls+1, len(s.steps))
		}
		step := s.steps[s.calls]
		s.calls++
		return step(cancel)
	}
}

func scriptedFailure(err error) func(context.CancelFunc) (net.Conn, error) {
	return func(context.CancelFunc) (net.Conn, error) { return nil, err }
}

// Tests only the harness probe loop with scripted dials (never the production
// server or the held HTTP barrier). Kernel close races can make a dial during
// listener close fail with ECONNRESET before the eventual ECONNREFUSED
// (golang/go#65240), so a reset cannot prove refusal and must not abort the
// wait, while any other error still fails at once and only a real refusal
// succeeds.
func TestServeBundleListenerCloseProbe(t *testing.T) {
	refused := fmt.Errorf("dial tcp4 127.0.0.1:1: %w", &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED})
	reset := fmt.Errorf("dial tcp4 127.0.0.1:1: %w", &os.SyscallError{Syscall: "connect", Err: syscall.ECONNRESET})
	if !errors.Is(refused, syscall.ECONNREFUSED) || !errors.Is(reset, syscall.ECONNRESET) {
		t.Fatal("scripted errors do not wrap their errno")
	}
	run := func(t *testing.T, s *scriptedDial) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), serveBundleStepTimeout)
		defer cancel()
		return probeListenerClosed(ctx, s.dial(cancel), "127.0.0.1:1")
	}

	// A probe that still connects (the close has not happened) is closed and the
	// wait continues until a real refusal.
	t.Run("connect-then-refused", func(t *testing.T) {
		s := &scriptedDial{}
		s.steps = []func(context.CancelFunc) (net.Conn, error){
			func(context.CancelFunc) (net.Conn, error) {
				near, far := net.Pipe()
				s.peers = append(s.peers, far)
				return near, nil
			},
			scriptedFailure(refused),
		}
		if err := run(t, s); err != nil {
			t.Fatalf("probe = %v, want nil after the refusal", err)
		}
		if s.calls != 2 {
			t.Fatalf("dial calls = %d, want 2", s.calls)
		}
		for _, far := range s.peers {
			far.SetReadDeadline(time.Now().Add(serveBundleStepTimeout))
			if _, err := far.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Errorf("the connected probe was not closed (peer read: %v)", err)
			}
			far.Close()
		}
	})

	// A reset is not a refusal: it must neither succeed nor fail the wait, and
	// both dials are required before the eventual refusal ends it.
	t.Run("reset-then-refused", func(t *testing.T) {
		s := &scriptedDial{steps: []func(context.CancelFunc) (net.Conn, error){scriptedFailure(reset), scriptedFailure(refused)}}
		if err := run(t, s); err != nil {
			t.Fatalf("probe = %v, want nil after reset then refusal", err)
		}
		if s.calls != 2 {
			t.Fatalf("dial calls = %d, want both the reset and the refusal (2)", s.calls)
		}
	})

	// Resets alone never prove closure: when the context ends the result is
	// that context's error, not success and not an unexpected-failure error.
	t.Run("reset-only-until-canceled", func(t *testing.T) {
		s := &scriptedDial{steps: []func(context.CancelFunc) (net.Conn, error){
			scriptedFailure(reset),
			func(cancel context.CancelFunc) (net.Conn, error) { cancel(); return nil, reset },
		}}
		err := run(t, s)
		if err == nil {
			t.Fatal("probe succeeded on resets only")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("probe = %v, want the canceled context's error", err)
		}
		if s.calls != 2 {
			t.Fatalf("dial calls = %d, want 2", s.calls)
		}
	})

	// Any other error is a real harness problem: fail at once, no second dial.
	t.Run("unexpected-error", func(t *testing.T) {
		boom := errors.New("boom")
		s := &scriptedDial{steps: []func(context.CancelFunc) (net.Conn, error){scriptedFailure(boom), scriptedFailure(refused)}}
		err := run(t, s)
		if !errors.Is(err, boom) {
			t.Fatalf("probe = %v, want the unexpected error", err)
		}
		if s.calls != 1 {
			t.Fatalf("dial calls = %d, want 1 (no retry after an unexpected error)", s.calls)
		}
	})
}

// requireStartupOnly asserts the complete retained-output contract of a server
// that was interrupted: exactly the two startup lines and no stderr.
func requireStartupOnly(t *testing.T, p *servefixture.Started, stdout, stderr string) {
	t.Helper()
	want := serveBundleBanner + p.URL + "\n" + serveBundleStop
	if stdout != want {
		t.Errorf("stdout = %q, want exactly %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty (no stop line, no browser failure)", stderr)
	}
}

// requireCleanInterrupt interrupts a started server and requires a completed
// exit 0 with only the startup lines.
func requireCleanInterrupt(t *testing.T, p *servefixture.Started, helper string) {
	t.Helper()
	if err := p.Interrupt(stepContext(t), helper); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	result, err := p.Wait(stepContext(t))
	if err != nil {
		t.Fatalf("fl did not return after the interrupt: %v", err)
	}
	if result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Errorf("exit code=%v signal=%v, want completed exit 0", result.ExitCode, result.Signal)
	}
	requireStartupOnly(t, p, result.Stdout, result.Stderr)
}

// ---- actual CLI replay of the retained cli-error and cli-serve rows ----

// serveBundleNormalize applies the only permitted normalizations: the case's
// own copied root becomes {ROOT}, and (when given) the harness-chosen port in
// the declared place becomes {PORT}.
func serveBundleNormalize(text, root string) string { return strings.ReplaceAll(text, root, "{ROOT}") }

// runServeBundleCLIRows executes every retained cli-error and cli-serve row as
// its own named subtest and returns how many of each kind it enumerated and ran.
func runServeBundleCLIRows(t *testing.T, corpus *servefixture.Corpus) (errorRows, serverRows, executed int) {
	t.Helper()
	helper := serveBundleConsoleHelper(t)
	for _, c := range corpus.Cases {
		switch c.Kind {
		case "cli-error":
			errorRows++
			t.Run(c.ID, func(t *testing.T) {
				executed++
				replayServeBundleCLIError(t, corpus, c)
			})
		case "cli-serve":
			serverRows++
			t.Run(c.ID, func(t *testing.T) {
				executed++
				replayServeBundleCLIServer(t, corpus, c, helper)
			})
		}
	}
	return
}

// runServeBundleToExit runs fl to completion and returns its raw outputs.
func runServeBundleToExit(t *testing.T, root string, argv []string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, flBinary, argv...)
	cmd.Dir = root
	cmd.Env = serveBundleChildEnv(root, "FAULTLINE_NO_BROWSER=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running fl: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("fl did not exit before the test deadline\nstdout: %q\nstderr: %q", out.String(), errOut.String())
	}
	return out.String(), errOut.String(), cmd.ProcessState.ExitCode()
}

// occupyServeBundlePort holds a loopback listener (never accepted from) so the
// port is genuinely in use; it is released with the test.
func occupyServeBundlePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// Catches any difference in a refusal: wrong message, wrong stream, wrong exit,
// output before a refusal, or a port/attachment error taking precedence over an
// earlier validation. Expected bytes are the frozen-TS capture; only the case
// root and the occupied port are normalized.
func replayServeBundleCLIError(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case) {
	root := filepath.Join(t.TempDir(), "fixture")
	if err := corpus.CopyFixture(c.Fixture, root); err != nil {
		t.Fatal(err)
	}
	port := 0
	if slices.Contains(c.Argv, "{PORT}") {
		port = occupyServeBundlePort(t)
	}
	argv, err := corpus.ResolveArgv(c.ID, root, port, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runServeBundleToExit(t, root, argv)
	normalize := func(text string) string {
		text = serveBundleNormalize(text, root)
		if port != 0 {
			text = strings.ReplaceAll(text, "127.0.0.1:"+strconv.Itoa(port), "127.0.0.1:{PORT}")
		}
		return text
	}
	want := corpus.Goldens[c.ID].Process
	if got := normalize(stdout); got != want.Stdout {
		t.Errorf("stdout\n got %q\nwant %q", got, want.Stdout)
	}
	if got := normalize(stderr); got != want.Stderr {
		t.Errorf("stderr\n got %q\nwant %q", got, want.Stderr)
	}
	if want.ExitCode == nil || exit != *want.ExitCode {
		t.Errorf("exit = %d, want %v", exit, want.ExitCode)
	}
}

// Catches a server that prints the wrong startup lines, serves other bytes than
// the frozen page, or does not return a completed exit 0 after one SIGINT.
func replayServeBundleCLIServer(t *testing.T, corpus *servefixture.Corpus, c servefixture.Case, helper string) {
	launch := startServeBundleCase(t, corpus, c.ID)
	res, body, err := fetchServeBundlePage(stepContext(t), launch.proc)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("GET / = %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if !bytes.Equal(body, launch.page) {
		t.Fatal("served page differs from the retained frozen-TS page: " + serveBundleDiff(body, launch.page))
	}
	if launch.proc.Exited() {
		t.Fatal("fl exited before it was interrupted")
	}
	if err := launch.proc.Interrupt(stepContext(t), helper); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	result, err := launch.proc.Wait(stepContext(t))
	if err != nil {
		t.Fatalf("fl did not return after the interrupt: %v", err)
	}
	if result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		code, signal := "<none>", "<none>"
		if result.ExitCode != nil {
			code = strconv.Itoa(*result.ExitCode)
		}
		if result.Signal != nil {
			signal = *result.Signal
		}
		t.Errorf("exit code=%s signal=%s, want completed exit 0 (stdout %q stderr %q)", code, signal, result.Stdout, result.Stderr)
	}
	want := corpus.Goldens[c.ID].Process
	stdout := serveBundleNormalize(strings.Replace(result.Stdout, launch.proc.URL, "http://127.0.0.1:{PORT}", 1), launch.root)
	if stdout != want.Stdout {
		t.Errorf("stdout\n got %q\nwant %q", stdout, want.Stdout)
	}
	if stderr := serveBundleNormalize(result.Stderr, launch.root); stderr != want.Stderr {
		t.Errorf("stderr\n got %q\nwant %q", stderr, want.Stderr)
	}
}

// subtestFilter reports whether -run selects individual subtests, in which case
// not every enumerated row is expected to execute.
func subtestFilter() bool {
	run := flag.Lookup("test.run")
	return run != nil && strings.Contains(run.Value.String(), "/")
}

// ---- direct real-server HTTP ----

// checkServeBundleDirectHTTP runs the one portable HTTP verifier against the
// real proofview server for views rendered from retained inputs, comparing to
// the retained frozen-TS page.
func checkServeBundleDirectHTTP(t *testing.T, corpus *servefixture.Corpus) {
	t.Helper()
	for _, id := range []string{"view-combination-000", "view-combination-111"} {
		t.Run(id, func(t *testing.T) {
			c := serveBundleFindCase(t, corpus, id)
			page, err := corpus.Page(id)
			if err != nil {
				t.Fatal(err)
			}
			copyRoot := filepath.Join(t.TempDir(), "fixture")
			if err := corpus.CopyFixture(c.Fixture, copyRoot); err != nil {
				t.Fatal(err)
			}
			in := func(p *string) *string {
				if p == nil {
					return nil
				}
				joined := filepath.Join(copyRoot, filepath.FromSlash(*p))
				return &joined
			}
			view, err := proofview.LoadVerifiedGitProofViewWithAttachments(filepath.Join(copyRoot, "bundle"), c.ExpectedRoot, proofview.AttachmentOptions{
				MinimizationFile:           in(c.Options.MinimizationFile),
				ExpectedMinimizationDigest: c.Options.ExpectedMinimizationDigest,
				RepairDirectory:            in(c.Options.RepairDirectory),
				ExpectedRepairDigest:       c.Options.ExpectedRepairDigest,
				PreventionDirectory:        in(c.Options.PreventionDirectory),
				ExpectedPreventionDigest:   c.Options.ExpectedPreventionDigest,
			})
			if err != nil {
				t.Fatal(err)
			}
			server, err := proofview.StartGitProofServer(view, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), serveBundleHTTPTimeout)
			defer cancel()
			checkErr := servefixture.CheckHTTP(ctx, server.URL(), page)
			// CheckHTTP has closed every client it owned, so Close drains.
			if err := server.Close(); err != nil {
				t.Errorf("server close: %v", err)
			}
			if checkErr != nil {
				t.Error(checkErr)
			}
		})
	}
}

// ---- actual CLI HTTP, snapshot, privacy and no-execution ----

// serveBundlePrivateSentinels restates, independently of the corpus metadata,
// the private strings of the coherent fixture that must never reach a page.
var serveBundlePrivateSentinels = []string{
	"state.txt must not be bad",
	"node witness.mjs",
	"FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML",
	"reviewer@example.test",
	"The repository state must not be bad.",
	"state.txt became bad",
	"/tmp/faultline-git-proof-repository-zUChrR",
	"FL_PRIVATE_INVARIANT_5C_NOT_FOR_HTML",
	"FL_PRIVATE_REPAIR_5C_NOT_FOR_HTML",
	"FL_PRIVATE_HARD_5C_NOT_FOR_HTML",
	"FL_PRIVATE_SOFT_5C_NOT_FOR_HTML",
	"FL_PRIVATE_DIAGNOSTIC_5C_NOT_FOR_HTML",
	"witness failed",
	"state=bad",
}

func serveBundleRootListing(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Catches the same HTTP contract diverging between the real server and the
// actual process (headers, framing, routes, limits).
func testServeBundleCLIHTTP(t *testing.T, corpus *servefixture.Corpus, helper string) {
	launch := startServeBundleCase(t, corpus, "cli-serve-111")
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleHTTPTimeout)
	defer cancel()
	if err := servefixture.CheckHTTP(ctx, launch.proc.URL, launch.page); err != nil {
		t.Error(err)
	}
	requireCleanInterrupt(t, launch.proc, helper)
}

// Catches a server that re-reads, re-verifies or re-renders after startup (the
// page must be the one rendered once), exposes any mutation/analysis/rerun
// route, leaks private text, or runs code from the repository it serves.
func testServeBundleRenderOnce(t *testing.T, corpus *servefixture.Corpus, helper string) {
	marker := "executed.marker"
	prepare := func(root string) {
		script := "import { writeFileSync } from \"node:fs\";\nwriteFileSync(new URL(\"./" + marker + "\", import.meta.url), \"ran\");\n"
		pkg := "{\"scripts\":{\"postinstall\":\"node witness.mjs\",\"serve\":\"node witness.mjs\"}}\n"
		for name, data := range map[string]string{"witness.mjs": script, "package.json": pkg} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	launch := startServeBundleCaseEnv(t, corpus, "cli-serve-111", nil, prepare)
	first, body, err := fetchServeBundlePage(stepContext(t), launch.proc)
	if err != nil || first.StatusCode != 200 {
		t.Fatalf("first fetch: %v %v", first, err)
	}
	if !bytes.Equal(body, launch.page) {
		t.Fatal("first page differs from the retained frozen-TS page: " + serveBundleDiff(body, launch.page))
	}
	for _, sentinel := range serveBundlePrivateSentinels {
		if bytes.Contains(body, []byte(sentinel)) {
			t.Errorf("page contains private text %q", sentinel)
		}
	}
	// Remove every copied input the process verified at startup (cli-serve-111
	// names {ROOT}/bundle, {ROOT}/minimization.json and the repair and
	// prevention directories under {ROOT}/.faultline). Each must exist first
	// and be gone afterwards, so the deletion cannot be vacuous.
	for _, name := range []string{"bundle", ".faultline", "minimization.json"} {
		path := filepath.Join(launch.root, name)
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("input %s does not exist before deletion: %v", name, err)
		}
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("input %s still exists after deletion: %v", name, err)
		}
	}
	listing := serveBundleRootListing(t, launch.root)
	_, again, err := fetchServeBundlePage(stepContext(t), launch.proc)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if !bytes.Equal(again, launch.page) {
		t.Fatal("page changed after its inputs changed: " + serveBundleDiff(again, launch.page))
	}
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleHTTPTimeout)
	defer cancel()
	if err := servefixture.CheckHTTP(ctx, launch.proc.URL, launch.page); err != nil {
		t.Error(err)
	}
	requireCleanInterrupt(t, launch.proc, helper)
	if _, err := os.Lstat(filepath.Join(launch.root, marker)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("repository code ran: %s exists (%v)", marker, err)
	}
	if after := serveBundleRootListing(t, launch.root); !slices.Equal(after, listing) {
		t.Errorf("serving changed the served directory: %v -> %v", listing, after)
	}
}

// Catches a command that acts on --serve-ms, --browser or --output (an early
// exit, a browser attempt, a written file). The environment has CI=true so an
// attempted browser open could only print its skipped-in-CI line, never launch
// anything, and that line would break the exact stdout.
func testServeBundleIgnoredOptions(t *testing.T, corpus *servefixture.Corpus, helper string) {
	root := ""
	env := func() []string { return serveBundleChildEnv(root, "CI=true") }
	// The case root is only known after launch; PWD matches the copy path
	// that startServeBundleCaseEnv derives from t.TempDir().
	root = filepath.Join(t.TempDir(), "fixture")
	launch := startServeBundleCaseEnvAt(t, corpus, "cli-serve-ignored-flags", root, env())
	before := serveBundleRootListing(t, launch.root)
	_, body, err := fetchServeBundlePage(stepContext(t), launch.proc)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if !bytes.Equal(body, launch.page) {
		t.Fatal("page differs: " + serveBundleDiff(body, launch.page))
	}
	if launch.proc.Exited() {
		t.Fatal("--serve-ms ended the server")
	}
	requireCleanInterrupt(t, launch.proc, helper)
	if _, err := os.Lstat(filepath.Join(launch.root, "ignored-output")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--output was acted on: %v", err)
	}
	if after := serveBundleRootListing(t, launch.root); !slices.Equal(after, before) {
		t.Errorf("serving changed the directory: %v -> %v", before, after)
	}
}

// startServeBundleCaseEnvAt is startServeBundleCaseEnv with a caller-chosen
// copy path (so the environment can name it before launch).
func startServeBundleCaseEnvAt(t *testing.T, corpus *servefixture.Corpus, id, root string, env []string) serveBundleLaunch {
	t.Helper()
	c := serveBundleFindCase(t, corpus, id)
	page, err := corpus.Page(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := corpus.CopyFixture(c.Fixture, root); err != nil {
		t.Fatal(err)
	}
	argv, err := corpus.ResolveArgv(id, root, 0, c.Argv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), serveBundleStartupTimeout)
	defer cancel()
	proc, err := servefixture.Start(ctx, flBinary, nil, argv, env, root)
	if err != nil {
		t.Fatalf("starting %s: %v", id, err)
	}
	t.Cleanup(func() {
		if err := proc.Dispose(); err != nil {
			t.Errorf("teardown of %s: %v", id, err)
		}
	})
	return serveBundleLaunch{proc: proc, page: page, root: root}
}

// Catches a base-bundle, pair or attachment refusal being replaced by the
// listen error when the requested port is genuinely occupied: the frozen
// order loads and verifies everything before it binds. Expected bytes are the
// retained refusals of the same rows (their --port abc replaced by the busy
// port).
func testServeBundleOccupiedPortPrecedence(t *testing.T, corpus *servefixture.Corpus) {
	for _, id := range []string{"cli-order-pair-before-io", "cli-order-base-before-port", "cli-order-attachment-before-port", "cli-order-later-value-before-pair"} {
		t.Run(id, func(t *testing.T) {
			c := serveBundleFindCase(t, corpus, id)
			root := filepath.Join(t.TempDir(), "fixture")
			if err := corpus.CopyFixture(c.Fixture, root); err != nil {
				t.Fatal(err)
			}
			port := occupyServeBundlePort(t)
			argv, err := corpus.ResolveArgv(id, root, port, c.Argv)
			if err != nil {
				t.Fatal(err)
			}
			replaced := false
			for i, arg := range argv {
				if arg == "abc" {
					argv[i], replaced = strconv.Itoa(port), true
				}
			}
			if !replaced {
				argv = append(argv, "--port", strconv.Itoa(port))
			}
			stdout, stderr, exit := runServeBundleToExit(t, root, argv)
			want := corpus.Goldens[id].Process
			if got := serveBundleNormalize(stdout, root); got != want.Stdout {
				t.Errorf("stdout %q, want %q", got, want.Stdout)
			}
			if got := serveBundleNormalize(stderr, root); got != want.Stderr {
				t.Errorf("stderr\n got %q\nwant %q (an occupied port must not win)", got, want.Stderr)
			}
			if want.ExitCode == nil || exit != *want.ExitCode {
				t.Errorf("exit = %d, want %v", exit, want.ExitCode)
			}
		})
	}
}

// Catches the actual binary claiming plain serve or the unsupported equals form
// instead of keeping the exact prior refusal.
func testServeBundleFallbacksActual(t *testing.T) {
	for _, argv := range [][]string{{"serve"}, {"serve", "--bundle=missing"}, {"serve", "--port", "0"}} {
		root := t.TempDir()
		stdout, stderr, exit := runServeBundleToExit(t, root, argv)
		const want = "FaultLine error: not yet ported: serve\nNext: pnpm fl help\n"
		if stdout != "" || stderr != want || exit != 1 {
			t.Errorf("%q: exit=%d stdout=%q stderr=%q, want exit 1, empty stdout, stderr %q", argv, exit, stdout, stderr, want)
		}
	}
}

// Held connections keep a graceful close open exactly as the source documents,
// and only the owner's release lets the process finish. Each kind is accepted
// and tracked before the interrupt (see the hold helpers), the listener is
// observed closed, the process is still running, and only then is the client
// released.
func testServeBundleHeldClients(t *testing.T, corpus *servefixture.Corpus, helper string) {
	kinds := []struct {
		name string
		hold func(ctx context.Context, p *servefixture.Started) (net.Conn, error)
	}{
		{"silent", func(ctx context.Context, p *servefixture.Started) (net.Conn, error) {
			return holdServeBundlePartial(ctx, p, "")
		}},
		{"partial-header", func(ctx context.Context, p *servefixture.Started) (net.Conn, error) {
			return holdServeBundlePartial(ctx, p, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n")
		}},
		{"incomplete-body", holdServeBundleConnection},
	}
	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			launch := startServeBundleCase(t, corpus, "cli-serve-000")
			if _, err := kind.hold(stepContext(t), launch.proc); err != nil {
				t.Fatalf("holding a tracked client: %v", err)
			}
			if err := launch.proc.Interrupt(stepContext(t), helper); err != nil {
				t.Fatalf("interrupt: %v", err)
			}
			if err := waitListenerClosed(stepContext(t), launch.proc); err != nil {
				t.Fatal(err)
			}
			if launch.proc.Exited() {
				t.Fatal("fl exited while a tracked client was still open")
			}
			if err := launch.proc.CloseClients(); err != nil {
				t.Fatalf("releasing the held client: %v", err)
			}
			result, err := launch.proc.Wait(stepContext(t))
			if err != nil {
				t.Fatalf("fl did not finish after the client was released: %v", err)
			}
			if result.Signal != nil || result.ExitCode == nil || *result.ExitCode != 0 {
				t.Errorf("exit code=%v signal=%v, want completed exit 0", result.ExitCode, result.Signal)
			}
			requireStartupOnly(t, launch.proc, result.Stdout, result.Stderr)
		})
	}
	// An idle keep-alive client (between two requests) is closed by the server
	// itself, so the process finishes with the client still open.
	t.Run("idle-keep-alive", func(t *testing.T) {
		launch := startServeBundleCase(t, corpus, "cli-serve-000")
		ctx := stepContext(t)
		conn, err := launch.proc.Dial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if dl, ok := ctx.Deadline(); ok {
			conn.SetDeadline(dl)
		}
		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(conn)
		res, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || !bytes.Equal(body, launch.page) {
			t.Fatalf("keep-alive page: %v", err)
		}
		requireCleanInterrupt(t, launch.proc, helper)
		if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
			t.Errorf("idle client read %v after shutdown, want the server's EOF", err)
		}
	})
}
