//go:build unix

package prevention

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// TestWriteRemovesStageAfterMkdir fails the first staged file write after
// the stage mkdir succeeded (a umask without owner write makes the stage
// 0500) and checks that the stage is removed and no .*.tmp is left behind.
func TestWriteRemovesStageAfterMkdir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	workspace(t)
	if _, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("first")); err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0o277)
	_, err := Write(jsjson.MakeObject(sampleInput()), defaultOutput("second"))
	syscall.Umask(old)
	if err == nil || !strings.HasPrefix(err.Error(), "EACCES: permission denied, open '"+filepath.Join(defaultRoot(), ".second.")) ||
		!strings.HasSuffix(err.Error(), ".tmp/README.md'") {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(defaultRoot())
	if len(entries) != 1 || entries[0].Name() != "first" {
		t.Fatalf("left %v", entries)
	}
}

func currentUmask() os.FileMode {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return os.FileMode(old)
}

// TestWriteRefusesReadOnlyParent makes the output parent read-only after
// prepare would create it: the stage mkdir fails and nothing is left behind.
func TestWriteRefusesReadOnlyParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	workspace(t)
	parent := filepath.Join(defaultRoot(), "ro")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(parent, 0o500)
	defer os.Chmod(parent, 0o700)
	_, err := Write(jsjson.MakeObject(sampleInput()), filepath.Join(parent, "out"))
	if err == nil || !strings.HasPrefix(err.Error(), "EACCES: permission denied, mkdir '"+parent+"/.out.") {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}
