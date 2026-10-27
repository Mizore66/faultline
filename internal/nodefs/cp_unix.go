//go:build unix

package nodefs

import (
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

func cpDie() { sigexit.Die(syscall.SIGABRT) }
