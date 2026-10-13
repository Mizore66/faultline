//go:build !faultline_test

package clock

import (
	"regexp"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestReleaseIgnoresSeed(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_SEED", "slice2")
	a, b := NewUUID(), NewUUID()
	if !uuidV4.MatchString(a) || a == b || a == "6639a543-cb34-48c9-a29a-1073897b64a8" {
		t.Fatalf("release UUIDs %s %s", a, b)
	}
	if ms := NowMillis(); ms < 1784203200000+86400000 {
		t.Fatalf("release clock looks seeded: %d", ms)
	}
}
