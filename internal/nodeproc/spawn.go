// Package nodeproc ports the parts of Node 22's child_process.spawnSync that
// FaultLine's git runner observes: libuv's executable lookup, the maxBuffer
// limit (shared by stdout and stderr), and how status and error are reported.
package nodeproc

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

// read drains r until EOF or until the shared budget is exceeded. Like
// libuv's OnRead, each chunk is kept before the overflow check.
func (s *stream) read(r io.Reader, done *sync.WaitGroup) {
	defer done.Done()
	chunk := make([]byte, 64*1024)
	for {
		n, err := r.Read(chunk)
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
func SpawnSync(file string, args []string, maxBuffer int) Result {
	path, code := lookPath(file)
	if code != "" {
		return Result{Err: spawnError(file, code)}
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return Result{Err: spawnError(file, errnoCode(err))}
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return Result{Err: spawnError(file, errnoCode(err))}
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{file}, args...), Stdout: outW, Stderr: errW}
	hideWindow(cmd)
	startErr := cmd.Start()
	outW.Close()
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return Result{Err: spawnError(file, errnoCode(startErr))}
	}
	var closeOnce sync.Once
	closePipes := func() {
		closeOnce.Do(func() {
			outR.Close()
			errR.Close()
		})
	}
	o := &output{maxBuffer: maxBuffer}
	o.onOverrun = func() {
		// SyncProcessRunner::Kill: send killSignal (SIGTERM), close the pipes.
		if runtime.GOOS == "windows" {
			cmd.Process.Kill()
		} else {
			cmd.Process.Signal(syscall.SIGTERM)
		}
		closePipes()
	}
	stdout, stderr := &stream{out: o}, &stream{out: o}
	var readers sync.WaitGroup
	readers.Add(2)
	go stdout.read(outR, &readers)
	go stderr.read(errR, &readers)
	readers.Wait()
	cmd.Wait()
	closePipes()
	result := Result{Stdout: stdout.buf, Stderr: stderr.buf}
	o.mu.Lock()
	overflow := o.overflow
	o.mu.Unlock()
	if overflow {
		result.Err = spawnError(file, "ENOBUFS")
	}
	if ps := cmd.ProcessState; ps != nil && ps.Exited() && !overflow {
		status := ps.ExitCode()
		result.Status = &status
	}
	return result
}

// errnoCode names err's errno the way Node's ErrnoException does.
func errnoCode(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if name, ok := errnoNames[errno]; ok {
			return name
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return "ENOENT"
	}
	if errors.Is(err, os.ErrPermission) {
		return "EACCES"
	}
	return "UNKNOWN"
}

// lookPath finds file as libuv does, returning the path to execute or the
// errno code spawnSync reports.
func lookPath(file string) (string, string) {
	if runtime.GOOS == "windows" {
		return lookPathWindows(file)
	}
	return lookPathPosix(file)
}

// lookPathPosix is libuv's uv__execvpe search (after musl's execvpe): each
// PATH entry is tried in order, an empty entry means the working directory,
// EACCES is remembered while the search continues, and any other failure
// ends it.
func lookPathPosix(file string) (string, string) {
	if strings.Contains(file, "/") {
		return file, checkExec(file)
	}
	pathEnv, ok := os.LookupEnv("PATH")
	if !ok {
		pathEnv = defaultPath
	}
	seenEACCES := false
	for _, dir := range strings.Split(pathEnv, ":") {
		candidate := file // an empty entry execs the bare name, relative to cwd
		if dir != "" {
			candidate = dir + "/" + file
		}
		switch code := checkExec(candidate); code {
		case "":
			return candidate, ""
		case "EACCES":
			seenEACCES = true
		case "ENOENT", "ENOTDIR":
		default:
			return "", code
		}
	}
	if seenEACCES {
		return "", "EACCES"
	}
	return "", "ENOENT"
}

// checkExec predicts execve's errno for path: "" when it would run.
func checkExec(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return errnoCode(err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "EACCES"
	}
	if err := access(path); err != nil {
		return errnoCode(err)
	}
	return ""
}

// lookPathWindows is libuv's search_path for a bare file name (FaultLine
// only spawns "git"): the working directory first, then
// each PATH entry (quotes stripped, empty entries skipped). A name without an
// extension is tried with .com and then .exe only; with an extension, the
// literal name comes first.
func lookPathWindows(file string) (string, string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "ENOENT"
	}
	ext := false
	if i := strings.LastIndexByte(file, '.'); i >= 0 && i < len(file)-1 && !strings.ContainsAny(file[i:], `\/`) {
		ext = true
	}
	try := func(dir string) string {
		if !isAbsWindows(dir) {
			dir = cwd + `\` + dir
		}
		var names []string
		if ext {
			names = append(names, file)
		}
		names = append(names, file+".com", file+".exe")
		for _, name := range names {
			candidate := strings.TrimRight(dir, `\/`) + `\` + name
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		return ""
	}
	if found := try(cwd); found != "" {
		return found, ""
	}
	for _, dir := range splitWindowsPath(os.Getenv("PATH")) {
		if found := try(dir); found != "" {
			return found, ""
		}
	}
	return "", "ENOENT"
}

func isAbsWindows(p string) bool {
	return len(p) > 0 && (p[0] == '\\' || p[0] == '/') || len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// splitWindowsPath splits PATH like libuv's search_path: ';'-separated, a
// quoted entry may contain ';', and surrounding quotes are dropped.
func splitWindowsPath(path string) []string {
	var out []string
	for i := 0; i < len(path); {
		start := i
		if path[i] == '"' || path[i] == '\'' {
			if j := strings.IndexByte(path[i+1:], path[i]); j >= 0 {
				i += 1 + j
			} else {
				i = len(path)
			}
		}
		end := len(path)
		if j := strings.IndexByte(path[i:], ';'); j >= 0 {
			end = i + j
		}
		entry := path[start:end]
		i = end + 1
		if entry == "" {
			continue
		}
		if entry[0] == '"' || entry[0] == '\'' {
			entry = entry[1:]
		}
		if entry != "" && (entry[len(entry)-1] == '"' || entry[len(entry)-1] == '\'') {
			entry = entry[:len(entry)-1]
		}
		if entry != "" {
			out = append(out, entry)
		}
	}
	return out
}
