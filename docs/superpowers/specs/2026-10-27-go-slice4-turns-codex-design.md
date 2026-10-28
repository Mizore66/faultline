# Go migration slice 4 — recording, turn proofs, Codex and repair

- **Date:** 2026-10-27
- **Status:** Approved design (brainstorming session, 2026-10-27)
- **Parent:** [Go migration overview](2026-09-25-go-migration-overview.md); builds on slices [1](2026-09-25-go-slice1-verify-design.md), [2](2026-10-14-go-slice2-git-path-design.md) and [3](2026-10-20-go-slice3-runtime-design.md)
- **TS oracle:** frozen TS at `ce99a79` (`v0.1.10-buildweek`), run as `node dist/cli.js`, the same oracle as slice 3

## 1. Scope and layout

Slice 4 is one spec delivered as three PRs. Each PR has its own plan, review rounds and done criteria (Section 4.2). PR 4b builds on PR 4a's ledger writers; PR 4c is independent of 4b except for shared harness pieces.

### Commands

**PR 4a: recording**

| Command | TS entry | Notes |
| --- | --- | --- |
| `record init`, `checkpoint`, `attach`, `stdin`, `verify` | `recordCommand` (`src/cli-app.ts:1488`), `src/ledger.ts` | `fl codex record …` is an alias (`cli-app.ts:2995`). |
| `codex sidecar config`, `install`, `status`, `hook` | `codexSidecarCommand` (`cli-app.ts:1757`), `src/codex-sidecar.ts` | `config`/`install` reuse slice 3's hook writer in `internal/projinit`. |
| `codex snapshot gc` | `cli-app.ts:3003-3016`, `src/snapshot-gc.ts` | |
| `codex --dry-run`, `codex --snapshot` | `cli-app.ts:3017-3021`, `src/git-snapshot.ts` | |

PR 4a also ports the `ledger.ts` writers (`createCodexLifecycleLedger`, `appendLifecycleEvent`, `signGitCheckpoint`, `captureGitCleanCheckpoint`, the atomic write and the lock) and the sidecar hook state machine (`codex-sidecar.ts:45-1111`: locks, stop receipts, baseline and turn snapshots, status inspection, `resolveLatestSidecarLedgerPath`). The ledger verifier already exists in `internal/bundle/gitproof`, and turn-snapshot capture in `internal/turnsnap`. None of these commands starts the `codex` CLI; the sidecar receives Codex hook events as JSON on stdin.

**PR 4b: turn proofs**

| Command | TS entry | Notes |
| --- | --- | --- |
| `investigate turns` (with `--latest`, `--minimize`) | `investigateCommand` turns branch (`cli-app.ts:2045-2160`), `src/turn-investigation.ts` | |
| `prove transition` | `proveTransitionCommand` (`cli-app.ts:2294-2356`), `src/turn-minimization-bridge.ts` | Shares `minimizeFromTurnInvestigation` (`cli-app.ts:2239-2288`) with `investigate turns --minimize`. |
| Turn proof bundles; turn branch of `fl verify` | `src/turn-proof-bundle.ts`; `cli-app.ts:2927-2939` | Replaces Go's `not yet ported: turn proof bundle verification`. |

PR 4b also adds the hostile-verifier turn corpus (Section 2.4).

**PR 4c: Codex, repair, attestation, provenance**

| Command | TS entry | Notes |
| --- | --- | --- |
| `witness implement` | `cli-app.ts:1169-1203`, `src/codex-loop.ts` | `--with-codex`, `--allow-codex-full-auto`. |
| `repair --bundle`, `repair brief` (`--input`, `--live`), `repair verify` | `cli-app.ts:2672-2801`, `src/codex-loop.ts`, `src/repair-brief.ts`, `src/repair-brief-store.ts`, `src/repair-worktree.ts`, `src/openai-response.ts`, `src/runners/codex-runner.ts` | |
| `attest create`, `attest verify` | `cli-app.ts:2462-2490`, `src/attestation.ts` | Turn bundles are refused, as in TS. |
| `provenance create`, `provenance verify` | `cli-app.ts:2497-2536`, `src/github-provenance.ts` | |

PR 4c also ports the repaired-run helpers from `src/prevention-from-artifacts.ts` (`commitRepairedWorktreeState` 342-480, `collectRepairedPreventionRuns` 486-540, the hard-guard digest sidecar 543-551) and adds `RepairBaseTree` and `HardGuardArtifactDigests` to `prevention.ArtifactOptions`, and cleans up `incident.DraftInput` (slice 3b deferred minor) before any repair path uses drafts.

Every other subcommand keeps throwing `not yet ported: <name>`.

### Out of scope

