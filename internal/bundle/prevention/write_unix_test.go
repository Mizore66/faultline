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
