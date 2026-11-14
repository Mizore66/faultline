package servefixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Test support for driving the real fl executable. Nothing here is production
// behavior and cmd/fl does not import it. Context deadlines are test deadlines
// only; they never become CLI semantics.

const (
	startupBanner = "FaultLine read-only Git proof page: "
	startupStop   = "Press Ctrl+C to stop."
)

// collector is a synchronized, append-only output stream whose writers wake
// waiting readers without polling.
type collector struct {
	mu      sync.Mutex
	buf     []byte
	changed chan struct{}
}

func newCollector() *collector { return &collector{changed: make(chan struct{})} }

func (c *collector) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf = append(c.buf, p...)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return len(p), nil
}

// snapshot returns a copy of the bytes so far and a channel closed by the next
// write.
func (c *collector) snapshot() ([]byte, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...), c.changed
}

// Started is one running fl child that has printed its complete startup.
type Started struct {
	URL string
	Cmd *exec.Cmd

	stdout, stderr *collector
	done           chan struct{} // closed once Cmd.Wait has returned
	waitErr        error

	mu       sync.Mutex
	clients  []net.Conn
	disposed bool
	own      ownership // platform ownership (process group on Unix)
}

// StartOptions describes the complete prefix of the selected readiness stream
// (stdout unless ReadyStream says otherwise) that marks a process ready. Validation must reject any extra bytes beyond that prefix.
type StartOptions struct {
	StartupLines       int
	StartupDescription string
	ValidateStartup    func([]byte) (string, error)
	// ReadyStream selects the stream whose bytes are counted and validated
	// for readiness: "" or "stdout" (the default) or "stderr". Only that
	// stream is inspected for readiness; both are always captured.
	ReadyStream string
}

// Start launches executable with prefix+argv, reads exactly two complete LF
// lines (banner with the actual bound http://127.0.0.1:<port> URL, then the
// stop line), and retains both output streams. It fails, after terminating and
// joining the child, on a startup exit, any extra byte after the two lines, a
// wrong host or an invalid port. env is the complete child environment.
func Start(ctx context.Context, executable string, prefix, argv, env []string, cwd string) (*Started, error) {
	return StartWithOptions(ctx, executable, prefix, argv, env, cwd, StartOptions{
		StartupLines: 2, StartupDescription: "two startup lines", ValidateStartup: validateStartup,
	})
}

