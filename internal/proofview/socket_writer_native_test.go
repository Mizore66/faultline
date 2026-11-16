//go:build linux || darwin || windows

package proofview

// P1 tests of the ACTUAL production socketTryWriter helper (unwired to HTTP).
// newSocketTryWriter is intentionally not defined here: until the helper is
// adopted these tests fail to compile (RED). No raw writev/WSASend, unsafe or
// syscall code lives in this file; it only consumes the helper's Try method.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

const (
	sbOpCap       = 20 * time.Second  // per-operation failure cap
	sbWholeCap    = 110 * time.Second // shared whole-probe failure deadline
	sbSmallBuf    = 8192
	sbGuard       = 64 << 20 // offered-byte failure guard for saturation
	sbBatchBody   = 32 << 10
	sbHealthyBody = 19000
	sbHdr         = "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 GMT\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n"
)

// ---------- fixtures ----------

func sbBody(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(97 + (i*7+i/251+seed*13)%26)
	}
	return b
}

// sbWire is the fixed five-vector batch: header+size, CRLF, body, CRLF, terminator.
func sbWire(body []byte) [][]byte {
	return [][]byte{
		[]byte(sbHdr + strconv.FormatInt(int64(len(body)), 16)),
		[]byte("\r\n"),
		body,
		[]byte("\r\n"),
		[]byte("0\r\n\r\n"),
	}
}

func sbBatch(i int) [][]byte {
	return [][]byte{[]byte(fmt.Sprintf("B%07d:", i)), sbBody(sbBatchBody, i), []byte("\r\n")}
}

func sbFlatten(v [][]byte) []byte {
	var out []byte
	for _, b := range v {
		out = append(out, b...)
	}
	return out
}

func sbTotal(v [][]byte) int {
	n := 0
	for _, b := range v {
		n += len(b)
	}
	return n
}

// sbTail returns the bytes after the first n accepted bytes of the batch.
func sbTail(v [][]byte, n int) []byte {
	var out []byte
	for _, b := range v {
		if n >= len(b) {
			n -= len(b)
			continue
		}
		out = append(out, b[n:]...)
		n = 0
	}
	return out
}

func sbSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// ---------- deadlines ----------

// sbDeadline is a pure min(now+20s, whole, t.Deadline()) calculation. It never
// fails the test, so callers owning resources fail through their cleanup. An
// exhausted budget yields a past time, which makes the blocking operation
// itself time out as a failure; it is never applied to simulate a Try.
func sbDeadline(t *testing.T, whole time.Time) time.Time {
	d := time.Now().Add(sbOpCap)
	if whole.Before(d) {
		d = whole
	}
	if td, ok := t.Deadline(); ok && td.Before(d) {
		d = td
	}
	return d
}

// ---------- owned loopback pair ----------

type sbPair struct {
	srv *net.TCPConn // accepted, writer side
	cli *net.TCPConn // dialed, application reader side
}

func sbNewPair(t *testing.T, whole time.Time, small bool) *sbPair {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	type acc struct {
		c   *net.TCPConn
		err error
	}
	ch := make(chan acc, 1)
	var wg sync.WaitGroup
	p := &sbPair{}
	// Registered immediately on acquisition: closing the listener unblocks
	// Accept, the join cannot hang, and any buffered accepted socket is closed.
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
		select {
		case a := <-ch:
			if a.c != nil {
				_ = a.c.Close()
			}
		default:
		}
		if p.srv != nil {
			_ = p.srv.Close()
		}
		if p.cli != nil {
			_ = p.cli.Close()
		}
	})
	tl := ln.(*net.TCPListener)
	if err := tl.SetDeadline(sbDeadline(t, whole)); err != nil {
		t.Fatalf("listener deadline: %v", err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := tl.AcceptTCP()
		ch <- acc{c, err}
	}()
	d := net.Dialer{Deadline: sbDeadline(t, whole)}
	nc, derr := d.Dial("tcp4", ln.Addr().String())
	if derr != nil {
		t.Fatalf("dial: %v", derr) // cleanup closes ln, joins accept, closes buffered conn
	}
	p.cli = nc.(*net.TCPConn)
	a := <-ch // Accept is bounded by the listener deadline
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	p.srv = a.c
	if small {
		if err := p.srv.SetWriteBuffer(sbSmallBuf); err != nil {
			t.Fatalf("SetWriteBuffer: %v", err)
		}
		if err := p.cli.SetReadBuffer(sbSmallBuf); err != nil {
			t.Fatalf("SetReadBuffer: %v", err)
		}
	}
	return p
}

