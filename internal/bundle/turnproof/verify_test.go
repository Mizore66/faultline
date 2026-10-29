package turnproof

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// base is difftest/testdata/bases/turn-two-states, found from this file.
func base(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..", "..", "difftest", "testdata", "bases", "turn-two-states")
}

func TestVerifyBaseIsValid(t *testing.T) {
	root, err := os.ReadFile(filepath.Join(base(t), "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(base(t), strings.TrimSpace(string(root)), true)
	if !v.Valid || v.ExternalRootStatus != "MATCH" || v.CheckedFiles != 14 {
		t.Fatalf("%+v", v.Result)
	}
	if v.Manifest.Get("schemaVersion").Str() != BundleSchemaVersion {
		t.Fatalf("manifest %v", v.Manifest)
	}
}

func TestEmptyExpectedRootIsProvided(t *testing.T) {
	v := Verify(base(t), "", true)
	if v.Valid || v.ExternalRootStatus != "MISMATCH" || v.Errors[0] != "externally supplied turn proof root is not a sha256 digest" {
		t.Fatalf("%+v", v.Result)
	}
}

func TestVerifyMissingDirectory(t *testing.T) {
	v := Verify(filepath.Join(t.TempDir(), "absent"), "", false)
	if v.Valid || v.CheckedFiles != 0 || v.RootDigest != nil || v.ExternalRootStatus != "NOT_PROVIDED" ||
		len(v.Errors) != 1 || v.Errors[0] != "Turn proof bundle directory does not exist" {
		t.Fatalf("%+v", v.Result)
	}
}

// copyBase copies the base bundle into a fresh directory.
func copyBase(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "bundle")
	err := filepath.WalkDir(base(t), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base(t), path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestVerifyMissingPackThrowsToOuterCatch: the pack is read outside any
// inner try (:946), so a missing pack ends verification with checkedFiles 0.
func TestVerifyMissingPackThrowsToOuterCatch(t *testing.T) {
	dir := copyBase(t)
	if err := os.Remove(filepath.Join(dir, "source", "trees.pack")); err != nil {
		t.Fatal(err)
	}
	v := Verify(dir, "", false)
	last := v.Errors[len(v.Errors)-1]
	if v.Valid || v.CheckedFiles != 0 || v.RootDigest == nil || !strings.HasPrefix(last, "Turn proof bundle verification failed safely: ") {
		t.Fatalf("%+v", v.Result)
	}
	if v.Manifest.Kind() == jsjson.Undefined {
		t.Fatal("the manifest parsed before the throw is kept")
	}
}
