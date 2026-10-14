//go:build !unix

package difftest

import "testing"

// setWriterUmask is a no-op: Windows has no permission bits to compare.
func setWriterUmask(testing.TB) {}
