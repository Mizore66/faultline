package proofview

// Native Windows feasibility probe for a strict "try-write" socket primitive.
//
// This is evidence gathering only. It adopts no production transport and makes
// no claim that either candidate mechanism is safe. Every potentially blocking
// native operation runs in a child process (this same test executable,
// re-entered through the entry test below) under a parent-enforced failure
// cap, so a blocked WSASend can never hang the package.
//
// FIONBIO is the Winsock ioctl _IOW('f', 126, u_long): IOC_IN (0x80000000) |
// (sizeof(u_long)=4 &0x7f)<<16 | 'f'<<8 | 126 == 0x8004667E, with a 4-byte
// little-endian u_long argument. It is not syscall.SetNonblock, which is a
// no-op on Windows.
//
// Candidate mechanisms (child mode names):
//   - toggle:          FIONBIO=1, one NULL-overlapped scatter/gather WSASend,
//     deferred FIONBIO=0 (restore failure fails the child).
//   - permanent-owned: FIONBIO=1 once on the child-owned accepted socket
//     before any reader, left on until the socket closes.
//
// The RawConn.Write callback always returns true and never runs an ordinary
// conn.Write while the fd write lock is held.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	twEnv       = "FAULTLINE_WINDOWS_TRYWRITE_CHILD"
	twNonceEnv  = "FAULTLINE_WINDOWS_TRYWRITE_NONCE"
	twNonceArg  = "faultline-trywrite-nonce="
	twSource    = "faultline-windows-trywrite-probe-v1"
	twResultTag = "PROBE_RESULT "
	twPhaseTag  = "PROBE_PHASE "

	twModeToggle    = "toggle"
	twModePermanent = "permanent-owned"
	twModeControl   = "control"

	twFIONBIO = 0x8004667E

	// Failure-only guards; neither is used as a behavior heuristic.
	twSaturateGuard = 64 << 20
	twPositiveCap   = 60 * time.Second
	twNegativeCap   = 15 * time.Second
)

var twWouldBlock = syscall.Errno(10035) // WSAEWOULDBLOCK

var twScenarios = []string{"batch", "saturate", "stress", "close"}

func twSetNonblock(h syscall.Handle, on bool) error {
	var in [4]byte
	if on {
		in[0] = 1
	}
	var ret uint32
	return syscall.WSAIoctl(h, twFIONBIO, &in[0], 4, nil, 0, &ret, nil, 0)
}

// twSend performs one synchronous NULL-overlapped scatter/gather WSASend.
// parts stay referenced across the call.
func twSend(h syscall.Handle, parts [][]byte) (int, error) {
	bufs := make([]syscall.WSABuf, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		bufs = append(bufs, syscall.WSABuf{Len: uint32(len(p)), Buf: &p[0]})
	}
	if len(bufs) == 0 {
		return 0, nil
	}
	var sent uint32
	err := syscall.WSASend(h, &bufs[0], uint32(len(bufs)), &sent, 0, nil, nil)
	runtime.KeepAlive(parts)
	runtime.KeepAlive(bufs)
	return int(sent), err
}

type twTryResult struct {
	N          int
	Err        error // native WSASend/ioctl error (may be WSAEWOULDBLOCK)
	RestoreErr error
}

// twTry runs one try-write over the concatenation of parts.
func twTry(c *net.TCPConn, mode string, parts ...[]byte) (twTryResult, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return twTryResult{}, err
	}
	var res twTryResult
	cb := func(fd uintptr) bool {
		h := syscall.Handle(fd)
		if mode == twModeToggle {
			if e := twSetNonblock(h, true); e != nil {
				res.Err = fmt.Errorf("FIONBIO=1: %w", e)
				return true
			}
			defer func() {
				if e := twSetNonblock(h, false); e != nil {
					res.RestoreErr = e
				}
			}()
		}
		res.N, res.Err = twSend(h, parts)
		return true
	}
	if err := raw.Write(cb); err != nil {
		return res, fmt.Errorf("RawConn.Write: %w", err)
	}
	return res, nil
}

