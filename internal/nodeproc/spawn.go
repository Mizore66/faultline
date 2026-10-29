// Package nodeproc ports the parts of Node 22's child_process.spawnSync that
// FaultLine's git runner observes: the environment and argv Node builds,
// libuv's executable lookup, its stdio (socketpairs on Unix), the maxBuffer
// limit (shared by stdout and stderr, read in 64 KiB segments), and how
// status and error are reported.
package nodeproc

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mizore66/faultline/internal/boot"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sigexit"
)

// Result mirrors spawnSync's { status, signal, stdout, stderr, error }.
type Result struct {
	Status         *int   // nil is JS null: killed by a signal, or never started
	Signal         string // signal: the name of the signal that ended the child ("SIGTERM"), or ""
	Stdout, Stderr []byte
	Err            error // "spawnSync <file> <CODE>", as Node's ErrnoException
}

// output collects both streams against one maxBuffer budget, like
// SyncProcessRunner::IncrementBufferSizeAndCheckOverflow. stopped is set once
// SyncProcessRunner::Kill closed the pipes (an overflow, a timeout): no read
// is kept after that logical close, but the chunk that overflowed was.
type output struct {
	mu        sync.Mutex
	total     int
	maxBuffer int
	stopped   bool
	onOverrun func()
}

// stream is one pipe's buffer. The byte slice is a named field so no
// io.ReaderFrom is promoted past the budget check.
type stream struct {
	out *output
	buf []byte
}

// segment is SyncProcessOutputBuffer's size: OnAlloc only offers the space
// left in the current 64 KiB segment, so no read crosses a segment boundary.
const segment = 64 * 1024

