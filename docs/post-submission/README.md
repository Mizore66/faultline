# Bucket 2 - post-submission product queue

**Status:** documentation-only roadmap | **do not merge while `SUBMISSION_FROZEN` is active**  
**Build Week judging pin (untouchable):** `v0.1.10-buildweek` -> `ce99a793af0b020bf1b9cf21114f8d7897b37207`  
**Historical previous pin (retain; do not move):** `v0.1.9-buildweek` -> `3ca1bb8adaebf163fe633642a0a4ba20dfb7810a`  
**Why this PR exists:** park the next honest engineering work *after* Devpost submit so the backlog is reviewable without polluting the frozen submission surface.

## What this is

A single labeled queue of **post-submission** product work. Each item is intentionally **not** on the judging pin and **must not** land on `main` until freeze lifts.

This is not evidence, not a claim of completion, and not part of the Build Week submission artifact set. Do not mention this PR in the demo video.

## Queue (implementation order)

| # | Item | Spec | Touches |
|---|------|------|---------|
| 1 | Batch turn-snapshot staging (`hash-object` / `update-index`) | [perf-batch-staging-e40b.md](perf-batch-staging-e40b.md) | `src/**`, oracle tests |
| 2 | Broaden CDX-09 Codex thread ID provenance | [cdx09-thread-bindings-broad-e40b.md](cdx09-thread-bindings-broad-e40b.md) | `src/**`, tamper tests |
| 3 | Hostile-verifier turn-proof corpus parity | [hostile-verifier-turn-parity-e40b.md](hostile-verifier-turn-parity-e40b.md) | tests / verifier corpus |
| 4 | Grow secret-scanner corpus past 151 cases | [secret-scanner-corpus-growth-e40b.md](secret-scanner-corpus-growth-e40b.md) | scanner corpus / tests |

After freeze lifts: **do not merge this umbrella wholesale**. Rebase this roadmap onto final `main` if needed, then split each item into its own source PR with separate tests and acceptance evidence. Close this umbrella once those implementation PRs exist.

## Hard gates (every item)

- Oracle equivalence for any staging/perf change (`tests/turn-snapshot-oracle.test.ts`)
- No pin/tag moves (`v0.1.10-buildweek` and historical `v0.1.9-buildweek` frozen)
- No fabricated evidence / no `TURN_PROOF` promotion machinery
- No plugin live-fire claims without retained artifacts
- No merge to `main` while `SUBMISSION_FROZEN` exists
- Specs must stay plain UTF-8 (no bidi override/isolate controls); CI rejects those controls in source and specification files

## Relationship to Bucket 1 / submission

| Surface | Role |
|---------|------|
| Pin `v0.1.10-buildweek` | What judges run (Build Week judging pin; includes EXT-01) |
| Pin `v0.1.9-buildweek` | Historical previous pin (lacks EXT-01; retain; do not move) |
| Bucket 1 on `main` | Submission freeze + de-risk tooling (docs/scripts/CI only) |
| **This PR (Bucket 2)** | Post-submit product backlog - reviewable now, merge later via split PRs |

## Prior split PRs (superseded drafts)

This umbrella replaces the parked drafts (close as duplicates; keep this PR Draft):

- #189 perf batch staging
- #190 secret-scanner corpus growth
- #191 hostile-verifier turn parity
- #192 CDX-09 thread bindings
