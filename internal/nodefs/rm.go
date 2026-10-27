package nodefs

import (
	"errors"
	"os"
	"sort"
	"time"
)

// RemoveAll is rmSync(path, { recursive: true, force: true }): Node 22's
// rimrafSync with its defaults (maxRetries 0). Only ENOENT is ignored; any
// other failure is returned as the error rmSync throws. Child paths are raw
// bytes (Node builds them as Buffers) and print through UTF-8 replacement.
func RemoveAll(path string) error { return RemoveAllDepth(path, -1) }

// ErrTooDeep reports that RemoveAllDepth reached a directory more than
// maxDepth levels below path, where V8's recursive rimrafSync runs out of
// stack. Nothing at or below that directory has been removed.
var ErrTooDeep = errors.New("rimraf: directory tree too deep")

// RemoveAllDepth is RemoveAll that fails with ErrTooDeep instead of
// descending more than maxDepth levels (-1 means no limit).
func RemoveAllDepth(path string, maxDepth int) error {
	r := remover{maxDepth: maxDepth}
	return r.rimraf(sysPath(path), path, 0)
}

// RemoveAllRetry is rmSync(path, {recursive: true, force: true,
// maxRetries, retryDelay}): RemoveAll, where every unlink and the final
// rmdir of each directory are retried up to maxRetries times on EBUSY,
// EMFILE, ENFILE, ENOTEMPTY and EPERM, sleeping i*retryDelay before
// retry i (internal/fs/rimraf.js _unlinkSync, _rmdirSync).
func RemoveAllRetry(path string, maxRetries int, retryDelay time.Duration) error {
	r := remover{maxDepth: -1, maxRetries: maxRetries, retryDelay: retryDelay}
	return r.rimraf(sysPath(path), path, 0)
}

type remover struct {
	maxDepth   int
	maxRetries int
	retryDelay time.Duration
}

// retryErrorCodes is rimraf.js retryErrorCodes.
func retryable(code string) bool {
	switch code {
	case "EBUSY", "EMFILE", "ENFILE", "ENOTEMPTY", "EPERM":
		return true
	}
	return false
}

// retryLoop is the try loop of rimraf.js _unlinkSync and of _rmdirSync's
// final rmdir: ENOENT ends it quietly, a retryable code sleeps i*retryDelay
// before the next try, and the last try throws.
func (r remover) retryLoop(op func() error) error {
	tries := r.maxRetries + 1
	for i := 1; i <= tries; i++ {
		err := op()
		if err == nil {
			return nil
		}
		e, _ := err.(*Error)
		code := ""
		if e != nil {
			code = e.Code
		}
		if retryable(code) && i < tries && r.retryDelay > 0 {
			time.Sleep(time.Duration(i) * r.retryDelay)
		} else if code == "ENOENT" {
			return nil
		} else if i == tries {
			return err
		}
	}
	return nil
}

func (r remover) rimraf(raw, display string, depth int) error {
	if r.maxDepth >= 0 && depth > r.maxDepth {
		return ErrTooDeep
	}
	info, err := statRaw(raw, false)
	if err != nil {
		switch codeOf(err) {
		case "ENOENT":
			return nil
		case "EPERM":
			if isWindows {
				if err := r.fixWinEPERM(raw, display, wrap(err, "lstat", display), depth); err != nil {
					return err
				}
			}
		}
		info = nil // stats stays undefined: rimraf falls through to unlink
	}
	if info != nil && info.IsDir() {
		err = r.rmdirTree(raw, display, nil, depth)
	} else {
		err = r.unlinkFile(raw, display)
	}
	if err == nil {
		return nil
	}
	e, ok := err.(*Error)
	if !ok {
		return err
	}
	switch e.Code {
	case "ENOENT":
		return nil
	case "EPERM":
		if isWindows {
			return r.fixWinEPERM(raw, display, err, depth)
		}
		return r.rmdirTree(raw, display, err, depth)
	case "EISDIR":
		return r.rmdirTree(raw, display, err, depth)
	}
	return err
}

// unlinkFile is rimraf's _unlinkSync.
func (r remover) unlinkFile(raw, display string) error {
	return r.retryLoop(func() error {
		if err := unlinkRaw(raw); err != nil {
			return wrap(err, "unlink", display)
		}
		return nil
	})
}

// rmdirTree is rimraf's _rmdirSync.
func (r remover) rmdirTree(raw, display string, original error, depth int) error {
	err := rmdirRaw(raw)
	if err == nil {
		return nil
	}
	e := wrap(err, "rmdir", display)
	code := e.(*Error).Code
	switch code {
	case "ENOENT":
		return nil
	case "ENOTDIR":
		if original != nil {
			return original
		}
		return e
	case "ENOTEMPTY", "EEXIST", "EPERM":
		names, err := readRawNames(raw)
		if err != nil {
			return wrap(err, "scandir", display)
		}
		for _, name := range names {
			child := raw + rmSep + name
			if err := r.rimraf(child, DecodeUTF8([]byte(child)), depth+1); err != nil {
				return err
			}
		}
		return r.retryLoop(func() error {
			if err := rmdirRaw(raw); err != nil {
				return wrap(err, "rmdir", display)
			}
			return nil
		})
	}
	if original != nil {
		return original
	}
	return e
}

