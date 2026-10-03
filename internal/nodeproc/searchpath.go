package nodeproc

import "strings"

// searchPath is libuv's search_path (src/win/process.c). A name with a
// directory part is resolved against cwd only. A bare name is tried in the
// working directory first (unless NoDefaultCurrentDirectoryInExePath is
// set), then in each PATH entry: ';'-separated, an entry starting with a
// quote runs to the matching quote, surrounding quotes are dropped, and an
// entry that is empty only after dropping them ("") means the working
// directory. exists is the GetFileAttributesW test. A name without an extension is tried as .com and then .exe;
// with one, the literal name comes first.
func searchPath(file, cwd, path string, searchCwd bool, exists func(string) bool) (string, bool) {
	if file == "" || file == "." {
		return "", false
	}
	nameStart := len(file)
	for nameStart > 0 && !strings.ContainsRune(`\/:`, rune(file[nameStart-1])) {
		nameStart--
	}
	name := file[nameStart:]
	dot := strings.IndexByte(name, '.')
	hasExt := dot >= 0 && dot+1 < len(name)
	if nameStart > 0 {
		return walkExt(file[:nameStart], name, cwd, hasExt, exists)
	}
	if searchCwd {
		if found, ok := walkExt("", file, cwd, hasExt, exists); ok {
			return found, true
		}
	}
	for _, dir := range splitSearchPath(path) {
		if found, ok := walkExt(dir, file, cwd, hasExt, exists); ok {
			return found, true
		}
	}
	return "", false
}

// splitSearchPath yields search_path's directory slices, including the
// empty ones left after quote removal.
func splitSearchPath(path string) []string {
	var dirs []string
	i := 0
	if strings.HasPrefix(path, ";") {
		i = 1
	}
	for i < len(path) {
		start, end := i, i
		if path[start] == '"' || path[start] == '\'' {
			if j := strings.IndexByte(path[start+1:], path[start]); j >= 0 {
				end = start + 1 + j
			} else {
				end = len(path)
			}
		}
		if j := strings.IndexByte(path[end:], ';'); j >= 0 {
			end += j
		} else {
			end = len(path)
		}
		dir := path[start:end]
		i = end + 1
		if dir == "" {
			continue
		}
		if dir[0] == '"' || dir[0] == '\'' {
			dir = dir[1:]
		}
		if dir != "" && (dir[len(dir)-1] == '"' || dir[len(dir)-1] == '\'') {
			dir = dir[:len(dir)-1]
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// walkExt is path_search_walk_ext.
func walkExt(dir, name, cwd string, hasExt bool, exists func(string) bool) (string, bool) {
	exts := []string{"com", "exe"}
	if hasExt {
		exts = []string{"", "com", "exe"}
	}
	for _, ext := range exts {
		if candidate := joinSearch(dir, name, ext, cwd); exists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// joinSearch is search_path_join_test's path construction: a UNC dir
// ignores cwd, a rooted dir takes only cwd's drive, a drive-relative dir
// ("D:" or "D:tools") is joined onto cwd when it names cwd's drive and used
// alone otherwise, and a drive-absolute dir ignores cwd.
func joinSearch(dir, name, ext, cwd string) string {
	isSep := func(c byte) bool { return c == '\\' || c == '/' }
	switch {
	case len(dir) > 2 && isSep(dir[0]) && isSep(dir[1]):
		cwd = ""
	case len(dir) >= 1 && isSep(dir[0]):
		cwd = cwd[:min(2, len(cwd))]
	case len(dir) >= 2 && dir[1] == ':' && (len(dir) < 3 || !isSep(dir[2])):
		if len(cwd) < 2 || !strings.EqualFold(cwd[:2], dir[:2]) {
			cwd = ""
		} else {
			dir = dir[2:]
		}
	case len(dir) > 2 && dir[1] == ':':
		cwd = ""
	}
	endsSep := func(s string) bool { return strings.ContainsRune(`\/:`, rune(s[len(s)-1])) }
	var b strings.Builder
	b.WriteString(cwd)
	if cwd != "" && !endsSep(cwd) {
		b.WriteByte('\\')
	}
	b.WriteString(dir)
	if dir != "" && !endsSep(dir) {
		b.WriteByte('\\')
	}
	b.WriteString(name)
	if ext != "" {
		if name != "" && name[len(name)-1] != '.' {
			b.WriteByte('.')
		}
		b.WriteString(ext)
	}
	return b.String()
}
