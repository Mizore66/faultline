//go:build linux || darwin

package proofview

// Native Linux/Darwin evidence for a one-shot nonblocking vector-write
// primitive (a single real writev(2) issued inside net.TCPConn's
// SyscallConn().Write callback).
//
// This is test-only P0 evidence. It adopts no production transport and makes
// no claim about asynchronous queues, HTTP lifecycle, or any kernel buffer
// threshold. All connections are owned 127.0.0.1 TCP pairs. No sleeps, no
// retry-to-green, no t.Parallel, no environment or global config changes.
//
// The callback ALWAYS returns true, so RawConn.Write never waits for
// writability; EINTR is the only retried condition (as libuv does). A partial
// accept or EAGAIN is a valid result and is preserved, not hidden. Any
// unaccepted suffix is drained with an ordinary conn.Write only after the
// callback has returned (never while the fd write lock is held).
//
// Deadlines and the byte guard below are FAILURE caps only, never transport
// policy or timing heuristics.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	uxReqCap   = 20 * time.Second  // failure cap per blocking operation
	uxTotalCap = 110 * time.Second // failure cap for the whole probe

	uxHdrText = "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 GMT\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n"

	uxHealthyBody = 19000
	uxLargeBody   = 4 << 20
	uxSmallBuf    = 8192

	// Failure guard for the saturation loop: if this many bytes were offered
	// without ever observing a partial/EAGAIN the sizing is inconclusive.
	uxSaturationGuard = 64 << 20
	uxBatchPayload    = 32 << 10
)

// ---------- failure deadlines ----------

// uxProbeDeadline is the whole-probe failure deadline: the shared cap, bounded
// by the test binary's own deadline when one exists. It is passed explicitly
// to every helper; there is no global state and it is never transport policy.
func uxProbeDeadline(t *testing.T) time.Time {
	dl := time.Now().Add(uxTotalCap)
	if td, ok := t.Deadline(); ok && td.Before(dl) {
		dl = td
	}
	return dl
}

// uxOpDeadline is min(now+per-op cap, whole-probe deadline).
func uxOpDeadline(dl time.Time) time.Time {
	op := time.Now().Add(uxReqCap)
	if dl.Before(op) {
		return dl
	}
	return op
}

// ---------- fixtures ----------

func uxBody(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(97 + (i*7+i/251+seed*13)%26)
	}
	return b
}

// uxWireVectors is the fixed five-vector chunked-like batch: header+size line,
// CRLF, body, CRLF, terminator.
func uxWireVectors(body []byte) [][]byte {
	return [][]byte{
		[]byte(uxHdrText + strconv.FormatInt(int64(len(body)), 16)),
		[]byte("\r\n"),
		body,
		[]byte("\r\n"),
		[]byte("0\r\n\r\n"),
	}
}

func uxFlatten(v [][]byte) []byte {
	var out []byte
	for _, b := range v {
		out = append(out, b...)
	}
	return out
}

func uxTotal(v [][]byte) int {
	n := 0
	for _, b := range v {
		n += len(b)
	}
	return n
}

// uxTailBytes returns the bytes remaining after dropping the first n accepted
// bytes of the batch.
func uxTailBytes(v [][]byte, n int) []byte {
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

func uxSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func uxFirstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

func uxLog(t *testing.T, name string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Errorf("marshal %s: %v", name, err)
		return
	}
	t.Logf("UX_EVIDENCE %s %s", name, b)
}

func uxPlatform() map[string]any {
	return map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0)}
}

// ---------- the single try-writev ----------

type uxTryResult struct {
	Total        int    `json:"total"`
	N            int    `json:"n"`
	Errno        int    `json:"errno"`
	ErrnoName    string `json:"errno_name,omitempty"`
	Callbacks    int    `json:"rawconn_callbacks"`
	Writevs      int    `json:"writev_syscalls_incl_eintr"`
	EINTRRetries int    `json:"eintr_retries"`
	Outcome      string `json:"outcome"` // full | partial | eagain-none | error
}

func uxErrnoName(e syscall.Errno) string {
	switch e {
	case syscall.EAGAIN:
		return "EAGAIN"
	case syscall.EPIPE:
		return "EPIPE"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ENOTCONN:
		return "ENOTCONN"
	}
	return fmt.Sprintf("errno(%d):%s", int(e), e.Error())
}

