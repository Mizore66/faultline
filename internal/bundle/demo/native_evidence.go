package demo

import (
	"errors"
	"io/fs"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// nativeEvidenceDeps lets tests force publication and removal failures
// without a production selector.
type nativeEvidenceDeps struct {
	atomicWrite func(string, string) (string, error)
	lstat       func(string) (fs.FileInfo, error)
	unlink      func(string) error
}

func productionNativeEvidenceDeps() nativeEvidenceDeps {
	return nativeEvidenceDeps{atomicWrite: nodefs.AtomicWriteFile, lstat: nodefs.Lstat, unlink: nodefs.Unlink}
}

func nativeEvidencePath(bundleDirectory string) string {
	return nodefs.Resolve(bundleDirectory) + ".native-execution.json"
}

// WriteNativeEvidence atomically publishes the digest-linked native execution
// sidecar beside a verified bundle and returns its absolute path.
func WriteNativeEvidence(written Written, verification bundle.Result, analysis, provenance jsjson.Value) (string, error) {
	return writeNativeEvidence(productionNativeEvidenceDeps(), written, verification, analysis, provenance)
}

// RemoveNativeEvidence removes the sidecar of a bundle directory. An absent
// destination is a no-op; anything but a regular non-symlink file is refused.
func RemoveNativeEvidence(bundleDirectory string) error {
	return removeNativeEvidence(productionNativeEvidenceDeps(), nativeEvidencePath(bundleDirectory), true)
}

func writeNativeEvidence(deps nativeEvidenceDeps, written Written, verification bundle.Result, analysis, provenance jsjson.Value) (string, error) {
	if !verification.Valid {
		return "", errors.New("Native evidence requires a valid bundle verification")
	}
	if verification.RootDigest == nil {
		return "", errors.New("Native evidence requires a verified bundle root digest")
	}
	if *verification.RootDigest != written.RootDigest {
		return "", errors.New("Native evidence verified root digest does not match the written bundle root digest")
	}
	analysisDigest, err := canonical.DigestJSON(analysis)
	if err != nil {
		return "", err
	}
	envelope := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-demo-cli-evidence.v1"),
		"analysisDigest", jsjson.MakeString(analysisDigest),
		"bundleRootDigest", jsjson.MakeString(written.RootDigest),
		"provenance", provenance,
	)
	path := nativeEvidencePath(written.Directory)
	if err := checkNativeEvidenceDestination(deps, path); err != nil {
		return "", err
	}
	published, publicationErr := deps.atomicWrite(path, jsjson.StringifyIndent(envelope, "  ")+"\n")
	if publicationErr != nil {
		return "", errors.Join(publicationErr, removeNativeEvidence(deps, path, false))
	}
	return published, nil
}

// removeNativeEvidence unlinks path only when it is a regular non-symlink
// file. When strict, any other entry is refused; otherwise it is left alone.
func removeNativeEvidence(deps nativeEvidenceDeps, path string, strict bool) error {
	info, err := deps.lstat(path)
	if err != nil {
		var fsErr *nodefs.Error
		if errors.Is(err, fs.ErrNotExist) || (errors.As(err, &fsErr) && fsErr.Code == "ENOENT") {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		if strict {
			return errors.New(jsexc.Concat("Native evidence destination must be a regular non-symlink file: ", path))
		}
		return nil
	}
	return deps.unlink(path)
}

// checkNativeEvidenceDestination refuses a present destination that is not a
// regular non-symlink file, before anything is published.
func checkNativeEvidenceDestination(deps nativeEvidenceDeps, path string) error {
	info, err := deps.lstat(path)
	if err != nil {
		var fsErr *nodefs.Error
		if errors.Is(err, fs.ErrNotExist) || (errors.As(err, &fsErr) && fsErr.Code == "ENOENT") {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(jsexc.Concat("Native evidence destination must be a regular non-symlink file: ", path))
	}
	return nil
}
