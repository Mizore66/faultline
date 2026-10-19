package nodefs

import (
	"io/fs"
	"os"
	"strings"
)

// Stat is statSync(path).
func Stat(path string) (fs.FileInfo, error) {
	info, err := statRaw(sysPath(path), true)
	if err != nil {
		return nil, wrap(err, "stat", path)
	}
	return info, nil
}

// RealpathSync is fs.realpathSync(p): Node's JavaScript implementation
// (lib/fs.js realpathSync), not the native one. It walks the resolved path
// one component at a time, lstat'ing each new prefix, and on a symbolic
// link stats it (so a loop or a dangling link fails there, with syscall
// "stat"), reads it, and restarts from the resolved target. Errors print
// the prefix that failed. Two quirks are kept: once a prefix is known to
// be a real directory it is not lstat'ed again, and on that path the walk
// stops early when the last stat'ed link target was a FIFO or a socket
// (Node checks the shared statValues there). Node also caches link targets
// by dev:ino to skip the repeat stat and readlink of a link it already
// read; that only matters if a link changes during the walk, so it is not
// reproduced.
func RealpathSync(p string) (string, error) {
	p = Resolve(p)
	knownHard := map[string]bool{}
	current := realpathSplitRoot(p)
	base := current
	pos := len(current)
	if isWindows {
		if _, err := statRaw(sysPath(base), false); err != nil {
			return "", wrap(err, "lstat", base)
		}
		knownHard[base] = true
	}
	var lastStat fs.FileMode // the shared statValues of the last binding.stat
	for pos < len(p) {
		next := realpathNextPart(p, pos)
		previous := current
		if next == -1 {
			last := p[pos:]
			current += last
			base = previous + last
			pos = len(p)
		} else {
			current += p[pos : next+1]
			base = previous + p[pos:next]
			pos = next + 1
		}
		if knownHard[base] {
			if t := lastStat.Type(); t == fs.ModeNamedPipe || t == fs.ModeSocket {
				break
			}
			continue
		}
		info, err := statRaw(sysPath(base), false)
		if err != nil {
			return "", wrap(err, "lstat", base)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			knownHard[base] = true
			continue
		}
		target, err := statRaw(sysPath(base), true)
		if err != nil {
			return "", wrap(err, "stat", base)
		}
		lastStat = target.Mode()
		link, err := os.Readlink(sysPath(base))
		if err != nil {
			return "", wrap(err, "readlink", base)
		}
		resolvedLink := Resolve(previous, DecodeUTF8([]byte(link)))
		p = Resolve(resolvedLink, p[pos:])
		current = realpathSplitRoot(p)
		base = current
		pos = len(current)
		if isWindows && !knownHard[base] {
			if _, err := statRaw(sysPath(base), false); err != nil {
				return "", wrap(err, "lstat", base)
			}
			knownHard[base] = true
		}
	}
	return p, nil
}

// realpathSplitRoot is lib/fs.js splitRoot: the leading slashes on POSIX;
// on Windows /^(?:[a-zA-Z]:|[\\/]{2}[^\\/]+[\\/][^\\/]+)?[\\/]*/.
func realpathSplitRoot(s string) string {
	if !isWindows {
		return s[:len(s)-len(strings.TrimLeft(s, "/"))]
	}
	isSep := func(c byte) bool { return c == '\\' || c == '/' }
	i := 0
	if len(s) >= 2 && isLetter(s[0]) && s[1] == ':' {
		i = 2
	} else if len(s) >= 2 && isSep(s[0]) && isSep(s[1]) {
		j := 2
		for j < len(s) && !isSep(s[j]) {
			j++
		}
		if j > 2 && j < len(s) {
			k := j + 1
			for k < len(s) && !isSep(s[k]) {
				k++
			}
			if k > j+1 {
				i = k
			}
		}
	}
	for i < len(s) && isSep(s[i]) {
		i++
	}
	return s[:i]
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// realpathNextPart is lib/fs.js nextPart: the next separator at or after i,
// or -1.
func realpathNextPart(p string, i int) int {
	for ; i < len(p); i++ {
		if p[i] == '/' || isWindows && p[i] == '\\' {
			return i
		}
	}
	return -1
}
