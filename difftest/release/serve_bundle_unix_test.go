//go:build unix

package release

import "testing"

// standaloneConsoleHelper: POSIX interrupts are real SIGINT signals; no
// emitter executable is needed.
func standaloneConsoleHelper(t *testing.T, repo string) string { return "" }
