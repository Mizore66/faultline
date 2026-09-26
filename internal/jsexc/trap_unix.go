//go:build unix

package jsexc

import (
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

func trap() { sigexit.Die(syscall.SIGTRAP) }
