//go:build unix

package nodeproc

import (
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// defaultPath is _PATH_DEFPATH, which libuv searches when PATH is unset
// (glibc and the macOS SDK both define "/usr/bin:/bin").
const defaultPath = "/usr/bin:/bin"

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

// start is libuv's uv__execvpe (Linux, from musl's execvpe) and
// uv__spawn_resolve_and_spawn (macOS): a name with a slash is executed as
// is; otherwise every PATH entry is tried by actually executing it. ENOENT
// and ENOTDIR move on, EACCES is remembered and the search continues, any
// other error ends it. Entries at least min(len(PATH)+1, PATH_MAX) long are
// skipped, and the result is EACCES if one was seen, else the last errno.
func start(file string, argv, env []string, files []*os.File) (*os.Process, string) {
	closeInheritedFds()
	attr := &os.ProcAttr{Env: env, Files: files}
	exec := func(path string) (*os.Process, string) {
		p, err := os.StartProcess(path, argv, attr)
		if err != nil {
			return nil, nodefs.ErrnoCode(err)
		}
		return p, ""
	}
	if strings.Contains(file, "/") {
		return exec(file)
	}
	path, ok := lookupEnv(env, "PATH")
	if !ok {
		path = defaultPath
	}
	if len(file) > nameMax {
		return nil, "ENAMETOOLONG"
	}
	limit := min(len(path), pathMax-1) + 1
	seenEACCES, last := false, "ENOENT"
	for _, dir := range strings.Split(path, ":") {
		if len(dir) >= limit {
			continue
		}
		candidate := file // an empty entry execs the bare name, relative to cwd
		if dir != "" {
			candidate = dir + "/" + file
		}
		p, code := exec(candidate)
		switch code {
		case "":
			return p, ""
		case "EACCES":
			seenEACCES = true
		case "ENOENT", "ENOTDIR":
		default:
			return nil, code
		}
		last = code
	}
	if seenEACCES {
		return nil, "EACCES"
	}
	return nil, last
}
