//go:build faultline_test && !linux && !darwin && !windows

package difftest

import (
	"errors"
	"os"
	"time"
)

// lutimes sets a path's atime and mtime to at without following a final
// symbolic link (Node's lutimesSync), for the set-mtime mutation. Here
// only a path that is not a symbolic link is supported.
func lutimes(path string, at time.Time) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("lutimes: a symbolic link's own times are not supported on this platform")
	}
	return os.Chtimes(path, at, at)
}
