package nodefs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// cpMessageError is an error whose text is the whole message: the plain
// Error that Node's C++ throws (THROW_ERR_FS_CP_*, ErrnoException) or a
// SystemError built in cp-sync.js.
type cpMessageError struct{ msg string }

func (e *cpMessageError) Error() string { return e.msg }

// CpAbort is the death of the process by an uncaught C++ exception inside
// node's cpSyncCopyDir (a std::filesystem call without an error_code that
// failed): libstdc++'s terminate handler prints the exception and aborts.
// The live test replaces it to compare the abort with Node's.
var CpAbort = func(what string) {
	os.Stderr.WriteString("terminate called after throwing an instance of 'std::filesystem::__cxx11::filesystem_error'\n  what():  filesystem error: " + what + "\n")
	cpDie()
}

// CpSyncTree is cpSync(src, dest, { recursive: true, dereference: false,
// errorOnExist: true, force: false }) as Node 22.22.2 on Linux runs it
// (repair-worktree.ts:111): lib/internal/fs/cp/cp-sync.js cpSyncFn, whose
// path checks (cpSyncCheckPaths) and directory copy (cpSyncCopyDir) are C++
// over libstdc++ std::filesystem, and whose file and link steps are JS.
// Only these options are ported.
func CpSyncTree(src, dest string) error {
	if err := cpCheckPaths(src, dest); err != nil {
		return err
	}
	return cpGetStats(src, dest)
}

// cpDirSep is the separator std::filesystem::path prefers.
func cpDirSep() string { return pathSeparator() }

// cppParentPath is std::filesystem::path::parent_path (POSIX form).
func cppParentPath(p string) string {
	trimmed := strings.TrimRight(p, "/")
	if trimmed == "" { // "", "/" and "//": no relative part, the path itself
		return p
	}
	if strings.HasSuffix(p, "/") { // an empty last element: drop it
		return trimmed
	}
	i := strings.LastIndex(trimmed, "/")
	if i < 0 {
		return ""
	}
	head := strings.TrimRight(trimmed[:i], "/")
	if head == "" {
		return "/"
	}
	return head
}

func cppAbsolute(p string) string {
	if strings.HasPrefix(p, "/") || (isWindows && IsAbsolute(p)) {
		return p
	}
	cwd := mustCwd()
	if strings.HasSuffix(cwd, "/") {
		return cwd + p
	}
	return cwd + "/" + p
}

func cppIsDirectory(p string) bool {
	info, err := os.Stat(sysPath(p))
	return err == nil && info.IsDir()
}

// cppEquivalent is std::filesystem::equivalent(a, b, ec): both stat'ed
// through links; one that cannot be is "not equivalent", and the error flag
// is set only when both fail.
func cppEquivalent(a, b string) (equal bool, failed bool) {
	ia, ea := os.Stat(sysPath(a))
	ib, eb := os.Stat(sysPath(b))
	if ea != nil && eb != nil {
		return false, true
	}
	if ea != nil || eb != nil {
		return false, false
	}
	// libstdc++: two files that are neither regular, directories nor links
	// (FIFOs, sockets, devices) are not comparable: ENOTSUP.
	other := func(i fs.FileInfo) bool { return !i.Mode().IsRegular() && !i.IsDir() }
	if other(ia) && other(ib) {
		return false, true
	}
	return os.SameFile(ia, ib), false
}

