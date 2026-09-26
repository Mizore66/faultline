//go:build unix

package nodeproc

import (
	"os/exec"
	"runtime"
	"syscall"
)

// defaultPath is _PATH_DEFPATH, which libuv searches when PATH is unset.
var defaultPath = map[bool]string{true: "/usr/bin:/bin:/usr/sbin:/sbin", false: "/usr/bin:/bin"}[runtime.GOOS == "darwin"]

func access(path string) error { return syscall.Access(path, 0x1) } // X_OK

func hideWindow(*exec.Cmd) {}

var errnoNames = map[syscall.Errno]string{
	syscall.E2BIG: "E2BIG", syscall.EACCES: "EACCES", syscall.EAGAIN: "EAGAIN",
	syscall.EFAULT: "EFAULT", syscall.EINVAL: "EINVAL", syscall.EIO: "EIO",
	syscall.EISDIR: "EISDIR", syscall.ELOOP: "ELOOP", syscall.EMFILE: "EMFILE",
	syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENFILE: "ENFILE", syscall.ENOENT: "ENOENT",
	syscall.ENOEXEC: "ENOEXEC", syscall.ENOMEM: "ENOMEM", syscall.ENOTDIR: "ENOTDIR",
	syscall.EPERM: "EPERM", syscall.ETXTBSY: "ETXTBSY",
}
