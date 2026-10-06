package boot

import "syscall"

// rlimInfinity is darwin's RLIM_INFINITY, 2^63 - 1 (sys/resource.h), the
// default hard RLIMIT_NOFILE: with it Node bisects the soft limit below
// 2^20 (1,048,575 on macOS 26) instead of copying the hard limit.
const rlimInfinity = uint64(syscall.RLIM_INFINITY)
