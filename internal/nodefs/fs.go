package nodefs

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"syscall"
)

// Error renders like a Node fs error message.
type Error struct {
	Code, Syscall, Path string
	NoPath              bool
}

var descriptions = map[string]string{
	"ENOENT":       "no such file or directory",
	"ENOTDIR":      "not a directory",
	"EISDIR":       "illegal operation on a directory",
	"EACCES":       "permission denied",
	"EPERM":        "operation not permitted",
	"ELOOP":        "too many symbolic links encountered",
	"ENAMETOOLONG": "name too long",
	"EMFILE":       "too many open files",
	"EBUSY":        "resource busy or locked",
	"EINVAL":       "invalid argument",
	"EIO":          "i/o error",
	"UNKNOWN":      "unknown error",
}

func (e *Error) Error() string {
	if e.NoPath {
		return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall
	}
	return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall + " '" + e.Path + "'"
}

func codeOf(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if runtime.GOOS == "windows" {
			// libuv (uv_translate_sys_error) maps these Win32 errors the way
			// Node reports them; Linux reports ENOENT for the same names.
			switch uintptr(errno) {
			case 123, 161: // ERROR_INVALID_NAME, ERROR_BAD_PATHNAME
				return "ENOENT"
			case 206: // ERROR_FILENAME_EXCED_RANGE
				return "ENAMETOOLONG"
			}
		}
		switch errno {
		case syscall.ENOENT:
			return "ENOENT"
		case syscall.ENOTDIR:
			return "ENOTDIR"
		case syscall.EISDIR:
			return "EISDIR"
		case syscall.EACCES:
			return "EACCES"
		case syscall.EPERM:
			return "EPERM"
		case syscall.ELOOP:
			return "ELOOP"
		case syscall.ENAMETOOLONG:
			return "ENAMETOOLONG"
		case syscall.EMFILE:
			return "EMFILE"
		case syscall.EBUSY:
			return "EBUSY"
		case syscall.EINVAL:
			return "EINVAL"
		case syscall.EIO:
			return "EIO"
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	}
	return "UNKNOWN"
}

// ancestorNotDir reports whether a parent component of path is a non-directory
// (Linux returns ENOTDIR there; Windows reports "not found").
func ancestorNotDir(path string) bool {
	for dir := Dirname(path); dir != path; path, dir = dir, Dirname(dir) {
		if info, err := os.Stat(dir); err == nil {
			return !info.IsDir()
		}
	}
	return false
}

func wrap(err error, syscallName, path string) error {
	code := codeOf(err)
	if code == "ENOENT" && ancestorNotDir(path) {
		code = "ENOTDIR"
	}
	return &Error{Code: code, Syscall: syscallName, Path: path}
}

// ReadBytes is readFileSync(path).
func ReadBytes(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, wrap(err, "open", path)
	}
	if info.IsDir() {
		return nil, &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, wrap(err, "open", path)
	}
	return b, nil
}

// ReadText is readFileSync(path, "utf8").
func ReadText(path string) (string, error) {
	b, err := ReadBytes(path)
	if err != nil {
		return "", err
	}
	return DecodeUTF8(b), nil
}

// Lstat is lstatSync(path).
func Lstat(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, wrap(err, "lstat", path)
	}
	return info, nil
}

// Exists is existsSync(path): true when stat (following links) succeeds.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadDirNames is readdirSync(path): names sorted by byte order (libuv scandir).
func ReadDirNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	sort.Strings(names)
	return names, nil
}

// MkdirTemp is mkdtempSync(join(tmpdir(), prefix)).
func MkdirTemp(prefix string) (string, error) { return os.MkdirTemp("", prefix) }

// RemoveAll is rmSync(path, { recursive: true, force: true }).
func RemoveAll(path string) { os.RemoveAll(path) }
