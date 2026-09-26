package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiesCommittedRerunBase(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "demo-rerun")
	root, err := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(dir, "", false)
	if !r.Valid || r.ExternalRootStatus != "NOT_PROVIDED" || r.RootDigest == nil || *r.RootDigest != strings.TrimSpace(string(root)) {
		t.Fatalf("result = %+v", r)
	}
	if r := Verify(dir, "", true); r.ExternalRootStatus != "NOT_PROVIDED" {
		t.Fatal(`TS treats --expect-root "" as not provided (truthiness)`)
	}
	if r := Verify(filepath.Join(dir, "nope"), "", false); r.Valid || r.Errors[0] != "bundle directory does not exist" {
		t.Fatalf("missing dir result = %+v", r)
	}
}
