# Release notes — v0.1.10-buildweek

> **Judging pin.** Annotated tag. Do **not** move or delete `v0.1.9-buildweek` (historical previous pin; retain for audit trail).

## This is the only release intended for judging

**Current tip (2026-07-21 retarget):** `89b8e2348037ece69faf47b6e230a78c3bab6f44` (syncs pin to `main` including narration cleanup #200 and package `@mizore66/faultline@0.1.4`). Prior tip `ce99a793af0b020bf1b9cf21114f8d7897b37207` remains in history.

Originally re-cut so judges receive the **EXT-01** security fixes. Independent external review (Chang Kai Zhe) found a Critical in an off-path Git capture entry point that missed the hardening sweep; fixes shipped same-day with regression tests and an honest disposition ([docs/ext-01-security-review.md](ext-01-security-review.md)).

### Why this pin exists

`v0.1.9-buildweek` remains an intact signed historical release, but it **does not** contain:

| ID | Severity | Disposition on this pin |
| --- | --- | --- |
| FL-SEC-001 | Critical | **Fixed** — hardened Git for `fl record` checkpoint/attach/stdin capture (`14ad89d`); regression `tests/fl-record-checkpoint-fsmonitor.test.ts` |
| FL-SEC-002 | High | **Mitigated** — proof-bearing PASS/FAIL require exit-code consistency; spoof → `HARNESS_ERROR`; residual: no nonce-authenticated result channel yet |
| FL-SEC-003 | Medium | **Fixed** — root `action.yml` SHA-pinned; `tests/workflow-pins.test.ts` |

Judge-flow blast radius on `v0.1.9` for the documented `doctor --proof-ready` + `judge-proof` path was effectively nil (vulnerable function is off that path). Re-cutting anyway so a security-minded judge cannot construct: “signed judging release still ships a known Critical after you invited the audit.”

### Also included (carried forward)

- Restored-clean / HEAD-base staging + oracle property test from `v0.1.9-buildweek`
- Sol P0 hardening, CDX-09 optional thread IDs, SEC-08 cold-reader chain docs
- `SUBMISSION_FROZEN` (re-asserted after this pin cut)
- Bucket 1 submission de-risk: teleprompter rehearsal harness, fallback footage, Devpost gallery/claims linter, CI evidence retention

### Historical / not for judging

| Tag | Status |
| --- | --- |
| `v0.1.9-buildweek` | Historical previous pin (retain; do not move) — **lacks EXT-01 fixes** |
| `v0.1.8-buildweek` | Historical |
| `v0.1.7-buildweek` and earlier buildweek tags | Historical |
| `v0.1.4` | **RETRACTED** — do not cite |

### Still human-gated (do not claim)

- Cold stranger Codespaces / comprehension scoresheet
- Demo video / `YOUTUBE_URL` (record from **this** pin via [video-teleprompter.md](video-teleprompter.md); rehearse with `node scripts/rehearse-teleprompter.mjs`)
- TIME-01 / TIME-03 soak and `TURN_PROOF` promotion
- Plugin live-fire (CDX-08)
- Production-untrusted-repo posture (FL-SEC-002 residual)

### Human cut checklist

```bash
# After pin surfaces land on main (this file + retargeted refs):
git checkout main && git pull
PIN_COMMIT=$(git rev-parse HEAD)   # must be the pin-surfaces commit
git tag -s v0.1.10-buildweek -m "$(cat <<'EOF'
This is the only release intended for judging.

FaultLine v0.1.10-buildweek incorporates EXT-01 (FL-SEC-001/002/003) so the
signed judging artifact includes the Critical/High/Medium dispositions.
v0.1.9-buildweek remains historical and must not move.
EOF
)" "$PIN_COMMIT"
git push origin v0.1.10-buildweek
# Then fill Devpost literal SHA to $PIN_COMMIT and re-assert SUBMISSION_FROZEN.
```
