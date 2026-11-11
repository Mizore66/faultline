//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUIDiagnosticsReplay runs the covered fl binary on invalid `ui --port`
// values and compares exact output with the hand-committed diagnostic, so the
// coverage gate sees the internal/cli/ui.go port check execute.
func TestUIDiagnosticsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "difftest", "testdata", "ui-hub", "ui-diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var row struct{ ID, Text string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.ID == "ui-invalid-port" {
			text = row.Text
		}
	}
	if text == "" {
		t.Fatal("no committed ui-invalid-port diagnostic")
	}
	want := "FaultLine error: " + text + "\nNext: pnpm fl help\n"
	for _, port := range []string{"abc", "65536", "-1"} {
		stdout, stderr, code := runFl(t, t.TempDir(), nil, "ui", "--port", port)
		if code != 1 || stdout != "" || stderr != want {
			t.Fatalf("--port %s: code=%d stdout=%q stderr=%q, want stderr %q", port, code, stdout, stderr, want)
		}
	}
}
