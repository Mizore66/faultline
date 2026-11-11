//go:build !(linux || darwin)

package cli

import "testing"

// Windows console detection is covered by internal/term's console test; the
// PTY process cases need a Unix pseudo-terminal.
func runWitnessReviewTTYProcessTests(t *testing.T) {}