// uxTry issues exactly one real nonblocking writev for the whole batch.
// unsafe justification: iovs holds pointers into the vecs backing arrays; both
// stay referenced past the call through runtime.KeepAlive, and the iovec
// array pointer is converted to uintptr only inside the Syscall argument list.
func uxTry(c *net.TCPConn, vecs [][]byte) (uxTryResult, error) {
	res := uxTryResult{Total: uxTotal(vecs)}
	iovs := make([]syscall.Iovec, len(vecs))
	for i, v := range vecs {
		if len(v) == 0 {
			return res, errors.New("empty vector")
		}
		iovs[i].Base = &v[0]
		iovs[i].SetLen(len(v))
	}
	rc, err := c.SyscallConn()
	if err != nil {
		return res, err
	}
	werr := rc.Write(func(fd uintptr) bool {
		res.Callbacks++
		for {
			res.Writevs++
			r, _, e := syscall.Syscall(syscall.SYS_WRITEV, fd, uintptr(unsafe.Pointer(&iovs[0])), uintptr(len(iovs)))
			if e == syscall.EINTR {
				res.EINTRRetries++
				continue
			}
			if e != 0 {
				res.Errno = int(e)
				res.ErrnoName = uxErrnoName(e)
			} else {
				res.N = int(r)
			}
			return true
		}
	})
	runtime.KeepAlive(vecs)
	runtime.KeepAlive(iovs)
	switch {
	case res.Errno == int(syscall.EAGAIN):
		res.Outcome = "eagain-none"
	case res.Errno != 0:
		res.Outcome = "error"
	case res.N == res.Total:
		res.Outcome = "full"
	default:
		res.Outcome = "partial"
	}
	return res, werr
}

// uxCheckTryBounds enforces API-level bounds without demanding any threshold.
func uxCheckTryBounds(t *testing.T, res uxTryResult, werr error) {
	t.Helper()
	if werr != nil {
		t.Fatalf("RawConn.Write: %v (%+v)", werr, res)
	}
	if res.Callbacks != 1 {
		t.Fatalf("rawconn callbacks = %d, want 1 (%+v)", res.Callbacks, res)
	}
	if res.Writevs != 1+res.EINTRRetries {
		t.Fatalf("writev count %d inconsistent with EINTR retries %d", res.Writevs, res.EINTRRetries)
	}
	if res.N < 0 || res.N > res.Total {
		t.Fatalf("n out of bounds: %+v", res)
	}
	if res.Outcome == "error" || (res.Errno != 0 && res.N != 0) {
		t.Fatalf("unexpected try error: %+v", res)
	}
}

// ---------- owned loopback pair ----------

type uxPair struct {
	srv *net.TCPConn // writer under test (accepted side)
	cli *net.TCPConn // application reader
}

func uxSetBuf(c *net.TCPConn, opt, size int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var se error
	if err := rc.Control(func(fd uintptr) { se = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, opt, size) }); err != nil {
		return err
	}
	return se
}

func uxGetBuf(c *net.TCPConn, opt int) int {
	rc, err := c.SyscallConn()
	if err != nil {
		return -1
	}
	v := -1
	_ = rc.Control(func(fd uintptr) {
		if x, e := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, opt); e == nil {
			v = x
		}
	})
	return v
}

// uxNewPair creates an owned accepted/dialed pair. sndbuf/rcvbuf of 0 keep
// kernel defaults. All sockets are closed and the accept goroutine joined on
// every path.
func uxNewPair(t *testing.T, dl time.Time, sndbuf, rcvbuf int) *uxPair {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tl := ln.(*net.TCPListener)
	_ = tl.SetDeadline(uxOpDeadline(dl))
	type acc struct {
		c   *net.TCPConn
		err error
	}
	ch := make(chan acc, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := tl.AcceptTCP()
		ch <- acc{c, err}
	}()
	p := &uxPair{}
	t.Cleanup(func() {
		if p.srv != nil {
			_ = p.srv.Close()
		}
		if p.cli != nil {
			_ = p.cli.Close()
		}
	})
	d := net.Dialer{Deadline: uxOpDeadline(dl)}
	if rcvbuf > 0 {
		d.Control = func(network, address string, rc syscall.RawConn) error {
			var se error
			if err := rc.Control(func(fd uintptr) {
				se = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, rcvbuf)
			}); err != nil {
				return err
			}
			return se
		}
	}
	nc, derr := d.Dial("tcp4", ln.Addr().String())
	if derr != nil {
		_ = ln.Close()
		wg.Wait()
		// The accept result is buffered; close any socket it may have taken.
		if a := <-ch; a.c != nil {
			_ = a.c.Close()
		}
		t.Fatalf("dial: %v", derr)
	}
	p.cli = nc.(*net.TCPConn)
	a := <-ch
	_ = ln.Close()
	wg.Wait()
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	p.srv = a.c
	if sndbuf > 0 {
		if err := uxSetBuf(p.srv, syscall.SO_SNDBUF, sndbuf); err != nil {
			t.Fatalf("SO_SNDBUF: %v", err)
		}
	}
	return p
}