// StartWithOptions is Start with a configurable readiness prefix. It retains
// exactly the same platform preparation, process ownership, output collection,
// startup failure cleanup, signal delivery, and Dispose behavior.
func StartWithOptions(ctx context.Context, executable string, prefix, argv, env []string, cwd string, options StartOptions) (*Started, error) {
	if options.StartupLines < 1 || options.StartupDescription == "" || options.ValidateStartup == nil {
		return nil, errors.New("invalid serve fixture startup options")
	}
	if options.ReadyStream != "" && options.ReadyStream != "stdout" && options.ReadyStream != "stderr" {
		return nil, fmt.Errorf("invalid serve fixture ReadyStream %q", options.ReadyStream)
	}
	args := append(append([]string{}, prefix...), argv...)
	cmd := exec.Command(executable, args...)
	cmd.Dir = cwd
	cmd.Env = env
	p := &Started{Cmd: cmd, stdout: newCollector(), stderr: newCollector(), done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.stdout, p.stderr
	// Preparation failure is reported before any child exists, so nothing is
	// launched and there is nothing to clean up.
	if err := prepare(cmd); err != nil {
		return nil, fmt.Errorf("preparing to start %s: %w", executable, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", executable, err)
	}
	p.own = takeOwnership(cmd)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	ready := p.stdout
	if options.ReadyStream == "stderr" {
		ready = p.stderr
	}
	fail := func(reason error) (*Started, error) {
		p.kill()
		<-p.done
		out, _ := p.stdout.snapshot()
		errOut, _ := p.stderr.snapshot()
		return nil, fmt.Errorf("%w\nstdout: %q\nstderr: %q\nexit: %s", reason, out, errOut, p.describeExit())
	}
	for {
		out, changed := ready.snapshot()
		if bytes.Count(out, []byte("\n")) >= options.StartupLines {
			url, err := options.ValidateStartup(out)
			if err != nil {
				return fail(err)
			}
			p.URL = url
			return p, nil
		}
		select {
		case <-changed:
		case <-p.done:
			// The final writes happen before Wait returns; look once more.
			if out, _ = ready.snapshot(); bytes.Count(out, []byte("\n")) >= options.StartupLines {
				continue
			}
			return fail(errors.New("fl exited before printing its " + options.StartupDescription))
		case <-ctx.Done():
			return fail(fmt.Errorf("startup deadline: %w", ctx.Err()))
		}
	}
}

// validateStartup requires stdout to be exactly the two startup lines so far.
func validateStartup(out []byte) (string, error) {
	text := string(out)
	first, rest, _ := strings.Cut(text, "\n")
	second, extra, _ := strings.Cut(rest, "\n")
	if extra != "" {
		return "", fmt.Errorf("extra stdout after the two startup lines: %q", extra)
	}
	url, ok := strings.CutPrefix(first, startupBanner)
	if !ok {
		return "", fmt.Errorf("first startup line %q lacks the banner", first)
	}
	if second != startupStop {
		return "", fmt.Errorf("second startup line is %q", second)
	}
	port, ok := strings.CutPrefix(url, "http://127.0.0.1:")
	if !ok {
		return "", fmt.Errorf("URL %q is not http://127.0.0.1:<port>", url)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", fmt.Errorf("URL %q has an invalid port", url)
	}
	return url, nil
}

// Exited reports whether the child has already been reaped.
func (p *Started) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Dial opens a raw TCP client to the bound address. The client is tracked so
// CloseClients or Dispose always releases it; the caller may also close it
// earlier, and that repeat close is tolerated.
func (p *Started) Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp4", strings.TrimPrefix(p.URL, "http://"))
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.clients = append(p.clients, conn)
	p.mu.Unlock()
	return conn, nil
}

// CloseClients releases every owned raw client (normal teardown order: release
// clients, then wait for the process).
func (p *Started) CloseClients() error {
	p.mu.Lock()
	clients := p.clients
	p.clients = nil
	p.mu.Unlock()
	var first error
	for _, c := range clients {
		// A caller may already have closed its client; only that repeat close
		// (net.ErrClosed) is expected. Every other close error is kept.
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) && first == nil {
			first = err
		}
	}
	return first
}

// Wait joins the child and returns its complete streams and raw status.
func (p *Started) Wait(ctx context.Context) (ProcessResult, error) {
	select {
	case <-p.done:
	case <-ctx.Done():
		return ProcessResult{}, fmt.Errorf("waiting for fl: %w", ctx.Err())
	}
	out, _ := p.stdout.snapshot()
	errOut, _ := p.stderr.snapshot()
	code, signal := exitStatus(p.Cmd.ProcessState)
	return ProcessResult{Stdout: string(out), Stderr: string(errOut), ExitCode: code, Signal: signal}, nil
}

func (p *Started) describeExit() string {
	if p.Cmd.ProcessState == nil {
		return "unknown"
	}
	code, signal := exitStatus(p.Cmd.ProcessState)
	switch {
	case signal != nil:
		return "signal " + *signal
	case code != nil:
		return "code " + strconv.Itoa(*code)
	}
	return "unknown"
}

// Dispose closes owned clients and, only if the child is still running, kills
// it and joins it so no service survives the test. A forced kill is reported as
// an error; normal assertions are made before teardown. Streams stay readable.
func (p *Started) Dispose() error {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return nil
	}
	p.disposed = true
	p.mu.Unlock()
	closeErr := p.CloseClients()
	var forced error
	if !p.Exited() {
		p.kill()
		<-p.done
		forced = errors.New("fl was still running at teardown and was force-terminated")
	}
	return errors.Join(forced, closeErr)
}
