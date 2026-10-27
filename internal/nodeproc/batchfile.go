package nodeproc

import (
	"runtime"
	"strings"
)

// refusesBatchFile is Node 22.22.2's Windows batch-file refusal: on Windows,
// spawn_sync.cc ParseOptions (UV_EINVAL right after reading file, before
// stdio, cwd or any PATH search) and process_wrap.cc Spawn (err = UV_EINVAL,
// so uv_spawn never runs) refuse a file IsWindowsBatchFile matches.
// spawnSync then reports "spawnSync <file> EINVAL"; spawn throws
// ErrnoException(UV_EINVAL, "spawn"), "spawn EINVAL", since EINVAL is not
// one of the errors it defers to an 'error' event.
func refusesBatchFile(file string) bool {
	return runtime.GOOS == "windows" && isWindowsBatchFile(file)
}

// isWindowsBatchFile ports Node 22.22.2 src/util-inl.h:666
// IsWindowsBatchFile: std::regex_search(file, /\.([a-zA-Z0-9]+)\s*[\.\s]*$/)
// (ECMAScript grammar, classic locale), the capture lowercased, compared
// with "cmd" and "bat".
//
// \s*[\.\s]* is any run of dots and whitespace (space, \t, \n, \v, \f, \r),
// and the capture cannot be followed by an alphanumeric, so a match exists
// exactly when the file, less that trailing run, ends in "." and a maximal
// run of ASCII alphanumerics; that run is the capture, whichever match the
// leftmost search finds. Bytes from 0x80 up (file is UTF-8) are neither.
// The argument is a C string, so it ends at the first NUL (one is normally
// rejected earlier, by normalizeSpawnArguments).
func isWindowsBatchFile(file string) bool {
	if i := strings.IndexByte(file, 0); i >= 0 {
		file = file[:i]
	}
	end := len(file)
	for end > 0 && isDotOrSpace(file[end-1]) {
		end--
	}
	start := end
	for start > 0 && isASCIIAlnum(file[start-1]) {
		start--
	}
	if start == end || start == 0 || file[start-1] != '.' {
		return false
	}
	ext := []byte(file[start:end])
	for i, c := range ext {
		if c >= 'A' && c <= 'Z' {
			ext[i] = c + 'a' - 'A'
		}
	}
	return string(ext) == "cmd" || string(ext) == "bat"
}

func isDotOrSpace(c byte) bool {
	switch c {
	case '.', ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
