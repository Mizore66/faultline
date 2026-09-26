package nodeproc

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// newStdio gives the child pipes for all three slots; the parent closes its
// end of stdin at once, so the child reads EOF.
func newStdio() (*stdio, error) {
	var parent, child [3]*os.File
	for i := range parent {
		r, w, err := os.Pipe()
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
	parent[0].Close()
	return &stdio{stdin: parent[0], stdout: parent[1], stderr: parent[2], child: child[:]}, nil
}

// start resolves file with libuv's search_path and creates the process
// hidden (windowsHide: true). CreateProcess errors go through libuv's
// uv_translate_sys_error table.
func start(file string, argv, env []string, files []*os.File) (*os.Process, string) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nodefs.ErrnoCode(err)
	}
	path, ok := searchPath(file, cwd, os.Getenv("PATH"), searchCwd(), fileExists)
	if !ok {
		return nil, "ENOENT"
	}
	p, err := os.StartProcess(path, argv, &os.ProcAttr{Env: env, Files: files, Sys: &syscall.SysProcAttr{HideWindow: true}})
	if err != nil {
		return nil, nodefs.ErrnoCode(err)
	}
	return p, ""
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
