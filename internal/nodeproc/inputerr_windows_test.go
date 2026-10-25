package nodeproc

import (
	"os"
	"syscall"
	"testing"
)

func TestInputWriteErrorWindowsCodes(t *testing.T) {
	wrap := func(e syscall.Errno) error { return &os.PathError{Op: "write", Path: "|1", Err: e} }
	if code, fails := inputWriteError(wrap(109), "windows"); code != "EOF" || fails {
		t.Errorf("ERROR_BROKEN_PIPE: got (%s, %v), want (EOF, false)", code, fails)
	}
	if code, fails := inputWriteError(wrap(232), "windows"); code != "EPIPE" || !fails {
		t.Errorf("ERROR_NO_DATA: got (%s, %v), want (EPIPE, true)", code, fails)
	}
}
