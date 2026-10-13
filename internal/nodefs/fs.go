package nodefs

import (
	"errors"
	"fmt"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
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

// uvCode is an error libuv sets by its uv code directly, not through
// uv_translate_sys_error.
type uvCode string

func (c uvCode) Error() string { return string(c) }

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
	var uv uvCode
	if errors.As(err, &uv) {
		return string(uv)
	}
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

// retryEINTR re-runs fn while it fails with EINTR, as libuv's uv__fs_work
// does for every synchronous fs request except read and close
// (src/unix/fs.c: "} while (r == -1 && errno == EINTR && retry_on_eintr)").
func retryEINTR(fn func() error) error {
	for {
		if err := fn(); !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// openRead opens path as uv_fs_open does for readFileSync: open(2) and
// nothing else, so a directory without read permission fails with EACCES
// here, and a readable one with EISDIR from the first read.
func openRead(path string) (*os.File, error) {
	f, err := os.Open(sysPath(path))
	if err != nil {
		return nil, wrap(err, "open", path)
	}
	if err := checkReadable(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// closeFile is uv_fs_close: close(2), where EINTR and EINPROGRESS count as
// success (the descriptor is gone either way).
func closeFile(f *os.File) error {
	err := f.Close()
	if err == nil || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EINPROGRESS) {
		return nil
	}
	return err
}

// leaked holds the files ReadBytes leaves open, as Node does, so that
// their finalizers do not close them.
var leaked []*os.File

// readErr is a failed read(2): Node throws it from C++ without a path.
func readErr(err error) error {
	return &Error{Code: codeOf(err), Syscall: "read", NoPath: true}
}

// readChunk is one uv_fs_read with position -1: a single read(2), whose
// EINTR libuv does not retry. n is 0 at end of file.
func readChunk(f *os.File, b []byte) (int, error) {
	n, err := readRaw(f, b)
	if err != nil {
		return 0, readErr(err)
	}
	return n, nil
}

// ReadBytes is readFileSync(path), including ERR_FS_FILE_TOO_LARGE: open,
// fstat (whose failure Node throws without a path: binding.fstat's
// do_not_throw_error flag reads the wrong argument, so it throws), then
// reads up to the size fstat reported for a regular file, or 8 KiB at a
// time to EOF for anything else.
func ReadBytes(path string) (data []byte, err error) {
	f, err := openRead(path)
	if err != nil {
		return nil, err
	}
	var info fs.FileInfo
	err = retryEINTR(func() (e error) { info, e = f.Stat(); return })
	if err != nil {
		// The throw leaves tryStatSync before its closeSync, so Node never
		// closes the descriptor; on a filesystem that fails getattr while
		// a file is open, later lstats of it fail too.
		leaked = append(leaked, f)
		return nil, &Error{Code: codeOf(err), Syscall: "fstat", NoPath: true}
	}
	// readFileSync closes the descriptor with closeSync after reading, and
	// in a finally after a failed read or ERR_FS_FILE_TOO_LARGE: an error
	// from close replaces any other.
	defer func() {
		if cerr := closeFile(f); cerr != nil {
			data, err = nil, &Error{Code: codeOf(cerr), Syscall: "close", NoPath: true}
		}
	}()
	size := info.Size()
	if !info.Mode().IsRegular() {
		size = 0
	}
	if size > kIoMaxLength {
		return nil, fmt.Errorf("File size (%d) is greater than 2 GiB", size)
	}
	if size == 0 { // not a regular file, or empty: "the kernel lies about many files"
		var out []byte
		chunk := make([]byte, 8192)
		for {
			n, err := readChunk(f, chunk)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return out, nil
			}
			out = append(out, chunk[:n]...)
		}
	}
	// A regular file is read up to the size fstat reported: bytes appended
	// meanwhile are not read, and a file that shrank gives what was there.
	b := make([]byte, size)
	pos := 0
	for pos < len(b) {
		n, err := readChunk(f, b[pos:])
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
		pos += n
	}
	return b[:pos], nil
}

// ReadText is readFileSync(path, "utf8"), Node 22's ReadFileUtf8
// (src/node_file.cc): open, then read(2) 8 KiB at a time until a read
// returns 0, with no fstat, so neither a failing fstat nor the size it
// reports matters. The bytes are then converted, and ERR_STRING_TOO_LONG is
// thrown when their count is at least V8's String::kMaxLength
// (src/util-inl.h ToV8Value: str.size() >= kMaxLength), whatever the
// decoded length would be. Bytes past that limit are only counted, although
// Node keeps them all: past the memory Node could have (physical memory, or
// the cgroup's limit), fl dies as Node would have been killed, so a file
// that never ends (a link to /dev/zero) ends both (round 6: counting alone
// spun forever).
func ReadText(path string) (string, error) {
	f, err := openRead(path)
	if err != nil {
		return "", err
	}
	// ReadFileUtf8 closes on leaving its scope, after a read error too,
	// with CHECK_EQ(0, uv_fs_close(...)): a failing close aborts Node.
	defer func() {
		if closeFile(f) != nil {
			jsexc.FatalCheck("node::fs::ReadFileUtf8(const v8::FunctionCallbackInfo<v8::Value>&)::<lambda()> at ../src/node_file.cc:2605",
				"(0) == (uv_fs_close(nullptr, &req, file, nullptr))")
		}
	}()
	var b []byte
	total := 0
	chunk := make([]byte, 8192)
	for {
		n, err := readChunk(f, chunk)
		if err != nil {
			return "", err
		}
		if n == 0 {
			break
		}
		total += n
		if total < maxStringLength {
			b = append(b, chunk[:n]...)
		} else if uint64(total) > memoryLimit() {
			// Node would hold all of it by now and have been killed.
			outOfMemory()
		}
	}
	if total >= maxStringLength {
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

// Exists is existsSync(path): uv_fs_access(path, F_OK), and on Windows
// also uv_fs_stat (following links), must succeed.
func Exists(path string) bool {
	return exists(sysPath(path))
}

// direntTypeKnown is whether libuv's uv__fs_get_dirent_type names d_type:
// FIFO, CHR, DIR, BLK, REG, LNK and SOCK. Every other value, DT_UNKNOWN (0)
// included, is UV_DIRENT_UNKNOWN, and Node lstats the entry
// (lib/internal/fs/utils.js getDirents).
func direntTypeKnown(t uint8) bool {
	switch t {
	case 1, 2, 4, 6, 8, 10, 12:
		return true
	}
	return false
}

// ReadDirNames is readdirSync(path, { withFileTypes: true }) as the bundle
// walkers use it: names sorted by byte order (libuv scandir), then decoded
// as UTF-8 with replacement, so a name with invalid bytes no longer names
// the file. An entry whose type libuv cannot name (DT_UNKNOWN on XFS
// without ftype, some NFS and FUSE, or a type outside libuv's seven) is
// lstat'ed by Node inside readdirSync, in order, and the first failure is
// thrown from there.
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
			if !direntTypeKnown(types[k]) {
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
	// GetTempDir: the first non-empty of TMPDIR, TMP, TEMP, read through
	// SafeGetenv, which ignores them in setuid, setgid and AT_SECURE
	// processes.
	if envUnsafe() {
		return "/tmp"
	}
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
