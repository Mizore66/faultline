//go:build !windows && !linux && !darwin

package nodefs

import "syscall"

var win32Codes map[uintptr]string

// unixCodes names the errnos FaultLine's fs and spawn calls can see.
var unixCodes = map[syscall.Errno]string{
	syscall.E2BIG: "E2BIG", syscall.EACCES: "EACCES", syscall.EAGAIN: "EAGAIN",
	syscall.EBADF: "EBADF", syscall.EBUSY: "EBUSY", syscall.EEXIST: "EEXIST",
	syscall.EFAULT: "EFAULT", syscall.EINVAL: "EINVAL", syscall.EIO: "EIO",
	syscall.EISDIR: "EISDIR", syscall.ELOOP: "ELOOP", syscall.EMFILE: "EMFILE",
	syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENFILE: "ENFILE", syscall.ENOENT: "ENOENT",
	syscall.ENOEXEC: "ENOEXEC", syscall.ENOMEM: "ENOMEM", syscall.ENOSPC: "ENOSPC",
	syscall.ENOTDIR: "ENOTDIR", syscall.ENOTEMPTY: "ENOTEMPTY", syscall.EPERM: "EPERM",
	syscall.ERANGE: "ERANGE", syscall.EROFS: "EROFS", syscall.ETXTBSY: "ETXTBSY",
	syscall.EXDEV: "EXDEV", syscall.EPIPE: "EPIPE", syscall.ENOBUFS: "ENOBUFS",
}
