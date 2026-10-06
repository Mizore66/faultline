package nodefs

import (
	"os"
	"syscall"
	"unsafe"
)

//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr

// envUnsafe is the test in Node's SafeGetenv (src/node_credentials.cc): the
// environment is not consulted when getauxval(AT_SECURE) is set and the
// permitted capabilities are not exactly CAP_NET_BIND_SERVICE, or when the
// real and effective user or group ids differ.
func envUnsafe() bool {
	if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return true
	}
	atSecure := false
	for a := getAuxv(); len(a) >= 2; a = a[2:] {
		if a[0] == 23 { // AT_SECURE
			atSecure = a[1] != 0
		}
	}
	return atSecure && !hasOnlyNetBindService()
}

// hasOnlyNetBindService is Node's HasOnly(CAP_NET_BIND_SERVICE): capget
// (version 3) succeeds and the permitted set is exactly that capability.
func hasOnlyNetBindService() bool {
	header := struct {
		version uint32
		pid     int32
	}{0x20080522, int32(os.Getpid())}
	var data [2]struct{ effective, permitted, inheritable uint32 }
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		return false
	}
	const capNetBindService = 10
	return data[0].permitted == 1<<capNetBindService && data[1].permitted == 0
}
