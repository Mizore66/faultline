package nodeproc

import (
	"os/exec"
	"syscall"
)

var defaultPath = ""

func access(string) error { return nil }

// hideWindow is spawnSync's windowsHide: true.
func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// errnoNames follows libuv's uv_translate_sys_error for CreateProcess failures.
var errnoNames = map[syscall.Errno]string{
	2: "ENOENT", 3: "ENOENT", 5: "EPERM", 8: "ENOMEM", 14: "ENOMEM",
	87: "EINVAL", 123: "ENOENT", 193: "EFTYPE", 206: "ENAMETOOLONG",
}
