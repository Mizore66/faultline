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
