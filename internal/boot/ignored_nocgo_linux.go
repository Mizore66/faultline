//go:build linux && !cgo

package boot

// inheritedIgnored is not known without cgo: the Go runtime replaces the
// inherited dispositions before any Go code runs, and keeps the originals
// out of reach (KNOWN_DIFFERENCES).
func inheritedIgnored() (uint64, bool) { return 0, false }
