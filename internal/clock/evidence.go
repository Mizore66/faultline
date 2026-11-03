package clock

import "time"

// EvidenceTimer measures unseeded monotonic elapsed time for execution evidence.
// Unlike Monotonic, its floored duration is allowed in captured output/digests.
// Wall timestamps still come from Now and NowMillis.
type EvidenceTimer struct{ started time.Time }

func StartEvidenceTimer() EvidenceTimer { return EvidenceTimer{started: time.Now()} }

func (t EvidenceTimer) Milliseconds() int64 { return evidenceMillis(time.Since(t.started)) }

func evidenceMillis(elapsed time.Duration) int64 { return int64(elapsed / time.Millisecond) }
