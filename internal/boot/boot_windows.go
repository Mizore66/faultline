package boot

// BeforeSpawn: Windows has no RLIMIT_NOFILE raise to complete.
func BeforeSpawn() {}
