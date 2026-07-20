# Impact validation — FaultLine Codex self-dogfood (sidecar + guided CI)

> Maintainer dogfood on `Mizore66/faultline`. **Organic development use:** Codex
> + FaultLine were used to do real work (including triaging a real CI failure),
> not a planted demo script. Not third-party adoption. Do not invent multi-OS
> soak stats or time-saved metrics from this file.

## Record metadata

- Record ID: `codex-self-dogfood-2026-07-20`
- Status: **completed** (organic Windows dogfood + guided CI freeze evidence)
- Date and timezone: 2026-07-19/20 (SGT / UTC as recorded on artifacts)
- Owner: FaultLine maintainers (Anas Qumhiyeh — human approve/freeze actor)
- Evidence retention location:
  - Private ledgers: `.git/faultline/recordings/` (local Git metadata; not in git tree)
  - Public excerpt: `docs/samples/faultline-self-sidecar-soak/`
  - Frozen witnesses: `.faultline/witnesses/frozen/` (local `.faultline/` gitignored)
- Consent / sharing scope: public repo dogfood; private full ledgers stay local
- Redaction review completed by: maintainer (paths redacted in public excerpt)

## Audience and job to be done

- Participant role: maintainer genuinely developing FaultLine with Codex
- Scenario: organic agent-assisted development + real CI failure triage via
  `fl investigate --ci-log`
- Triggering question: “Record this Codex session, and use FaultLine to help
  diagnose / unblock a CI failure I actually hit.”
- Current workflow: Codex with trusted `.codex/hooks.json` + `node dist/cli.js`
- What decision needs confidence: whether Codex-native runtime evidence and
  guided CI freeze work under real development pressure
- Why existing evidence was insufficient: earlier attempts had hooks installed
  but `NO_SIDECAR_RECORDINGS` until hooks were trusted and sessions completed

## A — Sidecar soak slice (SIDE_CAR)

### Incident replay / observation record

- Repository: `Mizore66/faultline` (this checkout)
- Transport: **`SIDE_CAR`** (Codex hooks → `fl codex sidecar hook`)
- Organic use: sessions rode along while shipping real product/CI fixes (not a
  synthetic “record one turn for the sample” exercise)
- Primary session: `019f7c98-39cd-72e1-9eb3-2a80f10fe1b8`
- Private ledger basename: `codex-60908cfa094e765871fc47e275078a07c207fa7b9ce4be0569481029ebcda511.json`
- Event count: **96** (valid hash chain)
- Head hash: `sha256:ef0c9a2f034f9388d84766237542cff5a16a422e4f04be912517c9d94e99d586`
- Window: ~74 minutes (`2026-07-20T06:27:48Z` → `2026-07-20T07:41:37Z`)
- Composition: 1 session start, 1 baseline snapshot, 19 turn starts, 18 turn
  completes, 18 turn-tree snapshots, 20/19 tool-use start/complete pairs
- Clean worktree checkpoints: **0** — latest Stop status
  `CHECKPOINT_SKIPPED_DIRTY` / `DIRTY_WORKTREE` (turn trees still recorded)
- Local verification: `fl record verify` → **valid: true**
- Public excerpt verification:
  `fl record verify --ledger docs/samples/faultline-self-sidecar-soak/ledger.json` → **valid**
- Secondary short session: `019f7cb6-70f5-79f2-8295-cd53cfcf5021` (5 events,
  `2026-07-19T23:29:21Z`–`23:29:26Z`, also `SIDE_CAR`, dirty Stop)

### What this showed

- Trusted Codex hooks fire on Windows against this repo’s built `dist/cli.js`
- Digests-only tool/turn attribution accumulates without storing prompt text
- Dirty-worktree policy is observable and fail-honest (`CHECKPOINT_SKIPPED_DIRTY`)
- Sidecar recording works during genuine multi-turn development, not only fixtures

### What this did not show

- Clean Git checkpoints on Stop (worktree remained dirty during development)
- Plugin-native install path — CODEX-01 still open
- Full TIME-02 arc ending in `PREVENTION_VERIFIED` on a portable prevention
  package (this record stops at organic sidecar use + guided CI freeze)

