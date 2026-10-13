package nodefs

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// memoryLimit is the most memory Node could hold here: physical memory, or
// less when the process's cgroup (v2 memory.max, v1 limit_in_bytes) caps it.
var memoryLimit = sync.OnceValue(func() uint64 {
	var info syscall.Sysinfo_t
	limit := ^uint64(0)
	if syscall.Sysinfo(&info) == nil {
		limit = uint64(info.Totalram) * uint64(info.Unit)
	}
	for _, f := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		if b, err := os.ReadFile(f); err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && n < limit {
				limit = n
			}
		}
	}
	return limit
})

// outOfMemory ends fl as the OOM killer ends Node: SIGKILL, no output.
func outOfMemory() {
	syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}
