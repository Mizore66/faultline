//go:build unix && !linux

package nodefs

import "os"

// envUnsafe is the test in Node's SafeGetenv (src/node_credentials.cc),
// where linux_at_secure is false off Linux: the real and effective user or
// group ids differ.
func envUnsafe() bool {
	return os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid()
}
