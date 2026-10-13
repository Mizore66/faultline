//go:build linux || darwin

package nodefs

import "syscall"

var win32Codes map[uintptr]string

// unixCodes is libuv's uv_err_name for system errnos: every UV_ERRNO_MAP
// name the platform's errno.h defines (libuv 1.51, include/uv.h). An errno
// missing here is UNKNOWN to uvException and "Unknown system error -N" to
// ErrnoException.
var unixCodes = map[syscall.Errno]string{
	syscall.E2BIG: "E2BIG", syscall.EACCES: "EACCES", syscall.EADDRINUSE: "EADDRINUSE",
	syscall.EADDRNOTAVAIL: "EADDRNOTAVAIL", syscall.EAFNOSUPPORT: "EAFNOSUPPORT",
	syscall.EAGAIN: "EAGAIN", syscall.EALREADY: "EALREADY", syscall.EBADF: "EBADF",
	syscall.EBUSY: "EBUSY", syscall.ECANCELED: "ECANCELED", syscall.ECONNABORTED: "ECONNABORTED",
	syscall.ECONNREFUSED: "ECONNREFUSED", syscall.ECONNRESET: "ECONNRESET",
	syscall.EDESTADDRREQ: "EDESTADDRREQ", syscall.EEXIST: "EEXIST", syscall.EFAULT: "EFAULT",
	syscall.EFBIG: "EFBIG", syscall.EHOSTUNREACH: "EHOSTUNREACH", syscall.EINTR: "EINTR",
	syscall.EINVAL: "EINVAL", syscall.EIO: "EIO", syscall.EISCONN: "EISCONN",
	syscall.EISDIR: "EISDIR", syscall.ELOOP: "ELOOP", syscall.EMFILE: "EMFILE",
	syscall.EMSGSIZE: "EMSGSIZE", syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENETDOWN: "ENETDOWN",
	syscall.ENETUNREACH: "ENETUNREACH", syscall.ENFILE: "ENFILE", syscall.ENOBUFS: "ENOBUFS",
	syscall.ENODEV: "ENODEV", syscall.ENOENT: "ENOENT", syscall.ENOMEM: "ENOMEM",
	syscall.ENOPROTOOPT: "ENOPROTOOPT", syscall.ENOSPC: "ENOSPC", syscall.ENOSYS: "ENOSYS",
	syscall.ENOTCONN: "ENOTCONN", syscall.ENOTDIR: "ENOTDIR", syscall.ENOTEMPTY: "ENOTEMPTY",
	syscall.ENOTSOCK: "ENOTSOCK", syscall.ENOTSUP: "ENOTSUP", syscall.EOVERFLOW: "EOVERFLOW",
	syscall.EPERM: "EPERM", syscall.EPIPE: "EPIPE", syscall.EPROTO: "EPROTO",
	syscall.EPROTONOSUPPORT: "EPROTONOSUPPORT", syscall.EPROTOTYPE: "EPROTOTYPE",
	syscall.ERANGE: "ERANGE", syscall.EROFS: "EROFS", syscall.ESHUTDOWN: "ESHUTDOWN",
	syscall.ESPIPE: "ESPIPE", syscall.ESRCH: "ESRCH", syscall.ETIMEDOUT: "ETIMEDOUT",
	syscall.ETXTBSY: "ETXTBSY", syscall.EXDEV: "EXDEV", syscall.ENXIO: "ENXIO",
	syscall.EMLINK: "EMLINK", syscall.EHOSTDOWN: "EHOSTDOWN",
	syscall.ENOTTY: "ENOTTY", syscall.EILSEQ: "EILSEQ", syscall.ESOCKTNOSUPPORT: "ESOCKTNOSUPPORT",
	syscall.ENODATA: "ENODATA", syscall.ENOEXEC: "ENOEXEC",
}
