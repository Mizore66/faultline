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
	"sync/atomic"
	"syscall"

	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sigexit"
)

// Result mirrors spawnSync's { status, stdout, stderr, error }.
type Result struct {
	Status         *int // nil is JS null: killed by a signal, or never started
	Stdout, Stderr []byte
	Err            error // "spawnSync <file> <CODE>", as Node's ErrnoException
}

// output collects both streams against one maxBuffer budget, like
// SyncProcessRunner::IncrementBufferSizeAndCheckOverflow.
type output struct {
	mu        sync.Mutex
	total     int
	maxBuffer int
	overflow  bool
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
			if s.out.overflow {
				s.out.mu.Unlock()
				return
			}
			s.buf = append(s.buf, chunk[:n]...)
			s.out.total += n
			if s.out.maxBuffer > 0 && s.out.total > s.out.maxBuffer {
				s.out.overflow = true
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
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, jsstr.ToUTF8(file))
	for _, arg := range args {
		argv = append(argv, jsstr.ToUTF8(arg))
	}
	io, err := newStdio()
	if err != nil {
		return Result{Err: spawnError(file, nodefs.SystemErrorName(err))}
	}
	proc, code := start(jsstr.ToUTF8(file), argv, nodeEnv(), io.child)
	io.closeChild()
	if code != "" {
		io.closeParent()
		return Result{Err: spawnError(file, code)}
	}
	var closeOnce sync.Once
	closePipes := func() { closeOnce.Do(io.closeParent) }
	o := &output{maxBuffer: maxBuffer}
	var killed atomic.Bool
	o.onOverrun = func() {
		killed.Store(true)
		// SyncProcessRunner::Kill: send killSignal (SIGTERM), close the pipes.
		if runtime.GOOS == "windows" {
			proc.Kill()
		} else {
			proc.Signal(syscall.SIGTERM)
		}
		closePipes()
	}
	stdout, stderr := &stream{out: o}, &stream{out: o}
	var readers sync.WaitGroup
	readers.Add(2)
	go stdout.read(io.stdout, &readers)
	go stderr.read(io.stderr, &readers)
	readers.Wait()
	state, _ := proc.Wait()
	closePipes()
	if state != nil && !killed.Load() {
		if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			sigexit.ChildKilled()
		}
	}
	result := Result{Stdout: stdout.buf, Stderr: stderr.buf}
	o.mu.Lock()
	overflow := o.overflow
	o.mu.Unlock()
	if overflow {
		result.Err = spawnError(file, "ENOBUFS")
	}
	// Node reports the exit code whenever the child exited normally, even
	// alongside ENOBUFS.
	if state != nil && state.Exited() {
		status := state.ExitCode()
		result.Status = &status
	}
	return result
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
	s.stdin.Close()
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
		env := make([]string, 0, len(environ))
		for _, entry := range environ {
			i := strings.IndexByte(entry[min(1, len(entry)):], '=') + min(1, len(entry))
			if entry == "" || entry[0] == '=' || i <= 0 || isArrayIndex(entry[:i]) {
				continue
			}
			env = append(env, entry)
		}
		return windowsEnvDedupe(env)
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
func windowsEnvDedupe(env []string) []string {
	name := func(entry string) string {
		i := strings.IndexByte(entry[min(1, len(entry)):], '=') + min(1, len(entry))
		return entry[:i]
	}
	sort.SliceStable(env, func(i, j int) bool {
		return slices.Compare(jsstr.ToUTF16(name(env[i])), jsstr.ToUTF16(name(env[j]))) < 0
	})
	seen := map[string]bool{}
	out := env[:0]
	for _, entry := range env {
		upper := strings.ToUpper(name(entry))
		if seen[upper] {
			continue
		}
		seen[upper] = true
		out = append(out, entry)
	}
	return out
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
	n := 0
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
		n = n*10 + int(key[i]-'0')
	}
	return n < 1<<32-1
}