// twCheck reports whether the try hit WSAEWOULDBLOCK; any other failure,
// including a failed mode restore, is an error. Real errors are never retried.
func twCheck(r twTryResult, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	if r.RestoreErr != nil {
		return false, fmt.Errorf("restore FIONBIO=0 failed: %w", r.RestoreErr)
	}
	if r.Err != nil {
		if r.Err == twWouldBlock {
			return true, nil
		}
		return false, fmt.Errorf("WSASend: %w (n=%d)", r.Err, r.N)
	}
	return false, nil
}

type twPair struct {
	w, p *net.TCPConn // w: child-owned accepted (writer) socket; p: peer
	once sync.Once
}

func (pr *twPair) close() {
	pr.once.Do(func() {
		pr.w.Close()
		pr.p.Close()
	})
}

func twNewPair(mode string) (*twPair, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	dc, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		return nil, err
	}
	sc, err := ln.Accept()
	if err != nil {
		dc.Close()
		return nil, err
	}
	pr := &twPair{w: sc.(*net.TCPConn), p: dc.(*net.TCPConn)}
	if mode == twModePermanent {
		raw, err := pr.w.SyscallConn()
		if err != nil {
			pr.close()
			return nil, err
		}
		var ierr error
		if err := raw.Control(func(fd uintptr) { ierr = twSetNonblock(syscall.Handle(fd), true) }); err != nil {
			pr.close()
			return nil, err
		}
		if ierr != nil {
			pr.close()
			return nil, fmt.Errorf("permanent FIONBIO=1: %w", ierr)
		}
	}
	return pr, nil
}

func twByte(off int) byte { return byte(off*131 + off>>7 + 17) }

func twFill(buf []byte, off int) []byte {
	for i := range buf {
		buf[i] = twByte(off + i)
	}
	return buf
}

func twPattern(n int) []byte { return twFill(make([]byte, n), 0) }

func twFirstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func twSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type twSat struct {
	Off, Tries, LastN int
	Kind              string
}

// twSaturate try-writes consecutive stream chunks until a partial or
// WSAEWOULDBLOCK result. The peer must not be reading.
func twSaturate(w *net.TCPConn, mode string) (twSat, error) {
	var s twSat
	const chunk = 16 << 10
	for {
		if s.Off > twSaturateGuard {
			return s, fmt.Errorf("guard: accepted %d bytes without blocking", s.Off)
		}
		buf := twFill(make([]byte, chunk), s.Off)
		r, err := twTry(w, mode, buf)
		wb, err := twCheck(r, err)
		if err != nil {
			return s, err
		}
		s.Tries++
		if wb {
			r.N = 0
		}
		s.Off += r.N
		if r.N < chunk {
			s.LastN = r.N
			s.Kind = "partial"
			if wb {
				s.Kind = "WSAEWOULDBLOCK"
			}
			return s, nil
		}
	}
}

func twSmallBuffers(pr *twPair) error {
	if err := pr.w.SetWriteBuffer(4096); err != nil {
		return err
	}
	return pr.p.SetReadBuffer(4096)
}

// ---- scenario: batch ----

