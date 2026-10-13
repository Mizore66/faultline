//go:build !linux && !darwin

package nodefs

import "os"

// memoryLimit: not measured on other systems; a file that never ends is read
// until fl is stopped (Node would run out of memory first).
func memoryLimit() uint64 { return ^uint64(0) }

func outOfMemory() { os.Exit(137) }
