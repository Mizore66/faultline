package gitinv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestFingerprintSortsAndSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0o644)
	os.Symlink("package.json", filepath.Join(dir, "go.mod"))
	fp, err := ComputeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := fp.Get("files").Obj().Keys()
	if len(files) != 2 || files[0] != "Cargo.toml" || files[1] != "package.json" {
		t.Fatalf("%v", files)
	}
	if !strings.HasPrefix(fp.Get("digest").Str(), "sha256:") {
		t.Fatal(fp.Get("digest").Str())
	}
}

func TestFingerprintSkipsDirectoriesAndEmpty(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Dockerfile"), 0o755)
	fp, err := ComputeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := fp.Get("files").Obj().Len(); n != 0 {
		t.Fatalf("%d files", n)
	}
}

const (
	dA   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dB   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	imgA = "ghcr.io/x/y@" + dA
)

func TestLoadRuntimeMapping(t *testing.T) {
	doc, _ := jsjson.Parse(`{"schemaVersion":"x","mapping":{"` + dB + `":"` + imgA + `","` + dA + `":"` + imgA + `"}}`)
	got, err := LoadRuntimeMapping(doc)
	if err != nil || len(got) != 2 || got[0].Fingerprint != dB || got[1].Fingerprint != dA {
		t.Fatalf("%v %v", got, err)
	}
	root, _ := jsjson.Parse(`{"provenanceDigest":1,"` + dA + `":"` + imgA + `"}`)
	if got, err := LoadRuntimeMapping(root); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	bad, _ := jsjson.Parse(`[]`)
	if _, err := LoadRuntimeMapping(bad); err == nil || err.Error() != "runtime mapping must be a JSON object" {
		t.Fatal(err)
	}
}

func TestHomogeneityAndResolve(t *testing.T) {
	if Homogeneity(nil) != "EMPTY" || Homogeneity([]string{dA, dA}) != "HOMOGENEOUS" || Homogeneity([]string{dA, dB}) != "HETEROGENEOUS" {
		t.Fatal("homogeneity")
	}
	if got := DistinctFingerprintDigests([]string{dB, dA, dB}); len(got) != 2 || got[0] != dA {
		t.Fatal(got)
	}
	_, err := ResolveRuntimeImageForFingerprint(dA, "HETEROGENEOUS", nil, imgA)
	if err == nil || err.Error() != EnvironmentChangedProofMessage {
		t.Fatal(err)
	}
	img, err := ResolveRuntimeImageForFingerprint(dA, "HOMOGENEOUS", nil, imgA)
	if err != nil || img != imgA {
		t.Fatal(img, err)
	}
}

func TestReadConfigRefusesSymlink(t *testing.T) {
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".faultline"), 0o755)
	if _, ok, err := ReadConfig(repo); ok || err != nil {
		t.Fatal(ok, err)
	}
	os.WriteFile(filepath.Join(repo, "real.json"), []byte("{}"), 0o644)
	os.Symlink("../real.json", filepath.Join(repo, ".faultline", "config.json"))
	_, _, err := ReadConfig(repo)
	if err == nil || !strings.HasPrefix(jsexc.Message(err), "FaultLine config must be a regular non-symlink file: ") {
		t.Fatal(err)
	}
}

func TestReadConfigImage(t *testing.T) {
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".faultline"), 0o755)
	text := `{"schemaVersion":"faultline.config.v1","image":"` + imgA + `","snapshot":{"trackedFilesOnly":false},"stores":{"incidents":"i","witnesses":"w","recordings":"r"}}`
	os.WriteFile(filepath.Join(repo, ".faultline", "config.json"), []byte(text), 0o644)
	image, ok, err := ResolveConfiguredImage(repo)
	if err != nil || !ok || image != imgA {
		t.Fatal(image, ok, err)
	}
}
