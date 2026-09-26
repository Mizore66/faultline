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
	"sync"
	"syscall"

	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
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
		return Result{Err: spawnError(file, nodefs.ErrnoCode(err))}
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
	o.onOverrun = func() {
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
	result := Result{Stdout: stdout.buf, Stderr: stderr.buf}
	o.mu.Lock()
	overflow := o.overflow
	o.mu.Unlock()
	if overflow {
		result.Err = spawnError(file, "ENOBUFS")
	}
	if state != nil && state.Exited() && !overflow {
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
// dropped. Nil (inherit) on Windows, whose environment is already UTF-16.
func nodeEnv() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	environ := syscall.Environ()
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
		if seen[key] {
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

// lookupEnv is getenv over an envPairs list: the first match wins.
func lookupEnv(env []string, key string) (string, bool) {
	for _, entry := range env {
		if len(entry) > len(key) && entry[len(key)] == '=' && entry[:len(key)] == key {
			return entry[len(key)+1:], true
		}
	}
	return "", false
}
