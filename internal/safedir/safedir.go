// Package safedir resolves directory segments without following
// user-controlled symlinks; only the macOS /var and /tmp aliases are trusted.
package safedir

import (
	"os"
	"path/filepath"
)

var darwinSystemDirectoryAliases = [][2]string{
	{"/var", "/private/var"},
	{"/tmp", "/private/tmp"},
}

// nodeRelative is node:path relative(), which returns "" where Go returns ".".
func nodeRelative(from, to string) string {
	rel, err := filepath.Rel(from, to)
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

// Port of src/safe-directory.ts:14 resolveSafeDirectorySegment. TS's null is
// ("", false). TS lets an lstat failure propagate as an exception; here it is
// reported as ("", false), which every caller treats as an unsafe segment.
func ResolveSafeDirectorySegment(path string) (string, bool) {
	stat, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	symlink := stat.Mode()&os.ModeSymlink != 0
	if stat.IsDir() && !symlink {
		return path, true
	}
	if !symlink || !isDarwin {
		return "", false
	}

	lexical, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	expectedTarget := ""
	for _, alias := range darwinSystemDirectoryAliases {
		if lexical == alias[0] {
			expectedTarget = alias[1]
			break
		}
	}
	if expectedTarget == "" {
		return "", false
	}
	real, err := filepath.EvalSymlinks(lexical)
	if err != nil || real != expectedTarget {
		return "", false
	}
	targetStat, err := os.Lstat(expectedTarget)
	if err != nil || !targetStat.IsDir() || targetStat.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return expectedTarget, true
}

// Port of src/safe-directory.ts:38 resolveTrustedSystemPath.
func ResolveTrustedSystemPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	if !isDarwin {
		return absolute
	}
	for _, alias := range darwinSystemDirectoryAliases {
		a, target := alias[0], alias[1]
		if absolute != a && !hasPrefix(absolute, a+"/") {
			continue
		}
		if got, ok := ResolveSafeDirectorySegment(a); !ok || got != target {
			return absolute
		}
		suffix := nodeRelative(a, absolute)
		if suffix != "" {
			return filepath.Join(target, suffix)
		}
		return target
	}
	return absolute
}

func hasPrefix(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

// Port of src/safe-directory.ts:53 relativeTrustedSystemPath.
func RelativeTrustedSystemPath(from, to string) string {
	return nodeRelative(ResolveTrustedSystemPath(from), ResolveTrustedSystemPath(to))
}
