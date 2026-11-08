package proofview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// incidentDiagnostic returns one committed expectation from
// interactive-diagnostics.jsonl. The file is hand-committed evidence, never
// generated from the code under test.
func incidentDiagnostic(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile("../../difftest/testdata/incident-renderer/interactive-diagnostics.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		var row struct{ ID, Text string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.ID == id {
			return row.Text
		}
	}
	t.Fatalf("no committed diagnostic %q", id)
	return ""
}

func TestInteractiveIncidentReplaceOnceDrift(t *testing.T) {
	want := incidentDiagnostic(t, "replace-count-drift")
	for _, page := range []string{"no marker", "<script><script>"} {
		func() {
			defer func() {
				if got, _ := recover().(string); got != want {
					t.Errorf("page %q: panic %q, want %q", page, got, want)
				}
			}()
			incidentReplaceOnce(page, "<script>", "x")
		}()
	}
	if got := incidentReplaceOnce("a<script>b", "<script>", "x"); got != "axb" {
		t.Fatalf("single replace: %q", got)
	}
}

const incidentDriftChildEnv = "FAULTLINE_INCIDENT_DRIFT_CHILD"

// TestInteractiveIncidentListenerDriftChild only runs inside the mutated
// overlay build started by TestInteractiveIncidentListenerDrift. It renders the
// disabled page and reports the recovered panic on a marked line.
func TestInteractiveIncidentListenerDriftChild(t *testing.T) {
	if os.Getenv(incidentDriftChildEnv) == "" {
		t.Skip("child of TestInteractiveIncidentListenerDrift")
	}
	analysis, err := jsjson.Parse(incidentTestLoad(t).rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		r, _ := recover().(string)
		t.Logf("DRIFT-DIAGNOSTIC:%s", r)
	}()
	renderInteractiveIncidentPage(analysis, incidentInteraction{credential: "x", enabled: false})
}

// The listener markers live in a const template, so the mutation is applied to
// a copy of incident_template.go through a go build overlay and the real
// renderer is run against it.
func TestInteractiveIncidentListenerDrift(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	template, err := filepath.Abs("incident_template.go")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(template)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, from, to string }{
		{"listener-start-drift", `const rerun=document.getElementById("rerun");`, `const rerun2=document.getElementById("rerun");`},
		{"listener-end-drift", `rerun.disabled=false}});`, `rerun.disabled=true}});`},
	} {
		t.Run(c.id, func(t *testing.T) {
			if strings.Count(string(src), c.from) != 1 {
				t.Fatalf("template no longer carries %q exactly once", c.from)
			}
			dir := t.TempDir()
			mutated := filepath.Join(dir, "incident_template.go")
			if err := os.WriteFile(mutated, []byte(strings.Replace(string(src), c.from, c.to, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {template: mutated}})
			overlayPath := filepath.Join(dir, "overlay.json")
			if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(gobin, "test", "-overlay="+overlayPath, "-count=1", "-v", "-run", "^TestInteractiveIncidentListenerDriftChild$", ".")
			cmd.Env = append(os.Environ(), incidentDriftChildEnv+"=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child failed: %v\n%s", err, out)
			}
			want := incidentDiagnostic(t, c.id)
			found := false
			for _, line := range strings.Split(string(out), "\n") {
				if _, after, ok := strings.Cut(line, "DRIFT-DIAGNOSTIC:"); ok {
					found = true
					if after != want {
						t.Errorf("got %q want %q", after, want)
					}
				}
			}
			if !found {
				t.Fatalf("no diagnostic in child output:\n%s", out)
			}
		})
	}
}
