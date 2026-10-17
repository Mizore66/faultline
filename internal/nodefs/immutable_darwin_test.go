package nodefs

import "syscall"

// setImmutable sets or clears UF_IMMUTABLE (chflags uchg), which an
// unprivileged owner may set and which makes unlink fail with EPERM for
// every user, root included. It returns why the test cannot run, or "".
func setImmutable(path string, on bool) string {
	flags := 0
	if on {
		flags = 0x2 // UF_IMMUTABLE
	}
	if err := syscall.Chflags(path, flags); err != nil {
		return "chflags: " + err.Error()
	}
	return ""
}
