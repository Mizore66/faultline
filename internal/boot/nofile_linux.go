package boot

// rlimInfinity is Linux's RLIM_INFINITY, all ones (syscall.RLIM_INFINITY
// is the untyped -1 there).
const rlimInfinity = ^uint64(0)
