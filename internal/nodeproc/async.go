package nodeproc

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/Mizore66/faultline/internal/boot"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// Stdio is one stdio slot of an async spawn: "ignore" or "pipe".
type Stdio int

const (
	StdioIgnore Stdio = iota // libuv opens /dev/null (NUL on Windows)
	StdioPipe
)

// SpawnOptions are the child_process.spawn options FaultLine's call sites
// use, always with shell: false; windowsHide is true unless WindowsShow.
type SpawnOptions struct {
	Cwd       string   // "" inherits fl's cwd
	Env       []string // "KEY=value" pairs in object order; nil means nodeEnv() (process.env)
	Stdin     []byte   // with StdinPipe: written, then end(); EPIPE is swallowed
	StdinPipe bool     // false: stdin "ignore"
	Stdout    Stdio
	Stderr    Stdio
	// OnStdout and OnStderr are the 'data' listeners. They are called on
	// one goroutine, in arrival order, and may call Kill.
	OnStdout, OnStderr func(chunk []byte)
	// WindowsVerbatimArguments is windowsVerbatimArguments: on Windows
	// libuv joins file and args with spaces without quoting any of them
	// (child_process.exec passes `/d /s /c "<command>"` this way). Ignored
	// elsewhere.
	WindowsVerbatimArguments bool
	// WindowsShow is windowsHide: false (exec's default): the child gets
	// no CREATE_NO_WINDOW and no SW_HIDE. Ignored elsewhere.
	WindowsShow bool
}

// Exit is what the call sites learn from 'close', or from 'error' when the
// executable could not be started.
type Exit struct {
	Code   *int   // nil when killed by a signal or never started
	Signal string // "SIGKILL", "SIGTERM", ... or ""
	Err    error  // the 'error' event (spawn failure), as Node's message: "spawn git ENOENT"
}

// Child is a ChildProcess.
type Child struct {
	mu     sync.Mutex
	proc   *os.Process // nil when the spawn failed
	reaped bool        // libuv's exit callback ran: ChildProcess._handle is null
	sent   bool        // a signal from Kill was delivered
	killed string      // that signal's name (Windows reports it as the signal)
	done   chan struct{}
	exit   Exit
}

// asyncSpawnErrors are the uv_spawn errors ChildProcess.prototype.spawn
// reports with an 'error' event on the next tick; any other one it throws.
var asyncSpawnErrors = map[string]bool{"EACCES": true, "EAGAIN": true, "EMFILE": true, "ENFILE": true, "ENOENT": true}

// Spawn is child_process.spawn(file, args, { cwd, env, stdio, shell: false,
// windowsHide: true }). It throws (panics with jsexc) where spawn throws
// synchronously: arguments with NUL bytes, an empty file, and uv_spawn
// errors Node does not defer (a cwd that is not a directory: "spawn
// ENOTDIR"). A failure to start the executable is reported by Wait, after
// which Node also emits 'close' with the negative errno as the code; the
// call sites settle on 'error' first, so Exit carries no code then.
func Spawn(file string, args []string, o SpawnOptions) *Child {
	validateSpawnArguments(file, args, o)
	boot.BeforeSpawn() // the children must see Node's RLIMIT_NOFILE
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, jsstr.ToUTF8(file))
	for _, arg := range args {
		argv = append(argv, jsstr.ToUTF8(arg))
	}
	env := nodeEnv()
	if o.Env != nil {
		env = envPairs(o.Env)
	}
	c := &Child{done: make(chan struct{})}
	fail := func(code string) *Child {
		if !asyncSpawnErrors[code] {
			jsexc.Throw(errors.New("spawn " + code))
		}
		c.exit = Exit{Err: errors.New("spawn " + file + " " + code)}
		close(c.done)
		return c
	}
	io, err := newAsyncStdio(o)
	if err != nil {
		return fail(nodefs.SystemErrorName(err))
	}
	proc, code := start(jsstr.ToUTF8(file), argv, env, io.child, jsstr.ToUTF8(o.Cwd), startOptions{verbatim: o.WindowsVerbatimArguments, show: o.WindowsShow})
	io.closeChild()
	if code != "" {
		io.closeParent()
		return fail(code)
	}
	c.proc = proc
	if io.stdin != nil {
		go func(stdin *os.File) {
			stdin.Write(o.Stdin) // an EPIPE here goes to the stdin 'error' listener the call sites add
			stdin.Close()        // end(): the child reads EOF
		}(io.stdin)
	}
	var streams []pipedStream
	for _, s := range []pipedStream{{io.stdout, o.OnStdout}, {io.stderr, o.OnStderr}} {
		if s.f != nil {
			streams = append(streams, s)
		}
	}
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		pump(streams)
	}()
	go func() {
		state, _ := proc.Wait()
		c.mu.Lock()
		c.reaped = true
		sent, killed := c.sent, c.killed
		c.mu.Unlock()
		exit := exitFrom(state, sent, killed)
		<-dispatched // 'close' waits for stdout and stderr to end
		c.exit = exit
		close(c.done)
	}()
	return c
}

