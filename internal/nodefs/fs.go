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
	"strconv"
	"strings"
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
		return e.Code + ": " + Describe(e.Code) + ", " + e.Syscall
	}
	return e.Code + ": " + Describe(e.Code) + ", " + e.Syscall + " '" + e.Path + "'"
}

func unknownSystemError(errno syscall.Errno) string {
	return "Unknown system error -" + strconv.Itoa(int(errno))
}

// ErrnoCode names err's errno the way Node's ErrnoException does (libuv's
// uv_err_name, after uv_translate_sys_error on Windows).
func ErrnoCode(err error) string { return codeOf(err) }

// SystemErrorName is util.getSystemErrorName for the errno err carries, as
// ErrnoException prints it: libuv's name, or on Unix "Unknown system error
// -N" for an errno libuv has no name for (libuv passes it through negated).
func SystemErrorName(err error) string { return codeOf(err) }

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
		// Node's fs and process errors are thrown from C++ (UVException),
		// with uv_err_name and uv_strerror, which both give "Unknown
		// system error -N" for an errno libuv has no name for.
		return unknownSystemError(errno)
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
// UTF-8 with each lone surrogate replaced by U+FFFD, and on Windows
// namespaced (see toNamespacedPath). Error messages print errorPath.
func sysPath(path string) string {
	if isWindows {
		return toNamespacedPath(jsstr.ToUTF8(path), processCwd, os.Getenv)
	}
	return jsstr.ToUTF8(path)
}

func wrap(err error, syscallName, path string) error {
	code := codeOf(err)
	if code == "ENOENT" && ancestorNotDir(path) {
		code = "ENOTDIR"
	}
	return &Error{Code: code, Syscall: syscallName, Path: errorPath(path)}
}

// kIoMaxLength is the largest file readFileSync reads into a Buffer.
const kIoMaxLength = 1<<31 - 1

// maxStringLength is V8's String::kMaxLength on 64-bit platforms.
const maxStringLength = 0x1fffffe8

// ErrStringTooLong is ERR_STRING_TOO_LONG, thrown when a utf8 read would
// decode to more UTF-16 units than V8 allows in one string.
var ErrStringTooLong = errors.New("Cannot create a string longer than 0x1fffffe8 characters")

// openRegular opens path the way readFileSync does: open(2) first, so a
// directory without read permission fails with EACCES on open, and a
// readable directory then fails with EISDIR on read.
func openRegular(path string) (*os.File, fs.FileInfo, error) {
	f, err := os.Open(sysPath(path))
	if err != nil {
		return nil, nil, wrap(err, "open", path)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, wrap(err, "fstat", path)
	}
	if info.IsDir() {
		f.Close()
		return nil, nil, &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	return f, info, nil
}

// readErr is a failed read(2): Node throws it from C++ without a path.
func readErr(err error) error {
	return &Error{Code: codeOf(err), Syscall: "read", NoPath: true}
}

// ReadBytes is readFileSync(path), including ERR_FS_FILE_TOO_LARGE.
func ReadBytes(path string) ([]byte, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := info.Size()
	if !info.Mode().IsRegular() {
		size = 0
	}
	if size > kIoMaxLength {
		return nil, fmt.Errorf("File size (%d) is greater than 2 GiB", size)
	}
	if size == 0 { // not a regular file, or empty: read to EOF
		b, err := io.ReadAll(f)
		if err != nil {
			return nil, readErr(err)
		}
		return b, nil
	}
	// A regular file is read up to the size fstat reported: bytes appended
	// meanwhile are not read, and a file that shrank gives what was there.
	b := make([]byte, size)
	pos := 0
	for pos < len(b) {
		n, err := f.Read(b[pos:])
		pos += n
		if err == io.EOF || n == 0 && err == nil {
			break
		}
		if err != nil {
			return nil, readErr(err)
		}
	}
	return b[:pos], nil
}

// ReadText is readFileSync(path, "utf8"). Node 22's ReadFileUtf8 reads the
// raw bytes and throws ERR_STRING_TOO_LONG when their count is at least V8's
// String::kMaxLength (src/util-inl.h ToV8Value: str.size() >= kMaxLength),
// whatever the decoded length would be; it has no 2 GiB check.
func ReadText(path string) (string, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info.Size() >= maxStringLength {
		return "", ErrStringTooLong
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", readErr(err)
	}
	if len(b) >= maxStringLength { // non-regular files report size 0
		return "", ErrStringTooLong
	}
	return DecodeUTF8(b), nil
}

// Lstat is lstatSync(path). On Windows it follows libuv (see statRaw):
// symlinks, WSL symlinks, drive-letter junctions and AppExecLinks are
// symbolic links; any other reparse point is a file or directory by the
// attributes of what it resolves to, or an error when that cannot be
// opened.
func Lstat(path string) (fs.FileInfo, error) {
	info, err := statRaw(sysPath(path), false)
	if err != nil {
		return nil, wrap(err, "lstat", path)
	}
	return info, nil
}

// Exists is existsSync(path): uv_fs_access, and on Windows also uv_fs_stat
// (following links), must succeed.
func Exists(path string) bool {
	_, err := statRaw(sysPath(path), true)
	return err == nil
}

// ReadDirNames is readdirSync(path, { withFileTypes: true }) as the bundle
// walkers use it: names sorted by byte order (libuv scandir), then decoded
// as UTF-8 with replacement, so a name with invalid bytes no longer names
// the file. An entry whose type the filesystem does not report
// (DT_UNKNOWN: XFS without ftype, some NFS and FUSE) is lstat'ed by Node
// inside readdirSync, in order, and the first failure is thrown from there.
func ReadDirNames(path string) ([]string, error) {
	names, types, err := scandir(sysPath(path))
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return names[order[i]] < names[order[j]] })
	sorted := make([]string, len(names))
	for i, k := range order {
		sorted[i] = DecodeUTF8([]byte(names[k]))
	}
	if types != nil {
		for i, k := range order {
			if types[k] == 0 { // DT_UNKNOWN
				if _, err := Lstat(Join(path, sorted[i])); err != nil {
					return nil, err
				}
			}
		}
	}
	return sorted, nil
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
// Node's error text naming the template. Node does not namespace the
// template on Windows.
func Mkdtemp(prefix string) (string, error) {
	var err error
	for range 100 {
		b := make([]byte, 6)
		for i := range b {
			b[i] = tempChars[rand.IntN(len(tempChars))]
		}
		path := prefix + string(b)
		if err = os.Mkdir(jsstr.ToUTF8(path), 0o700); err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	tmpl := prefix + "XXXXXX"
	code := codeOf(err)
	if code == "ENOENT" && ancestorNotDir(tmpl) {
		code = "ENOTDIR"
	}
	display := tmpl
	if isWindows {
		display = DecodeUTF8([]byte(stringFromPath(jsstr.ToUTF8(tmpl))))
	}
	return "", &Error{Code: code, Syscall: "mkdtemp", Path: display}
}

// Describe is uv_strerror for a libuv error name.
func Describe(code string) string {
	if strings.HasPrefix(code, "Unknown system error ") {
		return code
	}
	return descriptions[code]
}
