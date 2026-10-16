//go:build linux && !cgo

package boot

import "github.com/Mizore66/faultline/internal/sigexit"

// inheritedIgnored is not known without cgo: the Go runtime replaces the
// inherited dispositions before any Go code runs, and keeps the originals
// out of reach (KNOWN_DIFFERENCES).
func inheritedIgnored() (sigexit.Set, bool) { return sigexit.Set{}, false }