// cppIsInsideDir is node_file.cc isInsideDir.
func cppIsInsideDir(src, dest string) bool {
	parts := func(p string) []string {
		out := []string{"/"}
		for _, s := range strings.Split(cppAbsolute(p), "/") {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	s, d := parts(src), parts(dest)
	if len(s) > len(d) {
		return false
	}
	for i := range s {
		if s[i] != d[i] {
			return false
		}
	}
	return true
}

// glibcMessage is strerror(errno) as glibc words it (Go's table is glibc's
// with the first letter lowered).
func glibcMessage(errno syscall.Errno) string {
	s := errno.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func cpErrnoOf(err error) (syscall.Errno, bool) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno, true
	}
	return 0, false
}

// cppErrno is Environment::ThrowStdErrException(error_code, "cp", path):
// ErrnoException's "<CODE>, <strerror> '<path>'".
func cppErrno(err error, path string) error {
	if errno, ok := cpErrnoOf(err); ok {
		return &cpMessageError{codeOf(errno) + ", " + glibcMessage(errno) + " '" + errorPath(path) + "'"}
	}
	return &cpMessageError{codeOf(err) + ", " + err.Error() + " '" + errorPath(path) + "'"}
}

// cpSystemError is a SystemError of lib/internal/errors.js:
// "<prefix>: cp returned <code> (<message>) <path>".
func cpSystemError(prefix, code, message, path string) error {
	return &cpMessageError{prefix + ": cp returned " + code + " (" + message + ") " + errorPath(path)}
}

func cpEINVAL(message, path string) error {
	return cpSystemError("Invalid src or dest", "EINVAL", message, path)
}

// cpCheckPaths is node_file.cc CpSyncCheckPaths with dereference false and
// recursive true.
func cpCheckPaths(src, dest string) error {
	srcInfo, err := os.Stat(sysPath(src)) // std::filesystem::status follows links
	if err != nil {
		return &Error{Code: codeOf(err), Syscall: "lstat", Path: errorPath(src)}
	}
	destInfo, destErr := os.Lstat(sysPath(dest)) // symlink_status
	destExists := destErr == nil
	srcIsDir := srcInfo.IsDir()
	if destErr == nil {
		same, failed := cppEquivalent(src, dest)
		if failed { // the throwing overload
			CpAbort("in equivalent: Operation not supported [" + src + "] [" + dest + "]")
		}
		if same {
			return &cpMessageError{"src and dest cannot be the same " + dest}
		}
		destIsDir := destInfo.IsDir()
		if srcIsDir && !destIsDir {
			return &cpMessageError{"Cannot overwrite non-directory " + dest + " with directory " + src}
		}
		if !srcIsDir && destIsDir {
			return &cpMessageError{"Cannot overwrite directory " + dest + " with non-directory " + src}
		}
	}
	srcStr := src
	if !strings.HasSuffix(srcStr, cpDirSep()) {
		srcStr += cpDirSep()
	}
	if srcIsDir && strings.HasPrefix(dest, srcStr) {
		return &cpMessageError{"Cannot copy " + srcStr + " to a subdirectory of self " + dest}
	}
	destParent := cppParentPath(dest)
	for cppParentPath(src) != destParent && cppParentPath(destParent) != "" && cppParentPath(destParent) != destParent {
		same, failed := cppEquivalent(src, cppParentPath(dest))
		if same {
			return &cpMessageError{"Cannot copy " + srcStr + " to a subdirectory of self " + dest}
		}
		if failed {
			break
		}
		destParent = cppParentPath(destParent)
	}
	switch m := srcInfo.Mode(); {
	case m&fs.ModeSocket != 0:
		return &cpMessageError{"Cannot copy a socket file: " + dest}
	case m&fs.ModeNamedPipe != 0:
		return &cpMessageError{"Cannot copy a FIFO pipe: " + dest}
	case m&fs.ModeIrregular != 0:
		return &cpMessageError{"Cannot copy an unknown file type: " + dest}
	}
	parent := cppParentPath(dest)
	if !destExists || !pathExists(parent) {
		if parent != "" {
			os.MkdirAll(sysPath(parent), 0o777) // create_directories with an error_code: failures are ignored
		}
	}
	return nil
}

func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(sysPath(p))
	return err == nil
}

// rawMode is st_mode's permission, setuid, setgid and sticky bits.
func rawMode(m fs.FileMode) uint32 {
	raw := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		raw |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		raw |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		raw |= 0o1000
	}
	return raw
}

