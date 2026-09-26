package nodefs

import (
	"os"
	"sort"
)

// RemoveAll is rmSync(path, { recursive: true, force: true }): Node 22's
// rimrafSync with its defaults (maxRetries 0). Only ENOENT is ignored; any
// other failure is returned as the error rmSync throws. Child paths are raw
// bytes (Node builds them as Buffers) and print through UTF-8 replacement.
func RemoveAll(path string) error { return rimraf(sysPath(path), path) }

func rimraf(raw, display string) error {
	info, err := os.Lstat(raw)
	if err != nil {
		switch codeOf(err) {
		case "ENOENT":
			return nil
		case "EPERM":
			if isWindows {
				if err := fixWinEPERM(raw, display, wrap(err, "lstat", display)); err != nil {
					return err
				}
			}
		}
		info = nil // stats stays undefined: rimraf falls through to unlink
	}
	if info != nil && info.IsDir() {
		err = rmdirTree(raw, display, nil)
	} else {
		err = unlinkFile(raw, display)
	}
	if err == nil {
		return nil
	}
	switch err.(*Error).Code {
	case "ENOENT":
		return nil
	case "EPERM":
		if isWindows {
			return fixWinEPERM(raw, display, err)
		}
		return rmdirTree(raw, display, err)
	case "EISDIR":
		return rmdirTree(raw, display, err)
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
func rmdirTree(raw, display string, original error) error {
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
			if err := rimraf(child, DecodeUTF8([]byte(child))); err != nil {
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
func fixWinEPERM(raw, display string, original error) error {
	if err := os.Chmod(raw, 0o666); err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	info, err := os.Stat(raw)
	if err != nil {
		if codeOf(err) == "ENOENT" {
			return nil
		}
		return original
	}
	if info.IsDir() {
		return rmdirTree(raw, display, original)
	}
	return unlinkFile(raw, display)
}

// readRawNames is readdirSync(path, "buffer"): libuv sorts names on Unix and
// returns directory order on Windows.
func readRawNames(raw string) ([]string, error) {
	f, err := os.Open(raw)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
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
