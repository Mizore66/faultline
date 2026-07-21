# Bucket 2 — post-submission product queue (judge-facing)

**Status:** staged for review · **do not merge while `SUBMISSION_FROZEN` is active**  
**Judging pin (untouchable):** `v0.1.9-buildweek` → `3ca1bb8adaebf163fe633642a0a4ba20dfb7810a`  
**Why this PR exists:** park the next honest engineering work *after* Devpost submit so judges and humans can see the roadmap without polluting the frozen submission surface.

## What this is

A single labeled queue of **post-submission** product work. Each item is intentionally **not** on the judging pin and **must not** land on `main` until freeze lifts.

This is not evidence, not a claim of completion, and not part of the Build Week submission artifact set.

## Queue (implementation order)

| # | Item | Spec | Touches |
|---|------|------|---------|
| 1 | Batch turn-snapshot staging (`hash-object` / `update-index`) | [perf-batch-staging-e40b.md](perf-batch-staging-e40b.md) | `src/**`, oracle tests |
| 2 | Broaden CDX-09 Codex thread ID provenance | [cdx09-thread-bindings-broad-e40b.md](cdx09-thread-bindings-broad-e40b.md) | `src/**`, tamper tests |
| 3 | Hostile-verifier turn-proof corpus parity | [hostile-verifier-turn-parity-e40b.md](hostile-verifier-turn-parity-e40b.md) | tests / verifier corpus |
| 4 | Grow secret-scanner corpus past 151 cases | [secret-scanner-corpus-growth-e40b.md](secret-scanner-corpus-growth-e40b.md) | scanner corpus / tests |

## Hard gates (every item)

- Oracle equivalence for any staging/perf change (`tests/turn-snapshot-oracle.test.ts`)
- No pin/tag moves (`v0.1.9-buildweek` frozen)
- No fabricated evidence / no `TURN_PROOF` promotion machinery
- No plugin live-fire claims without retained artifacts
- No merge to `main` while `SUBMISSION_FROZEN` exists

## Relationship to Bucket 1 / submission

| Surface | Role |
|---------|------|
| Pin `v0.1.9-buildweek` | What judges run |
| Bucket 1 on `main` | Submission freeze + de-risk tooling (docs/scripts/CI only) |
| **This PR (Bucket 2)** | Post-submit product backlog — reviewable now, merge later |

## Prior split PRs (superseded)

This umbrella replaces the parked drafts:

- #189 perf batch staging
- #190 secret-scanner corpus growth
- #191 hostile-verifier turn parity
- #192 CDX-09 thread bindings
