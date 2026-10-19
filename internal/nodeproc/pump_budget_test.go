//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package nodeproc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// TestReadStreamBudget pins uv__read's budget (libuv unix/stream.c,
// count = 32): one wakeup hands at most 32 full 64 KiB reads to the
// listener before the loop polls the other streams again (r9-3 N7). A
// regular file stands in for a stream that always has a full segment
// ready, which a socketpair (about 200 KB of buffer) cannot hold.
func TestReadStreamBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, make([]byte, 40*segment+10), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var sizes []int
	s := pipedStream{f: f, listen: func(b []byte) { sizes = append(sizes, len(b)) }}
	if ended := readStream(s); ended || len(sizes) != 32 {
		t.Fatalf("first wakeup: %d reads (ended %v), want 32", len(sizes), ended)
	}
	for _, n := range sizes {
		if n != segment {
			t.Fatalf("read of %d bytes, want %d", n, segment)
		}
	}
	// The rest: 8 full reads, then the short one stops the wakeup.
	sizes = nil
	if ended := readStream(s); ended || len(sizes) != 9 || sizes[8] != 10 {
		t.Fatalf("second wakeup: %v (ended %v)", sizes, ended)
	}
	sizes = nil
	if ended := readStream(s); !ended || len(sizes) != 0 {
		t.Fatalf("third wakeup: %v (ended %v), want EOF", sizes, ended)
	}
}

// TestReadStreamError pins uv__read's error path: a read that fails with
// anything but EAGAIN goes to the stream's 'error' listener and ends the
// stream (a directory stands in for a failing stream: read gives EISDIR).
func TestReadStreamError(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []error
	s := pipedStream{f: f, listen: func([]byte) { t.Error("data from a directory") }, fail: func(e error) { got = append(got, e) }}
	if !readStream(s) || len(got) != 1 {
		t.Fatalf("ended without one error: %v", got)
	}
	if code := (&StreamError{Code: nodefs.SystemErrorName(got[0])}).Error(); code != "read EISDIR" {
		t.Errorf("error = %q", code)
	}
}