// ---------- reader ----------

type uxReader struct {
	conn *net.TCPConn
	done chan struct{}
	data []byte
	kind string
	err  error
}

// uxStartReader starts an application reader reading to EOF/error. The reader
// is closed and joined at test cleanup on every path; the read deadline is a
// failure cap.
func uxStartReader(t *testing.T, dl time.Time, conn *net.TCPConn) *uxReader {
	t.Helper()
	r := &uxReader{conn: conn, done: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close(); <-r.done })
	go func() {
		defer close(r.done)
		_ = conn.SetReadDeadline(uxOpDeadline(dl))
		buf := make([]byte, 64<<10)
		for {
			n, err := conn.Read(buf)
			r.data = append(r.data, buf[:n]...)
			if err != nil {
				r.err = err
				var ne net.Error
				switch {
				case errors.Is(err, io.EOF):
					r.kind = "EOF"
				case errors.Is(err, syscall.ECONNRESET):
					r.kind = "ECONNRESET"
				case errors.As(err, &ne) && ne.Timeout():
					r.kind = "timeout"
				default:
					r.kind = "other"
				}
				return
			}
		}
	}()
	return r
}

func (r *uxReader) wait() { <-r.done }

// uxWriteTail drains an unaccepted suffix with an ordinary Write. Callers must
// only call it after the RawConn callback has returned.
func uxWriteTail(c *net.TCPConn, dl time.Time, tail []byte) (int, error) {
	if len(tail) == 0 {
		return 0, nil
	}
	_ = c.SetWriteDeadline(uxOpDeadline(dl))
	return c.Write(tail)
}

func uxRequireExact(t *testing.T, r *uxReader, want []byte) {
	t.Helper()
	if r.kind != "EOF" {
		t.Errorf("reader ended with %s (%v), want clean EOF", r.kind, r.err)
	}
	if !bytes.Equal(r.data, want) {
		t.Errorf("stream mismatch: got %d bytes sha %s, want %d bytes sha %s, first diff at %d",
			len(r.data), uxSHA(r.data), len(want), uxSHA(want), uxFirstDiff(r.data, want))
	}
}

// ---------- cases ----------

func TestUnixSocketTryWriteGoInterop(t *testing.T) {
	dl := uxProbeDeadline(t)
	t.Run("fresh_batch_exact_fifo", func(t *testing.T) { uxTestFreshBatch(t, dl) })
	t.Run("paused_reader_saturation", func(t *testing.T) { uxTestSaturation(t, dl) })
	t.Run("destroy_vs_graceful", func(t *testing.T) { uxTestDestroyVsGraceful(t, dl) })
	t.Run("closed_peer_error_visible", func(t *testing.T) { uxTestClosedPeer(t, dl) })
}

func uxTestFreshBatch(t *testing.T, dl time.Time) {
	p := uxNewPair(t, dl, 0, 0)
	vecs := uxWireVectors(uxBody(uxHealthyBody, 0))
	wire := uxFlatten(vecs)
	rd := uxStartReader(t, dl, p.cli)

	res, werr := uxTry(p.srv, vecs)
	uxCheckTryBounds(t, res, werr)
	// Ordinary Write only after the callback returned.
	tail := uxTailBytes(vecs, res.N)
	tn, terr := uxWriteTail(p.srv, dl, tail)
	if terr != nil || tn != len(tail) {
		t.Errorf("tail write: wrote %d of %d: %v", tn, len(tail), terr)
	}
	if err := p.srv.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	rd.wait()
	uxRequireExact(t, rd, wire)
	uxLog(t, "fresh_batch", map[string]any{
		"platform": uxPlatform(), "try": res, "tail_len": len(tail), "tail_written": tn,
		"wire_len": len(wire), "wire_sha256": uxSHA(wire), "received_sha256": uxSHA(rd.data),
	})
}

