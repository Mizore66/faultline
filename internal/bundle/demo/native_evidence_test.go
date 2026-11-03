package demo

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// syntheticProvenance is writer input only, not execution evidence.
func syntheticProvenance() jsjson.Value {
	return jsjson.MustObject(
		"label", jsjson.MakeString("synthetic writer input, not execution evidence"),
		"order", jsjson.MakeNumber(1),
	)
}

func verifiedReplay(t *testing.T) (Written, bundle.Result, jsjson.Value) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "managed")
	analysis := demofixture.CreateReplayAnalysis()
	written, err := Write(filepath.Join(root, "demo"), analysis, WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	verification := Verify(written.Directory, written.RootDigest, true)
	if !verification.Valid || verification.RootDigest == nil {
		t.Fatalf("%+v", verification)
	}
	return written, verification, analysis
}

func emitDiagnostic(t *testing.T, err error) {
	t.Helper()
	observed, marshalErr := json.Marshal(err.Error())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", observed)
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s should be absent: %v", path, err)
	}
}

func TestNativeEvidenceWrite(t *testing.T) {
	written, verification, analysis := verifiedReplay(t)
	digest, err := canonical.DigestJSON(analysis)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"schemaVersion\": \"faultline.native-demo-cli-evidence.v1\",\n" +
		"  \"analysisDigest\": \"" + digest + "\",\n" +
		"  \"bundleRootDigest\": \"" + written.RootDigest + "\",\n" +
		"  \"provenance\": {\n" +
		"    \"label\": \"synthetic writer input, not execution evidence\",\n" +
		"    \"order\": 1\n" +
		"  }\n" +
		"}\n"
	wantPath := nodefs.Resolve(written.Directory) + ".native-execution.json"

	// An existing regular file is replaced.
	if err := os.WriteFile(wantPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := WriteNativeEvidence(written, verification, analysis, syntheticProvenance())
	if err != nil {
		t.Fatal(err)
	}
	if path != wantPath || !filepath.IsAbs(path) {
		t.Fatalf("path %q want %q", path, wantPath)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("bytes %q want %q", got, want)
	}
	if err := RemoveNativeEvidence(written.Directory); err != nil {
		t.Fatal(err)
	}
	absent(t, path)
	if err := RemoveNativeEvidence(written.Directory); err != nil {
		t.Fatalf("absent removal must be a no-op: %v", err)
	}
}

func TestNativeEvidenceDiagnostics(t *testing.T) {
	forced := errors.New("forced publication failure")
	failing := func() nativeEvidenceDeps {
		d := nativeEvidenceDeps{atomicWrite: nodefs.AtomicWriteFile, lstat: nodefs.Lstat, unlink: nodefs.Unlink}
		d.atomicWrite = func(string, string) (string, error) { return "", forced }
		return d
	}
	t.Run("invalid-verification-touches-nothing", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		verification.Valid = false
		calls := 0
		deps := nativeEvidenceDeps{
			atomicWrite: func(string, string) (string, error) { calls++; return "", nil },
			lstat:       func(string) (fs.FileInfo, error) { calls++; return nil, nil },
			unlink:      func(string) error { calls++; return nil },
		}
		_, err := writeNativeEvidence(deps, written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != "Native evidence requires a valid bundle verification" || calls != 0 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		emitDiagnostic(t, err)
	})
	t.Run("nil-root", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		verification.RootDigest = nil
		_, err := WriteNativeEvidence(written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != "Native evidence requires a verified bundle root digest" {
			t.Fatalf("err=%v", err)
		}
		emitDiagnostic(t, err)
	})
	t.Run("root-mismatch", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		other := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		verification.RootDigest = &other
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := WriteNativeEvidence(written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != "Native evidence verified root digest does not match the written bundle root digest" {
			t.Fatalf("err=%v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "keep" {
			t.Fatalf("pre-existing destination changed: %q", got)
		}
		emitDiagnostic(t, err)
	})
	t.Run("remove-refuses-directory", func(t *testing.T) {
		written, _, _ := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		err := RemoveNativeEvidence(written.Directory)
		if err == nil || err.Error() != "Native evidence destination must be a regular non-symlink file: "+path {
			t.Fatalf("err=%v", err)
		}
		if info, statErr := os.Lstat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("directory disturbed: %v", statErr)
		}
		emitDiagnostic(t, err)
	})
	t.Run("remove-refuses-symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs privileges on Windows")
		}
		written, _, _ := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		err := RemoveNativeEvidence(written.Directory)
		if err == nil || err.Error() != "Native evidence destination must be a regular non-symlink file: "+path {
			t.Fatalf("err=%v", err)
		}
		if info, statErr := os.Lstat(path); statErr != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("symlink disturbed: %v", statErr)
		}
		if got, _ := os.ReadFile(target); string(got) != "keep" {
			t.Fatalf("target %q", got)
		}
		emitDiagnostic(t, err)
	})
	t.Run("publication-failure-cleans-regular-file", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := writeNativeEvidence(failing(), written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != forced.Error() || !errors.Is(err, forced) {
			t.Fatalf("err=%v", err)
		}
		absent(t, path)
		emitDiagnostic(t, err)
	})
	t.Run("publication-failure-leaves-directory", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := writeNativeEvidence(failing(), written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != forced.Error() || !errors.Is(err, forced) {
			t.Fatalf("err=%v", err)
		}
		if info, statErr := os.Lstat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("directory disturbed: %v", statErr)
		}
		emitDiagnostic(t, err)
	})
	t.Run("publication-and-removal-failures-joined", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		removal := errors.New("forced removal failure")
		deps := failing()
		deps.unlink = func(string) error { return removal }
		_, err := writeNativeEvidence(deps, written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != "forced publication failure\nforced removal failure" ||
			!errors.Is(err, forced) || !errors.Is(err, removal) {
			t.Fatalf("err=%v", err)
		}
		emitDiagnostic(t, err)
	})
	t.Run("publication-and-lstat-failures-joined", func(t *testing.T) {
		written, verification, analysis := verifiedReplay(t)
		inspect := errors.New("forced lstat failure")
		deps := failing()
		deps.lstat = func(string) (fs.FileInfo, error) { return nil, inspect }
		_, err := writeNativeEvidence(deps, written, verification, analysis, syntheticProvenance())
		if err == nil || err.Error() != "forced publication failure\nforced lstat failure" ||
			!errors.Is(err, forced) || !errors.Is(err, inspect) {
			t.Fatalf("err=%v", err)
		}
		emitDiagnostic(t, err)
	})
	t.Run("remove-reports-unlink-failure", func(t *testing.T) {
		written, _, _ := verifiedReplay(t)
		path := nodefs.Resolve(written.Directory) + ".native-execution.json"
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		removal := errors.New("forced removal failure")
		deps := nativeEvidenceDeps{atomicWrite: nodefs.AtomicWriteFile, lstat: nodefs.Lstat,
			unlink: func(string) error { return removal }}
		err := removeNativeEvidence(deps, path, true)
		if !errors.Is(err, removal) || err.Error() != removal.Error() {
			t.Fatalf("err=%v", err)
		}
		emitDiagnostic(t, err)
	})
}
