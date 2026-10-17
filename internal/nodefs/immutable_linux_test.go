package nodefs

import (
	"os"
	"syscall"
	"unsafe"
)

// setImmutable sets or clears FS_IMMUTABLE_FL (chattr +i) through
// FS_IOC_SETFLAGS. That needs CAP_LINUX_IMMUTABLE (root outside a
// container that drops it) and a filesystem with the flag (not tmpfs);
// otherwise it returns why the test cannot run. An immutable file makes
// unlink fail with EPERM for root too.
func setImmutable(path string, on bool) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	const fsIocSetflags = 0x40086602 // _IOW('f', 2, long)
	var flags int32
	if on {
		flags = 0x10 // FS_IMMUTABLE_FL
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIocSetflags, uintptr(unsafe.Pointer(&flags))); errno != 0 {
		return "FS_IOC_SETFLAGS(FS_IMMUTABLE_FL) failed (" + errno.Error() + "): setting the immutable flag needs CAP_LINUX_IMMUTABLE and a filesystem that supports it"
	}
	return ""
}
