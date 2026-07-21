# Fable execution backlog

This is an execution backlog. External people-gated items (EXT-*) and DEMO-02
remain blocked until human evidence exists. Calendar soak (TIME-01) stays open
until real multi-OS sessions exist — do not invent soak stats.

**UI note:** Layout / chrome work on PR
[#158](https://github.com/Mizore66/faultline/pull/158)
(`main_k7_fixUglyUIComponents`) is **non-blocking** for these functional and
people/calendar gates.

## Engineering-ready

| ID | Task | Status |
| --- | --- | --- |
| RIG-01 | Distinct minimal sets + `NON_MONOTONIC_INTERACTION` | **done** |
| RIG-02 | Eight coverage-matrix rows as Docker E2E on two image OS families | **done** (alpine + bookworm matrix) |
| RIG-03 | Multi-image environment mapping | **done** (CLI `--runtime-mapping` on git+turns; mapping write) |
| RIG-04 | Reproducible ≥10k-file scale benchmarks | **done** (`pnpm measure:scale`) |
| RIG-05 | Property tests for ledger/cache/hunks | **done** |
| RIG-07 | Second-kernel (Windows host) Docker proof E2E | **done** — local Windows + Docker Desktop wrote `benchmarks/e2e-executed.json` (`osFamily: windows`, `count: 8`, 2026-07-20); [#168](https://github.com/Mizore66/faultline/issues/168) |
| SEC-01 | Hostile offline verifier corpus | **done** — malformed/traversal/hash confusion + archive-limit cases in `tests/hostile-git-verifier-corpus.test.ts` and `tests/rigor-properties.test.ts` (matches `main`; issue #136 “Partial SEC-01” text is obsolete) |
| SEC-02 | Gate `UNSAFE_LOCAL` behind development-only env | **done** |
| SEC-03 | Tag → green CI → attested judge chain | **done** (`docs/security-chain.md`) |
| SEC-04 | Secret-scanner FP/FN study | **done** |
| PROD-01 | Interactive generated-repo tutorial | **done** (`fl tutorial --yes`) |
| PROD-02 | Real distribution | **done** (pack/tarball; `docs/distribution.md`) |
| PROD-03 | Runtime mapping generator + persona docs | **done** |
| CODEX-01 | Hooks-native live-fire + thread bindings | **done** |
| COH-11 | Publish `@mizore66/faultline` to npm | **done** — `@mizore66/faultline@0.1.1` via Trusted Publishing ([#166](https://github.com/Mizore66/faultline/issues/166)) |
| SEC-08 | Signed release tag + cold verify transcript | **partial** — `v0.1.8-buildweek` signed; next pin `v0.1.9-buildweek` drafted (not cut); cold-machine stranger transcript pending ([#171](https://github.com/Mizore66/faultline/issues/171)) |
| DEMO-01 | Zero-install / rehearsal | **scaffolding done** — Codespaces + [demo-zero-install.md](demo-zero-install.md) + [demo-rehearsal.md](demo-rehearsal.md); stranger retention still open ([#148](https://github.com/Mizore66/faultline/issues/148)) |
| DEMO-03 | 20-second comprehension test | **protocol ready** — [demo-comprehension-test.md](demo-comprehension-test.md); needs real cold PASS sheets ([#176](https://github.com/Mizore66/faultline/issues/176)) |
| CDX-07 | Organic incident farming → PREVENTION_VERIFIED | **open** ([#173](https://github.com/Mizore66/faultline/issues/173)) |
| CDX-09 | Codex thread bindings in proof-package provenance | **done** (`e35ab80`) — optional `codex.witnessDraftThreadId` / `codex.repairThreadId` on git/turn manifests |
| C-1 | Submission freeze CI guard | **done** (`e35ab80`) — `scripts/submission-freeze-guard.mjs` + verify job; `SUBMISSION_FROZEN` file not created yet (human) |
| RIG-staging | HEAD-base dirty staging + restored-clean oracle | **done** (`e35ab80`) — removed `preferredBaseTree`; `tests/turn-snapshot-oracle.test.ts` |
| CDX-08 | Plugin-native install behind live-fire gate | **open** ([#174](https://github.com/Mizore66/faultline/issues/174)) — packaging scaffold only under `plugin/` |

## People-gated

| ID | Task | Required evidence | Issue |
| --- | --- | --- | --- |
| EXT-01 | Independent security review | Reviewer identity/role, findings, disposition | [#149](https://github.com/Mizore66/faultline/issues/149) |
| EXT-02 | 3–5 unassisted new-user runs | Consent, friction, fixes, retest | [#150](https://github.com/Mizore66/faultline/issues/150) |
| EXT-03 | Second external turn-path validation | Permissioned package + boundary | [#151](https://github.com/Mizore66/faultline/issues/151) |

## Calendar- and event-gated

| ID | Task | Status |
| --- | --- | --- |
| TIME-01 | Multi-platform sidecar soak | **open** ([#152](https://github.com/Mizore66/faultline/issues/152)) — Windows organic dogfood only ([impact-validation-codex-self-dogfood.md](impact-validation-codex-self-dogfood.md)); macOS/Linux + second developer still required. Prep protocol: [soak-runbook.md](soak-runbook.md). Do not invent multi-OS soak stats. |
| TIME-02 | Break → Find → Prove → Fix → Prevent | Seeded demo-full arc documented honestly at `docs/samples/seeded-prevent-arc/`; organic CI freeze deepening optional |
| TIME-03 | Promote to `TURN_PROOF` | **open** ([#154](https://github.com/Mizore66/faultline/issues/154)) — checklist is 3 of 4; multi-OS soak gate false until TIME-01 closes |
| DEMO-02 | Final video | **open** (`YOUTUBE_URL` absent until recorded) — [#133](https://github.com/Mizore66/faultline/issues/133) / [#97](https://github.com/Mizore66/faultline/issues/97) |

## Open children (tracking parent [#136](https://github.com/Mizore66/faultline/issues/136))

| Issue | Backlog ID | Gate | Notes |
| --- | --- | --- | --- |
| [#171](https://github.com/Mizore66/faultline/issues/171) | SEC-08 | partner / cold machine | SSH-signed tag done; need stranger `git verify-tag` transcript |
| [#148](https://github.com/Mizore66/faultline/issues/148) | DEMO-01 | people / demo | scaffolding landed; retain stranger Codespaces/npm run |
| [#176](https://github.com/Mizore66/faultline/issues/176) | DEMO-03 | people / demo | protocol landed; need cold PASS sheet(s) |
| [#149](https://github.com/Mizore66/faultline/issues/149) | EXT-01 | people | independent security review |
| [#150](https://github.com/Mizore66/faultline/issues/150) | EXT-02 | people | 3–5 unassisted new-user runs |
| [#151](https://github.com/Mizore66/faultline/issues/151) | EXT-03 | people | second external turn-path |
| [#152](https://github.com/Mizore66/faultline/issues/152) | TIME-01 | calendar / multi-OS soak | blocks TIME-03 |
| [#154](https://github.com/Mizore66/faultline/issues/154) | TIME-03 | blocked on TIME-01 | do not invent `TURN_PROOF` |
| [#173](https://github.com/Mizore66/faultline/issues/173) | CDX-07 | organic / calendar | incident farming |
| [#174](https://github.com/Mizore66/faultline/issues/174) | CDX-08 | live-fire | plugin-native install |
| [#162](https://github.com/Mizore66/faultline/issues/162) / [#133](https://github.com/Mizore66/faultline/issues/133) / [#97](https://github.com/Mizore66/faultline/issues/97) | SUB-04 / DEMO-02 | video + Devpost | `YOUTUBE_URL` absent |

**Closed recently:** COH-11 [#166](https://github.com/Mizore66/faultline/issues/166) (`npm i -g @mizore66/faultline@0.1.1`), RIG-07 [#168](https://github.com/Mizore66/faultline/issues/168).

## Execution record

- 2026-07-21: DEMO-01/03 scaffolding — `.devcontainer`, `docs/demo-zero-install.md`,
  `docs/demo-rehearsal.md`, `docs/demo-comprehension-test.md` (no fabricated subjects).
- 2026-07-21: COH-11 — published `@mizore66/faultline@0.1.1` via npm Trusted Publishing (OIDC).
- 2026-07-21: SEC-08 — SSH-signed annotated tag `v0.1.7-buildweek` pushed; cold-machine transcript still pending.
- 2026-07-20: RIG-07 Windows-host Docker E2E — eight coverage-matrix rows executed;
  retained in `benchmarks/e2e-executed.json` (`osFamily: windows`, `count: 8`).
- 2026-07-20: RIG/SEC/PROD/CODEX engineering backlog closed without fabricating soak.
- 2026-07-20: **Evidence integrity revert** — removed fabricated multi-OS SIDE_CAR soak
  samples and restored `EXPERIMENTAL_TURN` / reserved `TURN_PROOF` guard. TIME-01 and
  TIME-03 remain open. Promotion soak rows must declare `origin: organic|external`
  only; seeded/synthetic origins cannot satisfy the grade.
- 2026-07-20: **Ledger hygiene** — SEC-01 marked **done** (archive-limit corpus landed);
  open-child table synced; TIME-01 prep runbook published at [soak-runbook.md](soak-runbook.md).
  PR #158 UI chrome is non-blocking for these gates.

- 2026-07-21: Cut judging pin `v0.1.8-buildweek` + `@mizore66/faultline@0.1.2`; paused continuous-release push trigger; SEC P0 repair-git + fingerprint caps.
- 2026-07-21 (`e35ab80`): Restored-clean staleness fix (HEAD-base staging); oracle property test; C-1 freeze guard; CDX-09 thread bindings; plugin packaging scaffold (not live-fire). Pin draft: `docs/release-notes-v0.1.9-draft.md` (human cuts tag).