## B — Guided CI investigate (`fl investigate --ci-log`)

### Incident replay record

- **Organic CI failure:** https://github.com/Mizore66/faultline/actions/runs/29694948356
  (failed Verify on a real push; not a planted failing fixture for the camera)
- Local log artifact: `ci-failure-29694948356.log` (workspace; not committed)
- Guided command pattern: `fl investigate --ci-log .\ci-failure-29694948356.log --repo . --unsafe-local`
- Human review: local witness-review UI → Approve → Freeze
- Frozen witness (preferred): `ci-29694948356-pnpm-test`
  - Frozen digest: `sha256:89313b840699ff393ef7b2fff726c68fe4f4d723e6c167463b887ceda2216b12`
  - Approved by: `Anas Qumhiyeh` at `2026-07-19T18:08:37.753Z`
  - Witness command: `pnpm test`
  - Explicit range: `084a2aa8…` → `90c66178…`
  - `fl witness verify ci-29694948356-pnpm-test` → **valid: true**
- Second freeze also present: `ci-29694948356` →
  `sha256:9b3f21f79a769706101b1193bf0787fb93865b028edf7b36250fd4c7e8fd1d1e`
- Subsequent `main` history includes CI-oriented fixes (e.g. sidecar snapshot
  cache location, package-smoke JSON parse, Docker/runtime tightening) culminating
  in green Verify runs after the failure window

### What this showed

- Guided investigate correctly paused for human freeze (not silent auto-proof)
- A real CI log became a frozen, verifyable witness contract
- Codex continued engineering against that contract afterward

### What this did not show

- A portable `COMMIT_PROOF` package from this guided run (no retained proof
  bundle root claimed here)
- That `--unsafe-local` is proof-grade (it is explicitly non-proof)
- That freeze alone “fixed CI” — green CI came from code changes + re-runs

## Measured outcomes

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Sidecar recordings present | `NO_SIDECAR_RECORDINGS` earlier | `SIDECAR_RECORDINGS_READY`, 2 sessions | `fl codex sidecar status` | status JSON 2026-07-20 | Windows only (see Future work) |
| Primary session events | n/a | 96 valid events / ~74 min | ledger parse + `fl record verify` | private ledger + sample-meta | Dirty checkpoints only |
| Frozen CI witness | n/a | valid freeze for `pnpm test` | `fl witness verify` | `.faultline/witnesses/frozen/ci-29694948356-pnpm-test.json` | Local gitignored store |
| Time saved / adoption | not measured | not measured | — | — | Do not invent |

## Future work (explicit)

### Multi-OS / multi-developer soak (TIME-01 remainder)

This record is a **Windows + single-developer** organic soak slice. Still required
before claiming calendar TIME-01 complete:

- Repeat trusted-hook sessions on **macOS** and **Linux**
- At least one **second developer** machine/session
- Publish a stability table (sessions, turns, snapshots, torn-capture count,
  p95 Stop/snapshot latency **per OS**) only after those collections exist

Tracked: GitHub issue **#152**.

### Optional TIME-02 deepening

Organic development use and organic CI triage are recorded here. A stronger
TIME-02 package would additionally retain Break → Find → Prove → Fix → Prevent
with `PREVENTION_VERIFIED` artifacts. Tracked: **#153**.

## Decision and follow-up

- Supports: Codex-native recorder works under real development; guided CI freeze
  path is usable on genuine CI failures; public excerpt can back honesty-preserving demos
- Weakens: dirty Stop dominance; Windows-only soak; no portable proof root from this freeze
- Product change suggested: make review UI less digest-heavy; prompt clean commits
  before Stop when checkpoints matter
- Not justified: claiming multi-OS TIME-01 complete from this file alone
- Follow-up: multi-OS soak (#152); optional PREVENTION_VERIFIED deepening (#153)
- Publishable summary approved? **yes** (this file + public sample)

## Integrity checklist

- [x] Real maintainer Codex sessions and real CI freeze artifacts (organic use)
- [x] Metrics have method and source (or “not measured”)
- [x] No private prompt/transcript text published
- [x] Paths redacted in public excerpt
- [x] Distinguishes Windows organic soak vs multi-OS TIME-01 completion; freeze vs proof package
