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

The preload `difftest/gen/seed.mjs` already seeds every clock and UUID read slice 4 adds: ledger and event ids (`ledger-<uuid>`, `event-<uuid>`), `createdAt`/`occurredAt`/`capturedAt`, run `executionNonce`, `startedAt`/`finishedAt`/`durationMs`, bundle `generatedAt` and staging UUIDs, the `Date.now()` output names (`turns-*`, `repair-*`, `turn-transition-*`, `from-repair-*`), attestation `issuedAt`, provenance `createdAt`, `repairId` and temporary index names. Go reads them from `internal/clock` in TS's order. Values a seed cannot fix are masked: the process id in temporary file names (`<PID>`; no lock name holds one: `<ledger>.lock` and `<ledger>.sidecar.lock` are fixed), the sidecar receipt stage `.<sha256(receiptPath)[0:24]>.<pid>.tmp` (`src/codex-sidecar.ts:529`) as `.<STAGEKEY>.<pid>.tmp` (its key hashes the absolute receipt path, so it differs between the TS and the Go case directory; a sidecar unit test pins the key), and the age of an ACTIVE `sidecar status` lock (`<LOCKAGE>`; a stale lock's age is fixed by the seed and stays compared). The ledger's temporary name `.<base>.<pid>.<uuid>.tmp` keeps the slice 2 mask.

Each `fl` invocation starts at the seed epoch with UUID counter 0, so a second `record` or hook invocation in a case would write an `occurredAt` before the first one's and repeat `event-<uuid>` ids. Writer templates therefore gain `advanceClock: true`: invocation `n >= 1` runs with seed `<id>/<n>` and `FAULTLINE_TEST_CLOCK_OFFSET_MS = n x 3,600,000`, which `seed.mjs` and `internal/clock` add to the epoch. Scenario ledgers are built by the TS ledger functions with fixed timestamps before the epoch, not by running `fl`.

The sidecar's 2 s lock wait runs on the seeded clock: `withSidecarLock` reads `Date.now()` three times per 25 ms retry, and each read advances one millisecond, so a held fresh lock is reported after about 667 retries, about 17 s of real time, on both sides. A fresh planted lock is never stale (its real mtime is after the seed epoch), and its `lock.ageMilliseconds` is `0`; one planted with `set-mtime` 2000-01-01 always is stale.

### 2.2 Commit ids

`git commit-tree` takes author and committer time from the clock, and the hardened git environment passes only `PATH`, so the commit ids written by the turn-to-minimization bridge and by `commitRepairedWorktreeState` differ between runs. A new `<COMMIT>` mask replaces a commit id only when `git cat-file -p` of that id, on its own side, gives the expected tree, parents and message. A commit with a wrong tree, parent or message is left unmasked and fails the comparison. The rule is identical in `difftest/gen/gen-writers.ts` and `difftest/writer_test.go`, like slice 3a's `<PLANDIGEST>`.

### 2.3 Inputs and fakes

- **Stdin.** Writer templates gain an `input` field (bytes per invocation), for `codex sidecar hook` and `record stdin`. The 4 MB limit counts the UTF-8-decoded text (`readStandardInput`, `src/cli-app.ts:287`, checks `Buffer.byteLength` of the decoded body), so each invalid byte counts 3 (U+FFFD); its text, `Lifecycle stdin exceeds the 4 MB safety limit.`, is also what `codex sidecar hook` prints.
- **Relocation.** The sidecar ledger and receipts live in `.git/faultline/recordings/` and hold the case path (`workingDirectory`, `repositoryRoot`) and digests over it, so the writer relocation walk, which skips every `.git`, walks that one subtree on both sides.
- **Layout of the cases.** Templates may also live in `difftest/testdata/writers.d/*.json` and goldens in `difftest/testdata/golden/writers.d/*.jsonl`, one file per task; `.github/scripts/windows-writer-skips.txt` is marked `merge=union`.
- **Fake `codex`** selected with `FAULTLINE_CODEX_BIN` (which TS already honours): answers `--version`; `exec --full-auto` applies scripted edits. Variants: probe failure, non-zero exit, timeout, edits outside the allowed files.
- **Fake `gh`** on `PATH`: `attestation verify` returns a scripted pass or failure.
- **Fake OpenAI**: a local HTTP server the harness starts. TS reaches it through a new preload `difftest/gen/fetch-shim.mjs` that redirects `https://api.openai.com/v1/responses`; Go through a base-URL override that exists only under the `faultline_test` build tag. Release builds keep TS's hard-coded URL. Scripted responses: valid, malformed, wrong citations, HTTP errors.
- **Hook event sequences**: generated JSON documents for `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse` and `Stop`, including redelivery, a dirty tree, no HEAD, a contradictory receipt, a planted stale lock and a symlinked `.git/faultline`.