func twScenarioBatch(mode string) (map[string]any, error) {
	pr, err := twNewPair(mode)
	if err != nil {
		return nil, err
	}
	defer pr.close()
	if err := pr.w.SetWriteBuffer(256 << 10); err != nil {
		return nil, err
	}
	if err := pr.p.SetReadBuffer(256 << 10); err != nil {
		return nil, err
	}
	const iters = 64
	var want, got []byte
	off := 0
	mk := func(it int) [][]byte {
		sizes := []int{9, 700 + it*37, 1500 + (it*53)%900, 2048, 3} // header, bodies, terminator-like
		parts := make([][]byte, len(sizes))
		for i, n := range sizes {
			parts[i] = twFill(make([]byte, n), off)
			off += n
		}
		return parts
	}
	details := map[string]any{"iterations": iters}

	// First batch: exact accepted prefix read before any Go-drain.
	parts := mk(0)
	all := bytes.Join(parts, nil)
	r, err := twTry(pr.w, mode, parts...)
	wb, err := twCheck(r, err)
	if err != nil {
		return nil, err
	}
	if wb || r.N != len(all) {
		return nil, fmt.Errorf("first batch not wholly accepted: n=%d total=%d wouldblock=%v", r.N, len(all), wb)
	}
	prefix := make([]byte, r.N)
	if _, err := io.ReadFull(pr.p, prefix); err != nil {
		return nil, err
	}
	if d := twFirstDiff(prefix, all[:r.N]); d >= 0 {
		return nil, fmt.Errorf("first prefix mismatch at %d", d)
	}
	suffix := all[r.N:]
	details["first_total"] = len(all)
	details["first_accepted_n"] = r.N
	details["first_suffix_len"] = len(suffix)
	details["first_prefix_sha256"] = twSum(prefix)
	details["first_prefix_head_hex"] = hex.EncodeToString(prefix[:min(16, len(prefix))])
	want = append(want, all...)
	got = append(got, prefix...)

	// Remaining batches: serial try -> Go drain -> next batch, reader running.
	type readRes struct {
		b   []byte
		err error
	}
	done := make(chan readRes, 1)
	go func() {
		b, err := io.ReadAll(pr.p)
		done <- readRes{b, err}
	}()
	partials, fulls, blocks := 0, 0, 0
	var loopErr error
	for it := 1; it < iters && loopErr == nil; it++ {
		parts := mk(it)
		all := bytes.Join(parts, nil)
		want = append(want, all...)
		r, err := twTry(pr.w, mode, parts...)
		wb, err := twCheck(r, err)
		if err != nil {
			loopErr = err
			break
		}
		switch {
		case wb:
			blocks++
			r.N = 0
		case r.N < len(all):
			partials++
		default:
			fulls++
		}
		if rest := all[r.N:]; len(rest) > 0 {
			if _, err := pr.w.Write(rest); err != nil {
				loopErr = fmt.Errorf("go drain: %w", err)
			}
		}
	}
	if err := pr.w.CloseWrite(); err != nil && loopErr == nil {
		loopErr = err
	}
	if loopErr != nil {
		pr.close()
	}
	rr := <-done
	if loopErr != nil {
		return nil, loopErr
	}
	if rr.err != nil {
		return nil, rr.err
	}
	got = append(got, rr.b...)
	if d := twFirstDiff(got, want); d >= 0 {
		return nil, fmt.Errorf("stream mismatch at %d (got %d want %d bytes)", d, len(got), len(want))
	}
	details["stream_bytes"] = len(want)
	details["stream_sha256"] = twSum(want)
	details["later_full"] = fulls
	details["later_partial"] = partials
	details["later_wouldblock"] = blocks
	return details, nil
}

// ---- scenario: saturate ----

func twScenarioSaturate(mode string) (map[string]any, error) {
	pr, err := twNewPair(mode)
	if err != nil {
		return nil, err
	}
	if err := twSmallBuffers(pr); err != nil {
		pr.close()
		return nil, err
	}
	release := make(chan int)
	type readRes struct {
		b   []byte
		err error
	}
	done := make(chan readRes, 1)
	go func() {
		n, ok := <-release // peer-read permission withheld until sent
		if !ok {
			done <- readRes{nil, errors.New("not released")}
			return
		}
		b := make([]byte, n)
		_, err := io.ReadFull(pr.p, b)
		done <- readRes{b, err}
	}()
	released := false
	cleanup := func() {
		if !released {
			close(release)
		}
		pr.close()
		<-done
	}
	fail := func(err error) (map[string]any, error) {
		cleanup()
		return nil, err
	}

	sat, err := twSaturate(pr.w, mode)
	if err != nil {
		return fail(err)
	}
	// A further try with the reader still withheld. The saturation loop above
	// already observed a partial/WSAEWOULDBLOCK; kernel buffer space may change
	// between requests, so this result is recorded, not asserted to block.
	const further = 64 << 10
	buf := twFill(make([]byte, further), sat.Off)
	r, err := twTry(pr.w, mode, buf)
	wb, err := twCheck(r, err)
	if err != nil {
		return fail(err)
	}
	if wb {
		r.N = 0
	}
	if r.N < 0 || r.N > further {
		return fail(fmt.Errorf("further try accepted impossible n=%d of %d", r.N, further))
	}
	total := sat.Off + len(buf)
	suffix := buf[r.N:]
	released = true
	release <- total
	if _, err := pr.w.Write(suffix); err != nil {
		cleanup()
		return nil, fmt.Errorf("go drain: %w", err)
	}
	rr := <-done
	pr.close()
	if rr.err != nil {
		return nil, rr.err
	}
	if d := twFirstDiff(rr.b, twPattern(total)); d >= 0 {
		return nil, fmt.Errorf("stream mismatch at %d", d)
	}
	return map[string]any{
		"accepted_before_block":  sat.Off,
		"tries_before_block":     sat.Tries,
		"block_kind":             sat.Kind,
		"block_last_n":           sat.LastN,
		"further_accepted_n":     r.N,
		"further_wouldblock":     wb,
		"further_full":           r.N == further,
		"further_len":            further,
		"go_drained_suffix_len":  len(suffix),
		"stream_bytes":           total,
		"reader_withheld_during": "saturation and further try (channel gate, no sleeps)",
	}, nil
}

