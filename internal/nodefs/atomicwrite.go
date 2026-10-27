package nodefs

import (
	"io/fs"
	"os"
	"strconv"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
)

// AtomicWriteFile ports the staged write of src/ledger.ts:599 (and
// src/ledger-binding.ts:536, the same code): mkdirSync(dir, {recursive}),
// a ".<base>.<pid>.<uuid>.tmp" stage opened "wx" 0600, the text written as
// UTF-8, fsync, close, renameSync onto filePath, a best-effort directory
// fsync, and in `finally` the stage removed (rmSync force) when it still
// exists. It returns resolve(filePath).
//
// The open error names the stage; write, fsync and close errors name no
// path; the rename error names both paths; an rmSync failure in `finally`
// replaces the result.
func AtomicWriteFile(filePath, text string) (string, error) {
	destination := Resolve(filePath)
	directory := Dirname(destination)
	if err := MkdirAll(directory, 0o777); err != nil {
		return "", err
	}
	staged := Resolve(directory, jsexc.Concat(".", Basename(destination), ".", strconv.Itoa(os.Getpid()), ".", clock.NewUUID(), ".tmp"))
	err := WriteFileSyncUTF8(staged, text, 0o600)
	if err == nil {
		err = Rename(staged, destination)
	}
	if err == nil {
		SyncDir(directory)
	}
	// `finally`: a throw from rmSync replaces the result.
	if Exists(staged) {
		if rmErr := RemoveAll(staged); rmErr != nil {
			err = rmErr
		}
	}
	if err != nil {
		return "", err
	}
	return destination, nil
}

// OpenExclusive is openSync(path, "wx", mode): the descriptor, or the open
// error as Node prints it ("EEXIST: file already exists, open '<path>'").
func OpenExclusive(path string, mode fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(sysPath(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, openWriteErr(err, path, true)
	}
	return f, nil
}

// Close is closeSync(fd); a failure is "<CODE>: <description>, close".
func Close(f *os.File) error {
	if err := closeFile(f); err != nil {
		return &Error{Code: codeOf(err), Syscall: "close", NoPath: true}
	}
	return nil
}

// Unlink is unlinkSync(path): one unlink(2), its error naming the path.
func Unlink(path string) error {
	if err := unlinkRaw(sysPath(path)); err != nil {
		return wrap(err, "unlink", path)
	}
	return nil
}
