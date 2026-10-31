# FaultLine Go migration — overview

- **Date:** 2026-09-25
- **Status:** Approved design (brainstorming session, 2026-09-25)
- **Scope:** Program-level decisions for replacing the TypeScript implementation with Go. Each slice below gets its own spec → plan → implementation cycle.

## Goals

1. **Distribution.** One static binary. No Node or pnpm required by the CLI, the GitHub Action, Codespaces, or the Codex plugin.
2. **Performance.** Faster turn snapshotting (today p50 ≈ 1.5 s, p95 ≈ 1.8 s per `docs/turn-snapshot-overhead.md`), minimization, secret scanning, and bundle hashing.

## Non-goals

- Changing proof semantics, schema versions, or the CLI surface while porting.
- Fixing TypeScript bugs in TypeScript. Bugs are fixed in Go and recorded as known differences.

## Decisions

| Topic | Decision |
| --- | --- |
| End state | Full replacement. Every command is ported; `src/`, `tests/`, and the Node toolchain are deleted in the final slice. |
| Compatibility | Byte-identical artifacts: Go writes the same canonical JSON and digests as TS and verifies every bundle TS has written. CLI stdout, stderr, and exit codes are byte-identical except for entries in `difftest/KNOWN_DIFFERENCES.md`. |
| TypeScript status | Sources frozen at immutable `ce99a79`, the `src/` CI has built and every slice has been compared against. The locally cached `v0.1.10-buildweek` tag names that pin; the live remote tag observed 2026-10-30 peels to `89b8e23` (manifest `0.1.4`, identical `src/`), not the corpus source identity. No features, no fixes. The pin's manifest is `0.1.3`; the existing post-pin release commit `5c683c3` bumped it to `0.1.4`, used by current-tree oracle builds without changing `src/` ([5a spec, Section 3](2026-10-30-go-slice5a-cli-surfaces-design.md#3-version-provenance-and-standalone-behavior)). All new work happens in Go. Until slice 3 the recorded pin was `faa98c0` (`v0.1.9-buildweek`, 2026-09-25), but `14ad89d` and `ce99a79` changed `src/` after it (slice 3 spec, Section 3.1). |
| Strategy | Vertical slices, each proven against frozen TS by a differential harness before the next slice starts. |
| Release timing | No Go release until full parity (end of slice 5). Until then the published npm package remains TypeScript-based; source/manifest provenance is distinguished above. The checkout's manifest value is not evidence of a registry publication, and this migration work does not authorize publishing a new version. |
| Distribution | Static binaries on GitHub Releases, plus the `@mizore66/faultline` npm package rebuilt as a thin wrapper with per-platform optional dependencies (esbuild/biome pattern). `npx fl`, the README, and the Codex plugin keep working. The composite Action downloads the release binary instead of installing Node. |
| Platforms | linux, darwin, windows × amd64, arm64. |
| Toolchain | Go 1.27; standard library only. Go shells out to the `git` CLI exactly as TS does (no go-git), so Git behavior stays identical. |
| Repository layout | Go module at the repo root (`github.com/Mizore66/faultline`), `cmd/fl`, `internal/...`, `difftest/`. TS stays in `src/` until deletion. |
| Deadline | None. |

## Compatibility contract (all slices)

1. **Artifacts.** Every file Go writes into a bundle, ledger, or store is byte-identical to what frozen TS writes for the same inputs.
2. **Reading.** Go accepts every artifact frozen TS accepts and rejects every artifact TS rejects, with the same verdict and exit code.
3. **CLI output.** stdout, stderr, and exit code match byte for byte. The only allowed differences are the harness normalizations defined in the slice 1 spec and entries in `difftest/KNOWN_DIFFERENCES.md`.
4. **Canonical JSON.** One Go implementation of TS `canonicalJson` semantics (key order by en-US `localeCompare`, JS number and string formatting) is used for every digest. `encoding/json` is never used on a digest path. Details: slice 1 spec, Section 2.
5. **Golden corpus is permanent.** Golden outputs captured from frozen TS are committed and remain the regression oracle after TS is deleted.

## Slices

