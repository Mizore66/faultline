# Multi-platform Codex sidecar soak runbook (TIME-01 / #152)

> **Prep only.** This document defines how to collect organic evidence.
> Do **not** invent soak stats, mint `SIDE_CAR` ledgers from non-Codex sources,
> or flip `multiOsSoak.satisfied` until the criteria below are met with real
> sessions.

**Related:** [#152](https://github.com/Mizore66/faultline/issues/152) ·
[fable-execution-backlog.md](fable-execution-backlog.md) ·
Windows organic slice already published at
[samples/faultline-self-sidecar-soak/](samples/faultline-self-sidecar-soak/) ·
impact narrative [impact-validation-codex-self-dogfood.md](impact-validation-codex-self-dogfood.md).

## Goal

Close TIME-01 with **calendar-time** Codex `SIDE_CAR` soak across:

| Platform | Minimum |
| --- | --- |
| Windows | Organic maintainer dogfood (already landed) |
| macOS | ≥1 trusted-hook session by a **second developer** (or distinct human) |
| Linux | ≥1 trusted-hook session by a **second developer** (or distinct human) |

“Second developer” means a human other than the primary Windows dogfood author,
running real Codex work (not a scripted one-turn demo).

## Protocol for a second developer (macOS / Linux)

### 0. Preconditions

1. Clone `Mizore66/faultline` at the current submission pin (or newer `main`).
2. `pnpm install --frozen-lockfile` and `pnpm build`.
3. Install / trust Codex hooks so sidecar recording is live:
   - `pnpm fl codex sidecar config` (or project `fl init --yes` path)
   - Confirm Codex trusts `.codex/hooks.json` (untrusted hooks → no ledger).
4. Confirm doctor: `pnpm fl doctor` (sidecar path healthy). Proof-grade Docker
   is **not** required for soak capture itself.

### 1. Record a real session

1. Do genuine development or triage work in Codex against this repo (or a
   consented partner repo) for a meaningful window (prefer ≥30 minutes or
   multi-turn work — not a single planted turn).
2. Let Stops fire naturally. Dirty worktrees are OK and must be reported
   honestly (`CHECKPOINT_SKIPPED_DIRTY` is evidence, not failure).
3. Locate the private ledger under `.git/faultline/recordings/` (Git metadata;
   not committed by default).

### 2. Verify locally (required)

```bash
pnpm fl record verify --ledger <path-to-private-ledger.json>
```

Expect `valid: true` (hash chain). Keep the private ledger offline unless
redaction is reviewed.

### 3. Publish a redacted public slice (required for citation)

Mirror the Windows sample layout under a new directory, e.g.:

`docs/samples/faultline-sidecar-soak-<os>-<short-id>/`

| File | Role |
| --- | --- |
| `README.md` | Platform, developer role, claim boundary, verify command |
| `ledger.json` | Redacted hash-chained excerpt (no local paths, no prompt text) |
| `sample-meta.json` | Private stats + digests + `origin` + platform |

Redact absolute paths to a stable token (e.g. `/redacted/faultline-soak-<os>`).
Do **not** ship full private streams without review.

### 4. Record impact narrative (recommended)

Add or extend an impact validation note linking the public sample, session ids,
verify results, and explicit non-claims (same honesty style as the Windows
dogfood record).

## Stability table (promotion evidence)

When publishing cross-OS progress, use a machine-readable table (JSON) that
`soakRowsSatisfyPromotion` can evaluate. Every row **must** declare origin.

### Required shape

```json
{
  "schemaVersion": "faultline.multi-os-soak-stability.v1",
  "generatedAt": "2026-07-20T00:00:00.000Z",
  "claimBoundary": "Organic/external SIDE_CAR sessions only. Not TURN_PROOF by itself.",
  "rows": [
    {
      "platform": "windows",
      "origin": "organic",
      "developer": "maintainer",
      "sessions": 1,
      "turnsCompleted": 18,
      "snapshots": 18,
      "tornCaptures": 0,
      "ledgerVerify": "valid",
      "artifact": "docs/samples/faultline-self-sidecar-soak/"
    },
    {
      "platform": "macos",
      "origin": "organic",
      "developer": "second-developer",
      "sessions": 1,
      "turnsCompleted": 0,
      "snapshots": 0,
      "tornCaptures": 0,
      "ledgerVerify": "pending",
      "artifact": "docs/samples/faultline-sidecar-soak-macos-…/"
    },
    {
      "platform": "linux",
      "origin": "organic",
      "developer": "second-developer",
      "sessions": 1,
      "turnsCompleted": 0,
      "snapshots": 0,
      "tornCaptures": 0,
      "ledgerVerify": "pending",
      "artifact": "docs/samples/faultline-sidecar-soak-linux-…/"
    }
  ]
}
```

### Origin vocabulary (enforced)

| `origin` | Allowed for TIME-01 / `multiOsSoak`? | Meaning |
| --- | --- | --- |
| `organic` | **Yes** | Real Codex session by a human on that OS |
| `external` | **Yes** | Consented partner Codex session on that OS |
| `seeded_real_ledger` | **No** | Scripted/API-generated ledger — never satisfies soak |
| `synthetic` | **No** | Fixture / invented session — never satisfies soak |

Code gate: `soakRowsSatisfyPromotion()` in `src/turn-proof-promotion.ts`
requires platforms `windows` + `linux` + `macos` and
`origin ∈ {organic, external}` on **every** row.

### Where artifacts live

| Kind | Location |
| --- | --- |
| Private full ledgers | `.git/faultline/recordings/` (local; not in git tree) |
| Public redacted samples | `docs/samples/faultline-sidecar-soak-<os>-*/` |
| Stability table (when real) | e.g. `docs/samples/multi-os-soak-stability.json` (only after real rows exist) |
| Impact narratives | `docs/impact-validation-*.md` |
| Checklist pointer | `src/turn-proof-promotion.ts` → `multiOsSoak.artifact` |

## Criteria to flip `multiOsSoak.satisfied` → `true`

All of the following must be true before editing
`TURN_PROOF_PROMOTION_CHECKLIST.criteria.multiOsSoak`:

1. **Three platforms present** in the stability table: `windows`, `linux`, `macos`.
2. **Every row** has `origin: organic` or `origin: external` (no seeded/synthetic).
3. **Second-developer (or distinct human) evidence** on at least macOS and Linux
   (Windows maintainer dogfood alone is insufficient).
4. Each cited row has a **public redacted sample** (or partner handoff) that
   `fl record verify` accepts, plus a claim-boundary README.
5. Stats (sessions / turns / snapshots / torn captures / latency if published)
   come **only** from those verified sessions — never invented.
6. PR review explicitly confirms no `SIDE_CAR` fabrication and that
   `tests/evidence-honesty.test.ts` still passes.

Then, and only then:

1. Point `multiOsSoak.artifact` at the stability table + samples.
2. Set `multiOsSoak.satisfied: true`.
3. Close #152 with links to artifacts.
4. Re-evaluate TIME-03 / #154 (still needs EXT-03 and remaining promotion policy).

Until that day, keep `satisfied: false` and `artifact` → `#152`.

## Explicit non-goals

- Do not reintroduce `docs/samples/multi-os-sidecar-soak/` scripted ledgers.
- Do not treat CI compressed soak (#124) or hash-chain validity alone as
  multi-OS calendar soak.
- Do not promote `EXPERIMENTAL_TURN` → `TURN_PROOF` from this runbook alone.
