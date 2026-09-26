package gitproof

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommittedRunAuditsValidate(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "git-unbound", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		run := baseFile(t, "git-unbound", "runs/"+e.Name())
		if errs := ValidateSandboxPlanAudit(run.Get("sandbox")); len(errs) != 0 {
			t.Fatalf("%s: %q", e.Name(), errs)
		}
	}
}
