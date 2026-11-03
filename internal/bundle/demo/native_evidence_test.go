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

// writerInput is synthetic input to the writer, not execution evidence.
func writerInput() jsjson.Value {
	return jsjson.MustObject(
		"note", jsjson.MakeString("synthetic writer input, not execution evidence"),
		"kind", jsjson.MakeString("WRITER_TEST_INPUT"),
	)
}

func nativeFixture(t *testing.T) (Written, bundle.Result, jsjson.Value) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "managed")
	out := filepath.Join(root, "demo")
	analysis := demofixture.CreateReplayAnalysis()
	written, err := Write(out, analysis, WriteOptions{ProofRoot: &root})
	if err != nil {
		t.Fatal(err)
	}
	verification := Verify(out, written.RootDigest, true)
	if !verification.Valid {
		t.Fatalf("%+v", verification)
	}
	return written, verification, analysis
}

func logNativeDiagnostic(t *testing.T, err error) {
	t.Helper()
	observed, marshalErr := json.Marshal(err.Error())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("NATIVE-DIAGNOSTIC %s", observed)
}

func TestNativeEvidenceWrite(t *testing.T) {
	written, verification, analysis := nativeFixture(t)
	provenance := writerInput()
	digest, err := canonical.DigestJSON(analysis)
	if err != nil {
		t.Fatal(err)
	}
	path, err := WriteNativeEvidence(written, verification, analysis, provenance)
	if err != nil {
		t.Fatal(err)
	}
	if want := nodefs.Resolve(written.Directory) + ".native-execution.json"; path != want || !filepath.IsAbs(path) {
		t.Fatalf("path %q want %q", path, want)
	}
	want := "{\n" +
		"  \"schemaVersion\": \"faultline.native-demo-cli-evidence.v1\",\n" +
		"  \"analysisDigest\": \"" + digest + "\",\n" +
		"  \"bundleRootDigest\": \"" + written.RootDigest + "\",\n" +
		"  \"provenance\": {\n" +
		"    \"note\": \"synthetic writer input, not execution evidence\",\n" +
		"    \"kind\": \"WRITER_TEST_INPUT\"\n" +
		"  }\n" +
		"}\n"
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("bytes\n%q\nwant\n%q", got, want)
	}

	// Regular-file replacement.
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteNativeEvidence(written, verification, analysis, provenance); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatalf("replacement bytes %q", got)
	}

	// Removal, then absent removal is a no-op.
	if err := RemoveNativeEvidence(written.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lstat after removal: %v", err)
	}
	if err := RemoveNativeEvidence(written.Directory); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEvidenceDiagnostics(t *testing.T) {
	t.Run("invalid-verification", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		verification.Valid = false
		calls := 0
		deps := nativeEvidenceDeps{
			atomicWrite: func(string, string) (string, error) { calls++; return "", nil },
			lstat:       func(string) (fs.FileInfo, error) { calls++; return nil, nil },
			unlink:      func(string) error { calls++; return nil },
		}
		_, err := writeNativeEvidence(deps, written, verification, analysis, writerInput())
		if err == nil || err.Error() != "Native evidence requires a valid bundle verification" || calls != 0 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("nil-root", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		verification.RootDigest = nil
		_, err := WriteNativeEvidence(written, verification, analysis, writerInput())
		if err == nil || err.Error() != "Native evidence requires a verified bundle root digest" {
			t.Fatalf("err=%v", err)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("root-mismatch", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		other := "sha256:" + string(make([]byte, 0)) + "0000000000000000000000000000000000000000000000000000000000000000"
		verification.RootDigest = &other
		_, err := WriteNativeEvidence(written, verification, analysis, writerInput())
		if err == nil || err.Error() != "Native evidence verified root digest does not match the written bundle root digest" {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Lstat(nativeEvidencePath(written.Directory)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("destination touched: %v", statErr)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("remove-refuses-directory", func(t *testing.T) {
		written, _, _ := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
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
		logNativeDiagnostic(t, err)
	})
	t.Run("remove-refuses-symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs privileges on Windows")
		}
		written, _, _ := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
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
		logNativeDiagnostic(t, err)
	})
	t.Run("publication-failure-removes-regular-file", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		publication := errors.New("forced publication failure")
		deps := productionNativeEvidenceDeps()
		deps.atomicWrite = func(string, string) (string, error) { return "", publication }
		_, err := writeNativeEvidence(deps, written, verification, analysis, writerInput())
		if err == nil || err.Error() != publication.Error() || !errors.Is(err, publication) {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("regular file not cleaned: %v", statErr)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("publication-and-removal-failures-are-joined", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
		if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		publication := errors.New("forced publication failure")
		removal := errors.New("forced removal failure")
		deps := productionNativeEvidenceDeps()
		deps.atomicWrite = func(string, string) (string, error) { return "", publication }
		deps.unlink = func(string) error { return removal }
		_, err := writeNativeEvidence(deps, written, verification, analysis, writerInput())
		if err == nil || err.Error() != "forced publication failure\nforced removal failure" ||
			!errors.Is(err, publication) || !errors.Is(err, removal) {
			t.Fatalf("err=%v", err)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("publication-failure-leaves-directory", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		publication := errors.New("forced publication failure")
		deps := productionNativeEvidenceDeps()
		deps.atomicWrite = func(string, string) (string, error) { return "", publication }
		_, err := writeNativeEvidence(deps, written, verification, analysis, writerInput())
		if err == nil || err.Error() != publication.Error() || !errors.Is(err, publication) {
			t.Fatalf("err=%v", err)
		}
		if info, statErr := os.Lstat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("directory disturbed: %v", statErr)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("lstat-failure-is-joined", func(t *testing.T) {
		written, verification, analysis := nativeFixture(t)
		publication := errors.New("forced publication failure")
		inspect := errors.New("forced lstat failure")
		deps := productionNativeEvidenceDeps()
		deps.atomicWrite = func(string, string) (string, error) { return "", publication }
		deps.lstat = func(string) (fs.FileInfo, error) { return nil, inspect }
		_, err := writeNativeEvidence(deps, written, verification, analysis, writerInput())
		if err == nil || err.Error() != "forced publication failure\nforced lstat failure" ||
			!errors.Is(err, publication) || !errors.Is(err, inspect) {
			t.Fatalf("err=%v", err)
		}
		logNativeDiagnostic(t, err)
	})
	t.Run("remove-unlink-failure", func(t *testing.T) {
		written, _, _ := nativeFixture(t)
		path := nativeEvidencePath(written.Directory)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		removal := errors.New("forced removal failure")
		deps := productionNativeEvidenceDeps()
		deps.unlink = func(string) error { return removal }
		err := removeNativeEvidence(deps, path, true)
		if err != removal {
			t.Fatalf("err=%v", err)
		}
		logNativeDiagnostic(t, err)
	})
}