- PR #195's batch turn-snapshot staging (`git hash-object -w --stdin-paths --no-filters` with the per-path fallback). It exists only as a spec on an unmerged draft PR (`docs/post-submission/perf-batch-staging-e40b.md` on `cursor/submission-bucket2-e40b`), not in frozen TS, and it changes the git argv. It becomes a post-parity performance item, after slice 5 deletes TS.
- `captureAllowlistedEvent` (`src/redaction.ts:441`) and `src/prevention-from-turn.ts`: no caller in `src/`. They are not ported; `difftest/KNOWN_DIFFERENCES.md` records that.
- `serve --repair` and the UI hub's turn view: slice 5.

### Layout

```
internal/ledger            ledger.ts writers (create, append, checkpoint, atomic write, lock); the verifier stays in bundle/gitproof
internal/sidecar           codex-sidecar.ts hook state machine, status, latest-ledger resolution
internal/snapgc            snapshot-gc.ts and git-snapshot.ts
internal/turninv           turn-investigation.ts
internal/bundle/turnproof  turn-proof-bundle.ts writer and verifier
internal/minimize          gains the turn-minimization bridge
internal/codexloop         codex-loop.ts and runners/codex-runner.ts
internal/repair            repair-brief.ts, repair-brief-store.ts, repair-worktree.ts, openai-response.ts
internal/attest            attestation.ts
internal/provenance        github-provenance.ts
internal/cli/{record,codex,turns,prove,repair,attest,provenance}.go
```

All writers use the existing `canonical`, `jsjson`, `schema`, `nodefs`, `nodeproc`, `clock`, `safedir`, `safegit`, `gitinv`, `sandbox` and `turnsnap` packages.

## 2. Differential harness

Slice 4 extends the slice 3 writer replay; it adds inputs and fakes, not a new harness.

### 2.1 Seeded clock and IDs

The preload `difftest/gen/seed.mjs` already seeds every clock and UUID read slice 4 adds: ledger and event ids (`ledger-<uuid>`, `event-<uuid>`), `createdAt`/`occurredAt`/`capturedAt`, run `executionNonce`, `startedAt`/`finishedAt`/`durationMs`, bundle `generatedAt` and staging UUIDs, the `Date.now()` output names (`turns-*`, `repair-*`, `turn-transition-*`, `from-repair-*`), attestation `issuedAt`, provenance `createdAt`, `repairId` and temporary index names. Go reads them from `internal/clock` in TS's order. Values a seed cannot fix are masked: the process id in lock and temp file names (`<PID>`) and `sidecar status`'s `lock.ageMilliseconds`.

### 2.2 Commit ids and created Git objects

`git commit-tree` takes author and committer time from the clock, and the hardened git environment passes only `PATH`, so the commit ids written by the turn-to-minimization bridge and by `commitRepairedWorktreeState` differ between runs. A new `<COMMIT>` mask replaces a commit id only when `git cat-file -p` of that id, on its own side, gives the expected tree, parents and message. A commit with a wrong tree, parent or message is left unmasked and fails the comparison. The rule is identical in `difftest/gen/gen-writers.ts` and `difftest/writer_test.go`, like slice 3a's `<PLANDIGEST>`.

**PR 4c (plan correction 1).** A commit mask alone is not enough for `repair`: the repaired worktree holds `.faultline-repair/README.md`, whose text names the absolute managed root, so the blob, the tree and the commit ids all vary with the case directory. 4c masks every object a case creates (`<COMMIT:…>`, `<TREE:…>`, `<BLOB:…>`, checked with `git cat-file -p` on the side that made it), renames created loose objects and reflog lines by that name, and recomputes two relocation seeds from their real fields: the verifier's `exec:` id and the repaired runs' `run:` ids. An object that existed before the case or lives in the turn-snapshot store keeps its id; author and committer names, emails, tree, parents and message stay significant, and only timestamps are normalized.

### 2.3 Inputs and fakes

- **Stdin.** Writer templates gain an `input` field (bytes per invocation), for `codex sidecar hook` and `record stdin`.
- **Fake `codex`** selected with `FAULTLINE_CODEX_BIN` (which TS already honours): answers `--version`; `exec --full-auto` applies scripted edits. Variants: probe failure, non-zero exit, killed by SIGTERM (a timeout cannot be a golden: 120 s and 180 s are too slow, so the writer goldens get `exitCode: null` from a fake that kills itself and the timeout itself is compared live against `spawnSync`), edits outside the allowed files.
- **Fake `gh`** on `PATH`: `attestation verify` returns a scripted pass or failure (variants `pass`, `fail`, `long-fail`, `flood`; one shared body, `difftest/testdata/fakegh/lib.sh`). The fake `codex` is likewise one script with a `variant=` line.
- **Fake OpenAI**: a local HTTP server the harness starts. TS reaches it through a new preload `difftest/gen/fetch-shim.mjs` that redirects `https://api.openai.com/v1/responses`; Go through a base-URL override (`FAULTLINE_TEST_OPENAI_URL`) that exists only under the `faultline_test` build tag. Release builds keep TS's hard-coded URL, and the `go` CI job greps the untagged binary for the name. Scripted responses: valid, malformed, wrong citations, HTTP errors.
- **Hook event sequences**: generated JSON documents for `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse` and `Stop`, including redelivery, a dirty tree, no HEAD, a contradictory receipt, a planted stale lock and a symlinked `.git/faultline`.

