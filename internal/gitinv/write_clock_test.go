//go:build faultline_test

package gitinv

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func TestWriteRuntimeMappingRejectsConflicts(t *testing.T) {
	clock.ResetForTest("mapping")
	fp := "sha256:" + strings.Repeat("a", 64)
	img := "node@sha256:" + strings.Repeat("b", 64)
	img2 := "node@sha256:" + strings.Repeat("c", 64)
	_, err := WriteRuntimeMapping(RuntimeMappingWrite{Pairs: []MappingEntry{{fp, img}, {fp, img2}}, OutputPath: filepath.Join(t.TempDir(), "m.json")})
	if err == nil || err.Error() != "conflicting runtime mapping for "+fp {
		t.Fatalf("%v", err)
	}
}
