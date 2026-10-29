package attest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOnceAndVerify(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".faultline", "attestations")
	in := CreationInput{ReceiptID: "r1", Subject: "s", Issuer: "i", BundleRootDigest: "sha256:" + strings.Repeat("a", 64)}
	w, err := Write(root, in, root)
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyStored(root, "r1", &w.ReceiptDigest, root)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	if _, err := Write(root, in, root); err == nil || err.Error() != "Attestation receipt already exists and cannot be overwritten: "+w.Path {
		t.Fatalf("%v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("a staging file was left: %v", entries)
	}
}

func TestStoreOutsideRootRefused(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if _, err := Write(t.TempDir(), CreationInput{ReceiptID: "r1", Subject: "s", Issuer: "i", BundleRootDigest: "sha256:" + strings.Repeat("a", 64)}, root); err == nil ||
		!strings.HasPrefix(err.Error(), "Attestation store must be inside the managed root: ") {
		t.Fatalf("%v", err)
	}
}
