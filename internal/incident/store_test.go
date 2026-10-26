package incident

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/witness"
)

func sampleDraft(t *testing.T) jsjson.Value {
	t.Helper()
	draft, err := CreateDraft(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func canonicalText(t *testing.T, v jsjson.Value) string {
	t.Helper()
	text, err := canonical.CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// Port of tests/incident-store.test.ts:23.
func TestStoreWritesCanonicalWriteOnceDrafts(t *testing.T) {
	store := witness.DefaultIncidentDraftStore(t.TempDir())
	path, err := WriteDraft(store, sampleDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("incidents", "drafts", "first-incident.json")) {
		t.Fatalf("path %s", path)
	}
	// Windows has no POSIX modes: Go and Node both report 0666/0777 there.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && dir.Mode().Perm() != 0o700 {
		t.Fatalf("drafts mode %o", dir.Mode().Perm())
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != canonicalText(t, sampleDraft(t))+"\n" {
		t.Fatalf("bytes %s", bytes)
	}
	read, err := ReadDraft(store, "first-incident")
	if err != nil || canonicalText(t, read) != canonicalText(t, sampleDraft(t)) {
		t.Fatalf("read: %v", err)
	}
	if _, err := WriteDraft(store, sampleDraft(t)); err == nil || err.Error() != "Immutable incident draft already exists: "+path {
		t.Fatalf("second write: %v", err)
	}
}

// Port of tests/incident-store.test.ts:37.
func TestStoreRejectsChangedDigestBeforeWriting(t *testing.T) {
	store := witness.DefaultIncidentDraftStore(t.TempDir())
	altered := sampleDraft(t)
	altered.Obj().Set("draftDigest", jsjson.MakeString("sha256:"+strings.Repeat("f", 64)))
	_, err := WriteDraft(store, altered)
	if err == nil || err.Error() != "Refusing to persist an invalid incident draft: draft digest does not match the immutable draft payload" {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(filepath.Join(store, "drafts")); !os.IsNotExist(err) {
		t.Fatalf("drafts directory created: %v", err)
	}
}

// Port of tests/incident-store.test.ts:49.
func TestStoreRefusesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	linked := filepath.Join(root, ".faultline")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linked); err != nil {
		t.Skip(err)
	}
	_, err := WriteDraft(filepath.Join(linked, "incidents"), sampleDraft(t))
	if err == nil || !strings.Contains(err.Error(), "Incident draft store path must be a real non-symlink directory: ") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "incidents")); !os.IsNotExist(err) {
		t.Fatalf("wrote through the link: %v", err)
	}
	if _, err := ReadDraft(filepath.Join(linked, "incidents"), "first-incident"); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("read: %v", err)
	}
}

func TestStoreReadRefusals(t *testing.T) {
	store := witness.DefaultIncidentDraftStore(t.TempDir())
	if _, err := ReadDraft(store, "first-incident"); err == nil || !strings.HasPrefix(err.Error(), "Incident draft store directory does not exist: ") {
		t.Fatalf("missing store: %v", err)
	}
	if _, err := WriteDraft(store, sampleDraft(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDraft(store, "missing"); err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, lstat '") {
		t.Fatalf("missing draft: %v", err)
	}
	if _, err := ReadDraft(store, "../x"); err == nil || !strings.Contains(err.Error(), "draftId must be a safe identifier") {
		t.Fatalf("unsafe id: %v", err)
	}
	drafts := filepath.Join(store, "drafts")
	if err := os.WriteFile(filepath.Join(drafts, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDraft(store, "bad"); err == nil || !strings.HasPrefix(err.Error(), "Incident draft is not valid JSON: ") {
		t.Fatalf("bad json: %v", err)
	}
	// Symlinks need a privilege on Windows: skip only this check without it.
	if err := os.Symlink("first-incident.json", filepath.Join(drafts, "link.json")); err != nil {
		t.Logf("symlink check skipped: %v", err)
	} else if _, err := ReadDraft(store, "link"); err == nil || !strings.HasPrefix(err.Error(), "Incident draft must be a regular non-symlink file: ") {
		t.Fatalf("link: %v", err)
	}
	bytes, err := os.ReadFile(filepath.Join(drafts, "first-incident.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "renamed.json"), bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDraft(store, "renamed"); err == nil || err.Error() != "Stored incident draft identifier does not match the requested identifier" {
		t.Fatalf("renamed: %v", err)
	}
}
