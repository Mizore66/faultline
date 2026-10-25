package nodeproc

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// newStdio gives the child pipes for all three slots, with the 64 KiB
// buffers libuv gives its pipes. libuv's shutdown of the stdin pipe does not
// close it, so the parent keeps its end open until the child is done (a
// child reading stdin waits, as under Node). With input, endInput closes
// it once the input is written.
func newStdio(bool) (*stdio, error) {
	var parent, child [3]*os.File
	for i := range parent {
		r, w, err := pipe64k()
		if err != nil {
			for j := 0; j < i; j++ {
				parent[j].Close()
				child[j].Close()
			}
			return nil, err
		}
		if i == 0 {
			parent[i], child[i] = w, r
		} else {
			parent[i], child[i] = r, w
		}
	}
	return &stdio{stdin: parent[0], stdout: parent[1], stderr: parent[2], child: child[:]}, nil
}

// endInput ends the input option's write: libuv's shutdown of a pipe the
// parent only writes (no UV_HANDLE_READABLE) completes with close_pipe
// (uv__process_pipe_shutdown_req), so the child reads EOF.
func endInput(f *os.File) { f.Close() }

func pipe64k() (*os.File, *os.File, error) {
	var r, w syscall.Handle
	if err := syscall.CreatePipe(&r, &w, nil, 65536); err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(r), "|0"), os.NewFile(uintptr(w), "|1"), nil
}

// start is libuv's uv_spawn (src/win/process.c, 1.51): file is resolved
// with search_path against the cwd (dir when it is not "") and the PATH in
// env; the process is created hidden (windowsHide: true, so SW_HIDE and,
// since no stdio slot is inherited, CREATE_NO_WINDOW), with an inherited
// cwd of MAX_PATH or more shortened to its 8.3 form, and put in libuv's
// kill-on-close job object. CreateProcess errors go through libuv's
// uv_translate_sys_error table.
func start(file string, argv, env []string, files []*os.File, dir string, o startOptions) (*os.Process, string) {
	cwd := dir
	if dir == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil, nodefs.ErrnoCode(err)
		}
		// GetCurrentDirectoryW(0, NULL) counts the terminating NUL, so
		// libuv shortens a cwd of MAX_PATH-1 units or more, before the
		// search runs on it.
		if len(syscall.StringToUTF16(cwd)) >= syscall.MAX_PATH {
			short, err := shortPath(cwd)
			if err != nil {
				return nil, nodefs.ErrnoCode(err)
			}
			cwd, dir = short, short
		}
	}
	path, ok := searchPath(file, cwd, findPath(env), searchCwd(), fileExists)
	if !ok {
		return nil, "ENOENT"
	}
	const createNoWindow = 0x08000000
	sys := &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if o.show {
		// windowsHide: false: libuv passes SW_SHOWDEFAULT, which is what
		// leaving STARTF_USESHOWWINDOW unset means.
		sys = &syscall.SysProcAttr{}
	}
	if o.verbatim {
		// libuv's make_program_args with verbatim_arguments: every
		// argument, the program name included, copied as is and joined
		// with spaces.
		sys.CmdLine = strings.Join(argv, " ")
	}
	p, err := os.StartProcess(path, argv, &os.ProcAttr{Dir: dir, Env: env, Files: files, Sys: sys})
	if err != nil {
		return nil, nodefs.ErrnoCode(err)
	}
	assignToJob(p)
	return p, ""
}

// findPath is libuv's find_path: the value of the first entry of the
// child's environment whose name is PATH in any case.
func findPath(env []string) string {
	for _, entry := range env {
		if len(entry) >= 5 && strings.EqualFold(entry[:5], "PATH=") {
			return entry[5:]
		}
	}
	return ""
}

// shortPath is libuv's in-place GetShortPathNameW(cwd, cwd, cwd_len), whose
// buffer holds exactly the long path and its NUL. An 8.3 alias can be longer
// than its long name (".git" is "GIT~1"); GetShortPathNameW then returns the
// size it would need, the buffer keeps the long path, and libuv goes on with
// it. Only a return of 0 fails the spawn.
func shortPath(long string) (string, error) {
	return shortPathWith(long, syscall.GetShortPathName)
}

func shortPathWith(long string, get func(*uint16, *uint16, uint32) (uint32, error)) (string, error) {
	p, err := syscall.UTF16FromString(long)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, len(p))
	n, err := get(&p[0], &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	if int(n) >= len(buf) {
		return long, nil
	}
	return syscall.UTF16ToString(buf[:n]), nil
}

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	jobOnce                      sync.Once
	job                          uintptr
)

// assignToJob is uv__init_global_job_handle plus the assignment uv_spawn
// makes: one job object with BREAKAWAY_OK, SILENT_BREAKAWAY_OK,
// DIE_ON_UNHANDLED_EXCEPTION and KILL_ON_JOB_CLOSE holds fl and every git it
// starts, so git dies with fl. Failures are ignored where libuv would abort.
func assignToJob(p *os.Process) {
	jobOnce.Do(func() {
		h, _, _ := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			return
		}
		// JOBOBJECT_EXTENDED_LIMIT_INFORMATION; LimitFlags sits after two
		// LARGE_INTEGERs in BasicLimitInformation.
		var info [144]byte
		*(*uint32)(unsafe.Pointer(&info[16])) = 0x800 | 0x1000 | 0x400 | 0x2000
		if r, _, _ := procSetInformationJobObject.Call(h, 9 /* JobObjectExtendedLimitInformation */, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info))); r == 0 {
			return
		}
		self, _ := syscall.GetCurrentProcess()
		procAssignProcessToJobObject.Call(h, uintptr(self))
		job = h
	})
	if job == 0 {
		return
	}
	const processSetQuota, processTerminate = 0x0100, 0x0001
	h, _, _ := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(p.Pid))
	if h == 0 {
		return
	}
	procAssignProcessToJobObject.Call(job, h)
	syscall.CloseHandle(syscall.Handle(h))
}

var needCurrentDirectory = syscall.NewLazyDLL("kernel32.dll").NewProc("NeedCurrentDirectoryForExePathW")

// searchCwd is NeedCurrentDirectoryForExePathW(L""): false when
// NoDefaultCurrentDirectoryInExePath is set.
func searchCwd() bool {
	empty := [1]uint16{}
	if needCurrentDirectory.Find() != nil {
		_, set := os.LookupEnv("NoDefaultCurrentDirectoryInExePath")
		return !set
	}
	r, _, _ := needCurrentDirectory.Call(uintptr(unsafe.Pointer(&empty[0])))
	return r != 0
}

// fileExists is search_path_join_test's check: GetFileAttributesW, which
// does not follow a final symlink, and anything but a directory counts.
func fileExists(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(p)
	return err == nil && attrs&syscall.FILE_ATTRIBUTE_DIRECTORY == 0
}

// getenvUTF16 is GetEnvironmentVariableW(name), as uv_os_getenv calls it:
// the raw UTF-16 value, false when the lookup fails.
func getenvUTF16(name string) ([]uint16, bool) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, false
	}
	buf := make([]uint16, 256)
	for {
		n, err := syscall.GetEnvironmentVariable(p, &buf[0], uint32(len(buf)))
		if err != nil {
			return nil, false
		}
		if n < uint32(len(buf)) {
			return buf[:n], true
		}
		buf = make([]uint16, n)
	}
}