func uxBatch(i int) [][]byte {
	return [][]byte{
		[]byte(fmt.Sprintf("B%07d:", i)),
		uxBody(uxBatchPayload, i),
		[]byte("\r\n"),
	}
}

func uxTestSaturation(t *testing.T, dl time.Time) {
	p := uxNewPair(t, dl, uxSmallBuf, uxSmallBuf)
	snd, rcv := uxGetBuf(p.srv, syscall.SO_SNDBUF), uxGetBuf(p.cli, syscall.SO_RCVBUF)

	var expected bytes.Buffer
	var pending []byte
	var first uxTryResult
	attempts, offered := 0, 0
	observed := false
	// The reader is intentionally not started: no application read happens
	// before a partial/EAGAIN is observed.
	for offered < uxSaturationGuard {
		vecs := uxBatch(attempts)
		res, werr := uxTry(p.srv, vecs)
		uxCheckTryBounds(t, res, werr)
		attempts++
		offered += res.Total
		expected.Write(uxFlatten(vecs))
		if res.Outcome == "full" {
			continue
		}
		first, observed = res, true
		pending = uxTailBytes(vecs, res.N)
		break
	}
	if !observed {
		t.Fatalf("inconclusive sizing: no partial/EAGAIN within %d offered bytes (snd=%d rcv=%d)", offered, snd, rcv)
	}

	// Release the reader, then drain the pending suffix (ordinary Write, after
	// the callback returned) and offer one more batch once the reader is live.
	rd := uxStartReader(t, dl, p.cli)
	tn, terr := uxWriteTail(p.srv, dl, pending)
	if terr != nil || tn != len(pending) {
		t.Fatalf("pending tail write: wrote %d of %d: %v", tn, len(pending), terr)
	}
	vecs := uxBatch(attempts)
	later, werr := uxTry(p.srv, vecs)
	uxCheckTryBounds(t, later, werr)
	expected.Write(uxFlatten(vecs))
	ltail := uxTailBytes(vecs, later.N)
	ln, lerr := uxWriteTail(p.srv, dl, ltail)
	if lerr != nil || ln != len(ltail) {
		t.Fatalf("later tail write: wrote %d of %d: %v", ln, len(ltail), lerr)
	}
	if err := p.srv.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	rd.wait()
	uxRequireExact(t, rd, expected.Bytes())
	uxLog(t, "saturation", map[string]any{
		"platform": uxPlatform(), "sndbuf_reported": snd, "rcvbuf_reported": rcv,
		"batches_before_observation": attempts, "bytes_offered": offered, "first_non_full": first,
		"pending_tail_len": len(pending), "later_try_after_release": later, "later_tail_len": len(ltail),
		"stream_len": expected.Len(), "stream_sha256": uxSHA(expected.Bytes()), "received_sha256": uxSHA(rd.data),
	})
}