// read drains r until EOF or until the shared budget is exceeded. Like
// libuv's OnRead, each chunk is kept before the overflow check.
func (s *stream) read(r *os.File, done *sync.WaitGroup) {
	defer done.Done()
	chunk := make([]byte, segment)
	for {
		n, err := r.Read(chunk[:segment-len(s.buf)%segment])
		if n > 0 {
			s.out.mu.Lock()
			if s.out.stopped {
				s.out.mu.Unlock()
				return
			}
			s.buf = append(s.buf, chunk[:n]...)
			s.out.total += n
			if s.out.maxBuffer > 0 && s.out.total > s.out.maxBuffer {
				s.out.stopped = true
				s.out.mu.Unlock()
				s.out.onOverrun()
				return
			}
			s.out.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func spawnError(file, code string) error { return errors.New("spawnSync " + file + " " + code) }

// SpawnSync is spawnSync(file, args, { encoding: "buffer", maxBuffer,
// shell: false }) with the inherited environment and working directory.
// Arguments are JS strings: they reach the child as UTF-8 with each lone
// surrogate replaced by U+FFFD, as Node encodes them.
func SpawnSync(file string, args []string, maxBuffer int) Result {
	return SpawnSyncWith(file, args, SpawnSyncOptions{MaxBuffer: maxBuffer})
}

// SpawnSyncOptions are the spawnSync options FaultLine's call sites use,
// always with shell: false and windowsHide: true.
type SpawnSyncOptions struct {
	// Env is the env option as "KEY=value" pairs in object order, passed
	// verbatim (callers encode JS strings with jsstr.ToUTF8 where Node
	// would); on Windows the pairs are sorted and deduplicated as
	// normalizeSpawnArguments does. nil inherits process.env (nodeEnv).
	Env []string
	// Input is the input option, written to the child's stdin, which is
	// then shut down (the child reads EOF), when HasInput is set.
	Input    []byte
	HasInput bool
	// Cwd is the cwd option; "" inherits fl's working directory.
	Cwd string
	// MaxBuffer is maxBuffer; 0 is Node's default, 1024*1024. The limit is
	// shared by stdout and stderr (SyncProcessRunner counts both against
	// one total), as TestSpawnSyncMatchesNode pins.
	MaxBuffer int
	// Timeout is the timeout option; 0 is none. When it runs out the child
	// gets killSignal (SIGTERM; TerminateProcess on Windows), the pipes are
	// closed, and the error is "spawnSync <file> ETIMEDOUT" unless ENOBUFS
	// was recorded first.
	Timeout time.Duration
	// WindowsShow is windowsHide: false, spawnSync's default, which
	// src/git-snapshot.ts:18 keeps (no effect off Windows).
	WindowsShow bool

	// ignoreStdin is stdio[0] "ignore" (execFileSync's call sites): the
	// child's stdin is the null device instead of a pipe.
	ignoreStdin bool
}

// defaultMaxBuffer is child_process's MAX_BUFFER, spawnSync's default.
const defaultMaxBuffer = 1024 * 1024

// SpawnSyncWith is spawnSync(file, args, { encoding: "buffer", env, input,
// cwd, maxBuffer, timeout, shell: false, windowsHide: true }). A failed
// write of Input (the child exited without reading it) is reported like
// Node's pipe error ("spawnSync <file> EPIPE"), unless ENOBUFS or ETIMEDOUT
// is.
func SpawnSyncWith(file string, args []string, o SpawnSyncOptions) Result {
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
	maxBuffer := o.MaxBuffer
	if maxBuffer == 0 {
		maxBuffer = defaultMaxBuffer
	}
	if refusesBatchFile(jsstr.ToUTF8(file)) {
		// ParseOptions fails before the loop runs: status, stdout and
		// stderr are null.
		return Result{Err: spawnError(file, "EINVAL")}
	}
	io, err := newStdio(o.HasInput, o.ignoreStdin)
	if err != nil {
		return Result{Err: spawnError(file, nodefs.SystemErrorName(err))}
	}
	proc, code := start(jsstr.ToUTF8(file), argv, env, io.child, jsstr.ToUTF8(o.Cwd), startOptions{show: o.WindowsShow})
	io.closeChild()
	if code != "" {
		io.closeParent()
		return Result{Err: spawnError(file, code)}
	}
	var closeOnce sync.Once
	closePipes := func() { closeOnce.Do(io.closeParent) }
	run := &syncRunner{}
	out := &output{maxBuffer: maxBuffer}
	// SyncProcessRunner::Kill, after SetError(code): only the first error
	// is kept, the child is killed once, with killSignal (SIGTERM), and the
	// pipes are closed. Once the loop is over (finished) the timer is
	// closed and can no longer fire.
	kill := func(code string) {
		run.mu.Lock()
		if run.finished {
			run.mu.Unlock()
			return
		}
		if run.err == "" {
			run.err = code
		}
		if run.killed {
			run.mu.Unlock()
			return
		}
		run.killed = true
		run.mu.Unlock()
		// Kill closes every pipe at once: a read that already returned but
		// is not yet accepted under out.mu must not be kept (run.mu is not
		// held here, so no lock order is set between the two).
		out.mu.Lock()
		out.stopped = true
		out.mu.Unlock()
		var sent bool
		if runtime.GOOS == "windows" {
			sent = proc.Kill() == nil
		} else {
			proc.Signal(syscall.SIGTERM)
		}
		run.mu.Lock()
		run.sent = sent
		run.mu.Unlock()
		closePipes()
	}
	out.onOverrun = func() { kill("ENOBUFS") }
	// The kill timer runs from the spawn (uv_timer_start, then uv_spawn);
	// the input is written on its own goroutine, so a child that never
	// reads it still times out.
	var timer *time.Timer
	if o.Timeout > 0 {
		timer = time.AfterFunc(o.Timeout, func() { kill("ETIMEDOUT") })
	}
	// SyncProcessStdioPipe::Start: uv_write the input, then uv_shutdown.
	// Only the first pipe error is kept (SetPipeError).
	var pipeErr error
	pipeErrAfterKill := false
	var writer sync.WaitGroup
	if o.HasInput {
		writer.Add(1)
		go func() {
			defer writer.Done()
			if _, err := io.stdin.Write(o.Input); err != nil {
				run.mu.Lock()
				pipeErr, pipeErrAfterKill = err, run.killed
				run.mu.Unlock()
			}
			endInput(io.stdin)
		}()
	}
	stdout, stderr := &stream{out: out}, &stream{out: out}
	var readers sync.WaitGroup
	readers.Add(2)
	go stdout.read(io.stdout, &readers)
	go stderr.read(io.stderr, &readers)
	readers.Wait()
	state, _ := proc.Wait()
	writer.Wait()
	run.mu.Lock()
	run.finished = true
	runnerErr, killed, sent := run.err, run.killed, run.sent
	run.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	closePipes()
	var ws syscall.WaitStatus
	signaled := false
	if state != nil {
		ws, signaled = state.Sys().(syscall.WaitStatus)
		signaled = signaled && ws.Signaled()
	}
	if signaled && !killed {
		sigexit.ChildKilled(ws.Signal())
	}
	result := Result{Stdout: stdout.buf, Stderr: stderr.buf}
	// GetError: the runner's own error (ENOBUFS, ETIMEDOUT) wins over a
	// pipe error.
	if pipeErr != nil {
		code, failsStart := inputWriteError(pipeErr, runtime.GOOS)
		if failsStart && (runnerErr == "" || runnerErr == "ETIMEDOUT" && !pipeErrAfterKill) {
			// SyncProcessRunner::TryInitializeAndRunLoop returns as soon
			// as a pipe fails to start, before uv_run: no exit status and
			// no output (status, stdout and stderr are null), and no timer
			// fires.
			return Result{Err: spawnError(file, code)}
		}
		result.Err = spawnError(file, code)
	}
	if runnerErr != "" {
		result.Err = spawnError(file, runnerErr)
	}
	switch {
	case runtime.GOOS == "windows" && sent:
		// uv_process_kill records the signal it sent as exit_signal, which
		// the exit callback reports in place of the exit code, whatever asked
		// for the kill (ENOBUFS and ETIMEDOUT alike: spawn_sync.cc Kill,
		// win/process.c uv_process_kill). A kill that failed or found the
		// child gone keeps the exit status.
		result.Signal = "SIGTERM"
	case signaled:
		result.Signal = waitSignalName(ws)
	case state != nil && state.Exited():
		// Node reports the exit code whenever the child exited normally,
		// even alongside ENOBUFS or ETIMEDOUT.
		status := state.ExitCode()
		result.Status = &status
	}
	return result
}

// syncRunner is the SyncProcessRunner state the kill paths share.
type syncRunner struct {
	mu       sync.Mutex
	err      string // error_: the first runner error code (ENOBUFS, ETIMEDOUT)
	killed   bool   // killed_: Kill ran (fl's own kill, not reported to sigexit)
	sent     bool   // on Windows, TerminateProcess succeeded
	finished bool   // the loop is over: the timer is closed
}

// ExecFileSync is execFileSync(file, args, { encoding, stdio: ["ignore",
// "pipe", "pipe"], windowsHide: true }) with o's env, cwd, maxBuffer and
// timeout, as checkExecSyncError reports a failure (Node 22
// lib/child_process.js): a spawn error (ENOENT, ENOBUFS, ETIMEDOUT) as is,
// "spawnSync <file> <CODE>"; a non-zero status or a signal as "Command
// failed: <file> <args joined by " ">", followed by "\n" and stderr
// (decoded as UTF-8) when stderr is not empty. stdout is returned as
// captured in every case.
func ExecFileSync(file string, args []string, o SpawnSyncOptions) ([]byte, error) {
	o.ignoreStdin, o.HasInput, o.Input = true, false, nil
	r := SpawnSyncWith(file, args, o)
	if r.Err != nil {
		return r.Stdout, r.Err
	}
	if r.Status == nil || *r.Status != 0 {
		msg := jsexc.Concat("Command failed: ", jsexc.Join(append([]string{file}, args...), " "))
		if len(r.Stderr) > 0 {
			msg = jsexc.Concat(msg, "\n", jsstr.DecodeUTF8(r.Stderr))
		}
		return r.Stdout, errors.New(msg)
	}
	return r.Stdout, nil
}

// errorNoData is Windows' ERROR_NO_DATA.
const errorNoData = syscall.Errno(232)

// inputWriteError names a failed write of the input option as Node's
// SyncProcessStdioPipe reports it on goos, and whether the failure stops
// spawnSync before its loop runs.
//
// On Unix, libuv delivers every uv_write error to the write callback, so the
// loop runs, the child is waited for, and the error is the pipe error.
//
// On Windows (libuv 1.51 src/win/stream.c and pipe.c), uv_write returns an
// error from WriteFile itself through uv_translate_write_sys_error, which
// makes ERROR_BROKEN_PIPE and ERROR_NO_DATA EPIPE, and
// SyncProcessStdioPipe::Start's failure ends spawnSync at once. A write that
// was queued and fails later completes through uv__process_pipe_write_req
// with plain uv_translate_sys_error, where ERROR_BROKEN_PIPE is EOF. The
// named-pipe driver fails a write begun after the reader closed with
// STATUS_PIPE_CLOSING (ERROR_NO_DATA) and completes a pending write broken
// by the reader's close with STATUS_PIPE_BROKEN (ERROR_BROKEN_PIPE). Go's
// blocking write cannot tell the two apart otherwise, so ERROR_NO_DATA is
// the synchronous EPIPE, and ERROR_BROKEN_PIPE keeps the table's EOF.
func inputWriteError(err error, goos string) (code string, failsStart bool) {
	var errno syscall.Errno
	if goos == "windows" && errors.As(err, &errno) && errno == errorNoData {
		return "EPIPE", true
	}
	return nodefs.SystemErrorName(err), false
}

// stdio holds the parent's ends (stdin shut down for writing, stdout and
// stderr for reading) and the child's ends.
type stdio struct {
	stdin, stdout, stderr *os.File
	child                 []*os.File
}

func (s *stdio) closeChild() {
	for _, f := range s.child {
		f.Close()
	}
}

func (s *stdio) closeParent() {
	if s.stdin != nil { // nil: stdin "ignore"
		s.stdin.Close()
	}
	s.stdout.Close()
	s.stderr.Close()
}

// nodeEnv is the envPairs normalizeSpawnArguments builds from process.env:
// entries without "=" are skipped, keys and values are decoded as UTF-8 with
// replacement, each key appears once with the value getenv finds for it (the
// first entry), and a key whose bytes don't round-trip finds no value and is
// dropped. Keys that are array indices ("0" to "4294967294") are dropped:
// {...process.env} copies them through V8's indexed-property path, which
// process.env does not implement. On Linux an empty key is dropped too
// (getenv("") finds nothing). On Windows, process.env also leaves out the
// hidden per-drive variables ("=C:"), whose names start with '='.
func nodeEnv() []string {
	environ := syscall.Environ()
	if runtime.GOOS == "windows" {
		// Keys come from the block (uv_os_environ), values from looking
		// each kept key up again (uv_os_getenv, GetEnvironmentVariableW,
		// which matches case-insensitively and returns the first match).
		names := make([]string, 0, len(environ))
		for _, entry := range environ {
			i := strings.IndexByte(entry[min(1, len(entry)):], '=') + min(1, len(entry))
			if entry == "" || entry[0] == '=' || i <= 0 || isArrayIndex(entry[:i]) {
				continue
			}
			names = append(names, entry[:i])
		}
		env := make([]string, 0, len(names))
		for _, name := range windowsEnvDedupe(names) {
			if units, ok := getenvUTF16(name); ok {
				env = append(env, name+"="+decodeEnvValue(units))
			}
		}
		return env
	}
	first := map[string]string{}
	for _, entry := range environ {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				if _, ok := first[entry[:i]]; !ok {
					first[entry[:i]] = entry[i+1:]
				}
				break
			}
		}
	}
	seen := map[string]bool{}
	env := make([]string, 0, len(environ))
	for _, entry := range environ {
		i := 0
		for i < len(entry) && entry[i] != '=' {
			i++
		}
		if i == len(entry) {
			continue
		}
		key := nodefs.DecodeUTF8([]byte(entry[:i]))
		if seen[key] || isArrayIndex(key) || key == "" && runtime.GOOS == "linux" {
			continue
		}
		seen[key] = true
		value, ok := first[key]
		if !ok {
			continue
		}
		env = append(env, key+"="+nodefs.DecodeUTF8([]byte(value)))
	}
	return env
}

// windowsEnvDedupe is normalizeSpawnArguments on Windows, where names are
// case-insensitive: the names are sorted (by UTF-16 code units, as
// Array.prototype.sort does) and only the first of those that uppercase
// alike is kept. strings.ToUpper stands in for toUpperCase; it differs only
// where a letter uppercases to several (ß to SS).
func windowsEnvDedupe(names []string) []string {
	sort.SliceStable(names, func(i, j int) bool {
		return slices.Compare(jsstr.ToUTF16(names[i]), jsstr.ToUTF16(names[j])) < 0
	})
	seen := map[string]bool{}
	out := names[:0]
	for _, name := range names {
		upper := strings.ToUpper(name)
		if seen[upper] {
			continue
		}
		seen[upper] = true
		out = append(out, name)
	}
	return out
}

// decodeEnvValue is a value from GetEnvironmentVariableW as Node reads it:
// libuv converts the UTF-16 to WTF-8 (a lone surrogate becomes 3 bytes)
// and V8 decodes that as UTF-8, one U+FFFD per invalid byte.
func decodeEnvValue(units []uint16) string {
	return nodefs.DecodeUTF8([]byte(jsstr.FromUTF16(units)))
}

// lookupEnv is getenv over an envPairs list: the first match wins.
func lookupEnv(env []string, key string) (string, bool) {
	for _, entry := range env {
		if len(entry) > len(key) && entry[len(key)] == '=' && entry[:len(key)] == key {
			return entry[len(key)+1:], true
		}
	}
	return "", false
}

// isArrayIndex reports whether key is a canonical array index: a decimal
// integer below 2^32-1 without leading zeros.
func isArrayIndex(key string) bool {
	if key == "" || len(key) > 10 || key[0] == '0' && len(key) > 1 {
		return false
	}
	var n uint64 // ten digits overflow a 32-bit int
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
		n = n*10 + uint64(key[i]-'0')
	}
	return n < 1<<32-1
}

// Getenv is process.env[name] for one name: on Windows GetEnvironmentVariableW
// (case-insensitive) decoded as decodeEnvValue does, elsewhere getenv(3)
// (the first entry) decoded from UTF-8 with replacement. ok is false when
// the value is undefined.
func Getenv(name string) (value string, ok bool) {
	if runtime.GOOS == "windows" {
		units, ok := getenvUTF16(name)
		if !ok {
			return "", false
		}
		return decodeEnvValue(units), true
	}
	raw, ok := syscall.Getenv(name)
	if !ok {
		return "", false
	}
	return nodefs.DecodeUTF8([]byte(raw)), true
}

// startOptions are the Windows-only spawn options (windowsVerbatimArguments,
// windowsHide: false); POSIX ignores them.
type startOptions struct{ verbatim, show bool }