// ---- scenario: stress ----

func twScenarioStress(mode string) (map[string]any, error) {
	const iters = 2000
	pr, err := twNewPair(mode)
	if err != nil {
		return nil, err
	}
	defer pr.close()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		pr.close()
	}
	failed := func() error {
		mu.Lock()
		defer mu.Unlock()
		return firstErr
	}
	tick := make(chan int)
	wg.Add(3)
	go func() { // peer marker sender, paced by main loop (no timers)
		defer wg.Done()
		dead := false
		for i := range tick {
			if dead {
				continue
			}
			var m [16]byte
			binary.BigEndian.PutUint64(m[:8], uint64(i))
			copy(m[8:], "MARKER!!")
			if _, err := pr.p.Write(m[:]); err != nil {
				fail(fmt.Errorf("marker write %d: %w", i, err))
				dead = true
			}
		}
	}()
	go func() { // ordinary Go reader on the writer socket: pending read + reissue
		defer wg.Done()
		for i := 0; i < iters; i++ {
			var m [16]byte
			if _, err := io.ReadFull(pr.w, m[:]); err != nil {
				fail(fmt.Errorf("marker read %d: %w", i, err))
				return
			}
			if binary.BigEndian.Uint64(m[:8]) != uint64(i) || string(m[8:]) != "MARKER!!" {
				fail(fmt.Errorf("marker %d corrupt: %x", i, m))
				return
			}
		}
	}()
	var gotAll []byte
	go func() {
		defer wg.Done()
		b, err := io.ReadAll(pr.p)
		if err != nil {
			fail(fmt.Errorf("peer readall: %w", err))
			return
		}
		gotAll = b
	}()

	var want []byte
	off := 0
	fulls, partials, blocks := 0, 0, 0
	for i := 0; i < iters && failed() == nil; i++ {
		sizes := []int{8, 1 + (i*97)%3000, 1 + (i*31)%1200, 512, 5}
		parts := make([][]byte, len(sizes))
		for k, n := range sizes {
			parts[k] = twFill(make([]byte, n), off)
			off += n
		}
		binary.BigEndian.PutUint32(parts[0][:4], uint32(i))
		all := bytes.Join(parts, nil)
		want = append(want, all...)
		r, err := twTry(pr.w, mode, parts...)
		wb, err := twCheck(r, err)
		if err != nil {
			fail(fmt.Errorf("iter %d: %w", i, err))
			break
		}
		switch {
		case wb:
			blocks++
			r.N = 0
		case r.N < len(all):
			partials++
		default:
			fulls++
		}
		if rest := all[r.N:]; len(rest) > 0 {
			if _, err := pr.w.Write(rest); err != nil {
				fail(fmt.Errorf("iter %d go drain: %w", i, err))
				break
			}
		}
		tick <- i
	}
	close(tick)
	if failed() == nil {
		if err := pr.w.CloseWrite(); err != nil {
			fail(err)
		}
	}
	wg.Wait()
	if err := failed(); err != nil {
		return nil, err
	}
	if d := twFirstDiff(gotAll, want); d >= 0 {
		return nil, fmt.Errorf("stream mismatch at %d (got %d want %d)", d, len(gotAll), len(want))
	}
	return map[string]any{
		"iterations":            iters,
		"stream_bytes":          len(want),
		"stream_sha256":         twSum(want),
		"full":                  fulls,
		"partial":               partials,
		"wouldblock":            blocks,
		"markers_verified":      iters,
		"blocked_io_interleave": "raced only (not forced); one marker released per try-write iteration",
	}, nil
}