// fixWinEPERM is rimraf's fixWinEPERMSync.
func (r remover) fixWinEPERM(raw, display string, original error, depth int) error {
	if err := os.Chmod(raw, 0o666); err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	info, err := statRaw(raw, true)
	if err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	if info.IsDir() {
		return r.rmdirTree(raw, display, original, depth)
	}
	return r.unlinkFile(raw, display)
}

// RemoveAllAsync is fs/promises.rm(path, { recursive: true, force: true })
// in Node 22. validateRmOptions lstat()s the path first: ENOENT resolves,
// and any other failure rejects with that lstat error, where rimrafSync
// would go on to unlink (round 7 #12). Then the callback rimraf runs, which
// differs from rimrafSync in three ways: it starts on every child of a
// directory at once, so the children after a failing one are still
// removed (it keeps the first error here; Node reports whichever failure
// completes first); a failed rmdir reports its own error rather than the
// unlink error that led to it; and on Windows an lstat EPERM ends with
// fixWinEPERM's result. It has no depth limit (no recursion on the JS
// stack).
// Port of node v22 lib/internal/fs/promises.js rm and lib/internal/fs/rimraf.js rimraf.
func RemoveAllAsync(path string) error {
	raw := sysPath(path)
	if _, err := statRaw(raw, false); err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return wrap(err, "lstat", path)
	}
	return rimrafAsync(raw, path)
}

// rimrafAsync is rimraf: _rimraf, with ENOENT counting as success.
func rimrafAsync(raw, display string) error {
	err := rimrafAsyncOnce(raw, display)
	if e, ok := err.(*Error); ok && e.Code == "ENOENT" {
		return nil
	}
	return err
}

// rimrafAsyncOnce is _rimraf.
func rimrafAsyncOnce(raw, display string) error {
	info, err := statRaw(raw, false)
	if err != nil {
		switch codeOf(err) {
		case "ENOENT":
			return nil
		case "EPERM":
			if isWindows {
				return fixWinEPERMAsync(raw, display, wrap(err, "lstat", display))
			}
		}
	} else if info.IsDir() {
		return rmdirAsync(raw, display, nil)
	}
	if err := unlinkRaw(raw); err != nil {
		e := wrap(err, "unlink", display)
		switch e.(*Error).Code {
		case "ENOENT":
			return nil
		case "EISDIR":
			return rmdirAsync(raw, display, e)
		case "EPERM":
			if isWindows {
				return fixWinEPERMAsync(raw, display, e)
			}
			return rmdirAsync(raw, display, e)
		}
		return e
	}
	return nil
}

// rmdirAsync is _rmdir: ENOTDIR gives back original (nil when there was
// none, which rimraf takes as success).
func rmdirAsync(raw, display string, original error) error {
	err := rmdirRaw(raw)
	if err == nil {
		return nil
	}
	e := wrap(err, "rmdir", display)
	switch e.(*Error).Code {
	case "ENOTEMPTY", "EEXIST", "EPERM":
		return rmchildrenAsync(raw, display)
	case "ENOTDIR":
		return original
	}
	return e
}

// rmchildrenAsync is _rmchildren: every child is removed, then the
// directory, unless a child failed.
func rmchildrenAsync(raw, display string) error {
	names, err := readRawNames(raw)
	if err != nil {
		return wrap(err, "scandir", display)
	}
	var first error
	for _, name := range names {
		child := raw + rmSep + name
		if err := rimrafAsync(child, DecodeUTF8([]byte(child))); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return first
	}
	if err := rmdirRaw(raw); err != nil {
		return wrap(err, "rmdir", display)
	}
	return nil
}

// fixWinEPERMAsync is fixWinEPERM.
func fixWinEPERMAsync(raw, display string, original error) error {
	if err := os.Chmod(raw, 0o666); err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	info, err := statRaw(raw, true)
	if err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	if info.IsDir() {
		return rmdirAsync(raw, display, original)
	}
	if err := unlinkRaw(raw); err != nil {
		return wrap(err, "unlink", display)
	}
	return nil
}

// readRawNames is readdirSync(path, "buffer"): libuv sorts names on Unix and
// returns directory order on Windows.
func readRawNames(raw string) ([]string, error) {
	names, _, err := scandir(raw)
	if err != nil {
		return nil, err
	}
	if !isWindows {
		sort.Strings(names)
	}
	return names, nil
}

// rmSep is path.sep, which rimraf puts between a directory and a child.
var rmSep = map[bool]string{true: `\`, false: "/"}[isWindows]
