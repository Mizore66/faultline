package nodefs

import "syscall"

func init() {
	unixCodes[syscall.ENONET] = "ENONET"
	unixCodes[syscall.EREMOTEIO] = "EREMOTEIO"
	unixCodes[syscall.EUNATCH] = "EUNATCH"
}