### 2.4 Cases

- Writer cases for every command and branch in Section 1.
- A turn proof base in `difftest/testdata/bases/`, built by `difftest/gen/gen-bases.ts` from a recorded ledger with turn snapshots.
- The hostile-verifier turn corpus: the 15 cases in `docs/post-submission/hostile-verifier-turn-parity-e40b.md` (on the PR #195 branch) as `mutations.json` templates on the turn base, replayed through `fl verify`. Go reproduces TS exactly, including that the turn hash catalog has no 2048-entry cap and uses its own messages, not the Git verifier's (`parseHashCatalog`, `turn-proof-bundle.ts:390-410`): `hashes.txt must end with a trailing newline`; it iterates `text.trimEnd().split("\n")` without filtering empty strings, so an empty catalog and every blank line give `hashes.txt contains a malformed line: ` (JS `trimEnd` also strips trailing spaces, `\r`, U+2028 and U+FEFF); it matches `/^([a-f0-9]{64})  (.+)$/` (`.` excludes `\r`, U+2028 and U+2029); an unsafe path gives `hashes.txt contains an unsafe artifact path: <line>` quoting the whole line; a duplicate gives `hashes.txt declares duplicate artifact: <artifact>` and is not skipped (`Map.set` replaces the digest and keeps the first insertion position); a 2,100-line catalog yields one `declared artifact cannot be read safely (…)` and one `hashes.txt contains an unexpected artifact: …` per extra line. Duplicate JSON keys keep the last value as `JSON.parse` does, so a manifest whose duplicated key ends with the valid value verifies, and `fl verify`'s `schemaVersion` sniff uses the same rule. Hostile-corpus row 4 (`C:/outside`) is platform-dependent: a safe relative path read as ENOENT on POSIX, unsafe with `path.win32`, so its mutation template is `posixOnly` (skipped on Windows only).
- Every turn bundle, attestation, provenance receipt, repair brief and prevention package Go writes is also verified by the TS verifiers.

### 2.5 Live Node-oracle tests

- generated hook-event sequences through the sidecar state machine;
- `turnStatesFromLedger` and `reconstructStableTurnStates` on generated ledgers;
- the turn hash catalog parser;
- `extractOpenAiResponseText` on generated responses.

### 2.6 Faults and end to end

- The strace fault matrix gains the new writer paths: the ledger temporary file and rename, the ledger lock, sidecar stop receipts, the turn bundle stage, the repair brief stage and the attestation link. Renames are faulted through their source path (slice 3a CI: on x86_64, `strace -P` matches only `rename(2)`'s first argument). The 4a rows (record init and checkpoint, `codex --snapshot`, snapshot gc and the sidecar hooks) run through `TestLiveWriterFaults`. The harness gained `faultLastInvocation` (every invocation runs; only the last runs under strace and is compared: the stop receipt of `sidecar-hook-full-turn`) and per-side receipt stages (the stage key is recomputed from each side's case root before it is faulted, and `<STAGEKEY>` masks it in the compared output).
- The 4b rows (`turns-proof`, `turns-minimize`) fault the paths a fault-free TS trace discovers; a group that never fires is dropped, not faked (none was dropped in 4b). Two rows need a per-side call ordinal (`faultOrdinals`, `difftest/faultordinal_test.go`): the verifier's seeded `mkdtemp` (TS retries a failed mkdir with the real one, so every mkdir from its ordinal is failed in TS and the seeded name in Go; 2 injections in TS, 1 in Go) and the binding rewrite's `close` (the third `close` of the file on each side; a failed close of the preceding READ aborts Node with a native assertion, listed in KNOWN_DIFFERENCES). The `index-pack` file row runs under the `fail-unpack` fake git.
- One end-to-end test without Node per PR (4a: `TestEndToEndSlice4aWithoutNode`; its `record attach` names the ledger with `--ledger`, since `attach` has no `--session` and otherwise addresses `recordings/session.json`, `src/cli-app.ts:256`): 4a `record init` → hook events → `record verify`; 4b the same, then `investigate turns` → `fl verify` → `prove transition`; 4c `repair --bundle` with the fake `codex` → `repair verify` → `prevention write` → `attest create` → `attest verify`.

### 2.7 Not compared byte for byte

Real Codex or OpenAI output (always faked) and the turn-snapshot quiescence wall time.

## 3. Behaviour

### 3.1 Compatibility

stdout, stderr and exit codes match frozen TS byte for byte, including `Next: pnpm fl help`. Every KNOWN_DIFFERENCES rule from slices 1–3 applies to the new commands.

### 3.2 Process model

Go keeps TS's choice at each call site:

- **git**, always `spawnSync`. Hardened (`safegit.HostCommand()` with the stripped environment): the ledger checkpoint (60 s timeout) and the sidecar storage lookup (no timeout); turn-snapshot capture reuses `turnsnap`. Not hardened, with the full inherited environment: `codex --snapshot` (`git -C …`, `src/git-snapshot.ts:18`) and the turn-to-minimization bridge (`rev-parse`, `commit-tree` with `-c user.name=FaultLine -c user.email=faultline@example.invalid`).
- **Docker**, through slice 2's sandbox: 3 runs per state in `investigate turns`; the repair verifier's 1 + 3 runs.
- **`codex` CLI**: `FAULTLINE_CODEX_BIN`, else `codex` on `PATH`; a `--version` probe, then `spawnSync(bin, ["exec", "--full-auto", prompt], {input: prompt})` with an environment of `PATH` (plus `ComSpec` and `SystemRoot` on Windows); 120 s for `witness implement`, 180 s for `repair`.
- **`gh attestation verify`**, synchronous, with the offline `--bundle` and `--custom-trusted-root` flags.
- **OpenAI**, `repair brief --live` only: one POST to the Responses API with a strict json_schema `text.format`, `Authorization: Bearer $OPENAI_API_KEY`.

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

- **Two checkpoint runners.** `fl record` uses `defaultHardenedGitRunner`: a 60 s `spawnSync` timeout and the error `Git checkpoint capture failed for <args>: ...` (`src/ledger.ts:274`). The sidecar's Stop uses its own `runHardenedGit`: no timeout, and the error `FaultLine could not safely inspect this Git worktree: ...` (`src/codex-sidecar.ts:891`). `nodeproc.SpawnSyncOptions` gains `Timeout`, which Node reports as `spawnSync git ETIMEDOUT` with a null status.
- **Capture comes before the lock.** `record checkpoint`, `record attach --checkpoint` and the `record stdin` checkpoint lines capture the checkpoint while the arguments are evaluated, before `appendLifecycleEventAtomic` takes its lock; that function's `mkdirSync(dirname, {recursive: true})` runs before the lock and the read, so a failed append still leaves the directories behind.
- **`codex snapshot gc`** works on `resolve(--repo)`, not on the Git top level or the common directory: the object store is the literal `<repo>/.git/faultline/objects` (`src/turn-snapshot.ts:194`). In a linked worktree (`.git` is a file) it reports `ALREADY_EMPTY`, and from a subdirectory it finds nothing.
- **`codex --snapshot`** writes under `<--repo>/.faultline/recordings`, not under the top level. Its git runs unhardened with the full environment and no `GIT_OPTIONAL_LOCKS`, so its `status` may refresh `.git/index`; the index rule compares the index by content.

### 3.4 Turn proofs

- State reconstruction, stable transitions, the duplicate-ordinal error and failure attribution match `src/turn-investigation.ts:347-613`.
- The bundle packs trees with `git rev-list --objects` and `git pack-objects --stdout --compression=9`.
- `fl verify` on a turn bundle prints TS's lines (`Evidence grade: EXPERIMENTAL_TURN` is fixed text), unpacks with `unpack-objects --strict` and falls back to `index-pack --strict`, and keeps TS's limits: 128 MiB per artifact, `hashes.txt` at most 4 MiB, 2048 files and 512 MiB in total; the turn hash catalog itself is uncapped.
- `--expect-root ""` is "provided" for a turn bundle: the turn verifier tests `expectedRoot === undefined` (`:818`), not truthiness, so it gives `externalRootStatus: "MISMATCH"` and `externally supplied turn proof root is not a sha256 digest`.
- `prove transition` (`cli-app.ts:2294-2356`) verifies the bundle (`mkdtemp` and git) before it reads `--proposal`, so a missing `--proposal` is reported after the verification; it re-reads `investigation.json` with a plain `JSON.parse` and a schema parse; its session binding write passes no `ledgerPath`, so a bound one is kept; it never sets `process.exitCode` and exits 0 whatever the minimization concluded.
- `investigate turns` (`cli-app.ts:2052-2124`) reads the binding (`resolveInheritedSessionFacts`) before the `--latest`/`--ledger` conflict check; `--latest` wins over a bound ledger path; the image is resolved before the binding write, and the frozen witness is read after it; the lifecycle ledger is read again after the output path is computed and before the writer runs; `Date.now()` is read for `turns-<ms>` only when `--output` is absent.
- The minimization output is unmanaged: `minimizeFromTurnInvestigation` writes to `resolve(output ?? .faultline/minimizations/turn-transition-<NNNN>-<Date.now()>.json)` without `minimize git`'s `safeManagedFileOutput`. The flag is `--minimization-output` in `investigate turns --minimize` and `--output` in `prove transition`.
- SHA-256 turn bundles never verify (the pack verifier's `git init --bare` runs with the hardened environment, which does not pass `GIT_DEFAULT_HASH`, so its bare repository is SHA-1), and the minimization bridge refuses SHA-256 trees (it parses every `rev-parse` and `commit-tree` output with `/^[a-f0-9]{40}$/`, `turn-minimization-bridge.ts:9`). Go reproduces both.
- The bridge runs six unhardened `git` processes (`turn-minimization-bridge.ts:21-32, 54-109`): `rev-parse --verify --end-of-options <before>^{tree}`, the same for `<after>`, `commitTree`'s own `rev-parse` of each tree, and `-c user.name=FaultLine -c user.email=faultline@example.invalid commit-tree <tree> -m <message>` for each; each is `spawnSync("git", ["-C", repo, ...args], {encoding: "utf8", windowsHide: true, maxBuffer: 16 MiB})` with the full inherited environment and no timeout. A spawn error throws Node's own error; a non-zero or null status throws `stderr.trim() || "git <args> failed"`.
- `trees.pack` bytes follow git and zlib, like `descendant.bundle`: `pack-objects --stdout --compression=9` of the same sorted id list gave identical bytes on git 2.39.5 and 2.54.0 for the probe tree (KNOWN_DIFFERENCES extends "Git bundle bytes across git versions").
- Two temporary-directory calls share the seeded suffix counter: the investigation's root uses `fs/promises.mkdtemp` (`nodefs.MkdtempAsync`) and the pack verifier `mkdtempSync` (`nodefs.Mkdtemp`); the verifier runs twice per `investigate turns` (the stage, then the CLI), so the call order fixes every later suffix. Its cleanup is `rmSync(…, {recursive, force, maxRetries: 3, retryDelay: 50})` on POSIX, and `20`/`250` with `EPERM`/`EBUSY` swallowed on Windows.
- `runtimeMapping` is `z.record(DigestSchema, DigestPinnedImageSchema)`, so zod also validates each key: the key issues come first, then the value issues, both at path `[…, key]`.
- The turn verifier and writer quote git's stderr as `<label>: <detail>`, not `<label> failed: <detail>`; its temporary bare repository is `faultline-turn-proof-pack-*`. `maskGitOutput` gains the labels `Turn tree pack is not a valid Git pack`, `Could not create temporary bare repository for pack verification`, `Could not enumerate objects for turn tree <oid>` and `Could not pack turn trees`, and the second `<GITTMP>` marker.
- The turn verifier is an `async` function, so V8's stack under its zod `.message` reads (`SiteTurnProof`) and its `errors.push(...semantics)` spread (`SpreadTurnSemantics`) differ from the Git verifier's. They are measured by `TestLiveStackThresholdsMatchNode` and `TestLiveSpreadLimitsMatchNode`; only linux/arm64 22.22.2 has been measured, and the other platforms default to the Git verifier's values until `stack-live` measures them (KNOWN_DIFFERENCES).

### 3.5 Codex and repair

- `--allow-codex-full-auto` is checked inside the runner; without it the result is `CODEX_UNAVAILABLE` (exit 1 for `witness implement`, exit 0 for `repair`).
- `repair` uses the repository recorded in the investigation before `--repo`; `codexThreadId` is always `null` from the CLI.
- `repair brief` takes exactly one of `--live`/`--input` and exactly one of `--bundle`/`--investigation`; evidence schema v1 is refused as legacy; `--live` without `OPENAI_API_KEY` throws TS's text.
- A `PreToolUse`/`PostToolUse` for a turn that was never started fails with `Stop was observed before the matching UserPromptSubmit event.` (`turnOrdinalForStartedTurn`, `src/codex-sidecar.ts:440`): TS reuses that message for a different cause, and Go reproduces the text.
- `provenance create` requires the `GITHUB_*` environment and refuses locally; run and execution ids are sorted with `localeCompare`.

### 3.6 Expected KNOWN_DIFFERENCES entries

Added as confirmed: the commit-id mask (Section 2.2); the `<STAGEKEY>` and `<LOCKAGE>` masks (Section 2.1); the seeded lock wait costing about 17 s of real time on both sides (not an output difference); the turn pack bytes following git and zlib, SHA-256 turn bundles and the bridge (TS behaviour reproduced), the turn stack and spread sites with their measured platforms; `captureAllowlistedEvent` and `prevention-from-turn` not ported; batch staging deferred past parity.

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
   - PR 4a: `TestEndToEndSlice4aWithoutNode`.
4. Every turn bundle, attestation, provenance receipt, repair brief and prevention package Go writes verifies under TS.
5. PR 4b: the hostile-verifier turn corpus gives identical verdicts and error text in Go and TS.
6. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference.
7. The overview is updated: slice 4 complete; PR #195's batch staging moved to a post-parity performance item; `captureAllowlistedEvent` and `prevention-from-turn` recorded as not ported.
8. Slice 3b's carried items are folded in: `ArtifactOptions.RepairBaseTree` and `HardGuardArtifactDigests`, and the `incident.DraftInput` cleanup (PR 4c).

Adversarial review rounds run per PR, time-boxed to about 90 minutes each, until a round finds no P0 or P1.
