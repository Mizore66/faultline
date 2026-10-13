package nodefs

import "syscall"

func init() { unixCodes[syscall.EFTYPE] = "EFTYPE" }
