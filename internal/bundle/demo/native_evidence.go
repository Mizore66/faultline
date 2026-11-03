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

const (
	nativeEvidenceSchema = "faultline.native-demo-cli-evidence.v1"
	nativeEvidenceSuffix = ".native-execution.json"
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

// nativeEvidencePath is the sidecar beside a bundle directory.
func nativeEvidencePath(bundleDirectory string) string {
	return nodefs.Resolve(bundleDirectory) + nativeEvidenceSuffix
}

// WriteNativeEvidence publishes the digest-linked native execution sidecar for
// a verified bundle and returns its absolute path. On a publication error the
// destination is removed when it is a regular non-symlink file, and the
// publication and cleanup errors are joined.
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
		"schemaVersion", jsjson.MakeString(nativeEvidenceSchema),
		"analysisDigest", jsjson.MakeString(analysisDigest),
		"bundleRootDigest", jsjson.MakeString(written.RootDigest),
		"provenance", provenance,
	)
	text := jsexc.Concat(jsjson.StringifyIndent(envelope, "  "), "\n")
	path := nativeEvidencePath(written.Directory)
	published, publicationErr := deps.atomicWrite(path, text)
	if publicationErr != nil {
		cleanupErr := removeNativeEvidence(deps, path, false)
		return "", errors.Join(publicationErr, cleanupErr)
	}
	return published, nil
}

// removeNativeEvidence unlinks path only when it is a regular non-symlink
// file. strict refuses other entries; otherwise they are left untouched.
func removeNativeEvidence(deps nativeEvidenceDeps, path string, strict bool) error {
	info, err := deps.lstat(path)
	if err != nil {
		if isNotExist(err) {
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

func isNotExist(err error) bool {
	var fsErr *nodefs.Error
	if errors.As(err, &fsErr) && fsErr.Code == "ENOENT" {
		return true
	}
	return errors.Is(err, fs.ErrNotExist)
}
