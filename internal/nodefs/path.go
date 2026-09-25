package nodefs

import (
	"os"
	"path/filepath"
)

// Resolve is path.resolve.
func Resolve(p ...string) string {
	joined := filepath.Join(p...)
	if filepath.IsAbs(joined) {
		return filepath.Clean(joined)
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, joined)
}

// Join is path.join.
func Join(p ...string) string { return filepath.Join(p...) }

// Dirname is path.dirname.
func Dirname(p string) string { return filepath.Dir(p) }

// IsAbsolute is path.isAbsolute.
func IsAbsolute(p string) bool { return filepath.IsAbs(p) }

// Relative is path.relative: "" when both resolve to the same path.
func Relative(from, to string) string {
	r, err := filepath.Rel(Resolve(from), Resolve(to))
	if err != nil {
		return Resolve(to)
	}
	if r == "." {
		return ""
	}
	return r
}
