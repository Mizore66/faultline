# Go Slice 4b (turn proofs) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs `investigate turns` (with `--latest`, `--minimize`, `--transition`, `--max-executions`, `--minimization-output`, `--runtime-mapping`, `--output`), `prove transition`, and the turn branch of `fl verify`. It writes and verifies turn proof bundles. For each command, stdout, stderr, exit status, the files it writes (with their modes) and the git and Docker argv are byte-identical to frozen TS `node dist/cli.js`. Writer goldens, the hostile-verifier turn corpus and live Node-oracle tests prove it.

**Architecture:** Each TS module becomes one Go package or file:
- `internal/turninv`: `src/turn-investigation.ts`. That is the schemas, state extraction from the ledger, stable-state reconstruction, transitions, failure attribution, run facts, and the two-phase `investigateTurnTrees`.
- `internal/bundle/turnproof`: `src/turn-proof-bundle.ts`. That is the uncapped hash catalog, the tree pack writer and verifier, the proof semantics, the lifecycle binding, the write-once writer and the verifier.
- `internal/minimize/bridge.go`: `src/turn-minimization-bridge.ts`. It runs unhardened `git` through `spawnSync` with the inherited environment.
- `internal/cli/{turnmin,turns,prove,verify}.go`: the commands. `turnmin.go` holds `minimizeFromTurnInvestigation` (`cli-app.ts:2239-2288`), which `investigate turns --minimize` and `prove transition` share.

The slice 3 and 4a harness is extended, not replaced. It gets:
- content-addressed `<COMMIT:…>`/`<TREE:…>`/`<BLOB:…>` names for the Git objects a case creates. This is the same rule as PR 4c's Task 2, so the controller keeps one copy.
- turn scenarios built from frozen TS ledger functions with fixed ids and times;
- a deterministic turn proof base, `turn-two-states`, plus `turn-non-monotonic`. Both are built by `difftest/gen/gen-bases.ts`.
- the 15 hostile-verifier cases as `mutations.json` templates on those bases;
- three new git-diagnostic masks for the turn pack verifier.

**Tech Stack:** Go 1.27 (standard library only). TypeScript generators run with the repo's `tsx`. Node 22.22.2, git, and the fake Dockers in `difftest/testdata/fakedocker`. No real Docker and no `codex` binary.

**Spec:** [docs/superpowers/specs/2026-10-27-go-slice4-turns-codex-design.md](../specs/2026-10-27-go-slice4-turns-codex-design.md) (PR 4b scope; Sections 2 and 3 apply). Read it before any task. Also read:
- the [slice 4a plan](2026-10-27-go-slice4a-recording.md). PR 4b builds on its harness (Task 1) and consumes the Produces blocks of its Task 4 (`internal/ledger`) and Task 6 (`internal/sidecar`).
- the [slice 3b plan](2026-10-26-go-slice3b-incident-prevention.md).
- the PR 4c plan's Task 2 (`docs/superpowers/plans/2026-10-27-go-slice4c-codex-repair.md` on `go-migration/slice4c`), whose object-mask rule Task 1 here reproduces.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- **JSON.**
  - Never use `encoding/json` on a value that is hashed, compared canonically, written or printed.
  - `JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`.
  - `canonicalJson` is `canonical.CanonicalJSON`, `digestJson` is `canonical.DigestJSON` and `sha256(text)` is `canonical.SHA256Hex` (`SHA256HexBytes` for a Buffer).
  - Turn bundle artifacts are `canonicalJson(value) + "\n"` of the zod-parsed value (`jsonBytes`, `turn-proof-bundle.ts:183`). zod output follows the schema's shape order, and so does `schema.Parse`.
  - Stdout is `JSON.stringify(…, null, 2)` in the TS object-literal order, with `undefined` members left out.