// ---- scenario: close ----

// twClassifyClose decides whether err is an acceptable consequence of our own
// local close. Native, mode and restore failures are never acceptable, and no
// error observed before closeIssued is acceptable. Only net.ErrClosed counts.
func twClassifyClose(op string, native bool, err error, issuedBeforeErr bool) error {
	switch {
	case err == nil:
		return fmt.Errorf("%s: ended without error", op)
	case native:
		return fmt.Errorf("%s: native/mode/restore failure: %w", op, err)
	case !issuedBeforeErr:
		return fmt.Errorf("%s: error before local close was issued: %w", op, err)
	case !errors.Is(err, net.ErrClosed):
		return fmt.Errorf("%s: error is not net.ErrClosed: %w", op, err)
	}
	return nil
}

// twClassifierCases is a synthetic sensitivity table for twClassifyClose. It
// runs inside the close child; it exercises classification only, not Windows.
func twClassifierCases() error {
	closed := &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}
	cases := []struct {
		name   string
		native bool
		err    error
		issued bool
		accept bool
	}{
		{"closed-after-issue", false, fmt.Errorf("RawConn.Write: %w", closed), true, true},
		{"closed-before-issue", false, closed, false, false},
		{"nil", false, nil, true, false},
		{"reset-after-issue", false, &net.OpError{Op: "write", Err: syscall.Errno(10054)}, true, false},
		{"native-wouldblock-class", true, twWouldBlock, true, false},
		{"restore-failure", true, fmt.Errorf("restore: %w", syscall.Errno(10038)), true, false},
		{"native-even-if-closed-text", true, closed, true, false},
		{"plain-string-closed", false, errors.New("use of closed network connection"), true, false},
	}
	for _, c := range cases {
		if got := twClassifyClose(c.name, c.native, c.err, c.issued) == nil; got != c.accept {
			return fmt.Errorf("classifier case %s: accepted=%v want %v", c.name, got, c.accept)
		}
	}
	return nil
}

func twCloseRound(mode string, useTry, useWrite bool) (map[string]any, error) {
	pr, err := twNewPair(mode)
	if err != nil {
		return nil, err
	}
	defer pr.close()
	if err := twSmallBuffers(pr); err != nil {
		return nil, err
	}
	sat, err := twSaturate(pr.w, mode)
	if err != nil {
		return nil, err
	}
	var (
		wg                     sync.WaitGroup
		closeIssued            atomic.Bool
		tryErr, writeErr       error
		tryNative              bool
		tryIssued, writeIssued bool
		tryIters               int
	)
	firstTry := make(chan struct{})
	started := make(chan struct{})
	if useTry {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signalled := false
			for {
				r, err := twTry(pr.w, mode, []byte{'x'})
				if !signalled {
					signalled = true
					close(firstTry)
				}
				tryIters++
				switch {
				case r.RestoreErr != nil:
					tryErr, tryNative = fmt.Errorf("restore: %w", r.RestoreErr), true
				case r.Err != nil && r.Err != twWouldBlock:
					tryErr, tryNative = r.Err, true
				case err != nil:
					tryErr = err // RawConn.Write failure: closure candidate
				default:
					runtime.Gosched()
					continue
				}
				tryIssued = closeIssued.Load()
				return
			}
		}()
	}
	if useWrite {
		wg.Add(1)
		go func() {
			defer wg.Done()
			close(started)
			_, writeErr = pr.w.Write(make([]byte, 4<<20))
			writeIssued = closeIssued.Load()
		}()
		<-started
	}
	if useTry && !useWrite {
		<-firstTry
	}
	// pr.close closes the local (writer) socket first, then the peer.
	closeIssued.Store(true)
	pr.close()
	wg.Wait()
	if useTry {
		if err := twClassifyClose("try_loop", tryNative, tryErr, tryIssued); err != nil {
			return nil, err
		}
	}
	if useWrite {
		if err := twClassifyClose("go_write", false, writeErr, writeIssued); err != nil {
			return nil, err
		}
	}
	d := map[string]any{"saturated_at": sat.Off, "try_loop": useTry, "go_write": useWrite, "try_iterations": tryIters}
	if tryErr != nil {
		d["try_loop_error"] = tryErr.Error()
	}
	if writeErr != nil {
		d["go_write_error"] = writeErr.Error()
	}
	return d, nil
}

