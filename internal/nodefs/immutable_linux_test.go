package nodefs

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// setImmutable sets or clears FS_IMMUTABLE_FL (chattr +i), which makes
// unlink fail with EPERM for every user, root included. FS_IOC_SETFLAGS
// needs CAP_LINUX_IMMUTABLE; without it (CI runs as an unprivileged user
// with passwordless sudo) it runs `sudo -n chattr`. It returns why the
// test cannot run, or "".
func setImmutable(path string, on bool) string {
	if ioctlImmutable(path, on) == nil {
		return ""
	}
	flag := "-i"
	if on {
		flag = "+i"
	}
	if out, err := exec.Command("sudo", "-n", "chattr", flag, path).CombinedOutput(); err != nil {
		return "setting FS_IMMUTABLE_FL needs CAP_LINUX_IMMUTABLE or passwordless sudo and a filesystem with the flag: " + err.Error() + " " + string(out)
	}
	return ""
}

func ioctlImmutable(path string, on bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	const fsIocSetflags = 0x40086602 // _IOW('f', 2, long)
	var flags int32
	if on {
		flags = 0x10 // FS_IMMUTABLE_FL
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIocSetflags, uintptr(unsafe.Pointer(&flags))); errno != 0 {
		return errno
	}
	return nil
}
