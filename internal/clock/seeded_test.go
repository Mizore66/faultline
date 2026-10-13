//go:build faultline_test

package clock

import "testing"

func TestSeededSequence(t *testing.T) {
	reset("slice2")
	if got := ISO(Now()); got != "2026-07-16T12:00:00.000Z" {
		t.Fatalf("first Now = %s", got)
	}
	if got := NowMillis(); got != 1784203200001 {
		t.Fatalf("second read = %d (Now and NowMillis share one counter)", got)
	}
	for i, want := range []string{"6639a543-cb34-48c9-a29a-1073897b64a8", "96c7242c-e44d-4ac1-9d9e-828863fec810"} {
		if got := NewUUID(); got != want {
			t.Fatalf("uuid %d = %s, want %s", i, got, want)
		}
	}
	for i, want := range []string{"YefOax", "I8Q8zW"} {
		if got := TempSuffix(); got != want {
			t.Fatalf("suffix %d = %s, want %s", i, got, want)
		}
	}
}
