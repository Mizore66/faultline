package nodefs

import (
	"os"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// This file ports Node 22's lib/path.js. Go's path/filepath differs from it
// in ways that change which directory a bundle path names: filepath.IsAbs is
// false for `\x` on Windows, filepath.Join keeps drive-relative `C:foo` under
// the cwd, and os.Getwd returns the logical $PWD instead of the physical cwd.
// All separators and dots are ASCII, so byte offsets stand in for UTF-16 ones.

var isWindows = runtime.GOOS == "windows"

// Cwd is process.cwd(): the physical working directory (libuv uv_cwd), or
// the uv_cwd error Node throws when it is gone.
func Cwd() (string, error) {
	cwd, err := syscall.Getwd()
	if err != nil {
		return "", &Error{Code: codeOf(err), Syscall: "uv_cwd", NoPath: true}
	}
	return cwd, nil
}

func mustCwd() string { return jsexc.Must(Cwd()) }

func isPosixSep(c byte) bool { return c == '/' }
func isWinSep(c byte) bool   { return c == '/' || c == '\\' }
func isDeviceRoot(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// at is StringPrototypeCharCodeAt with NaN (out of range) as 0.
func at(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"COM¹": true, "COM²": true, "COM³": true, "LPT¹": true, "LPT²": true, "LPT³": true,
}

// isWindowsReservedName is path.js isWindowsReservedName. Only ASCII letters
// upper-case into the list (¹²³ upper-case to themselves), so ASCII
// upper-casing matches String.prototype.toUpperCase here.
func isWindowsReservedName(path string, colonIndex int) bool {
	if colonIndex < 0 { // indexOf found no ':' so this is slice(0, -1): drop one UTF-16 unit
		units := jsstr.ToUTF16(path)
		return len(units) > 0 && windowsReservedNames[asciiUpper(jsstr.FromUTF16(units[:len(units)-1]))]
	}
	return windowsReservedNames[asciiUpper(path[:min(colonIndex, len(path))])]
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// normalizeString resolves . and .. segments (path.js normalizeString).
func normalizeString(path string, allowAboveRoot bool, separator byte, isSep func(byte) bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if isSep(code) {
			break
		} else {
			code = '/'
		}
		if isSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, separator)
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res += string(separator) + ".."
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res += string(separator) + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}
	return res
}

// PosixResolve is path.posix.resolve with process.cwd() supplied by cwd.
func PosixResolve(cwd func() string, args ...string) string {
	if len(args) == 0 || len(args) == 1 && (args[0] == "" || args[0] == ".") {
		if c := cwd(); at(c, 0) == '/' {
			return c
		}
	}
	resolvedPath := ""
	resolvedAbsolute := false
	for i := len(args) - 1; i >= 0 && !resolvedAbsolute; i-- {
		path := args[i]
		if path == "" {
			continue
		}
		resolvedPath = path + "/" + resolvedPath
		resolvedAbsolute = path[0] == '/'
	}
	if !resolvedAbsolute {
		c := cwd()
		resolvedPath = c + "/" + resolvedPath
		resolvedAbsolute = at(c, 0) == '/'
	}
	resolvedPath = normalizeString(resolvedPath, !resolvedAbsolute, '/', isPosixSep)
	if resolvedAbsolute {
		return "/" + resolvedPath
	}
	if resolvedPath != "" {
		return resolvedPath
	}
	return "."
}

// PosixNormalize is path.posix.normalize.
func PosixNormalize(path string) string {
	if path == "" {
		return "."
	}
	isAbsolute := path[0] == '/'
	trailingSeparator := path[len(path)-1] == '/'
	path = normalizeString(path, !isAbsolute, '/', isPosixSep)
	if path == "" {
		if isAbsolute {
			return "/"
		}
		if trailingSeparator {
			return "./"
		}
		return "."
	}
	if trailingSeparator {
		path += "/"
	}
	if isAbsolute {
		return "/" + path
	}
	return path
}

func nonEmpty(args []string) []string {
	var out []string
	for _, a := range args {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// PosixJoin is path.posix.join.
func PosixJoin(args ...string) string {
	parts := nonEmpty(args)
	if len(parts) == 0 {
		return "."
	}
	return PosixNormalize(strings.Join(parts, "/"))
}

// PosixRelative is path.posix.relative.
func PosixRelative(cwd func() string, from, to string) string {
	if from == to {
		return ""
	}
	from = PosixResolve(cwd, from)
	to = PosixResolve(cwd, to)
	if from == to {
		return ""
	}
	const fromStart, toStart = 1, 1
	fromEnd := len(from)
	fromLen := fromEnd - fromStart
	toLen := len(to) - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fromCode := from[fromStart+i]
		if fromCode != to[toStart+i] {
			break
		} else if fromCode == '/' {
			lastCommonSep = i
		}
	}
	if i == length {
		if toLen > length {
			if to[toStart+i] == '/' {
				return to[toStart+i+1:]
			}
			if i == 0 {
				return to[toStart+i:]
			}
		} else if fromLen > length {
			if from[fromStart+i] == '/' {
				lastCommonSep = i
			} else if i == 0 {
				lastCommonSep = 0
			}
		}
	}
	out := ""
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || from[i] == '/' {
			if out == "" {
				out = ".."
			} else {
				out += "/.."
			}
		}
	}
	return out + to[toStart+lastCommonSep:]
}

// PosixDirname is path.posix.dirname.
func PosixDirname(path string) string {
	if path == "" {
		return "."
	}
	hasRoot := path[0] == '/'
	end := -1
	matchedSlash := true
	for i := len(path) - 1; i >= 1; i-- {
		if path[i] == '/' {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if hasRoot {
			return "/"
		}
		return "."
	}
	if hasRoot && end == 1 {
		return "//"
	}
	return path[:end]
}

// uncRoot matches `\\server\share` at the start of path (both separators
// accepted). ok is false when path does not start with such a root.
// firstPart is "server"; j is the index after "share".
func uncRoot(path string) (firstPart string, last, j int, ok bool) {
	n := len(path)
	j = 2
	last = j
	for j < n && !isWinSep(path[j]) {
		j++
	}
	if !(j < n && j != last) {
		return "", 0, 0, false
	}
	firstPart = path[last:j]
	last = j
	for j < n && isWinSep(path[j]) {
		j++
	}
	if !(j < n && j != last) {
		return "", 0, 0, false
	}
	last = j
	for j < n && !isWinSep(path[j]) {
		j++
	}
	if !(j == n || j != last) {
		return "", 0, 0, false
	}
	return firstPart, last, j, true
}

// Win32Resolve is path.win32.resolve; cwd is process.cwd() and env is
// process.env (for the per-drive `=C:` variables).
func Win32Resolve(cwd func() string, env func(string) string, args ...string) string {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false
	for i := len(args) - 1; i >= -1; i-- {
		var path string
		if i >= 0 {
			path = args[i]
			if path == "" {
				continue
			}
		} else if resolvedDevice == "" {
			path = cwd()
			if len(args) == 0 || len(args) == 1 && (args[0] == "" || args[0] == ".") && isWinSep(at(path, 0)) {
				if !isWindows {
					path = strings.ReplaceAll(path, "/", `\`)
				}
				return path
			}
		} else {
			path = env("=" + resolvedDevice)
			if path == "" {
				path = cwd()
			}
			if strings.ToLower(path[:min(2, len(path))]) != strings.ToLower(resolvedDevice) && at(path, 2) == '\\' {
				path = resolvedDevice + `\`
			}
		}
		n := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		code := at(path, 0)
		if n == 1 {
			if isWinSep(code) {
				rootEnd = 1
				isAbsolute = true
			}
		} else if isWinSep(code) {
			isAbsolute = true
			if isWinSep(at(path, 1)) {
				if firstPart, last, j, ok := uncRoot(path); ok {
					if firstPart != "." && firstPart != "?" {
						device = `\\` + firstPart + `\` + path[last:j]
						rootEnd = j
					} else {
						device = `\\` + firstPart
						rootEnd = 4
					}
				}
			} else {
				rootEnd = 1
			}
		} else if isDeviceRoot(code) && at(path, 1) == ':' {
			device = path[:2]
			rootEnd = 2
			if n > 2 && isWinSep(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		}
		if device != "" {
			if resolvedDevice != "" {
				if jsLower(device) != jsLower(resolvedDevice) {
					continue
				}
			} else {
				resolvedDevice = device
			}
		}
		if resolvedAbsolute {
			if resolvedDevice != "" {
				break
			}
		} else {
			resolvedTail = path[min(rootEnd, n):] + `\` + resolvedTail
			resolvedAbsolute = isAbsolute
			if isAbsolute && resolvedDevice != "" {
				break
			}
		}
	}
	resolvedTail = normalizeString(resolvedTail, !resolvedAbsolute, '\\', isWinSep)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if s := resolvedDevice + resolvedTail; s != "" {
		return s
	}
	return "."
}

// Win32Normalize is path.win32.normalize.
func Win32Normalize(path string) string {
	n := len(path)
	if n == 0 {
		return "."
	}
	rootEnd := 0
	var device *string
	setDevice := func(d string) { device = &d }
	isAbsolute := false
	code := path[0]
	if n == 1 {
		if code == '/' {
			return `\`
		}
		return path
	}
	if isWinSep(code) {
		isAbsolute = true
		if isWinSep(path[1]) {
			if firstPart, last, j, ok := uncRoot(path); ok {
				if firstPart == "." || firstPart == "?" {
					setDevice(`\\` + firstPart)
					rootEnd = 4
					colonIndex := strings.IndexByte(path, ':')
					possibleDevice := path[min(4, n):max(min(colonIndex+1, n), min(4, n))]
					if isWindowsReservedName(possibleDevice, len(possibleDevice)-1) {
						setDevice(`\\?\` + possibleDevice)
						rootEnd = 4 + len(possibleDevice)
					}
				} else if j == n {
					return `\\` + firstPart + `\` + path[last:] + `\`
				} else {
					setDevice(`\\` + firstPart + `\` + path[last:j])
					rootEnd = j
				}
			}
		} else {
			rootEnd = 1
		}
	} else {
		colonIndex := strings.IndexByte(path, ':')
		if colonIndex > 0 {
			if isDeviceRoot(code) && colonIndex == 1 {
				setDevice(path[:2])
				rootEnd = 2
				if n > 2 && isWinSep(path[2]) {
					isAbsolute = true
					rootEnd = 3
				}
			} else if isWindowsReservedName(path, colonIndex) {
				setDevice(path[:colonIndex+1])
				rootEnd = colonIndex + 1
			}
		}
	}
	tail := ""
	if rootEnd < n {
		tail = normalizeString(path[rootEnd:], !isAbsolute, '\\', isWinSep)
	}
	if tail == "" && !isAbsolute {
		tail = "."
	}
	if tail != "" && isWinSep(path[n-1]) {
		tail += `\`
	}
	if !isAbsolute && device == nil && strings.Contains(path, ":") {
		if len(tail) >= 2 && isDeviceRoot(tail[0]) && tail[1] == ':' {
			return `.\` + tail
		}
		for index := strings.IndexByte(path, ':'); index != -1; {
			if index == n-1 || isWinSep(path[index+1]) {
				return `.\` + tail
			}
			next := strings.IndexByte(path[index+1:], ':')
			if next == -1 {
				break
			}
			index += 1 + next
		}
	}
	if isWindowsReservedName(path, strings.IndexByte(path, ':')) {
		d := ""
		if device != nil {
			d = *device
		}
		return `.\` + d + tail
	}
	if device == nil {
		if isAbsolute {
			return `\` + tail
		}
		return tail
	}
	if isAbsolute {
		return *device + `\` + tail
	}
	return *device + tail
}

// Win32Join is path.win32.join.
func Win32Join(args ...string) string {
	parts := nonEmpty(args)
	if len(parts) == 0 {
		return "."
	}
	firstPart := parts[0]
	joined := strings.Join(parts, `\`)
	needsReplace := true
	slashCount := 0
	if isWinSep(firstPart[0]) {
		slashCount++
		if len(firstPart) > 1 && isWinSep(firstPart[1]) {
			slashCount++
			if len(firstPart) > 2 {
				if isWinSep(firstPart[2]) {
					slashCount++
				} else {
					needsReplace = false
				}
			}
		}
	}
	if needsReplace {
		for slashCount < len(joined) && isWinSep(joined[slashCount]) {
			slashCount++
		}
		if slashCount >= 2 {
			joined = `\` + joined[slashCount:]
		}
	}
	for _, p := range strings.Split(joined, `\`) {
		if colonIndex := strings.IndexByte(p, ':'); p != "" && colonIndex != -1 && isWindowsReservedName(p, colonIndex) {
			return strings.ReplaceAll(joined, "/", `\`)
		}
	}
	return Win32Normalize(joined)
}

var lowerCaser = cases.Lower(language.Und)

// jsLower is String.prototype.toLowerCase (full, locale-independent mapping).
func jsLower(s string) string { return lowerCaser.String(s) }

// Win32Relative is path.win32.relative.
func Win32Relative(cwd func() string, env func(string) string, from, to string) string {
	if from == to {
		return ""
	}
	fromOrig := Win32Resolve(cwd, env, from)
	toOrig := Win32Resolve(cwd, env, to)
	if fromOrig == toOrig {
		return ""
	}
	from = jsLower(fromOrig)
	to = jsLower(toOrig)
	if from == to {
		return ""
	}
	if jsstr.Length(fromOrig) != jsstr.Length(from) || jsstr.Length(toOrig) != jsstr.Length(to) {
		fromSplit := strings.Split(fromOrig, `\`)
		toSplit := strings.Split(toOrig, `\`)
		if fromSplit[len(fromSplit)-1] == "" {
			fromSplit = fromSplit[:len(fromSplit)-1]
		}
		if toSplit[len(toSplit)-1] == "" {
			toSplit = toSplit[:len(toSplit)-1]
		}
		fromLen, toLen := len(fromSplit), len(toSplit)
		length := min(fromLen, toLen)
		i := 0
		for ; i < length; i++ {
			if jsLower(fromSplit[i]) != jsLower(toSplit[i]) {
				break
			}
		}
		if i == 0 {
			return toOrig
		} else if i == length {
			if toLen > length {
				return strings.Join(toSplit[i:], `\`)
			}
			if fromLen > length {
				return strings.Repeat(`..\`, fromLen-1-i) + ".."
			}
			return ""
		}
		return strings.Repeat(`..\`, fromLen-i) + strings.Join(toSplit[i:], `\`)
	}
	return win32RelativeUnits(jsstr.ToUTF16(fromOrig), jsstr.ToUTF16(toOrig), jsstr.ToUTF16(from), jsstr.ToUTF16(to))
}

// win32RelativeUnits is the tail of path.win32.relative, in UTF-16 units: it
// indexes the original strings with offsets found in the lower-cased ones.
func win32RelativeUnits(fromOrig, toOrig, from, to []uint16) string {
	const bs = '\\'
	fromStart := 0
	for fromStart < len(from) && from[fromStart] == bs {
		fromStart++
	}
	fromEnd := len(from)
	for fromEnd-1 > fromStart && from[fromEnd-1] == bs {
		fromEnd--
	}
	fromLen := fromEnd - fromStart
	toStart := 0
	for toStart < len(to) && to[toStart] == bs {
		toStart++
	}
	toEnd := len(to)
	for toEnd-1 > toStart && to[toEnd-1] == bs {
		toEnd--
	}
	toLen := toEnd - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		fromCode := from[fromStart+i]
		if fromCode != to[toStart+i] {
			break
		} else if fromCode == bs {
			lastCommonSep = i
		}
	}
	slice := func(u []uint16, a, b int) string {
		b = min(b, len(u))
		if a >= b {
			return ""
		}
		return jsstr.FromUTF16(u[a:b])
	}
	if i != length {
		if lastCommonSep == -1 {
			return jsstr.FromUTF16(toOrig)
		}
	} else {
		if toLen > length {
			if to[toStart+i] == bs {
				return slice(toOrig, toStart+i+1, len(toOrig))
			}
			if i == 2 {
				return slice(toOrig, toStart+i, len(toOrig))
			}
		}
		if fromLen > length {
			if from[fromStart+i] == bs {
				lastCommonSep = i
			} else if i == 2 {
				lastCommonSep = 3
			}
		}
		if lastCommonSep == -1 {
			lastCommonSep = 0
		}
	}
	out := ""
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || from[i] == bs {
			if out == "" {
				out = ".."
			} else {
				out += `\..`
			}
		}
	}
	toStart += lastCommonSep
	if out != "" {
		return out + slice(toOrig, toStart, toEnd)
	}
	if toStart < len(toOrig) && toOrig[toStart] == bs {
		toStart++
	}
	return slice(toOrig, toStart, toEnd)
}

// Win32IsAbsolute is path.win32.isAbsolute.
func Win32IsAbsolute(path string) bool {
	if path == "" {
		return false
	}
	return isWinSep(path[0]) || len(path) > 2 && isDeviceRoot(path[0]) && path[1] == ':' && isWinSep(path[2])
}

// Win32Dirname is path.win32.dirname.
func Win32Dirname(path string) string {
	n := len(path)
	if n == 0 {
		return "."
	}
	rootEnd := -1
	offset := 0
	code := path[0]
	if n == 1 {
		if isWinSep(code) {
			return path
		}
		return "."
	}
	if isWinSep(code) {
		rootEnd, offset = 1, 1
		if isWinSep(path[1]) {
			j := 2
			last := j
			for j < n && !isWinSep(path[j]) {
				j++
			}
			if j < n && j != last {
				last = j
				for j < n && isWinSep(path[j]) {
					j++
				}
				if j < n && j != last {
					last = j
					for j < n && !isWinSep(path[j]) {
						j++
					}
					if j == n {
						return path
					}
					if j != last {
						rootEnd, offset = j+1, j+1
					}
				}
			}
		}
	} else if isDeviceRoot(code) && path[1] == ':' {
		rootEnd = 2
		if n > 2 && isWinSep(path[2]) {
			rootEnd = 3
		}
		offset = rootEnd
	}
	end := -1
	matchedSlash := true
	for i := n - 1; i >= offset; i-- {
		if isWinSep(path[i]) {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if rootEnd == -1 {
			return "."
		}
		end = rootEnd
	}
	return path[:end]
}

// Resolve is path.resolve; it throws Node's uv_cwd error when a relative
// path needs a working directory that no longer exists.
func Resolve(p ...string) string {
	if isWindows {
		return Win32Resolve(mustCwd, os.Getenv, p...)
	}
	return PosixResolve(mustCwd, p...)
}

// Join is path.join.
func Join(p ...string) string {
	if isWindows {
		return Win32Join(p...)
	}
	return PosixJoin(p...)
}

// Dirname is path.dirname.
func Dirname(p string) string {
	if isWindows {
		return Win32Dirname(p)
	}
	return PosixDirname(p)
}

// IsAbsolute is path.isAbsolute.
func IsAbsolute(p string) bool {
	if isWindows {
		return Win32IsAbsolute(p)
	}
	return p != "" && p[0] == '/'
}

// Relative is path.relative.
func Relative(from, to string) string {
	if isWindows {
		return Win32Relative(mustCwd, os.Getenv, from, to)
	}
	return PosixRelative(mustCwd, from, to)
}
