//go:build faultline_test

package witness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func ptr(s string) *string { return &s }

func TestSessionBindingWriteReadInherit(t *testing.T) {
	clock.ResetForTest("binding")
	store := filepath.Join(t.TempDir(), "drafts", "nested")
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := WriteSessionBinding(store, BindingInput{IncidentID: "inc-1", LedgerPath: ptr("/ledger.json")}); err != nil {
		t.Fatal(err)
	}
	b, err := WriteSessionBinding(store, BindingInput{IncidentID: "inc-1", ExpectDigest: &digest})
	if err != nil {
		t.Fatal(err)
	}
	if b.Get("ledgerPath").Str() != "/ledger.json" || b.Get("updatedAt").Str() != "2026-07-16T12:00:00.001Z" {
		t.Fatalf("previous ledger not kept or clock misread: %s %s", b.Get("ledgerPath").Str(), b.Get("updatedAt").Str())
	}
	path := filepath.Join(store, "bindings", "inc-1.json")
	text, _ := os.ReadFile(path)
	want := `{"expectDigest":"` + digest + `","incidentId":"inc-1","ledgerPath":"/ledger.json","schemaVersion":"faultline.incident-session-binding.v1","updatedAt":"2026-07-16T12:00:00.001Z"}` + "\n"
	if string(text) != want {
		t.Fatalf("%s", text)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	facts := ResolveInheritedSessionFacts(InheritInput{StoreDirectory: store, IncidentID: "inc-1", LedgerPath: ptr("/explicit")})
	if *facts.ExpectDigest != digest || *facts.LedgerPath != "/explicit" || !facts.InheritedExpectDigest || facts.InheritedLedgerPath {
		t.Fatalf("%+v", facts)
	}
	if _, ok, err := ReadSessionBinding(store, "other"); ok || err != nil {
		t.Fatalf("missing binding: %v %v", ok, err)
	}
}

func TestSessionBindingRefusesSymlinksAndMismatches(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := WriteSessionBinding(filepath.Join(root, "link", "s"), BindingInput{IncidentID: "x", UpdatedAt: ptr("2020-01-01T00:00:00Z")})
	if err == nil || !strings.HasPrefix(err.Error(), "Incident binding store path must be a real non-symlink directory: ") {
		t.Fatalf("%v", err)
	}
	store := filepath.Join(root, "s")
	os.MkdirAll(filepath.Join(store, "bindings"), 0o700)
	os.WriteFile(filepath.Join(store, "bindings", "x.json"), []byte(`{"schemaVersion":"faultline.incident-session-binding.v1","incidentId":"y","updatedAt":"2020-01-01T00:00:00Z"}`), 0o600)
	if _, _, err := ReadSessionBinding(store, "x"); err == nil || err.Error() != "Stored incident session binding identifier does not match the requested identifier" {
		t.Fatalf("%v", err)
	}
}

func TestEnsureDirectoryIsSafeNodeErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	os.WriteFile(file, nil, 0o600)
	if err := EnsureDirectoryIsSafe(filepath.Join(file, "sub")); err == nil || err.Error() != "ENOTDIR: not a directory, mkdir '"+filepath.Join(file, "sub")+"'" {
		t.Fatalf("%v", err)
	}
	if err := EnsureDirectoryIsSafe(file); err == nil || err.Error() != "Witness lock directory must be a non-symlink directory: "+file {
		t.Fatalf("%v", err)
	}
}
