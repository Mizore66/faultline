package nodefs

import (
	"errors"
	"os"
	"sort"
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

type remover struct{ maxDepth int }

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
		err = unlinkFile(raw, display)
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

// unlinkFile is rimraf's _unlinkSync with one try.
func unlinkFile(raw, display string) error {
	if err := unlinkRaw(raw); err != nil {
		if e := wrap(err, "unlink", display); e.(*Error).Code != "ENOENT" {
			return e
		}
	}
	return nil
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
		if err := rmdirRaw(raw); err != nil {
			if e := wrap(err, "rmdir", display); e.(*Error).Code != "ENOENT" {
				return e
			}
		}
		return nil
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
	return unlinkFile(raw, display)
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
