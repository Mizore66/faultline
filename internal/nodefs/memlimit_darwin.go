package nodefs

import (
	"encoding/binary"
	"os"
	"sync"
	"syscall"
)

// memoryLimit is the most memory Node could hold here: physical memory
// (hw.memsize; syscall.Sysctl drops the value's trailing zero bytes).
var memoryLimit = sync.OnceValue(func() uint64 {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return ^uint64(0)
	}
	var b [8]byte
	copy(b[:], s)
	return binary.LittleEndian.Uint64(b[:])
})

// outOfMemory ends fl as macOS ends a process that exhausts memory:
// SIGKILL, no output.
func outOfMemory() {
	syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
