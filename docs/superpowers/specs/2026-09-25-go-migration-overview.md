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
| TypeScript status | Frozen as of 2026-09-25 at `faa98c0` (tag `v0.1.9-buildweek`). No features, no fixes. All new work happens in Go. |
| Strategy | Vertical slices, each proven against frozen TS by a differential harness before the next slice starts. |
| Release timing | No Go release until full parity (end of slice 5). Until then the published npm package stays at the frozen TS version. |
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
| 3 | Runtime | `runtime`, `doctor`, `init`, `incident`, `prevention` (proof creation and its own `verify` subcommand), and the write side of `config` and `runtime-mapping-file`. |
| 4 | Turns and Codex | `codex`, `record`, the sidecar, turn snapshots, `investigate turns`, `prove transition`, `witness implement`, turn-bundle verify, `attest`, `provenance` and `repair`. Also `secret-allowlist` and `captureAllowlistedEvent` (reachable only from `turn-snapshot`), PR #195's batch turn-snapshot staging (`git update-index --stdin-paths --no-filters`, with the per-path fallback for paths containing TAB, LF or CR, gated on identical tree digests), and hostile-verifier corpus parity for turn bundles. |
| 5 | Surfaces, packaging, deletion | `serve`, `ui`, `judge-demo`, `judge-preview`, `demo` (including `demo live-git`), `tutorial`, `help` and its aliases, `quickstart`, `advanced`, `--version`, `witness review` (server and TTY), `witness propose --live`, guided `investigate --ci-log` / `--resume`, release builds, npm wrapper, Action, Codex plugin, then deletion of TS. |

Every one of the ~40 subcommands dispatched in `src/cli-app.ts` must be assigned to a slice spec before slice 5 closes. As of slice 2 every top-level command and subcommand there has a slice in the table above; `verify` (proof bundles) shipped in slice 1.

## Slice 5 decisions (recorded 2026-10-14)

Decided after slice 1 merged (PR #205); slice 5's own spec builds on them.

1. **Linux release binaries are static and musl-linked, built with cgo.** Since slice 1, a C constructor in `internal/boot` records the signal dispositions the parent left before the Go runtime replaces them (KNOWN_DIFFERENCES: signals). A plain `CGO_ENABLED=0` build cannot, so the release links cgo statically against musl (for example `CC="zig cc -target <arch>-linux-musl"` with `-linkmode external -extldflags -static`). The binary then runs on glibc and musl systems alike, and in `FROM scratch` images. `CGO_ENABLED=0` stays a supported fallback build, with the documented signal difference. macOS and Windows builds are unchanged.
2. **The error hint becomes `Next: fl help`.** Slice 1 keeps TS's `Next: pnpm fl help` byte for byte, because the harness compares with the frozen TS. Slice 5 ports `help` and then changes the hint, with a golden update and a harness normalization for the remaining comparisons with TS. The `Note: Do not place '--' ... Use 'pnpm fl <command>'` hint changes the same way.
3. **`--json` output mode for agents (optional).** Slice 5 may add `--json` to `verify` and other report commands: one JSON object on stdout with the verdict, status fields and the error list, and the same exit code. It is new surface with no TS counterpart, so it is tested directly rather than against goldens.

## Known program risks

- **Collation parity.** `internal/collation` (an ICU port that replaced `golang.org/x/text/collate`) must match Node ICU `localeCompare` for every key TS can produce. Mitigated by live property tests (slice 1).
- **zod message parity.** Go reproduces zod v3 issue messages for the issue kinds the schemas use. Any kind excluded from exact matching is documented.
- **Node error text.** Node filesystem error messages that reach CLI output must be reproduced.
- **Windows behavior.** Path separators and symlink permissions differ; covered by the Windows CI job.
- **Size.** About 30k lines of TS source and 13k lines of tests.
