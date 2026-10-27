# Go Slice 4a (recording) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs `record init|checkpoint|attach|stdin|verify` (and the `codex record` alias), `codex sidecar config|install|status|hook`, `codex snapshot gc`, `codex --dry-run` and `codex --snapshot`. For each, stdout, stderr, exit status, the files it writes (with their modes) and the git argv are byte-identical to frozen TS `node dist/cli.js`. Writer goldens and live Node-oracle tests prove it.

**Architecture:** Each TS module becomes one Go package:
- `internal/ledger`: the `src/ledger.ts` writers (create, append, sign, clean-checkpoint capture, atomic write, the append lock, file verification). The schemas and the verifier stay in `internal/bundle/gitproof`, which exports what the writers need.
- `internal/sidecar`: the `src/codex-sidecar.ts` hook state machine, stop receipts, the sidecar lock, status inspection and `resolveLatestSidecarLedgerPath`. The storage lookup moves here from `internal/projinit/sidecar.go`.
- `internal/snapgc`: `src/snapshot-gc.ts` and `src/git-snapshot.ts`.
- `internal/cli/{record,codex,codex_snapshot,codex_sidecar,stdin}.go`: the commands.

The slice 3 writer replay is extended, not replaced. It gets:
- a per-invocation stdin field (`input`);
- a per-invocation clock (`advanceClock`);
- mutations applied before a later invocation (`inv`);
- two new mutation ops (`add-symlink`, `set-mtime`);
- two masks (`<STAGEKEY>`, `<LOCKAGE>`);
- a relocation walk into `.git/faultline/recordings`;
- per-task writer files (`writers.d/`), so the CLI tasks can run in parallel;
- a generator of Codex hook-event sequences for the live tests.

**Tech Stack:** Go 1.27 (standard library only), TypeScript generators run with the repo's `tsx`, Node 22.22.2, and git. No Docker and no `codex` binary are involved: the sidecar receives hook events as JSON.

**Spec:** [docs/superpowers/specs/2026-10-27-go-slice4-turns-codex-design.md](../specs/2026-10-27-go-slice4-turns-codex-design.md) (PR 4a scope; Sections 2 and 3 apply). Read it before any task, together with the [slice 3b plan](2026-10-26-go-slice3b-incident-prevention.md) and the [slice 3a plan](2026-10-20-go-slice3a-runtime-doctor-init.md). This plan reuses the slice 1–3 packages and helpers by name. PR 4b's plan consumes the `internal/ledger` and `internal/sidecar` Produces blocks below; they are complete on purpose.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- **JSON.**
  - Never use `encoding/json` on a value that is hashed, compared canonically, written or printed.
  - `JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`.
  - `canonicalJson` is `canonical.CanonicalJSON`, `digestJson` is `canonical.DigestJSON` and `sha256(text)` is `canonical.SHA256Hex`.
  - Ledgers and stop receipts are written as `JSON.stringify(…, null, 2) + "\n"` of the zod-parsed value. zod output follows the schema's shape order, not the input's, and so does `schema.Parse`.
  - Stdout is `JSON.stringify(…, null, 2)` in the TS object-literal order, with `undefined` members left out.
- **Frozen TS.**
  - `src/`, `package.json` and `pnpm-lock.yaml` are never modified.
  - The oracle is `node dist/cli.js` built from the current `src/`, which equals `ce99a79` (`v0.1.10-buildweek`), on Node 22.22.2.
  - Final goldens and scenario trees come from podman `localhost/r73img` (Node 22.22.2, `/usr/bin/timeout`, `tsx` under `/src/node_modules`) running as uid 1000. Running as root changes the results of permission-dependent cases. Command (from the worktree root; it works on a copy so the bind mount stays read-only):

    ```bash
    podman run --rm --user 1000:1000 -v "$PWD":/w:ro -v "$PWD/difftest/testdata":/out localhost/r73img bash -c '
      cp -a /w /tmp/r && cd /tmp/r && ln -s /src/node_modules node_modules && pnpm build >/dev/null &&
      pnpm exec tsx difftest/gen/scenarios.ts >/dev/null &&
      FAULTLINE_WRITERS_ONLY="${ONLY:-.}" pnpm exec tsx difftest/gen/gen-writers.ts &&
      cp -a difftest/testdata/. /out/'
    ```

    Pass `-e ONLY='^(record-|codex-record-)'` (or another regex) to regenerate one task's cases. The bind-mounted `/out` must be writable by uid 1000: run `chmod -R a+rwX difftest/testdata` first, then `git diff --stat` afterwards and restore modes with `git checkout -- .` on anything you did not mean to change.
- **Strings.**
  - JS strings are WTF-8 (`internal/jsstr`); TS `slice`/`length` count UTF-16 units.
  - `localeCompare` is `canonical.SortLocale` or `canonical.LocaleCompare`. A bare `.sort()` or a string `>` is `jsstr.CompareUTF16`.
  - JS regexes are `jsre.MustCompile(source, flags)`.
  - `.trim()` is `jsstr.Trim`; `toString("utf8")` is `jsstr.DecodeUTF8`.
  - Every ported function carries a comment naming its TS source.