// ---------- reader ----------

type sbReadRes struct {
	data []byte
	err  error // nil means clean EOF
}

type sbReader struct {
	conn *net.TCPConn
	ch   chan sbReadRes // buffered: the goroutine never blocks on send
	once sync.Once
	res  sbReadRes
}

// sbStartReader reads to EOF. Cleanup closes the conn and joins the goroutine
// on every path; the read deadline is a failure cap only.
func sbStartReader(t *testing.T, conn *net.TCPConn, whole time.Time) *sbReader {
	t.Helper()
	dl := sbDeadline(t, whole)
	r := &sbReader{conn: conn, ch: make(chan sbReadRes, 1)}
	t.Cleanup(func() { _ = conn.Close(); r.wait() })
	go func() {
		var res sbReadRes
		if err := conn.SetReadDeadline(dl); err != nil {
			res.err = err
			r.ch <- res
			return
		}
		buf := make([]byte, 64<<10)
		for {
			n, err := conn.Read(buf)
			res.data = append(res.data, buf[:n]...)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					res.err = err
				}
				r.ch <- res
				return
			}
		}
	}()
	return r
}

// wait joins the reader; results are only read after completion.
func (r *sbReader) wait() sbReadRes {
	r.once.Do(func() { r.res = <-r.ch })
	return r.res
}

func sbRequireExact(t *testing.T, r *sbReader, want []byte) {
	t.Helper()
	res := r.wait()
	if res.err != nil {
		t.Errorf("reader did not end with clean EOF: %v", res.err)
	}
	if !bytes.Equal(res.data, want) {
		t.Errorf("stream mismatch: got %d bytes sha %s, want %d bytes sha %s",
			len(res.data), sbSHA(res.data), len(want), sbSHA(want))
	}
}

// ---------- helpers over the actual helper ----------

type sbTryFn func(parts [][]byte) (int, bool, error)

// sbCheck validates one Try result against the contract. A nonempty batch
// must either transfer a positive prefix, or be a wouldBlock with n=0.
func sbCheck(t *testing.T, parts [][]byte, n int, wb bool, err error) {
	t.Helper()
	total := sbTotal(parts)
	switch {
	case err != nil:
		t.Fatalf("Try returned error (n=%d wouldBlock=%v): %v", n, wb, err)
	case n < 0 || n > total:
		t.Fatalf("Try n=%d outside [0,%d]", n, total)
	case wb && n != 0:
		t.Fatalf("Try wouldBlock with n=%d, want 0", n)
	case !wb && n == 0:
		t.Fatalf("Try neither accepted bytes nor wouldBlock (n=0, err=nil)")
	}
}

// sbWriteGo performs an ordinary Go write; call only after Try has returned.
func sbWriteGo(t *testing.T, c *net.TCPConn, b []byte, whole time.Time) {
	t.Helper()
	if len(b) == 0 {
		return
	}
	if err := c.SetWriteDeadline(sbDeadline(t, whole)); err != nil {
		t.Fatalf("write deadline: %v", err)
	}
	n, err := c.Write(b)
	// Clear the failure-cap deadline so it cannot affect a later Try.
	if cerr := c.SetWriteDeadline(time.Time{}); cerr != nil {
		t.Fatalf("clear write deadline: %v", cerr)
	}
	if err != nil || n != len(b) {
		t.Fatalf("ordinary write: wrote %d of %d: %v", n, len(b), err)
	}
}

func sbNewWriter(t *testing.T, c *net.TCPConn) sbTryFn {
	t.Helper()
	w, err := newSocketTryWriter(c)
	if err != nil {
		t.Fatalf("newSocketTryWriter: %v", err)
	}
	if w == nil {
		t.Fatalf("newSocketTryWriter returned nil writer and nil error")
	}
	return w.Try
}

// ---------- cases ----------

