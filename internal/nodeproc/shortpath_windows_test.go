package nodeproc

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// The 8.3 form of a 259+-unit cwd can be longer than the cwd. libuv's
// GetShortPathNameW(cwd, cwd, cwd_len) then returns the size it needs, and
// libuv spawns from the long cwd; fl used to slice past its buffer and
// panic (round 5, row 7).
func TestShortPathLongerThanLongName(t *testing.T) {
	long := `C:\` + strings.Repeat(`.git\`, 60)
	need := uint32(len(long) + 40)
	got, err := shortPathWith(long, func(_, _ *uint16, size uint32) (uint32, error) {
		if size != uint32(len(long)+1) {
			t.Errorf("buffer of %d units, want %d", size, len(long)+1)
		}
		return need, nil
	})
	if err != nil || got != long {
		t.Fatalf("got %q, %v; want the long path", got, err)
	}
	short := `C:\GIT~1`
	got, err = shortPathWith(long, func(_, buf *uint16, size uint32) (uint32, error) {
		u := syscall.StringToUTF16(short)
		copy(unsafe.Slice(buf, size), u)
		return uint32(len(u) - 1), nil
	})
	if err != nil || got != short {
		t.Fatalf("got %q, %v; want %q", got, err, short)
	}
	if _, err := shortPathWith(long, func(_, _ *uint16, _ uint32) (uint32, error) {
		return 0, syscall.ERROR_FILE_NOT_FOUND
	}); err != syscall.ERROR_FILE_NOT_FOUND {
		t.Fatalf("err %v, want ERROR_FILE_NOT_FOUND", err)
	}
}