// asyncStdio holds the parent's ends of the piped slots (nil for "ignore")
// and the descriptors the child gets.
type asyncStdio struct {
	stdin, stdout, stderr *os.File
	child                 []*os.File
}

func (s *asyncStdio) closeChild() {
	for _, f := range s.child {
		f.Close()
	}
}

func (s *asyncStdio) closeParent() {
	for _, f := range []*os.File{s.stdin, s.stdout, s.stderr} {
		if f != nil {
			f.Close()
		}
	}
}

// pipedStream is a piped child stream and its 'data' listener.
type pipedStream struct {
	f      *os.File
	listen func([]byte)
}

// pump delivers the streams' data to their listeners on one goroutine
// until every stream has ended. Where the kernel can say in which order the
// streams became readable (epoll, kqueue) it follows libuv's loop, so that
// data the child wrote to one stream before the other reaches the listeners
// in that order, as in Node (round 7 #7: two readers raced for the
// docker output budget). Elsewhere, and should the poller fail, each stream
// has its own reader and the listeners run in the order the reads finish.
func pump(streams []pipedStream) {
	if rest := pumpPolled(streams); len(rest) > 0 {
		pumpEach(rest)
	}
}

// pumpEach reads each stream on its own goroutine and dispatches the
// chunks on this one, in arrival order.
func pumpEach(streams []pipedStream) {
	chunks := make(chan chunk)
	var readers sync.WaitGroup
	for _, s := range streams {
		readers.Add(1)
		go readChunks(s.f, s.listen, chunks, &readers)
	}
	go func() {
		readers.Wait()
		close(chunks)
	}()
	for ch := range chunks {
		if ch.listen != nil {
			ch.listen(ch.data)
		}
	}
}

// chunk is one read from a piped stream, for its 'data' listener.
type chunk struct {
	listen func([]byte)
	data   []byte
}

// readChunks reads f in libuv's 64 KiB reads until EOF or an error, which
// for a socket the child holds ends the stream as well.
func readChunks(f *os.File, listen func([]byte), out chan<- chunk, done *sync.WaitGroup) {
	defer done.Done()
	defer f.Close()
	for {
		buf := make([]byte, segment)
		n, err := f.Read(buf)
		if n > 0 {
			out <- chunk{listen, buf[:n]}
		}
		if err != nil {
			return
		}
	}
}

// Wait returns once Node would have emitted 'close' (or 'error' when the
// spawn failed).
func (c *Child) Wait() Exit {
	<-c.done
	return c.exit
}

