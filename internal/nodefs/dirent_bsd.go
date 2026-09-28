//go:build unix && !linux

package nodefs

import "syscall"

const oDirectory = syscall.O_DIRECTORY