- **Frozen TS.**
  - `src/`, `package.json` and `pnpm-lock.yaml` are never modified.
  - The oracle is `node dist/cli.js` built from the current `src/`, which equals `ce99a79` (`v0.1.10-buildweek`), on Node 22.22.2.
  - Final goldens and scenario trees come from podman `localhost/r73img` (Node 22.22.2, `/usr/bin/timeout`, `tsx` under `/src/node_modules`) running as uid 1000. Command (from the worktree root; it works on a copy so the bind mount stays read-only):

    ```bash
    podman run --rm --user 1000:1000 -v "$PWD":/w:ro -v "$PWD/difftest/testdata":/out localhost/r73img bash -c '
      cp -a /w /tmp/r && cd /tmp/r && ln -s /src/node_modules node_modules && pnpm build >/dev/null &&
      pnpm exec tsx difftest/gen/gen-bases.ts turn-two-states turn-non-monotonic >/dev/null &&
      FAULTLINE_GOLDEN_BASES="${BASES:-turn-two-states,turn-non-monotonic}" pnpm exec tsx difftest/gen/gen-goldens.ts &&
      pnpm exec tsx difftest/gen/scenarios.ts >/dev/null &&
      FAULTLINE_WRITERS_ONLY="${ONLY:-.}" pnpm exec tsx difftest/gen/gen-writers.ts &&
      cp -a difftest/testdata/. /out/'
    ```

    `-e ONLY='^(turns-)'` (or another regex) regenerates one task's writer cases. Run `chmod -R a+rwX difftest/testdata` first so uid 1000 can write to `/out`. Afterwards run `git diff --stat`, and restore modes with `git checkout -- <path>` on anything you did not mean to change.
- **Strings.**
  - JS strings are WTF-8 (`internal/jsstr`). TS `slice`/`length`/`padStart` count UTF-16 units.
  - `localeCompare` is `canonical.SortLocale` or `canonical.LocaleCompare`. A bare `.sort()` or a string `>` is `jsstr.CompareUTF16`.
  - JS regexes are `jsre.MustCompile(source, flags)`. In a JS regex, `.` excludes `\n`, `\r`, U+2028 and U+2029.
  - `.trim()` is `jsstr.Trim`. `.trimEnd()` needs `jsstr.TrimEnd`, which Task 5 adds. `toString("utf8")` is `jsstr.DecodeUTF8`.
  - Every ported function carries a comment naming its TS source line.
