//go:build !unix

package difftest

// setWriterUmask is a no-op: Windows has no permission bits to compare.
func setWriterUmask() {}
