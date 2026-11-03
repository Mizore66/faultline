//go:build windows

package nativefixture

import "os"

// Windows supplies an observed exit code, not a Unix signal description.
func observedSignal(state *os.ProcessState) string { return "" }
