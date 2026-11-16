//go:build faultline_test

package difftest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// guidedWrapperGuardRow is one committed diagnostic. stdout, stderr and exit
// follow the frozen TypeScript: the guard in src/cli-app.ts throws and
// src/cli.ts main().catch prints formatCliFailure(error) then sets exit code 1.
// The rows are data written from those sources, never captured from Go output.
type guidedWrapperGuardRow struct {
	ID     string   `json:"id"`
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
	Source string   `json:"source"`
}

// TestGuidedWrapperGuardsReplay runs the covered fl binary for the guided
// wrapper guards that fire before doctor, so no tool, repository or Docker is
// needed. It does not require Node.
func TestGuidedWrapperGuardsReplay(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "guided-investigate", "diagnostics", "wrapper-guards.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row guidedWrapperGuardRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.ID == "" || row.Stderr == "" || len(row.Args) == 0 || row.Exit == 0 || slices.Contains(ids, row.ID) {
			t.Fatalf("invalid or duplicate diagnostic row %q", row.ID)
		}
		ids = append(ids, row.ID)
		t.Run(row.ID, func(t *testing.T) {
			dir, home := t.TempDir(), t.TempDir()
			env := map[string]string{"HOME": home, "USERPROFILE": home, "TMPDIR": home, "TEMP": home, "TMP": home}
			stdout, stderr, code := runFlOnce(t, dir, env, row.Args...)
			if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
				t.Fatalf("stdout=%q stderr=%q exit=%d; want %q %q %d", stdout, stderr, code, row.Stdout, row.Stderr, row.Exit)
			}
			// No unintended effects: the guard fires before any file is written.
			for _, root := range []string{dir, home} {
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("%s has effects: %v %v", root, entries, err)
				}
			}
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 {
		t.Fatalf("expected 5 committed wrapper-guard rows, got %v", ids)
	}
}