func twScenarioClose(mode string) (map[string]any, error) {
	if err := twClassifierCases(); err != nil {
		return nil, err
	}
	out := map[string]any{"classifier_cases": "synthetic table passed", "interleaving": "raced only (not forced): close issued after worker start signal, no sleeps"}
	for _, c := range []struct {
		name    string
		try, wr bool
	}{{"try_loop", true, false}, {"go_write", false, true}, {"both", true, true}} {
		d, err := twCloseRound(mode, c.try, c.wr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		out[c.name] = d
	}
	return out, nil
}

// ---- negative control ----

// twScenarioNegative saturates with a toggled (restored-to-blocking) socket,
// then issues the same NULL-overlapped send in blocking mode with the reader
// withheld. It is expected to block forever; the parent kills it.
func twScenarioNegative(string) (map[string]any, error) {
	pr, err := twNewPair(twModeToggle)
	if err != nil {
		return nil, err
	}
	defer pr.close()
	if err := twSmallBuffers(pr); err != nil {
		return nil, err
	}
	sat, err := twSaturate(pr.w, twModeToggle)
	if err != nil {
		return nil, err
	}
	raw, err := pr.w.SyscallConn()
	if err != nil {
		return nil, err
	}
	fmt.Printf("%sabout-to-block-nil-overlapped-send saturated_at=%d\n", twPhaseTag, sat.Off)
	os.Stdout.Sync()
	var n int
	var serr error
	if err := raw.Write(func(fd uintptr) bool {
		n, serr = twSend(syscall.Handle(fd), [][]byte{make([]byte, 1<<20)})
		return true
	}); err != nil {
		return nil, err
	}
	return map[string]any{"blocking_send_returned": true, "n": n, "err": fmt.Sprint(serr), "saturated_at": sat.Off}, nil
}

// ---- child / parent plumbing ----

func twRunChild(t *testing.T, spec string) {
	mode, scenario, ok := strings.Cut(spec, "/")
	if !ok {
		t.Fatalf("bad child spec %q", spec)
	}
	fns := map[string]func(string) (map[string]any, error){
		"batch": twScenarioBatch, "saturate": twScenarioSaturate,
		"stress": twScenarioStress, "close": twScenarioClose,
		"negative": twScenarioNegative,
	}
	fn := fns[scenario]
	if fn == nil || (mode != twModeToggle && mode != twModePermanent && mode != twModeControl) {
		t.Fatalf("unknown child spec %q", spec)
	}
	details, err := fn(mode)
	out := map[string]any{
		"source": twSource, "goos": runtime.GOOS, "goarch": runtime.GOARCH,
		"go": runtime.Version(), "mode": mode, "scenario": scenario,
		"ok": err == nil, "details": details,
	}
	if err != nil {
		out["error"] = err.Error()
	}
	b, _ := json.Marshal(out)
	fmt.Printf("%s%s\n", twResultTag, b)
	if err != nil {
		t.Fatalf("child %s failed: %v", spec, err)
	}
}

type twOutcome struct {
	stdout, stderr string
	exit           int
	timedOut       bool
	runErr         error
}

func twSpawn(t *testing.T, spec, nonce string, limit time.Duration) twOutcome {
	t.Helper()
	if dl, ok := t.Deadline(); ok {
		rem := time.Until(dl) - 20*time.Second // headroom for WaitDelay and reporting
		if rem <= 0 {
			t.Fatalf("not enough test deadline left for child %s", spec)
		}
		limit = min(limit, rem)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	// The child also has its own bounded timeout in case the parent dies.
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsSocketTryWriteGoInterop$", "-test.count=1",
		"-test.timeout="+(limit+30*time.Second).String(), twNonceArg+nonce)
	cmd.Env = append(os.Environ(), twEnv+"="+spec, twNonceEnv+"="+nonce)
	cmd.WaitDelay = 5 * time.Second
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	runErr := cmd.Run()
	o := twOutcome{stdout: so.String(), stderr: se.String(), exit: -1, runErr: runErr, timedOut: ctx.Err() == context.DeadlineExceeded}
	if cmd.ProcessState != nil {
		o.exit = cmd.ProcessState.ExitCode()
	}
	return o
}

func twReplay(t *testing.T, spec string, o twOutcome) {
	t.Helper()
	t.Logf("child %s: exit=%d timedOut=%v runErr=%v\n--- stdout ---\n%s\n--- stderr ---\n%s", spec, o.exit, o.timedOut, o.runErr, o.stdout, o.stderr)
}

func twParse(stdout string) (map[string]any, int) {
	var res map[string]any
	count := 0
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, twResultTag); ok {
			count++
			if err := json.Unmarshal([]byte(rest), &res); err != nil {
				return nil, count
			}
		}
	}
	return res, count
}

