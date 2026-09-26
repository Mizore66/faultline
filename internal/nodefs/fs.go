package nodefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
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
	"EBADF":        "bad file descriptor",
	"ENOMEM":       "not enough memory",
	"EXDEV":        "cross-device link not permitted",
	"EOF":          "end of file",
	"ENOTSUP":      "operation not supported on socket",
	"ENOTEMPTY":    "directory not empty",
	"EEXIST":       "file already exists",
	"EROFS":        "read-only file system",
	"ENOSPC":       "no space left on device",
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
		if isWindows {
			if code, ok := win32Codes[uintptr(errno)]; ok {
				return code
			}
			return "UNKNOWN"
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
		case syscall.EEXIST:
			return "EEXIST"
		case syscall.EROFS:
			return "EROFS"
		case syscall.ENOSPC:
			return "ENOSPC"
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

// kIoMaxLength is the largest file readFileSync reads into a Buffer.
const kIoMaxLength = 1<<31 - 1

// maxStringLength is V8's String::kMaxLength on 64-bit platforms.
const maxStringLength = 0x1fffffe8

// ErrStringTooLong is ERR_STRING_TOO_LONG, thrown when a utf8 read would
// decode to more UTF-16 units than V8 allows in one string.
var ErrStringTooLong = errors.New("Cannot create a string longer than 0x1fffffe8 characters")

func openRegular(path string) (*os.File, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, wrap(err, "open", path)
	}
	if info.IsDir() {
		return nil, 0, &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, wrap(err, "open", path)
	}
	return f, info.Size(), nil
}

// ReadBytes is readFileSync(path), including ERR_FS_FILE_TOO_LARGE.
func ReadBytes(path string) ([]byte, error) {
	f, size, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if size > kIoMaxLength {
		return nil, fmt.Errorf("File size (%d) is greater than 2 GiB", size)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, wrap(err, "read", path)
	}
	return b, nil
}

// ReadText is readFileSync(path, "utf8"). Node's utf8 fast path has no
// 2 GiB check; anything decoding past V8's string limit is ERR_STRING_TOO_LONG.
func ReadText(path string) (string, error) {
	f, size, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Decoding yields between n/2 and n UTF-16 units for n bytes.
	if size/2 > maxStringLength {
		return "", ErrStringTooLong
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", wrap(err, "read", path)
	}
	if len(b) > maxStringLength && decodedUTF16Length(b) > maxStringLength {
		return "", ErrStringTooLong
	}
	return DecodeUTF8(b), nil
}

// Lstat is lstatSync(path). On Windows, reparse points that Go reports as
// ModeIrregular are classified the way libuv does: a readable link (symlink
// or junction) is a symbolic link, anything else is a file or directory by
// its attributes (for example OneDrive placeholders and WOF-compressed files
// are regular files).
func Lstat(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, wrap(err, "lstat", path)
	}
	if isWindows && info.Mode()&fs.ModeIrregular != 0 {
		mode := info.Mode() &^ (fs.ModeIrregular | fs.ModeType)
		if _, err := os.Readlink(path); err == nil {
			mode |= fs.ModeSymlink
		} else if info.IsDir() {
			mode |= fs.ModeDir
		}
		return libuvInfo{info, mode}, nil
	}
	return info, nil
}

type libuvInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (i libuvInfo) Mode() fs.FileMode { return i.mode }
func (i libuvInfo) IsDir() bool       { return i.mode.IsDir() }

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
	// libuv sorts the raw names; Node then decodes each as UTF-8 with
	// replacement, so a name with invalid bytes no longer names the file.
	sort.Strings(names)
	for i, name := range names {
		names[i] = DecodeUTF8([]byte(name))
	}
	return names, nil
}

// Tmpdir is os.tmpdir().
func Tmpdir() string {
	if isWindows {
		path := os.Getenv("TEMP")
		if path == "" {
			path = os.Getenv("TMP")
		}
		if path == "" {
			root := os.Getenv("SystemRoot")
			if root == "" {
				root = os.Getenv("windir")
			}
			if root == "" {
				root = "undefined" // (undefined) + '\\temp'
			}
			path = root + `\temp`
		}
		if len(path) > 1 && path[len(path)-1] == '\\' && path[len(path)-2] != ':' {
			return path[:len(path)-1]
		}
		return path
	}
	// GetTempDir: the first non-empty of TMPDIR, TMP, TEMP.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if dir := os.Getenv(key); dir != "" {
			dir = DecodeUTF8([]byte(dir))
			if len(dir) > 1 && dir[len(dir)-1] == '/' {
				dir = dir[:len(dir)-1]
			}
			return dir
		}
	}
	return "/tmp"
}

const tempChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Mkdtemp is mkdtempSync(prefix): uv_fs_mkdtemp on prefix + "XXXXXX", with
// Node's error text naming the template.
func Mkdtemp(prefix string) (string, error) {
	var err error
	for range 100 {
		b := make([]byte, 6)
		for i := range b {
			b[i] = tempChars[rand.IntN(len(tempChars))]
		}
		path := prefix + string(b)
		if err = os.Mkdir(path, 0o700); err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	return "", wrap(err, "mkdtemp", prefix+"XXXXXX")
}

// RemoveAll is rmSync(path, { recursive: true, force: true }).
func RemoveAll(path string) { os.RemoveAll(path) }
