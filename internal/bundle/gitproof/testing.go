//go:build faultline_test

package gitproof

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// BaseFileForTest parses a file of a committed difftest base
// (difftest/testdata/bases/<base>/<rel>); other packages' tests use it.
func BaseFileForTest(t testing.TB, base, rel string) jsjson.Value {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", "..", "difftest", "testdata", "bases", base, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
