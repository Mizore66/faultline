package allowlist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/redaction"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil || len(a.Entries) != 0 {
		t.Fatalf("got %v, %v", a, err)
	}
	// allowlistConfigDigest({schemaVersion, entries: []}) from TS.
	if got := ConfigDigest(a); got != "sha256:c5edaf730c99ce840a0a5377628298f685f20c4d6948caa506b0e5274d188649" {
		t.Fatalf("empty digest %s", got)
	}
	if ConfigDigest(Allowlist{}) != ConfigDigest(a) {
		t.Fatal("zero value differs from Empty")
	}
}

func TestLoadRejectsSymlinkDirectoryAndOversize(t *testing.T) {
	const notRegular = ".faultline-secret-allowlist.json must be a regular non-symlink file."
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target.json"), []byte(`{"schemaVersion":"faultline.secret-allowlist.v1","entries":[]}`), 0o644)
	if err := os.Symlink("target.json", filepath.Join(dir, Filename)); err != nil {
		t.Skip(err)
	}
	if _, err := Load(dir); err == nil || err.Error() != notRegular {
		t.Fatalf("symlink: %v", err)
	}
	dir = t.TempDir()
	os.Mkdir(filepath.Join(dir, Filename), 0o755)
	if _, err := Load(dir); err == nil || err.Error() != notRegular {
		t.Fatalf("directory: %v", err)
	}
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, Filename), make([]byte, 256*1024+1), 0o644)
	if _, err := Load(dir); err == nil || err.Error() != ".faultline-secret-allowlist.json exceeds FaultLine's 256 KiB parse limit." {
		t.Fatalf("oversize: %v", err)
	}
	// Exactly 256 KiB is parsed (and fails as JSON).
	os.WriteFile(filepath.Join(dir, Filename), []byte(strings.Repeat(" ", 256*1024)), 0o644)
	if _, err := Load(dir); err == nil || err.Error() != "Unexpected end of JSON input" {
		t.Fatalf("256 KiB: %v", err)
	}
}

func TestLoadSchemaErrorAndKeyOrder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, Filename), []byte(`{"schemaVersion":"faultline.secret-allowlist.v1","entries":[{"path":"","kind":"k","occurrenceDigest":"x"}]}`), 0o644)
	_, err := Load(dir)
	want := "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 1,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at least 1 character(s)\",\n    \"path\": [\n      \"entries\",\n      0,\n      \"path\"\n    ]\n  },\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"Invalid\",\n    \"path\": [\n      \"entries\",\n      0,\n      \"occurrenceDigest\"\n    ]\n  }\n]"
	if err == nil || jsexc.Message(err) != want {
		t.Fatalf("got %v", err)
	}
	// zod re-emits keys in schema order; the digest is over that output.
	os.WriteFile(filepath.Join(dir, Filename), []byte(`{"entries":[{"note":"n","kind":"OPENAI_API_KEY","path":"a.txt","occurrenceDigest":"sha256:`+strings.Repeat("a", 64)+`"}],"schemaVersion":"faultline.secret-allowlist.v1"}`), 0o644)
	a, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsjson.Stringify(a.Value()); got != `{"schemaVersion":"faultline.secret-allowlist.v1","entries":[{"path":"a.txt","kind":"OPENAI_API_KEY","occurrenceDigest":"sha256:`+strings.Repeat("a", 64)+`","note":"n"}]}` {
		t.Fatalf("value %s", got)
	}
	if got := ConfigDigest(a); got != "sha256:518ef20eee259ee19bfe86b9da75088337e350af71c47aaa5b1b9844d9872186" {
		t.Fatalf("digest %s", got)
	}
}

func TestFilterAndEntropyMatching(t *testing.T) {
	d := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	fileDigest := d("f")
	other := d("e")
	a := Allowlist{Entries: []Entry{
		{Path: "a.txt", Kind: "OPENAI_API_KEY", OccurrenceDigest: d("1")},
		{Path: "a.txt", Kind: "GITHUB_TOKEN", OccurrenceDigest: d("2"), FileDigest: &other},
		{Path: "a.txt", Kind: "HIGH_ENTROPY_TOKEN", OccurrenceDigest: "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae", FileDigest: &fileDigest},
	}}
	occ := []redaction.Occurrence{
		{Kind: "OPENAI_API_KEY", Confidence: "HIGH", Digest: d("1")},
		{Kind: "GITHUB_TOKEN", Confidence: "HIGH", Digest: d("2")},
		{Kind: "GENERIC_SECRET_ASSIGNMENT", Confidence: "LIMITED", Digest: d("3")},
		{Kind: "AWS_ACCESS_KEY_ID", Confidence: "HIGH", Digest: d("4")},
		{Kind: "GITHUB_TOKEN", Confidence: "HIGH", Digest: d("5")},
	}
	left := FilterHighOccurrences(occ, fileDigest, "a.txt", a)
	if got := strings.Join(Kinds(left), ","); got != "GITHUB_TOKEN,AWS_ACCESS_KEY_ID" || len(left) != 3 {
		t.Fatalf("remaining %q (%d)", got, len(left))
	}
	if got := len(FilterHighOccurrences(occ, fileDigest, "b.txt", a)); got != 4 {
		t.Fatalf("other path kept %d", got)
	}
	if !IsEntropyTokenAllowlisted("foo", fileDigest, "a.txt", a) || IsEntropyTokenAllowlisted("foo", other, "a.txt", a) ||
		IsEntropyTokenAllowlisted("bar", fileDigest, "a.txt", a) {
		t.Fatal("entropy entry matching")
	}
}

func TestFileContentDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("foobar"), 0o644)
	if got, _ := FileContentDigest(path, 3); got != "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae" {
		t.Fatalf("prefix digest %s", got)
	}
	if got, _ := FileContentDigest(path, 100); got != "sha256:c3ab8ff13720e8ad9047dd39466b3c8974e592c2fa383d4a3960714caef0c4f2" {
		t.Fatalf("whole digest %s", got)
	}
	if _, err := FileContentDigest(path+"x", 3); err == nil || !strings.HasPrefix(err.Error(), "ENOENT: no such file or directory, open ") {
		t.Fatalf("missing: %v", err)
	}
}
