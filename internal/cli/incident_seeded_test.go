//go:build faultline_test

package cli

import (
	"regexp"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func TestGeneratedIncidentIDShape(t *testing.T) {
	clock.ResetForTest("incident-id")
	id := generatedIncidentID()
	if !regexp.MustCompile(`^incident-\d{17}-[0-9a-f-]{12}$`).MatchString(id) {
		t.Fatal(id)
	}
	// The seeded clock starts at 2026-07-16T12:00:00.000Z: its first 17 digits.
	if id[:len("incident-20260716120000000-")] != "incident-20260716120000000-" {
		t.Fatal(id)
	}
}
