package cli

import (
	"io"
	"io/fs"
	"os"

	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// stdinLimit is readStandardInput's 4_000_000: Buffer.byteLength of the
// decoded body, so each U+FFFD counts 3 bytes.
const stdinLimit = 4_000_000

// readStandardInput ports src/cli-app.ts:287. process.stdin.setEncoding
// ("utf8") decodes with a StringDecoder, which equals one decode of all the
// bytes (an incomplete trailing sequence becomes U+FFFD at end). The limit is
// checked after every chunk; the body only grows, so one check at EOF gives
// the same outcome. TS keeps reading to EOF after rejecting, with no
// timeout, and so does this.
func (e *env) readStandardInput() (string, error) {
	stdin := e.stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	kind := stdinKind(stdin)
	if kind == stdinDummy {
		return "", nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", stdinReadError(kind, err)
	}
	body := jsstr.DecodeUTF8(data)
	if len(body) > stdinLimit { // a decoded body has no lone surrogates: its length is its UTF-8 length
		return "", fail("Lifecycle stdin exceeds the 4 MB safety limit.")
	}
	return body, nil
}

// The process.stdin kinds of Node's getStdin
// (lib/internal/bootstrap/switches/is_main_thread.js), from uv_guess_handle.
const (
	stdinFile   = iota // fs.ReadStream (a regular file or a character device)
	stdinStream        // net.Socket or tty.ReadStream (a pipe, socket or terminal)
	stdinDummy         // an empty Readable (UNKNOWN: a directory, a block device)
)

func stdinKind(r io.Reader) int {
	f, ok := r.(*os.File)
	if !ok {
		return stdinStream
	}
	info, err := f.Stat()
	if err != nil {
		return stdinDummy // uv_guess_handle's fstat failure
	}
	mode := info.Mode()
	switch {
	case mode.IsRegular():
		return stdinFile
	case mode&fs.ModeCharDevice != 0:
		return stdinFile // a terminal is a stream, but its read errors are not reachable
	case mode&(fs.ModeNamedPipe|fs.ModeSocket) != 0:
		return stdinStream
	}
	return stdinDummy
}

// stdinReadError is the error the stream emits: fs.ReadStream's is an fs
// error without a path ("EBADF: bad file descriptor, read"); a stream's is
// errnoException(nread, "read") ("read ECONNRESET"). libuv opens a pipe
// whose access mode is write-only as unreadable, and uv_read_start then
// fails with ENOTCONN where read(2) would give EBADF.
func stdinReadError(kind int, err error) error {
	code := nodefs.ErrnoCode(err)
	if kind == stdinFile {
		return &nodefs.Error{Code: code, Syscall: "read", NoPath: true}
	}
	if code == "EBADF" {
		code = "ENOTCONN"
	}
	return fail("read %s", code)
}