func uxTestDestroyVsGraceful(t *testing.T, dl time.Time) {
	body := uxBody(uxLargeBody, 3)
	vecs := uxWireVectors(body)
	wire := uxFlatten(vecs)

	t.Run("destroy_with_paused_reader", func(t *testing.T) {
		p := uxNewPair(t, dl, uxSmallBuf, uxSmallBuf)
		snd, rcv := uxGetBuf(p.srv, syscall.SO_SNDBUF), uxGetBuf(p.cli, syscall.SO_RCVBUF)
		if snd < 0 || rcv < 0 || len(wire) <= snd+rcv {
			t.Fatalf("inconclusive sizing: wire %d not above reported budget snd=%d rcv=%d", len(wire), snd, rcv)
		}
		res, werr := uxTry(p.srv, vecs)
		uxCheckTryBounds(t, res, werr)
		// Hard close with the reader still paused; unaccepted suffix is discarded.
		if err := p.srv.SetLinger(0); err != nil {
			t.Fatalf("SetLinger: %v", err)
		}
		if err := p.srv.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		rd := uxStartReader(t, dl, p.cli)
		rd.wait()
		if rd.kind != "EOF" && rd.kind != "ECONNRESET" {
			t.Errorf("reader ended with %s (%v)", rd.kind, rd.err)
		}
		if len(rd.data) > res.N {
			t.Errorf("received %d bytes > accepted n %d: invented bytes", len(rd.data), res.N)
		}
		if !bytes.HasPrefix(wire, rd.data) {
			t.Errorf("received bytes are not a prefix of the wire stream (first diff %d)", uxFirstDiff(rd.data, wire))
		}
		uxLog(t, "destroy", map[string]any{
			"platform": uxPlatform(), "sndbuf_reported": snd, "rcvbuf_reported": rcv, "wire_len": len(wire),
			"try": res, "received_len": len(rd.data), "received_equals_accepted": len(rd.data) == res.N,
			"read_end": rd.kind, "received_sha256": uxSHA(rd.data), "wire_prefix_sha256": uxSHA(wire[:len(rd.data)]),
		})
	})

	t.Run("graceful_with_active_reader", func(t *testing.T) {
		// Default kernel buffers: the small-buffer case is covered above, and
		// this case only needs a reader-active full delivery.
		p := uxNewPair(t, dl, 0, 0)
		snd, rcv := uxGetBuf(p.srv, syscall.SO_SNDBUF), uxGetBuf(p.cli, syscall.SO_RCVBUF)
		rd := uxStartReader(t, dl, p.cli)
		res, werr := uxTry(p.srv, vecs)
		uxCheckTryBounds(t, res, werr)
		tail := uxTailBytes(vecs, res.N)
		tn, terr := uxWriteTail(p.srv, dl, tail)
		if terr != nil || tn != len(tail) {
			t.Errorf("tail write: wrote %d of %d: %v", tn, len(tail), terr)
		}
		if err := p.srv.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		rd.wait()
		uxRequireExact(t, rd, wire)
		uxLog(t, "graceful", map[string]any{
			"platform": uxPlatform(), "sndbuf_reported": snd, "rcvbuf_reported": rcv,
			"try": res, "tail_len": len(tail), "tail_written": tn,
			"wire_len": len(wire), "wire_sha256": uxSHA(wire), "received_sha256": uxSHA(rd.data),
		})
	})
}

func uxTestClosedPeer(t *testing.T, dl time.Time) {
	p := uxNewPair(t, dl, 0, 0)
	// Peer aborts the connection (RST) with no application read.
	if err := p.cli.SetLinger(0); err != nil {
		t.Fatalf("SetLinger: %v", err)
	}
	if err := p.cli.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}
	// Close-observed gate: a bounded read on the accepted side must reach a
	// named terminal state before the single try. No retry-until-error.
	_ = p.srv.SetReadDeadline(uxOpDeadline(dl))
	buf := make([]byte, 4096)
	readBytes, readKind := 0, ""
	var readErr error
	for readKind == "" {
		n, err := p.srv.Read(buf)
		readBytes += n
		if err == nil {
			continue
		}
		readErr = err
		var ne net.Error
		switch {
		case errors.Is(err, io.EOF):
			readKind = "EOF"
		case errors.Is(err, syscall.ECONNRESET):
			readKind = "ECONNRESET"
		case errors.As(err, &ne) && ne.Timeout():
			readKind = "timeout"
		default:
			readKind = "other"
		}
	}
	if readKind != "EOF" && readKind != "ECONNRESET" {
		t.Fatalf("accepted side did not observe peer close: %s (%v)", readKind, readErr)
	}
	if readBytes != 0 {
		t.Errorf("accepted side read %d unexpected bytes", readBytes)
	}

	vecs := uxWireVectors(uxBody(uxHealthyBody, 5))
	res, werr := uxTry(p.srv, vecs)
	if werr != nil {
		t.Fatalf("RawConn.Write: %v (%+v)", werr, res)
	}
	if res.Callbacks != 1 || res.Writevs != 1+res.EINTRRetries {
		t.Fatalf("callback/syscall accounting broken: %+v", res)
	}
	if res.Outcome != "error" {
		t.Fatalf("write after observed peer reset did not report an error: %+v", res)
	}
	if res.N != 0 {
		t.Errorf("error result carries n=%d", res.N)
	}
	switch syscall.Errno(res.Errno) {
	case syscall.EPIPE, syscall.ECONNRESET:
	default:
		t.Errorf("unexpected errno after peer reset: %s", res.ErrnoName)
	}
	uxLog(t, "closed_peer", map[string]any{"platform": uxPlatform(), "read_terminal": readKind, "read_bytes": readBytes, "try": res})
}
