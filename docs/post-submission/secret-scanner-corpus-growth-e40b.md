# Post-submission queue — Grow secret-scanner corpus past 151 cases

**Status:** parked until Devpost is submitted and `SUBMISSION_FROZEN` is lifted.

**Rule:** touches `src/**` (and possibly tests). Must not merge to main while freeze is active.

## Intent
Grow secret-scanner corpus past 151 cases

## Gate
- Oracle equivalence (`tests/turn-snapshot-oracle.test.ts`) for any staging/perf change
- No pin/tag moves
- No evidence-grade / TURN_PROOF promotion machinery
- No plugin live-fire claims

## Next engineering steps
See parent note in PR / issue body when work begins.
