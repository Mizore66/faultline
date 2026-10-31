//go:build linux

package main

// Decoding of the ordinary open group (open, openat, openat2) and the path
// resolution that decides whether a call names the target. decodeOpen records
// the open_how peek ERROR (howErr) instead of discarding it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const atFDCWD = int32(-100)

// openCall is a decoded open(2), openat(2) or openat2(2) entry.
type openCall struct {
	name    string
	dirfd   int32
	pathPtr uintptr
	flags   uint64
	mode    uint64
	// openat2 only
	isHow   bool
	howOK   bool  // the open_how struct was read and decoded
	howErr  error // the peek error when it was attempted and failed (nil if not attempted)
	resolve uint64
	howSize uint64
}

// decodeOpen recognises the ordinary open group by syscall number (the
// caller has already checked the syscall ABI).
func decodeOpen(tid int, info syscallInfo) (openCall, bool) {
	a := info.args
	switch {
	case hasOpen && info.nr == sysOpen:
		return openCall{name: "open", dirfd: atFDCWD, pathPtr: uintptr(a[0]), flags: a[1], mode: a[2]}, true
	case info.nr == sysOpenat:
		return openCall{name: "openat", dirfd: int32(a[0]), pathPtr: uintptr(a[1]), flags: a[2], mode: a[3]}, true
	case info.nr == sysOpenat2:
		c := openCall{name: "openat2", dirfd: int32(a[0]), pathPtr: uintptr(a[1]), isHow: true, howSize: a[3]}
		if a[3] >= 16 {
			n := a[3]
			if n > 24 {
				n = 24
			}
			buf := make([]byte, n)
			if _, err := syscall.PtracePeekData(tid, uintptr(a[2]), buf); err == nil {
				c.howOK = true
				c.flags = nativeU64(buf[0:])
				c.mode = nativeU64(buf[8:])
				if n >= 24 {
					c.resolve = nativeU64(buf[16:])
				}
			} else {
				c.howErr = err
			}
		}
		return c, true
	}
	return openCall{}, false
}

func nativeU64(b []byte) uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(b[i]) << (8 * uint(i)) // little-endian: both supported arches
	}
	return v
}

// flagsSym names the flag bits this arch's syscall package knows; the raw
// hexadecimal value is always logged beside it, and any unnamed bit is shown.
func flagsSym(f uint64) string {
	parts := []string{[...]string{"O_RDONLY", "O_WRONLY", "O_RDWR", "O_ACCMODE3"}[f&3]}
	rest := f &^ 3
	for _, b := range []struct {
		bit  uint64
		name string
	}{
		{syscall.O_CREAT, "O_CREAT"}, {syscall.O_EXCL, "O_EXCL"}, {syscall.O_NOCTTY, "O_NOCTTY"},
		{syscall.O_TRUNC, "O_TRUNC"}, {syscall.O_APPEND, "O_APPEND"}, {syscall.O_NONBLOCK, "O_NONBLOCK"},
		{syscall.O_SYNC, "O_SYNC"}, {syscall.O_DIRECTORY, "O_DIRECTORY"}, {syscall.O_NOFOLLOW, "O_NOFOLLOW"},
		{syscall.O_CLOEXEC, "O_CLOEXEC"},
	} {
		if rest&b.bit == b.bit {
			parts = append(parts, b.name)
			rest &^= b.bit
		}
	}
	if rest != 0 {
		parts = append(parts, fmt.Sprintf("0x%x", rest))
	}
	return strings.Join(parts, "|")
}

// q quotes s for the log. The literal injected marker never appears in a
// quoted field, so the raw (INJECTED) count counts injected EXIT lines only.
func q(s string) string {
	return strings.ReplaceAll(strconv.Quote(s), "(INJECTED)", `(INJECTED\x29`)
}

