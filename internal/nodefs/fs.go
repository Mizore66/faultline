package nodefs

import (
	"errors"
	"fmt"
	"github.com/Mizore66/faultline/internal/jsstr"
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

// descriptions is uv_strerror: libuv 1.51 include/uv.h UV_ERRNO_MAP.
var descriptions = map[string]string{
	"E2BIG":           "argument list too long",
	"EACCES":          "permission denied",
	"EADDRINUSE":      "address already in use",
	"EADDRNOTAVAIL":   "address not available",
	"EAFNOSUPPORT":    "address family not supported",
	"EAGAIN":          "resource temporarily unavailable",
	"EAI_ADDRFAMILY":  "address family not supported",
	"EAI_AGAIN":       "temporary failure",
	"EAI_BADFLAGS":    "bad ai_flags value",
	"EAI_BADHINTS":    "invalid value for hints",
	"EAI_CANCELED":    "request canceled",
	"EAI_FAIL":        "permanent failure",
	"EAI_FAMILY":      "ai_family not supported",
	"EAI_MEMORY":      "out of memory",
	"EAI_NODATA":      "no address",
	"EAI_NONAME":      "unknown node or service",
	"EAI_OVERFLOW":    "argument buffer overflow",
	"EAI_PROTOCOL":    "resolved protocol is unknown",
	"EAI_SERVICE":     "service not available for socket type",
	"EAI_SOCKTYPE":    "socket type not supported",
	"EALREADY":        "connection already in progress",
	"EBADF":           "bad file descriptor",
	"EBUSY":           "resource busy or locked",
	"ECANCELED":       "operation canceled",
	"ECHARSET":        "invalid Unicode character",
	"ECONNABORTED":    "software caused connection abort",
	"ECONNREFUSED":    "connection refused",
	"ECONNRESET":      "connection reset by peer",
	"EDESTADDRREQ":    "destination address required",
	"EEXIST":          "file already exists",
	"EFAULT":          "bad address in system call argument",
	"EFBIG":           "file too large",
	"EHOSTUNREACH":    "host is unreachable",
	"EINTR":           "interrupted system call",
	"EINVAL":          "invalid argument",
	"EIO":             "i/o error",
	"EISCONN":         "socket is already connected",
	"EISDIR":          "illegal operation on a directory",
	"ELOOP":           "too many symbolic links encountered",
	"EMFILE":          "too many open files",
	"EMSGSIZE":        "message too long",
	"ENAMETOOLONG":    "name too long",
	"ENETDOWN":        "network is down",
	"ENETUNREACH":     "network is unreachable",
	"ENFILE":          "file table overflow",
	"ENOBUFS":         "no buffer space available",
	"ENODEV":          "no such device",
	"ENOENT":          "no such file or directory",
	"ENOMEM":          "not enough memory",
	"ENONET":          "machine is not on the network",
	"ENOPROTOOPT":     "protocol not available",
	"ENOSPC":          "no space left on device",
	"ENOSYS":          "function not implemented",
	"ENOTCONN":        "socket is not connected",
	"ENOTDIR":         "not a directory",
	"ENOTEMPTY":       "directory not empty",
	"ENOTSOCK":        "socket operation on non-socket",
	"ENOTSUP":         "operation not supported on socket",
	"EOVERFLOW":       "value too large for defined data type",
	"EPERM":           "operation not permitted",
	"EPIPE":           "broken pipe",
	"EPROTO":          "protocol error",
	"EPROTONOSUPPORT": "protocol not supported",
	"EPROTOTYPE":      "protocol wrong type for socket",
	"ERANGE":          "result too large",
	"EROFS":           "read-only file system",
	"ESHUTDOWN":       "cannot send after transport endpoint shutdown",
	"ESPIPE":          "invalid seek",
	"ESRCH":           "no such process",
	"ETIMEDOUT":       "connection timed out",
	"ETXTBSY":         "text file is busy",
	"EXDEV":           "cross-device link not permitted",
	"UNKNOWN":         "unknown error",
	"EOF":             "end of file",
	"ENXIO":           "no such device or address",
	"EMLINK":          "too many links",
	"EHOSTDOWN":       "host is down",
	"EREMOTEIO":       "remote I/O error",
	"ENOTTY":          "inappropriate ioctl for device",
	"EFTYPE":          "inappropriate file type or format",
	"EILSEQ":          "illegal byte sequence",
	"ESOCKTNOSUPPORT": "socket type not supported",
	"ENODATA":         "no data available",
	"EUNATCH":         "protocol driver not attached",
	"ENOEXEC":         "exec format error",
}

func (e *Error) Error() string {
	if e.NoPath {
		return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall
	}
	return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall + " '" + e.Path + "'"
}

// ErrnoCode names err's errno the way Node's ErrnoException does (libuv's
// uv_err_name, after uv_translate_sys_error on Windows).
func ErrnoCode(err error) string { return codeOf(err) }

func codeOf(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if isWindows {
			if code, ok := win32Codes[uintptr(errno)]; ok {
				return code
			}
			return "UNKNOWN"
		}
		if code, ok := unixCodes[errno]; ok {
			return code
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
		if info, err := os.Stat(sysPath(dir)); err == nil {
			return !info.IsDir()
		}
	}
	return false
}

// sysPath is the path Node hands to the syscall: JS strings are encoded as
// UTF-8 with each lone surrogate replaced by U+FFFD. Error messages keep the
// JS string (and print it through the same replacement).
func sysPath(path string) string { return jsstr.ToUTF8(path) }

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
	info, err := os.Stat(sysPath(path))
	if err != nil {
		return nil, 0, wrap(err, "open", path)
	}
	if info.IsDir() {
		return nil, 0, &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	f, err := os.Open(sysPath(path))
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

// ReadText is readFileSync(path, "utf8"). Node 22's ReadFileUtf8 reads the
// raw bytes and throws ERR_STRING_TOO_LONG when their count is at least V8's
// String::kMaxLength (src/util-inl.h ToV8Value: str.size() >= kMaxLength),
// whatever the decoded length would be; it has no 2 GiB check.
func ReadText(path string) (string, error) {
	f, size, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if size >= maxStringLength {
		return "", ErrStringTooLong
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", wrap(err, "read", path)
	}
	if len(b) >= maxStringLength { // non-regular files report size 0
		return "", ErrStringTooLong
	}
	return DecodeUTF8(b), nil
}

// Lstat is lstatSync(path). On Windows, reparse points are classified the
// way libuv does (see lstatReparse): symlinks, WSL symlinks, drive-letter
// junctions and AppExecLinks are symbolic links; anything else (OneDrive
// placeholders, WOF-compressed files, volume mount points) is a file or
// directory by the attributes of what it resolves to.
func Lstat(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(sysPath(path))
	if err != nil {
		return nil, wrap(err, "lstat", path)
	}
	if isWindows {
		if info, err = lstatReparse(sysPath(path), info); err != nil {
			return nil, wrap(err, "lstat", path)
		}
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
	_, err := os.Stat(sysPath(path))
	return err == nil
}

// ReadDirNames is readdirSync(path): names sorted by byte order (libuv scandir).
func ReadDirNames(path string) ([]string, error) {
	f, err := os.Open(sysPath(path))
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
		if err = os.Mkdir(sysPath(path), 0o700); err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	return "", wrap(err, "mkdtemp", prefix+"XXXXXX")
}

// Describe is uv_strerror for a libuv error name.
func Describe(code string) string { return descriptions[code] }
