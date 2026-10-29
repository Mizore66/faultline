package snapgc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const id = "ab" + "0123456789abcdef0123456789abcdef012345"

func TestPurgeRefusesReferencedThenForces(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git/faultline/objects/ab", id[2:]), "z")
	write(t, filepath.Join(repo, ".git/objects/info/alternates"), "../faultline/objects\n")
	write(t, filepath.Join(repo, ".faultline/recordings/l.json"), `{"treeDigest":"`+id+`"}`)
	_, err := Purge(repo, false)
	if err == nil || !strings.HasPrefix(err.Error(), "Refusing to purge quarantined turn-snapshot objects: still referenced by 1 path(s) under .faultline (.faultline/recordings/l.json).") {
		t.Fatalf("%v", err)
	}
	r, err := Purge(repo, true)
	if err != nil || r.Status != "PURGED" || r.RemovedEntries != 1 || len(r.ReferencingPaths) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".git/objects/info/alternates")); !os.IsNotExist(err) {
		t.Fatal("alternates kept")
	}
}

func TestListSkipsTmpAndSha256(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AB", strings.ToUpper(id[2:])), "")
	write(t, filepath.Join(dir, "ab", "x.tmp"), "")
	write(t, filepath.Join(dir, "cd", strings.Repeat("e", 62)), "")
	ids, err := ListQuarantinedObjectIDs(dir)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("%q %v", ids, err)
	}
}

func TestPurgeKeepsOtherAlternatesAndCountsFilesOnly(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git/faultline/objects/ab", id[2:]), "z")
	write(t, filepath.Join(repo, ".git/faultline/objects/info/keep"), "")
	os.MkdirAll(filepath.Join(repo, ".git/faultline/objects/pack"), 0o755)
	os.Symlink("keep", filepath.Join(repo, ".git/faultline/objects/info/link"))
	alternates := filepath.Join(repo, ".git/objects/info/alternates")
	write(t, alternates, " /x/objects \r\n\r\n../faultline/objects\r\n../faultline/objects\n")
	os.Chmod(alternates, 0o600)
	// A .txt outside the bundle directories is not a retention surface, and
	// a 64-hex run holds no 40-hex word.
	write(t, filepath.Join(repo, ".faultline/notes.txt"), id)
	write(t, filepath.Join(repo, ".faultline/x.json"), id+strings.Repeat("0", 24))
	r, err := Purge(repo, false)
	if err != nil || r.Status != "PURGED" || r.RemovedEntries != 2 || r.ReferencingPaths == nil || len(r.ReferencingPaths) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	b, err := os.ReadFile(alternates)
	if err != nil || string(b) != "/x/objects\n" {
		t.Fatalf("%q %v", b, err)
	}
	// Windows has no POSIX permission bits (chmod only toggles read-only and
	// the mode reads back 0666), so only there the 0600 assertion is not made.
	if info, _ := os.Stat(alternates); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
}

func TestPurgeRefusesSymlinkedAlternatesButNotDangling(t *testing.T) {
	repo := t.TempDir()
	alternates := filepath.Join(repo, ".git/objects/info/alternates")
	os.MkdirAll(filepath.Dir(alternates), 0o755)
	os.Symlink("missing", alternates)
	if r, err := Purge(repo, false); err != nil || r.Status != "ALREADY_EMPTY" {
		t.Fatalf("dangling: %+v %v", r, err)
	}
	write(t, filepath.Join(repo, "real"), "../faultline/objects\n")
	os.Remove(alternates)
	os.Symlink(filepath.Join(repo, "real"), alternates)
	if _, err := Purge(repo, false); err == nil || err.Error() != "Refusing to edit non-regular Git alternates file: "+alternates {
		t.Fatalf("%v", err)
	}
}

func TestFindReferencesSortsAndCaps(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"b/ledger", "a/LEDGER", "proofs/z.bin", "B.json", "x/hashes.txt", "big.json"} {
		write(t, filepath.Join(repo, ".faultline", name), "id "+strings.ToUpper(id)+" end")
	}
	big := filepath.Join(repo, ".faultline", "big.json")
	os.Truncate(big, 8*1024*1024+1)
	got, err := FindReferences(repo, map[string]bool{id: true})
	// localeCompare order, as Node sorts these.
	want := []string{".faultline/a/LEDGER", ".faultline/B.json", ".faultline/b/ledger", ".faultline/proofs/z.bin", ".faultline/x/hashes.txt"}
	if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := FindReferences(filepath.Join(repo, "missing"), map[string]bool{}); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%q %v", got, err)
	}
}
