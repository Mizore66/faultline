package nodefs

import (
	"io/fs"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// Basename is path.basename for a path without trailing separators (a
// resolved path): the part after the last separator, with a Windows drive
// or UNC root removed first.
func Basename(p string) string {
	if isWindows {
		if len(p) >= 2 && p[1] == ':' && isDeviceRoot(p[0]) {
			p = p[2:]
		}
		i := strings.LastIndexAny(p, `/\`)
		return p[i+1:]
	}
	return p[strings.LastIndexByte(p, '/')+1:]
}

// WriteFileSyncUTF8 is openSync(path, "wx", mode), writeFileSync(fd, text,
// "utf8"), fsyncSync(fd) and closeSync(fd), with the descriptor closed on
// every failure (the caller's `finally`). An open(2) failure names the path;
// write, fsync and close failures do not.
func WriteFileSyncUTF8(path, text string, mode fs.FileMode) error {
	f, err := os.OpenFile(sysPath(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return openWriteErr(err, path, true)
	}
	data := []byte(jsstr.ToUTF8(text))
	for len(data) > 0 {
		n, err := f.Write(data)
		if err != nil {
			closeFile(f)
			return &Error{Code: codeOf(err), Syscall: "write", NoPath: true}
		}
		data = data[n:]
	}
	if err := retryEINTR(f.Sync); err != nil {
		closeFile(f)
		return &Error{Code: codeOf(err), Syscall: "fsync", NoPath: true}
	}
	if err := closeFile(f); err != nil {
		return &Error{Code: codeOf(err), Syscall: "close", NoPath: true}
	}
	return nil
}

// Rename is renameSync(from, to); a failure prints both paths.
func Rename(from, to string) error {
	if err := renameRaw(sysPath(from), sysPath(to)); err != nil {
		return &Error{Code: codeOf(err), Syscall: "rename", Path: errorPath(from), Dest: errorPath(to)}
	}
	return nil
}

// SyncDir is the best-effort directory fsync after a rename: openSync(dir,
// "r"), fsyncSync, closeSync, every failure ignored.
func SyncDir(dir string) {
	f, err := os.Open(sysPath(dir))
	if err != nil {
		return
	}
	f.Sync()
	closeFile(f)
}

// Link is linkSync(from, to); a failure prints both paths.
func Link(from, to string) error {
	if err := retryEINTR(func() error { return os.Link(sysPath(from), sysPath(to)) }); err != nil {
		return &Error{Code: codeOf(err), Syscall: "link", Path: errorPath(from), Dest: errorPath(to)}
	}
	return nil
}
