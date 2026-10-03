package nodefs

import (
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// On Windows, Node hands libuv a namespaced path for nearly every fs call
// (lstat, readdir, open, readFileSync, existsSync, unlink, rmdir, chmod; not
// mkdtemp): src/path.cc ToNamespacedPath resolves the path and prefixes
// \\?\ (or \\?\UNC\), which turns off Win32 path normalization (trailing
// dots and spaces, reserved device names, MAX_PATH). Error messages print
// the namespaced path with the prefix removed (api/exceptions.cc
// StringFromPath), which is the resolved path, not the JS string.

// toNamespacedPath is ToNamespacedPath on a UTF-8 path.
func toNamespacedPath(path string, cwd func() string, getenv func(string) string) string {
	if path == "" {
		return path
	}
	r := cppWin32Resolve(path, cwd, getenv)
	if len(r) <= 2 {
		return path
	}
	if r[0] == '\\' {
		if r[1] == '\\' && r[2] != '?' && r[2] != '.' {
			return `\\?\UNC\` + r[2:]
		}
	} else if isDeviceRoot(r[0]) && r[1] == ':' && r[2] == '\\' {
		return `\\?\` + r
	}
	return r
}

// stringFromPath is StringFromPath: the path an fs error message prints.
func stringFromPath(p string) string {
	if strings.HasPrefix(p, `\\?\UNC\`) {
		return `\\` + p[8:]
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p[4:]
	}
	return p
}

// cppAt is std::string::operator[], which reads '\0' at size().
func cppAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func cppLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// cppWin32Resolve is node::PathResolve (src/path.cc, _WIN32) for one path.
// It differs from path.win32.resolve: it works on UTF-8 bytes, lowercases
// ASCII only, does not skip an empty path, and accepts a drive cwd from
// the =X: variable unless it names another drive and has '/' at index 2.
func cppWin32Resolve(p string, cwd func() string, getenv func(string) string) string {
	resolvedDevice, resolvedTail := "", ""
	resolvedAbsolute := false
	cwdPath := cwd()
	for i := 0; i >= -1; i-- {
		var path string
		switch {
		case i >= 0:
			path = p
		case resolvedDevice == "":
			path = cwdPath
		default:
			path = getenv("=" + resolvedDevice)
			if path == "" {
				path = cwdPath
			}
			if path == "" || cppLower(path[:min(2, len(path))]) != cppLower(resolvedDevice) && cppAt(path, 2) == '/' {
				path = resolvedDevice + `\`
			}
		}
		n := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		code := cppAt(path, 0)
		if n == 1 {
			if isWinSep(code) {
				rootEnd = 1
				isAbsolute = true
			}
		} else if isWinSep(code) {
			isAbsolute = true
			if isWinSep(path[1]) {
				j, last := 2, 2
				for j < n && !isWinSep(path[j]) {
					j++
				}
				if j < n && j != last {
					firstPart := path[last:j]
					last = j
					for j < n && isWinSep(path[j]) {
						j++
					}
					if j < n && j != last {
						last = j
						for j < n && !isWinSep(path[j]) {
							j++
						}
						if j == n || j != last {
							if firstPart != "." && firstPart != "?" {
								device = `\\` + firstPart + `\` + path[last:j]
								rootEnd = j
							} else {
								device = `\\` + firstPart
								rootEnd = 4
							}
						}
					}
				}
			}
		} else if isDeviceRoot(code) && cppAt(path, 1) == ':' {
			device = path[:2]
			rootEnd = 2
			if n > 2 && isWinSep(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		}
		if device != "" {
			if resolvedDevice != "" {
				if cppLower(device) != cppLower(resolvedDevice) {
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
	resolvedTail = cppNormalizeString(resolvedTail, !resolvedAbsolute, '\\')
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if resolvedDevice != "" || resolvedTail != "" {
		return resolvedDevice + resolvedTail
	}
	return "."
}

// cppNormalizeString is node::NormalizeString with Windows separators. Unlike
// path.js it finds the previous segment with find_last_of.
func cppNormalizeString(path string, allowAboveRoot bool, separator byte) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if isWinSep(code) {
			break
		} else {
			code = '/'
		}
		if isWinSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				n := len(res)
				if n < 2 || lastSegmentLength != 2 || res[n-1] != '.' || res[n-2] != '.' {
					if n > 2 {
						idx := strings.LastIndexByte(res, separator)
						if idx < 0 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:idx]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, separator)
						}
						lastSlash = i
						dots = 0
						continue
					} else if n != 0 {
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

// processCwd is Environment::GetCwd: uv_cwd, or the executable's directory
// when the cwd is gone.
func processCwd() string {
	if c, err := Cwd(); err == nil {
		return jsstr.ToUTF8(c)
	}
	exe, _ := os.Executable()
	if i := strings.LastIndexByte(exe, '\\'); i >= 0 {
		return exe[:i]
	}
	return ""
}

// errorPath is the path an fs error prints for a JS path: the JS string
// (printed through UTF-8 replacement) on Unix, the resolved namespaced path
// without its prefix on Windows.
func errorPath(path string) string {
	if !isWindows {
		return path
	}
	return DecodeUTF8([]byte(stringFromPath(sysPath(path))))
}
