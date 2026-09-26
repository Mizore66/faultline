//go:build unix

package nodefs

import (
	"errors"
	"os"
	"runtime"
	"syscall"
)

// pathMaxBytes is Node's buffer for process.cwd() (PATH_MAX_BYTES):
// PATH_MAX, 1024 on darwin and 4096 on Linux.
var pathMaxBytes = map[bool]int{true: 1024, false: 4096}[runtime.GOOS == "darwin"]

// Cwd is process.cwd(): libuv's uv_cwd into a PATH_MAX_BYTES buffer, the
// bytes decoded as UTF-8 with replacement. Errors are the uv_cwd errors Node
// throws: getcwd's errno, ENOBUFS when the path is exactly one byte too long
// for the buffer (uv_cwd's scratch retry fits it), ERANGE when longer.
func Cwd() (string, error) {
	cwd, err := syscall.Getwd()
	if err == nil && runtime.GOOS == "darwin" {
		// Go's darwin getcwd wrapper only reports errno on a -1 return, but
		// libc getcwd fails by returning NULL and may leave a stale path in
		// the buffer (deleted or recreated cwd, unreadable ancestor). Accept
		// the result only if it still names the current directory.
		err = sameAsDot(cwd)
	}
	if err != nil {
		if errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ERANGE) {
			// Too long for Go's PATH_MAX buffer. Under libuv (glibc or
			// darwin libc) that is ERANGE, or ENOBUFS at exactly the limit.
			code := "ERANGE"
			if n, ok := physicalCwdLength(); ok {
				switch {
				case n == pathMaxBytes:
					code = "ENOBUFS"
				case n < pathMaxBytes:
					code = "ENOENT" // not a length problem: getcwd failed outright
				}
			}
			return "", &Error{Code: code, Syscall: "uv_cwd", NoPath: true}
		}
		return "", &Error{Code: codeOf(err), Syscall: "uv_cwd", NoPath: true}
	}
	if len(cwd) >= pathMaxBytes {
		code := "ERANGE"
		if len(cwd) == pathMaxBytes {
			code = "ENOBUFS"
		}
		return "", &Error{Code: code, Syscall: "uv_cwd", NoPath: true}
	}
	return DecodeUTF8([]byte(cwd)), nil
}

// sameAsDot reports ENOENT unless path names the current directory, or the
// error from stat-ing path (EACCES for an unreadable ancestor).
func sameAsDot(path string) error {
	dot, err := os.Stat(".")
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(dot, info) {
		return syscall.ENOENT
	}
	return nil
}

// physicalCwdLength walks up from "." through ".." (as getcwd does) and
// returns the length of the physical path.
func physicalCwdLength() (int, bool) {
	root, err := os.Stat("/")
	if err != nil {
		return 0, false
	}
	dot, err := os.Stat(".")
	if err != nil {
		return 0, false
	}
	if os.SameFile(root, dot) {
		return 1, true
	}
	n := 0
	for parent := ".."; ; parent += "/.." {
		f, err := os.Open(parent)
		if err != nil {
			return 0, false
		}
		names, err := f.Readdirnames(-1)
		pd, statErr := f.Stat()
		f.Close()
		if err != nil || statErr != nil {
			return 0, false
		}
		found := false
		for _, name := range names {
			if d, err := os.Lstat(parent + "/" + name); err == nil && os.SameFile(d, dot) {
				n += 1 + len(name)
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
		if os.SameFile(pd, root) {
			return n, true
		}
		dot = pd
	}
}