- **Numbers.** `Number(x)` is `jsjson.ToNumber`. `Number.isInteger(n)` is `!IsInf && !IsNaN && n == math.Trunc(n)`. `-0` survives (`Number("-0")`): it prints and canonicalizes as `0`.
- **Time and child processes.**
  - Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock`. `Date.now()` is `clock.NowMillis`, `new Date()` is `clock.Now`. `TestNoDirectClockReads` enforces this.
  - Real waits (`Atomics.wait`) are `time.Sleep`; they are not clock reads.
  - Child processes start only through `nodeproc.SpawnSyncWith`, matching the TS `spawnSync` call site (env, timeout, `windowsHide`).
- **Commits.**
  - `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`. Never plain `gh`.
  - End each commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
  - TruffleHog pre-commit hook: never bypass it.
- **Output.** `Next: pnpm fl help` and the TS `next` strings stay verbatim.
- **Git's own diagnostics in goldens.**
  - `difftest-goldens` regenerates on ubuntu-24.04 (git 2.43), and podman `r73img` has git 2.39.5. So a golden may quote only git messages whose text is the same in both.
  - Those already in goldens qualify: `fatal: not a git repository (or any of the parent directories): .git`, `fatal: ambiguous argument 'HEAD': …`, `fatal: Needed a single revision`.
  - The new prefixes `Git checkpoint capture failed for`, `Git sidecar capture failed for` and `FaultLine could not safely inspect this Git worktree` are not in `GIT_LABELS`. Use failures whose text is stable rather than widening the mask, which would move the slice 3 `init-cli-*` goldens.
- **Shell commands.** Each must finish in under 5 minutes. Use `timeout 280 … > log; tail` (macOS has no `timeout`: run long commands in the podman image, or use `perl -e 'alarm 280; exec @ARGV' …`). The full TestWriterReplay takes over 40 minutes, so run filtered subsets locally. CI runs the full suite.
- **Parallel tasks.** Each task touches only the files in its **Files** block. Shared files are touched only in Task 1 (the harness and its wiring) and Task 9, except `.github/scripts/windows-writer-skips.txt`, to which a task only appends lines.

## Corrections to the spec found while planning

Fold these into the spec in Task 9.

1. **`<PID>` mask.**
   - No lock name holds a process id: `<ledger>.lock` and `<ledger>.sidecar.lock` are fixed.
   - The two temporary names do. The ledger's is `.<base>.<pid>.<uuid>.tmp`, which the slice 2 mask already writes `.<pid>.<uuid>.tmp`; that mask is kept as is, lower case.
   - The sidecar receipt stage is `.<sha256(receiptPath)[0:24]>.<pid>.tmp` (`codex-sidecar.ts:529`). Its key hashes the absolute receipt path, so it differs between the TS and the Go case directory as well. The new mask writes it `.<STAGEKEY>.<pid>.tmp`, and a sidecar unit test pins the key (spec 2.1).
2. **The seeded clock restarts in every process.** Each `fl` invocation starts at the seed epoch with UUID counter 0. So a second `record` or hook invocation in the same case would write an `occurredAt` before the first one's ("occurs before the preceding ledger timestamp"), and `record` would repeat `event-<uuid>` ids.
   - Writer templates gain `advanceClock: true`. Invocation `n ≥ 1` then runs with seed `<id>/<n>` and `FAULTLINE_TEST_CLOCK_OFFSET_MS = n × 3,600,000`, which `seed.mjs` and `internal/clock` add to the epoch.
   - Scenario ledgers are built by the TS ledger functions with fixed timestamps before the epoch, not by running `fl` (spec 2.1).
3. **The 2 s sidecar lock wait runs on the seeded clock.**
   - `withSidecarLock` reads `Date.now()` three times per 25 ms retry: in `clearStaleSidecarLock`, in the deadline check and in the wait argument. Under the seed, each read advances one millisecond, so a held fresh lock is reported after about 667 retries, about 17 s of real time, on both sides.
   - A fresh planted lock is never stale, because its real mtime is after the seed epoch. One planted with `set-mtime` 2000-01-01 always is.
   - `lock.ageMilliseconds` is therefore `0` for a fresh lock. It is masked anyway (`<LOCKAGE>`), and `lock.state` stays compared (spec 2.1).
4. **Two checkpoint runners.**
   - `fl record` uses `defaultHardenedGitRunner`: a 60 s `spawnSync` timeout and the error `Git checkpoint capture failed for <args>: …` (`ledger.ts:274`).
   - The sidecar's Stop uses its own `runHardenedGit`: no timeout, and the error `FaultLine could not safely inspect this Git worktree: …` (`codex-sidecar.ts:891`).
   - `nodeproc.SpawnSyncOptions` gains `Timeout`, which Node reports as `spawnSync git ETIMEDOUT`, status null (spec 3.2).
5. **Capture comes before the lock.**
   - `record checkpoint`, `record attach --checkpoint` and the `record stdin` checkpoint lines capture the checkpoint while the arguments are evaluated, before `appendLifecycleEventAtomic` takes its lock.
   - `appendLifecycleEventAtomic`'s `mkdirSync(dirname, {recursive: true})` runs before the lock and the read, so a failed append still leaves the directories behind (spec 3.3).
6. **Relocation must enter `.git`.** The sidecar ledger and receipts live in `.git/faultline/recordings/`. They hold the case path (`workingDirectory`, `repositoryRoot`) and digests over it. So the writer relocation walk, which skips every `.git`, must walk that one subtree on both sides (spec 2.3).
7. **Fan-out layout of the writer cases.**
   - Templates may also live in `difftest/testdata/writers.d/*.json` and goldens in `difftest/testdata/golden/writers.d/*.jsonl`, one file per task.
   - `.github/scripts/windows-writer-skips.txt` is marked `merge=union`.
   - Parallel tasks therefore never edit the same lines.
8. **`codex snapshot gc` works on `resolve(--repo)`.** It uses neither the Git top level nor the common directory: the object store is the literal `<repo>/.git/faultline/objects` (`turn-snapshot.ts:194`). In a linked worktree (`.git` is a file) it reports `ALREADY_EMPTY`, and from a subdirectory it finds nothing.
9. **`codex --snapshot` writes under `<--repo>/.faultline/recordings`**, not under the top level. Its git runs unhardened with the full environment and no `GIT_OPTIONAL_LOCKS`, so its `status` may refresh `.git/index`. The existing index rule compares the index by content.
10. **The 4 MB stdin limit counts the decoded text.** `readStandardInput` (`cli-app.ts:287`) checks `Buffer.byteLength(body)` of the UTF-8-decoded text, so each invalid byte counts 3 (U+FFFD). Its text, `Lifecycle stdin exceeds the 4 MB safety limit.`, is also what `codex sidecar hook` prints.
11. **One TS message is reused for a different cause.** A `PreToolUse`/`PostToolUse` for a turn that was never started fails with `Stop was observed before the matching UserPromptSubmit event.` (`turnOrdinalForStartedTurn`, `codex-sidecar.ts:440`). Go reproduces the text.

## File Structure

```
internal/clock/seeded.go                 + FAULTLINE_TEST_CLOCK_OFFSET_MS (test builds only)            Task 1
internal/nodeproc/spawn.go               + SpawnSyncOptions.Timeout, WindowsShow                         Task 2
internal/nodefs/atomicwrite.go           AtomicWriteFile (from internal/ledgerbind), OpenExclusive, Close, Unlink   Task 2
internal/nodefs/dirents.go               ReadDirents (readdirSync withFileTypes), MtimeMs              Task 2
internal/ledgerbind/ledgerbind.go        atomicWrite → nodefs.AtomicWriteFile                           Task 2
internal/bundle/gitproof/exports.go      exported schemas and hashes for the writers                    Task 2
internal/cli/{cli.go,stdin.go,codex.go}  dispatch of record and codex; readStandardInput; codex router  Task 3
internal/cli/{record,codex_snapshot,codex_sidecar}.go   stubs (Task 3), then Tasks 7, 5, 8
internal/ledger/ledger.go                src/ledger.ts:190-372, 599-711 writers                         Task 4
internal/ledger/checkpoint.go            src/ledger.ts:238-316 sign and clean-checkpoint capture        Task 4
internal/snapgc/gc.go                    src/snapshot-gc.ts                                             Task 5
internal/snapgc/gitsnapshot.go           src/git-snapshot.ts                                            Task 5
internal/sidecar/storage.go              codex-sidecar.ts:207-337 (from internal/projinit/sidecar.go, plus create)   Task 6
internal/sidecar/hook.go                 codex-sidecar.ts:39-990 schemas, lock, receipts, state machine Task 6
internal/sidecar/inspect.go              codex-sidecar.ts:992-1170 status inspection, latest ledger     Task 6
internal/projinit/sidecar.go             storage helpers removed; CodexSidecarLedgerPath forwards       Task 6
difftest/gen/gen-writers.ts, difftest/writer_test.go      input, advanceClock, beforeInv, writers.d, masks, relocation   Task 1
difftest/gen/cases.ts, difftest/cases.go                  add-symlink and set-mtime ops; <STAGEKEY> and <LOCKAGE> masks   Task 1
difftest/gen/scenarios.ts, difftest/scenarios_test.go     slice 4a scenarios (record, sidecar, snapgc), runCli input       Task 1
difftest/gen/hooks.go, hooks_test.go     generated Codex hook-event sequences                           Task 1
difftest/gen/oracle-{ledger,snapgc,sidecar}.ts            oracle ops; created empty and wired by Task 1, filled by 4, 5, 6
difftest/testdata/writers.d/4a-*.json, golden/writers.d/4a-*.jsonl   one pair per task (harness, record, codex, sidecar)
difftest/ledger_writers_live_test.go, snapgc_live_test.go, sidecar_live_test.go   Tasks 4, 5, 6
```

## Execution waves

Tasks in one wave share no files and can run in parallel. A wave starts when the tasks it consumes are merged.

- **Wave 1 (parallel):** Task 1 (harness), Task 2 (primitives), Task 3 (CLI scaffolding).
- **Wave 2 (parallel):** Task 4 (ledger writers; needs 1, 2), Task 5 (snapshot GC, Git sidecar snapshot and `fl codex` without `sidecar`; needs 1, 2, 3).
- **Wave 3 (parallel):** Task 6 (sidecar state machine; needs 1, 2, 4), Task 7 (`fl record`; needs 1, 3, 4).
- **Wave 4:** Task 8 (`fl codex sidecar`; needs 1, 3, 6).
- **Wave 5:** Task 9 (faults, end to end, CI, coverage gate, KNOWN_DIFFERENCES, docs, full regeneration; needs all).

The golden-touching tasks (5, 7, 8) each own one `writers.d` pair and append only their own lines to `windows-writer-skips.txt`, which is `merge=union`. So a podman regeneration with `ONLY=<their prefix>` changes nothing outside their files. `difftest/coverage_test.go` is edited by Tasks 7, 8 and 9 only, which run in different waves.

---

### Task 1: Writer harness for slice 4a

**Files:**
- Modify (TS side): `difftest/gen/gen-writers.ts`, `difftest/gen/cases.ts`, `difftest/gen/scenarios.ts`, `difftest/gen/seed.mjs`, `difftest/gen/node-oracle.ts` (imports and spreads the three new op modules; nothing else)
- Modify (Go side): `difftest/writer_test.go`, `difftest/cases.go`, `difftest/cases_test.go`, `difftest/scenarios_test.go`, `internal/clock/seeded.go`, `internal/clock/seeded_test.go`, `.gitattributes`
- Create: `difftest/gen/hooks.go`, `difftest/gen/hooks_test.go`, `difftest/gen/oracle-ledger.ts`, `difftest/gen/oracle-snapgc.ts`, `difftest/gen/oracle-sidecar.ts` (each `export const ops: Record<string, Handler> = {};`), `difftest/testdata/writers.d/4a-harness.json`, `difftest/testdata/golden/writers.d/4a-harness.jsonl`, the slice 4a scenario trees under `difftest/testdata/scenarios/`

**Interfaces:**
- Consumes: the slice 3 writer replay (`runCase`/`runWriterCaseWith`, `relocation`, `normalizeWriter`, `applyMutation`, `copyScenario`); `ledger-seed.ts`'s ledger helpers; frozen TS `createCodexLifecycleLedger`, `appendLifecycleEvent`, `signGitCheckpoint`, `captureTurnTreeSnapshot`.
- Produces:
  - Writer template keys (both loaders, both `TEMPLATE_KEYS` lists), all optional:
    - `input: (string | null)[]`, one entry per invocation. A string is the invocation's stdin, its UTF-8 bytes. `null` or a missing entry is an empty, closed stdin, which is what both sides give today.
    - `inputHex: (string | null)[]`: the same with hex-encoded bytes, for invalid UTF-8. A template has at most one of `input` and `inputHex`.
    - In `input` strings, `<WS>`, `<TMP>` and `<HOME>` are substituted as in argv (hook documents carry `"cwd": "<WS>"`). `<REPEAT:c:n>` expands to the single character `c` repeated `n` times (`n` ≤ 8,388,608), for the oversized prompt. The same two rules apply to `add-file` mutation `content`.
    - `advanceClock: true`: invocation `n ≥ 1` runs with `FAULTLINE_TEST_SEED=<id>/<n>` and `FAULTLINE_TEST_CLOCK_OFFSET_MS=<n × 3600000>`. Invocation 0 keeps seed `<id>` and no offset, so existing cases are unchanged.
  - A mutation key `inv: n`: the mutation is applied just before invocation `n`, not at case setup. The default is setup. Non-chmod mutations still precede `resetModes`, and only setup mutations are reset.
  - Mutation ops on both sides:
    - `{"op":"add-symlink","path":p,"target":t}` makes `p` a symlink to `t`; `p` may be missing.
    - `{"op":"set-mtime","path":p,"iso":"2000-01-01T00:00:00.000Z"}` sets atime and mtime with `lutimes` semantics, so it works on a symlink itself.
  - Masks in `normalizeWriter` (both sides, after the `.<pid>.<uuid>.tmp` mask):
    - `<STAGEKEY>`: `/\.([0-9a-f]{24})\.([0-9]+)\.tmp/` becomes `.<STAGEKEY>.<pid>.tmp`.
    - `<LOCKAGE>`: `"ageMilliseconds": <digits>` becomes `"ageMilliseconds": "<LOCKAGE>"`.
  - Relocation (both sides) walks `.git/faultline/recordings/` as well, and only that subtree of any `.git`.
  - Templates load from `writers.json` and then each `writers.d/*.json` in byte order. Goldens load from `golden/writers.jsonl` and `golden/writers.d/*.jsonl`. `gen-writers.ts` writes each template's lines to the golden file named after its template file. A duplicate id across files fails both loaders.
  - `seed.mjs` and `internal/clock` add `FAULTLINE_TEST_CLOCK_OFFSET_MS` (an integer; anything else fails the process at load with `bad FAULTLINE_TEST_CLOCK_OFFSET_MS`) to the epoch.
  - Scenarios (built by `scenarios.ts` with frozen TS functions and fixed timestamps; listed in `writerScenarios`):
    - `record-repo`: a clean one-commit repository at `repo/` with an index. Files: `state.txt` (`good\n`), `.gitignore` (`.faultline/\n`). Plus `ledger.json`, a valid `SIDE_CAR` ledger for session `s-1`: `SESSION_STARTED` with `workingDirectory` = the literal `/work/repo`, then turn 1 started and completed.
    - `record-ledgers`: `record-repo` plus these fixtures:
      - `ended.json` (a `SESSION_ENDED` ledger);
      - `active.json` (turn 2 started, not completed);
      - `tampered.json` (a payload edited after signing);
      - `bad-schema.json` (a `sequence` of `0`);
      - `not-json.json` (`{`);
      - `events/` holding NDJSON stdin bodies (see Task 7).
    - `record-dirty`: `record-repo` plus an untracked `scratch.txt`, and `state.txt` modified.
    - `record-fsmonitor`: `record-repo` with `core.fsmonitor` set to a script that writes `FSMONITOR_RAN` (`tests/fl-record-checkpoint-fsmonitor.test.ts:28`).
    - `sidecar-repo`: a clean one-commit repository at the workspace root, with `.gitignore` (`.faultline/\n`) and a matching index. The hook's `cwd` is the workspace itself.
    - `sidecar-dirty`: `sidecar-repo` plus an untracked `notes.txt`.
    - `sidecar-no-head`: `git init` with no commit.
    - `sidecar-worktree`: the `init-worktree` layout (a linked worktree `wt/` of `main/`).
    - `sidecar-recorded`: `sidecar-repo` plus, under `.git/faultline/recordings/`, one complete recorded session `s-1`: SessionStart, turn `t-1` with Stop (checkpoint and turn snapshot) and its receipt. It is written by running the TS hook state machine (`recordObservedCodexHook`) under the scenario seed. The scenario `.gitignore` keeps it out of status.
    - `snapgc-repo`: `sidecar-repo` plus a slow-path capture's quarantine store (`.git/faultline/objects/xx/…`), the alternates entry, and a `.faultline/recordings/codex-x.json` that mentions one quarantined object id.
- `difftest/gen/hooks.go` (package `gen`):

```go
// HookEvent is one Codex hook document (codex-sidecar.ts:68-113): Fields
// in the order they are written; Raw, when set, replaces the document (a
// malformed or oversized input).
type HookEvent struct {
	Fields [][2]any // {key, value}; value is string, float64, bool, nil or json.RawMessage
	Raw    []byte
}

// HookStep is one hook invocation of a sequence, with the workspace change
// applied before it.
type HookStep struct {
	Event HookEvent
	Op    HookOp
}

// HookOp changes the workspace before a step.
type HookOp struct {
	Kind string // "", "dirty", "clean", "commit", "plant-fresh-lock", "plant-stale-lock", "plant-receipt", "plant-stage", "symlink-storage", "remove-receipt", "truncate-ledger"
	Arg  string
}

// HookSequence returns the i-th generated sequence for session s: a
// SessionStart, then up to 4 turns of UserPromptSubmit, Pre/PostToolUse
// pairs and Stop, with redeliveries (any step repeated, also out of
// order), wrong session ids, a changed model or cwd, a missing turn_id,
// unknown extra fields (transcript_path, tool_input) that must be
// stripped, a 4 MiB+1 prompt, and HookOps between steps. Deterministic in
// (i, s).
func HookSequence(r *rand.Rand, i int, s string) []HookStep
```

**Gotchas:**
- **Stdin.**
  - Go writes the bytes to `cmd.Stdin` and closes it. TS's `runCli` spawns with `stdio: ["pipe", …]`, calls `child.stdin.end(bytes)` and swallows `EPIPE` on stdin, because a command may exit without reading it.
  - Today's cases inherit a closed stdin on both sides: check what `runCli` and `exec.Cmd` give now (TS `spawn` default `pipe`, never written, so the child sees EOF only after the parent closes it; Go `nil` is `/dev/null`). Make both explicit: an empty pipe that is closed at once. Regenerate all goldens once (Task 1 Step 4) and confirm no existing line changes.
- **advanceClock.**
  - The offset is added to `EPOCH` in `seed.mjs` before the first `Date` read.
  - In `internal/clock/seeded.go` it is read once in `load()` with the seed. `ResetForTest` sets it to 0.
  - Release builds (`release.go`) never read it, and `TestNoDirectClockReads` stays green.
- **Scenario ledgers.**
  - They are built in `scenarios.ts` by calling `createCodexLifecycleLedger`/`appendLifecycleEvent` with explicit `ledgerId`, `eventId` and `occurredAt` (`2026-07-16T11:00:00.000Z` plus one second per event, an hour before the seed epoch), so later appends by `fl` are in order.
  - `workingDirectory` is a fixed string, not the case path, so the scenario bytes are path-independent.
- **`sidecar-recorded`** is the one scenario that holds case-path-dependent bytes (the hook's `cwd`). Build it inside the scenario's own temporary workspace, then copy it under `difftest/testdata/scenarios/`. A writer case that reads it sees a `workingDirectory` that differs from its own `cwd` and gets the "does not match this Codex session's cwd" refusal. That is the case `sidecar-hook-moved-repo` (Task 8). Fresh recordings for the other sidecar cases are made by earlier invocations of the case itself.
- **Relocation into `.git`.** The walk enters `<any>/.git/faultline/recordings` only; `.git/index`, objects and the turn-snapshot cache keep their existing rules.
- **`writers.d`.**
  - `TestWindowsWriterSkipsList` reads the union of the loaders.
  - `.gitattributes` gains `.github/scripts/windows-writer-skips.txt merge=union`.
  - `FAULTLINE_WRITERS_ONLY` keeps the lines of non-matching cases from whichever golden file holds them.
- **Oracle op modules.** `node-oracle.ts` imports `ops` from the three new files and spreads them into `handlers` after its own entries. Tasks 4, 5 and 6 then edit only their own file, so they never conflict.
- **Not in 4a.** Spec 2.2's `<COMMIT>` mask, the fake `codex`, the fake `gh` and the fake OpenAI belong to PRs 4b and 4c, whose plans add them. No 4a command writes a commit or starts those tools.
- **Hook generator.** Keys must be emitted in the given order (Codex sends them in its own order, and zod's output order follows the schema anyway). Use a hand-written JSON writer over `Fields`; `encoding/json` is fine here because the documents are inputs, never hashed or compared.

**Harness self-test cases** (`writers.d/4a-harness.json`; these use only commands Go already ports):
- `harness-stdin-ignored`: `witness propose` with `input: ["{}\n"]`, which proves that a command that never reads stdin is unaffected.
- `harness-advance-clock` (scenario `witness-proposed`, which holds `proposal.json`): `witness propose --input proposal.json --store s0`, then the same with `--store s1`, with `advanceClock`, so the second `proposedAt` is an hour later and its UUIDs come from seed `<id>/1`.
- `harness-inv-mutation`: an `add-symlink` with `inv: 1` between two invocations.
- `harness-set-mtime`: `set-mtime` on a file before `verify`, so the tree digest is unchanged and the mutation op is exercised.

- [ ] **Step 1: Write the failing tests**

```go
// difftest/cases_test.go
func TestSlice4aMasks(t *testing.T) {
	r := writerRoots{ws: "/c/ws", tmp: "/c/tmp", home: "/c/home"}
	in := "rename '/c/ws/.git/faultline/recordings/.0123456789abcdef01234567.4242.tmp' -> 'x'\n" +
		`"lock": {"state": "ACTIVE", "ageMilliseconds": 17}`
	want := "rename '<WS>/.git/faultline/recordings/.<STAGEKEY>.<pid>.tmp' -> 'x'\n" +
		`"lock": {"state": "ACTIVE", "ageMilliseconds": "<LOCKAGE>"}`
	if got := normalizeWriter(in, r); got != want {
		t.Fatalf("%q", got)
	}
}

func TestWriterTemplateInputs(t *testing.T) {
	repo := repoRoot(t)
	for _, tpl := range loadWriterTemplates(t, repo) {
		if len(tpl.Input) > len(tpl.Argv) {
			t.Errorf("%s: %d inputs for %d invocations", tpl.ID, len(tpl.Input), len(tpl.Argv))
		}
	}
}
```

```go
// internal/clock/seeded_test.go
func TestClockOffset(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_SEED", "offset")
	t.Setenv("FAULTLINE_TEST_CLOCK_OFFSET_MS", "3600000")
	state.init = false
	if got := NowMillis(); got != epochMillis+3600000 {
		t.Fatalf("%d", got)
	}
	ResetForTest("x")
	if got := NowMillis(); got != epochMillis {
		t.Fatalf("reset keeps the offset: %d", got)
	}
}
```

```go
// difftest/gen/hooks_test.go
func TestHookSequenceDeterministicAndStarts(t *testing.T) {
	a := HookSequence(rand.New(rand.NewPCG(1, 2)), 7, "s-1")
	b := HookSequence(rand.New(rand.NewPCG(1, 2)), 7, "s-1")
	if !reflect.DeepEqual(a, b) || len(a) == 0 {
		t.Fatal("not deterministic")
	}
	if name, _ := a[0].Event.Fields[0][1].(string); a[0].Event.Raw == nil && name != "SessionStart" {
		t.Fatalf("first step %v", a[0].Event.Fields)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ ./internal/clock/ ./difftest/gen/ -run 'TestSlice4aMasks|TestWriterTemplateInputs|TestClockOffset|TestHookSequence'`
Expected: FAIL (undefined `tpl.Input`, `HookSequence`; unmasked text; no offset).

- [ ] **Step 3: Implement** both loaders, the masks, the ops, the offset, the relocation subtree, `writers.d`, the scenarios and the generator. Every Go twin names its TS twin in a comment, as the slice 3 harness does.

- [ ] **Step 4: Regenerate in podman and check no existing golden moved**

Run (podman command in Global Constraints, full regeneration): `git diff --stat difftest/testdata/golden/writers.jsonl`
Expected: no change to `writers.jsonl`; new files only under `writers.d/` and `scenarios/`.

- [ ] **Step 5: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/clock/ ./difftest/gen/ && go test -tags faultline_test ./difftest/ -run 'TestSlice4aMasks|TestWriterTemplateInputs|TestClockOffset|TestWriterReplay/(posix/)?harness-|TestWindowsWriterSkipsList|TestNoDirectClockReads|TestNormalizeWriter' -v > /tmp/s4a-t1.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4a-t1.log | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add difftest internal/clock .gitattributes
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(go): slice 4a writer harness (stdin, per-invocation clock, writers.d, masks, hook events)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Primitives: spawnSync timeout, atomic write, exclusive open, dirents, gitproof exports

**Files:**
- Modify: `internal/nodeproc/spawn.go`, `internal/nodeproc/spawn_test.go`, `internal/nodefs/atomic.go`, `internal/ledgerbind/ledgerbind.go`, `internal/bundle/gitproof/ledger.go`, `internal/bundle/gitproof/ledgerio.go`
- Create: `internal/nodefs/atomicwrite.go`, `internal/nodefs/atomicwrite_test.go`, `internal/nodefs/dirents.go`, `internal/nodefs/dirents_test.go`, `internal/bundle/gitproof/exports.go`, `internal/bundle/gitproof/exports_test.go`

**Interfaces:**
- Consumes: `nodefs` internals (`sysPath`, `wrap`, `openWriteErr`, `retryEINTR`, `closeFile`, `unlinkRaw`, `scandir`, `statRaw`), `clock.NewUUID`.
- Produces:

```go
// internal/nodeproc
type SpawnSyncOptions struct {
	Env       []string
	Input     []byte
	HasInput  bool
	Cwd       string
	MaxBuffer int
	// Timeout is spawnSync's timeout option; 0 is none. When it fires the
	// child gets SIGTERM (killSignal's default), Status is nil and Err is
	// "spawnSync <file> ETIMEDOUT"; the output read so far is kept.
	Timeout time.Duration
	// WindowsShow is windowsHide: false, spawnSync's default, which
	// src/git-snapshot.ts:18 keeps (no effect off Windows).
	WindowsShow bool
}

// internal/nodefs
// AtomicWriteFile ports the staged write of src/ledger.ts:599 (and
// src/ledger-binding.ts:536, the same code): mkdirSync(dir, {recursive}),
// a ".<base>.<pid>.<uuid>.tmp" stage opened "wx" 0600, the text written as
// UTF-8, fsync, close, renameSync onto filePath, a best-effort directory
// fsync, and in `finally` the stage removed (rmSync force) when it still
// exists. It returns resolve(filePath).
func AtomicWriteFile(filePath, text string) (string, error)

// OpenExclusive is openSync(path, "wx", mode): the descriptor, or the open
// error as Node prints it ("EEXIST: file already exists, open '<path>'").
func OpenExclusive(path string, mode fs.FileMode) (*os.File, error)

// Close is closeSync(fd); a failure is "<CODE>: <description>, close".
func Close(f *os.File) error

// Unlink is unlinkSync(path): one unlink(2), its error naming the path.
func Unlink(path string) error

// Dirent is one entry of readdirSync(path, { withFileTypes: true }).
type Dirent struct {
	Name                       string // decoded as UTF-8 with replacement
	IsFile, IsDir, IsSymlink   bool
}

// ReadDirents is readdirSync(path, { withFileTypes: true }): byte-sorted
// names (libuv scandir), types from d_type, an lstat in order for an
// unknown type (the first failure thrown), as ReadDirNames.
func ReadDirents(path string) ([]Dirent, error)

// MtimeMs is fs.Stats.mtimeMs of an lstat result: milliseconds with the
// sub-millisecond fraction Node keeps (sec*1e3 + nsec/1e6 as a float64).
func MtimeMs(info fs.FileInfo) float64
```

```go
// internal/bundle/gitproof (exports.go; the unexported names stay)
const CodexLifecycleLedgerVersion = codexLifecycleLedgerVersion // "faultline.codex-lifecycle-ledger.v1"
const GitCheckpointVersion = gitCheckpointVersion               // "faultline.git-checkpoint.v1"
var (
	LedgerIdentifierSchema         = ledgerIdentifierSchema
	CodexTransportSchema           = codexTransportSchema
	LifecycleEventInputSchema      = lifecycleEventInputSchema
	UnsignedCodexLifecycleEventSchema = unsignedCodexLifecycleEventSchema
	UnsignedGitCheckpointSchema    = unsignedGitCheckpointSchema
	GitCheckpointSchema            = gitCheckpointSchema
)
// CodexTransportOptions is CodexTransportSchema.options, in order.
var CodexTransportOptions = []string{"CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT"}
func LedgerGenesisHash(ledger jsjson.Value) string       // src/ledger.ts:223
func HashLifecycleEvent(event jsjson.Value) string        // src/ledger.ts:234 (parses; throws a ZodError via jsexc)
func VerifyGitCheckpoint(value jsjson.Value) []string    // src/ledger.ts:243
func TimestampMs(iso string) float64                      // Date.parse of a canonical timestamp (NaN otherwise)
// ErrorSummary is src/ledger.ts:328 errorSummary.
func ErrorSummary(errs []string) string
// VerifyCodexLifecycleLedgerFile ports src/ledger.ts:663: a read or schema
// failure becomes {valid:false, errors:[message], eventCount:0, headHash:null}.
func VerifyCodexLifecycleLedgerFile(filePath string) LedgerVerification
```

**Gotchas:**
- **Timeout.**
  - Node's `SyncProcessRunner` arms a uv timer. On expiry it calls `Kill()` (SIGTERM; `TerminateProcess` on Windows), closes the pipes and sets the error to `UV_ETIMEDOUT`.
  - The child's exit status is then a signal, so `status` is null. ENOBUFS, if it was already set, wins (`GetError` keeps the first error).
  - Port that order exactly and test it with `sh -c 'sleep 5'` and a 100 ms timeout.
  - Do not report the SIGTERM to `sigexit.ChildKilled`: Node treats the killed child as its own kill, which is what the existing `killed` flag already covers. Set it from the timer too.
- **AtomicWriteFile** replaces `ledgerbind.atomicWrite` with no behaviour change. Its error paths:
  - The open error names the stage path.
  - Write, fsync and close errors name no path.
  - The rename error names both paths.
  - An `rmSync` failure in `finally` replaces the result.

  The slice 2 `ledger-bind` fault cases cover it and must stay green. The process id is `os.Getpid()`.
- **MtimeMs** must equal Node's for the stale-lock arithmetic (`Date.now() - stat.mtimeMs`), so keep the nanoseconds: `float64(sec)*1e3 + float64(nsec)/1e6`. On Windows, use the file time's 100 ns resolution, as libuv does.
- **`errorSummary` bug.** The current Go `errorSummary` returns `"invalid ledger"` for an empty list. TS returns `errors.join("; ")`, which is `""` (`errors[0] ?? …` applies only to length 1). Fix it while exporting it. No caller passes an empty list today, so no golden moves.
- **gitproof exports.**
  - `HashLifecycleEvent` parses with `UnsignedCodexLifecycleEventSchema` first, as TS does. A parse failure throws a ZodError through `jsexc`, and its message is read at the caller's site.
  - `VerifyCodexLifecycleLedgerFile` uses `ReadCodexLifecycleLedger` and keeps `headHash` null on failure (`LedgerVerification.HeadHash == nil`).

- [ ] **Step 1: Write the failing tests**

```go
// internal/nodeproc/spawn_test.go
func TestSpawnSyncTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	start := time.Now()
	r := SpawnSyncWith("sh", []string{"-c", "printf partial; sleep 5"}, SpawnSyncOptions{Timeout: 200 * time.Millisecond})
	if r.Err == nil || r.Err.Error() != "spawnSync sh ETIMEDOUT" || r.Status != nil || string(r.Stdout) != "partial" {
		t.Fatalf("%+v %v", r, r.Err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the child was not killed")
	}
}
```

```go
// internal/nodefs/atomicwrite_test.go
func TestAtomicWriteFileLeavesNoStage(t *testing.T) {
	dir := t.TempDir()
	path, err := AtomicWriteFile(filepath.Join(dir, "a", "ledger.json"), "x\n")
	if err != nil || path != filepath.Join(dir, "a", "ledger.json") {
		t.Fatal(path, err)
	}
	names, _ := os.ReadDir(filepath.Join(dir, "a"))
	if len(names) != 1 {
		t.Fatalf("%v", names)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("%v", info.Mode())
	}
}

func TestOpenExclusiveText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	f, err := OpenExclusive(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer Close(f)
	if _, err := OpenExclusive(p, 0o600); err == nil || err.Error() != "EEXIST: file already exists, open '"+p+"'" {
		t.Fatalf("%v", err)
	}
}
```

```go
// internal/bundle/gitproof/exports_test.go
func TestVerifyLedgerFileUnreadable(t *testing.T) {
	v := VerifyCodexLifecycleLedgerFile(filepath.Join(t.TempDir(), "missing.json"))
	if v.Valid || v.HeadHash != nil || len(v.Errors) != 1 || !strings.HasPrefix(v.Errors[0], "Unable to read lifecycle ledger ") {
		t.Fatalf("%+v", v)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nodeproc/ ./internal/nodefs/ ./internal/bundle/gitproof/ -run 'TestSpawnSyncTimeout|TestAtomicWriteFile|TestOpenExclusive|TestVerifyLedgerFile|TestReadDirents'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**; move `ledgerbind.atomicWrite` onto `nodefs.AtomicWriteFile`. Add `TestReadDirents` (a file, a directory, a symlink, a FIFO; names byte-sorted).

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/nodeproc/ ./internal/nodefs/ ./internal/ledgerbind/ ./internal/bundle/gitproof/ && GOOS=windows go vet ./internal/nodeproc/ ./internal/nodefs/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/ledger-bind|TestNoDirectClockReads'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodeproc internal/nodefs internal/ledgerbind internal/bundle/gitproof
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): spawnSync timeout, shared atomic write, exclusive open, dirents, ledger exports

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: CLI scaffolding: `record` and `codex` dispatch, stdin, the codex router

**Files:**
- Modify: `internal/cli/cli.go`, `internal/cli/cli_test.go`
- Create: `internal/cli/stdin.go`, `internal/cli/stdin_test.go`, `internal/cli/codex.go`, `internal/cli/codex_test.go`, and three stubs that later tasks replace whole: `internal/cli/record.go` (Task 7), `internal/cli/codex_snapshot.go` (Task 5), `internal/cli/codex_sidecar.go` (Task 8)

**Interfaces:**
- Consumes: `env`, `fail`, `hasFlag`, `option`, `jsstr.DecodeUTF8`, `jsstr.ToUTF8`.
- Produces:

```go
// cli.go
type env struct {
	stdin          io.Reader // nil: os.Stdin
	stdout, stderr io.Writer
	exitCode       int
}
// RunWithStdin is Run with the process's stdin replaced (tests).
func RunWithStdin(args []string, stdin io.Reader, stdout, stderr io.Writer) int
// Run keeps its signature and reads os.Stdin.

// stdin.go: readStandardInput ports src/cli-app.ts:287.
func (e *env) readStandardInput() (string, error)

// codex.go: the `codex` case of runCli (src/cli-app.ts:2994-3028).
func codexCommand(e *env, args []string) error

// Stubs; each later task replaces its file and keeps the signature.
func recordCommand(e *env, args []string) error              // Task 7
func codexSnapshotGCCommand(e *env, gcArgs []string) error   // Task 5
func codexDryRunCommand(e *env) error                        // Task 5
func codexSnapshotCommand(e *env, args []string) error       // Task 5
func codexSidecarCommand(e *env, args []string) error        // Task 8
```

Each stub body is `return fail("not yet ported: <command>")` with `<command>` `record`, `codex snapshot gc`, `codex --dry-run`, `codex --snapshot` and `codex sidecar`.

**Gotchas:**
- **Dispatch.** `cli.go`'s switch gains `command == "codex"` → `codexCommand(e, rest)` and `command == "record"` → `recordCommand(e, rest)`. Both names stay in `topLevelCommands`.
- **The codex router order** (`cli-app.ts:2994-3028`) is exact:
  1. `args[0] == "record"` → `recordCommand(e, args[1:])`.
  2. `args[0] == "sidecar"` → `codexSidecarCommand(e, args[1:])`.
  3. `args[0] == "snapshot" && args[1] == "gc"` → `codexSnapshotGCCommand(e, args[2:])`.
  4. `hasFlag(args, "--dry-run")` (anywhere in args) → `codexDryRunCommand(e)`.
  5. `hasFlag(args, "--snapshot")` → `codexSnapshotCommand(e, args)`.
  6. Otherwise throw the fixed text `The current build exposes an observed Codex hook sidecar, quarantined turn-snapshot GC, and a clean Git snapshot. Run: fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, fl codex snapshot gc --repo <directory>, fl codex --dry-run, or fl codex --snapshot --repo <directory>`.

  So `fl codex snapshot` without `gc` falls through to the flags, `fl codex --snapshot --dry-run` is a dry run, and `fl codex sidecar --dry-run` is the sidecar's usage error.
- **readStandardInput.**
  - `process.stdin.setEncoding("utf8")` decodes with a `StringDecoder`. Bytes are joined and decoded once, and an incomplete trailing sequence becomes U+FFFD at `end`.
  - After every chunk, `Buffer.byteLength(body, "utf8") > 4_000_000` rejects with `Lifecycle stdin exceeds the 4 MB safety limit.`. That is the decoded text's UTF-8 length, so U+FFFD counts 3 bytes.
  - Because the body only grows, reading everything, decoding with `jsstr.DecodeUTF8` and checking once at the end gives the same outcome.
  - A read error rejects with Node's stream error message. The only reachable one is `EISDIR` when stdin is a directory: `EISDIR: illegal operation on a directory, read`.
  - Confirm `jsstr.DecodeUTF8` equals Node's `StringDecoder` on split and invalid sequences with the `inputHex` writer cases of Tasks 7 and 8 (`record-stdin-invalid-utf8`, `sidecar-hook-invalid-utf8`). If one differs, port `StringDecoder` here.
- **No timeout.** TS waits for EOF forever. So does Go.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/codex_test.go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

const codexUsage = "The current build exposes an observed Codex hook sidecar, quarantined turn-snapshot GC, and a clean Git snapshot. Run: fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, fl codex snapshot gc --repo <directory>, fl codex --dry-run, or fl codex --snapshot --repo <directory>"

func TestCodexRouterFallsThrough(t *testing.T) {
	for _, args := range [][]string{{"codex"}, {"codex", "snapshot"}, {"codex", "nope", "gc"}} {
		_, stderr, code := run(args...)
		if code != 1 || stderr != "FaultLine error: "+codexUsage+"\nNext: pnpm fl help\n" {
			t.Errorf("%q: %d %q", args, code, stderr)
		}
	}
}

func TestReadStandardInputLimit(t *testing.T) {
	e := &env{stdin: strings.NewReader(strings.Repeat("\xff", 1_333_334))}
	if _, err := e.readStandardInput(); err == nil || err.Error() != "Lifecycle stdin exceeds the 4 MB safety limit." {
		t.Fatalf("%v", err)
	}
	e = &env{stdin: bytes.NewReader([]byte("a\xe2\x82"))}
	if s, err := e.readStandardInput(); err != nil || s != "a�" {
		t.Fatalf("%q %v", s, err)
	}
}
```

(1,333,334 bytes of `0xFF` decode to 1,333,334 U+FFFD, which are 4,000,002 UTF-8 bytes: just over the limit. 1,333,333 would be 3,999,999 and must pass; add that case too.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cli/ -run 'TestCodexRouter|TestReadStandardInput'`
Expected: FAIL (`not yet ported: codex`; undefined `readStandardInput`).

- [ ] **Step 3: Implement** the dispatch, `RunWithStdin`, `readStandardInput`, the router and the stubs.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/cli/ && go vet ./... && GOOS=windows go vet ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): dispatch fl record and fl codex; stdin reader; codex router

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Ledger writers (`internal/ledger`)

**Files:**
- Create: `internal/ledger/ledger.go`, `internal/ledger/checkpoint.go`, `internal/ledger/ledger_test.go`, `internal/ledger/checkpoint_test.go`, `difftest/ledger_writers_live_test.go`
- Modify: `difftest/gen/oracle-ledger.ts` (its `ops` only)

**Interfaces:**
- Consumes: Task 2's `nodefs.AtomicWriteFile`, `nodefs.OpenExclusive`, `nodefs.Close`, `nodefs.Unlink`, `nodeproc.SpawnSyncOptions.Timeout`, and the gitproof exports; `gitproof.CodexLifecycleLedgerSchema`, `gitproof.VerifyCodexLifecycleLedger`, `gitproof.ReadVerifiedCodexLifecycleLedger`, `gitproof.CanonicalTimestamp`; `safegit.HostCommand()`; `gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})`; `clock.{Now,ISO,NewUUID}`; `canonical.DigestJSON`; `bundle.ParseValue`.
- Produces (PR 4b and Task 6 rely on every one of these):

```go
package ledger // Port of src/ledger.ts writers; the verifier is gitproof.

// IntegrityError ports LedgerIntegrityError (src/ledger.ts:215). Its
// Message is the TS message; callers test for it with errors.As.
type IntegrityError struct{ Message string }
func (e *IntegrityError) Error() string

// CreateOptions ports CreateLedgerOptions (src/ledger.ts:190). "" is
// undefined for LedgerID and CreatedAt.
type CreateOptions struct{ SessionID, LedgerID, CreatedAt string }

// Create ports createCodexLifecycleLedger (src/ledger.ts:318): reads
// clock.NewUUID() only when LedgerID is "", then clock.Now() only when
// CreatedAt is "", in that order, and parses with
// gitproof.CodexLifecycleLedgerSchema (a ZodError on failure).
func Create(o CreateOptions) (jsjson.Value, error)

// AppendOptions ports AppendLifecycleEventOptions (src/ledger.ts:196).
type AppendOptions struct{ EventID, OccurredAt string } // "" is undefined

// Append ports appendLifecycleEvent (src/ledger.ts:345): it returns the new
// ledger value and never mutates its input.
func Append(ledger, input jsjson.Value, o AppendOptions) (jsjson.Value, error)

// WriteAtomic ports writeCodexLifecycleLedgerAtomic (src/ledger.ts:632):
// ensureAppendableLedger, then nodefs.AtomicWriteFile with
// JSON.stringify(parsed, null, 2) + "\n". It returns resolve(filePath).
func WriteAtomic(filePath string, ledger jsjson.Value) (string, error)

// AppendAtomic ports appendLifecycleEventAtomic (src/ledger.ts:689).
func AppendAtomic(filePath string, input jsjson.Value, o AppendOptions) (jsjson.Value, error)

// GitRunner ports GitCommandRunner (src/ledger.ts:201): trimmed stdout or
// an error.
type GitRunner func(repository string, args []string) (string, error)

// DefaultHardenedGitRunner ports defaultHardenedGitRunner (src/ledger.ts:274).
func DefaultHardenedGitRunner(repository string, args []string) (string, error)

// CaptureOptions ports CaptureGitCheckpointOptions (src/ledger.ts:203).
type CaptureOptions struct {
	Now    func() time.Time // nil: clock.Now
	RunGit GitRunner        // nil: DefaultHardenedGitRunner
}

// CaptureGitCleanCheckpoint ports captureGitCleanCheckpoint (src/ledger.ts:293).
func CaptureGitCleanCheckpoint(repository string, o CaptureOptions) (jsjson.Value, error)

// SignGitCheckpoint ports signGitCheckpoint (src/ledger.ts:238).
func SignGitCheckpoint(unsigned jsjson.Value) (jsjson.Value, error)

// ErrDirtyWorktree is the message isDirtyCheckpointError compares
// (codex-sidecar.ts:811).
const ErrDirtyWorktree = "Git checkpoint capture requires a clean worktree."

// Re-exported for one import path in PR 4b:
func Verify(value jsjson.Value) gitproof.LedgerVerification          // gitproof.VerifyCodexLifecycleLedger
func VerifyFile(filePath string) gitproof.LedgerVerification         // gitproof.VerifyCodexLifecycleLedgerFile
func ReadVerified(filePath string) (jsjson.Value, error)             // gitproof.ReadVerifiedCodexLifecycleLedger
```

**Gotchas:**
- **Append, in TS order** (`ledger.ts:345-372`):
  1. `ensureAppendableLedger`: parse with the ledger schema (a ZodError propagates), then verify. If invalid, `Refusing to append to an invalid ledger: <errorSummary>`.
  2. `LifecycleEventInputSchema.parse(input)`: a ZodError, read at the CLI site.
  3. `occurredAt = o.OccurredAt || clock.ISO(clock.Now())`. The clock is read only when absent, and before the event id.
  4. `CanonicalTimestampSchema.parse(occurredAt)`, a ZodError at path root: `Expected a canonical ISO-8601 UTC timestamp`.
  5. `eventId = o.EventID || "event-" + clock.NewUUID()`. It is not schema-checked here; the final parse checks it.
  6. Build the unsigned envelope in the key order `schemaVersion, ledgerId, sessionId, sequence, eventId, occurredAt, event, previousHash`. `previousHash` is the last event's `hash`, else `LedgerGenesisHash`.
  7. `hash = HashLifecycleEvent(unsigned)`. It parses the unsigned event, so a bad `eventId` surfaces here as a ZodError. Then `signed = {...unsigned, hash}`.
  8. Parse the next ledger `{...current, events: [...events, signed]}` with the ledger schema, verify it, and on failure throw `Refusing an invalid lifecycle transition: <errorSummary>`.

  Both refusals are `*IntegrityError`.
- **`event` is the parsed input.**
  - zod strips nothing here (every object is `.strict()`), but it reorders keys to the schema's order. So `event.payload` keys follow the schema (`turnId, turnOrdinal, outcome, outputDigest, contribution`), whatever order the stdin line used.
  - `schema.Parse` already does this; never re-insert the input's order.
- **AppendAtomic order** (`ledger.ts:689-711`):
  1. `absolutePath = resolve(filePath)`, `lockPath = absolutePath + ".lock"`.
  2. `mkdirSync(dirname, {recursive: true})`, through `nodefs.MkdirAll(dir, 0o777)`.
  3. `OpenExclusive(lockPath, 0o600)`. On failure: `Could not acquire lifecycle ledger append lock <lockPath>: <node message>` (`*IntegrityError`).
  4. `ReadVerified`, `Append`, `WriteAtomic`.
  5. `finally`: `closeSync(lock)` (an error replaces the result), then `unlinkSync(lockPath)` with errors ignored.

  There is no wait and no retry: a held lock fails at once.
- **Clean-checkpoint capture** (`ledger.ts:293-316`):
  1. `requestedRoot = resolve(repository)`.
  2. `repositoryRoot = resolve(run(requestedRoot, ["rev-parse", "--show-toplevel"]))`.
  3. `diff-index --ignore-cr-at-eol HEAD`, then `ls-files --others --exclude-standard`, both against `repositoryRoot`.
  4. If either output is non-empty (after trim), throw `*IntegrityError{ErrDirtyWorktree}`.
  5. `rev-parse HEAD`, `rev-parse HEAD^{tree}`.
  6. `capturedAt = ISO(now())`. This is the only clock read, after all git calls.
  7. `SignGitCheckpoint({schemaVersion, repositoryRoot, headCommit, treeDigest, capturedAt, clean: true})`.

  The runner trims each output. The error is the runner's.
- **`diff-index` without a refresh** reports a file whose index stat data is stale (the scenario copy's mtimes) as modified even when its content matches. Checked: a fresh clone then `touch` gives `:100644 100644 <blob> 0000… M`.
  - `--ignore-cr-at-eol` makes git compare content for a stat-dirty path, so a byte-identical file passes. Both sides run the same git, so the result matches whatever it is.
  - So a scenario copy whose index stat data is stale (`writeIndex` writes zero stat fields) is still clean when its content matches. Verified by hand: `diff-index HEAD` lists the touched file, and `diff-index --ignore-cr-at-eol HEAD` lists nothing.
- **DefaultHardenedGitRunner** (`ledger.ts:274-287`):
  - `SpawnSyncWith("git", [...safegit.HostCommand(), "-C", repo, ...args], {Env: hardened env, Timeout: 60*time.Second})`, with `encoding: "utf8"` (stdout and stderr via `jsstr.DecodeUTF8`).
  - The env is `hardenedCheckpointGitEnvironment`: `PATH, GIT_CONFIG_NOSYSTEM=1, GIT_CONFIG_GLOBAL=/dev/null|NUL, GIT_ATTR_NOSYSTEM=1, GIT_TERMINAL_PROMPT=0, GIT_OPTIONAL_LOCKS=0, GIT_NO_REPLACE_OBJECTS=1, GIT_LFS_SKIP_SMUDGE=1, GIT_ALLOW_PROTOCOL=none`, plus `SystemRoot`/`ComSpec`/`PATHEXT` on win32 when non-empty. That is exactly `gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})`; on POSIX encode the values with `jsstr.ToUTF8`, as `projinit.runHardenedGit` does.
  - Failure (`error` or status ≠ 0): `Git checkpoint capture failed for <args.join(" ")>: <(stderr + error.message).trim() || "exit " + (status ?? "unknown")>`. It is a plain `Error`, not an `IntegrityError`, so the sidecar treats it as UNAVAILABLE, never DIRTY.
  - Node's `error.message` here is `spawnSync git ENOENT`, `… ETIMEDOUT` or `… ENOBUFS`.
- **Lone surrogates.** Append and Create get JS strings from CLI args (`--session`, `--turn`); keep them WTF-8 end to end. `JSON.stringify` writes them as `\udXXX` escapes, which `jsjson.StringifyIndent` does.
- **`sequence`** is `events.length + 1` as a float64 number.

- [ ] **Step 1: Write the failing tests**

```go
// internal/ledger/ledger_test.go
package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustJSON(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const started = `{"type":"SESSION_STARTED","payload":{"transport":"SIDE_CAR","workingDirectory":"/w"}}`

func TestCreateAppendWriteVerify(t *testing.T) {
	l, err := Create(CreateOptions{SessionID: "s-1", LedgerID: "ledger-1", CreatedAt: "2026-01-01T00:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	l, err = Append(l, mustJSON(t, started), AppendOptions{EventID: "e-1", OccurredAt: "2026-01-01T00:00:01.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rec", "l.json")
	if _, err := WriteAtomic(path, l); err != nil {
		t.Fatal(err)
	}
	if v := VerifyFile(path); !v.Valid || v.EventCount != 1 {
		t.Fatalf("%+v", v)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "{\n  \"schemaVersion\": \"faultline.codex-lifecycle-ledger.v1\",\n  \"ledgerId\": \"ledger-1\",") || !strings.HasSuffix(string(b), "}\n") {
		t.Fatalf("%s", b)
	}
}

func TestAppendRefusesSecondStart(t *testing.T) {
	l, _ := Create(CreateOptions{SessionID: "s-1", LedgerID: "ledger-1", CreatedAt: "2026-01-01T00:00:00.000Z"})
	l, _ = Append(l, mustJSON(t, started), AppendOptions{EventID: "e-1", OccurredAt: "2026-01-01T00:00:01.000Z"})
	_, err := Append(l, mustJSON(t, started), AppendOptions{EventID: "e-2", OccurredAt: "2026-01-01T00:00:02.000Z"})
	var ie *IntegrityError
	if !errors.As(err, &ie) || ie.Message != "Refusing an invalid lifecycle transition: Event 2 (SESSION_STARTED) starts a session that has already started; Event 2 (SESSION_STARTED) must be the first event" {
		t.Fatalf("%v", err)
	}
}

func TestAppendAtomicHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.json")
	os.WriteFile(path+".lock", nil, 0o600)
	_, err := AppendAtomic(path, mustJSON(t, started), AppendOptions{})
	want := "Could not acquire lifecycle ledger append lock " + path + ".lock: EEXIST: file already exists, open '" + path + ".lock'"
	if err == nil || err.Error() != want {
		t.Fatalf("%v", err)
	}
}
```

```go
// internal/ledger/checkpoint_test.go
func TestCaptureRefusesDirtyAndSigns(t *testing.T) {
	calls := []string{}
	run := func(repo string, args []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "rev-parse --show-toplevel":
			return "/r", nil
		case "ls-files --others --exclude-standard":
			return "new.txt", nil
		}
		return "", nil
	}
	_, err := CaptureGitCleanCheckpoint("/r", CaptureOptions{RunGit: run})
	if err == nil || err.Error() != ErrDirtyWorktree || len(calls) != 3 {
		t.Fatalf("%v %q", err, calls)
	}
}
```

Port `tests/ledger.test.ts` 112–223 (create/append/write, the transport label, tamper, reorder, duplicate sequence, session mismatch, lifecycle order, a real clean checkpoint, serialized appends) and `tests/fl-record-checkpoint-fsmonitor.test.ts:29` (a repository `core.fsmonitor` that would write a marker is never run). The serialized-appends case becomes: 8 goroutines `AppendAtomic` in a loop and retry on the lock error. Assert a contiguous sequence; TS runs them sequentially, so the Go test only proves the lock excludes.

**Live test** `TestLiveLedgerWriters` (`difftest/ledger_writers_live_test.go`). Oracle ops in `oracle-ledger.ts`:
- `ledgerCreate({options})`
- `ledgerAppend({ledger, input, options})`
- `ledgerAppendAtomic({file, input, options})`
- `ledgerWriteAtomic({file, ledger})`
- `captureGitCleanCheckpoint({repository, path, steps?})`

Each returns `JSON.stringify` of `{value}` or `{threw: message, zod: boolean}`, plus, for the file ops, the resulting directory listing with modes and bytes. Under one seed (`oracle.StartSeeded` and `clock.ResetForTest` with the seeded-tick alignment of `TestLiveTurnSnapshotCapture`):
- 2,000 generated `(ledger, input, options)` triples from `gen.MutateLedgerEvents` plus new generators: every event type, every payload field at its schema bounds, `-0`, `1e21`, `NaN`-like strings, lone surrogates, missing and extra keys, wrong ordinals, timestamps equal and earlier, ended and active ledgers, and option ids that fail `IdentifierSchema`.
- 200 atomic-file cases: missing directory, held lock, unreadable ledger, invalid ledger, a directory at the ledger path, a read-only directory (non-root only).
- 60 real repositories for capture: clean, tracked change, CRLF-only change, untracked, ignored-only, no HEAD, a subdirectory, a symlinked path, fsmonitor configured.
- 300 scripted-runner sequences for capture: the runner throws at each call, or returns padded or empty outputs.

`requireReached("internal/ledger/ledger.go", …)` and `internal/ledger/checkpoint.go`. Add both files to `liveGatedFiles` in Task 9; until then the test lists them in a local slice and Task 9 moves them.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/ledger/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription; every function carries its `src/ledger.ts` line.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/ledger/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveLedgerWriters -v > /tmp/s4a-t4.log 2>&1; tail -5 /tmp/s4a-t4.log` (in podman or with the `perl alarm` wrapper; under 280 s)
Expected: PASS, with a `LIVE-SUMMARY` line.

- [ ] **Step 5: Commit**

```bash
git add internal/ledger difftest/ledger_writers_live_test.go difftest/gen/oracle-ledger.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): lifecycle ledger writers, append lock and clean Git checkpoint

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Snapshot GC, the clean Git sidecar snapshot, and `fl codex` without `sidecar`

**Files:**
- Create: `internal/snapgc/gc.go`, `internal/snapgc/gitsnapshot.go`, `internal/snapgc/gc_test.go`, `internal/snapgc/gitsnapshot_test.go`, `internal/cli/codex_snapshot_test.go`, `difftest/snapgc_live_test.go`, `difftest/testdata/writers.d/4a-codex.json`, `difftest/testdata/golden/writers.d/4a-codex.jsonl`
- Modify: `internal/cli/codex_snapshot.go` (replace the Task 3 stub), `difftest/gen/oracle-snapgc.ts` (its `ops`), `.github/scripts/windows-writer-skips.txt` (append this task's lines only)

**Interfaces:**
- Consumes: `turnsnap.ObjectDirectory`, `turnsnap.PrimaryObjectDirectory`, `turnsnap.SnapshotAlternatesEntry`; `nodefs.{Resolve,Join,Relative,Basename,Exists,Lstat,ReadDirents,ReadDirNames,ReadText,RemoveAll,WriteFileUTF8,MkdirAll}`; `nodeproc.SpawnSyncWith` (inherited env, `WindowsShow: true`); `canonical.{DigestJSON,SortLocale}`; `jsre.MustCompile`; `clock.Now`.
- Produces:

```go
package snapgc

// Result ports SnapshotGcResult (src/snapshot-gc.ts:14).
type Result struct {
	RepositoryRoot, ObjectDirectory string
	RemovedEntries                  int
	Status                          string   // "PURGED" or "ALREADY_EMPTY"
	ReferencingPaths                []string // never nil
}

// Purge ports purgeFaultlineSnapshotObjects (src/snapshot-gc.ts:143).
func Purge(repository string, force bool) (Result, error)

// ListQuarantinedObjectIDs ports listQuarantinedObjectIds (:57).
func ListQuarantinedObjectIDs(objectDirectory string) ([]string, error)

// FindReferences ports findQuarantinedObjectReferences (:99).
func FindReferences(repositoryRoot string, ids map[string]bool) ([]string, error)

// CaptureCleanGitSnapshot ports captureCleanGitSnapshot (src/git-snapshot.ts:26).
func CaptureCleanGitSnapshot(repository string) (jsjson.Value, error)

// WriteGitSidecarSnapshot ports writeGitSidecarSnapshot (src/git-snapshot.ts:47).
func WriteGitSidecarSnapshot(repository string, snapshot jsjson.Value) (string, error)
```

```go
// internal/cli/codex_snapshot.go
func codexSnapshotGCCommand(e *env, gcArgs []string) error // cli-app.ts:3003-3016
func codexDryRunCommand(e *env) error                      // cli-app.ts:3017-3020
func codexSnapshotCommand(e *env, args []string) error     // cli-app.ts:3021-3027
```

**Gotchas:**
- **Purge order** (`snapshot-gc.ts:143-184`):
  - `repositoryRoot = resolve(repository)` (correction 8: no git) and `objectDirectory = turnsnap.ObjectDirectory(root)`.
  - If `!existsSync(objectDirectory)`, run `removeAlternatesEntry` and return `ALREADY_EMPTY` with 0 and `[]`.
  - Otherwise:
    1. List the ids, then find the references.
    2. If any references remain and there is no `--force`, throw.
    3. Count the entries, then `rmSync(objectDirectory, {recursive, force})` (`nodefs.RemoveAll`), then `removeAlternatesEntry`.
    4. Status is `ALREADY_EMPTY` when the count is 0, else `PURGED`. The referencing paths are kept, which is non-empty only under `--force`.
- **Refusal text:** `Refusing to purge quarantined turn-snapshot objects: still referenced by ${n} path(s) under .faultline (${first5.join(", ")}${n > 5 ? ` (+${n - 5} more)` : ""}). Re-run with --force after confirming those ledgers/turn bundles no longer need the trees, or archive them first.`
- **removeAlternatesEntry** (`:38-54`):
  1. `existsSync` follows symlinks, so a dangling symlink is "absent" and nothing happens.
  2. Then `lstat`: a symlink or non-file throws `Refusing to edit non-regular Git alternates file: <path>`.
  3. Read the file as UTF-8, split on `/\r?\n/`, JS-trim each line, and drop empty lines and `../faultline/objects`.
  4. If nothing remains, `rmSync(force)`. Otherwise write `lines.join("\n") + "\n"` with flag `w` (`nodefs.WriteFileUTF8(…, 0o666, false)`). An existing file keeps its mode.
- **ListQuarantinedObjectIDs** (`:57-74`):
  - Takes `Dirent.IsDir` only, so a symlink to a directory is skipped. It skips `info` and `pack`, and requires `/^[0-9a-f]{2}$/i`. The prefix is lower-cased.
  - Inside a bucket, `readdirSync(bucket)` gives plain names: skip names ending in `.tmp`. The lower-cased rest must match `/^[0-9a-f]{38}$/`, so upper-case hex is accepted and SHA-256 names (62 hex) are ignored.
  - Order: buckets in byte order, then names in byte order.
- **FindReferences** (`:99-134`):
  - An empty id set returns `[]` before any filesystem access.
  - `walkRegularFiles`:
    - `existsSync` first.
    - `readdirSync(withFileTypes)` is outside any `try`, so an unreadable directory throws its Node error to the CLI.
    - Each entry is lstat'ed inside a `try` whose errors are ignored. Symlinks are skipped; directories recurse.
  - `relativePath = relative(root, file)` with `\` → `/`. The filter is:
    - `/(?:^|\/)(?:recordings|turn-proof-bundles|git-proof-bundles|bundles|proofs|turn-proofs|git-proofs)\//`,
    - or `/(?:ledger|investigation|manifest|ROOT\.sha256|hashes\.txt)$/i`,
    - or `endsWith(".json")`.
  - Files over 8 MiB (`8 * 1024 * 1024`, by lstat size) are skipped. A read error is skipped.
  - Match `/\b([0-9a-f]{40})\b/gi`, lower-case each match, stop at the first hit per file.
  - Sort with `localeCompare` (`canonical.SortLocale`).
  - `\b` is JS's ASCII word boundary, so a 64-hex run yields no 40-hex match. Use `jsre`.
- **countEntriesRecursive** counts `Dirent.IsFile` only, recursing on `IsDir`. Symlinks and FIFOs are not counted.
- **Git sidecar snapshot** (`git-snapshot.ts`):
  - **runGit**:
    - `SpawnSyncWith("git", ["-C", repo, ...args], {WindowsShow: true})`, with the inherited env and cwd and the default maxBuffer. There is no hardening: the user's git config applies.
    - Error: `Git sidecar capture failed for ${args.join(" ")}: ${detail || `exit ${status ?? "unknown"}`}`. Output is trimmed.
  - **Capture**:
    1. `rev-parse --show-toplevel` from `resolve(repository)`. The result is not resolved again (`repoRoot` is git's text).
    2. `status --porcelain=v1 --untracked-files=all`. Non-empty output throws `Git sidecar capture requires a clean worktree. Commit, stash, or explicitly model changes before recording evidence.`
    3. `rev-parse HEAD`, then `rev-parse HEAD^{tree}`.
    4. Unsigned, in this order: `{schemaVersion: "faultline.git-sidecar.v1", recorder: "git-sidecar", repositoryName: basename(repoRoot), headCommit, treeDigest, capturedAt: ISO(clock.Now()), clean: true}`.
    5. `digest = DigestJSON(unsigned)`; the snapshot is `{...unsigned, digest}`.
  - **Write**:
    - The directory is `join(resolve(repository), ".faultline", "recordings")`, made with `mkdirSync({recursive: true})`; correction 9 applies.
    - The file is `<digest without "sha256:">.json`, written as `StringifyIndent + "\n"` with flag `w` and default mode `0o666`.
- **CLI outputs** (`JSON.stringify(…, null, 2) + "\n"`, exit 0):
  - **gc**: `{status, repositoryRoot, objectDirectory, removedEntries, referencingPaths, next: "fl doctor --security"}`. `--repo` uses `option` (absent or last → cwd); `--force` uses `hasFlag`.
  - **dry run**: exactly `{adapter: "observed-codex-hook-sidecar", status: "DRY_RUN", records: ["public session and turn identifiers", "prompt digest only", "clean Git checkpoint when available"], limitation: "Install and trust the emitted hook configuration to observe public lifecycle metadata. FaultLine does not intercept private model state, transcripts, or reasoning."}`.
  - **--snapshot**: `repository = resolve(option("--repo") ?? cwd)`, then capture, then write, then print `{snapshot, file, limitation: "This is a clean Git sidecar snapshot, not a live Codex transport event."}`.

**Writer cases** (`writers.d/4a-codex.json`; scenario in parentheses; every case that runs git is `posixOnly`):
- **Router:**
  - `codex-usage` (`codex`) and `codex-snapshot-no-gc` (`codex snapshot`): the fall-through text.
  - `codex-dry-run` and `codex-dry-run-anywhere` (`codex foo --snapshot --dry-run`, a dry run).
- **--snapshot:**
  - `codex-snapshot-clean` (sidecar-repo)
  - `codex-snapshot-repo-flag` (record-repo, `--repo repo`)
  - `codex-snapshot-twice` (sidecar-repo, two invocations with `advanceClock`: two files)
  - `codex-snapshot-dirty` (sidecar-dirty)
  - `codex-snapshot-no-head` (sidecar-no-head)
  - `codex-snapshot-not-a-repo` (empty)
  - `codex-snapshot-nested-repo-flag` (record-repo plus an `add-file` mutation `repo/.faultline/keep`, ignored by the repo's `.gitignore`; `--repo repo/.faultline` writes `repo/.faultline/.faultline/recordings/…`)
  - `codex-snapshot-recordings-is-file` (sidecar-repo plus the file `.faultline/recordings`: mkdir's EEXIST)
  - `codex-snapshot-user-config` (sidecar-repo plus an untracked file and `user.gitconfig` holding `[status]\n\tshowUntrackedFiles = no\n`, with `env: {"GIT_CONFIG_GLOBAL": "<WS>/user.gitconfig"}`, because the template env overrides the harness's empty global config: TS honours it, so the snapshot succeeds; this pins "not hardened")
- **gc:**
  - `codex-gc-empty` (sidecar-repo)
  - `codex-gc-referenced` (snapgc-repo)
  - `codex-gc-force` (snapgc-repo, `--force`)
  - `codex-gc-unreferenced` (snapgc-repo, the referencing file deleted)
  - `codex-gc-alternates-other-entries` (alternates `/x/objects\r\n../faultline/objects\r\n`: rewritten as `/x/objects\n`)
  - `codex-gc-alternates-symlink` (refusal)
  - `codex-gc-alternates-dangling` (a dangling symlink: untouched, no error)
  - `codex-gc-alternates-only-without-store` (no store, alternates holding only the entry: removed)
  - `codex-gc-store-empty-buckets` (only `info/`, `pack/`: `ALREADY_EMPTY` after removal)
  - `codex-gc-many-references` (7 referencing files: `(+2 more)`)
  - `codex-gc-uppercase-bucket` (an `AB/` bucket and a `.tmp` name, referenced in lower case)
  - `codex-gc-unreadable-faultline-dir` (a `chmod 000` directory under `.faultline`: the Node `EACCES … scandir` text; non-root only)
  - `codex-gc-worktree` (sidecar-worktree, `--repo wt`: `ALREADY_EMPTY`)
  - `codex-gc-repo-last` (`codex snapshot gc --repo`: the cwd is used)

- [ ] **Step 1: Write the failing tests**

```go
// internal/snapgc/gc_test.go
package snapgc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const id = "ab" + "0123456789abcdef0123456789abcdef012345"

func TestPurgeRefusesReferencedThenForces(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git/faultline/objects/ab", id[2:]), "z")
	write(t, filepath.Join(repo, ".git/objects/info/alternates"), "../faultline/objects\n")
	write(t, filepath.Join(repo, ".faultline/recordings/l.json"), `{"treeDigest":"`+id+`"}`)
	_, err := Purge(repo, false)
	if err == nil || !strings.HasPrefix(err.Error(), "Refusing to purge quarantined turn-snapshot objects: still referenced by 1 path(s) under .faultline (.faultline/recordings/l.json).") {
		t.Fatalf("%v", err)
	}
	r, err := Purge(repo, true)
	if err != nil || r.Status != "PURGED" || r.RemovedEntries != 1 || len(r.ReferencingPaths) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".git/objects/info/alternates")); !os.IsNotExist(err) {
		t.Fatal("alternates kept")
	}
}

func TestListSkipsTmpAndSha256(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AB", strings.ToUpper(id[2:])), "")
	write(t, filepath.Join(dir, "ab", "x.tmp"), "")
	write(t, filepath.Join(dir, "cd", strings.Repeat("e", 62)), "")
	ids, err := ListQuarantinedObjectIDs(dir)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("%q %v", ids, err)
	}
}
```

Port `tests/git-snapshot.test.ts:14` as `TestCaptureCleanAndRefuseDirty` (real git, temp repo).

`internal/cli/codex_snapshot_test.go` pins the dry-run bytes:

```go
func TestCodexDryRun(t *testing.T) {
	stdout, stderr, code := run("codex", "--dry-run")
	want := "{\n  \"adapter\": \"observed-codex-hook-sidecar\",\n  \"status\": \"DRY_RUN\",\n  \"records\": [\n    \"public session and turn identifiers\",\n    \"prompt digest only\",\n    \"clean Git checkpoint when available\"\n  ],\n  \"limitation\": \"Install and trust the emitted hook configuration to observe public lifecycle metadata. FaultLine does not intercept private model state, transcripts, or reasoning.\"\n}\n"
	if code != 0 || stderr != "" || stdout != want {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
}
```

**Live test** `TestLiveSnapshotGC` (`difftest/snapgc_live_test.go`). Oracle ops in `oracle-snapgc.ts`: `purgeFaultlineSnapshotObjects({repository, force})`, `listQuarantinedObjectIds({dir})` and `findQuarantinedObjectReferences({repository, ids})`. Each returns its value or `{threw}`, and the purge op also returns the tree afterwards.
- 300 generated trees, each copied twice:
  - bucket names in mixed case;
  - `.tmp` and SHA-256 names, symlinked buckets, FIFOs;
  - alternates with CRLF, blank lines, whitespace, the entry repeated, a symlink;
  - `.faultline` files on every filter branch, with ids at word boundaries, inside 64-hex runs and in upper case;
  - files over 8 MiB (sparse);
  - unreadable files (skipped) and directories (thrown; non-root).
- `requireReached("internal/snapgc/gc.go", …)`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/snapgc/ ./internal/cli/ -run 'TestPurge|TestList|TestCapture|TestCodexDryRun'`
Expected: FAIL (package does not exist; `not yet ported: codex --dry-run`).

- [ ] **Step 3: Implement**, add the writer cases and generate this task's goldens (podman, `ONLY='^codex-(usage|snapshot|dry-run|gc)'`; confirm that only `golden/writers.d/4a-codex.jsonl` changed).

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/snapgc/ ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?codex-(usage|snapshot|dry-run|gc)|TestWindowsWriterSkipsList' > /tmp/s4a-t5.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4a-t5.log | tail -20; FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveSnapshotGC -v 2>&1 | tail -3`
Expected: PASS. A mismatch is fixed in Go, never in the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/snapgc internal/cli/codex_snapshot.go internal/cli/codex_snapshot_test.go difftest/snapgc_live_test.go difftest/gen/oracle-snapgc.ts difftest/testdata/writers.d/4a-codex.json difftest/testdata/golden/writers.d/4a-codex.jsonl .github/scripts/windows-writer-skips.txt
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl codex snapshot gc, --dry-run and --snapshot with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Sidecar hook state machine, status inspection and the latest ledger (`internal/sidecar`)

**Files:**
- Create: `internal/sidecar/storage.go`, `internal/sidecar/hook.go`, `internal/sidecar/receipt.go`, `internal/sidecar/lock.go`, `internal/sidecar/inspect.go`, `internal/sidecar/sidecar_test.go`, `internal/sidecar/inspect_test.go`, `difftest/sidecar_live_test.go`
- Modify: `internal/projinit/sidecar.go` (drop `runHardenedGit`, `assertSafeDirectory`, `safeDirectoryExists`, `existingSafeChild`, `recordingDirectory`, `sidecarIdentifierSchema`; `CodexSidecarLedgerPath` forwards to `sidecar.LedgerPath`), `difftest/gen/oracle-sidecar.ts` (its `ops`)

**Interfaces:**
- Consumes:
  - Task 4: `ledger.Create`, `ledger.Append`, `ledger.WriteAtomic`, `ledger.ReadVerified`, `ledger.VerifyFile`, `ledger.CaptureGitCleanCheckpoint`, `ledger.CaptureOptions`, `ledger.IntegrityError`, `ledger.ErrDirtyWorktree`.
  - Task 2: `nodefs.{OpenExclusive,Close,Unlink,MtimeMs,ReadDirents}`.
  - `turnsnap.Capture`, `turnsnap.RepoCachePath`, `turnsnap.Error`; `safegit.HostCommand()`; `gitinv.HardenedGitEnvironment`; `nodeproc.Getenv`; `canonical.{SHA256Hex,DigestJSON,CanonicalJSON,SortLocale}`; `bundle.ParseValue`; `clock.NowMillis`.
- Produces (Task 8 and PR 4b rely on these):

```go
package sidecar // Port of src/codex-sidecar.ts.

// Error ports CodexSidecarError (codex-sidecar.ts:194).
type Error struct{ Message string }
func (e *Error) Error() string

// IdentifierSchema is codex-sidecar.ts:62.
var IdentifierSchema schema.Schema
// HookInputSchema ports CodexSidecarHookInputSchema (:107).
var HookInputSchema schema.Schema

// Result ports CodexSidecarResult (:128). Optional members are omitted
// from JSON() when empty / nil.
type Result struct {
	Status           string // SESSION_STARTED, TURN_STARTED, TOOL_USE_STARTED, TOOL_USE_COMPLETED, CHECKPOINT_RECORDED, CHECKPOINT_SKIPPED_DIRTY, CHECKPOINT_SKIPPED_UNAVAILABLE, TURN_SNAPSHOT_RECORDED
	LedgerPath       string
	SessionID        string
	TurnID           string // "" is absent
	ToolName         string // "" is absent
	Idempotent       bool
	CheckpointDigest string // "" is absent
	Reason           string // "" is absent: DIRTY_WORKTREE or CHECKPOINT_UNAVAILABLE
	TurnSnapshot     jsjson.Value // Undefined kind is absent
}
// JSON is the result object in TS key order: status, ledgerPath,
// sessionId, turnId?, toolName?, idempotent, checkpointDigest?, reason?,
// turnSnapshot? (each literal at its TS construction site; see Gotchas).
func (r Result) JSON() jsjson.Value

// RecordObservedHook ports recordObservedCodexHook (:972). stderr receives
// what TS writes to process.stderr during the call (the object-database
// warning, snapshot warnings, a skipped turn snapshot), in order.
func RecordObservedHook(value jsjson.Value, stderr io.Writer) (Result, error)

// LedgerPath ports codexSidecarLedgerPath (:334): never creates storage.
func LedgerPath(cwd, sessionID string) (string, error)

// RecordingDirectory ports recordingDirectory (:297).
func RecordingDirectory(cwd string, create bool) (string, error)

// Inspection ports CodexSidecarInspection (:141) as its JSON value:
// {storageDirectory, recordings: [...]}, each recording in TS key order.
type Inspection struct {
	StorageDirectory string
	Recordings       []Recording
}
type Recording struct {
	LedgerPath        string
	SessionID         string // "" is absent
	Valid             bool
	EventCount        int
	HeadHash          *string
	LastEventType     string // "" is absent (with LastEventOccurredAt)
	LastEventOccurredAt string
	LatestCheckpoint  jsjson.Value // Undefined is absent
	LatestStop        jsjson.Value // Undefined is absent
	Lock              jsjson.Value // Undefined is absent
	Errors            []string
	Shape             RecordingShape // which TS literal built it (see Gotchas)
}

// RecordingShape names the three recording literals of :1036, :1068 and
// :1097, whose key orders differ.
type RecordingShape int

const (
	ShapeUnverified    RecordingShape = iota // {ledgerPath, valid, eventCount, headHash, errors}
	ShapeContradictory                       // receipt errors; sessionId, lastEvent?, latestCheckpoint?, lock?
	ShapeValid                               // sessionId, lastEvent?, latestCheckpoint?, latestStop?, lock?
)
func (i Inspection) JSON() jsjson.Value

// Inspect ports inspectObservedCodexSidecar (:1016); sessionID "" is undefined.
func Inspect(cwd, sessionID string) (Inspection, error)

// Latest ports ResolvedLatestSidecarLedger (:1113).
type Latest struct {
	LedgerPath          string
	SessionID           string // "" is undefined
	ValidCandidateCount int
	LastEventOccurredAt string // "" is undefined
}
// JSON is {ledgerPath, sessionId?, validCandidateCount, lastEventOccurredAt?}.
func (l Latest) JSON() jsjson.Value

// ResolveLatestLedgerPath ports resolveLatestSidecarLedgerPath (:1124).
func ResolveLatestLedgerPath(cwd string) (Latest, error)

// EventID ports sidecarEventId (:344); turnID nil is null.
func EventID(sessionID string, turnID *string, phase string) string

// DigestToolAllowlistedFields ports :363.
func DigestToolAllowlistedFields(sessionID, turnID, toolName string, toolCallID *string) string
```

**Gotchas:**
- **Storage** (`:216-314`). Move the slice 3a code and add `create`. In TS order:
  1. `top = runHardenedGit(cwd, rev-parse --show-toplevel)`.
  2. `common = runHardenedGit(resolve(top), rev-parse --git-common-dir)`.
  3. An empty `common` throws `Git did not report a common metadata directory for this worktree.`.
  4. `realpath(resolve(top, common))`, wrapped as `FaultLine could not resolve Git metadata storage: …`.
  5. `assertSafeDirectory(gitDir, "FaultLine Git metadata directory")`.
  6. Then `faultline` and `recordings` through `ensureSafeChildDirectory(parent, name, label, create)`:
     - `assertSafeDirectory(parent, "FaultLine Git metadata directory")`. The label is always this one, even for `faultline`.
     - If the child is absent: return it when `!create`; otherwise `mkdirSync(child, {mode: 0o700})`. On a mkdir error, accept only `EEXIST` with a now-safe directory. Otherwise throw `FaultLine could not create <label>: <message>`.
     - `assertSafeDirectory(child, label)`.
  7. With `create == false`, the early returns at `:309` and `:312` skip the ensure step when a level is missing.

  Labels: `FaultLine sidecar storage`, `FaultLine sidecar recordings`. Texts: `<label> is unavailable: <msg>` and `<label> must be a real directory, not a symlink or file.`. Keep projinit's tests passing.
- **Hook input schemas** (`:68-113`).
  - `HookBaseSchema` is a plain `z.object`, which strips unknown keys:
    - `session_id`: identifier;
    - `cwd`: `min(1)`;
    - `model`: `min(1).max(200)`.
  - It is extended per event. `UserPromptSubmit` adds `turn_id` and `prompt` (`max(4 * 1024 * 1024)` UTF-16 units). `Stop` adds `turn_id`. Pre/PostToolUse add `turn_id`, `tool_name` and `tool_call_id?`.
  - `ToolNameSchema`'s regex has no message, so zod's default is `Invalid`; `schema.Regex(m, "")` already gives it.
  - The discriminator is `hook_event_name`, and the options are listed in the order `SessionStart, UserPromptSubmit, Stop, PreToolUse, PostToolUse`. That order is the one `invalid_union_discriminator` lists.
  - Go needs a non-strict object that drops unknown keys: check that `s.Object` without `.Strict()` strips them, which zod's default does.
- **recordObservedCodexHook** (`:972-990`):
  1. Parse the input (a ZodError, read at the CLI site).
  2. `cwd = resolve(input.cwd)`.
  3. `RecordingDirectory(cwd, true)`.
  4. `ledgerPath = join(dir, "codex-" + sha256(session_id) + ".json")`.
  5. `withSidecarLock`, then dispatch on the event.
- **withSidecarLock** (`:496-523`). Correction 3 explains its timing.
  1. `assertSafeDirectory(dirname(ledgerPath), "FaultLine sidecar recordings")`.
  2. `lockPath = ledgerPath + ".sidecar.lock"`.
  3. `deadline = Date.now() + 2000`.
  4. Loop:
     - `OpenExclusive(lockPath, 0o600)`.
     - On an error that is not `EEXIST`, rethrow the raw Node error.
     - `clearStaleSidecarLock`:
       - `lstat`; ENOENT means `true` and the loop continues; another error is thrown raw.
       - A symlink or non-file throws `FaultLine sidecar lock must be a regular file, not a symlink or directory.`.
       - If `Date.now() - mtimeMs <= 45000`, `false`.
       - Otherwise `unlinkSync`: ENOENT means `true`; another error throws `FaultLine could not recover a stale sidecar lock: <msg>`.
     - `if (Date.now() >= deadline)` throw `Another FaultLine sidecar event is still recording for this Codex session; inspect fl codex sidecar status after the current turn.`.
     - `wait(min(25, max(1, deadline - Date.now())))` with `time.Sleep`.
  5. `finally`: close, then unlink with errors ignored.

  Every `Date.now()` is a `clock.NowMillis()` read, in this order: deadline; then per retry the stale check, the deadline check and the wait argument.
- **Stop receipts.**
  - The path is `join(dir, "codex-stop-" + sha256(session_id + "\u0000" + turn_id) + ".json")`.
  - `writeJsonAtomic` (`:525`):
    1. `assertSafeDirectory(dir)`.
    2. `safeRegularFileExists(path, "FaultLine sidecar receipt")`, which throws for a symlink or directory.
    3. The stage is `join(dir, "." + sha256(path).slice(0, 24) + "." + pid + ".tmp")`.
    4. `writeFileSync(stage, StringifyIndent + "\n", {flag: "wx", mode: 0o600})`, with no fsync, then `renameSync`.
    5. `finally`: if the stage exists, `rmSync(force)`.

    No uuid is involved: a leftover stage with the same pid makes `wx` fail with EEXIST. `sha256(path)` hashes the absolute path string.
  - The receipt is `StopReceiptSchema.parse(receipt)`, a strict object in the key order `schemaVersion, sessionId, turnId, status, checkpointDigest?, reason?, turnSnapshotDigest?`. The parse reorders to that order, so `turnSnapshotDigest` always comes last.
  - `readStopReceipt`: `safeRegularFileExists`, then parse. Every failure becomes `Unable to read the sidecar stop receipt: <msg>`, a ZodError's message included, read inside the catch.
- **Result key order.** Each TS literal builds its own order, and `{...result, limitation}` (Task 8) appends `limitation` last:
  - `SESSION_STARTED`: `status, ledgerPath, sessionId, idempotent`.
  - `TURN_STARTED`: `status, ledgerPath, sessionId, turnId, idempotent`.
  - Tool use: `status, ledgerPath, sessionId, turnId, toolName, idempotent`.
  - `checkpointResult` RECORDED: `status, ledgerPath, sessionId, turnId, idempotent, checkpointDigest, turnSnapshot?`.
  - `checkpointResult` skipped: `status, ledgerPath, sessionId, turnId, idempotent, reason, turnSnapshot?`.
  - `turnSnapshotOnlyResult`: `status, ledgerPath, sessionId, turnId, idempotent, turnSnapshot`.
  - `resultFromReceipt`: the same three shapes. The skipped one's reason is `receipt.reason ?? (status === DIRTY ? "DIRTY_WORKTREE" : "CHECKPOINT_UNAVAILABLE")`, which also covers a `TURN_SNAPSHOT_RECORDED`-less skipped receipt.

  The fixed key list in `Result.JSON` gives all of these, because no shape puts a key out of that list's order. Pin each shape in a unit test.
- **State machine.** Transcribe `:745-965` line by line. The orders that matter:
  - **SessionStart, new ledger:**
    1. `createStartedLedger`: ledger id `ledger-codex-sidecar-` + `sha256(session_id).slice(0, 32)`; `createdAt` read from the clock.
    2. Append `SESSION_STARTED` with event id `EventID(s, nil, "session-start")`; `occurredAt` read from the clock.
    3. Write the warning line (`TURN_SNAPSHOT_OBJECT_DB_WARNING` + `\n`) to stderr.
    4. Capture the baseline snapshot with `FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY === "1"`, `sessionCachePath: RepoCachePath(cwd)` and `onWarning` writing `FaultLine snapshot warning: <w>\n` to stderr. A `turnsnap.Error` becomes `SessionStart baseline snapshot failed: <msg>`; any other error propagates.
    5. Append `SESSION_BASELINE_SNAPSHOT`.
    6. Write the ledger.

    Nothing is written on failure. The stderr writes go to `RecordObservedHook`'s `stderr` parameter.
  - **SessionStart, existing ledger:** `readSidecarLedger` (`safeRegularFileExists` then `ReadVerified`), `assertExistingSession`, then compare the existing `session-start` event with `sameJson`. Idempotent; no capture.
  - **assertExistingSession** compares `{transport: "SIDE_CAR", workingDirectory: cwd, model}` canonically against the started payload. A `codexThreadId` or `actor` in the payload therefore fails.
  - **UserPromptSubmit:**
    - `previousActive` is the first `TURN_STARTED` without a completion.
    - The prompt digest is `"sha256:" + sha256(prompt)`.
    - An existing `turn-start` event must match `{type, payload: {turnId, turnOrdinal: existing ordinal, promptDigest}}`; this is checked before `previousActive`.
    - The ordinal is `count(TURN_COMPLETED) + 1`.
  - **Stop:** the order at `:820-921`:
    1. Receipt.
    2. Existing checkpoint (recover).
    3. Matching completion (recover: snapshot-only or UNAVAILABLE).
    4. Fresh: append `TURN_COMPLETED`, capture the turn snapshot (a `turnsnap.Error` writes `FaultLine turn-tree snapshot skipped: <msg>\n` to stderr), append `TURN_TREE_SNAPSHOT`, then `ledger.CaptureGitCleanCheckpoint(cwd, {RunGit: runHardenedGit})`.
    5. Any capture error: write the ledger, then the result DIRTY when it is an `*ledger.IntegrityError` whose message is exactly `ErrDirtyWorktree`, else UNAVAILABLE, then the receipt.
    6. Success: append the checkpoint, write the ledger, the result, the receipt.

    A receipt write failure after the ledger write leaves the ledger written (fault matrix).
  - **Tool use:**
    - `callKey = tool_call_id ?? "anon-" + sha256(tool_name + "\u0000" + turn_id).slice(0, 16)`.
    - The phase is `tool-pre-<key>` or `tool-post-<key>`.
    - The payload key order is `turnId, turnOrdinal, toolName, allowlistedFieldsDigest, toolCallId?`; the schema parse reorders it to `turnId, turnOrdinal, toolName, toolCallId?, allowlistedFieldsDigest`.
    - The digest object is `{sessionId, turnId, toolName, toolCallId?}`.
    - Correction 11 applies to an unknown turn.
  - **appendIfAbsent:** if an event with the id exists, compare `sameJson(existing.event, input)`, the raw input and not the parsed one. Key order does not matter, because `canonicalJson` sorts.
- **sidecarEventId** is `codex-sidecar-<sha256(s)[0:24]>-<turnId === null ? "session" : sha256(turnId)[0:24]>-<phase>`. It is used directly as an `eventId`, so its length (up to 160) can fail the identifier schema for a long `tool_call_id`, with the ZodError from `Append`.
- **Inspect** (`:1016-1111`):
  1. Parse `sessionId` when given.
  2. `RecordingDirectory(resolve(cwd), false)`.
  3. If the directory is not a safe existing one, return `{storageDirectory, recordings: []}`.
  4. Ledger paths: `ReadDirents` names matching `/^codex-[a-f0-9]{64}\.json$/` (the `Dirent` type is unused), joined and filtered by `safeRegularFileExists`, which throws for a symlink. Sorted with `localeCompare`. With a session: only its path, when it is a regular file.
  5. Per ledger:
     - `VerifyFile`. If invalid: `{ledgerPath, valid: false, eventCount, headHash, errors}`, with no `sessionId`.
     - `readSidecarLedger`, then `lastEvent`, the lock health and `latestCheckpoint` (the last `WORKTREE_CHECKPOINT`: `{digest, capturedAt, headCommit, treeDigest, afterTurnOrdinal}`), then the last completion.
     - The receipt scan: `stopReceiptsForSession` reads every `/^codex-stop-[a-f0-9]{64}\.json$/` entry in readdir order. Errors are `Invalid sidecar stop receipt <name>: <msg>`. Only the session's receipts are kept, in a Map by `turnId`, so the last one wins and the insertion order is that of the first set.
     - Each receipt is checked with `assertReceiptMatchesLedger`. Errors are `Contradictory sidecar stop receipt for <turnId>: <msg>`.
     - With errors: `{ledgerPath, sessionId, valid: false, eventCount, headHash, lastEvent?, latestCheckpoint?, lock?, errors}`.
     - Otherwise `latestStop`:
       - no receipt and no checkpoint for the ordinal: `{turnId, status: "CHECKPOINT_PENDING_RECOVERY", receipt: "MISSING"}`;
       - no receipt, with a checkpoint: `{turnId, status: "CHECKPOINT_RECORDED", receipt: "RECOVERABLE_FROM_LEDGER"}`;
       - a receipt: `{turnId, status, reason?, receipt: "DURABLE"}`.

       The recording is `{ledgerPath, sessionId, valid: true, eventCount, headHash, lastEvent?, latestCheckpoint?, latestStop?, lock?, errors: []}`.
  - The three recording shapes have different key orders: the `Shape` field picks one. `lastEvent` is `{type, occurredAt}`.
- **Lock health** (`:454`):
  - `lstat`; ENOENT means absent; another error is thrown raw.
  - A symlink or non-file is `{state: "INVALID"}`.
  - Otherwise `age = Math.max(0, Math.round(Date.now() - mtimeMs))`, reading `clock.NowMillis()` once. JS `Math.round` rounds half up (`math.Floor(x + 0.5)`), not half away from zero.
  - The result is `{state: age > 45000 ? "STALE" : "ACTIVE", ageMilliseconds: age}`.
- **ResolveLatestLedgerPath** (`:1124-1170`):
  - Zero valid recordings: throw `No valid Codex sidecar ledger found under Git metadata recordings. Run a Codex session with trusted FaultLine hooks, or pass --ledger <path> explicitly.`.
  - One: return it with count 1.
  - Several, none with a `lastEvent`: `Ambiguous latest sidecar ledger: N valid recordings without comparable last-event timestamps. Pass --ledger explicitly. Candidates: <paths joined "; ">`.
  - Otherwise the maximum `occurredAt` by string `>` (`jsstr.CompareUTF16`). A tie: `Ambiguous latest sidecar ledger: N valid recordings share last-event time <t>. Pass --ledger explicitly. Candidates: …`.
  - The returned count is the valid count, not the timed one.
  - No 4a command calls it. It is ported and live-tested here for PR 4b's `investigate turns --latest`.
- **The hardened runner** here is `runHardenedGit` (`:233`). It has no timeout, and its error is `FaultLine could not safely inspect this Git worktree: …`. Keep the implementation moved from projinit byte for byte.
  - Its signature is variadic (`runHardenedGit(repository string, args ...string)`). For the Stop checkpoint, pass `ledger.CaptureOptions{RunGit: func(r string, a []string) (string, error) { return runHardenedGit(r, a...) }}`.
  - Its errors are plain `*Error`, never `*ledger.IntegrityError`, so a git failure is UNAVAILABLE and never DIRTY.

- [ ] **Step 1: Write the failing tests**

```go
// internal/sidecar/sidecar_test.go
package sidecar

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func repo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=t@e.test", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	return dir
}

func hook(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEventID(t *testing.T) {
	turn := "t-1"
	if got := EventID("s-1", nil, "session-start"); got != "codex-sidecar-6a840baf5d8c3ff241688aeb-session-session-start" {
		t.Fatal(got)
	}
	if got := EventID("s-1", &turn, "turn-stop"); !strings.HasPrefix(got, "codex-sidecar-6a840baf5d8c3ff241688aeb-") || !strings.HasSuffix(got, "-turn-stop") {
		t.Fatal(got)
	}
}

func TestSessionTurnStopIsIdempotent(t *testing.T) {
	dir := repo(t)
	base := `"session_id":"s-1","cwd":` + jsjson.Stringify(jsjson.MakeString(dir)) + `,"model":"m"`
	var stderr bytes.Buffer
	steps := []string{
		`{"hook_event_name":"SessionStart",` + base + `}`,
		`{"hook_event_name":"UserPromptSubmit",` + base + `,"turn_id":"t-1","prompt":"secret prompt","transcript_path":"/x"}`,
		`{"hook_event_name":"Stop",` + base + `,"turn_id":"t-1"}`,
	}
	for _, s := range steps {
		if _, err := RecordObservedHook(hook(t, s), &stderr); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	r, err := RecordObservedHook(hook(t, steps[2]), &stderr)
	if err != nil || r.Status != "CHECKPOINT_RECORDED" || !r.Idempotent {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(r.LedgerPath)
	if bytes.Contains(b, []byte("secret prompt")) || bytes.Contains(b, []byte("transcript_path")) {
		t.Fatal("prompt or transcript path reached the ledger")
	}
}

func TestHeldLockTimesOut(t *testing.T) {
	dir := repo(t)
	rec, err := RecordingDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(rec, "codex-6a840baf5d8c3ff241688aeb14546e653774cd5387faf1cb982b0fbbf1fbb810.json")
	os.WriteFile(ledger+".sidecar.lock", nil, 0o600)
	_, err = RecordObservedHook(hook(t, `{"hook_event_name":"SessionStart","session_id":"s-1","cwd":`+jsjson.Stringify(jsjson.MakeString(dir))+`,"model":"m"}`), &bytes.Buffer{})
	if err == nil || err.Error() != "Another FaultLine sidecar event is still recording for this Codex session; inspect fl codex sidecar status after the current turn." {
		t.Fatalf("%v", err)
	}
}
```

Port all 11 cases of `tests/codex-sidecar.test.ts` (80–408) and the fsmonitor case (234). Add unit tests for the three recording shapes' key order, `Result.JSON` for each result literal, the `<STAGEKEY>` stage name (`"." + SHA256Hex(receiptPath)[:24] + "." + pid + ".tmp"`) and `Math.round(0.5) == 1`, `Math.round(-0.5) == -0` (then `max(0, …)` gives `0`).

**Live test** `TestLiveSidecarHooks` (`difftest/sidecar_live_test.go`). Oracle ops in `oracle-sidecar.ts`:
- `recordObservedCodexHook({value, path, env})`: it captures `process.stderr.write` during the call and returns `{result} | {threw, zod}` with that stderr;
- `inspectObservedCodexSidecar({cwd, sessionId})`;
- `resolveLatestSidecarLedgerPath({cwd})`;
- `codexSidecarLedgerPath({cwd, sessionId})`.

Under one seed, with the clock-alignment of `TestLiveTurnSnapshotCapture` and `FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY` toggled per sequence:
- 400 `gen.HookSequence`s on repositories copied twice, each side at the same path in turn. Repositories are clean, dirty, with no HEAD, a linked worktree, a subdirectory cwd, and with `.git/faultline` a symlink or a file.
- After every step, compare the result or the error, stderr, the whole `.git/faultline` tree (ledger bytes, receipts, lock, modes) and `inspect` of the repository.
- The `HookOp` kinds plant stale and fresh locks (`set-mtime` semantics), contradictory receipts, symlinked storage, truncated ledgers and removed receipts.
- Then 100 multi-session stores for `resolveLatestSidecarLedgerPath`: 0, 1 and several valid ledgers, tied and untied last events, invalid ones.

The planted fresh lock costs about 17 s per step (correction 3), so cap it at 5 sequences. `plant-stage` creates a leftover receipt stage `.<sha256(receiptPath)[0:24]>.<pid>.tmp` with each side's own pid, TS's from a new oracle op `processPid` and Go's from `os.Getpid()`. The receipt write then fails with Node's `EEXIST … open` text on both sides. A writer case cannot predict fl's pid, so this lives only here. `requireReached` on `hook.go`, `receipt.go`, `lock.go` and `inspect.go`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sidecar/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription; move the storage code from projinit and keep `projinit.CodexSidecarLedgerPath` as a one-line forward.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/sidecar/ ./internal/projinit/ ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?init-' > /tmp/s4a-t6a.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4a-t6a.log | tail; FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveSidecarHooks -v > /tmp/s4a-t6.log 2>&1; tail -5 /tmp/s4a-t6.log` (each under 280 s; in podman for the live test)
Expected: PASS; the slice 3a `init-` goldens unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar internal/projinit difftest/sidecar_live_test.go difftest/gen/oracle-sidecar.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): Codex sidecar hook state machine, stop receipts, lock and status inspection

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: `fl record` and `fl codex record`

**Files:**
- Modify: `internal/cli/record.go` (replace the Task 3 stub), `difftest/coverage_test.go` (drop only the `unreachableLiterals` entry `Identifier contains unsupported characters`, which `record-init-session-regex` now reaches), `.github/scripts/windows-writer-skips.txt` (append only)
- Create: `internal/cli/record_test.go`, `difftest/testdata/writers.d/4a-record.json`, `difftest/testdata/golden/writers.d/4a-record.jsonl`

**Interfaces:**
- Consumes:
  - Task 4: `ledger.Create`, `ledger.Append`, `ledger.WriteAtomic`, `ledger.AppendAtomic`, `ledger.CaptureGitCleanCheckpoint`, `ledger.Verify`, `ledger.VerifyFile`.
  - Task 3: `e.readStandardInput`.
  - Task 2: `gitproof.LifecycleEventInputSchema`, `gitproof.CodexTransportSchema`, `gitproof.CodexTransportOptions`.
  - `option`, `requiredOption`, `hasFlag`, `fail`, `e.printJSON`, `obj`, `jsjson.ToNumber`, `nodefs.{Resolve,Exists}`.
- Produces: `func recordCommand(e *env, args []string) error` (`cli-app.ts:1488`), reached from `record` and `codex record`; `func ledgerPathOption(args []string, sessionID string, hasSession bool) string` (`cli-app.ts:256`).

**Gotchas:**
- **`ledgerPath`** is `resolve(option("--ledger") ?? ".faultline/recordings/" + (sessionId ?? "session") + ".json")`. `init` passes the session; the others do not, so their default is `session.json`.
- **init** (`:1491-1509`), in order:
  1. `requiredOption --session`.
  2. `file = ledgerPath(args, session)`.
  3. If `existsSync(file)` (following symlinks; a dangling symlink is "absent"): `Refusing to overwrite an existing lifecycle ledger: <file>`.
  4. `repository = resolve(option --repo ?? cwd)`. It is not checked to exist.
  5. `Create({sessionId})`: a UUID, then the clock. A bad session id fails here as a ZodError.
  6. The transport is `parseObservedTransport(option --transport ?? "SIDE_CAR")`. Text: ``--transport must be one of CODEX_CLI, CODEX_APP, SIDE_CAR, OBSERVED_EXTERNAL_TRANSPORT (got ${JSON.stringify(raw)}). Use OBSERVED_EXTERNAL_TRANSPORT for honestly labeled non-Codex checkpoints — never rebadge Cursor/editor history as SIDE_CAR or CODEX_*.`` (an em dash; `JSON.stringify` of the raw value).
  7. The payload is `{transport, workingDirectory: repository}`, then each of `codexThreadId` (`--thread`), `model` (`--model`) and `actor` (`--actor`) only when `option(...)` is truthy, so `""` is left out.
  8. `Append`: the clock, then a UUID.
  9. `WriteAtomic(file)`.
  10. Print `{status: "RECORDING_STARTED", ledger: file, sessionId, headHash: events.at(-1)?.hash}`, where `sessionId` is the raw option string.

  The transport is evaluated inside the `appendLifecycleEvent` argument, after `Create`. So a bad transport has already consumed a UUID and a clock tick, which matters only for later seeded values; nothing is written.
- **checkpoint** (`:1511-1522`):
  1. `file`.
  2. `repository = resolve(requiredOption --repo)`.
  3. `afterTurnOrdinal = Number(requiredOption --after-turn)`. If `!Number.isInteger(n) || n < 0`: `--after-turn must be a non-negative integer.`. `Number(" 1 ")`, `"0x1"`, `"1e0"`, `"-0"` and `""` (refused earlier by `requiredOption`) follow `ToNumber`, and `-0` is accepted.
  4. Capture (correction 5), with the full `ledger.DefaultHardenedGitRunner`.
  5. `AppendAtomic(file, {type: "WORKTREE_CHECKPOINT", payload: {checkpoint, afterTurnOrdinal}})`.
  6. Print `{status: "CHECKPOINT_RECORDED", ledger: file, headHash}`.
- **attach** (`:1524-1562`):
  - `repository = option(--repo) ? resolve(requiredOption --repo) : undefined`. `--repo` with an empty value is "absent", and `--repo --x` passes `option` but fails `requiredOption`.
  - Then `requiredOption --turn`, then the `--ordinal` number check: `--ordinal must be a positive integer.` (`n <= 0` refused, `-0` refused).
  - Append `TURN_STARTED` with `requiredOption --prompt-digest`, which is evaluated inside the argument, after the ordinal.
  - Append `TURN_COMPLETED` with `outcome = option --outcome ?? "COMPLETED"`. That value is unchecked here, so a bad one is a ZodError from the append. `outputDigest` and `contribution` are included only when truthy.
  - If `--checkpoint`: without a repository, `--repo is required when --checkpoint is used.`, thrown after both appends, which stay written. Otherwise capture and append.
  - `verification = Verify(ledger)`, the in-memory one.
  - Print `{status: valid ? "ATTACHED" : "INVALID", ledger: file, turnId, turnOrdinal, checkpoint: "RECORDED"|"NOT_REQUESTED", limitation: "Observed sidecar attribution only; FaultLine does not claim private Codex interception or turn-level blame.", valid, errors, eventCount, headHash}`. The spread of `LedgerVerification` puts its keys last, in its order. The exit code is `valid ? 0 : 1`.
  - `turnOrdinal` is the number, so `"1e0"` prints `1`.
- **stdin** (`:1564-1587`):
  1. `file`, then `repository` as in attach.
  2. `readStandardInput()`, split on `/\r?\n/`, JS-trim, drop empty lines. None left: `Lifecycle stdin contained no NDJSON events.`.
  3. Per line, in order:
     - `JSON.parse`. A failure is `Lifecycle stdin contains invalid JSON: <V8 message>`; `jsjson.Parse` already gives V8's text.
     - `lifecycleInputFromLine(envelope, repository)` (`:1461`):
       - A non-object (or array) is returned as is.
       - `event = typeof envelope.event === "object" && envelope.event !== null ? envelope.event : envelope`. An array `event` counts as an object.
       - Unless `event.type === "WORKTREE_CHECKPOINT"`, return `event`.
       - `payload` = `event.payload` if it is a non-null object, else undefined. With no payload, or `"checkpoint" in payload` (own or inherited; for parsed JSON, own keys only), return `event`.
       - With no repository: `A --repo is required when stdin asks FaultLine to capture a WORKTREE_CHECKPOINT.`.
       - Otherwise return `{type: "WORKTREE_CHECKPOINT", payload: {afterTurnOrdinal: payload.afterTurnOrdinal, checkpoint: Capture(repository)}}`. The key order (`afterTurnOrdinal` first) is then reordered by the schema parse. `afterTurnOrdinal` undefined is dropped and fails the parse as "Required".
     - `LifecycleEventInputSchema.parse(...)`: a ZodError, read at the CLI site.
     - The metadata is the envelope when it is a non-array object, else `{}`. `eventId`/`occurredAt` are taken only when they are strings.
     - `AppendAtomic`.
  4. After the loop, print `{status: valid ? "EVENTS_RECORDED" : "INVALID", ledger: file, ...VerifyFile(file)}` with exit `valid ? 0 : 1`.

  A failure on line k leaves lines 1..k-1 appended. Each line takes and releases the lock.
- **verify** (`:1588-1594`): `{ledger: file, ...VerifyFile(file)}`, exit `valid ? 0 : 1`. A missing file is `valid: false` with the read error, exit 1, and not a thrown error.
- **default**: `Usage: fl record init|stdin|checkpoint|attach|verify ...`.
- **codex record** runs the same function with `args[1:]`, so its usage text is the same.

**Writer cases** (`writers.d/4a-record.json`; scenario in parentheses; `advanceClock: true` on every multi-invocation case; `posixOnly` on every case that runs git):
- **init:**
  - `record-init` (empty; default path `.faultline/recordings/s-1.json`)
  - `record-init-ledger-flag`
  - `record-init-transports` (four invocations, one per transport, distinct `--ledger`)
  - `record-init-bad-transport`, `record-init-transport-last` (`--transport` last: the default)
  - `record-init-thread-model-actor`, `record-init-empty-model` (`--model ""`: left out)
  - `record-init-long-model` (201 characters: zod)
  - `record-init-bad-session` (`--session -x`: Missing required option), `record-init-session-regex` (`--session "a b"`: zod)
  - `record-init-exists`, `record-init-exists-dangling-symlink` (`add-symlink`: proceeds and replaces the link with the rename)
  - `record-init-dir-is-file` (`.faultline/recordings` a file: mkdir's ENOTDIR/EEXIST)
  - `record-init-lone-surrogate-actor` (argv with `\ud800`: Windows-only behaviour differs, so posixOnly)
- **checkpoint** (record-repo, all posixOnly):
  - `record-checkpoint`, `record-checkpoint-after-turn-forms` (four invocations on `ledger.json` with `--after-turn` `0x1`, `1e0` and ` 1 `, which append three checkpoints after turn 1, then `-0`, which passes the CLI check and fails the transition with `claims after turn 0; expected 1`)
  - `record-checkpoint-negative`, `record-checkpoint-fraction`, `record-checkpoint-nan`, `record-checkpoint-missing-repo`
  - `record-checkpoint-dirty` (record-dirty)
  - `record-checkpoint-not-a-repo`
  - `record-checkpoint-wrong-turn` (`--after-turn 2`: the transition refusal; the lock is released)
  - `record-checkpoint-held-lock` (`add-file ledger.json.lock`)
  - `record-checkpoint-missing-ledger` (the read error inside the lock; the lock is removed)
  - `record-checkpoint-ended` (`ended.json`: "occurs after SESSION_ENDED")
  - `record-checkpoint-tampered` (`tampered.json`)
  - `record-checkpoint-fsmonitor` (record-fsmonitor: no `FSMONITOR_RAN`)
  - `record-checkpoint-no-head` (sidecar-no-head with `--repo .`: `Git checkpoint capture failed for diff-index --ignore-cr-at-eol HEAD: fatal: ambiguous argument 'HEAD': …`. That git text is already in the slice 2 goldens. The 60 s timeout is not a writer case, since it would cost a minute per side; `TestSpawnSyncTimeout` and a `TestLiveLedgerWriters` scripted step that returns `ETIMEDOUT` cover it.)
  - `record-checkpoint-subdir` (`--repo repo/sub`: `repositoryRoot` is the top level)
- **attach:**
  - `record-attach` (record-repo: turn 2 on `ledger.json`)
  - `record-attach-checkpoint` (posixOnly), `record-attach-checkpoint-no-repo` (two events written, then the refusal)
  - `record-attach-outcome-bad` (zod after `TURN_STARTED` is written), `record-attach-outcome-failed`
  - `record-attach-ordinal-zero`, `record-attach-ordinal-wrong` (`--ordinal 3`: transition refusal)
  - `record-attach-output-digest`, `record-attach-contribution`
  - `record-attach-prompt-digest-bad` (zod), `record-attach-prompt-digest-missing`
  - `record-attach-repo-empty` (`--repo "" --checkpoint`: "--repo is required…")
  - `record-attach-active` (`active.json`: starts a turn while one is active)
- **stdin** (inputs given by `input`):
  - `record-stdin-no-ledger` (empty, one valid line: the default `.faultline/recordings/session.json` does not exist, so the first append's read fails, after `mkdirSync` made `.faultline/recordings`)
  - `record-stdin-events` (record-repo: turn 2 started and completed)
  - `record-stdin-envelope` (`{eventId, occurredAt, event}` lines; the explicit ids are used)
  - `record-stdin-envelope-bad-time` (`occurredAt: "2026-07-16"`: zod at root)
  - `record-stdin-crlf-blank-lines`
  - `record-stdin-empty` (`""`), `record-stdin-whitespace-only`
  - `record-stdin-invalid-json` (the second line: the first stays appended)
  - `record-stdin-array-line`, `record-stdin-number-line` (zod)
  - `record-stdin-checkpoint-capture` (posixOnly; `{"type":"WORKTREE_CHECKPOINT","payload":{"afterTurnOrdinal":1}}` with `--repo repo`)
  - `record-stdin-checkpoint-no-repo`
  - `record-stdin-checkpoint-supplied` (a payload with `checkpoint` already present: no capture, digest verified)
  - `record-stdin-checkpoint-missing-ordinal` (posixOnly; the capture runs, then zod "Required")
  - `record-stdin-over-limit` (`inputHex` of 1,333,334 `ff` bytes: the 4 MB text)
  - `record-stdin-invalid-utf8` (`inputHex` with a split sequence inside a JSON string)
  - `record-stdin-key-order` (payload keys reversed: the ledger bytes follow the schema)
  - `record-stdin-held-lock`
- **verify:**
  - `record-verify` (record-repo), `record-verify-default-path` (`.faultline/recordings/session.json` by mutation)
  - `record-verify-missing`, `record-verify-not-json`, `record-verify-bad-schema`, `record-verify-tampered`, `record-verify-active` (valid)
- **router:** `record-usage`, `record-unknown-action`, `codex-record-init` (alias), `codex-record-usage`.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/record_test.go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordInitThenVerify(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "l.json")
	stdout, stderr, code := run("record", "init", "--session", "s-1", "--ledger", ledger, "--repo", dir, "--model", "")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "{\n  \"status\": \"RECORDING_STARTED\",\n  \"ledger\": "+jsonString(ledger)+",\n  \"sessionId\": \"s-1\",\n  \"headHash\": \"sha256:") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if b, _ := os.ReadFile(ledger); strings.Contains(string(b), "\"model\"") {
		t.Fatal("empty --model must be left out")
	}
	stdout, _, code = run("record", "verify", "--ledger", ledger)
	if code != 0 || !strings.Contains(stdout, "\"valid\": true") {
		t.Fatalf("%d %s", code, stdout)
	}
}

func TestRecordAfterTurnParsing(t *testing.T) {
	for _, v := range []string{"-1", "1.5", "abc", "Infinity"} {
		_, stderr, code := run("record", "checkpoint", "--repo", ".", "--after-turn", v)
		if code != 1 || !strings.Contains(stderr, "--after-turn must be a non-negative integer.") {
			t.Errorf("%s: %d %q", v, code, stderr)
		}
	}
}

func TestRecordStdinRequiresEvents(t *testing.T) {
	var out, errb strings.Builder
	code := RunWithStdin([]string{"record", "stdin", "--ledger", filepath.Join(t.TempDir(), "l.json")}, strings.NewReader("\r\n  \n"), &out, &errb)
	if code != 1 || errb.String() != "FaultLine error: Lifecycle stdin contained no NDJSON events.\nNext: pnpm fl help\n" {
		t.Fatalf("%d %q", code, errb.String())
	}
}
```

(`jsonString(s)` is `jsjson.Stringify(jsjson.MakeString(s))`; add it to the test file.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cli/ -run TestRecord`
Expected: FAIL (`not yet ported: record`).

- [ ] **Step 3: Implement; add the writer cases; generate the goldens** (podman, `ONLY='^(record-|codex-record-)'`; confirm that only `golden/writers.d/4a-record.jsonl` changed).

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?(record-|codex-record-)|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4a-t7.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4a-t7.log | tail -20`
Expected: PASS. A mismatch is fixed in Go, never in the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/record.go internal/cli/record_test.go difftest/coverage_test.go difftest/testdata/writers.d/4a-record.json difftest/testdata/golden/writers.d/4a-record.jsonl .github/scripts/windows-writer-skips.txt
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl record init|checkpoint|attach|stdin|verify and fl codex record

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: `fl codex sidecar config|install|status|hook`

**Files:**
- Modify: `internal/cli/codex_sidecar.go` (replace the Task 3 stub), `difftest/coverage_test.go` (drop only the `unreachableLiterals` entries this task makes reachable: the four `sidecarCliCommand` texts at lines 234–237 today), `.github/scripts/windows-writer-skips.txt` (append only)
- Create: `internal/cli/codex_sidecar_test.go`, `difftest/testdata/writers.d/4a-sidecar.json`, `difftest/testdata/golden/writers.d/4a-sidecar.jsonl`

**Interfaces:**
- Consumes:
  - Task 6: `sidecar.RecordObservedHook(value, stderr)`, `sidecar.Inspect`, `Inspection.JSON()`, `Result.JSON()`.
  - `projinit.SidecarCliCommand`, `projinit.HookConfigFor`, `projinit.HookTarget`, `projinit.WriteHookConfig`.
  - Task 3: `e.readStandardInput`; `readJSONInput`; `option`, `requiredOption`, `hasFlag`, `fail`, `e.printJSON`, `obj`.
- Produces: `func codexSidecarCommand(e *env, args []string) error` (`cli-app.ts:1757`).

**Gotchas:**
- **config:** `SidecarCliCommand(args)`, then print `StringifyIndent(HookConfigFor(cmd))`. There is no `--repo` and no git.
- **install** (`:1766-1788`), in order:
  1. `SidecarCliCommand` and the config.
  2. `HookTarget(requiredOption --repo)`. It runs git (`faultline-install-check`), so a non-repository fails here, before the `--yes` check.
  3. Without `--yes`: `{status: "CONFIRMATION_REQUIRED", target: file, effect: "Creates a new project .codex/hooks.json containing the displayed reviewed hook configuration. It does not overwrite or merge an existing hook document.", hooks: config, next: "After reviewing the displayed configuration, rerun the same install command with --yes."}`, exit 1.
  4. With it: `WriteHookConfig`, then `{status: "PROJECT_HOOKS_INSTALLED", hookFile, next: "Restart or begin Codex in this trusted project, then use /hooks to review and trust the displayed FaultLine hooks before they run."}`, exit 0.

  `HookTarget` returns `(file, codexPresent, err)`, and `WriteHookConfig` re-derives the directory from the repository. Pass the same resolved repository.
- **status** (`:1789-1806`):
  - `repository = resolve(option --repo ?? cwd)`; `Inspect(repository, option --session)`. With `--session` last, the option is undefined, so all sessions are listed.
  - Status: `NO_SIDECAR_RECORDINGS` when there are no recordings, `SIDECAR_RECORDING_INVALID` when any is invalid, else `SIDECAR_RECORDINGS_READY`.
  - The output is `{status, storageDirectory, recordings, next, privacy}`. The spread of the inspection puts `storageDirectory` and `recordings` second.
  - `next` is one of three arrays, copied verbatim from `:1799-1802`. The ready one has one string per recording, with `ledgerPath` interpolated.
  - `privacy` is fixed text (`:1803`).
  - Exit `invalid ? 1 : 0`, so "no recordings" exits 0.
- **Usage text** (`:1809`, verbatim) for any action other than config, install, status and hook. The empty action is included.
- **hook** (`:1811-1836`):
  - `quiet = hasFlag --quiet`.
  - Inside the `try`: `input = option(--input) === undefined ? JSON.parse(await readStandardInput()) : readJsonInput(requiredOption --input)`. So `--input` last means stdin, and `--input --quiet` fails `requiredOption`, inside the try.
  - `RecordObservedHook(input, e.stderr)`.
  - Quiet: stdout `{"continue":true}\n` exactly (not indented), exit 0.
  - Not quiet: `StringifyIndent({...result.JSON(), limitation: "Observed public Codex lifecycle metadata only; this is not private model-state or intent capture."})`.
  - The `catch`:
    - Not quiet: rethrow, so the CLI prints `FaultLine error: <msg>` and exits 1.
    - Quiet: stderr gets `FaultLine sidecar did not record this hook: <msg>\n`, then `Run fl codex sidecar status in the repository after the turn for a detailed health check.\n`, and stdout `{"continue":true}\n`; exit 0.
  - In the quiet catch, `error.message` of a ZodError is read inside the CLI's catch at that site. Use `jsjson.AtCallSite(jsjson.SiteCLI, err.Error)`, the same site as the CLI's formatter. The stack depth difference is not modelled and never matters for these small issue lists.
  - The `JSON.parse` error of stdin is V8's (`Unexpected end of JSON input` for empty stdin). The `readJsonInput` error is `Unable to read JSON input <file>: <msg>`.
- **Warnings reach stderr on success too.** A quiet SessionStart prints the object-database warning line on stderr and `{"continue":true}` on stdout.
- **Clock.** Each hook invocation is a fresh process. Use `advanceClock` in every multi-invocation case (correction 2), or the second hook's events are timestamped before the first's.

**Writer cases** (`writers.d/4a-sidecar.json`; all but `config` are `posixOnly`, since they run git; the hook JSON goes in `input`, with `cwd` set to `<WS>` (or `<WS>/sub`), which `input` substitutes like argv):

- **config:**
  - `sidecar-config-cli` (init-sidecar, `--cli tool.js`)
  - `sidecar-config-cli-quote` (`--cli "it's/tool.js"`)
  - `sidecar-config-command`, `sidecar-config-command-windows`
  - `sidecar-config-both`, `sidecar-config-neither`
  - `sidecar-config-command-empty` (`--command "  "`: "must not be empty.")
  - `sidecar-config-command-newline` (`--command "a\nb"`)
  - `sidecar-config-windows-with-cli`
  - `sidecar-config-cli-not-js`, `sidecar-config-cli-missing`, `sidecar-config-cli-symlink`
- **install:**
  - `sidecar-install-preview` (exit 1), `sidecar-install-yes`
  - `sidecar-install-twice` (the second invocation refuses the existing document)
  - `sidecar-install-not-a-repo` (empty plus `tool.js`)
  - `sidecar-install-repo-symlink`, `sidecar-install-codex-file` (`.codex` a file), `sidecar-install-no-repo-flag`
  - `sidecar-install-worktree` (sidecar-worktree, `--repo wt`)
- **hook, a session** (sidecar-repo; the invocations of one case run in order with `advanceClock`, and `input` substitutes `<WS>`):
  - `sidecar-hook-session` (SessionStart, not quiet)
  - `sidecar-hook-session-quiet`
  - `sidecar-hook-full-turn` (SessionStart, UserPromptSubmit with `transcript_path` and a prompt, PreToolUse, PostToolUse, Stop; the ledger, receipt, `.git/faultline` objects and cache in the tree)
  - `sidecar-hook-redelivery` (the full turn, then each event again: all idempotent)
  - `sidecar-hook-dirty-stop` (sidecar-dirty: `CHECKPOINT_SKIPPED_DIRTY` with the turn snapshot)
  - `sidecar-hook-tracked-only` (`env: {"FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY": "1"}`)
  - `sidecar-hook-no-head` (sidecar-no-head: the baseline snapshot failure text)
  - `sidecar-hook-subdir-cwd` (`cwd` `<WS>/sub`)
  - `sidecar-hook-worktree` (sidecar-worktree, `cwd` `<WS>/wt`: storage under `main/.git/faultline/recordings`)
- **hook, refusals:**
  - `sidecar-hook-prompt-before-start`, `sidecar-hook-stop-before-prompt`
  - `sidecar-hook-tool-unknown-turn` (correction 11)
  - `sidecar-hook-second-turn-active` (a second UserPromptSubmit while turn 1 is open)
  - `sidecar-hook-model-changed`
  - `sidecar-hook-other-session-same-file` (after a SessionStart for `s-1`, an `inv: 1` mutation `{"op":"rename","file":…,"to":…}` (the existing op, which takes `file`, not `path`) moves `.git/faultline/recordings/codex-6a840baf5d8c3ff241688aeb14546e653774cd5387faf1cb982b0fbbf1fbb810.json` to `codex-450222bcc3475b01aa69d5b0150a75a08a3bc3a87bb87ce4290ea7e0d4e51877.json`, which is `sha256("s-2")`; a SessionStart for `s-2` then gets "belongs to a different Codex session")
  - `sidecar-hook-moved-repo` (sidecar-recorded: the cwd mismatch)
  - `sidecar-hook-unknown-event` (zod discriminator), `sidecar-hook-bad-session-id`
  - `sidecar-hook-prompt-over-stdin-limit` (a prompt of `<REPEAT:a:4194305>` on stdin: the document is over 4,000,000 bytes, so the stdin limit fires before zod)
  - `sidecar-hook-prompt-too-long` (the same document through `--input big.json`, written by an `add-file` mutation with `<REPEAT:a:4194305>`; `readJsonInput` has no limit, so zod's `too_big` on `prompt` is reached)
  - `sidecar-hook-empty-stdin`, `sidecar-hook-empty-stdin-quiet`
  - `sidecar-hook-invalid-json-quiet`, `sidecar-hook-over-limit-quiet`, `sidecar-hook-invalid-utf8`
  - `sidecar-hook-input-file`, `sidecar-hook-input-missing-file`, `sidecar-hook-input-last` (stdin used)
- **hook, storage and locks:**
  - `sidecar-hook-storage-symlink` (`.git/faultline` an `add-symlink`)
  - `sidecar-hook-recordings-file` (`.git/faultline/recordings` a file)
  - `sidecar-hook-ledger-symlink`, `sidecar-hook-receipt-dir` (the receipt path is a directory: refusal after the ledger write)
  - `sidecar-hook-stale-lock` (a planted lock with `set-mtime` 2000-01-01: recovered)
  - `sidecar-hook-fresh-lock` (a planted fresh lock: the "still recording" text after the seeded wait; about 17 s per side, the only such case)
  - `sidecar-hook-lock-dir` (the lock path is a directory: EISDIR is not EEXIST, so the raw Node error)
  - `sidecar-hook-contradictory-receipt` (an `inv: 1` mutation rewrites the receipt after Stop; the redelivered Stop refuses it)
- **status:**
  - `sidecar-status-none`, `sidecar-status-ready` (after a full turn), `sidecar-status-session` (`--session s-1`), `sidecar-status-session-unknown`, `sidecar-status-session-bad`
  - `sidecar-status-invalid-ledger` (a truncated ledger: exit 1)
  - `sidecar-status-contradictory-receipt`, `sidecar-status-invalid-receipt`
  - `sidecar-status-pending-recovery` (the receipt deleted: `RECOVERABLE_FROM_LEDGER`)
  - `sidecar-status-lock-active` (a fresh lock: `ACTIVE`, `<LOCKAGE>`), `sidecar-status-lock-stale`, `sidecar-status-lock-invalid` (the lock a directory)
  - `sidecar-status-not-a-repo` (the git error text), `sidecar-status-two-sessions` (the `localeCompare` order)
- **usage:** `sidecar-usage`, `sidecar-usage-unknown`.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/codex_sidecar_test.go
package cli

import (
	"strings"
	"testing"
)

func TestSidecarHookQuietSwallowsErrors(t *testing.T) {
	var out, errb strings.Builder
	code := RunWithStdin([]string{"codex", "sidecar", "hook", "--quiet"}, strings.NewReader(""), &out, &errb)
	want := "FaultLine sidecar did not record this hook: Unexpected end of JSON input\nRun fl codex sidecar status in the repository after the turn for a detailed health check.\n"
	if code != 0 || out.String() != "{\"continue\":true}\n" || errb.String() != want {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}

func TestSidecarUsage(t *testing.T) {
	_, stderr, code := run("codex", "sidecar")
	if code != 1 || !strings.HasPrefix(stderr, "FaultLine error: Usage: fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>]) | fl codex sidecar install") {
		t.Fatalf("%d %q", code, stderr)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cli/ -run TestSidecar`
Expected: FAIL (`not yet ported: codex sidecar`).

- [ ] **Step 3: Implement; add the writer cases; generate the goldens** (podman, `ONLY='^sidecar-'`; confirm that only `golden/writers.d/4a-sidecar.jsonl` changed). Drop the `unreachableLiterals` entries that `TestGoldensReachEveryErrorLiteral` now reports as reached, and nothing else.

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?sidecar-|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4a-t8.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4a-t8.log | tail -20` (about 4 minutes, most of it the fresh-lock case)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/codex_sidecar.go internal/cli/codex_sidecar_test.go difftest/coverage_test.go difftest/testdata/writers.d/4a-sidecar.json difftest/testdata/golden/writers.d/4a-sidecar.jsonl .github/scripts/windows-writer-skips.txt
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl codex sidecar config, install, status and hook with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Faults, end to end, CI, coverage gate, docs and the done check

**Files:**
- Modify:
  - Tests: `difftest/writerfault_live_test.go` (4a writer paths, the last-invocation fault and per-side receipt stages), `difftest/endtoend_test.go` (`TestEndToEndSlice4aWithoutNode`).
  - Gates: `difftest/coverage_test.go`:
    - `gatedDirs` gains `internal/ledger`, `internal/sidecar` and `internal/snapgc`.
    - `liveGatedFiles` gains `internal/ledger/ledger.go` and `checkpoint.go` → `TestLiveLedgerWriters`; `internal/snapgc/gc.go` → `TestLiveSnapshotGC`; `internal/sidecar/{hook,receipt,lock,inspect}.go` → `TestLiveSidecarHooks`.
    - The `not yet ported: %s` waiver drops `codex` and `record`.
    - Remove the `coverage gate: unported caller` markers in `internal/projinit/sidecar.go` that Task 8 made reachable.
  - Also `difftest/covergate_test.go`, only if a new package needs a build tag.
  - CI: `.github/workflows/go.yml`, `.github/scripts/check-skips.sh` (only if a new skip reason appears).
  - Docs:
    - `difftest/KNOWN_DIFFERENCES.md`;
    - the slice 4 spec (the eleven corrections above, each folded into its section);
    - `docs/superpowers/specs/2026-09-25-go-migration-overview.md` (the slice 4 row: PR 4a done);
    - this plan (tick the boxes).

**Fault matrix** (spec 2.6; Linux, strace, `ENOSPC`/`EIO`/`EACCES`; path discovery from a fault-free TS trace):

| Case | Label | Glob | Groups |
| --- | --- | --- | --- |
| `record-init` | `recordings-dir` | `ws/.faultline/recordings` | mkdir |
| `record-init` | `ledger-stage` | `ws/.faultline/recordings/.s-1.json.<PID>.*.tmp` | open, write, fsync, close, rename |
| `record-checkpoint` | `append-lock` | `ws/ledger.json.lock` | open, close, unlink |
| `record-checkpoint` | `append-stage` | `ws/.ledger.json.<PID>.*.tmp` | open, write, fsync, rename |
| `codex-snapshot-clean` | `snapshot-dir` | `ws/.faultline/recordings` | mkdir |
| `codex-snapshot-clean` | `snapshot-file` | `ws/.faultline/recordings/*.json` | open, write, close |
| `codex-gc-force` | `object-store` | `ws/.git/faultline/objects/*/*` | unlink |
| `codex-gc-force` | `alternates-remove` | `ws/.git/objects/info/alternates` | unlink |
| `codex-gc-alternates-other-entries` | `alternates-rewrite` | `ws/.git/objects/info/alternates` | open, write |
| `sidecar-hook-session-quiet` | `storage` | `ws/.git/faultline` | mkdir |
| `sidecar-hook-session-quiet` | `recordings` | `ws/.git/faultline/recordings` | mkdir |
| `sidecar-hook-session-quiet` | `sidecar-lock` | `ws/.git/faultline/recordings/codex-*.json.sidecar.lock` | open, close, unlink |
| `sidecar-hook-session-quiet` | `sidecar-ledger-stage` | `ws/.git/faultline/recordings/.codex-*.json.<PID>.*.tmp` | open, write, fsync, rename |
| `sidecar-hook-full-turn` (last invocation) | `receipt-stage` | `ws/.git/faultline/recordings/.*.<PID>.tmp` | open, write, close, rename |

The fault harness needs three changes for this table.
- **`faultLastInvocation`.** A map like `faultFirstInvocation`: every invocation runs, and only the last runs under strace and is compared. `runWithFault` gets the invocation index from `writerRun.command` and returns that invocation's result.
- **Receipt stage per side.** The receipt stage key is `sha256(<case root>/ws/.git/faultline/recordings/codex-stop-<key>.json)[0:24]`, so it differs between the TS and the Go case directory (correction 1).
  - An entry flagged `stagePerSide: true` has its discovered `rel` rewritten for each side: the 24-hex component is recomputed from that side's root.
  - Then `<STAGEKEY>` masks the file name in the compared output.
- **Rename through its source.** Renames are faulted through their source path (spec 2.6: on x86_64, `strace -P` matches only `rename(2)`'s first argument).

A group that never fires is dropped from the table, not faked; note which in the commit message.

**End to end without Node** (`TestEndToEndSlice4aWithoutNode`, spec 2.6). It builds `fl` and runs it in a temporary repository with `PATH` set to the git directory only, and no `node`:
1. `fl codex sidecar install --repo . --command "fl codex sidecar hook --quiet" --yes`: `PROJECT_HOOKS_INSTALLED`.
2. `fl record init --session e2e --repo .`: `RECORDING_STARTED`.
3. `fl record attach --turn t-1 --ordinal 1 --prompt-digest sha256:<64 hex> --repo . --checkpoint`: `ATTACHED`, `checkpoint: RECORDED`.
4. Hook events on stdin with `--quiet`: SessionStart, UserPromptSubmit, PreToolUse, PostToolUse and Stop. Each prints `{"continue":true}` and exits 0, and none prints `did not record`.
5. `fl codex sidecar status --repo .`: `SIDECAR_RECORDINGS_READY`, with `latestStop.status: CHECKPOINT_RECORDED` and `receipt: DURABLE`.
6. `fl record verify --ledger <the status ledgerPath>` and `fl record verify --ledger .faultline/recordings/e2e.json`: both `valid: true`, exit 0.
7. `fl codex snapshot gc --repo .`: exit 0, with status `ALREADY_EMPTY` (the clean session took the fast path).

**CI** (`.github/workflows/go.yml`):
- **`go`** (six runners): the 4a writer cases run through `TestWriterReplay`. Windows skips exactly the listed `posixOnly` cases.
- **`difftest-goldens`**: already regenerates everything. `gen-writers.ts` now writes `golden/writers.d/*`, so check that the `git status --porcelain difftest/testdata` line covers the new directory (it does, being under `difftest/testdata`).
- **`difftest-live`**:
  - The new `TestLiveLedgerWriters`, `TestLiveSnapshotGC` and `TestLiveSidecarHooks` join through the `-list 'TestLive'` derivation.
  - Update the step comment with their measured durations. Re-check the 60-minute job timeout against a local measurement in podman; the sidecar test's five fresh-lock sequences cost about 3 minutes.
- **`difftest-live-faults`**: `TestLiveWriterFaults` already runs; add the new rows' expected count to the comment.
- **`difftest-live-replay`**: add `TestEndToEndSlice4aWithoutNode` to its `tests=` list.
- **`stack-live`** (macOS): add `TestLiveSidecarHooks`. macOS `/var` is `/private/var`, so the storage `realpath` walk differs from Linux (spec 4.1).

**KNOWN_DIFFERENCES** (add only what a task confirmed). Expected:
- `<STAGEKEY>` and `<LOCKAGE>` are masked, and why.
- The seeded lock wait takes about 17 s of real time on both sides, which is not an output difference.
- `captureAllowlistedEvent` (`src/redaction.ts:441`) and `src/prevention-from-turn.ts` have no caller in `src/` and are not ported.
- PR #195's batch turn-snapshot staging is not in frozen TS and is deferred past parity.

- [ ] **Step 1: Add the fault rows and the end-to-end test; run them**

Run: `go test -tags faultline_test ./difftest/ -run TestEndToEndSlice4aWithoutNode -v` and, in podman as uid 1000: `FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveWriterFaults/(record-|codex-|sidecar-)' > /tmp/s4a-t9.log 2>&1; grep -E '^(---|FAIL|ok)' /tmp/s4a-t9.log | head -60`. Split the filter by prefix if it runs past 280 s.
Expected: PASS. Fix any mismatch in Go.

- [ ] **Step 2: Apply the CI, gate and docs changes; run the gates**

Run: `go test -tags faultline_test ./difftest/ -run 'TestGoldensReachEveryErrorLiteral|TestWindowsWriterSkipsList|TestNoDirectClockReads' -v 2>&1 | tail -5`. Then run the full gated replay in the background with `FAULTLINE_COVERAGE_GATE=1` (3a Task 13's report has the command), polling its log.
Expected: PASS; the coverage gate reports no missed site in `internal/ledger`, `internal/sidecar`, `internal/snapgc` or the new `internal/cli` files.

- [ ] **Step 3: Full podman regeneration, no drift**

Run the podman command of Global Constraints with no `ONLY`, preceded by `pnpm exec tsx difftest/gen/gen-goldens.ts` and followed by `pnpm exec tsx difftest/gen/gen-corpus.ts` inside the container, as `difftest-goldens` does. Then: `git status --porcelain difftest/testdata`
Expected: empty output. Any drift is a nondeterminism bug to fix in the harness or Go, not a golden to commit.

- [ ] **Step 4: Commit; hand back to the controller to push** (the controller pushes as Mizore66 and reads CI)

```bash
git add .github difftest docs internal/projinit
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "ci(go): slice 4a faults, end to end, coverage gate and docs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Done criteria (spec 4.2, PR 4a):
1. `go test ./...` passes on all six runners, and every `record-`, `codex-` and `sidecar-` writer case matches its golden.
2. `difftest-live`, `difftest-live-faults` and `stack-live` pass, including `TestLiveLedgerWriters`, `TestLiveSnapshotGC`, `TestLiveSidecarHooks` and the 4a fault rows.
3. `TestEndToEndSlice4aWithoutNode` passes.
4. Point 4 names no artifact kind that 4a writes. The ledgers, receipts and hook documents 4a writes are compared byte for byte with TS's, which implies they verify under TS. Every replayed `record verify` and `codex sidecar status` invocation also checks them with each side's own verifier.
5. Point 5 applies to PR 4b.
6. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference.
7. The overview marks PR 4a done in the slice 4 row.

Adversarial review rounds of about 90 minutes each follow, until a round finds no P0 or P1.
