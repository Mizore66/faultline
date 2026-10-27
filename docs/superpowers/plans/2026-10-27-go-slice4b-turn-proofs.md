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

---

### Task 1: Writer harness for 4b: created Git object names and turn scenarios

**Files:**
- Modify (TS side): `difftest/gen/gen-writers.ts` (`createdObjects`, `objectNames`, the loose-object and reflog rules, and the object seeds in `relocation`), `difftest/gen/scenarios.ts` (the `turn-*` scenarios)
- Modify (Go side): `difftest/writer_test.go` (the twins), `difftest/scenarios_test.go` (`writerScenarios`)
- Create: `difftest/masks_test.go`, the `turn-*` scenario trees under `difftest/testdata/scenarios/`

**Interfaces:**
- Consumes:
  - 4a Task 1: `writers.d/`, the `advanceClock` and `inv` template keys, and `stdoutArgument` (`<STDOUT:n:/pointer>`).
  - The slice 3 writer replay: `relocation`, `loggedCasePaths`, `normalizeWriter`, `buildRepo`.
  - Frozen TS: `createCodexLifecycleLedger`, `appendLifecycleEvent` and `signTurnTreeSnapshot` (`src/turn-snapshot.ts:290`).
- Produces (identical to PR 4c Task 2's Produces, minus its two repair seeds; the controller keeps whichever copy merges first):
  - `createdObjects(t testing.TB, ws string, before map[string]map[string]bool) map[string]map[string]string`. The keys are repository directories relative to the workspace, then object ids, mapped to their type.
  - `objectSet(t testing.TB, repoDir string) map[string]bool`.
  - `objectNames(t testing.TB, repoDir string, created map[string]string, text func(string) string) map[string]string`. It maps an id to `<BLOB:h12>`, `<TREE:h12>` or `<COMMIT:h12>`.
  - The TS twins `createdObjects`, `objectSet` and `objectNames` in `gen-writers.ts`.
  - `relocation(…)` takes the created object ids as seeds. A file that holds one gets a `<PATHDIGEST:file:…>` name, and so does every digest over it.
  - Scenarios (in `writerScenarios`, built by `scenarios.ts` with frozen TS functions and fixed times):
    - `turn-two-states`: `gitScenario([state("good"), state("bad")])`, the witness frozen into `repo/.faultline/incidents` as `git-two-states` has it, plus `ledger.json`:
      - `SESSION_STARTED` with `workingDirectory` `/work/repo`;
      - `SESSION_BASELINE_SNAPSHOT` of tree 0;
      - turn `turn-1` started and completed;
      - `TURN_TREE_SNAPSHOT` of tree 1.

      Each snapshot is `signTurnTreeSnapshot({schemaVersion, repositoryRoot: "/work/repo", headCommit: commits[0], treeDigest: trees[i], capturedAt, dirty: i > 0, statusDigest: "sha256:" + sha256("status " + i)})`. Ledger and event ids are fixed, and times are `2026-07-16T11:00:00.000Z` plus one second per event, before the seed epoch.
    - `turn-non-monotonic`: states good, bad, good, bad (a baseline and three turns).
    - `turn-pass-only`: good, good (no transition).
    - `turn-baseline-only`: one snapshot (`NO_TURN_SNAPSHOTS`).
    - `turn-duplicate-ordinal`: turn 1 completed, then two `TURN_TREE_SNAPSHOT`s with ordinal 1 (trees 1 and 0). The ledger verifier accepts this, because each snapshot only has to match the completed ordinal (`ledger.ts:505`), so `appendLifecycleEvent` builds it and `investigate turns` reaches `DUPLICATE_TURN_SNAPSHOTS`.
    - `turn-heterogeneous`: state 1 adds `package.json`, and `mapping.json` maps both fingerprints.
    - `turn-missing-tree`: `turn-two-states` whose turn snapshot names a tree id that is not in the repository: `"e".repeat(40)`, signed. Materializing it fails, which gives `EXECUTION_ERROR`.
    - `turn-sha256`: `turn-two-states` built with `objectFormat: "sha256"`, so its snapshots carry 64-hex trees (correction 3).
    - `turn-sidecar`: `turn-two-states` with the ledger under `.git/faultline/recordings/codex-<sha256("s-1")>.json` instead of `ledger.json`, for `--latest`. Its `.gitignore` holds `.faultline/`.
    - `turn-latest-ambiguous`: two valid recordings whose last events share one `occurredAt`.

**Gotchas:**
- **The object rule is 4c Task 2's, word for word.**
  - **Listing.** For each `.git` directory in the workspace, run `git cat-file --batch-all-objects --batch-check='%(objectname) %(objecttype)'`:
    - before the first invocation, and after each one;
    - with the harness's own git environment, not fl's;
    - outside the argv log, as `gitIndexDigest` does.

    The new ids are the difference.
  - **Naming,** on each side, from `git cat-file -p` on that side, in dependency order (blobs, then trees, then commits). Each object is named from text in which the case paths and every already-named id are masked:
    - **blob:** `<BLOB:` + the first 12 hex digits of `sha256("blob\n" + text(content))` + `>`.
    - **tree:** each entry `<mode> <type> <id or its name>\t<name>`, joined with `\n`, hashed as `"tree\n" + …`.
    - **commit:** the tree's name, then each parent's name (or its id when the parent existed before the case), the `author` and `committer` lines with their trailing `<seconds> <zone>` removed, and the message. Hashed as `"commit\n" + …`.

    So a bridge commit with a wrong tree, identity or message gets a different name and fails, as spec 2.2 requires.
  - **Unmasked ids.** An id that existed before the case is never masked. That covers every turn tree the scenario's commits hold, so `trees.pack`, `investigation.json` and the ledger keep their exact ids.
  - **Loose objects.** A loose object new to the case, under `<repo>/.git/objects/xx/…`, is hashed as `sha256("git-object:" + its name)`. Loose objects that existed before keep their byte digest.
  - **Reflogs.** A new or changed `.git/**/logs/**` file has each line's `<seconds> <zone>\t` replaced with `<TIME>\t` before hashing. No 4b command writes a reflog (`commit-tree` updates no ref), so this rule only keeps the twins identical to 4c's.
- **Why 4b needs it.**
  - The bridge's `commit-tree` (correction 4) takes its author and committer time from the wall clock: the inherited environment carries no `GIT_*_DATE`, and the harness drops every inherited `GIT_` name.
  - So `beforeCommit`/`afterCommit`, the minimization run facts that name them, the result file and its `resultDigest` all vary between runs.
  - The bridge writes two loose commit objects into `repo/.git/objects`.
- **Existing goldens.** No existing case creates objects, so every existing golden stays byte-identical. Step 4 checks that.
- **Scenario bytes are path-independent.** The ledger's `workingDirectory` and the snapshots' `repositoryRoot` are the literal `/work/repo`. Only `investigation.repository` (fl's `resolve(--repo)`) holds the case path, and `relocation` already masks it.

**No writer cases.** A mutation cannot run a command, so the object rule is tested by `TestBridgeCommitNamesIgnoreTimeButNotTreeOrMessage` below and by `checkObjectNames()`, its TS twin, which `gen-writers.ts` runs on start-up. The bridge cases in Tasks 9 and 10 exercise the rule end to end. `TestTurnScenariosVerify` checks the scenario ledgers.

- [ ] **Step 1: Write the failing tests**

```go
// difftest/masks_test.go
//go:build faultline_test && unix

package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// gitIn runs the harness's git in dir (identity from the arguments only).
func gitIn(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=FaultLine", "-c", "user.email=faultline@example.invalid"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestBridgeCommitNamesIgnoreTimeButNotTreeOrMessage(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "", "init", "-q")
	blob := gitIn(t, repo, "bad\n", "hash-object", "-w", "--stdin")
	tree := gitIn(t, repo, "100644 blob "+blob+"\tstate.txt\n", "mktree")
	before := map[string]map[string]bool{".": objectSet(t, repo)}
	msg := "faultline-turn-min after t1 (" + tree[:12] + ")"
	c1 := gitIn(t, repo, "", "commit-tree", tree, "-m", msg)
	time.Sleep(1100 * time.Millisecond) // a different committer second
	c2 := gitIn(t, repo, "", "commit-tree", tree, "-m", msg)
	c3 := gitIn(t, repo, "", "commit-tree", tree, "-m", "faultline-turn-min before t0 ("+tree[:12]+")")
	created := createdObjects(t, repo, before)["."]
	names := objectNames(t, repo, created, func(s string) string { return s })
	if c1 == c2 || names[c1] == "" || names[c1] != names[c2] || !strings.HasPrefix(names[c1], "<COMMIT:") {
		t.Fatalf("c1 %q c2 %q", names[c1], names[c2])
	}
	if names[c3] == names[c1] {
		t.Fatal("a different message must give a different name")
	}
	if _, ok := names[tree]; ok {
		t.Fatal("a tree that existed before the case is never masked")
	}
}

func TestTurnScenariosVerify(t *testing.T) {
	repo := repoRoot(t)
	for _, name := range []string{"turn-two-states", "turn-non-monotonic", "turn-pass-only", "turn-baseline-only", "turn-duplicate-ordinal", "turn-heterogeneous"} {
		r := makeWriterRoots(t)
		copyScenario(t, repo, name, r.ws)
		text, err := nodefs.ReadText(filepath.Join(r.ws, "ledger.json"))
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsjson.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if got := gitproof.VerifyCodexLifecycleLedger(v); !got.Valid {
			t.Errorf("%s: %v", name, got.Errors)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run 'TestBridgeCommitNames|TestTurnScenariosVerify' -v`
Expected: FAIL (undefined `objectSet`, `createdObjects`, `objectNames`; unknown scenario `turn-two-states`).

- [ ] **Step 3: Implement** both twins and the scenarios, and call the twins from `runCase` and `runWriterCaseWith`. Record the object sets after `prepareWriterCase` and before the first invocation. Every Go twin names its TS twin in a comment.

- [ ] **Step 4: Regenerate in podman and check that no existing golden moved**

Run the podman command from Global Constraints with no `ONLY`, in the background with a polled log (about 40 minutes). Then: `git status --porcelain difftest/testdata | grep -v 'scenarios/turn-'`
Expected: empty output.

- [ ] **Step 5: Run to verify the tests pass**

Run: `go test -tags faultline_test ./difftest/ -run 'TestBridgeCommitNames|TestTurnScenariosVerify|TestNormalizeWriter|TestWindowsWriterSkipsList|TestNoDirectClockReads' -v > /tmp/s4b-t1.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4b-t1.log | tail`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(difftest): created Git object names and turn scenarios for slice 4b

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Turn proof bases, the hostile-verifier turn corpus and the turn git masks

**Files:**
- Modify (TS side):
  - `difftest/gen/fixtures.ts`: `createTurnRepository`, `turnLedger`, `nativeTurnFixture`, `createTurnFrozenWitness`.
  - `difftest/gen/gen-bases.ts`: `turnBase` and two calls.
  - `difftest/gen/cases.ts`:
    - the `pad-file` and `replace-pack` ops (both take `file`, so neither joins `PATH_OPS`);
    - the `resignTurn` and `posixOnly` template keys;
    - `TURN_GIT_LABELS` in `maskGitOutput`;
    - the `faultline-turn-proof-pack-` `<GITTMP>` marker;
    - `canonicalText`, exported beside `canonicalDigest`.
- Modify (Go side): `difftest/cases.go` (the twins), `difftest/cases_test.go`, `difftest/replay_test.go` (`deferredTypes`, and the `posixOnly` skip on Windows).
- Append: `difftest/testdata/mutations.json`.
- Create:
  - `difftest/testdata/bases/turn-two-states/`, `difftest/testdata/bases/turn-non-monotonic/`;
  - `difftest/testdata/golden/turn-two-states.jsonl`, `golden/turn-non-monotonic.jsonl`;
  - `difftest/testdata/fixtures/turn-ancestor-only.pack`, `fixtures/turn-not-a-pack.bin`;
  - `difftest/testdata/fakegit/fail-unpack/git`, `fakegit/fail-unpack-index/git`.

**Interfaces:**
- Consumes: frozen TS `investigateTurnTrees`, `writeTurnInvestigationProofBundle`, `createCodexLifecycleLedger`, `appendLifecycleEvent`, `signTurnTreeSnapshot`; `fixtures.ts`'s `createFrozenWitness`, `deterministicDockerRunner`, `pinnedImage`.
- Produces:
  - Bases (deterministic: a rerun of `gen-bases.ts turn-two-states turn-non-monotonic` gives byte-identical directories; Task 11 makes `difftest-goldens` check that):
    - `turn-two-states`: states good and bad. 2 states, 6 runs, 1 `PASS_TO_FAIL` transition, `introduction.status` `ATTRIBUTED`. 14 declared artifacts, 16 physical files.
    - `turn-non-monotonic`: good, bad and good. 3 states, 9 runs, 2 transitions, `nonMonotonic: true`.
  - The mutation template keys and ops, on both twins:
    - `{"op":"pad-file","file":f,"total":n}`: append `0x20` bytes until `f` is exactly `n` bytes; fails if it is already longer.
    - `{"op":"replace-pack","file":"source/trees.pack","from":fixture}`: copy the fixture over the pack, then set `source/trees.metadata.json` `pack.digest` (`"sha256:" + sha256(bytes)`) and `pack.bytes`, writing it as `canonicalText(v) + "\n"`.
    - `"resignTurn": true` runs after the template's edit and before `rehash`. See Gotchas.
    - `"posixOnly": true`: the Go replay skips the case on Windows (`t.Skip("posixOnly mutation")`). TS generates it everywhere.
  - Go `deferredTypes = map[string]string{"turn": "Task 7 ports the turn verifier"}`. `TestGoldenReplay` checks that a deferred base has its golden and then `continue`s without running it. Task 7 deletes the entry.

**Gotchas:**
- **Deterministic bases.** `nativeTurnFixture(observed)` rewrites what `investigateTurnTrees` reads from the clock, in this order:
  1. For run `i` (in array order): `executionNonce = "00000000-0000-4000-8000-" + i.toString(16).padStart(12, "0")`, `startedAt = finishedAt = "2026-07-16T11:03:00.000Z"`, `durationMs = 0`, `result.executor = "NATIVE_DOCKER"`.
  2. Recompute `executionId` with the turn formula (`turn-investigation.ts:544-555`) and `runId` as `digestJson(run without runId)`.
  3. Set `repository` to the literal `/work/turn-repository`, since the writer packs from `options.repository`, never from `result.repository`.
  4. Rebuild `stableStates`, `transitions`, `nonMonotonic` and `introduction` with the frozen TS `reconstructStableTurnStates`, `findStableTurnTransitions` and `attributeFailureIntroduction`.
  5. Set `proof` to the native values with `reason: "Each listed transition has three distinct Docker-isolated executions on both adjacent turn-tree states."` and `evidenceGrade: "EXPERIMENTAL_TURN"`.
  6. Parse with `TurnInvestigationResultSchema`.

  Sandbox audits hold no path, and the fake runner's output is fixed text. The ledger uses fixed ids and times, and its snapshots have `repositoryRoot: "/work/turn-repository"`. The scratch repository uses `gen-bases.ts`'s fixed `GIT_*_DATE`. `generatedAt` is `"2026-07-16T11:04:00.000Z"`.
- **The pack follows git** (correction 8). The bases are committed snapshots like the Git bases. Task 11's drift check regenerates them on ubuntu-24.04.
- **The `resignTurn` key** is the editor who rewrites everything consistently, so that only the semantic checks can catch the edit. Apply it on both twins in this order:
  1. Read `investigation.json`.
  2. For each run, `runId = digestJson(run without runId)` (`executionId` is left alone), and keep the old→new map.
  3. Replace old ids in `stableStates[].runIds` and in `transitions[].before.runIds`/`after.runIds`.
  4. Write `investigation.json` as `canonicalText + "\n"`.
  5. Delete every `runs/*.json`, then write `runs/<hex>.json` for each run.
  6. Write `transitions/<NNNN>.json` from the investigation's transitions; delete extra ones.
  7. Set `manifest.artifacts.runs` to `[{runId, path}]`, `manifest.artifacts.transitions` to `[{index, path}]` and `manifest.investigationDigest` to `canonicalDigest(investigation)`, then write the manifest.
  8. The template's `rehash: true` then rewrites `hashes.txt` and `ROOT.sha256`.
- **Git labels** (correction 10).
  - `maskGitOutput` (both twins) applies `maskGitDetail` after each of these needles, up to the next `\n- ` (or the end):
    - `Turn tree pack is not a valid Git pack: `
    - `Could not create temporary bare repository for pack verification: `
    - `Could not pack turn trees: `
    - the regex `Could not enumerate objects for turn tree [0-9a-f]{40,64}: `
  - The `<GITTMP>` loop also replaces `faultline-turn-proof-pack-` runs.
  - With `maskGit` false (a fake-git case), nothing is masked, as before.
  - `TestMaskGitDetail` gains a case for each.
- **Fake gits.**
  - `fail-unpack`: `unpack-objects` prints `fatal: planted unpack failure` and exits 128; everything else runs the real git. The verifier then falls back to `index-pack --strict <path>`, and the bundle is VALID.
  - `fail-unpack-index`: `unpack-objects` and `index-pack` both fail with planted text. This gives `Turn tree pack is not a valid Git pack: <index-pack stderr>`, because the index-pack stderr wins (`turn-proof-bundle.ts:475-481`).
- **`fl verify` routing.** `fl verify` reads `manifest.json` itself and picks the verifier by `schemaVersion` (`cli-app.ts:2907-2942`). A manifest that is missing, does not parse (truncated, BOM, invalid UTF-8, a flipped byte) or has no string `schemaVersion` therefore goes to the demo verifier (`verifyProofBundle`), not the turn verifier. Those turn-base cases pin the routing; their goldens hold the demo verifier's text, `Bundle self-consistency: INVALID` and the rest. This is existing slice 1 behaviour; Task 7 only adds the turn branch.
- **Generic templates apply to the turn bases.** A template without `bases` runs on every base that is not `git-shape-*`. So flip, delete, make-dir, the symlinks, the strays, the JSON corruptions, the `hashes-*` path cases and `root-mismatch` all reach the turn bases with no new entry. The new templates below name `"bases": ["turn-two-states"]`, or both turn bases where stated.

**The hostile-verifier corpus** (`docs/post-submission/hostile-verifier-turn-parity-e40b.md`). For each of its 15 cases: the templates that cover it, and what TS prints. The goldens pin the exact text.

| # | Case | Templates (new ones in bold) | Verdict |
| --- | --- | --- | --- |
| 1 | missing manifest/hashes/ROOT | `delete` on each file; **`turn-rename-hashes`** is generic `rename-hashes` | INVALID: `Turn proof bundle verification failed safely: ENOENT…` for hashes/ROOT. A missing manifest routes to the demo verifier (see Gotchas). |
| 2 | extra undeclared file | `add-root-stray`, `add-nested-stray`; **`turn-stray-declared`** (`add-file` `source/extra.bin` + `rehash`) | INVALID: `undeclared physical file…`, or `hashes.txt contains an unexpected artifact: source/extra.bin` |
| 3 | traversal | `hashes-traversal`, `hashes-dotdot-escape`; **`turn-hashes-nested-traversal`** (`  manifest.json` → `  source/../../outside`) | INVALID: `hashes.txt contains an unsafe artifact path: <whole line>` |
| 4 | absolute / Windows paths | `hashes-absolute`, `hashes-backslash`, `hashes-nul`; **`turn-hashes-drive-letter`** (`  C:/outside`, `posixOnly`) | INVALID; on POSIX the drive-letter path is safe and reads as ENOENT (correction 11) |
| 5 | symlink / special file | `symlink-to-hashes`, `symlink-outside`, `root-symlink`, `make-dir`; **`turn-fifo`** (`fifo` at `source/extra.fifo`) | INVALID: `Turn proof bundle contains a symbolic link or special file: …` or `must be a real directory` |
| 6 | duplicate JSON keys | **`turn-dup-key-last-valid`** (`"integrityScope":"x","integrityScope":"complete-declared-file-set"`, rehash), **`turn-dup-key-last-invalid`** (the reverse), **`turn-dup-schema-version-git`** (a second `schemaVersion` of `faultline.git-proof-bundle.v1` last) | VALID; INVALID with `manifest schema validation failed`; routed to the Git verifier (correction 12) |
| 7 | oversized `hashes.txt` | **`turn-hashes-at-read-limit`** (`pad-file` 4,194,304), **`turn-hashes-one-over`** (4,194,305) | the first is INVALID only for the missing final newline and the root mismatch (`trimEnd` drops the padding, so every line still parses); the second fails safely with `hashes.txt exceeds FaultLine's 4194304 byte read limit` |
| 8 | catalog entries over the cap | **`turn-catalog-at-2048`** (`append-catalog-lines` 2,034), **`turn-catalog-over-2048`** (2,035), **`turn-catalog-2100`** (2,100); **`turn-files-at-limit`** (`many-files` 2,032), **`turn-files-one-over`** (2,033); **`turn-declared-total-one-over`** (`pad-total` 536,870,913, declared) | no cap: one `declared artifact cannot be read safely` and one `unexpected artifact` per extra line (correction 1); the physical cap throws `too many files` |
| 9 | root mismatch | `root-mismatch`; the base's `root-bad`, `root-malformed` and `root-empty` invocations | INVALID; `root-empty` is MISMATCH with `not a sha256 digest` (correction 2) |
| 10 | run-id reuse / metadata contradiction | **`turn-run-id-reuse`** (`runs[1].runId` from `runs[0]`, `resignTurn` off, rehash), **`turn-metadata-tree-swap`** (`trees[0].treeDigest` from `trees[1]`, rehash), **`turn-run-tree-swap`** (`runs[0].treeDigest` from state 1, `resignTurn`, rehash) | INVALID with `duplicate runId`/`runId does not match`, `source tree metadata does not match investigation states`, `run state does not match the state sequence` |
| 11 | contradictory outcomes | **`turn-flip-verdict`** (`runs[3].result` verdict PASS, reason `PREDICATE_PASS`, exitCode 0, `resignTurn`), **`turn-transition-kind`** (`transitions[0].kind` `FAIL_TO_PASS`, `resignTurn`), **`turn-proof-bit`** (`proof.isProof` false, `resignTurn`), **`turn-non-monotonic-flag`** (on `turn-non-monotonic`: `nonMonotonic` false, `resignTurn`) | INVALID with only the reconstruction errors (`persisted stable states contradict…` and the rest) |
| 12 | pack tampering | `flip-first`/`flip-last` on `source/trees.pack`; **`turn-pack-wrong-trees`** (`replace-pack` `turn-ancestor-only.pack`, rehash), **`turn-pack-garbage`** (`replace-pack` `turn-not-a-pack.bin`, rehash), **`turn-git-fail-init`** (`fake-git` `fail-init`), **`turn-git-fail-unpack`**, **`turn-git-fail-unpack-index`** | `digest or size does not match`; `Turn tree object missing or wrong type in portable pack: <tree>`; `Turn tree pack is not a valid Git pack: <GIT-STDERR>`; the planted texts; VALID via `index-pack` |
| 13 | downgrade / grade confusion | **`turn-grade-turn-proof`** (`evidenceGrade` `TURN_PROOF`), **`turn-grade-none`** (`NONE`), **`turn-label-legacy`** (the legacy label), **`turn-label-commit`** (the COMMIT_PROOF label), **`turn-strip-grade`** (delete `evidenceGrade`), all with `resignTurn`; **`turn-schema-as-git`** (manifest `schemaVersion` `faultline.git-proof-bundle.v1`, rehash) | the `TURN_PROOF is reserved…` text; `must remain graded EXPERIMENTAL_TURN`; VALID; the `must carry evidenceLabel` text; `investigation schema validation failed`; the Git verifier's refusal |
| 14 | archive / compression bombs | none: the verifier takes a directory, never an archive. The pack is unpacked by git with `--strict` under the 64 MiB output cap. **`turn-deep-literal`** (`replace-nested` depth 3,000 in `manifest.json`) stands in as the parser-bomb analogue | the zod message at `SiteTurnProof` (correction 14) |
| 15 | malformed JSON | `truncate-json`, `bom-json`, `invalid-utf8-json`, `retype-first`, `drop-first`, `add-unknown-key`, `add-proto-key` on every JSON file | INVALID: `<label> JSON validation failed: <V8 text>` or a schema failure; a broken manifest routes to the demo verifier |

Also turn-specific: **`turn-ledger-hash`** (`lifecycle/ledger.json` `events[2].hash`, rehash), **`turn-ledger-snapshot-edit`** (a snapshot's `dirty` flipped with `resignLedger`, rehash: the event chain is re-signed but the snapshot digest is not, so the binding fails with `Turn tree snapshot digest does not match its contents`), **`turn-lifecycle-transport`** (`manifest.lifecycle.transport` `CODEX_CLI`, rehash) and **`turn-manifest-codex-thread`** (`json-edit` of `manifest.json` at `["codex"]` with value `{"repairThreadId":"t-1"}`, rehash, VALID: the `codex` section is not cross-checked). The test file header (Task 7's `difftest/turnproof_corpus_test.go`) lists these 15 rows and the N/A reason for row 14.

- [ ] **Step 1: Write the failing tests**

```go
// difftest/cases_test.go
func TestTurnGitLabelsMasked(t *testing.T) {
	in := "- Turn tree pack is not a valid Git pack: fatal: early EOF\nfatal: index-pack failed\n- next"
	if got := maskGitOutput(in, true); got != "- Turn tree pack is not a valid Git pack: <GIT-STDERR>\n- next" {
		t.Fatalf("%q", got)
	}
	in = "x /tmp/faultline-turn-proof-pack-Ab12Cd/objects y"
	if got := maskGitOutput(in, true); got != "x <GITTMP>/objects y" {
		t.Fatalf("%q", got)
	}
	in = "Could not enumerate objects for turn tree " + strings.Repeat("a", 40) + ": fatal: bad object"
	if got := maskGitOutput(in, true); !strings.HasSuffix(got, ": <GIT-STDERR>") {
		t.Fatalf("%q", got)
	}
}

func TestPadFileAndResignTurn(t *testing.T) {
	repo := repoRoot(t)
	work := t.TempDir()
	copyTree(t, filepath.Join(repo, "difftest", "testdata", "bases", "turn-two-states"), work)
	tpl := mustTemplate(t, `{"id":"x","op":"pad-file","file":"hashes.txt","total":4194304}`)
	if !applyMutation(work, testCase{id: "x", base: "turn-two-states", tpl: tpl, file: "hashes.txt"}) {
		t.Fatal("not applied")
	}
	if info, _ := os.Stat(filepath.Join(work, "hashes.txt")); info.Size() != 4194304 {
		t.Fatal(info.Size())
	}
}
```

(`mustTemplate` parses one template with the loader's own decoder; add it beside `loadTemplates` if it does not exist.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run 'TestTurnGitLabelsMasked|TestPadFileAndResignTurn' -v`
Expected: FAIL (the labels are unmasked; no base `turn-two-states`; unknown op `pad-file`).

- [ ] **Step 3: Implement** the fixtures, both bases, both twins, the fake gits and the templates. Then generate the bases and the goldens in podman: `-e BASES=turn-two-states,turn-non-monotonic`, with the gen-bases line of the podman command. Run the base generation twice and `diff -r` the two outputs (they must be identical). Then run `git status --porcelain difftest/testdata`: only `bases/turn-*`, `golden/turn-*`, `fixtures/turn-*`, `fakegit/fail-unpack*` and `mutations.json` may change.

- [ ] **Step 4: Run to verify the tests pass and no other base's golden moved**

Run: `go test -tags faultline_test ./difftest/ -run 'TestTurnGitLabelsMasked|TestPadFileAndResignTurn|TestMaskGitDetail|TestGoldenReplay' > /tmp/s4b-t2.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4b-t2.log | tail` (TestGoldenReplay runs every other base and skips the deferred turn bases; in podman, under 280 s with `-run 'TestGoldenReplay/(git-two-states|demo-replay)'`)
Expected: PASS. Also run `grep -c '"case":"turn-two-states__' difftest/testdata/golden/turn-two-states.jsonl`: it prints the number of expanded cases (over 150).

- [ ] **Step 5: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(difftest): turn proof bases, hostile-verifier turn corpus and turn git masks

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `internal/turninv` schemas and state reconstruction, with their live oracle

**Files:**
- Create: `internal/turninv/schemas.go`, `internal/turninv/states.go`, `internal/turninv/states_test.go`, `difftest/turninv_live_test.go`, `difftest/gen/oracle-turninv.ts`
- Modify:
  - `internal/schema/composite.go`, `internal/schema/schema_test.go`: `RecordOf`.
  - `difftest/gen/node-oracle.ts`: one import line and one spread line for `oracle-turninv.ts`; and the `namedSchemas` additions.
  - `difftest/named_live_test.go`: the turn schemas in `namedSchemas` and `namedSeeds`.

**Interfaces:**
- Consumes: `gitproof.TurnTreeSnapshotSchema`, `gitproof.EnvironmentFingerprintSchema`, `gitproof.MaterializedOverlaySchema`, `gitproof.StableExecutionCount`; `evidence.TurnPathEvidenceGradeExperimental`, `evidence.TurnPathEvidenceGradeParityReserved`, `evidence.TurnPathEvidenceLabelExperimental`; `bundle.ParseValue`; `v8sort.Sort`.
- Produces:

```go
// internal/schema
// RecordOf is z.record(key, value): each own key is parsed with key (its
// issues first, at path [..., key]), then its value with value (at the
// same path). The parsed key replaces the original; __proto__ is dropped.
func RecordOf(key, value Schema) Schema
```

```go
package turninv // Port of src/turn-investigation.ts.

const (
	SchemaVersion        = "faultline.turn-investigation.v1" // :47
	SessionBaselineTurnID = "session-baseline"               // :58
	DuplicateOrdinalError = "Duplicate turnOrdinal in session baseline / TURN_TREE_SNAPSHOT sequence." // :383
)

// Schemas, exported for the bundle (Tasks 7, 8) and the live oracle.
var (
	TurnStateSchema       *schema.ObjectSchema // :60
	RunFactSchema         *schema.ObjectSchema // :105 TurnInvestigationRunFactSchema
	StableStateSchema     *schema.ObjectSchema // :161
	StableTransitionSchema *schema.ObjectSchema // :175
	IntroductionSchema    *schema.ObjectSchema // :186
	ResultSchema          *schema.ObjectSchema // :204 TurnInvestigationResultSchema
)

// State ports TurnState (:60).
type State struct {
	Index, TurnOrdinal       int
	TurnID, Role, TreeDigest, SnapshotDigest string
	Dirty                    bool
}
// Value is the state in TS key order: index, turnId, turnOrdinal, role,
// treeDigest, snapshotDigest, dirty.
func (s State) Value() jsjson.Value
// StatesFromValue reads result.states (already schema-parsed).
func StatesFromValue(v jsjson.Value) []State

// StatesFromLedger ports turnStatesFromLedger (:347). ledger is a parsed
// CodexLifecycleLedger; a snapshot that fails TurnTreeSnapshotSchema
// throws the ZodError (jsexc), as TS's .parse does.
func StatesFromLedger(ledger jsjson.Value) ([]State, error)

// DuplicateTurnOrdinal ports duplicateTurnOrdinalError (:379); "" is null.
func DuplicateTurnOrdinal(states []State) string

// ReconstructStable ports reconstructStableTurnStates (:467). runs are
// schema-parsed run facts in array order; requireNative is
// options.requireNativeExecutor === true. Each stable state is returned as
// its JSON value in StableStateSchema key order.
func ReconstructStable(states []State, runs []jsjson.Value, requireNative bool) []jsjson.Value

// FindTransitions ports findStableTurnTransitions (:511).
func FindTransitions(stable []jsjson.Value) []jsjson.Value

// HasNonMonotonic ports hasNonMonotonicTransitions (:526).
func HasNonMonotonic(transitions []jsjson.Value) bool

// AttributeFailureIntroduction ports attributeFailureIntroduction (:393).
// stable nil is the TS default [].
func AttributeFailureIntroduction(states []State, transitions, stable []jsjson.Value) jsjson.Value

// IsDecisiveRun ports isDecisiveRun (:454).
func IsDecisiveRun(run jsjson.Value) bool
```

**Gotchas:**
- **The schemas are not the Git ones.**
  - `turn-investigation.ts:51-54` declares `DigestSchema` and `GitObjectIdSchema` with no regex message, so zod's text is `Invalid`. `gitproof`'s versions carry messages. Declare them here: `s.String(s.Regex(s.Pattern(src), ""))`.
  - `TimestampSchema` is `z.string().datetime({ offset: true })`, which is `s.String(s.Datetime())`.
  - `SandboxAuditSchema` (`:73-103`) and `WitnessVerificationSummarySchema` (`:195-202`) are local copies whose digests have no message. Declare them here; do not reuse `gitproof.SandboxAuditSchema`.
  - `MaterializedOverlaySchema` and `EnvironmentFingerprintSchema` are the imported ones; reuse gitproof's.
  - `executionAttempt` is `int().min(1).max(3)`: `s.Number(s.Int(), s.Gte(1), s.Lte(3))`.
  - `stdoutPreview`/`stderrPreview` are `max(32768)` UTF-16 units.
  - `executionIds`/`runIds` are `.length(3)`: `s.Array(…, s.Length(3))`.
  - `runtimeMapping` is `RecordOf(DigestSchema, DigestPinnedImageSchema)` (correction 13). The image regex has no message either.
  - `proof.evidenceGrade` is `z.enum(["TURN_PROOF", "EXPERIMENTAL_TURN", "NONE"])`, in that order (`:242-246`). The order shows in `invalid_enum_value`'s `options`.
  - Every object is `.strict()`.
- **`RecordOf` order.** zod 3's `ZodRecord._parse` (sync) builds one `{key, value}` pair per own key, keys from `ctx.data` in own-key order. Each key is parsed at path `[…, key]`, then each value at the same path, and `ParseStatus.mergeObjectSync` collects issues key by key: key issues, then value issues, per entry.
  - A `__proto__` key is dropped from the output (zod's `mergeObjectSync` skips it).
  - Unit-test `RecordOf` with a bad key and a bad value in the same entry. Check the issue order in `TestLiveNamedSchemas` (below).
- **`StatesFromLedger`** (`:347-376`):
  - Walk `ledger.events` in order. A `SESSION_BASELINE_SNAPSHOT` gives `{index: states.length, turnId: "session-baseline", turnOrdinal: 0, role: "SESSION_BASELINE", …}`; a `TURN_TREE_SNAPSHOT` gives `{…, turnId: payload.turnId, turnOrdinal: payload.turnOrdinal, role: "TURN", …}`.
  - Each snapshot is parsed with `TurnTreeSnapshotSchema.parse` first. A failure throws.
  - Then `states.sort((l, r) => l.turnOrdinal - r.turnOrdinal || l.index - r.index)`. That is V8's TimSort with a numeric comparator: use `v8sort.Sort` with `cmp = sign(l.TurnOrdinal - r.TurnOrdinal)`, then index.
  - The sort keeps each state's original `index`. So after sorting, `index` need not equal the array position, which matters for `ReconstructStable`'s `stateIndex` and for the semantics in Task 7.
- **`ReconstructStable`** (`:467-509`):
  - For each state in array order: filter the runs with `run.stateIndex === state.index`, then sort by `executionAttempt` with a numeric comparator (V8 TimSort, stable).
  - Skip unless there are exactly 3, with 3 distinct attempts and 3 distinct `executionId`s.
  - `verdict` is `stateRuns[0].result.verdict`. It must be PASS or FAIL.
  - Every run must match the state's `turnId`, `turnOrdinal`, `role`, `treeDigest` and `snapshotDigest` and share the verdict, and `IsDecisiveRun` must hold. With `requireNative`, every `result.executor` must be `NATIVE_DOCKER`.
  - Push `{stateIndex: state.index, turnId, turnOrdinal, role, treeDigest, snapshotDigest, verdict, executionIds, runIds}` in that key order.
- **`FindTransitions`** (`:511-524`) pairs neighbours in the stable list only when `after.stateIndex === before.stateIndex + 1` and the verdicts differ. `kind` is `PASS_TO_FAIL` when `before` is PASS. `before` and `after` are the stable state objects themselves.
- **`AttributeFailureIntroduction`** (`:393-452`). The four `reason` texts are verbatim. The `→` is U+2192:
  - `No PASS→FAIL transition is available to attribute.`
  - `No SESSION_BASELINE_SNAPSHOT; cannot claim a turn introduced the failure.`
  - `No stable passing session baseline; cannot claim a turn introduced the failure.`
  - `The earliest PASS→FAIL transition crosses an observation gap or is not a stable adjacent pair.`

  The success text is `` `Earliest recorded stable PASS→FAIL occurred at turn ${n}.` `` with `jsjson.FormatNumber`. The baseline lookup is `states.find(role === "SESSION_BASELINE" && turnOrdinal === 0)`, by array position after the sort. The output key order is `status, turnOrdinal, turnId, reason`.
- **Numbers.** `turnOrdinal`, `index` and `stateIndex` come from parsed JSON. They are integers by schema, but `-0` can appear in a hostile bundle (Task 7 verifies hostile input). Keep them as `float64` inside comparisons (`jsjson.Value.Num()`), and convert to `int` only for the `State` struct after the schema accepted them. `-0 === 0` is true.

**Live test** `TestLiveTurnStates` (`difftest/turninv_live_test.go`). The oracle ops in `oracle-turninv.ts` take canonical JSON and return `JSON.stringify` of `{value}` or `{threw, zod}`:
- `turnStatesFromLedger({ledger})`
- `duplicateTurnOrdinalError({states})`
- `reconstructStableTurnStates({states, runs, requireNativeExecutor})`
- `findStableTurnTransitions({stable})`
- `attributeFailureIntroduction({states, transitions, stableStates})`

The inputs (spec 2.5):
- 1,500 generated ledgers. Start from `ledger-seed.ts`'s `richLedger` (it already holds both snapshot kinds) and from the `turn-*` scenario ledgers, then mutate:
  - snapshot events reordered across turns;
  - duplicated ordinals;
  - `-0` and `1e0` ordinals;
  - a snapshot's fields corrupted (`gen.CorruptLeaf` on the payload);
  - baselines missing;
  - up to 40 turns.

  Ledgers are not re-verified: `turnStatesFromLedger` does not verify.
- 2,000 generated `(states, runs)` pairs: 3 runs per state, with random drops, duplicate attempts, duplicate execution ids, mixed verdicts, `EXIT_ZERO`/`EXIT_NONZERO` reasons, `INJECTED_RUNNER` executors, mismatched tree digests and shuffled run order. Each with `requireNativeExecutor` true and false.
- `findStableTurnTransitions` and `attributeFailureIntroduction` on every reconstructed list, plus 300 hand-shaped lists: gaps, baseline FAIL, baseline missing, `stableStates` empty.

Go must give the same value or the same thrown message. `requireReached("internal/turninv/states.go", …)`. Task 11 adds the file to `liveGatedFiles`.

`TestLiveNamedSchemas` gains:
- `TurnInvestigationResultSchema`, seeded with `turn-two-states/investigation.json` and `turn-non-monotonic/investigation.json`;
- `TurnInvestigationRunFactSchema`, seeded with the first run of each;
- `StableTurnTransitionSchema`, seeded with `turn-two-states/transitions/0000.json`;
- `TurnStateSchema`, seeded with `investigation.json#states.0`.

The four names are added to `namedSchemas` on both sides. Until Task 2's bases merge, the seeds are inline JSON texts in the test file. Task 2's bases replace them.

- [ ] **Step 1: Write the failing tests**

```go
// internal/turninv/states_test.go
package turninv

import (
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

func run(state State, attempt int, verdict, reason, executor string) jsjson.Value {
	return jsjson.MustObject(
		"stateIndex", jsjson.MakeNumber(float64(state.Index)),
		"executionAttempt", jsjson.MakeNumber(float64(attempt)),
		"executionId", jsjson.MakeString("sha256:"+strings.Repeat(string(rune('a'+state.Index)), 63)+string(rune('0'+attempt))),
		"runId", jsjson.MakeString("sha256:"+strings.Repeat("f", 63)+string(rune('0'+attempt))),
		"turnId", jsjson.MakeString(state.TurnID),
		"turnOrdinal", jsjson.MakeNumber(float64(state.TurnOrdinal)),
		"role", jsjson.MakeString(state.Role),
		"treeDigest", jsjson.MakeString(state.TreeDigest),
		"snapshotDigest", jsjson.MakeString(state.SnapshotDigest),
		"result", jsjson.MustObject("kind", jsjson.MakeString("DOCKER_ISOLATED"), "executor", jsjson.MakeString(executor),
			"verdict", jsjson.MakeString(verdict), "reason", jsjson.MakeString(reason)),
	)
}

func states() []State {
	return []State{
		{Index: 0, TurnID: SessionBaselineTurnID, TurnOrdinal: 0, Role: "SESSION_BASELINE", TreeDigest: strings.Repeat("a", 40), SnapshotDigest: "sha256:" + strings.Repeat("1", 64)},
		{Index: 1, TurnID: "turn-1", TurnOrdinal: 1, Role: "TURN", TreeDigest: strings.Repeat("b", 40), SnapshotDigest: "sha256:" + strings.Repeat("2", 64), Dirty: true},
	}
}

func TestPassToFailIsAttributed(t *testing.T) {
	s := states()
	var runs []jsjson.Value
	for a := 3; a >= 1; a-- { // unsorted on purpose
		runs = append(runs, run(s[0], a, "PASS", "PREDICATE_PASS", "NATIVE_DOCKER"), run(s[1], a, "FAIL", "PREDICATE_FAIL", "NATIVE_DOCKER"))
	}
	stable := ReconstructStable(s, runs, true)
	if len(stable) != 2 || stable[0].Get("executionIds").Items()[0].Str()[70:] != "1" {
		t.Fatalf("%v", stable)
	}
	transitions := FindTransitions(stable)
	if len(transitions) != 1 || transitions[0].Get("kind").Str() != "PASS_TO_FAIL" {
		t.Fatal(transitions)
	}
	got := jsjson.StringifyIndent(AttributeFailureIntroduction(s, transitions, stable), "")
	if got != `{"status":"ATTRIBUTED","turnOrdinal":1,"turnId":"turn-1","reason":"Earliest recorded stable PASS→FAIL occurred at turn 1."}` {
		t.Fatal(got)
	}
}

func TestLegacyExitReasonIsNotDecisive(t *testing.T) {
	s := states()
	var runs []jsjson.Value
	for a := 1; a <= 3; a++ {
		runs = append(runs, run(s[0], a, "PASS", "EXIT_ZERO", "NATIVE_DOCKER"))
	}
	if got := ReconstructStable(s, runs, false); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestStatesFromLedgerSortsByOrdinalThenIndex(t *testing.T) {
	if DuplicateTurnOrdinal([]State{{TurnOrdinal: 1}, {TurnOrdinal: 1}}) != DuplicateOrdinalError {
		t.Fatal("duplicate not reported")
	}
	if DuplicateTurnOrdinal(states()) != "" {
		t.Fatal("false duplicate")
	}
}
```

```go
// internal/schema/schema_test.go
func TestRecordOfKeyIssuesFirst(t *testing.T) {
	r := RecordOf(String(Regex(Pattern(`^k$`), "")), String(MinLength(2)))
	_, issues, ok := Parse(r, mustParse(t, `{"x":"y"}`))
	if ok || len(issues) != 2 || ErrorMessage(issues[:1]) == ErrorMessage(issues[1:]) {
		t.Fatalf("%v", issues)
	}
	if !strings.Contains(ErrorMessage(issues), `"validation": "regex"`) || strings.Index(ErrorMessage(issues), "regex") > strings.Index(ErrorMessage(issues), "too_small") {
		t.Fatal(ErrorMessage(issues))
	}
}
```

(`mustParse` exists in `schema_test.go`; if not, add it as `jsjson.Parse` plus `t.Fatal`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/turninv/ ./internal/schema/ -run 'TestPassToFail|TestLegacyExit|TestStatesFromLedger|TestRecordOf'`
Expected: FAIL (the package does not exist; `RecordOf` is undefined).

- [ ] **Step 3: Implement** by transcription. Every function carries its `src/turn-investigation.ts` line. Add `StatesFromLedger` tests ported from `tests/turn-investigation.test.ts` (the ordering, baseline and duplicate cases) and the oracle ops.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/turninv/ ./internal/schema/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveTurnStates|TestLiveNamedSchemas' -v > /tmp/s4b-t3.log 2>&1; tail -5 /tmp/s4b-t3.log` (in podman, under 280 s)
Expected: PASS, with a `LIVE-SUMMARY` line.

- [ ] **Step 5: Commit**

```bash
git add internal/turninv internal/schema difftest/turninv_live_test.go difftest/gen/oracle-turninv.ts difftest/gen/node-oracle.ts difftest/named_live_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): turn investigation schemas, state reconstruction and transitions

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: The turn-minimization bridge and `minimizeFromTurnInvestigation`

**Files:**
- Create: `internal/minimize/bridge.go`, `internal/minimize/bridge_test.go`, `internal/cli/turnmin.go`, `internal/cli/turnmin_test.go`, `difftest/turnbridge_live_test.go`, `difftest/gen/oracle-bridge.ts`
- Modify: `difftest/gen/node-oracle.ts` (one import line and one spread line)

**Interfaces:**
- Consumes:
  - Task 3: `turninv.StableTransitionSchema`.
  - `nodeproc.SpawnSyncWith` (nil `Env`, so it inherits; `MaxBuffer: 16 << 20`).
  - `minimize.MinimizeGitDiff`, `minimize.WriteResult`; `sandbox.NativeRunner()`; `jsjson.ToNumber`, `jsjson.FormatNumber`; `bundle.ParseValue`; `clock.NowMillis`; `nodefs.Resolve`, `nodefs.Join`.
- Produces:

```go
package minimize

// BridgeNote is SyntheticTurnCommits.note (turn-minimization-bridge.ts:118).
const BridgeNote = "Synthetic orphan commits were created from turn tree digests so Git-path counterfactual minimization can run. They are not Codex history and do not promote EXPERIMENTAL_TURN to TURN_PROOF / COMMIT_PROOF."

// SyntheticTurnCommits ports SyntheticTurnCommits (:11).
type SyntheticTurnCommits struct {
	BeforeCommit, AfterCommit, BeforeTree, AfterTree string
	TransitionIndex                                  float64 // as the caller passed it (-0 stays -0)
	Transition                                       jsjson.Value
	Note                                             string
}

// SelectTurnTransition ports selectTurnTransition (:38).
func SelectTurnTransition(transitions []jsjson.Value, index float64) (jsjson.Value, error)

// SyntheticCommitsForTurnTransition ports :79.
func SyntheticCommitsForTurnTransition(repository string, transition jsjson.Value, index float64) (SyntheticTurnCommits, error)

// BridgeTurnTransition ports bridgeTurnTransitionToMinimizationCommits (:123).
func BridgeTurnTransition(repository string, transitions []jsjson.Value, index float64) (SyntheticTurnCommits, error)
```

```go
// internal/cli/turnmin.go
// turnMinOptions are minimizeFromTurnInvestigation's options
// (cli-app.ts:2239); nil pointers are undefined.
type turnMinOptions struct {
	repository, expectedFrozenDigest, image string
	transitions                             []jsjson.Value
	frozenWitness                           jsjson.Value
	transitionIndex, maxExecutions, output  *string
}

// minimizeFromTurnInvestigation ports cli-app.ts:2239-2288: the returned
// value is the object TS returns, in its key order.
func minimizeFromTurnInvestigation(o turnMinOptions) (jsjson.Value, error)
```

**Gotchas:**
- **runGit** (`turn-minimization-bridge.ts:21-32`): `SpawnSyncWith("git", ["-C", repository, ...args], {MaxBuffer: 16 << 20})`.
  - Leave `Env` nil, so the whole environment is inherited (correction 4). Set no cwd and no timeout. Windows already gets `windowsHide: true`.
  - Decode stdout and stderr with `jsstr.DecodeUTF8`.
  - `r.Err != nil` returns `r.Err` itself. Its message is `spawnSync git ENOENT`, or `ENOBUFS` past 16 MiB, which `rev-parse` and `commit-tree` cannot reach (coverage gate reason).
  - `r.Status == nil || *r.Status != 0` returns `errors.New(jsstr.Trim(stderr) || "git " + strings.Join(args, " ") + " failed")`.
  - Success returns `jsstr.Trim(stdout)`.
- **Call order and messages** (`:84-120`):
  1. `kind !== "PASS_TO_FAIL"` gives `Counterfactual minimization from a turn package currently requires PASS→FAIL (got <kind>).`
  2. A wrong verdict pair gives `Turn transition endpoints are not a stable PASS→FAIL pair.`
  3. `rev-parse --verify --end-of-options <before.treeDigest>^{tree}`, then the same for `after`. Each output is parsed with `/^[a-f0-9]{40}$/`, which has no message, so a ZodError (`Invalid`) at the root, thrown through `jsexc`. A SHA-256 tree hits this (correction 3).
  4. `commitTree(beforeTree, "faultline-turn-min before t<before.turnOrdinal> (<before.treeDigest[0:12]>)")`, which runs `rev-parse` of `<tree>^{tree}` again, then `-c user.name=FaultLine -c user.email=faultline@example.invalid commit-tree <tree> -m <message>`, each parsed with the same schema.
  5. The same for `after`.

  The message interpolates `turnOrdinal` with `jsjson.FormatNumber` and slices the original `treeDigest`, not the resolved tree.
- **SelectTurnTransition** (`:38-52`):
  - `!Number.isInteger(i) || i < 0` gives `Transition index must be a non-negative integer (got <FormatNumber(i)>).` The CLI checks first, so this is unreachable from `fl` (coverage gate reason).
  - `transitions[i]` absent gives `Turn investigation has no transition at index <i> (<len> available).`
  - Then `StableTransitionSchema.parse`, which succeeds for every verified input; reach the failure in the unit test.
  - `-0` indexes element 0, and `FormatNumber(-0)` is `0`.
- **minimizeFromTurnInvestigation** (`cli-app.ts:2239-2288`), in order:
  1. `transitionIndex = o.transitionIndex == nil ? 0 : ToNumber(*o.transitionIndex)`. `--transition ""` is `0`, and `" 1 "`, `"1e0"` and `"0x1"` are `1`.
  2. `!isInteger || < 0` gives `--transition must be a non-negative integer.` (`-0` passes).
  3. Run the bridge.
  4. `MinimizeGitDiff(request, sandbox.NativeRunner(), false)`. The request keys, in order: `repository` (`o.repository`, already resolved), `before: bridge.BeforeCommit`, `after: bridge.AfterCommit`, `frozenWitness`, `expectedFrozenDigest`, `sandbox: {mode: "DOCKER_ISOLATED", image}`, and `budget: {maxExecutions: ToNumber(*o.maxExecutions)}` only when given. A minimization that throws propagates.
  5. Only now, with `o.output` nil, read `clock.NowMillis()` for `turn-transition-<pad4(FormatNumber(i))>-<ms>.json` under `.faultline/minimizations`. `padStart(4, "0")` pads the formatted number, so 12345 stays `12345`. Then `resolve` it.
  6. `minimize.WriteResult(output, result)`. It is not `safeManagedFileOutput` (correction 5), so any path is accepted, and an existing file gives `Git minimization result already exists and will not be replaced: <path>`.
  7. Return `{bridge: {transitionIndex, beforeTree, afterTree, beforeCommit, afterCommit, note}, result, path, resultDigest, evidenceGradeNote: "Counterfactual minimization from a turn boundary reuses Git-path machinery on synthetic commits. The parent turn package remains EXPERIMENTAL_TURN until TURN_PROOF promotion criteria are met."}`. `result` is the minimization as `MinimizeGitDiff` returned it, and `transitionIndex` is the number.
- **Clock order.** The minimization reads the clock and UUIDs first, then the output name reads `Date.now()`. Swapping them moves every later seeded value.

**Live test** `TestLiveTurnBridge` (`difftest/turnbridge_live_test.go`). The oracle op `bridgeTurnTransition({repository, transitions, transitionIndex, env})` in `oracle-bridge.ts` sets `process.env` from `env` for the call and restores it after. It returns `JSON.stringify` of the result, or `{threw, zod}`, plus the sorted ids of the loose objects the call created.

The Go side sets the same variables with `t.Setenv`. `GIT_AUTHOR_DATE` and `GIT_COMMITTER_DATE` are fixed on both sides, so the commit ids are deterministic and comparable. That also proves the bridge inherits the environment: a hardened git would drop the dates and give different ids.

300 cases on two copies of the same generated repository:
- PASS_TO_FAIL, FAIL_TO_PASS, PASS_TO_FAIL with the verdicts swapped;
- a tree id, a commit id (peeled to its tree), an annotated tag id, a missing id, an abbreviated id, a `-`-prefixed string;
- a SHA-256 repository;
- indices 0, 1, -0, 2.5, 99;
- a `GIT_CONFIG_GLOBAL` holding `[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = false\n` (git's own failure text, same machine);
- `PATH` without git (`spawnSync git ENOENT`).

`requireReached("internal/minimize/bridge.go", …)`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/minimize/bridge_test.go
package minimize

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func bridgeRepo(t *testing.T) (dir, goodTree, badTree string) {
	t.Helper()
	dir = t.TempDir()
	run := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "-q")
	good := run("good\n", "hash-object", "-w", "--stdin")
	bad := run("bad\n", "hash-object", "-w", "--stdin")
	return dir, run("100644 blob "+good+"\tstate.txt\n", "mktree"), run("100644 blob "+bad+"\tstate.txt\n", "mktree")
}

func transition(kind, beforeVerdict, afterVerdict, beforeTree, afterTree string) jsjson.Value {
	state := func(i int, verdict, tree string) jsjson.Value {
		ids := jsjson.MakeArray([]jsjson.Value{jsjson.MakeString("sha256:" + strings.Repeat("1", 64)), jsjson.MakeString("sha256:" + strings.Repeat("2", 64)), jsjson.MakeString("sha256:" + strings.Repeat("3", 64))})
		return jsjson.MustObject("stateIndex", jsjson.MakeNumber(float64(i)), "turnId", jsjson.MakeString("turn"), "turnOrdinal", jsjson.MakeNumber(float64(i)),
			"role", jsjson.MakeString("TURN"), "treeDigest", jsjson.MakeString(tree), "snapshotDigest", jsjson.MakeString("sha256:"+strings.Repeat("4", 64)),
			"verdict", jsjson.MakeString(verdict), "executionIds", ids, "runIds", ids)
	}
	return jsjson.MustObject("kind", jsjson.MakeString(kind), "before", state(0, beforeVerdict, beforeTree), "after", state(1, afterVerdict, afterTree))
}

func TestBridgeCommitsBothTrees(t *testing.T) {
	t.Setenv("GIT_AUTHOR_DATE", "2026-07-16T10:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-07-16T10:00:00Z")
	dir, good, bad := bridgeRepo(t)
	b, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "PASS", "FAIL", good, bad)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.BeforeTree != good || b.AfterTree != bad || len(b.BeforeCommit) != 40 || b.BeforeCommit == b.AfterCommit || b.Note != BridgeNote {
		t.Fatalf("%+v", b)
	}
	out, _ := exec.Command("git", "-C", dir, "cat-file", "-p", b.AfterCommit).Output()
	if !strings.Contains(string(out), "committer FaultLine <faultline@example.invalid>") || !strings.HasSuffix(string(out), "faultline-turn-min after t1 ("+bad[:12]+")\n") {
		t.Fatalf("%s", out)
	}
}

func TestBridgeRefusals(t *testing.T) {
	dir, good, bad := bridgeRepo(t)
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("FAIL_TO_PASS", "FAIL", "PASS", good, bad)}, 0); err == nil ||
		err.Error() != "Counterfactual minimization from a turn package currently requires PASS→FAIL (got FAIL_TO_PASS)." {
		t.Fatal(err)
	}
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "FAIL", "FAIL", good, bad)}, 0); err == nil ||
		err.Error() != "Turn transition endpoints are not a stable PASS→FAIL pair." {
		t.Fatal(err)
	}
	if _, err := BridgeTurnTransition(dir, nil, 3); err == nil || err.Error() != "Turn investigation has no transition at index 3 (0 available)." {
		t.Fatal(err)
	}
	missing := strings.Repeat("e", 40)
	if _, err := BridgeTurnTransition(dir, []jsjson.Value{transition("PASS_TO_FAIL", "PASS", "FAIL", missing, bad)}, 0); err == nil || !strings.HasPrefix(err.Error(), "fatal: ") {
		t.Fatal(err)
	}
}
```

```go
// internal/cli/turnmin_test.go
package cli

import "testing"

func TestTurnMinRejectsBadTransitionIndex(t *testing.T) {
	for _, v := range []string{"-1", "1.5", "abc", "Infinity"} {
		_, err := minimizeFromTurnInvestigation(turnMinOptions{transitionIndex: &v})
		if err == nil || err.Error() != "--transition must be a non-negative integer." {
			t.Errorf("%s: %v", v, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/minimize/ ./internal/cli/ -run 'TestBridge|TestTurnMin'`
Expected: FAIL (undefined: `BridgeTurnTransition`, `minimizeFromTurnInvestigation`).

- [ ] **Step 3: Implement** by transcription, with the TS line on every function, and the oracle op.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/minimize/ ./internal/cli/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveTurnBridge -v > /tmp/s4b-t4.log 2>&1; tail -5 /tmp/s4b-t4.log`
Expected: PASS, with a `LIVE-SUMMARY` line.

- [ ] **Step 5: Commit**

```bash
git add internal/minimize/bridge.go internal/minimize/bridge_test.go internal/cli/turnmin.go internal/cli/turnmin_test.go difftest/turnbridge_live_test.go difftest/gen/oracle-bridge.ts difftest/gen/node-oracle.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): turn-minimization bridge (unhardened commit-tree) and minimizeFromTurnInvestigation

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Turn bundle file walks, the uncapped hash catalog and the tree pack

**Files:**
- Create: `internal/bundle/turnproof/catalog.go`, `internal/bundle/turnproof/pack.go`, `internal/bundle/turnproof/catalog_test.go`, `internal/bundle/turnproof/pack_test.go`, `difftest/turnproof_live_test.go` (with `TestLiveTurnHashCatalog` and `TestLiveTurnTreesPack`; Tasks 7 and 8 add their tests to the same file below these), `difftest/gen/oracle-turnproof.ts` (Tasks 7 and 8 add ops to it)
- Modify:
  - `internal/jsstr/jsstr.go`, `internal/jsstr/jsstr_test.go`: `TrimEnd`.
  - `internal/nodefs/rm.go`, `internal/nodefs/rm_test.go`: `RemoveAllRetry`.
  - `difftest/gen/node-oracle.ts`: one import line and one spread line.

**Interfaces:**
- Consumes: `gitinv.RunHardenedGit`, `gitinv.HardenedOptions`, `gitinv.GitRun` (`.Detail`), `gitinv.ToGitPath` (export `toGitPath` here if 4c Task 1 has not; the two exports are one line and dedupe trivially); `nodefs.{Mkdtemp,Tmpdir,Join,Resolve,Relative,IsAbsolute,Lstat,ReadDirNames,ReadBytes,MkdirAll,WriteFileBytes}`; `canonical.{SHA256HexBytes,SortLocale}`; `jsexc`; `bundle.{Throw,Must,Try}`.
- Produces:

```go
// internal/jsstr
// TrimEnd is String.prototype.trimEnd (the IsWhitespace set).
func TrimEnd(s string) string

// internal/nodefs
// RemoveAllRetry is rmSync(path, {recursive: true, force: true,
// maxRetries, retryDelay}): RemoveAll, where every unlink and the final
// rmdir of each directory are retried up to maxRetries times on EBUSY,
// EMFILE, ENFILE, ENOTEMPTY and EPERM, sleeping i*retryDelay before
// retry i (internal/fs/rimraf.js _unlinkSync, _rmdirSync).
func RemoveAllRetry(path string, maxRetries int, retryDelay time.Duration) error
```

```go
package turnproof // Port of src/turn-proof-bundle.ts.

const (
	BundleSchemaVersion     = "faultline.turn-proof-bundle.v1"  // :59
	SourceSchemaVersion     = "faultline.turn-proof-source.v1"  // :60
	MaxSourceArtifactBytes  = 128 * 1024 * 1024                 // :64
	MaxHashCatalogBytes     = 4 * 1024 * 1024                   // :65
	MaxArtifacts            = 2048                              // :66
	MaxTotalBytes           = 512 * 1024 * 1024                 // :67
)

// IsSafeRelativeArtifactPath ports :72.
func IsSafeRelativeArtifactPath(value string) bool

// Catalog is the Map<string, string> parseHashCatalog returns: Keys in
// first-insertion order, Digests by artifact (a duplicate's last digest).
type Catalog struct {
	Keys    []string
	Digests map[string]string
}

// ParseHashCatalog ports parseHashCatalog (:390): no entry cap.
func ParseHashCatalog(hashes []byte, errs *[]string) Catalog

// The throwing helpers (jsexc), used by the verifier and the writer:
func safeArtifactPath(root, artifact string) string                 // :195
func assertNoLinksOrSpecialFiles(directory string)                  // :205
func assertRegularFile(path, label string, maximumBytes int64) int64 // :259
func readBoundedFile(path, label string, maximumBytes int64) []byte  // :266
func collectFiles(root, current string, budget *fileBudget) []string // :271
func writeArtifact(root, artifact string, b []byte)                 // :296

// PackTurnTrees ports packTurnTrees (:412): treeDigests in state order.
func PackTurnTrees(repository string, treeDigests []string) ([]byte, error)

// VerifyTreesPack ports verifyTreesPack (:452): it appends to errs and
// returns an error only for what TS throws out of it (mkdtempSync, the
// pack-file write and the cleanup).
func VerifyTreesPack(pack []byte, treeDigests []string, errs *[]string) error
```

**Gotchas:**
- **The catalog** (`:390-410`; correction 1):

  ```go
  // ParseHashCatalog ports turn-proof-bundle.ts:390.
  func ParseHashCatalog(hashes []byte, errs *[]string) Catalog {
  	c := Catalog{Digests: map[string]string{}}
  	text := jsstr.DecodeUTF8(hashes)
  	if !strings.HasSuffix(text, "\n") {
  		*errs = append(*errs, "hashes.txt must end with a trailing newline")
  	}
  	for _, line := range strings.Split(jsstr.TrimEnd(text), "\n") {
  		digest, artifact, ok := catalogLine(line) // /^([a-f0-9]{64})  (.+)$/
  		if !ok {
  			*errs = append(*errs, jsexc.Concat("hashes.txt contains a malformed line: ", line))
  			continue
  		}
  		if !IsSafeRelativeArtifactPath(artifact) {
  			*errs = append(*errs, jsexc.Concat("hashes.txt contains an unsafe artifact path: ", line))
  			continue
  		}
  		if _, dup := c.Digests[artifact]; dup {
  			*errs = append(*errs, jsexc.Concat("hashes.txt declares duplicate artifact: ", artifact))
  		} else {
  			c.Keys = append(c.Keys, artifact)
  		}
  		jsexc.MapSet(c.Digests, artifact, digest)
  	}
  	return c
  }
  ```

  - `catalogLine` is `gitproof.parseCatalogLine`'s logic: 64 lowercase hex characters, two spaces, then at least one character. `.` excludes `\r`, U+2028 and U+2029; `\n` cannot occur after the split. Copy it here; do not import gitproof's unexported helper.
  - `jsexc.Concat` and `jsexc.MapSet` keep V8's string-length and Map-size limits, as in gitproof.
  - Empty strings are not filtered, so `""`, `"\n"` and `"a\n\nb\n"` each report an empty malformed line.
- **`TrimEnd`** strips trailing code units for which `IsWhitespace` holds: the same set as `Trim`, U+FEFF included.
- **`collectFiles`** (`:271-294`) is the Git verifier's walk with the turn texts:
  - `Turn proof bundle contains a symbolic link or special file: <child>`
  - `Turn proof bundle artifact exceeds FaultLine's read limit: <child>`
  - `Turn proof bundle contains too many files to verify safely`
  - `Turn proof bundle exceeds FaultLine's total verification read limit`

  `readdirSync(withFileTypes)` order is libuv's byte order, which `nodefs.ReadDirNames` gives. `relative(root, child)` has `\` replaced by `/`.
- **`assertNoLinksOrSpecialFiles`** (`:205-218`): `Turn proof bundle must be a real directory: <dir>`, then `… contains a symbolic link or special file: <child>`.
- **`assertRegularFile`**: `<label> must be a regular non-symlink file` and `<label> exceeds FaultLine's <n> byte read limit`, with `n` as `String(maximumBytes)`.
- **`safeArtifactPath`**: `Unsafe artifact path: <a>` and `Artifact path escapes its bundle: <a>`.
- **`writeArtifact`**: `mkdirSync(dirname, {recursive: true, mode: 0o700})`, which is `nodefs.MkdirAll(…, 0o700)`. Then `writeFileSync(…, {mode: 0o600, flag: "wx"})`, which is `nodefs.WriteFileBytes(…, 0o600, true)`. Then `assertRegularFile(dest, "artifact <a>")`.
- **packTurnTrees** (`:412-434`):
  1. For each tree in state order: `RunHardenedGit(["rev-list", "--objects", tree], {Repository: repository})`. On `exitCode !== 0 || error !== undefined`, throw `Could not enumerate objects for turn tree <tree>: <stderr.trim() || error || "Git failed.">` (`GitRun.Detail`).
  2. Split stdout on `/\r?\n/`, JS-trim each line, take the first `\s+` field, and add it to an insertion-ordered set.
  3. An empty set throws `Turn tree pack enumeration produced no Git objects.` This is unreachable, because `rev-list --objects <tree>` lists the tree itself (coverage gate reason).
  4. stdin is the ids sorted with `localeCompare` (`canonical.SortLocale`; hex is ASCII, so this equals byte order), joined with `\n`, plus `\n`.
  5. `RunHardenedGit(["pack-objects", "--stdout", "--compression=9"], {Repository, Stdin, HasStdin: true})`. A failure or empty stdout throws `Could not pack turn trees: <detail>`.
  6. Over 128 MiB throws `Turn tree pack exceeds FaultLine's portable artifact limit.`, unreachable because of the 64 MiB output cap (coverage gate reason).
- **verifyTreesPack** (`:452-495`):
  1. `bare = nodefs.Mkdtemp(Join(Tmpdir(), "faultline-turn-proof-pack-"))`, the sync suffix (correction 9). A failure throws out of the verifier into its `catch`.
  2. `init --bare` with `{Repository: bare}`. Its failure gives `Could not create temporary bare repository for pack verification: <detail>`, then return (the `finally` still runs).
  3. `unpack-objects -q --strict` with `{Repository: bare, GitDir: bare, Stdin: pack, HasStdin: true}`.
  4. If that fails:
     - `mkdirSync(join(bare, "objects", "pack"), {recursive, 0o700})`;
     - write `turn-trees.pack` there (mode 0o600, flag `w`, not exclusive);
     - run `index-pack --strict <ToGitPath(packFile)>` with `{Repository: bare, GitDir: bare}`.

     If index-pack also fails, append `Turn tree pack is not a valid Git pack: <first non-empty of: index stderr trimmed, unpack stderr trimmed, index error, unpack error, "Git failed.">` and return.
  5. For each tree: `cat-file -t <tree>` with `{Repository: bare, GitDir: bare}`. A failure, or trimmed stdout other than `tree`, appends `Turn tree object missing or wrong type in portable pack: <tree>`.
  6. `finally`: `removeTemporaryDirectory(bare)`:
     - `RemoveAllRetry(bare, 3, 50ms)` on POSIX;
     - `(20, 250ms)` on Windows, where an `EPERM` or `EBUSY` code is swallowed;
     - any other error is thrown, which the verifier's outer catch reports.
- **The environment.** Every call above goes through `runHardenedGit`, so it gets `MATERIALIZATION_SAFE_GIT_CONFIG` and the stripped environment, with `GIT_DIR` set when `gitDir` is given. `GIT_DEFAULT_HASH` is not passed (correction 3).

**Live tests** (`difftest/turnproof_live_test.go`):
- `TestLiveTurnHashCatalog`: the oracle op `parseTurnHashCatalog({hex})` evaluates `parseHashCatalog`, which is not exported, from `dist/turn-proof-bundle.js`. It uses the `new Function` extraction that `safeGitLists` uses, together with `isSafeRelativeArtifactPath`. It returns `{errors, entries: [[artifact, digest], …]}`.

  Inputs: 5,000 generated catalogs. They cover:
  - valid lines in random order;
  - CRLF line ends, `\r` inside a name, U+2028/U+2029/U+FEFF/NBSP at the end and inside;
  - uppercase hex, 63 and 65 hex digits, one space or three;
  - the unsafe path kinds (`/`, `\`, NUL, `.`, `..`, empty segments);
  - duplicates (also with a different digest);
  - blank lines, an empty catalog and no final newline;
  - lone surrogates and invalid UTF-8;
  - 2,047 to 2,100 entries.

  Go must give equal errors and entries.
- `TestLiveTurnTreesPack`: the oracle ops `packTurnTrees({repository, trees})` and `verifyTreesPack({hex, trees})` evaluate the unexported functions in the same way, under the seeded `mkdtemp` (`oracle.StartSeeded`). The inputs are 200 generated repositories with 2 to 6 trees:
  - shared and unshared blobs, empty trees, nested trees;
  - a tree id that is a commit or a blob;
  - a missing tree;
  - packs that are truncated, byte-flipped, the pack of another repository, or empty;
  - a SHA-256 repository.

  Compare the pack bytes, the error lists, and that the temporary directory is gone afterwards.
- `requireReached("internal/bundle/turnproof/catalog.go", …)` and `pack.go`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/bundle/turnproof/catalog_test.go
package turnproof

import (
	"fmt"
	"strings"
	"testing"
)

func TestCatalogHasNoCapAndOwnMessages(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2100; i++ {
		fmt.Fprintf(&b, "%s  extra/%05d.json\n", strings.Repeat("0", 64), i)
	}
	var errs []string
	c := ParseHashCatalog([]byte(b.String()), &errs)
	if len(c.Keys) != 2100 || len(errs) != 0 {
		t.Fatalf("keys %d errs %q", len(c.Keys), errs)
	}
}

func TestCatalogLineRules(t *testing.T) {
	h := strings.Repeat("a", 64)
	var errs []string
	c := ParseHashCatalog([]byte(h+"  m.json\n\n"+h+"  ../x\n"+h+"  m.json\n"+h+" one-space\n"+h+"  last   "), &errs)
	want := []string{
		"hashes.txt must end with a trailing newline",
		"hashes.txt contains a malformed line: ",
		"hashes.txt contains an unsafe artifact path: " + h + "  ../x",
		"hashes.txt declares duplicate artifact: m.json",
		"hashes.txt contains a malformed line: " + h + " one-space",
	}
	if strings.Join(errs, "|") != strings.Join(want, "|") || len(c.Keys) != 2 || c.Keys[1] != "last" {
		t.Fatalf("%q %q", errs, c.Keys)
	}
}

```

```go
// internal/bundle/turnproof/pack_test.go
package turnproof

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPackRoundTrip(t *testing.T) {
	dir := t.TempDir()
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "-q")
	blob := git("good\n", "hash-object", "-w", "--stdin")
	tree := git("100644 blob "+blob+"\tstate.txt\n", "mktree")
	pack, err := PackTurnTrees(dir, []string{tree, tree})
	if err != nil || len(pack) < 32 || string(pack[:4]) != "PACK" {
		t.Fatalf("%v %d", err, len(pack))
	}
	var errs []string
	if err := VerifyTreesPack(pack, []string{tree}, &errs); err != nil || len(errs) != 0 {
		t.Fatalf("%v %q", err, errs)
	}
	errs = nil
	VerifyTreesPack(pack[:len(pack)-5], []string{tree}, &errs)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "Turn tree pack is not a valid Git pack: ") {
		t.Fatalf("%q", errs)
	}
}
```

```go
// internal/jsstr/jsstr_test.go
func TestTrimEnd(t *testing.T) {
	if got := TrimEnd("a b \t\r\n \u2028\ufeff"); got != "a b" {
		t.Fatalf("%q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/turnproof/ ./internal/jsstr/ ./internal/nodefs/ -run 'TestCatalog|TestPack|TestTrimEnd|TestRemoveAllRetry'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**, with a TS line on every function. Add `TestRemoveAllRetry`: a directory that is read-only for one 50 ms window is removed on the retry, and with no retry the first error is returned. Write the oracle ops.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/bundle/turnproof/ ./internal/jsstr/ ./internal/nodefs/ && GOOS=windows go vet ./internal/bundle/turnproof/ ./internal/nodefs/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveTurnHashCatalog|TestLiveTurnTreesPack' -v > /tmp/s4b-t5.log 2>&1; tail -5 /tmp/s4b-t5.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/turnproof internal/jsstr internal/nodefs internal/gitinv difftest/turnproof_live_test.go difftest/gen/oracle-turnproof.ts difftest/gen/node-oracle.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): uncapped turn hash catalog, turn bundle file walks and the portable tree pack

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `investigateTurnTrees` (`internal/turninv/investigate.go`)

**Files:**
- Create: `internal/turninv/investigate.go`, `internal/turninv/investigate_test.go`
- Modify: `difftest/turninv_live_test.go` (`TestLiveInvestigateTurns`, below Task 3's test), `difftest/gen/oracle-turninv.ts` (its `ops`)

**Interfaces:**
- Consumes:
  - Task 3: the schemas and `StatesFromLedger`, `DuplicateTurnOrdinal`, `ReconstructStable`, `FindTransitions`, `HasNonMonotonic`, `AttributeFailureIntroduction`.
  - `gitproof.VerifyFrozenWitnessRecord`, `gitproof.ReadVerifiedCodexLifecycleLedger` (4a's `ledger.ReadVerified` is the same function).
  - `gitinv.{MaterializeGitTree,MaterializeFrozenOverlays,ComputeFingerprint,Homogeneity,MissingRuntimeMappingDigests,BuildEffectiveRuntimeMapping,MappingEntry,EnvironmentChangedProofMessage}`.
  - `sandbox.{CreatePlan,AuditPlan,Execute,Runner,NativeRunner}`; `redaction.RedactText`; `evidence.TurnPathEvidence`; `clock.{NowMillis,NewUUID}`; `nodefs.{MkdtempAsync,RemoveAllAsync,Tmpdir,Join,Resolve}`; `canonical.DigestJSON`.
- Produces:

```go
// Request is investigateTurnTrees' options (turn-investigation.ts:613).
type Request struct {
	Repository, LedgerPath, ExpectedFrozenDigest, Image string
	FrozenWitness                                       jsjson.Value
	RuntimeMapping                                      []gitinv.MappingEntry // nil is undefined ({})
	Runner                                              sandbox.Runner        // nil: native
	Injected                                            bool                  // options.runner !== undefined
}

// InvestigateTurnTrees ports investigateTurnTrees (:613); the result is the
// ResultSchema-parsed value; an error is what the TS promise rejects with
// (the ledger read, a schema parse, the temp root).
func InvestigateTurnTrees(r Request) (jsjson.Value, error)

// Digest ports turnInvestigationDigest (:866).
func Digest(result jsjson.Value) string
```

**Gotchas:**
- **Order** (`:627-664`):
  1. `executionTrust` (`INJECTED_RUNNER` when injected).
  2. `repository = resolve(r.Repository)`.
  3. `VerifyFrozenWitnessRecord(frozen, expected, true)` and the witness summary: `{valid, errors, frozenDigest, witnessDigest: valid ? frozen.witnessDigest : null, externalDigestStatus, approval}`.
  4. If the witness is invalid or does not MATCH: `baseResult("INVALID_WITNESS", verification.errors, {witness, executionTrust, proofReason: "Frozen witness digest mismatch."})`. The repository is `null` here, and there is no fallback text for an empty error list, unlike Git's.
  5. `ReadVerifiedCodexLifecycleLedger(resolve(ledgerPath))`. Its error propagates (the CLI prints it).
  6. `ledgerDigest = digestJson(ledger)`.
  7. `StatesFromLedger`: a snapshot ZodError propagates.
  8. Fewer than 2 states gives `NO_TURN_SNAPSHOTS`, with `[]` errors and the reason `Need at least two snapshot states (session baseline and/or TURN_TREE_SNAPSHOT events) for turn localization.`
  9. A duplicate ordinal gives `DUPLICATE_TURN_SNAPSHOTS`, with `[DuplicateOrdinalError]` and the reason `Multiple snapshot events share a turnOrdinal; refuse ambiguous localization.`
  10. `tempRoot = MkdtempAsync(Join(Tmpdir(), "faultline-turn-investigation-"))`.
- **baseResult** (`:311-341`):
  - It builds the full result object in the schema's key order and parses it.
  - `introduction` is `{status: "NOT_APPLICABLE", turnOrdinal: null, turnId: null, reason: proofReason ?? errors[0] ?? "Turn investigation did not localize a transition."}`.
  - `proof` is `emptyProof(proofReason ?? errors[0] ?? "Turn investigation did not produce proof.", trust)`, where `emptyProof` uses `TurnPathEvidence(false)`, which gives `"NONE"` and the experimental label.
  - `dockerIsolated` is `trust === "NATIVE_DOCKER"`.
- **Phase 1** (`:674-694`): for each state in sorted order:
  1. Materialize `fp-<padStart(String(turnOrdinal), 4, "0")>-<treeDigest[0:12]>`.
  2. Compute the fingerprint and push it.
  3. Push a materialization error (`errorMessage`) and continue.
  4. `finally`: run cleanup and push its message when it returns one.

  Fingerprints are pushed in success order, so a failure leaves the list shorter.
- **The checks after phase 1:**
  - **Count mismatch:** `EXECUTION_ERROR` with `errors.length ? errors : ["Could not fingerprint every turn state."]`, `environmentFingerprints: fingerprints`, `environmentHomogeneity(fingerprints)`, `runtimeMapping: {}`, and the reason `Could not compute an environment fingerprint for every turn state.` This returns from inside the `try`, so the `finally` (`rm`) still runs before the value is returned.
  - **Heterogeneous with missing mappings:** `ENVIRONMENT_CHANGED` with `[...errors, EnvironmentChangedProofMessage]` and that message as the reason.
  - **A throw from `BuildEffectiveRuntimeMapping`:** `ENVIRONMENT_CHANGED` when the message is the environment-changed text, otherwise `EXECUTION_ERROR`, with `[...errors, message]` and the message as the reason. That covers a mapped image that is not digest-pinned (a ZodError message) and `No digest-pinned runtime image is available…`.
- **Phase 2** (`:755-809`), by index:
  1. A missing state or fingerprint pushes `Missing prepared state or fingerprint at index <i>`. This is unreachable after the count check (coverage gate reason).
  2. Look up the image in the effective mapping. A missing one pushes `No resolved runtime image for fingerprint <digest>`, also unreachable.
  3. Materialize `run-<pad4>-<tree12>`.
  4. Run `MaterializeFrozenOverlays(worktree, frozen, tree)`.
  5. `CreatePlan({witness: {digest: frozenDigest, command}, sourceDirectory: worktree, mode: "DOCKER_ISOLATED", image})`, in exactly that key order (`:776-781`). There are no `environment`, `allowedEnvironment` or `limits` keys: unlike Git, the witness's `timeoutSeconds` never reaches the sandbox.
  6. `AuditPlan`.
  7. For attempt 1..3:
     1. `startedEpoch = NowMillis()`, `startedAt = ISO(startedEpoch)`;
     2. `Execute(plan, runner, injected)`;
     3. `finishedEpoch = NowMillis()`;
     4. `runFact`, which reads `NewUUID()` first.

  Any throw in the body pushes its message and moves to the next state. There is no `configurationError` break, unlike Git.
- **runFact** (`:531-598`):
  - `executionId = digestJson({schemaVersion, executionNonce, stateIndex: state.index, turnId, turnOrdinal, treeDigest, snapshotDigest, environmentFingerprintDigest, frozenDigest, executionAttempt})`.
  - The previews use the location `$.turn-run.stdout`/`$.turn-run.stderr`, not Git's `$.run.`.
  - The unsigned key order is `schemaVersion, executionId, executionNonce, stateIndex, executionAttempt, turnId, turnOrdinal, role, treeDigest, snapshotDigest, environmentFingerprintDigest, frozenDigest, witnessDigest, startedAt, finishedAt, durationMs, overlays, sandbox, result{…17 keys…}`.
  - `runId = digestJson(unsigned)`, then parse `{...unsigned, runId}` with `RunFactSchema`. The parse reorders to the schema's order, where `runId` is second.
- **After the loops:**
  1. `finally`: `RemoveAllAsync(tempRoot)`. Its error replaces the result, as `rm` rejecting would.
  2. `homogeneity` is recomputed.
  3. `ReconstructStable(states, runs, false)`, then transitions, `classifyStatus` and attribution.
  4. `isProof = dockerIsolated && status === "COMPLETED" && errors.length === 0 && transitions.length > 0`.
  5. `reason` is the five-way choice at `:821-829`, verbatim; the INJECTED_RUNNER branch comes first.
  6. `TurnPathEvidence(transitions.length > 0)`.
  7. Build the final object in schema key order and parse it.
- **classifyStatus** (`:600-605`):
  - Any error gives `EXECUTION_ERROR`.
  - Otherwise any `SANDBOX_UNAVAILABLE` reason gives `SANDBOX_UNAVAILABLE`.
  - Otherwise any `ERROR` verdict gives `EXECUTION_ERROR`.
  - Otherwise `COMPLETED`.
- **Clock and UUID order across a run:** `mkdtemp` suffix, then per materialization the index file's `randomUUID` (`git-materialization.ts:288`), the sandbox container name's `randomUUID` (`sandbox.ts:464`), the overlay stage `randomUUID`s, then per attempt `Date.now()`, `Date.now()`, `randomUUID`. The existing Go helpers already read in TS's order; keep calling them in TS's sequence.

**Live test** `TestLiveInvestigateTurns`. The oracle op `investigateTurnTrees({request, path, argvLog})` mirrors `investigateGitRange`'s op: same `withPath`, same argv log, `THREW:` on rejection. 120 generated sessions, each run on both sides under one seed with `clock.ResetForTest` aligned through `seededTick`:
- 2 to 8 states;
- a baseline or not;
- dirty turns;
- the verdict patterns all-pass, pass-to-fail, non-monotonic and all-fail;
- a missing tree (materialization error);
- the fake Dockers `pass-fail`, `broken-state`, `daemon-down` and `flaky-daemon`;
- heterogeneous `package.json` with a complete, partial, invalid and absent mapping;
- an invalid and a mismatched witness;
- a ledger with a duplicate ordinal;
- a ledger with one snapshot.

Compare the result JSON and the argv log. `requireReached("internal/turninv/investigate.go", …)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/turninv/investigate_test.go
//go:build unix

package turninv

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestInvalidWitnessShortCircuits(t *testing.T) {
	frozen := gitinv.FrozenWitnessForTest(t)
	result, err := InvestigateTurnTrees(Request{
		Repository: t.TempDir(), LedgerPath: "/nonexistent/ledger.json", FrozenWitness: frozen,
		ExpectedFrozenDigest: "sha256:" + strings.Repeat("0", 64),
		Image:                "registry.example/faultline-node@sha256:" + strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err) // the ledger is never read for an invalid witness
	}
	if result.Get("status").Str() != "INVALID_WITNESS" || result.Get("proof", "reason").Str() != "Frozen witness digest mismatch." ||
		result.Get("repository").Kind() != jsjson.Null || result.Get("proof", "evidenceGrade").Str() != "NONE" {
		t.Fatal(result)
	}
}
```

Port the remaining cases of `tests/turn-investigation.test.ts` with `gitinv.FakeDockerDirForTest(t, "pass-fail")` on `PATH` and ledgers built by 4a's `ledger.Create`/`ledger.Append` once 4a Task 4 has merged, or the frozen fixture JSON from Task 2's base until then. The cases: a baseline PASS→FAIL is `ATTRIBUTED` and `isProof` true with the native runner, the injected runner is never proof, a duplicate ordinal, fewer than two states, a heterogeneous refusal, a materialization error.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/turninv/ -run TestInvalidWitness`
Expected: FAIL (undefined: `InvestigateTurnTrees`).

- [ ] **Step 3: Implement** by transcription; extend the oracle ops.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/turninv/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveInvestigateTurns -v > /tmp/s4b-t6.log 2>&1; tail -5 /tmp/s4b-t6.log` (podman, under 280 s; lower the session count locally if needed and record it)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/turninv difftest/turninv_live_test.go difftest/gen/oracle-turninv.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): investigateTurnTrees (fingerprint, replay three times per turn state)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Turn proof semantics, the verifier and `fl verify`'s turn branch

**Files:**
- Create:
  - `internal/bundle/turnproof/schemas.go`, `semantics.go`, `verify.go`;
  - `internal/bundle/turnproof/semantics_test.go`, `verify_test.go`;
  - `difftest/turnproof_corpus_test.go` (the header that maps the 15 hostile-corpus rows to their templates, and `TestTurnCorpusCoversEveryRow`).
- Modify:
  - `internal/cli/verify.go` and `internal/cli/cli_test.go` (the turn branch);
  - `internal/jsjson/stack.go` and `internal/jsjson/jsjson_test.go` (`SiteTurnProof`, `SpreadTurnSemantics`);
  - `difftest/replay_test.go` (delete the `deferredTypes` entry for `turn`; add `"turn": true` to `portedTypes`);
  - `difftest/stack_live_test.go` (the `turn` site), `difftest/spread_live_test.go` (the `turn semantics` site);
  - `difftest/coverage_test.go` (drop only the `not yet ported: turn proof bundle verification` entry);
  - `difftest/turnproof_live_test.go` (`TestLiveTurnSemantics`), `difftest/gen/oracle-turnproof.ts` (its `ops`).

**Interfaces:**
- Consumes:
  - Task 3: `turninv.ResultSchema`, `RunFactSchema`, `StableTransitionSchema`, `StatesFromLedger`, `StatesFromValue`, `ReconstructStable`, `FindTransitions`, `SchemaVersion`.
  - Task 5: `ParseHashCatalog`, the file-walk helpers, `VerifyTreesPack`.
  - `gitproof.{CodexLifecycleLedgerSchema,VerifyCodexLifecycleLedger,FrozenWitnessSchema,VerifyFrozenWitnessRecord,ValidateSandboxPlanAudit,CommandDigest}`; `evidence.{TurnPathEvidenceGradeExperimental,TurnPathEvidenceGradeParityReserved,TurnPathEvidenceLabelExperimental,TurnPathEvidenceLabelsExperimental}`; `bundle.{Result,SameCanonical,Without,ParseValue}`; `schema.{Parse,ErrorMessage}`.
- Produces:

```go
package turnproof

var (
	SourceMetadataSchema *schema.ObjectSchema // :98
	ManifestSchema       *schema.ObjectSchema // :113
	LifecycleBoundSchema *schema.ObjectSchema // :90
)

// RunArtifactPath ports :303; TransitionArtifactPath ports :307.
func RunArtifactPath(run jsjson.Value) string
func TransitionArtifactPath(index int) string
// RequiredArtifactPaths ports :311 (localeCompare-sorted).
func RequiredArtifactPaths(result jsjson.Value) []string
// ExpectedManifestArtifacts ports :325.
func ExpectedManifestArtifacts(result jsjson.Value) jsjson.Value

// BindTurnLifecycle ports bindTurnLifecycle (:497): the binding and the
// parsed ledger, or what TS throws.
func BindTurnLifecycle(ledgerInput, result jsjson.Value) (binding, ledger jsjson.Value, err error)

// ValidateProofSemantics ports validateTurnInvestigationProofSemantics (:533).
func ValidateProofSemantics(result, frozenWitness jsjson.Value) []string

// Verification ports TurnProofBundleVerification (:166).
type Verification struct {
	bundle.Result              // Valid, CheckedFiles, Errors, RootDigest, ExternalRootStatus
	Manifest      jsjson.Value // Undefined when absent
}

// Verify ports verifyTurnInvestigationProofBundle (:812); rootProvided is
// expectedRoot !== undefined (correction 2).
func Verify(directory, expectedRoot string, rootProvided bool) Verification
```

```go
// internal/jsjson
const SiteTurnProof Site = …  // appended before siteCount
const SpreadTurnSemantics SpreadSite = …
```

**Gotchas:**
- **Schemas** (`:62-136`).
  - Here `DigestSchema` has the message `expected sha256:<64 lowercase hex characters>`, unlike `turninv`'s.
  - `ArtifactPathSchema` is `z.string().min(1).max(1024).refine(isSafe, "artifact path must be a safe POSIX-relative path")`. zod runs the refine only when `min` and `max` passed (a dirty status still runs refinements in zod 3; check `s.Refine`'s behaviour against `TestLiveNamedSchemas`).
  - `trees` is `.min(2)`. `pack.bytes` is `int().positive()`.
  - `codex` is optional with two optional identifiers (`min(1).max(160).regex(…)`, no message).
  - `lifecycle` is `LifecycleBoundSchema`: `status` is the literal `BOUND`, `path` the literal `lifecycle/ledger.json`, and `transport` the four-value enum.
  - Every literal under `artifacts` is a `z.literal`.
- **`validateTurnInvestigationProofSemantics`** (`:533-690`). Transcribe line by line. The error texts and their order are the golden contract.
  - `→` and `—` do not occur here, but the `"…"` quotes in the evidence-label message are ASCII `"`.
  - The fingerprint loop indexes `result.states[index]` by position, not by `index`.
  - `run.sandbox.commandDigest` is compared with `"sha256:" + sha256(Buffer.from(command, "utf8"))`, which is `gitproof.CommandDigest`.
  - The locked runtime check is the seven-way `||` at `:632-633`, with one message.
  - `[1, 2, 3].every((a) => attempts.has(a))` is checked per state after the size check.
  - `ReconstructStable(states, runs, true)`, then `sameCanonical` against the persisted lists.
  - The `TURN_PROOF is reserved…` text is one line, verbatim from `:678`.
  - The label message interpolates `TURN_PATH_EVIDENCE_LABEL_EXPERIMENTAL` and the legacy label (`:686`).
- **`expectedRunId`/`expectedExecutionId`** (`:338-356`): the turn formula, with the keys listed at `:339-350`. It is not gitproof's.
- **`bindTurnLifecycle`** (`:497-527`), in order:
  1. `CodexLifecycleLedgerSchema.parse`; a throw propagates.
  2. Verify. Invalid, or `headHash === null`: `Cannot bind an invalid Codex lifecycle ledger: <errors.join("; ")>`.
  3. `ledgerDigest === null` or `digestJson(ledger) !==` it: `Lifecycle ledger digest does not match the turn investigation ledgerDigest.`
  4. `turnStatesFromLedger(ledger)` against `result.states` (canonical): `Lifecycle ledger turn states do not canonically match the investigation state sequence.`
  5. No `SESSION_STARTED`: `Cannot bind a lifecycle ledger that has no SESSION_STARTED observation.` This is unreachable after a successful verification (coverage gate reason).
  6. Parse the binding `{status: "BOUND", path, ledgerDigest: result.ledgerDigest, headHash, transport}` with `LifecycleBoundSchema`.
- **The verifier** (`:812-972`). The order that matters:
  1. `!existsSync(root)` returns at once with `["Turn proof bundle directory does not exist"]` and `checkedFiles: 0`, before any other check.
  2. `assertNoLinksOrSpecialFiles(root)`.
  3. Then `assertRegularFile(hashes, …, 4 MiB)` and `assertRegularFile(ROOT.sha256, …, 1024)`, both before either is read.
  4. `storedRoot` is JS-trimmed.
  5. The external root check (correction 2).
  6. The catalog loop: `declaredBytes +=` before the cap check; `break` after pushing `declared artifacts exceed FaultLine's total verification read limit`. Per artifact, errors are `artifact digest mismatch: <a>` or `declared artifact cannot be read safely (<a>): <msg>`.
  7. Four `parseJsonFile`s, labelled `manifest`, `investigation`, `frozen witness` and `source metadata`, each with `<label> JSON validation failed: <msg>`. `JSON.parse` of an undefined payload is not reached: a missing file is the read error.
  8. Four `safeParse`s, in the order manifest, result, frozen, metadata; each failure is `<x> schema validation failed: <zod message>`. Read the zod messages at `SiteTurnProof`: `defer jsjson.UseCallSite(jsjson.SiteTurnProof)()` at the top of `Verify`, like gitproof's.
  9. Only when all four parsed, the remaining checks in source order:
     - digests;
     - the artifact index;
     - required/unexpected catalog entries;
     - `collectFiles` (it can throw `too many files`, which lands in the outer `catch`);
     - the run artifacts, keyed by `runId` with `Map` semantics: a duplicate `runId` in the investigation keeps the last run;
     - the transition artifacts, where `result.transitions[index]` may be undefined;
     - the ledger: its own parse, then `bindTurnLifecycle` in a `try` whose message is prefixed `lifecycle ledger cannot bind to the investigation: `;
     - the tree metadata;
     - the pack: `readBoundedFile` outside any inner `try`, so a missing pack throws to the outer `catch`;
     - `VerifyTreesPack`;
     - `verifyFrozenWitnessRecord`;
     - `errors.push(...semantics)`, guarded with `SpreadFits(len, SpreadTurnSemantics)`.
  10. Return `valid: errors.length === 0` with `manifest` only when the manifest parsed. Otherwise `valid: false`. `checkedFiles` is `catalog.size`.
  11. The outer `catch` pushes `Turn proof bundle verification failed safely: <msg>` and returns `checkedFiles: 0` with whatever `rootDigest` and `externalRootStatus` were set.
- **The CLI branch** (`cli-app.ts:2927-2939`), in `internal/cli/verify.go`:

  ```go
  case turnproof.BundleSchemaVersion:
  	r := turnproof.Verify(root, expectedRoot, rootProvided)
  	first := "Integrity"
  	if r.ExternalRootStatus == "NOT_PROVIDED" {
  		first = "Turn proof self-consistency"
  	}
  	e.out(first + ": " + verdictWord(r.Valid) + "\n")
  	e.out(fmt.Sprintf("Evidence grade: EXPERIMENTAL_TURN\nDeclared files checked: %d\nBundle root: %s\nExternal root: %s\n", r.CheckedFiles, orUnavailable(r.RootDigest), r.ExternalRootStatus))
  	e.printErrorsAndExit(r.Result)
  	return nil
  ```

  The schema sniff (`JSON.parse`, last key wins) is the existing one (correction 12).
- **Stack and spread sites** (correction 14).
  - Add `SiteTurnProof` to every `stackModelFor` branch with the `SiteGitProof` value and a `// measured by TestLiveStackThresholdsMatchNode/turn` comment. Add the row `{name: "turn", base: "turn-two-states", file: "manifest.json", old: \`"integrityScope":"complete-declared-file-set"\`}` to the stack test. Then put the linux/amd64 value you measure in podman (the test logs the bisected depth) into the model. The arm64 and darwin values come from `stack-live` in Task 11.
  - Do the same for `SpreadTurnSemantics`, with `buildTurnSemanticsBundle`: `turn-two-states` whose `investigation.json` gets `n` extra runs that each produce one semantic error, rehashed.

**Live test** `TestLiveTurnSemantics`. The oracle ops are `validateTurnInvestigationProofSemantics({result, frozen})` and `bindTurnLifecycle`, the latter evaluated from `dist/turn-proof-bundle.js` because it is not exported. The inputs are 3,000 mutations of Task 2's two bases: `investigation.json` with `gen.CorruptLeaf`, `gen.SwapLeaf`, run and state reorders, re-signed run ids, and the frozen witness edited. Each is parsed with `ResultSchema` first, and only parsing inputs are compared. `requireReached` on `semantics.go` and `verify.go`.

**Golden replay.** With the `turn` type ported, `TestGoldenReplay` runs every Task 2 case against the Go verifier.

- [ ] **Step 1: Write the failing tests**

```go
// internal/bundle/turnproof/verify_test.go
package turnproof

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// base is difftest/testdata/bases/turn-two-states, found from this file.
func base(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..", "..", "difftest", "testdata", "bases", "turn-two-states")
}

func TestVerifyBaseIsValid(t *testing.T) {
	root, err := os.ReadFile(filepath.Join(base(t), "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(base(t), strings.TrimSpace(string(root)), true)
	if !v.Valid || v.ExternalRootStatus != "MATCH" || v.CheckedFiles != 14 {
		t.Fatalf("%+v", v.Result)
	}
}

func TestEmptyExpectedRootIsProvided(t *testing.T) {
	v := Verify(base(t), "", true)
	if v.Valid || v.ExternalRootStatus != "MISMATCH" || v.Errors[0] != "externally supplied turn proof root is not a sha256 digest" {
		t.Fatalf("%+v", v.Result)
	}
}
```

```go
// internal/cli/cli_test.go
func TestVerifyTurnBundleLines(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	stdout, _, code := run("verify", filepath.Join(filepath.Dir(here), "..", "..", "difftest", "testdata", "bases", "turn-two-states"))
	if code != 0 || !strings.HasPrefix(stdout, "Turn proof self-consistency: VALID\nEvidence grade: EXPERIMENTAL_TURN\nDeclared files checked: 14\nBundle root: sha256:") ||
		!strings.HasSuffix(stdout, "\nExternal root: NOT_PROVIDED\n") {
		t.Fatalf("%d %q", code, stdout)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/turnproof/ ./internal/cli/ -run 'TestVerifyBase|TestEmptyExpectedRoot|TestVerifyTurnBundleLines'`
Expected: FAIL (undefined: `Verify`; `not yet ported: turn proof bundle verification`).

- [ ] **Step 3: Implement** by transcription, the oracle ops, the stack and spread rows, and `turnproof_corpus_test.go`. Its `TestTurnCorpusCoversEveryRow` loads `mutations.json` and requires every new template id named in Task 2's table to exist with a `turn-` base. Its header lists the 15 rows, the N/A reason for row 14, and correction 11's platform note.

- [ ] **Step 4: Run to verify the tests pass and the corpus matches TS**

Run: `go test ./internal/bundle/turnproof/ ./internal/cli/ ./internal/jsjson/ && go test -tags faultline_test ./difftest/ -run 'TestGoldenReplay/turn-|TestTurnCorpusCoversEveryRow' > /tmp/s4b-t7.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4b-t7.log | tail; FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveTurnSemantics|TestLiveStackThresholdsMatchNode/turn|TestLiveSpreadLimitsMatchNode/turn' -v 2>&1 | tail -5` (podman; split the golden replay by base when it runs past 280 s)
Expected: PASS. Every corpus case has identical stdout, stderr and exit status in Go and TS (spec 4.2 point 5).

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/turnproof internal/cli/verify.go internal/cli/cli_test.go internal/jsjson difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): turn proof bundle verifier and fl verify's turn branch; hostile turn corpus replays

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```