### 2.4 Cases

- Writer cases for every command and branch in Section 1.
- A turn proof base in `difftest/testdata/bases/`, built by `difftest/gen/gen-bases.ts` from a recorded ledger with turn snapshots.
- The hostile-verifier turn corpus: the 15 cases in `docs/post-submission/hostile-verifier-turn-parity-e40b.md` (on the PR #195 branch) as `mutations.json` templates on the turn base, replayed through `fl verify`. Go reproduces TS exactly, including that the turn hash catalog has no 2048-entry cap and uses its own messages ("must end with a newline", "invalid hash catalog entry").
- Every turn bundle, attestation, provenance receipt, repair brief and prevention package Go writes is also verified by the TS verifiers.

### 2.5 Live Node-oracle tests

- generated hook-event sequences through the sidecar state machine;
- `turnStatesFromLedger` and `reconstructStableTurnStates` on generated ledgers;
- the turn hash catalog parser;
- `extractOpenAiResponseText` on generated responses.

### 2.6 Faults and end to end

- The strace fault matrix gains the new writer paths: the ledger temporary file and rename, the ledger lock, sidecar stop receipts, the turn bundle stage, the repair brief stage and the attestation link. Renames are faulted through their source path (slice 3a CI: on x86_64, `strace -P` matches only `rename(2)`'s first argument).
- One end-to-end test without Node per PR: 4a `record init` → hook events → `record verify`; 4b the same, then `investigate turns` → `fl verify` → `prove transition`; 4c (`TestEndToEndSlice4cWithoutNode`, as reordered by plan correction 4) `investigate git` → `repair --bundle` with the fake `codex` → `verify` of its package → `repair brief --input` → `repair verify` → `prevention write --from-bundle` on the repair's own runs → `attest create` → `attest verify`.
- 4c's fault paths (19 groups) include the attestation store root and staged file, the provenance receipt and root, the repair brief stage, manifest and root, the two `witness implement` overlay files, and the repair managed root, `repair-meta.json`, `repair.patch`, the temporary index, the prevention stage and `prevention-export.json`; `repair-bundle-codex-ok` is a two-invocation case of which only the last (repair) invocation is faulted.

### 2.7 Not compared byte for byte

Real Codex or OpenAI output (always faked) and the turn-snapshot quiescence wall time.

## 3. Behaviour

### 3.1 Compatibility

stdout, stderr and exit codes match frozen TS byte for byte, including `Next: pnpm fl help`. Every KNOWN_DIFFERENCES rule from slices 1–3 applies to the new commands.

### 3.2 Process model

Go keeps TS's choice at each call site:

- **git**, always `spawnSync`. Hardened (`safegit.HostCommand()` with the stripped environment): the ledger checkpoint (60 s timeout) and the sidecar storage lookup (no timeout); turn-snapshot capture reuses `turnsnap`. Not hardened, with the full inherited environment: `codex --snapshot` (`git -C …`, `src/git-snapshot.ts:18`) and the turn-to-minimization bridge (`rev-parse`, `commit-tree` with `-c user.name=FaultLine -c user.email=faultline@example.invalid`).
- **Docker**, through slice 2's sandbox: 3 runs per state in `investigate turns`; the repair verifier's 1 + 3 runs.
- **`codex` CLI**: `FAULTLINE_CODEX_BIN`, else `codex` on `PATH` (which never fails to resolve: `resolveOnPath` falls back to the bare name, so `Codex CLI was not found on PATH` is unreachable and unported); a `--version` probe, then `spawnSync(bin, ["exec", "--full-auto", prompt], {input: prompt})` with an environment of `PATH` (plus `ComSpec` and `SystemRoot` on Windows); 120 s for `witness implement`, 180 s for `repair`.
- **`gh attestation verify`**, synchronous, with the offline `--bundle` and `--custom-trusted-root` flags.
- **OpenAI**, `repair brief --live` only: one POST to the Responses API with a strict json_schema `text.format`, `Authorization: Bearer $OPENAI_API_KEY`. Go's client reproduces undici's request and outcomes (TLS roots, `Accept-Encoding`, proxies and redirects differ: KNOWN_DIFFERENCES).

### 3.3 Writes

| Writer | Rule |
| --- | --- |
| Ledger | temporary file `.<base>.<pid>.<uuid>.tmp` (0600, `wx`), fsync, rename, best-effort directory fsync; lock `<ledger>.lock` (`wx`, fails at once if held) |
| Sidecar | storage `realpath(git-common-dir)/faultline/recordings/` (0700 per level); lock `<ledger>.sidecar.lock` (2 s wait, 25 ms retry, stale after 45 s); stop receipts through a temporary file |
| Turn bundle | stage `.<name>.staging-<uuid>` (0700/0600, `wx`), self-verify, rename |
| Snapshot GC | `rm -rf` of the snapshot object store; rewrite or removal of `alternates` |
| `codex --snapshot` | `.faultline/recordings/<digest>.json`, overwritten, default mode |
| Attestation | write-once through `link(2)` |
| Provenance receipt | `ci-receipt.json`, 0600, `wx` |
| Repair brief | stage, fsync, rename |
| `witness implement` | overwrites its two overlay files |

When a write or a child process fails part way, Go leaves the same files behind as TS (fault matrix, Section 2.6).

### 3.4 Turn proofs

- State reconstruction, stable transitions, the duplicate-ordinal error and failure attribution match `src/turn-investigation.ts:347-613`.
- The bundle packs trees with `git rev-list --objects` and `git pack-objects --stdout --compression=9`.
- `fl verify` on a turn bundle prints TS's lines (`Evidence grade: EXPERIMENTAL_TURN` is fixed text), unpacks with `unpack-objects --strict` and falls back to `index-pack --strict`, and keeps TS's limits: 128 MiB per artifact, `hashes.txt` at most 4 MiB, 2048 files and 512 MiB in total; the turn hash catalog itself is uncapped.
- `prove transition` exits 0 even when the minimization is not certified.
- `investigate turns` writes the session binding before it reads the frozen witness.

### 3.5 Codex and repair

- `--allow-codex-full-auto` is checked inside the runner; without it the result is `CODEX_UNAVAILABLE` with `transport: "NONE"` and the note `Codex CLI could not be started: …` (exit 1 for `witness implement`, exit 0 for `repair`); a failed `collectRepairPatch` is reported the same way.
- `repair` uses the repository recorded in the investigation before `--repo` (a committed base records a repository that does not exist, so every successful case investigates first in the same workspace; `commit-tree` needs a `[user]` identity in the repository config, and its absence is compared only by a same-machine live test); `codexThreadId` is always `null` from the CLI.
- `repair brief` takes exactly one of `--live`/`--input` and exactly one of `--bundle`/`--investigation`; evidence schema v1 is refused as legacy; `--live` without `OPENAI_API_KEY` throws TS's text.
- `provenance create` requires the `GITHUB_*` environment and refuses locally; run and execution ids are sorted with `localeCompare`.

### 3.6 Expected KNOWN_DIFFERENCES entries

Added as confirmed: the created-object masks (Section 2.2); the live repair brief HTTP differences (TLS roots, `Accept-Encoding` and Brotli, proxies, redirects, header size); Codex timeouts compared live; the identity-less `commit-tree` error; `captureAllowlistedEvent` and `prevention-from-turn` not ported; batch staging deferred past parity.

## 4. CI and done criteria

### 4.1 CI

- **`go`** (six runners): the 4a/4b/4c writer replays; Windows skips only POSIX-only cases (`.github/scripts/windows-writer-skips.txt`); the skip guard, the coverage gate and the error-literal gate cover every new package.
- **`difftest-goldens`**: regenerates goldens, the turn base and the corpus mutations, and fails on drift.
- **`difftest-live`**: the new live tests through its `-list 'TestLive'` derivation; the budget is checked against measured durations.
- **`difftest-live-faults`**: the new writer paths.
- **`difftest-live-replay`**: the three end-to-end tests and the TS cross-verification of every new artifact kind.
- **`stack-live`**: the sidecar storage walk and turn reconstruction live tests on macOS.

### 4.2 Done criteria

Each holds per PR, for the commands that PR ports.

1. `go test ./...` passes on all six runners, and every writer case matches its golden.
2. `difftest-live`, `difftest-live-faults` and `stack-live` pass.
3. The PR's end-to-end test without Node passes.
4. Every turn bundle, attestation, provenance receipt, repair brief and prevention package Go writes verifies under TS.
5. PR 4b: the hostile-verifier turn corpus gives identical verdicts and error text in Go and TS.
6. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference.
7. The overview is updated: slice 4 complete; PR #195's batch staging moved to a post-parity performance item; `captureAllowlistedEvent` and `prevention-from-turn` recorded as not ported.
8. Slice 3b's carried items are folded in: `ArtifactOptions.RepairBaseTree` and `HardGuardArtifactDigests`, and the `incident.DraftInput` cleanup (PR 4c).

Adversarial review rounds run per PR, time-boxed to about 90 minutes each, until a round finds no P0 or P1.
