//go:build !unix

package difftest

import "errors"

func mkfifo(string) error { return errors.New("no named pipes in the file system") }