- **Numbers.** `Number(x)` is `jsjson.ToNumber`. `Number.isInteger(n)` is `!IsInf && !IsNaN && n == math.Trunc(n)`. `-0` survives `Number("-0")` and passes `n < 0 === false`. `String(n)` and template interpolation are `jsjson.FormatNumber`.
- **Time and child processes.**
  - Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock`. `Date.now()` is `clock.NowMillis`, `new Date()` is `clock.Now`, and `randomUUID()` is `clock.NewUUID`. `TestNoDirectClockReads` enforces this.
  - `fs.mkdtempSync` is `nodefs.Mkdtemp`. `fs/promises.mkdtemp` is `nodefs.MkdtempAsync`. They share the seeded suffix counter, so their call order must match TS.
  - Child processes start only through:
    - `nodeproc.SpawnSyncWith` (`spawnSync`);
    - `gitinv.RunHardenedGit` (`runHardenedGit`, async, 30 s, 64 MiB);
    - `sandbox.Execute`.

    Each Go call site must match its TS call site.
- **Commits.**
  - `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`. Never plain `gh`.
  - End each commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
  - TruffleHog pre-commit hook: never bypass it.
- **Output.** `Next: pnpm fl help` and the TS `note`/`next` strings stay verbatim, including `→` (U+2192) and `—` (U+2014).
- **Git's own diagnostics in goldens.** `difftest-goldens` regenerates on ubuntu-24.04 (git 2.43), while podman `r73img` has git 2.39.5. A golden may quote a git message only after `maskGitDetail` has replaced it, or when the text is the same in both versions. Task 2 adds the turn verifier's prefixes to the mask.
- **Shell commands.** Each must finish in under 5 minutes: `timeout 280 … > log 2>&1; tail`. macOS has no `timeout`: use `perl -e 'alarm 280; exec @ARGV' …` or podman. The full TestWriterReplay takes over 40 minutes, so run filtered subsets locally; CI runs the full suite.
- **Shared files.** Several tasks append to these, each at the end:
  - `.github/scripts/windows-writer-skips.txt` (`merge=union` since 4a Task 1);
  - the import block and the `handlers` spread in `difftest/gen/node-oracle.ts`, one import line and one spread line per task, as 4a's op modules do;
  - `difftest/testdata/mutations.json` (Task 2 only).

  Every other file is owned by exactly one task, as its **Files** block says.
- **Relation to 4a.** This branch is rebased onto 4a's integration branch once 4a's Tasks 1, 4 and 6 have merged.
  - Tasks 2, 3, 4, 5, 6 and 7 use no 4a package. They can start on the current base, `eba43ad`.
  - Task 1 needs 4a Task 1: `writers.d/`, the `input`/`advanceClock`/`inv` template keys, and the op-module pattern.
  - Task 8 needs 4a Task 6 (`sidecar.ResolveLatestLedgerPath`).
  - Task 10's end-to-end test needs 4a Tasks 7 and 8 (`fl record`, `fl codex sidecar`).

## Corrections to the spec found while planning

Task 11 folds these into the spec.

1. **The turn hash catalog's messages are not Git's (spec 2.4).** The spec quotes "must end with a newline" and "invalid hash catalog entry", but those are the Git verifier's texts (`git-proof-bundle.ts:865-869`). `parseHashCatalog` (`turn-proof-bundle.ts:390-410`) has its own texts and rules:
   - `hashes.txt must end with a trailing newline`.
   - It iterates `text.trimEnd().split("\n")` and does not filter empty strings. So an empty catalog, and every blank line inside one, gives `hashes.txt contains a malformed line: ` with an empty line. JS `trimEnd` also strips trailing spaces, `\r`, U+2028 and U+FEFF from the last line before it is matched.
   - It matches `/^([a-f0-9]{64})  (.+)$/`, where `.` excludes `\r`, U+2028 and U+2029. A miss gives `hashes.txt contains a malformed line: <line>`.
   - An unsafe path gives `hashes.txt contains an unsafe artifact path: <line>`. The whole line is quoted, not the path.
   - A duplicate gives `hashes.txt declares duplicate artifact: <artifact>`, but the duplicate is not skipped: `Map.set` replaces the digest and keeps the first insertion position.
   - There is no entry cap. A 2,100-line catalog yields one `declared artifact cannot be read safely (…)` and one `hashes.txt contains an unexpected artifact: …` per extra line.
2. **`--expect-root ""` is "provided" for a turn bundle.** The turn verifier tests `expectedRoot === undefined` (`:818`), not truthiness as the demo and Git verifiers do. So `--expect-root ""` gives `externalRootStatus: "MISMATCH"` and `externally supplied turn proof root is not a sha256 digest`. Go passes `option`'s `ok` as `rootProvided`.
3. **SHA-256 turn bundles never verify, and the bridge refuses SHA-256 trees.**
   - The pack verifier's `git init --bare` runs with the hardened environment, which does not pass `GIT_DEFAULT_HASH` (`git-materialization.ts:72-96`). Its bare repository is therefore always SHA-1, and a SHA-256 pack fails `unpack-objects` and `index-pack`.
   - The bridge parses every `rev-parse` and `commit-tree` output with `/^[a-f0-9]{40}$/` (`turn-minimization-bridge.ts:9`). A SHA-256 tree therefore throws a ZodError (`Invalid` at the root).
   - Go reproduces both; `difftest/testdata/base-env.json` gets no turn entry.
4. **The bridge runs six `git` processes, unhardened** (`turn-minimization-bridge.ts:21-32, 54-109`):
   1. `rev-parse --verify --end-of-options <before>^{tree}`
   2. the same for `<after>`
   3. `commitTree`'s own `rev-parse` of `<beforeTree>^{tree}`
   4. `-c user.name=FaultLine -c user.email=faultline@example.invalid commit-tree <tree> -m <message>`
   5. and 6. the same two for the after tree.

   Each is `spawnSync("git", ["-C", repo, ...args], {encoding: "utf8", windowsHide: true, maxBuffer: 16 MiB})`, with the full inherited environment and no timeout.
   - A spawn error throws Node's error itself (`spawnSync git ENOENT`).
   - A non-zero or null status throws `stderr.trim() || "git <args> failed"`.
5. **The minimization output is unmanaged.**
   - `minimizeFromTurnInvestigation` writes to `resolve(output ?? .faultline/minimizations/turn-transition-<NNNN>-<Date.now()>.json)`. It does not apply `minimize git`'s `safeManagedFileOutput`, so any path is accepted.
   - The flag is `--minimization-output` in `investigate turns --minimize` and `--output` in `prove transition`. In `investigate turns`, `--output` names the bundle.
6. **`prove transition`'s order** (`cli-app.ts:2294-2356`):
   1. It verifies the bundle (`mkdtemp` and git) before it reads `--proposal`, so a missing `--proposal` is reported after the verification.
   2. It re-reads `investigation.json` with a plain `JSON.parse` and a schema parse.
   3. Its session binding write passes no `ledgerPath`, so a previously bound ledger path is kept.
   4. It never sets `process.exitCode`, so it exits 0 whatever the minimization concluded (spec 3.4).
7. **`investigate turns`' order** (`cli-app.ts:2052-2124`):
   1. The binding is read (`resolveInheritedSessionFacts`) before the `--latest`/`--ledger` conflict check.
   2. `--latest` wins over a bound ledger path.
   3. The image is resolved before the binding write.
   4. The frozen witness is read after the binding write (spec 3.4).
   5. The lifecycle ledger is read again (`readVerifiedCodexLifecycleLedger`) after the output path is computed and before the writer runs.
   6. `Date.now()` is read for `turns-<ms>` only when `--output` is absent.
8. **`trees.pack` bytes follow git and zlib, like `descendant.bundle`.** `pack-objects --stdout --compression=9` of the same sorted id list gave identical bytes on git 2.39.5 (podman `r73img`) and git 2.54.0 (macOS) for the probe tree. The existing KNOWN_DIFFERENCES entry "Git bundle bytes across git versions" is extended to the turn pack.
9. **Two temporary-directory calls share the seeded suffix counter.**
   - The investigation's root uses `fs/promises.mkdtemp` (`nodefs.MkdtempAsync`). The pack verifier uses `mkdtempSync` (`nodefs.Mkdtemp`).
   - The verifier runs twice per `investigate turns`: once on the stage, once from the CLI. So the call order fixes every later suffix.
   - The verifier's cleanup is `rmSync(…, {recursive, force, maxRetries: 3, retryDelay: 50})` on POSIX, and `20`/`250` with `EPERM`/`EBUSY` swallowed on Windows. Go gets `nodefs.RemoveAllRetry` (Task 5).
10. **Golden masks.**
    - The turn verifier and writer quote git's stderr as `<label>: <detail>`, not `<label> failed: <detail>`. Its temporary bare repository is `faultline-turn-proof-pack-*`.
    - `maskGitOutput` (both twins) gains these labels, all in the `": "` form:
      - `Turn tree pack is not a valid Git pack`
      - `Could not create temporary bare repository for pack verification`
      - `Could not enumerate objects for turn tree <oid>`
      - `Could not pack turn trees`

      It also gains the second `<GITTMP>` marker (Task 2).
11. **Hostile-corpus case 4 is platform-dependent.**
    - `C:/outside` is a safe POSIX-relative path to `isSafeRelativeArtifactPath`. On Linux it reads as a missing file (ENOENT). With `path.win32` it escapes the bundle (`Artifact path escapes its bundle`).
    - Mutation templates gain `"posixOnly": true`, which the Go replay skips on Windows only. The skip guard already allows `TestGoldenReplay/.+` there.
12. **Duplicate JSON keys (corpus case 6).**
    - `JSON.parse` keeps the last value. A manifest whose duplicated key ends with the valid value verifies VALID: this is documented parser behaviour, not a fail-closed case.
    - `fl verify`'s `schemaVersion` sniff uses the same last-wins rule, so a manifest whose last `schemaVersion` is Git's is routed to the Git verifier.
13. **`runtimeMapping` is `z.record(DigestSchema, DigestPinnedImageSchema)`**, so zod also validates each key: the key issues come first, then the value issues, both at path `[…, key]`. `internal/schema` gains `RecordOf(key, value)` (Task 3).
14. **Two stack-dependent limits need a new call site.**
    - The turn verifier is an `async` function, so V8's stack under its zod `.message` reads and its `errors.push(...semantics)` spread differs from the Git verifier's.
    - `jsjson` gains `SiteTurnProof` and `SpreadTurnSemantics`.
    - They are measured by extending `TestLiveStackThresholdsMatchNode` and `TestLiveSpreadLimitsMatchNode` (Task 7). Until the arm64 and darwin rows are measured by `stack-live`, they default to the Git verifier's values.

## Execution waves

Tasks in one wave touch disjoint files, apart from the append-only shared files in Global Constraints. A task starts once every task it depends on has merged.

- **Wave 1 (no 4a package needed):**
  - Task 2: the turn proof bases, the hostile corpus and the golden masks.
  - Task 3: `internal/turninv` schemas, state reconstruction and its live oracle.
  - Task 5: the uncapped hash catalog, the tree pack, `RemoveAllRetry`, `TrimEnd` and the catalog oracle.
  - Task 1 (writer harness for 4b) joins this wave as soon as 4a Task 1 has merged; it needs no 4b task.
- **Wave 2:**
  - Task 4: the bridge and `minimizeFromTurnInvestigation`; needs 3.
  - Task 6: `investigateTurnTrees`; needs 3.
  - Task 7: turn proof semantics, the verifier and the `fl verify` turn branch; needs 2, 3 and 5.
- **Wave 3:** Task 8, the turn proof bundle writer; needs 7.
- **Wave 4:**
  - Task 9: `fl investigate turns`; needs 1, 4, 6, 8 and 4a Task 6.
  - Task 10: `fl prove transition`; needs 1, 4 and 7. Its code and unit tests start in this wave, but its writer goldens use `investigate turns` as invocation 0, so its golden step waits for Task 9 to merge.
- **Wave 5:** Task 11, after all of them: the fault matrix, the 4b end to end without Node, CI, the coverage gate, KNOWN_DIFFERENCES, docs and a full podman regeneration with no diff. Its end-to-end test also needs 4a Tasks 7 and 8.

Dependencies (`A → B`: B needs A):

```
4a T1 → 1        4a T6 → 9        4a T7, T8 → 11
1 → 9, 10        2 → 7            3 → 4, 6, 7       5 → 7
4 → 9, 10        6 → 9            7 → 8, 10         8 → 9
9 → 10 (goldens only)             every task → 11
```

Tasks that consume 4a packages: Task 1 (4a Task 1's harness), Task 9 (`sidecar.ResolveLatestLedgerPath`, `sidecar.Latest`) and Task 11 (the e2e uses `fl codex sidecar hook` and `fl record verify`). The `internal/ledger` writers are consumed only by test code in Tasks 6, 9 and 11 (`ledger.Create`, `ledger.Append`, `ledger.WriteAtomic`), which builds ledgers. Until 4a Task 4 merges, Tasks 3 and 6 build their test ledgers with the frozen TS through the oracle, or with `difftest/gen/ledger-seed.ts`.

## File Structure

```
internal/jsstr/jsstr.go                    + TrimEnd (String.prototype.trimEnd)                           Task 5
internal/nodefs/rm.go                      + RemoveAllRetry (rmSync maxRetries/retryDelay)                Task 5
internal/schema/composite.go               + RecordOf(key, value) (z.record with a key schema)            Task 3
internal/jsjson/stack.go                   + SiteTurnProof, SpreadTurnSemantics                           Task 7
internal/turninv/schemas.go                turn-investigation.ts:47-252 schemas, constants                Task 3
internal/turninv/states.go                 :343-529 turnStatesFromLedger, duplicates, attribution, stable, transitions   Task 3
internal/turninv/investigate.go            :254-341, :531-868 run facts, baseResult, investigateTurnTrees  Task 6
internal/bundle/turnproof/catalog.go       turn-proof-bundle.ts:62-78, 175-410 limits, paths, file walks, parseHashCatalog   Task 5
internal/bundle/turnproof/pack.go          :412-495 packTurnTrees, verifyTreesPack, removeTemporaryDirectory   Task 5
internal/bundle/turnproof/schemas.go       :59-140 manifest and source schemas                            Task 7
internal/bundle/turnproof/semantics.go     :311-356, :497-690 artifact sets, ids, bindTurnLifecycle, semantics   Task 7
internal/bundle/turnproof/verify.go        :808-972 verifyTurnInvestigationProofBundle                    Task 7
internal/bundle/turnproof/write.go         :195-310, :358-378, :692-806 output preparation, readme, manifest, writer   Task 8
internal/minimize/bridge.go                turn-minimization-bridge.ts                                    Task 4
internal/cli/turnmin.go                    cli-app.ts:2239-2288 minimizeFromTurnInvestigation             Task 4
internal/cli/verify.go                     turn branch (cli-app.ts:2927-2939)                             Task 7
internal/cli/turns.go                      cli-app.ts:2045-2160                                           Task 9
internal/cli/investigate.go                `turns` → turnsCommand (one line)                              Task 9
internal/cli/prove.go, internal/cli/cli.go cli-app.ts:2294-2356, 3038-3044                                Task 10
difftest/gen/gen-writers.ts, difftest/writer_test.go, difftest/masks_test.go   created-object names (4c Task 2's rule)   Task 1
difftest/gen/scenarios.ts, difftest/scenarios_test.go     turn-* scenarios                                Task 1
difftest/gen/gen-bases.ts, difftest/gen/fixtures.ts       turn-two-states, turn-non-monotonic bases       Task 2
difftest/gen/cases.ts, difftest/cases.go, difftest/replay_test.go   posixOnly key, turn git labels, pendingTypes   Task 2
difftest/testdata/bases/turn-*, golden/turn-*.jsonl, fixtures/turn-*.pack, mutations.json (appended)   Task 2
difftest/gen/oracle-turninv.ts, oracle-turnproof.ts, oracle-bridge.ts   one op module per task            Tasks 3/6, 5/7/8, 4
difftest/turninv_live_test.go, turnproof_live_test.go, turnbridge_live_test.go   live tests               Tasks 3, 4, 5, 6, 7, 8
difftest/testdata/writers.d/4b-*.json, golden/writers.d/4b-*.jsonl     one pair per CLI task              Tasks 1, 9, 10
```
