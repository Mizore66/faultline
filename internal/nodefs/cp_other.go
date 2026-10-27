//go:build !unix

package nodefs

import "os"

func cpDie() { os.Exit(134) }
