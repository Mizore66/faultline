package nodeproc

import (
	"os"
	"syscall"
	"testing"
)

// A failed write of spawnSync's input: on Windows ERROR_NO_DATA (232) is
// uv_write's synchronous error, EPIPE through uv_translate_write_sys_error,
// which ends spawnSync before its loop runs; ERROR_BROKEN_PIPE (109) is a
// queued write broken later, EOF through uv_translate_sys_error. On Unix
// every write error goes to the callback.
func TestInputWriteError(t *testing.T) {
	wrap := func(e syscall.Errno) error { return &os.PathError{Op: "write", Path: "|1", Err: e} }
	for _, tc := range []struct {
		goos       string
		err        error
		code       string
		failsStart bool
	}{
		{"windows", wrap(232), "EPIPE", true},
		{"windows", syscall.Errno(232), "EPIPE", true},
	} {
		code, fails := inputWriteError(tc.err, tc.goos)
		if code != tc.code || fails != tc.failsStart {
			t.Errorf("%s %v: got (%s, %v), want (%s, %v)", tc.goos, tc.err, code, fails, tc.code, tc.failsStart)
		}
	}
	// The rest follow nodefs.SystemErrorName for the host's errno table.
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if goos != "windows" {
			if _, fails := inputWriteError(wrap(232), goos); fails {
				t.Errorf("%s: errno 232 fails the start", goos)
			}
		}
		if _, fails := inputWriteError(wrap(109), goos); fails {
			t.Errorf("%s: errno 109 fails the start", goos)
		}
	}
}