func TestSocketTryWriterNative(t *testing.T) {
	whole := time.Now().Add(sbWholeCap)
	t.Run("fresh_batch_exact_fifo", func(t *testing.T) { sbFresh(t, whole) })
	t.Run("paused_reader_saturation", func(t *testing.T) { sbSaturation(t, whole) })
	t.Run("go_write_try_go_write_interop", func(t *testing.T) { sbInterop(t, whole) })
	t.Run("closed_connection_error", func(t *testing.T) { sbClosed(t, whole) })
}

func sbFresh(t *testing.T, whole time.Time) {
	p := sbNewPair(t, whole, false)
	try := sbNewWriter(t, p.srv)
	parts := sbWire(sbBody(sbHealthyBody, 0))
	want := sbFlatten(parts)
	rd := sbStartReader(t, p.cli, whole)

	n, wb, err := try(parts)
	sbCheck(t, parts, n, wb, err)
	t.Logf("fresh Try: n=%d wouldBlock=%v total=%d", n, wb, sbTotal(parts))
	if n <= 0 {
		// Anti-vacuity: a fresh healthy socket must realize some accepted
		// prefix; zero acceptance is inconclusive, not proof.
		t.Fatalf("fresh Try accepted no bytes (n=%d wouldBlock=%v): inconclusive", n, wb)
	}
	sbWriteGo(t, p.srv, sbTail(parts, n), whole)
	if err := p.srv.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	sbRequireExact(t, rd, want)
}

func sbSaturation(t *testing.T, whole time.Time) {
	p := sbNewPair(t, whole, true)
	try := sbNewWriter(t, p.srv)

	var expected bytes.Buffer
	var pending []byte
	offered, attempts, accepted := 0, 0, 0
	observed := false
	// No application read happens before a partial/wouldBlock is observed.
	for offered < sbGuard {
		parts := sbBatch(attempts)
		attempts++
		n, wb, err := try(parts)
		sbCheck(t, parts, n, wb, err)
		offered += sbTotal(parts)
		accepted += n
		expected.Write(sbFlatten(parts))
		if n == sbTotal(parts) {
			continue
		}
		t.Logf("saturation observation: attempt=%d n=%d wouldBlock=%v total=%d accepted_so_far=%d offered=%d",
			attempts, n, wb, sbTotal(parts), accepted, offered)
		if accepted <= 0 {
			// Anti-vacuity: an observation with no Try-accepted bytes (including
			// this attempt) is not evidence of saturation.
			t.Fatalf("inconclusive: first non-full Try with zero accepted bytes (wouldBlock=%v)", wb)
		}
		pending = sbTail(parts, n)
		observed = true
		break
	}
	if !observed {
		t.Fatalf("inconclusive: no partial/wouldBlock within %d offered bytes", offered)
	}

	rd := sbStartReader(t, p.cli, whole)
	sbWriteGo(t, p.srv, pending, whole)
	if err := p.srv.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	sbRequireExact(t, rd, expected.Bytes())
}

func sbInterop(t *testing.T, whole time.Time) {
	p := sbNewPair(t, whole, false)
	try := sbNewWriter(t, p.srv)
	rd := sbStartReader(t, p.cli, whole)

	before := sbBody(256<<10, 1)
	parts := sbWire(sbBody(sbHealthyBody, 2))
	after := sbBody(256<<10, 3)
	want := append(append(append([]byte{}, before...), sbFlatten(parts)...), after...)

	sbWriteGo(t, p.srv, before, whole)
	n, wb, err := try(parts)
	sbCheck(t, parts, n, wb, err)
	t.Logf("interop Try: n=%d wouldBlock=%v total=%d", n, wb, sbTotal(parts))
	sbWriteGo(t, p.srv, sbTail(parts, n), whole)
	sbWriteGo(t, p.srv, after, whole)
	if err := p.srv.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	sbRequireExact(t, rd, want)
}

func sbClosed(t *testing.T, whole time.Time) {
	p := sbNewPair(t, whole, false)
	try := sbNewWriter(t, p.srv)
	if err := p.srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	n, wb, err := try(sbWire(sbBody(sbHealthyBody, 4)))
	if !errors.Is(err, net.ErrClosed) {
		t.Errorf("Try after Close error = %v, want errors.Is net.ErrClosed", err)
	}
	if n != 0 {
		t.Errorf("Try after Close n=%d, want 0", n)
	}
	if wb {
		t.Errorf("Try after Close reported wouldBlock")
	}
}