// TestWindowsSocketTryWriteGoInterop probes, on native Windows, whether a
// synchronous NULL-overlapped WSASend with FIONBIO can act as a try-write
// next to Go's overlapped net poller. It is evidence gathering only.
func TestWindowsSocketTryWriteGoInterop(t *testing.T) {
	if spec := os.Getenv(twEnv); spec != "" {
		// Child mode: never spawns children. An ambient spec without the
		// matching per-parent nonce (env and argv) is a configuration error.
		want := twNonceArg + os.Getenv(twNonceEnv)
		if os.Getenv(twNonceEnv) == "" || len(os.Args) == 0 || os.Args[len(os.Args)-1] != want {
			t.Fatalf("%s is set without a matching per-parent nonce; refusing to run as a child", twEnv)
		}
		twRunChild(t, spec)
		return
	}
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nb)
	for _, mode := range []string{twModeToggle, twModePermanent} {
		for _, sc := range twScenarios {
			spec := mode + "/" + sc
			t.Run(strings.ReplaceAll(spec, "/", "_"), func(t *testing.T) {
				o := twSpawn(t, spec, nonce, twPositiveCap)
				twReplay(t, spec, o)
				if o.timedOut {
					t.Fatalf("child %s exceeded failure cap %v (killed)", spec, twPositiveCap)
				}
				if o.exit != 0 {
					t.Fatalf("child %s exit=%d err=%v", spec, o.exit, o.runErr)
				}
				res, n := twParse(o.stdout)
				if n != 1 || res == nil {
					t.Fatalf("child %s: want exactly one valid %q line, got %d", spec, twResultTag, n)
				}
				for k, want := range map[string]any{
					"source": twSource, "goos": runtime.GOOS, "goarch": runtime.GOARCH,
					"mode": mode, "scenario": sc, "ok": true,
				} {
					if res[k] != want {
						t.Fatalf("child %s: %s=%v want %v", spec, k, res[k], want)
					}
				}
			})
		}
	}
	t.Run("negative_blocking_control", func(t *testing.T) {
		spec := twModeControl + "/negative"
		o := twSpawn(t, spec, nonce, twNegativeCap)
		twReplay(t, spec, o)
		_, n := twParse(o.stdout)
		reached := strings.Contains(o.stdout, twPhaseTag+"about-to-block-nil-overlapped-send")
		switch {
		case !reached:
			t.Fatalf("negative control never reached the blocking send (exit=%d timedOut=%v)", o.exit, o.timedOut)
		case o.timedOut && n == 0:
			t.Logf("NEGATIVE CONTROL RESULT: blocking-mode NULL-overlapped send blocked and was killed by the parent cap (expected; not a positive pass)")
		default:
			t.Errorf("negative control insensitive: blocking-mode send returned (timedOut=%v exit=%d results=%d)", o.timedOut, o.exit, n)
		}
	})
}
