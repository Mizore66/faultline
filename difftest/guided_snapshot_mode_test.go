//go:build faultline_test

package difftest

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// TestGuidedSnapshotModeObservation drives the real guidedPlan.snapshot over owned entries whose permissions are
// set explicitly (so the umask is not a factor) and asserts the exact observed kinds, bytes and modes. The
// frozen generator observes lstatSync(path).mode & 0o777; on Windows libuv reports 0666 (0444 for READONLY)
// for files and directories alike, which the snapshot obtains through nodefs.Lstat (assignStat in
// internal/nodefs/lstat_windows.go). The expected table is the observation each platform must produce, not a
// waiver: every platform runs and compares exactly.
func TestGuidedSnapshotModeObservation(t *testing.T) {
	slot := t.TempDir()
	dir := filepath.Join(slot, "dir")
	file := filepath.Join(dir, "file.txt")
	ro := filepath.Join(slot, "readonly.txt")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("writable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ro, []byte("read-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Restore write access before TempDir removal, which cannot delete a Windows read-only file.
	t.Cleanup(func() { _ = os.Chmod(ro, 0o600) })
	for path, mode := range map[string]os.FileMode{dir: 0o750, file: 0o640, ro: 0o400} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	dirMode, fileMode, roMode := "750", "640", "400"
	if runtime.GOOS == "windows" {
		dirMode, fileMode, roMode = "666", "666", "444"
	}
	want := []guidedFileEntry{
		{Path: "dir", Kind: "dir", Mode: dirMode},
		{Path: "dir/file.txt", Kind: "file", Mode: fileMode, Bytes: []byte("writable\n")},
		{Path: "readonly.txt", Kind: "file", Mode: roMode, Bytes: []byte("read-only\n")},
	}
	got := (&guidedPlan{slot: slot}).snapshot(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot on %s:\n got  %+v\n want %+v", runtime.GOOS, got, want)
	}
}
