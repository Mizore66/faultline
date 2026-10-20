package gitinv

import (
	"bytes"
	"errors"
	"io/fs"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// sha256Digest ports src/safe-overlay.ts:20 sha256Digest for bytes.
func sha256Digest(b []byte) string { return "sha256:" + canonical.SHA256HexBytes(b) }

// safeOverlayParts ports src/safe-overlay.ts:24 safeOverlayParts. The
// SAFE_OVERLAY_PATH test (^[^\\/\0]+(?:\/[^\\/\0]+)*$) is: no backslash,
// no NUL, and no empty "/"-separated segment.
func safeOverlayParts(value string) ([]string, error) {
	unsafe := errors.New(jsexc.Concat("Frozen overlay path is unsafe: ", value))
	if value == "" || strings.ContainsAny(value, "\\\x00") {
		return nil, unsafe
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, unsafe
		}
	}
	return parts, nil
}

// lstatIfPresent ports src/safe-overlay.ts:35 lstatIfPresent.
func lstatIfPresent(path string) (fs.FileInfo, error) {
	info, err := nodefs.Lstat(path)
	if err != nil {
		var e *nodefs.Error
		if errors.As(err, &e) && e.Code == "ENOENT" {
			return nil, nil
		}
		return nil, err
	}
	return info, nil
}

// safeOverlayTarget ports src/safe-overlay.ts:49 safeOverlayTarget.
func safeOverlayTarget(worktree, overlayPath string) (string, error) {
	parts, err := safeOverlayParts(overlayPath)
	if err != nil {
		return "", err
	}
	target := nodefs.Resolve(append([]string{worktree}, parts...)...)
	containment := nodefs.Relative(worktree, target)
	if containment == "" || containment == ".." || strings.HasPrefix(containment, "../") || strings.HasPrefix(containment, `..\`) || nodefs.IsAbsolute(containment) {
		return "", errors.New(jsexc.Concat("Frozen overlay path escaped its worktree: ", overlayPath))
	}

	current := worktree
	for _, part := range parts[:len(parts)-1] {
		current = nodefs.Join(current, part)
		entry, err := lstatIfPresent(current)
		if err != nil {
			return "", err
		}
		if entry == nil {
			// Docker's sandbox runs as uid 65534; newly created overlay
			// directories must be traversable, while the bind mount itself
			// remains read-only.
			if err := nodefs.Mkdir(current, 0o755); err != nil {
				return "", err
			}
			created, err := nodefs.Lstat(current)
			if err != nil {
				return "", err
			}
			if !created.IsDir() || created.Mode()&fs.ModeSymlink != 0 {
				return "", errors.New(jsexc.Concat("Could not safely create overlay directory: ", overlayPath))
			}
			continue
		}
		if !entry.IsDir() || entry.Mode()&fs.ModeSymlink != 0 {
			return "", errors.New(jsexc.Concat("Frozen overlay parent is not a safe directory: ", overlayPath))
		}
	}
	existing, err := lstatIfPresent(target)
	if err != nil {
		return "", err
	}
	if existing != nil && existing.Mode()&fs.ModeSymlink != 0 {
		return "", errors.New(jsexc.Concat("Frozen overlay target is a symbolic link: ", overlayPath))
	}
	return target, nil
}

// MaterializeFrozenOverlays ports src/safe-overlay.ts:98
// materializeFrozenOverlays: frozenWitness is the schema-parsed frozen
// witness, index nil for an absent inspector. It returns the
// MaterializedOverlay facts as a JS array.
func MaterializeFrozenOverlays(worktree string, frozenWitness jsjson.Value, index OverlayIndexInspector) (jsjson.Value, error) {
	overlays := frozenWitness.Get("proposal", "witness", "overlays").Items()
	if index != nil {
		paths := make([]string, len(overlays))
		for i, overlay := range overlays {
			paths[i] = overlay.Get("path").Str()
		}
		if err := AssertOverlayTargetsNotGitSymlinks(index, paths); err != nil {
			return jsjson.Value{}, err
		}
	}
	facts := make([]jsjson.Value, 0, len(overlays))
	for _, overlay := range overlays {
		path, digest := overlay.Get("path").Str(), overlay.Get("bytesDigest").Str()
		data := jsstr.DecodeBase64Lenient(overlay.Get("bytesBase64").Str())
		if sha256Digest(data) != digest {
			return jsjson.Value{}, errors.New(jsexc.Concat("Frozen overlay digest does not match its bytes: ", path))
		}
		target, err := safeOverlayTarget(worktree, path)
		if err != nil {
			return jsjson.Value{}, err
		}
		// Replace instead of writing through an existing inode. That avoids
		// mutating an unexpected hard link and means a raced leaf symlink is
		// replaced rather than followed. The temp file is in the checked parent.
		staged := nodefs.Join(nodefs.Dirname(target), ".faultline-overlay-"+clock.NewUUID())
		err = writeStagedOverlay(staged, target, data)
		if err != nil {
			return jsjson.Value{}, err
		}
		reread, err := nodefs.ReadBytes(target)
		if err != nil {
			return jsjson.Value{}, err
		}
		if !bytes.Equal(reread, data) || sha256Digest(reread) != digest {
			return jsjson.Value{}, errors.New(jsexc.Concat("FaultLine could not verify exact materialized overlay bytes: ", path))
		}
		fact := jsjson.NewObj()
		fact.Set("path", jsjson.MakeString(path))
		fact.Set("bytesDigest", jsjson.MakeString(digest))
		fact.Set("bytesLength", jsjson.MakeNumber(float64(len(data))))
		facts = append(facts, jsjson.MakeObject(fact))
	}
	return jsjson.MakeArray(facts), nil
}

// writeStagedOverlay is the try/finally of src/safe-overlay.ts:121-129: an
// error from the finally rm replaces the try block's.
func writeStagedOverlay(staged, target string, data []byte) error {
	// The unprivileged Docker user needs to read the frozen command's
	// overlays. The container bind mount is read-only, so 0644 does not
	// grant it a way to mutate this temporary worktree.
	err := nodefs.WriteFileBytes(staged, data, 0o644, true)
	if err == nil {
		err = nodefs.Rename(staged, target)
	}
	if rmErr := RemoveForce(staged); rmErr != nil {
		return rmErr
	}
	return err
}

// RemoveForce is fs.promises.rm(path, { force: true }) in Node 22, and
// rmSync(path, { force: true }) alike (same validation and message):
// validateRmOptions lstat()s the path (ENOENT resolves, any other failure
// rejects with the lstat error, a directory rejects with ERR_FS_EISDIR),
// then rimraf removes what is there, which for a non-directory is unlink.
func RemoveForce(path string) error {
	info, err := nodefs.Lstat(path)
	if err != nil {
		var e *nodefs.Error
		if errors.As(err, &e) && e.Code == "ENOENT" {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return errors.New(jsexc.Concat("Path is a directory: rm returned EISDIR (is a directory) ", path))
	}
	return nodefs.RemoveAll(path)
}
