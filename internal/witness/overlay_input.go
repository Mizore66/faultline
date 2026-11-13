package witness

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"strings"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// ModelOverlay is one model-selected overlay file as read from the staging
// root: a normalized slash path and the raw bytes in canonical base64.
type ModelOverlay struct {
	Path, BytesBase64 string
}

type overlayOps struct {
	lstat func(string) (fs.FileInfo, error)
	read  func(string) ([]byte, error)
}

var nodeOverlayOps = overlayOps{lstat: nodefs.Lstat, read: nodefs.ReadBytes}

// ReadModelOverlayInput is src/overlay-input.ts readModelOverlayInput: it
// reads each selected path sequentially through real directories beneath
// root. There is deliberately no byte cap, no root-ancestor check, and no
// stronger open-at protocol: the lstat/read window is the frozen behavior.
func ReadModelOverlayInput(root string, paths []string) ([]ModelOverlay, error) {
	return readModelOverlayInput(nodeOverlayOps, root, paths)
}

func overlayParts(root, path string) ([]string, error) {
	unsafe := errors.New("Model proposed an unsafe overlay path: " + path)
	target := nodefs.Resolve(root, path)
	nested := nodefs.Relative(root, target)
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) || strings.Contains(path, `\`) {
		return nil, unsafe
	}
	var parts []string
	for _, p := range strings.FieldsFunc(nested, func(r rune) bool { return r == '/' || r == '\\' }) {
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, unsafe
	}
	for _, p := range parts {
		if p == "." || p == ".." {
			return nil, unsafe
		}
	}
	return parts, nil
}

func readModelOverlayInput(ops overlayOps, overlayRoot string, paths []string) ([]ModelOverlay, error) {
	root := nodefs.Resolve(overlayRoot)
	info, err := ops.lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("Overlay root must be a real directory: " + root)
	}
	out := make([]ModelOverlay, 0, len(paths))
	for _, path := range paths {
		parts, err := overlayParts(root, path)
		if err != nil {
			return nil, err
		}
		current := root
		for _, part := range parts[:len(parts)-1] {
			current = nodefs.Join(current, part)
			st, err := ops.lstat(current)
			if err != nil {
				return nil, err
			}
			if !st.IsDir() || st.Mode()&fs.ModeSymlink != 0 {
				return nil, errors.New("Model overlay cannot traverse a symbolic link or non-directory: " + path)
			}
		}
		leaf := nodefs.Join(append([]string{root}, parts...)...)
		st, err := ops.lstat(leaf)
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Mode()&fs.ModeSymlink != 0 {
			return nil, errors.New("Model overlay must be a regular non-symlink file: " + path)
		}
		raw, err := ops.read(leaf)
		if err != nil {
			return nil, err
		}
		out = append(out, ModelOverlay{
			Path:        strings.Join(parts, "/"),
			BytesBase64: base64.StdEncoding.EncodeToString(raw),
		})
	}
	return out, nil
}
