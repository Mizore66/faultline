package nodeproc

import (
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// newStdio gives the child pipes for all three slots, with the 64 KiB
// buffers libuv gives its pipes. libuv's shutdown of the stdin pipe does not
// close it, so the parent keeps its end open until the child is done (a
// child reading stdin waits, as under Node).
func newStdio() (*stdio, error) {
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

func pipe64k() (*os.File, *os.File, error) {
	var r, w syscall.Handle
	if err := syscall.CreatePipe(&r, &w, nil, 65536); err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(r), "|0"), os.NewFile(uintptr(w), "|1"), nil
}

// start is libuv's uv_spawn (src/win/process.c, 1.51): file is resolved
// with search_path; the process is created hidden (windowsHide: true, so
// SW_HIDE and, since no stdio slot is inherited, CREATE_NO_WINDOW), with a
// cwd of MAX_PATH or more shortened to its 8.3 form, and put in libuv's
// kill-on-close job object. CreateProcess errors go through libuv's
// uv_translate_sys_error table.
func start(file string, argv, env []string, files []*os.File) (*os.Process, string) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nodefs.ErrnoCode(err)
	}
	// GetCurrentDirectoryW(0, NULL) counts the terminating NUL, so libuv
	// shortens a cwd of MAX_PATH-1 units or more, before the search runs
	// on it.
	dir := ""
	if len(syscall.StringToUTF16(cwd)) >= syscall.MAX_PATH {
		short, err := shortPath(cwd)
		if err != nil {
			return nil, nodefs.ErrnoCode(err)
		}
		cwd, dir = short, short
	}
	path, ok := searchPath(file, cwd, os.Getenv("PATH"), searchCwd(), fileExists)
	if !ok {
		return nil, "ENOENT"
	}
	const createNoWindow = 0x08000000
	p, err := os.StartProcess(path, argv, &os.ProcAttr{Dir: dir, Env: env, Files: files,
		Sys: &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}})
	if err != nil {
		return nil, nodefs.ErrnoCode(err)
	}
	assignToJob(p)
	return p, ""
}

func shortPath(long string) (string, error) {
	p, err := syscall.UTF16FromString(long)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, len(p))
	n, err := syscall.GetShortPathName(&p[0], &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
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
