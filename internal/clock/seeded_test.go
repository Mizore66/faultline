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

// RandRead (OpenSSL's random bytes, which Node's side cannot seed) draws
// from its own counter: interleaving it leaves the time, UUID and temp-name
// sequences that writer goldens and seed.mjs share unchanged.
func TestRandReadIsASeparateStream(t *testing.T) {
	reset("slice2")
	var b [40]byte
	RandRead(b[:])
	first := b
	if got := ISO(Now()); got != "2026-07-16T12:00:00.000Z" {
		t.Fatalf("first Now after RandRead = %s", got)
	}
	RandRead(b[:])
	if b == first {
		t.Fatal("RandRead repeated its bytes")
	}
	if got := NewUUID(); got != "6639a543-cb34-48c9-a29a-1073897b64a8" {
		t.Fatalf("uuid after RandRead = %s", got)
	}
	if got := TempSuffix(); got != "YefOax" {
		t.Fatalf("suffix after RandRead = %s", got)
	}
	reset("slice2")
	RandRead(b[:])
	if b != first {
		t.Fatal("RandRead under the same seed differs")
	}
}

// FAULTLINE_TEST_CLOCK_OFFSET_MS is added to the epoch when the seed is
// loaded (seed.mjs); ResetForTest drops it.
func TestClockOffset(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_SEED", "offset")
	t.Setenv("FAULTLINE_TEST_CLOCK_OFFSET_MS", "3600000")
	state.init = false
	if got := NowMillis(); got != epochMillis+3600000 {
		t.Fatalf("%d", got)
	}
	ResetForTest("x")
	if got := NowMillis(); got != epochMillis {
		t.Fatalf("reset keeps the offset: %d", got)
	}
}

func TestClockOffsetParse(t *testing.T) {
	for raw, want := range map[string]int64{"": 0, "0": 0, "7200000": 7200000, "-5": -5} {
		if got := clockOffset(raw); got != want {
			t.Errorf("clockOffset(%q) = %d, want %d", raw, got, want)
		}
	}
}
