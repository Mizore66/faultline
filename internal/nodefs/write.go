package nodefs

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// WriteFileUTF8 is writeFileSync(path, text, { encoding: "utf8", mode, flag })
// with flag "w" (exclusive false) or "wx" (exclusive true): Node 22's C++
// WriteFileUtf8 fast path. open(2) failures name the path ("EEXIST: file
// already exists, open '<path>'"); write and close failures do not. The text
// is encoded as Node encodes a JS string (lone surrogates become U+FFFD).
func WriteFileUTF8(path, text string, mode fs.FileMode, exclusive bool) error {
	return WriteFileBytes(path, []byte(jsstr.ToUTF8(text)), mode, exclusive)
}

// WriteFileBytes is writeFile(path, buffer, { mode, flag }) with flag "w"
// or "wx", reporting failures as WriteFileUTF8 does.
func WriteFileBytes(path string, data []byte, mode fs.FileMode, exclusive bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(sysPath(path), flags, mode)
	if err != nil {
		return wrap(err, "open", path)
	}
	var writeErr error
	for len(data) > 0 {
		n, err := f.Write(data)
		if err != nil {
			writeErr = &Error{Code: codeOf(err), Syscall: "write", NoPath: true}
			break
		}
		data = data[n:]
	}
	if cerr := closeFile(f); cerr != nil && writeErr == nil {
		return &Error{Code: codeOf(cerr), Syscall: "close", NoPath: true}
	}
	return writeErr
}

// Mkdir is mkdirSync(path, { mode }): one mkdir(2).
func Mkdir(path string, mode fs.FileMode) error {
	if err := retryEINTR(func() error { return os.Mkdir(sysPath(path), mode) }); err != nil {
		return wrap(err, "mkdir", path)
	}
	return nil
}

// pathSeparator is node's kPathSeparator.
func pathSeparator() string {
	if isWindows {
		return `\`
	}
	return "/"
}

// MkdirAll is mkdirSync(path, { recursive: true, mode }): node_file.cc
// MKDirpSync, whose failure Node throws naming the requested path whatever
// component failed.
func MkdirAll(path string, mode fs.FileMode) error {
	if code := mkdirp(sysPath(path), mode); code != "" {
		return &Error{Code: code, Syscall: "mkdir", Path: errorPath(path)}
	}
	return nil
}

// mkdirp ports MKDirpSync over raw (system) paths; it returns the libuv
// error name, or "" on success.
func mkdirp(raw string, mode fs.FileMode) string {
	paths := []string{raw}
	for len(paths) > 0 {
		next := paths[len(paths)-1]
		paths = paths[:len(paths)-1]
		err := retryEINTR(func() error { return os.Mkdir(next, mode) })
		code := ""
		if err != nil {
			code = codeOf(err)
		}
		for {
			switch code {
			case "":
			case "EACCES", "ENOSPC", "ENOTDIR", "EPERM":
				return code
			case "ENOENT":
				dirname := next
				if i := strings.LastIndex(next, pathSeparator()); i >= 0 {
					dirname = next[:i]
				}
				if dirname != next {
					paths = append(paths, next, dirname)
				} else if len(paths) == 0 {
					code = "EEXIST"
					continue
				}
			default:
				orig := code
				info, serr := os.Stat(next)
				if serr == nil && !info.IsDir() {
					if orig == "EEXIST" && len(paths) > 0 {
						return "ENOTDIR"
					}
					return "EEXIST"
				}
				if serr != nil {
					return codeOf(serr)
				}
			}
			break
		}
	}
	return ""
}

// IsExist reports whether err is a Node fs error with code EEXIST.
func IsExist(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "EEXIST"
}
