package nodeproc

import (
	"os"
	"strconv"
	"sync"
	"syscall"
)

var cloexecOnce sync.Once

// closeInheritedFds matches libuv's POSIX_SPAWN_CLOEXEC_DEFAULT on macOS:
// descriptors fl inherited (3 and up) do not reach git. Go's own
// descriptors are already close-on-exec, so marking the inherited ones once
// is enough.
func closeInheritedFds() {
	cloexecOnce.Do(func() {
		names, err := readFdNames()
		if err != nil {
			return
		}
		for _, name := range names {
			if fd, err := strconv.Atoi(name); err == nil && fd >= 3 {
				syscall.CloseOnExec(fd)
			}
		}
	})
}

func readFdNames() ([]string, error) {
	d, err := os.Open("/dev/fd")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.Readdirnames(-1)
}
