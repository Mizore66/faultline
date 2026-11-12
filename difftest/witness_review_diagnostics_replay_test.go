//go:build faultline_test

package difftest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWitnessReviewDiagnosticsReplay runs the covered fl binary on the
// refused `witness review` invocations and compares exact output with the
// hand-committed diagnostics, so the coverage gate sees the
// internal/cli/witness_review.go usage and port checks execute.
func TestWitnessReviewDiagnosticsReplay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "difftest", "testdata", "witness-review", "cli-diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var row struct{ ID, Text string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		texts[row.ID] = row.Text
	}
	for _, id := range []string{"witness-review-usage", "witness-review-invalid-port"} {
		if texts[id] == "" {
			t.Fatalf("no committed %s diagnostic", id)
		}
	}
	want := func(id string) string { return "FaultLine error: " + texts[id] + "\nNext: pnpm fl help\n" }
	for _, tc := range []struct {
		id   string
		args []string
	}{
		{"witness-review-usage", []string{"witness", "review"}},
		{"witness-review-invalid-port", []string{"witness", "review", "demo-1", "--port", "abc"}},
		{"witness-review-invalid-port", []string{"witness", "review", "demo-1", "--port", "65536"}},
		{"witness-review-invalid-port", []string{"witness", "review", "demo-1", "--port", "-1"}},
		{"witness-review-invalid-port", []string{"witness", "review", "demo-1", "--port", "1.5"}},
	} {
		stdout, stderr, code := runFl(t, t.TempDir(), nil, tc.args...)
		if code != 1 || stdout != "" || stderr != want(tc.id) {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q, want stderr %q", tc.args, code, stdout, stderr, want(tc.id))
		}
	}
}
