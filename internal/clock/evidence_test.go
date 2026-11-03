package clock

import (
	"testing"
	"time"
)

func TestEvidenceMillisecondsFloor(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    int64
	}{
		{0, 0},
		{999 * time.Microsecond, 0},
		{time.Millisecond, 1},
		{1999 * time.Microsecond, 1},
		{2 * time.Millisecond, 2},
	} {
		if got := evidenceMillis(tc.elapsed); got != tc.want {
			t.Errorf("elapsed=%s: milliseconds=%d want%d", tc.elapsed, got, tc.want)
		}
	}
}

// Using the seeded wall-clock API for evidence would put started outside this
// live interval in faultline_test builds. Bounds follow actual elapsed time;
// there is no sleep or scheduling-dependent upper time limit.
func TestEvidenceTimerIndependentOfWallReads(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_SEED", "native-evidence")
	_ = Now()
	before := time.Now()
	timer := StartEvidenceTimer()
	after := time.Now()
	if timer.started.Before(before) || timer.started.After(after) {
		t.Fatal("evidence timer did not start inside the live monotonic interval")
	}
	_ = Now()
	_ = NowMillis()
	lower := int64(time.Since(after) / time.Millisecond)
	got := timer.Milliseconds()
	upper := int64(time.Since(before) / time.Millisecond)
	if got < 0 || got < lower || got > upper {
		t.Fatalf("evidence duration=%d outside observed elapsed bounds [%d,%d]", got, lower, upper)
	}
}