func modeFromRaw(raw uint32) fs.FileMode {
	m := fs.FileMode(raw & 0o777)
	if raw&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if raw&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if raw&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

// cpGetStats is cp-sync.js getStats with dereference false.
func cpGetStats(src, dest string) error {
	srcStat, err := Lstat(src)
	if err != nil {
		return err
	}
	destStat, err := Lstat(dest)
	if err != nil {
		var ne *Error
		if !errors.As(err, &ne) || ne.Code != "ENOENT" {
			return err
		}
		destStat = nil // lstatSync(dest, { throwIfNoEntry: false })
	}
	m := srcStat.Mode()
	switch {
	case m.IsDir():
		return cpCopyDir(src, dest)
	case m.IsRegular() || m&fs.ModeCharDevice != 0 || (m&fs.ModeDevice != 0):
		if destStat == nil {
			if err := cpCopyFileSync(src, dest); err != nil {
				return err
			}
			if err := os.Chmod(sysPath(dest), modeFromRaw(rawMode(m))); err != nil {
				return wrap(err, "chmod", dest)
			}
			return nil
		}
		return cpSystemError("Target already exists", "EEXIST", dest+" already exists", dest)
	case m&fs.ModeSymlink != 0:
		return cpOnLink(destStat != nil, src, dest)
	}
	return nil
}

// cpCopyFileSync is copyFileSync(src, dest, 0): libuv's uv__fs_copyfile.
func cpCopyFileSync(src, dest string) error {
	fail := func(err error) error {
		return &Error{Code: codeOf(err), Syscall: "copyfile", Path: errorPath(src), Dest: errorPath(dest)}
	}
	in, err := os.Open(sysPath(src))
	if err != nil {
		return fail(err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fail(err)
	}
	out, err := os.OpenFile(sysPath(dest), os.O_WRONLY|os.O_CREATE, modeFromRaw(rawMode(info.Mode())))
	if err != nil {
		return fail(err)
	}
	defer out.Close()
	if err := out.Truncate(0); err != nil {
		return fail(err)
	}
	if err := out.Chmod(modeFromRaw(rawMode(info.Mode()))); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	return nil
}

// cpOnLink is cp-sync.js onLink with verbatimSymlinks false.
func cpOnLink(destExists bool, src, dest string) error {
	resolvedSrc, err := os.Readlink(sysPath(src))
	if err != nil {
		return wrap(err, "readlink", src)
	}
	if !IsAbsolute(resolvedSrc) {
		resolvedSrc = Resolve(Dirname(src), resolvedSrc)
	}
	symlink := func() error {
		if err := os.Symlink(jsstr.ToUTF8(resolvedSrc), sysPath(dest)); err != nil {
			return &Error{Code: codeOf(err), Syscall: "symlink", Path: errorPath(resolvedSrc), Dest: errorPath(dest)}
		}
		return nil
	}
	if !destExists {
		return symlink()
	}
	resolvedDest, err := os.Readlink(sysPath(dest))
	if err != nil {
		if c := codeOf(err); c == "EINVAL" || c == "UNKNOWN" {
			return symlink()
		}
		return wrap(err, "readlink", dest)
	}
	if !IsAbsolute(resolvedDest) {
		resolvedDest = Resolve(Dirname(dest), resolvedDest)
	}
	if cpIsSrcSubdir(resolvedSrc, resolvedDest) {
		return cpEINVAL("cannot copy "+resolvedSrc+" to a subdirectory of self "+resolvedDest, dest)
	}
	info, err := Stat(dest)
	if err != nil {
		return err
	}
	if info.IsDir() && cpIsSrcSubdir(resolvedDest, resolvedSrc) {
		return cpSystemError("Cannot overwrite symlink in subdirectory of self", "EINVAL",
			"cannot overwrite "+resolvedDest+" with "+resolvedSrc, dest)
	}
	if err := os.Remove(sysPath(dest)); err != nil {
		return wrap(err, "unlink", dest)
	}
	return symlink()
}

// cpIsSrcSubdir is cp.js isSrcSubdir.
func cpIsSrcSubdir(src, dest string) bool {
	split := func(p string) []string {
		var out []string
		for _, s := range strings.Split(Resolve(p), pathSeparator()) {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	s, d := split(src), split(dest)
	for i := range s {
		if i >= len(d) || d[i] != s[i] {
			return false
		}
	}
	return true
}

// cpCopyDir is node_file.cc CpSyncCopyDir with force false, dereference
// false, errorOnExist true, verbatimSymlinks false and no timestamps.
func cpCopyDir(src, dest string) error {
	if err := os.MkdirAll(sysPath(dest), 0o777); err != nil {
		return cppErrno(err, dest)
	}
	return cpCopyDirContents(src, dest)
}

func cpCopyDirContents(src, dest string) error {
	dir, err := os.Open(sysPath(src))
	var entries []fs.DirEntry
	if err == nil {
		entries, err = dir.ReadDir(-1) // directory order, as directory_iterator
		dir.Close()
	}
	if err != nil {
		CpAbort("directory iterator cannot open directory: " + glibcErr(err) + " [" + src + "]")
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		srcItem, destItem := joinSlash(src, name), joinSlash(dest, name)
		switch t := entry.Type(); {
		case t&fs.ModeSymlink != 0:
			if err := cpCopyLinkEntry(src, srcItem, destItem, dest); err != nil {
				return err
			}
		case t.IsDir():
			if err := os.Mkdir(sysPath(destItem), 0o777); err != nil && !cppIsDirectory(destItem) {
				CpAbort("cannot create directory: " + glibcErr(err) + " [" + destItem + "]")
				return err
			}
			if err := cpCopyDirContents(srcItem, destItem); err != nil {
				return err
			}
		case t.IsRegular():
			if err := cpCopyRegular(srcItem, destItem, dest); err != nil {
				return err
			}
		}
	}
	return nil
}

func glibcErr(err error) string {
	if errno, ok := cpErrnoOf(err); ok {
		return glibcMessage(errno)
	}
	return err.Error()
}

// joinSlash is std::filesystem::path operator/ for a name.
func joinSlash(dir, name string) string {
	if dir == "" || strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// cpCopyRegular is std::filesystem::copy_file(from, to, none) in libstdc++,
// then the EEXIST rewrite of CpSyncCopyDir.
func cpCopyRegular(src, destItem, destDir string) error {
	exists := func() error {
		return &cpMessageError{"[ERR_FS_CP_EEXIST]: Target already exists: cp returned EEXIST (" + destItem + " already exists)"}
	}
	// libstdc++ looks at the target before it opens the source.
	if toInfo, err := os.Stat(sysPath(destItem)); err == nil {
		if !toInfo.Mode().IsRegular() {
			return cppErrno(syscall.ENOTSUP, destDir)
		}
		return exists()
	} else if errno, ok := cpErrnoOf(err); !ok || errno != syscall.ENOENT {
		return cppErrno(err, destDir)
	}
	in, err := os.Open(sysPath(src))
	if err != nil {
		return cppErrno(err, destDir)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return cppErrno(err, destDir)
	}
	mode := modeFromRaw(rawMode(info.Mode()))
	out, err := os.OpenFile(sysPath(destItem), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errno, ok := cpErrnoOf(err); ok && errno == syscall.EEXIST {
			return exists()
		}
		return cppErrno(err, destDir)
	}
	defer out.Close()
	// libstdc++ (the build Node's Linux releases use) creates the file and
	// then fchmod's it to the source's mode, so the umask does not apply.
	if err := out.Chmod(mode); err != nil {
		return cppErrno(err, destDir)
	}
	if _, err := io.Copy(out, in); err != nil {
		return cppErrno(err, destDir)
	}
	return nil
}

// cpCopyLinkEntry is the symlink branch of CpSyncCopyDir.
// srcDir is the directory being copied: the target is resolved against it
// (src / symlink_target), not against the link's own path.
func cpCopyLinkEntry(srcDir, srcItem, destItem, destDir string) error {
	target, err := os.Readlink(sysPath(srcItem))
	if err != nil {
		return cppErrno(err, destDir)
	}
	if pathExists(destItem) {
		if info, err := os.Lstat(sysPath(destItem)); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			current, err := os.Readlink(sysPath(destItem))
			if err != nil {
				return cppErrno(err, destDir)
			}
			if cppIsDirectory(target) && cppIsInsideDir(target, current) {
				return &cpMessageError{"Cannot copy " + target + " to a subdirectory of self " + current}
			}
			if cppIsDirectory(destItem) && cppIsInsideDir(current, target) {
				return &cpMessageError{"cannot overwrite " + current + " with " + target}
			}
			if err := os.Remove(sysPath(destItem)); err != nil {
				return cppErrno(err, destDir)
			}
		} else if err == nil && info.Mode().IsRegular() {
			return cppErrno(syscall.EEXIST, destItem)
		}
	}
	if err := os.Symlink(cppWeaklyCanonical(cppAbsolute(joinSlashAbs(srcDir, target))), sysPath(destItem)); err != nil {
		return cppErrno(err, destDir)
	}
	return nil
}

// joinSlashAbs is src / target: an absolute target replaces src.
func joinSlashAbs(src, target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	return joinSlash(src, target)
}

// cppWeaklyCanonical is std::filesystem::weakly_canonical (libstdc++) in
// its throwing form: a path that exists is canonical()'d; a missing one has
// its longest existing prefix canonical()'d and the rest appended, then
// normalized. A status or canonical failure that is not "not found" (a
// symlink loop, a prefix without search permission) throws a
// filesystem_error nothing catches, so the process aborts.
func cppWeaklyCanonical(p string) string {
	status := func(path string) bool {
		_, err := os.Stat(sysPath(path))
		if err == nil {
			return true
		}
		if errno, ok := cpErrnoOf(err); ok && (errno == syscall.ENOENT || errno == syscall.ENOTDIR) {
			return false
		}
		CpAbort("cannot canonicalize: " + glibcErr(err) + " [" + p + "]")
		return false
	}
	canonical := func(path string) string {
		real, err := RealpathSync(path)
		if err != nil {
			CpAbort("cannot canonicalize: " + glibcErr(err) + " [" + path + "]")
		}
		return real
	}
	if status(p) {
		return canonical(p)
	}
	rest := strings.Split(p, "/")
	result := ""
	i := 0
	for ; i < len(rest); i++ {
		if rest[i] == "" {
			continue
		}
		if !status(result + "/" + rest[i]) {
			break
		}
		result += "/" + rest[i]
	}
	if result != "" {
		result = canonical(result)
	}
	for ; i < len(rest); i++ {
		if rest[i] != "" {
			result += "/" + rest[i]
		}
	}
	return PosixNormalize(result)
}
