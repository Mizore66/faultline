//go:build unix

package nodefs

import (
	"os"
	"path/filepath"
	"testing"
)

// A readable directory without search permission keeps its entry types:
// libuv reads them from the directory record, without a stat.
func TestReadDirTypesUnsearchable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory search bits")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "f"), nil, 0o644)
	os.Chmod(dir, 0o644)
	defer os.Chmod(dir, 0o755)
	got, err := ReadDirTypes(dir)
	if err != nil || len(got) != 2 || got[0] != (Dirent{Name: "f", IsFile: true}) || got[1] != (Dirent{Name: "sub", IsDir: true}) {
		t.Fatalf("%v %v", got, err)
	}
}
