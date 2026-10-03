//go:build unix

package nodeproc

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// defaultPath is the search path when PATH is unset: glibc's CS_PATH on
// Linux (Node's official builds link glibc), _PATH_DEFPATH on macOS.
var defaultPath = map[bool]string{true: "/bin:/usr/bin", false: "/usr/bin:/bin"}[runtime.GOOS == "linux"]

// pathMax is PATH_MAX: libuv skips PATH entries at least this long (or
// longer than PATH itself).
var pathMax = map[bool]int{true: 1024, false: 4096}[runtime.GOOS != "linux"]

const nameMax = 255

// newStdio is libuv's uv__process_init_stdio for three UV_CREATE_PIPE
// slots: each is a socketpair. Node shuts down its end of stdin at once, so
// the child reads EOF.
func newStdio() (*stdio, error) {
	var fds [3][2]int
	var err error
	syscall.ForkLock.RLock()
	for i := range fds {
		if fds[i], err = syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0); err != nil {
			for j := 0; j < i; j++ {
				syscall.Close(fds[j][0])
				syscall.Close(fds[j][1])
			}
			syscall.ForkLock.RUnlock()
			return nil, err
		}
		syscall.CloseOnExec(fds[i][0])
		syscall.CloseOnExec(fds[i][1])
		// uv__process_init_stdio sizes both ends' buffers to 64 KiB.
		for _, fd := range fds[i] {
			syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 65536)
			syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 65536)
		}
	}
	syscall.ForkLock.RUnlock()
	parent := make([]*os.File, 3)
	child := make([]*os.File, 3)
	for i := range fds {
		syscall.SetNonblock(fds[i][0], true)
		parent[i] = os.NewFile(uintptr(fds[i][0]), "|0")
		child[i] = os.NewFile(uintptr(fds[i][1]), "|1")
	}
	syscall.Shutdown(fds[0][0], syscall.SHUT_WR)
	return &stdio{stdin: parent[0], stdout: parent[1], stderr: parent[2], child: child}, nil
}

// start runs file the way libuv's child does. On Linux libuv calls the
// C library's execvp, which Node's official builds take from glibc (see
// execvpGlibc); on macOS it is libuv's own uv__spawn_resolve_and_spawn loop
// over posix_spawn (see execvpDarwin).
func start(file string, argv, env []string, files []*os.File) (*os.Process, string) {
	closeInheritedFds()
	attr := &os.ProcAttr{Env: env, Files: files}
	exec := func(path string, argv []string) (*os.Process, syscall.Errno) {
		p, err := os.StartProcess(path, argv, attr)
		if err != nil {
			var errno syscall.Errno
			if errors.As(err, &errno) {
				return nil, errno
			}
			return nil, syscall.EINVAL
		}
		return p, 0
	}
	path, ok := lookupEnv(env, "PATH")
	if !ok {
		path = defaultPath
	}
	var p *os.Process
	var errno syscall.Errno
	if runtime.GOOS == "linux" {
		p, errno = execvpGlibc(file, argv, path, exec)
	} else {
		p, errno = execvpDarwin(file, argv, path, exec)
	}
	if errno != 0 {
		return nil, nodefs.SystemErrorName(errno)
	}
	return p, ""
}

type execFunc func(path string, argv []string) (*os.Process, syscall.Errno)

// execvpGlibc is glibc 2.36's __execvpe_common (posix/execvpe.c): a name
// with a slash is executed as is; otherwise each PATH entry is tried. A
// candidate that fails with ENOEXEC is run as a script by /bin/sh
// (maybe_script_execute), and that result counts instead. EACCES is
// remembered; ENOENT, ESTALE, ENOTDIR, ENODEV and ETIMEDOUT move on; any
// other error ends the search. An entry at least min(len(PATH), 4095)+1
// long is skipped, leaving the search on its ':' so that an empty entry
// (the bare name, relative to the cwd) is tried next; when it is the last
// entry the search ends with whatever errno the child last saw, which Go
// cannot reproduce (KNOWN_DIFFERENCES.md) and reports as ENOENT.
func execvpGlibc(file string, argv []string, path string, exec execFunc) (*os.Process, syscall.Errno) {
	try := func(candidate string) (*os.Process, syscall.Errno) {
		p, errno := exec(candidate, argv)
		if errno == syscall.ENOEXEC {
			shArgv := append([]string{"/bin/sh", candidate}, argv[min(1, len(argv)):]...)
			p, errno = exec("/bin/sh", shArgv)
		}
		return p, errno
	}
	if file == "" {
		return nil, syscall.ENOENT
	}
	if strings.Contains(file, "/") {
		return try(file)
	}
	if len(file) >= nameMax {
		return nil, syscall.ENAMETOOLONG
	}
	pathLen := min(len(path), pathMax-1) + 1
	gotEACCES := false
	errno := syscall.ENOENT
	for p := 0; ; {
		sub := strings.IndexByte(path[p:], ':')
		if sub < 0 {
			sub = len(path)
		} else {
			sub += p
		}
		if sub-p >= pathLen {
			if sub == len(path) {
				break
			}
			p = sub // the ':' itself: an empty entry comes next
			continue
		}
		candidate := file
		if sub > p {
			candidate = path[p:sub] + "/" + file
		}
		var proc *os.Process
		proc, errno = try(candidate)
		if errno == 0 {
			return proc, 0
		}
		switch errno {
		case syscall.EACCES:
			gotEACCES = true
		case syscall.ENOENT, syscall.ESTALE, syscall.ENOTDIR, syscall.ENODEV, syscall.ETIMEDOUT:
		default:
			return nil, errno
		}
		if sub == len(path) {
			break
		}
		p = sub + 1
	}
	if gotEACCES {
		return nil, syscall.EACCES
	}
	return nil, errno
}

// execvpDarwin is libuv's uv__spawn_resolve_and_spawn (macOS): a name with
// a slash is spawned as is; otherwise every PATH entry is tried by spawning
// it. ENOENT and ENOTDIR move on, EACCES is remembered and the search
// continues, any other error ends it. Entries at least min(len(PATH)+1,
// PATH_MAX) long are skipped, and the result is EACCES if one was seen,
// else the last errno.
func execvpDarwin(file string, argv []string, path string, exec execFunc) (*os.Process, syscall.Errno) {
	if strings.Contains(file, "/") {
		return exec(file, argv)
	}
	if len(file) > nameMax {
		return nil, syscall.ENAMETOOLONG
	}
	limit := min(len(path), pathMax-1) + 1
	seenEACCES, last := false, syscall.ENOENT
	for _, dir := range strings.Split(path, ":") {
		if len(dir) >= limit {
			continue
		}
		candidate := file // an empty entry execs the bare name, relative to cwd
		if dir != "" {
			candidate = dir + "/" + file
		}
		p, errno := exec(candidate, argv)
		switch errno {
		case 0:
			return p, 0
		case syscall.EACCES:
			seenEACCES = true
		case syscall.ENOENT, syscall.ENOTDIR:
		default:
			return nil, errno
		}
		last = errno
	}
	if seenEACCES {
		return nil, syscall.EACCES
	}
	return nil, last
}

// getenvUTF16 is only used on Windows.
func getenvUTF16(string) ([]uint16, bool) { return nil, false }