// readCString reads at most limit bytes of a NUL-terminated string from the
// tracee with aligned word peeks (an aligned word never straddles a page).
func readCString(tid int, addr uintptr, limit int) ([]byte, bool, error) {
	var out []byte
	for len(out) < limit {
		cur := addr + uintptr(len(out))
		base := cur &^ 7
		var w [8]byte
		if _, err := syscall.PtracePeekData(tid, base, w[:]); err != nil {
			return out, false, err
		}
		for i := int(cur - base); i < 8 && len(out) < limit; i++ {
			if w[i] == 0 {
				return out, true, nil
			}
			out = append(out, w[i])
		}
	}
	return out, false, nil
}

type resKind int

const (
	resNoMatch    resKind = iota // provably a different file
	resMatch                     // names the target
	resUnresolved                // could not decide: evidence incomplete
	resRefused                   // would be ambiguous: not injected
)

type resolution struct {
	kind     resKind
	resolved string
	reason   string
}

func hasDotDot(p string) bool {
	for _, c := range strings.Split(p, "/") {
		if c == ".." {
			return true
		}
	}
	return false
}

// pathKey is p with every component but the last resolved through symlinks
// (and ".." applied after that resolution). The last component is kept by
// name. ok is false when ".." cannot be resolved because the directory does
// not exist.
func pathKey(p string) (string, bool) {
	dots := hasDotDot(p)
	dir, last := filepath.Split(p)
	if last == "" || last == "." || last == ".." {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r, true
		}
		if dots {
			return "", false
		}
		return filepath.Clean(p), true
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(r, last), true
	}
	if dots {
		return "", false
	}
	return filepath.Clean(p), true
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// resolve decides whether raw (relative to dirfd or the thread's cwd, read
// from /proc/<tid> while the thread is stopped at the syscall entry) names the
// target. Limits (documented, not hidden): the check is made from the tracer's
// own filesystem view, so a mount namespace or chroot that differs, a bind
// mount or a hard link to the target is not an alias this code can see; a
// directory changed by another thread between this check and the kernel's
// lookup is not seen; a symlink in the final component is refused, never
// followed.
func (tr *tracer) resolve(tid int, dirfd int32, raw string) resolution {
	if raw == "" {
		return resolution{kind: resNoMatch}
	}
	joined := raw
	if raw[0] != '/' {
		link := "/proc/" + strconv.Itoa(tid) + "/cwd"
		if dirfd != atFDCWD {
			link = "/proc/" + strconv.Itoa(tid) + "/fd/" + strconv.Itoa(int(dirfd))
		}
		base, err := os.Readlink(link)
		if err != nil {
			return resolution{kind: resUnresolved, reason: "readlink " + link + ": " + err.Error()}
		}
		if !strings.HasPrefix(base, "/") {
			return resolution{kind: resNoMatch, reason: "dirfd is not a directory path"}
		}
		if strings.HasSuffix(base, " (deleted)") {
			return resolution{kind: resUnresolved, reason: "base directory " + base + " was removed"}
		}
		joined = strings.TrimRight(base, "/") + "/" + raw
	}
	trimmed := strings.TrimRight(joined, "/")
	last := filepath.Base(trimmed)
	wantKey := func() (string, bool) { return pathKey(tr.want) }
	if !hasDotDot(joined) && last != filepath.Base(tr.want) {
		// A different name. Only a final-component symlink can still reach the target.
		if isSymlink(trimmed) {
			if full, err := filepath.EvalSymlinks(trimmed); err == nil {
				if wk, ok := wantKey(); ok && full == wk {
					return resolution{kind: resRefused, reason: "final component is a symlink to the target (alias unsupported)"}
				}
			}
		}
		return resolution{kind: resNoMatch}
	}
	if strings.HasSuffix(joined, "/") {
		return resolution{kind: resRefused, reason: "trailing slash on the target name"}
	}
	wk, ok := wantKey()
	if !ok {
		return resolution{kind: resUnresolved, reason: "cannot resolve the target itself"}
	}
	key, ok := pathKey(joined)
	if !ok {
		return resolution{kind: resUnresolved, reason: "'..' through an unresolvable directory"}
	}
	if key != wk {
		return resolution{kind: resNoMatch}
	}
	if isSymlink(joined) {
		return resolution{kind: resRefused, reason: "final component is a symlink (the file opened is not the named path)"}
	}
	return resolution{kind: resMatch, resolved: key}
}
