package proofview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestInteractiveIncidentPage(t *testing.T) {
	corpus := incidentTestLoad(t)
	analysis, err := jsjson.Parse(corpus.rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path string
		enabled    bool
	}{
		{"enabled", "interactive-enabled.html", true},
		{"disabled", "interactive-disabled.html", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			expected, err := os.ReadFile(filepath.Join("../../difftest/testdata/incident-renderer", c.path))
			if err != nil {
				t.Fatal(err)
			}
			want := string(expected)
			got := renderInteractiveIncidentPage(analysis, incidentInteraction{credential: "test_nonce-ABC123", enabled: c.enabled})
			if got != want {
				t.Fatalf("interactive page differs: got %d want %d bytes", len(got), len(want))
			}
			hasFetch := strings.Contains(got, "/api/rerun")
			if c.enabled != hasFetch {
				t.Fatalf("enabled=%v but /api/rerun present=%v", c.enabled, hasFetch)
			}
			if !c.enabled && strings.Contains(got, `addEventListener("click",async`) {
				t.Fatal("disabled page keeps the rerun listener")
			}
		})
	}
}

func TestInteractiveIncidentDefaultUnchanged(t *testing.T) {
	corpus := incidentTestLoad(t)
	analysis, err := jsjson.Parse(corpus.rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := RenderIncidentPage(analysis); got != corpus.expected[0] {
		t.Fatal("RenderIncidentPage default bytes changed")
	}
}

func TestInteractiveIncidentCredentialEscaping(t *testing.T) {
	corpus := incidentTestLoad(t)
	analysis, err := jsjson.Parse(corpus.rows[0].AnalysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	got := renderInteractiveIncidentPage(analysis, incidentInteraction{credential: `a"<&'b`, enabled: true})
	if !strings.Contains(got, `<script nonce="`+EscapeHTML(`a"<&'b`)+`">`) {
		t.Fatal("nonce attribute not escaped")
	}
	if !strings.Contains(got, `"X-FaultLine-Native-RERUN":"a\"<&'b"`) {
		t.Fatal("header value not JSON-stringified")
	}
}
