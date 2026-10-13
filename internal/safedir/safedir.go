// Package safedir resolves directory segments without following
// user-controlled symlinks; only the macOS /var and /tmp aliases are trusted.
package safedir

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Mizore66/faultline/internal/nodefs"
)

var darwinSystemDirectoryAliases = [][2]string{
	{"/var", "/private/var"},
	{"/tmp", "/private/tmp"},
}

// Port of src/safe-directory.ts:14 resolveSafeDirectorySegment. TS's null is
// ("", false, nil). The first lstat is outside TS's try/catch, so its error
// propagates in Node's format (e.g. "EACCES: permission denied, lstat '<p>'");
// the realpath and target lstat failures are caught in TS and give null.
func ResolveSafeDirectorySegment(path string) (string, bool, error) {
	stat, err := nodefs.Lstat(path)
	if err != nil {
		return "", false, err
	}
	symlink := stat.Mode()&os.ModeSymlink != 0
	if stat.IsDir() && !symlink {
		return path, true, nil
	}
	if !symlink || !isDarwin {
		return "", false, nil
	}

	lexical := nodefs.Resolve(path)
	expectedTarget := ""
	for _, alias := range darwinSystemDirectoryAliases {
		if lexical == alias[0] {
			expectedTarget = alias[1]
			break
		}
	}
	if expectedTarget == "" {
		return "", false, nil
	}
	real, err := filepath.EvalSymlinks(lexical)
	if err != nil || real != expectedTarget {
		return "", false, nil
	}
	targetStat, err := nodefs.Lstat(expectedTarget)
	if err != nil || !targetStat.IsDir() || targetStat.Mode()&os.ModeSymlink != 0 {
		return "", false, nil
	}
	return expectedTarget, true, nil
}

// Port of src/safe-directory.ts:38 resolveTrustedSystemPath. The alias lstat
// is not caught in TS, so its error propagates.
func ResolveTrustedSystemPath(path string) (string, error) {
	absolute := nodefs.Resolve(path)
	if !isDarwin {
		return absolute, nil
	}
	for _, alias := range darwinSystemDirectoryAliases {
		a, target := alias[0], alias[1]
		if absolute != a && !strings.HasPrefix(absolute, a+"/") {
			continue
		}
		got, ok, err := ResolveSafeDirectorySegment(a)
		if err != nil {
			return "", err
		}
		if !ok || got != target {
			return absolute, nil
		}
		if suffix := nodefs.Relative(a, absolute); suffix != "" {
			return nodefs.Join(target, suffix), nil
		}
		return target, nil
	}
	return absolute, nil
}

// Port of src/safe-directory.ts:53 relativeTrustedSystemPath.
func RelativeTrustedSystemPath(from, to string) (string, error) {
	f, err := ResolveTrustedSystemPath(from)
	if err != nil {
		return "", err
	}
	t, err := ResolveTrustedSystemPath(to)
	if err != nil {
		return "", err
	}
	return nodefs.Relative(f, t), nil
}
