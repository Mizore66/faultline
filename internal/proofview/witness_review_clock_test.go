//go:build faultline_test

package proofview

import (
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

// A supplied NowMs must be used without any clock read: a seeded read consumed
// here would shift every later approval and frozen timestamp by one tick.
func TestWitnessReviewSuppliedNowDoesNotAdvanceSeededClock(t *testing.T) {
	clock.ResetForTest("witness-review-now-order")
	first := clock.NowMillis()
	clock.ResetForTest("witness-review-now-order")

	now := float64(1_700_000_000_000)
	for i := 0; i < 3; i++ {
		buildWitnessReviewView(witnessReviewFixture(t), nil, witnessReviewPageState{NowMs: &now})
		renderWitnessReviewPage(witnessReviewFixture(t), nil, "t", false, witnessReviewPageState{NowMs: &now})
	}
	if ticks, _ := clock.ConsumedForTest(); ticks != 0 {
		t.Fatalf("supplied NowMs consumed %d seeded clock ticks, want 0", ticks)
	}
	if got := clock.NowMillis(); got != first {
		t.Fatalf("first seeded read after rendering = %d, want %d (the sequence must not shift)", got, first)
	}

	// Without an override the display age uses the real wall clock (as the
	// frozen TS Date.now() does), never the seeded record-timestamp sequence.
	clock.ResetForTest("witness-review-now-order")
	buildWitnessReviewView(witnessReviewFixture(t), nil, witnessReviewPageState{})
	if ticks, _ := clock.ConsumedForTest(); ticks != 0 {
		t.Fatalf("no override consumed %d seeded ticks, want 0", ticks)
	}
}
