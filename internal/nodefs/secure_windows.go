package nodefs

// envUnsafe: SafeGetenv always reads the environment on Windows.
func envUnsafe() bool { return false }
