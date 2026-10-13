package nodefs

import (
	"io"
	"os"
)

// readRaw is one ReadFile on f; Windows has no EINTR.
func readRaw(f *os.File, b []byte) (int, error) {
	n, err := f.Read(b)
	if err == io.EOF {
		return 0, nil
	}
	return n, err
}

// checkReadable fails a directory with EISDIR, read, as Node does: Go opens
// directories with FILE_FLAG_BACKUP_SEMANTICS, where ReadFile would fail
// differently.
func checkReadable(f *os.File) error {
	info, err := f.Stat()
	if err == nil && info.IsDir() {
		return &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	return nil
}