| # | Slice | Contents (exact command list is fixed in each slice's spec) |
| --- | --- | --- |
| 1 | Foundation + harness + verify | Canonical JSON, schema library, safe paths, differential harness, `fl verify` for `faultline.proof-bundle.v2`, `faultline.git-proof-bundle.v1`, and prevention proofs. |
| 2 | Git path | `investigate git`, `minimize git` and `minimize verify`, `judge-proof`, `commit-proof-preview`, the offline `witness` subcommands, `ledger bind` and `ledger verify`, and Git proof bundle writing (byte-identical). Pulled forward from slice 3 because these commands cannot produce a proof without them: the Docker sandbox (`sandbox`), `git-materialization`, `safe-overlay`, `environment-fingerprint`, `redaction`, the read side of `config` and the load side of `runtime-mapping-file`, `incident-binding`, `evidence-grade` and `turn-proof-promotion`. |
| 3 | Runtime (complete: PR 3a runtime, doctor, init; PR 3b incident, prevention) | `runtime`, `doctor`, `init`, `incident`, `prevention` (proof creation and its own `verify` subcommand), and the write side of `config` and `runtime-mapping-file`. Pulled forward from slice 4 because `init --yes`, `doctor --security` and `init --cli` need them: the capture side of turn snapshots (`captureTurnTreeSnapshot`, the repository cache, quiescence and the object alternates), `secret-allowlist`, and the sidecar hook-config writer (`codexSidecarLedgerPath`, `sidecarCliCommand`, `codexSidecarHookConfig`, `sidecarProjectHookTarget`, `writeSidecarProjectHookConfig`). |
| 4 | Turns and Codex (complete: [PR #214](https://github.com/Mizore66/faultline/pull/214), merged 2026-10-29) | `codex`, `record`, the rest of the sidecar (`codex sidecar install\|status\|config` and the hook), `codex snapshot gc`, turn bundles (the turn-snapshot code beyond the capture slice 3 ports), `investigate turns`, `prove transition`, `witness implement`, turn-bundle verify, `attest`, `provenance`, `repair`, and hostile-verifier corpus parity for turn bundles. The later approved Slice 4 spec excludes the two no-caller helpers and defers PR #195 batch staging until post-parity; see the closure notes below. |
| 5 | Surfaces, packaging, deletion ([5a](2026-10-30-go-slice5a-cli-surfaces-design.md) complete and merged as PR #215; [5b.1](2026-10-30-go-slice5b1-cached-artifacts-preview-design.md) local implementation, independent review/final fix and bounded post-fix integration complete; full acceptance pending, including hosted/six-native/Linux gates and long/full-tagged gaps; uncommitted; see the [plan status](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md)) | `serve`, `ui`, `judge-demo`, `judge-preview`, `demo` (including `demo live-git`), `tutorial`, `help` and its aliases, `quickstart`, `advanced`, `--version`, `witness review` (server and TTY), `witness propose --live`, guided `investigate --ci-log` / `--resume`, release builds, npm wrapper, both Actions, Codex plugin, then deletion of TS. |

Every one of the ~40 subcommands dispatched in `src/cli-app.ts` must be assigned to a slice spec before slice 5 closes. As of slice 2 every top-level command and subcommand there has a slice in the table above; `verify` (proof bundles) shipped in slice 1. Slice 3 is complete (2026-10-26): `runtime`, `doctor`, `init`, `incident suggest|start|status|continue` and `prevention verify|write` run in Go. Slice 4 is complete; the remaining unported command surfaces belong to Slice 5.

**Slice 4 closure (updated 2026-10-30).** PR #214 merged at `6f2450549acb002f04a73dd3c1e3091299d7d59f`. Its reviewed head, `1a9e871c98d28077d578d62c28ea96b0794277dc`, completed all four hosted workflows successfully on attempt 1: push [Go](https://github.com/Mizore66/faultline/actions/runs/40922006791) and [Verify](https://github.com/Mizore66/faultline/actions/runs/40922006757), PR [Go](https://github.com/Mizore66/faultline/actions/runs/40922018416) and [Verify](https://github.com/Mizore66/faultline/actions/runs/40922018405). The final rollup was 54 checks: 52 successful and two expected main-push-only provenance skips, with no failed or pending checks. This evidence belongs to the reviewed head, not a fresh test run on the merge commit or Slice 5.

The audited gates include six-runner Go suites, pinned-Node golden regeneration/no drift, full live writer replay and cross-verification, both Ubuntu amd64 positional coverage gates, native Windows runtime regressions, native-runner oracle comparisons, Linux writer faults/real Docker, and hosted stack/platform comparisons. The Linux ARM regular matrix did not enable the positional coverage flag. Skipped provenance jobs do not prove signed attestations. The audited push Colima job and PR Windows Docker job did not execute their Docker E2E steps (VM startup failure and image applicability respectively); their green containing jobs are not macOS/Windows Docker execution evidence. The non-blocking high-severity Zod advisory remains a separate open follow-up. These results supersede the earlier local-only status, not historical failures or incomplete attempts.

**Slice 4 scope reconciliation.** The [approved Slice 4 spec](2026-10-27-go-slice4-turns-codex-design.md), Sections 1 (out of scope), 3.6 and 4.2 item 7, already excludes `captureAllowlistedEvent` and `src/prevention-from-turn.ts` (no caller in `src/`) and defers PR #195 batch turn-snapshot staging to a post-parity performance item. Neither helper is claimed ported. Slice 5's compatibility-closure work must make their final disposition explicit before TS retirement; this status correction does not approve deleting them or add them to completed Slice 4 scope.

**Slice 5a closure (updated 2026-10-30).** [PR #215](https://github.com/Mizore66/faultline/pull/215) merged at `647440884a6668042f7726f2cbade38886d00a41` after explicit ready/merge authorization under verified Mizore66. Reviewed head `707a28a8691d5dd8a13f3b94faa6f819ac34a406` passed all four automatic hosted workflows on attempt 1: push [Go](https://github.com/Mizore66/faultline/actions/runs/41004463505) and [Verify](https://github.com/Mizore66/faultline/actions/runs/41004463562), PR [Go](https://github.com/Mizore66/faultline/actions/runs/41004467971) and [Verify](https://github.com/Mizore66/faultline/actions/runs/41004467894). All 52 successful job logs were audited; two main-push-only provenance skips were expected. Both events ran all six native Go targets; the two live CLI matrices each compared 67 cases. This is reviewed-head acceptance, not fresh post-merge CI or signed-attestation evidence. The tolerated Zod audit failure remains open; Windows image applicability and macOS Colima startup skips do not establish Docker execution on those platforms. No release was published.

**Slice 5 sequencing.** The approved direction remains small parity-first subprojects, followed by validated distribution and an explicitly reviewed final cutover. The [5a spec](2026-10-30-go-slice5a-cli-surfaces-design.md) records the dependency map. The user approved splitting 5b: [5b.1](2026-10-30-go-slice5b1-cached-artifacts-preview-design.md) covers cached REPLAY analysis, demo-v2 writing and complete static judge-preview. Its specification and [implementation plan](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md) were approved and executed in the working tree on branch `go-slice5b1-design-20261030` (nothing staged, committed or published). **Status (2026-10-31): local implementation, independent task/whole-branch review, the one final fix/scoped recheck and bounded post-fix integration are complete; full acceptance remains pending.** The [plan status](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md) records actual results/skips, retained process deviations, long/full-tagged evidence gaps and the unexecuted exact-head hosted, six-native, Ubuntu positional, canonical pnpm/tsx and native Linux+strace gates. Local completion does not waive any of them or establish full 5b.1 acceptance. Publication is not authorized. A separate 5b follow-up owns genuine RERUN execution, the interactive renderer, full judge-demo and plain serve, including mutation security and lifecycle decisions. Until that follow-up, judge-demo (including cached export-only) and plain serve remain explicitly unported. New report-mode features are excluded; existing `witness review --json` remains in scope. Protected-source changes, oracle retirement and publication need separate authorization.

## Slice 5 decisions (recorded 2026-10-14)

Decided after slice 1 merged (PR #205); slice 5's own spec builds on them.

1. **Linux release binaries are static and musl-linked, built with cgo.** Since slice 1, a C constructor in `internal/boot` records the signal dispositions the parent left before the Go runtime replaces them (KNOWN_DIFFERENCES: signals). A plain `CGO_ENABLED=0` build cannot, so the release links cgo statically against musl (for example `CC="zig cc -target <arch>-linux-musl"` with `-linkmode external -extldflags -static`). The binary then runs on glibc and musl systems alike, and in `FROM scratch` images. `CGO_ENABLED=0` stays a supported fallback build, with the documented signal difference. macOS and Windows builds are unchanged.
2. **The error hint becomes `Next: fl help`.** Slice 1 keeps TS's `Next: pnpm fl help` byte for byte, because the harness compares with the frozen TS. Slice 5 ports `help` and then changes the hint, with a golden update and a harness normalization for the remaining comparisons with TS. The `Note: Do not place '--' ... Use 'pnpm fl <command>'` hint changes the same way.
3. **New `--json` report modes (originally optional; deferred by the 2026-10-30 scope direction).** The earlier option was to add `--json` to `verify` and other report commands, tested directly because it has no TS counterpart. It is not selected for the current Slice 5 migration scope: do not add those new modes. Existing surfaces such as `doctor --json` and `witness review --json` retain their parity obligations.
4. **`doctor` drops its Node check, and sidecar hooks call the `fl` binary** (recorded 2026-10-20, slice 3 spec 3.1). Slice 3 keeps TS's `node --version` check (exit 1 when it is not READY) and the hook command `node '<cli>' codex sidecar hook --quiet` that `init --cli` writes, byte for byte. Once the Go binary ships, Node is no longer a FaultLine dependency: slice 5 removes the check from `doctor` and has the hooks run `fl codex sidecar hook --quiet`, with golden updates.

## Known program risks

- **Collation parity.** `internal/collation` (an ICU port that replaced `golang.org/x/text/collate`) must match Node ICU `localeCompare` for every key TS can produce. Mitigated by live property tests (slice 1).
- **zod message parity.** Go reproduces zod v3 issue messages for the issue kinds the schemas use. Any kind excluded from exact matching is documented.
- **Node error text.** Node filesystem error messages that reach CLI output must be reproduced.
- **Windows behavior.** Path separators and symlink permissions differ; covered by the Windows CI job.
- **Size.** About 30k lines of TS source and 13k lines of tests.