// Kill is child.kill(sig), "" meaning SIGTERM. It returns false once the
// child has exited (or never started), and when the signal could not be
// sent. An unknown signal name throws ERR_UNKNOWN_SIGNAL.
func (c *Child) Kill(sig string) bool {
	if sig == "" {
		sig = "SIGTERM"
	}
	num, ok := signalNumbers[sig]
	if !ok {
		jsexc.Throw(errors.New("Unknown signal: " + sig))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == nil || c.reaped {
		return false
	}
	if !killProcess(c.proc, num) {
		return false
	}
	c.sent, c.killed = true, sig
	return true
}

// validateSpawnArguments is the part of normalizeSpawnArguments that
// throws for FaultLine's arguments, in Node's order.
func validateSpawnArguments(file string, args []string, o SpawnOptions) {
	if strings.IndexByte(file, 0) >= 0 {
		throwInvalidArgValue("The argument 'file'", file, "must be a string without null bytes")
	}
	if file == "" {
		throwInvalidArgValue("The argument 'file'", file, "cannot be empty")
	}
	for i, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			throwInvalidArgValue(fmt.Sprintf("The argument 'args[%d]'", i), arg, "must be a string without null bytes")
		}
	}
	if strings.IndexByte(o.Cwd, 0) >= 0 {
		throwInvalidArgValue("The property 'options.cwd'", o.Cwd, "must be a string, Uint8Array, or URL without null bytes")
	}
	for _, entry := range o.Env {
		key, value, _ := strings.Cut(entry, "=")
		name := "The property 'options.env['" + key + "']'"
		if strings.IndexByte(key, 0) >= 0 {
			throwInvalidArgValue(name, key, "must be a string without null bytes")
		}
		if strings.IndexByte(value, 0) >= 0 {
			throwInvalidArgValue(name, value, "must be a string without null bytes")
		}
	}
}

// throwInvalidArgValue throws ERR_INVALID_ARG_VALUE (a TypeError), whose
// message cuts the inspected value at 128 UTF-16 units.
func throwInvalidArgValue(name, value, reason string) {
	jsexc.Throw(InvalidArgValue(name, value, reason))
}

// InvalidArgValue is the ERR_INVALID_ARG_VALUE error Node builds for a
// string value, as throwInvalidArgValue throws it.
func InvalidArgValue(name, value, reason string) error {
	inspected := jsstr.ToUTF16(inspectString(value))
	shown := jsstr.FromUTF16(inspected)
	if len(inspected) > 128 {
		shown = jsstr.FromUTF16(inspected[:128]) + "..."
	}
	return errors.New(name + " " + reason + ". Received " + shown)
}

// inspectString is util.inspect for a string at the top level: quoted
// with ', or " or ` when that avoids escaping a ', control characters,
// lone surrogates and the backslash escaped (strEscape), and split into
// one literal per line, joined with " +\n  ", when longer than 76 units.
func inspectString(s string) string {
	units := jsstr.ToUTF16(s)
	if len(units) > 16 && len(units) > 76 {
		var lines []string
		start := 0
		for i, u := range units {
			if u == '\n' {
				lines = append(lines, strEscape(units[start:i+1]))
				start = i + 1
			}
		}
		if start < len(units) || len(lines) == 0 {
			lines = append(lines, strEscape(units[start:]))
		}
		return strings.Join(lines, " +\n  ")
	}
	return strEscape(units)
}

func strEscape(units []uint16) string {
	quote := uint16('\'')
	if slices.Contains(units, '\'') {
		if !slices.Contains(units, '"') {
			quote = '"'
		} else if !slices.Contains(units, '`') && !containsDollarBrace(units) {
			quote = '`'
		}
	}
	out := []uint16{quote}
	for i := 0; i < len(units); i++ {
		u := units[i]
		var esc string
		switch {
		case u < 0x20 || u >= 0x7f && u <= 0x9f:
			esc = controlEscapes[u]
			if esc == "" {
				esc = fmt.Sprintf(`\x%02X`, u)
			}
		case u == '\'' && quote == '\'':
			esc = `\'`
		case u == '\\':
			esc = `\\`
		case u >= 0xd800 && u <= 0xdbff:
			if i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
				out = append(out, u, units[i+1])
				i++
				continue
			}
			esc = fmt.Sprintf(`\u%x`, u)
		case u >= 0xdc00 && u <= 0xdfff:
			esc = fmt.Sprintf(`\u%x`, u)
		default:
			out = append(out, u)
			continue
		}
		out = append(out, jsstr.ToUTF16(esc)...)
	}
	return jsstr.FromUTF16(append(out, quote))
}

var controlEscapes = map[uint16]string{'\b': `\b`, '\t': `\t`, '\n': `\n`, '\f': `\f`, '\r': `\r`}

func containsDollarBrace(units []uint16) bool {
	for i := 0; i+1 < len(units); i++ {
		if units[i] == '$' && units[i+1] == '{' {
			return true
		}
	}
	return false
}
