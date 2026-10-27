# Go Slice 4c (Codex, repair, attestation, provenance) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs these commands byte-identically to frozen TS `node dist/cli.js`:
- `witness implement` (`--with-codex`, `--allow-codex-full-auto`);
- `repair --bundle` (`--with-codex`, `--allow-codex-full-auto`, `--instructions-only`, `--keep-worktree`);
- `repair brief` (`--input` and `--live`) and `repair verify`;
- `attest create|verify`;
- `provenance create|verify`.

"Byte-identical" covers stdout, stderr, exit status, the files written with their modes, the git and Docker argv, and the argv `codex` and `gh` receive. It also covers the request body the OpenAI Responses API receives. Writer goldens, live Node-oracle tests and TS cross-verification of every artifact Go writes prove it.

**Architecture:**
- **New packages.** Each TS module becomes one Go file, in four packages:
  - `internal/codexloop`: `codex-loop.ts` and `runners/codex-runner.ts`.
  - `internal/repair`: `repair-brief.ts`, `repair-brief-store.ts`, `repair-worktree.ts` and `openai-response.ts`.
  - `internal/attest`: `attestation.ts`.
  - `internal/provenance`: `github-provenance.ts`.
- **Existing packages.**
  - The repaired-run helpers join their TS file's other port in `internal/bundle/prevention`.
  - `nodeproc` gains spawnSync's `timeout` and an `execFileSync` port; `nodefs` gains `cpSync`.
- **Harness.** The slice 3 writer replay gains:
  - three fakes: `codex`, `gh`, and an OpenAI HTTP server;
  - a fetch-shim preload for TS and a `faultline_test`-only base-URL override for Go;
  - content-addressed masks for the Git objects a repair creates (`<COMMIT:…>`, `<TREE:…>`, `<BLOB:…>`);
  - TS cross-verification of attestations, repair briefs, provenance receipts and repair-written prevention packages.

**Tech Stack:** Go 1.27 (standard library only), TypeScript generators run with the repo's `tsx`, Node 22.22.2, git, POSIX `sh` fakes. Docker is involved only through the fake Dockers.

**Spec:** [docs/superpowers/specs/2026-10-27-go-slice4-turns-codex-design.md](../specs/2026-10-27-go-slice4-turns-codex-design.md) (PR 4c scope; Sections 2 and 3 apply). Read it before any task. Also read the [slice 3b plan](2026-10-26-go-slice3b-incident-prevention.md) and the [slice 3a plan](2026-10-20-go-slice3a-runtime-doctor-init.md): this plan reuses their packages, helpers and harness by name.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- **JSON.**
  - Never use `encoding/json` on a value that is hashed, compared canonically, written, printed or sent to OpenAI.
  - `JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`. A bare `JSON.stringify(x)` is `jsjson.Stringify(v)`.
  - `canonicalJson` is `canonical.CanonicalJSON`; `digestJson` is `canonical.DigestJSON`.
  - Attestation receipts, provenance receipts, repair-brief files and prevention files are canonical JSON plus `\n`. `repair-meta.json`, `codex-thread.json`, `verification.json`, `prevention-export.json` and `hard-guard-digests.json` are `JSON.stringify(…, null, 2)` plus `\n`. Stdout is `JSON.stringify(…, null, 2)` in TS insertion order.
- **Frozen TS.**
  - `src/`, `package.json` and `pnpm-lock.yaml` are never modified.
  - The oracle is `node dist/cli.js` built from the current `src/`, which equals `ce99a79` (`v0.1.10-buildweek`), on Node 22.22.2.
  - Final goldens come from podman `localhost/r73img` (Node 22.22.2) running as uid 1000. Running as root changes the results of permission-dependent cases. `/opt/homebrew/bin/node` on the dev Mac is 22.23.x and must not make goldens.
- **Strings.**
  - JS strings are WTF-8 (`internal/jsstr`); TS `slice` and `length` count UTF-16 units.
  - `localeCompare` is `canonical.SortLocale` or `canonical.LocaleCompare`. A bare `.sort()` is `jsstr.CompareUTF16`.
  - JS `trim` is `jsstr.Trim`.
  - JS regexes are `jsre.MustCompile(source, flags)`.
  - `sha256(string)` hashes the string's UTF-8 encoding, with lone surrogates as U+FFFD, as `createHash().update(string)` does.
  - Every ported function carries a comment naming its TS source, e.g. `// Port of src/attestation.ts:262 writeNewReceipt.`
- **Time and child processes.**
  - Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock`. `TestNoDirectClockReads` enforces this. Each task lists the clock and UUID reads in TS order.
  - Child processes start only through `nodeproc.Spawn` (async: `runHardenedGit`), or `nodeproc.SpawnSyncWith` and `nodeproc.ExecFileSync` (sync), matching the TS call site.
  - HTTP goes only through `internal/repair`'s Responses client (Task 9).
- **Commits.**
  - Commit with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`. Never change the global git config. Never run plain `gh`.
  - End each commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
  - The TruffleHog pre-commit hook must never be bypassed.
  - No real-looking secret is committed. The fake OpenAI key is the literal `fl-test-key` (in `writers.json` env values and in Go tests); never use an `sk-` value.
- **Output.** `Next: pnpm fl help` and the TS `next`/`nextCommands` strings stay verbatim.
- **Shell commands.** Each must finish in under 5 minutes: `timeout 280 … > log 2>&1; tail`. The full TestWriterReplay takes over 40 minutes, so run filtered subsets locally. CI runs the full suite.
- **Shared files.** Several tasks append to the same files, and each appends at the end:
  - `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl` and `.github/scripts/windows-writer-skips.txt`;
  - the op table in `difftest/gen/node-oracle.ts`;
  - the dispatch switch in `internal/cli/cli.go`, one `case` line each.

  Appending at the end keeps parallel branches union-mergeable. Goldens are regenerated with `FAULTLINE_WRITERS_ONLY=<the task's id prefix>`, so a task's golden diff holds only its own lines.
- **Independence from 4a/4b.** This PR branches off `f22d48f`, so it builds the harness pieces it needs itself (Task 1). PRs 4a and 4b may build a `<COMMIT>` mask and a writer-template `input` field of their own. The controller dedupes at merge: one mask implementation in `gen-writers.ts` and `writer_test.go`, one KNOWN_DIFFERENCES entry.

## Execution waves

Tasks in one wave touch disjoint files, apart from the append-only shared files above, so they can run in parallel. A task may start once every task it depends on has merged.

- **Wave 1** (no dependencies): Tasks 1, 3, 4, 5, 6, 7 and 8.
  - 1: harness fakes, environment and cross-verification.
  - 3: spawnSync `timeout` and `execFileSync`.
  - 4: `cpSync`.
  - 5: the 3b carry-overs, the hard-guard helpers and the `DraftInput` cleanup.
  - 6: the evidence packet, brief validation and response extraction.
  - 7: the attestation library.
  - 8: `implementWitnessWithCodex`.
- **Wave 2:**
  - Task 2 (the created-Git-object and repaired-run masks) after 1.
  - Tasks 9 (brief store) and 10 (live brief client) after 6.
  - Task 11 (repair worktree) after 4.
  - Task 12 (Codex runner) after 3.
  - Task 13 (repaired-run helpers) after 3 and 5.
  - Task 14 (provenance library) after 3.
  - Task 15 (`fl attest`) after 1 and 7.
- **Wave 3:**
  - Task 16 (repair orchestration and frozen-witness verifier) after 2, 5, 11, 12 and 13.
  - Task 17 (`fl provenance`) after 1 and 14.
  - Task 18 (`fl repair brief` and `fl repair verify`) after 1, 9 and 10.
  - Task 19 (`fl witness implement`) after 1, 8 and 12.
- **Wave 4:** Task 20 (`fl repair --bundle`) after 2, 16 and 18. It extends 18's `internal/cli/repair.go`.
- **Wave 5:** Task 21 after all of them: the fault matrix, the 4c end to end without Node, CI, the coverage gate, KNOWN_DIFFERENCES, docs and a full podman regeneration with no diff.

Dependencies (`A → B`: B needs A):

```
1 → 2, 15, 17, 18, 19      2 → 20       3 → 12, 13, 14     4 → 11
2 → 16  5 → 13, 16          6 → 9, 10    7 → 15             8 → 19
9 → 18      10 → 18        11 → 16      12 → 16, 19        13 → 16
14 → 17     16 → 20        18 → 20      every task → 21
```

## Corrections to the spec found while planning

Task 21 folds these into the spec.

1. **A commit mask alone is not enough (spec 2.2).** `commitRepairedWorktreeState` builds its tree from the worktree on disk (`prevention-from-artifacts.ts:438-464`). That worktree holds:
   - `.faultline-repair/README.md`, whose text names the absolute managed root (`repair-worktree.ts:120-129`);
   - the materialized frozen overlays.

   The README blob's id, the tree id and the commit id therefore all depend on the case directory, and the commit id also depends on the wall clock. Four more things vary with the machine or the path:
   - **Loose objects.** The blobs, trees and commits git writes are zlib-compressed loose objects under `repo/.git/objects/`, so their bytes depend on git's zlib build.
   - **Reflog.** With `--keep-worktree`, `.git/worktrees/worktree/logs/HEAD` records an identity and a time.
   - **Verifier execution id.** It hashes `worktree: <absolute path>` (`codex-loop.ts:243-255`), and no file stores the object it hashes.
   - **Repaired run ids.** They hash the commit (`prevention-from-artifacts.ts:514-525`).

   Task 2 therefore builds content-addressed masks for every Git object a case creates (`<COMMIT:…>`, `<TREE:…>`, `<BLOB:…>`, checked through `git cat-file -p` on the side that made them), a loose-object and reflog rule, and two relocation seeds: the verifier execution id and the repaired run id. Both are recomputed from their fields, so a wrong value stays unmasked and fails.
2. **`commit-tree` needs an identity from the repository config.** The hardened environment keeps only `PATH` and sets `GIT_CONFIG_GLOBAL=/dev/null`. Without `user.name` and `user.email` in `repo/.git/config`, git fails with `fatal: unable to auto-detect email address (got '<user>@<host>.(none)')`, which depends on the machine. The repair scenarios therefore add a `[user]` section to the repository config. The identity-less failure is compared only by the same-machine live test (Task 13).
3. **The repair worktree uses the investigation's repository** (`codex-loop.ts:390`). The committed bases record `/tmp/faultline-git-proof-repository-*`, which does not exist. Every successful `repair --bundle` case therefore runs `investigate git` first in the same workspace and passes `<STDOUT:0:/proofBundle/directory>`. A committed base gives the `REPAIR_SETUP_FAILED` case (`Git rev-parse --show-toplevel failed: …`).
4. **The 4c end to end (spec 2.6) is reordered.**
   - `repair verify` checks a repair brief, so `repair brief --input` runs before it.
   - `repair --bundle` already writes the prevention package. `prevention write --from-bundle` then rebuilds a second package from that package's own `repaired-runs.json` (whose `runs` array the CLI accepts as it is), its `prevention.json` repaired commit and tree, and the managed root's `artifacts/repair.patch`.
   - `attest create` attests the Git bundle: a prevention package takes `proofBundleSummary`'s demo branch and is refused (`cli-app.ts:2453-2459`).
5. **The hard-guard helpers and the two new `ArtifactOptions` have no effect the CLI can reach.**
   - `hardGuardDigestsFromStrings` and `writeHardGuardDigestSidecar` have no caller in `src/`.
   - `repair` passes `repairBaseTree`, but always the first-bad tree of the same bundle, and passes no hard-guard digests.
   - Their refusals and outputs are therefore checked by live tests. The coverage gate lists them with that reason.
6. **The harness must not inherit `GITHUB_*`.** GitHub Actions sets `GITHUB_SHA`, `GITHUB_REPOSITORY` and the rest, and `provenance create` reads them. `inheritedOut` drops `GITHUB_ACTIONS` only, so a golden made in podman would drift on CI. Task 1 drops every `GITHUB_*` name.
7. **Codex failures reported as "could not be started".**
   - `--allow-codex-full-auto` is checked inside the runner (spec 3.5). Without it, the runner's throw becomes `CODEX_UNAVAILABLE` with `transport: "NONE"` and the note `Codex CLI could not be started: Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex.` (`witness implement`: `Codex drafting requires …`).
   - A failed `collectRepairPatch` is reported the same way, because it throws inside the same try (`codex-loop.ts:479, 502-506`).
8. **`Codex CLI was not found on PATH` cannot be reached.** `resolveOnPath` falls back to the bare name and never returns `undefined` (`runners/codex-runner.ts:36, 83-87`). The check is not ported; a TS-cited comment says why.
9. **Codex timeouts cannot be goldens.** Waiting out TS's 120 s and 180 s `spawnSync` timeouts is too slow for a writer case. The writer goldens get the same `exitCode: null` from a fake that kills itself with SIGTERM. The timeout itself is compared live against Node's `spawnSync` (Task 3).
10. **The writer-template `input` field is not needed by 4c.** No 4c command reads stdin, and the `codex` prompt is a child's stdin, not fl's. PR 4c does not build the field; 4a builds it.
11. **`attest create` verifies the bundle before it checks `--receipt`, `--subject` and `--issuer`** (`cli-app.ts:2466-2471`). The attestation store's directories are made with `mkdirSync`'s default mode (0777 under the umask), not 0700 (`attestation.ts:146`).
12. **The live repair brief HTTP client.** Node's `fetch` ignores `HTTPS_PROXY`, uses its bundled CA roots and strips a UTF-8 BOM from the response body (`response.text()` and `.json()`). Go's client sets `Proxy: nil` and strips the BOM. The TLS roots and the request headers other than `Authorization` and `Content-Type` go to KNOWN_DIFFERENCES.

## File Structure

```
internal/nodeproc/spawn.go               + SpawnSyncOptions.Timeout (SIGTERM, ETIMEDOUT); ExecFileSync (execFileSync + checkExecSyncError)
internal/nodefs/cp.go                    cpSync(src, dest, {recursive, dereference:false, errorOnExist:true, force:false}) (Node v22.22.2 lib/internal/fs/cp/cp-sync.js)
internal/incident/draft.go               DraftInput cleanup (drop the ignored Symptom/Language/Behavior/TimeoutSeconds)
internal/bundle/prevention/artifacts.go  + ArtifactOptions.RepairBaseTree/HasRepairBaseTree, HardGuardArtifactDigests/HasHardGuardArtifactDigests
internal/bundle/prevention/hardguard.go  src/prevention-from-artifacts.ts:542-551 (hardGuardDigestsFromStrings, writeHardGuardDigestSidecar)
internal/bundle/prevention/repaired.go   src/prevention-from-artifacts.ts:342-540 (commitRepairedWorktreeState, collectRepairedPreventionRuns)
internal/bundle/demo/verify.go           + VerifyWithManifest (the parsed manifest TS returns beside the result)
internal/gitinv/hardened.go              export ToGitPath
internal/nodefs/dirent.go                + ReadDirTypes (readdirSync withFileTypes: name, isDirectory, isFile)
internal/jsdate/jsdate.go                IsCanonicalISO: `new Date(v).toISOString() === v` without a general Date parser
internal/repair/brief.go                 src/repair-brief.ts:1-188 (schemas, createRepairEvidencePacket, validateRepairBrief, repairBriefJsonSchema)
internal/repair/response.go              src/openai-response.ts
internal/repair/live.go                  src/repair-brief.ts:195-232 (proposeRepairBriefWithGpt)
internal/repair/openai_release.go        //go:build !faultline_test: the fixed https://api.openai.com/v1/responses
internal/repair/openai_test_url.go       //go:build faultline_test: FAULTLINE_TEST_OPENAI_URL override
internal/repair/store.go                 src/repair-brief-store.ts
internal/repair/worktree.go              src/repair-worktree.ts
internal/codexloop/runner.go             src/runners/codex-runner.ts
internal/codexloop/implement.go          src/codex-loop.ts:1-184
internal/codexloop/repair.go             src/codex-loop.ts:186-197, 275-670
internal/codexloop/verifier.go           src/codex-loop.ts:199-273
internal/attest/attest.go                src/attestation.ts
internal/provenance/provenance.go        src/github-provenance.ts
internal/cli/attest.go                   cli-app.ts:2431-2490
internal/cli/provenance.go               cli-app.ts:2497-2536
internal/cli/repair.go                   cli-app.ts:2672-2801
internal/cli/witness.go                  + implement (cli-app.ts:1169-1203)
internal/cli/cli.go                      + dispatch attest, provenance, repair
difftest/gen/fetch-shim.mjs              TS preload: redirect https://api.openai.com/v1/responses to FAULTLINE_TEST_OPENAI_URL
difftest/testdata/fakecodex/<name>/codex sh fakes (recorder style: log path written in)
difftest/testdata/fakegh/<name>/gh       sh fakes
difftest/testdata/fakeopenai/<name>.json scripted Responses API replies
difftest/fakeopenai.go                   the fake OpenAI server (Go side); difftest/gen/fake-openai.ts (TS side)
difftest/gen/gen-writers.ts, difftest/writer_test.go   + fakeCodex/fakeGh/fakeOpenAI keys, <FILE:> in env, GITHUB_* dropped, cross-verify kinds, Git-object masks, repair seeds
difftest/gen/scenarios.ts                + repair-*, attest-*, provenance-*, witness-implement-* scenarios (appended)
difftest/testdata/writers.json, golden/writers.jsonl, .github/scripts/windows-writer-skips.txt  (appended)
difftest/{repair,attest,provenance,codex}_live_test.go, difftest/cpsync_live_test.go
```

---

### Task 1: Harness: fake `codex`, fake `gh`, fake OpenAI, environment and cross-verification

**Files:**
- Create:
  - the fakes: `difftest/testdata/fakecodex/{edits,noop,probe-fail,probe-empty,probe-crlf,exit-nonzero,killed,flood,outside-edits,symlink-edit}/codex`, `difftest/testdata/fakecodex/README.md`, `difftest/testdata/fakegh/{pass,fail,long-fail}/gh`, `difftest/testdata/fakegh/README.md`;
  - the TS side: `difftest/gen/fetch-shim.mjs`, `difftest/gen/fake-openai.ts`;
  - the Go side: `difftest/fakeopenai.go` (`//go:build faultline_test`), `difftest/fakes_test.go`.
- Modify:
  - `difftest/gen/gen-writers.ts`: the new template keys, the `<FILE:…>` placeholder in env values, the cross-verify kinds and the fake OpenAI server.
  - `difftest/gen/scenarios.ts`: `inheritedOut` drops every `GITHUB_*`; `runCli` adds `--import fetch-shim.mjs` after the seed preload; `writerEnv` handles the new keys.
  - `difftest/writer_test.go`: the same five changes as its twin.
  - `.gitattributes`: `difftest/testdata/fakecodex/** text eol=lf` and `difftest/testdata/fakegh/** text eol=lf`.

**Interfaces:**
- Produces, as writer template keys (TS `TEMPLATE_KEYS` and Go `writerTemplateKeys` both gain them):
  - **`fakeCodex: "<name>"`.** The case gets a copy of `difftest/testdata/fakecodex/<name>/codex` at `<HOME>/fakecodex/codex` (mode 0755), with the case's argv log path written into its `log=` line, as `writeRecorders` does. `FAULTLINE_CODEX_BIN` is that path.
  - **`fakeCodexOnPath: true`.** As above, but `<HOME>/fakecodex` goes on `PATH` right after the recorders, and `FAULTLINE_CODEX_BIN` is not set.
  - **`fakeGh: "<name>"`.** `difftest/testdata/fakegh/<name>` goes on `PATH` after the recorders. The fake `gh` appends its record to `$FAULTLINE_ARGV_LOG` itself, since `gh` inherits fl's whole environment.
  - **`fakeOpenAI: "<ws-relative reply file>"`.** The harness starts the fake OpenAI server for the case and sets `FAULTLINE_TEST_OPENAI_URL=http://127.0.0.1:<port>/v1/responses`.
- Produces, as the record formats (`\x1f` separates arguments, `\x1e` ends a record, as in `difftest/testdata/recorder/git`):
  - `codex` record: `codex <args…>`, then `STDIN=<stdin bytes>`, then `PATH=<$PATH>`, then `ENV=<the sorted names of the other variables, joined by ','>`. The names exclude `PWD`, `OLDPWD`, `SHLVL` and `_`, which `sh` sets on its own.
  - `gh` record: `gh <args…>`.
  - `openai` record: `openai`, then `<method>`, `<path>`, `<Authorization header>`, `<Content-Type header>`, `<request body>`.
- Produces, as functions:
  - TS `writtenAttestation(stdout)`, `writtenRepairBrief(stdout)`, `writtenProvenanceReceipt(stdout)`, `writtenRepairPrevention(stdout)`.
  - Their Go twins `writtenAttestation(stdout string) (path, receiptDigest string, ok bool)`, `writtenRepairBrief(stdout string) (dir, artifactDigest string, ok bool)`, `writtenProvenanceReceipt(stdout string) (path string, ok bool)`, `writtenRepairPrevention(stdout string) (dir, rootDigest string, ok bool)`.
  - Go `startFakeOpenAI(t *testing.T, ws, replyFile, argvLog string) (url string)`.
  - Go `installFakeCodex(t testing.TB, repo string, r writerRoots, name string) (bin string)`, which writes `<HOME>/fakecodex/codex` with the case's argv log path in it and returns the path. `writerEnv` uses it, and so do the self-test and Task 21's end to end.
- The reply file format, read at request time from the workspace: `{"status": <int>, "contentType": "<header>", "body": "<raw response text>"}`.

**Gotchas:**
- **Fake `codex` behaviour.** Every fake answers `--version`; only the `probe-*` fakes change that answer. A plain answer is stdout `codex-cli 0.0.0-fake` and exit 0. `exec --full-auto <prompt>` then reads the prompt on stdin, records it, and acts by variant:
  - `edits`:
    - if `README.faultline.txt` is in the cwd (the `witness implement` overlay directory), rewrites `witness.mjs` with a fixed passing witness;
    - otherwise writes `good\n` to `state.txt`.

    Then prints `fake codex: edited` and exits 0.
  - `noop`: prints `fake codex: nothing to do`, exits 0, changes nothing.
  - `probe-fail`: `--version` exits 2 with stderr `codex: unsupported\n`.
  - `probe-empty`: `--version` prints `\n` and exits 0.
  - `probe-crlf`: `--version` prints `  codex-cli 1.2.3\r\nbuild fake\r\n`.
  - `exit-nonzero`: writes `bad\n` to `state.txt`, then exits 3 with stderr `codex failed\n`.
  - `killed`: `kill -TERM $$`. TS sees `status: null`, which is what a timeout gives (correction 9).
  - `flood`: prints 6,000 `é` (12,000 bytes) to stdout and 5,000 `x` to stderr, then exits 0. This checks the 4,096-unit preview slices.
  - `outside-edits`: as `edits`, and also writes `../outside.txt`, which lands in the managed repair root.
  - `symlink-edit`: replaces `state.txt` with a symlink to a new `good.txt` that holds `good\n`.
- **Fake `gh`:**
  - `pass` prints `Loaded digest sha256:… for file://…` (the receipt path from its argv) and exits 0.
  - `fail` prints stderr `Error: verifying with issuer "sigstore.dev"\r\n` and exits 1.
  - `long-fail` prints 9,000 bytes on stdout and stderr, so the detail is cut at 8,000 UTF-16 units.
- **No real tool may answer.**
  - The developer machine has a real `codex` and a real `gh` on `PATH`.
  - A case that reaches the Codex runner must set `fakeCodex`, `fakeCodexOnPath` or `pathTools`.
  - A case that reaches `gh attestation verify` must set `fakeGh` or `pathTools`.
  - The loaders reject an unknown key, and the tasks' case lists say which fake each case uses.
- **`GITHUB_*`.** `inheritedOut` drops every `GITHUB_*` name on both sides (correction 6). A provenance case sets them explicitly in `env`.
- **`<FILE:rel>` in env values.** The value becomes the trimmed text of the workspace file `rel` when the invocation starts, as `fileArgument` already does for argv. Provenance cases need it for `GITHUB_SHA`.
- **The fetch shim** (`difftest/gen/fetch-shim.mjs`) does this:
  - It wraps `globalThis.fetch`. When `FAULTLINE_TEST_OPENAI_URL` is set and the URL is exactly `https://api.openai.com/v1/responses`, it calls the real fetch with the test URL and the same `init`.
  - Otherwise it passes the call through unchanged.
  - It reads no clock and no UUID, so the seed order is unchanged.
  - `runCli` loads it after `seed.mjs` for every TS writer invocation and every oracle start. Starting the oracle with it is harmless when the variable is unset.
- **The fake OpenAI server** (TS twin in `fake-openai.ts`, Go twin in `fakeopenai.go`) does this:
  - It listens on `127.0.0.1:0`.
  - For each request it appends one `openai` record to the case's argv log, then answers from the reply file: the status, `Content-Type`, and the body bytes as UTF-8.
  - It never reads a clock.
  - Its port appears in no output, because every scripted reply answers.
- **Cross-verification, after the tree and the argv log are read:**
  - **Attestation:** TS `fl attest verify <receiptId> --store <dirname(path)> --expect-digest <receiptDigest>` must exit 0. The receipt id is the basename without `.json`.
  - **Repair brief:** TS `fl repair verify <dir> --expect-digest <artifactDigest>` must exit 0 with `"valid": true`.
  - **Provenance receipt:** TS `fl provenance verify --bundle <the case argv's --bundle> --receipt <path> --trust <ws>/trust.json --attestation-bundle <ws>/attestation.sigstore.json`, with fake `gh` `pass` on `PATH`. It must exit 0. The provenance scenarios write both files.
  - **Repair prevention package** (`preventionExport.directory` and `rootDigest` with `ok: true`): TS `fl verify <dir> --expect-root <rootDigest>` must print `Integrity: VALID` and `External root: MATCH`.
  - The Go replay runs the same checks with TS on what Go wrote.

- [ ] **Step 1: Write the failing self-tests**

```go
// difftest/fakes_test.go
//go:build faultline_test && unix

package difftest

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeCodexRecordsArgvStdinAndEnv(t *testing.T) {
	repo := repoRoot(t)
	r := makeWriterRoots(t)
	bin := installFakeCodex(t, repo, r, "edits") // <HOME>/fakecodex/codex with the log path written in
	work := t.TempDir()
	cmd := exec.Command(bin, "exec", "--full-auto", "fix it")
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = strings.NewReader("fix it")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	records := readArgvLog(r.argvLog)
	if len(records) != 1 || !strings.HasPrefix(records[0], "codex exec\x1f--full-auto\x1ffix it\x1fSTDIN=fix it\x1fPATH=") ||
		!strings.HasSuffix(records[0], "\x1fENV=") {
		t.Fatalf("record %q", records)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "state.txt")); string(b) != "good\n" {
		t.Fatalf("state.txt %q", b)
	}
}

func TestFakeOpenAIRecordsRequestAndReplies(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "reply.json"), []byte(`{"status":500,"contentType":"text/plain","body":"upstream down"}`), 0o644)
	log := filepath.Join(t.TempDir(), "argv.log")
	url := startFakeOpenAI(t, ws, "reply.json", log)
	req, _ := http.NewRequest("POST", url, strings.NewReader(`{"model":"gpt-5.6"}`))
	req.Header.Set("Authorization", "Bearer fl-test-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || string(body) != "upstream down" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	want := "openai\x1fPOST\x1f/v1/responses\x1fBearer fl-test-key\x1fapplication/json\x1f{\"model\":\"gpt-5.6\"}"
	if got := readArgvLog(log); len(got) != 1 || got[0] != want {
		t.Fatalf("%q", got)
	}
}

func TestInheritedOutDropsGitHub(t *testing.T) {
	for _, name := range []string{"GITHUB_SHA", "GITHUB_ACTIONS", "GITHUB_REPOSITORY", "OPENAI_API_KEY", "GIT_DIR", "FAULTLINE_CODEX_BIN"} {
		if !inheritedOut(name) {
			t.Errorf("%s is inherited", name)
		}
	}
}

func TestWrittenArtifactDetectors(t *testing.T) {
	if p, d, ok := writtenAttestation("{\n  \"status\": \"ATTESTED\",\n  \"path\": \"/s/r1.json\",\n  \"receiptDigest\": \"sha256:aa\"\n}\n"); !ok || p != "/s/r1.json" || d != "sha256:aa" {
		t.Fatal(p, d, ok)
	}
	if _, _, ok := writtenRepairBrief("{\"status\":\"PERSISTED\"}\n"); ok {
		t.Fatal("no directory: not a brief")
	}
	if d, r, ok := writtenRepairPrevention(`{"status":"REPAIR_CANDIDATE_VERIFIED","preventionExport":{"ok":true,"directory":"/p","rootDigest":"sha256:bb"}}`); !ok || d != "/p" || r != "sha256:bb" {
		t.Fatal(d, r, ok)
	}
	if _, _, ok := writtenRepairPrevention(`{"preventionExport":{"ok":false,"reasons":["x"]}}`); ok {
		t.Fatal("a failed export is not a package")
	}
}
```

The TS generator gets the same detector checks as an `if (…) throw` self-check at start-up, as `checkTemplates` does.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run 'TestFakeCodex|TestFakeOpenAI|TestInheritedOutDropsGitHub|TestWrittenArtifactDetectors' > /tmp/s4c-t1.log 2>&1; tail -5 /tmp/s4c-t1.log`
Expected: FAIL (undefined: `installFakeCodex`, `startFakeOpenAI`, `writtenAttestation`; `GITHUB_SHA is inherited`).

- [ ] **Step 3: Implement**

Write the fakes as POSIX `sh`, in the recorder's style:
- `self` and `log=` lines;
- one `printf '%s\036'` per record;
- long-running or killing children use `exec`.

Write each README's table as `fakedocker/README.md` does. Add the keys, the env placeholder, the server and the cross-verify kinds to both twins. Keep the function names identical across the twins, with a comment naming the twin.

- [ ] **Step 4: Run to verify the tests pass, and that no existing golden moved**

Run: `go test -tags faultline_test ./difftest/ -run 'TestFakeCodex|TestFakeOpenAI|TestInheritedOutDropsGitHub|TestWrittenArtifactDetectors|TestWindowsWriterSkipsList' -v > /tmp/s4c-t1.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t1.log`
Expected: PASS.

Then, in podman as uid 1000:

```bash
podman run --rm --userns=keep-id -u 1000 -v "$PWD":/w -w /w localhost/r73img sh -c 'pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && timeout 280 env FAULTLINE_WRITERS_ONLY="^(propose-input|investigate-two-states|prevention-write-bundle-ok)$" pnpm exec tsx difftest/gen/gen-writers.ts' > /tmp/s4c-t1-gen.log 2>&1; tail -3 /tmp/s4c-t1-gen.log; git status --porcelain difftest/testdata
```

Expected: `writers: … cases`, and no changed file under `difftest/testdata`: the shim and the new env rules change nothing for existing cases.

- [ ] **Step 5: Commit**

```bash
git add difftest .gitattributes
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(difftest): fake codex, gh and OpenAI for the slice 4c writer replay

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Harness: masks for created Git objects and repaired-run digests

**Files:**
- Modify: `difftest/gen/gen-writers.ts`, `difftest/writer_test.go` (both twins: `createdObjects`, `objectNames`, the loose-object and reflog rules, the repair seeds in `relocation`), `difftest/gen/cases.ts` (if `normalizeWriter` needs the new names), `difftest/masks_test.go` (new).

**Interfaces:**
- Consumes: Task 1's records (the `docker run --mount type=bind,src=<worktree>` records name the repaired worktrees; `loggedCasePaths` already collects them).
- Produces:
  - Go `createdObjects(t testing.TB, ws string, before map[string]map[string]bool) map[string]map[string]string`. The keys are repository directories (relative to the workspace), then object ids, mapped to their type. `before` is the set of object ids each repository held before the case's first invocation.
  - Go `objectNames(t testing.TB, repoDir string, created map[string]string, text func(string) string) map[string]string`, which maps an id to `<BLOB:h12>`, `<TREE:h12>` or `<COMMIT:h12>`.
  - Their TS twins.
  - `relocation(…)` gains the objects as seeds, so that every digest over a file that holds a created id gets a `<PATHDIGEST:…>` name, and two computed seed kinds: `exec:<masked worktree>` and `run:<attempt>`.

**Gotchas:**
- **Why these masks.** The spec asked for a `<COMMIT>` mask. Correction 1 explains why repair also needs content-addressed names for blobs and trees: the README blob holds the absolute managed root, so the blob, the tree and the commit all vary with the case directory, and the commit also with the clock.
- **Listing the created objects.** For each `.git` directory in the workspace, run `git cat-file --batch-all-objects --batch-check='%(objectname) %(objecttype)'` before the first invocation and after each one. Use the harness's own git environment, not fl's, and keep the calls out of the argv log, as `gitIndexDigest` does. The new ids are the difference.
- **Naming the objects** on each side, from `git cat-file -p` on that side. In dependency order (blobs, then trees, then commits), each object is named from text in which the case paths and every already-named id are masked:
  - **blob:** `<BLOB:` + the first 12 hex digits of `sha256("blob\n" + text(content))` + `>`.
  - **tree:** each entry `<mode> <type> <id or its name>\t<name>`, joined by `\n`, then hashed as `"tree\n" + …`.
  - **commit:** the tree's name, each parent's name (or the parent id when it existed before the case), the `author` and `committer` lines with their trailing `<seconds> <zone>` removed, and the message. Hashed as `"commit\n" + …`.

  A commit with a wrong tree, parent, identity or message therefore gets a different name and fails the comparison, as spec 2.2 requires.
- **Ids that stay unmasked.** An id that existed before the case is never masked: the scenario's base commits are fixed by `buildRepo`'s fixed dates.
- **Loose objects.** A loose object under `<repo>/.git/objects/xx/…` that is new to the case is hashed as `sha256("git-object:" + its name)`, like `snapshotObjectDigest`. Loose objects that existed before keep their byte digest, so no existing golden moves.
- **Reflogs.** Any `.git/**/logs/**` file that is new or changed has each line's `<seconds> <zone>\t` replaced with `<TIME>\t` before hashing. With the scenario's `[user]` config the identity is fixed. A reflog that existed before keeps its byte digest.
- **The `exec:` seed** is computed, never read from a file. For each repaired worktree path `w` a recorder logged, and each PASS run fact in a bundle in the workspace, add the digest of this object, keys in this order:
  - `kind`: `"faultline.repair-verify-execution.v1"`
  - `executor`: `"NATIVE_DOCKER"`
  - `verdict`: `"PASS"`
  - `reason`: `"PREDICATE_PASS"`
  - `policyDigest` and `environmentPolicyDigest`: the run's own
  - `exitCode`: `0`
  - `stdoutDigest`: `sha256` of the fake Docker's exact `PREDICATE_PASS` stdout for `state=good`
  - `stderrDigest`: `sha256` of the empty string
  - `worktree`: `w`
  - `frozenDigest`: the run's `frozenDigest`

  Name it `exec:` + the masked `w`. A candidate that appears nowhere is dropped. A wrong id is never named, so it stays visible and fails.
- **The `run:` seed.** For each created commit and tree pair and each `exec:` digest, add `digestJson` of this object, keys in this order:
  - `schemaVersion`: `"faultline.prevention-repaired-run.v1"`
  - `attempt`: 1, 2 or 3
  - `commit` and `tree`: the pair
  - `witnessDigest` and `environmentDigest`: from the bundle
  - `executionId`: the `exec:` digest
  - `detail`: `"Frozen witness PREDICATE_PASS on repaired worktree (NATIVE_DOCKER)."`

  Name it `run:<attempt>`.
- **Relocation.** Feed the named object ids, the `exec:` digests and the `run:` digests into `relocation`'s seeds. Its existing passes then name `repaired-runs.json`, `prevention.json`, the manifest's `rootDigest`, `verification.json` and `prevention-export.json`.
- **Existing goldens.** No existing case creates objects, so every existing golden stays byte-identical. Step 4 checks that.

- [ ] **Step 1: Write the failing test**

```go
// difftest/masks_test.go
//go:build faultline_test && unix

package difftest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// gitIn runs the harness's git in dir (identity from the arguments only).
func gitIn(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=FaultLine Test", "-c", "user.email=faultline@example.test"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitNamesIgnoreTimeButNotTreeParentOrMessage(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "", "init", "-q")
	base := gitIn(t, repo, "", "commit-tree", gitIn(t, repo, "", "write-tree"), "-m", "base")
	before := map[string]map[string]bool{".": objectSet(t, repo)}
	blob := gitIn(t, repo, "/case/ws/x\n", "hash-object", "-w", "--stdin")
	tree := gitIn(t, repo, "100644 blob "+blob+"\tREADME.md\n", "mktree")
	c1 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "faultline: repaired prevention state")
	time.Sleep(1100 * time.Millisecond) // a different committer second: a different id, the same name
	c2 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "faultline: repaired prevention state")
	c3 := gitIn(t, repo, "", "commit-tree", tree, "-p", base, "-m", "other message")
	created := createdObjects(t, repo, before)["."]
	names := objectNames(t, repo, created, func(s string) string { return strings.ReplaceAll(s, "/case/ws", "<WS>") })
	if c1 == c2 || names[c1] == "" || names[c1] != names[c2] || !strings.HasPrefix(names[c1], "<COMMIT:") {
		t.Fatalf("c1 %q c2 %q", names[c1], names[c2])
	}
	if names[c3] == names[c1] {
		t.Fatal("a different message must give a different name")
	}
	if _, ok := names[base]; ok {
		t.Fatal("an object that existed before the case is never masked")
	}
	if !strings.HasPrefix(names[blob], "<BLOB:") || !strings.HasPrefix(names[tree], "<TREE:") {
		t.Fatal(names[blob], names[tree])
	}
}
```

(`objectSet(t, repo) map[string]bool` is the `--batch-all-objects` id set, also used by `runWriterCaseWith`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run TestCommitNames -v`
Expected: FAIL (undefined: `objectSet`, `createdObjects`, `objectNames`).

- [ ] **Step 3: Implement** both twins, and call them from `runCase` and `runWriterCaseWith`. Record the object sets after `prepareWriterCase` and before the first invocation.

- [ ] **Step 4: Run to verify the tests pass, and that no golden moved**

Run: `go test -tags faultline_test ./difftest/ -run 'TestCommitNames|TestGitIndexRule' -v > /tmp/s4c-t2.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t2.log`
Expected: PASS.

Then regenerate every writer golden in podman as uid 1000. Use the command from Task 1 Step 4 without `FAULTLINE_WRITERS_ONLY`, in the background with polled logs (about 40 minutes). Expected: `git status --porcelain difftest/testdata` is empty.

- [ ] **Step 5: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(difftest): content-addressed masks for Git objects a case creates

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: spawnSync `timeout` and `execFileSync`

**Files:**
- Modify: `internal/nodeproc/spawn.go`, `internal/nodeproc/spawn_test.go`, `difftest/gen/node-oracle.ts` (ops `spawnSyncTimeout` and `execFileSyncError`, appended), `difftest/spawn_live_test.go` (`TestLiveSpawnSyncTimeout`, `TestLiveExecFileSync`).

**Interfaces:**
- Produces:
  - `nodeproc.SpawnSyncOptions.Timeout time.Duration`, where 0 means no timeout. When the timer fires, the child gets SIGTERM (`proc.Kill()` on Windows), the pipes close, and the result has `Status == nil`, `Signal == "SIGTERM"` and `Err == "spawnSync <file> ETIMEDOUT"`.
  - `nodeproc.Result.Signal string`, the name of the signal that ended the child, or `""`.
  - `nodeproc.ExecFileSync(file string, args []string, o SpawnSyncOptions) (stdout []byte, err error)`, the port of `execFileSync(file, args, { encoding, stdio: ["ignore", "pipe", "pipe"] })`:
    - On a spawn error (`Err` set) it returns that error, whose message is `spawnSync <file> <CODE>`.
    - On a non-zero status or a signal it returns an error with message `Command failed: <file> <args joined by " ">` + (`"\n" + stderr` when stderr is non-empty). This is Node's `checkExecSyncError`, with stderr decoded as UTF-8.
    - In both cases it returns stdout as captured.
- Consumers: Task 12 (Codex runner, 120 s and 180 s), Task 13 (`commitRepairedWorktreeState`, 120 s, 32 MiB), Task 14 (`execFileSync("git", …)`), Task 14 (`gh`: no timeout).

**Gotchas:**
- **Kill order.** Node's `SyncProcessRunner` kills on timeout exactly as it does on a maxBuffer overrun: `killSignal` (SIGTERM), then the pipes close, then the error is set. `ENOBUFS` wins over `ETIMEDOUT` only if it was set first: the first error recorded is kept (`SetError`). Keep the existing `killed` flag so that `sigexit.ChildKilled` is not called for fl's own kill.
- **Timer start.** The timer starts after the spawn succeeds and before the input write. The input write runs on its own goroutine, so a child that never reads stdin still times out.
- **`execFileSync`'s message** in Node 22.22.2 (`lib/child_process.js` `checkExecSyncError`):
  - It is `Command failed: ${[file, ...args].join(" ")}` (`err.message`), and `+ "\n" + stderr` when `stderr.length > 0`.
  - The stdio here is `["ignore", "pipe", "pipe"]`, so stderr is captured, not inherited. Measured on Node: `"Command failed: /bin/sh -c echo out; echo err >&2; exit 3\nerr\n"`.
  - A spawn error (ENOENT) is the bare `spawnSync <file> ENOENT`.
- **`execFileSync`'s other defaults:**
  - It runs without a shell.
  - `maxBuffer` is 1 MiB, with ENOBUFS after SIGTERM.
  - `windowsHide: true` comes from the caller.
- **Environment.** Leave `Env` nil, so the child inherits fl's environment: `github-provenance.ts:244` passes no `env`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/nodeproc/spawn_test.go
func TestSpawnSyncTimeoutSendsSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	start := time.Now()
	r := SpawnSyncWith("/bin/sh", []string{"-c", "sleep 5"}, SpawnSyncOptions{Timeout: 200 * time.Millisecond})
	if r.Status != nil || r.Signal != "SIGTERM" || r.Err == nil || r.Err.Error() != "spawnSync /bin/sh ETIMEDOUT" {
		t.Fatalf("%+v", r)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the timeout did not stop the child")
	}
}

func TestExecFileSyncMessages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	_, err := ExecFileSync("/bin/sh", []string{"-c", "echo out; echo err >&2; exit 3"}, SpawnSyncOptions{})
	if err == nil || err.Error() != "Command failed: /bin/sh -c echo out; echo err >&2; exit 3\nerr\n" {
		t.Fatalf("%v", err)
	}
	_, err = ExecFileSync("/nonexistent-gitx", []string{"a"}, SpawnSyncOptions{})
	if err == nil || err.Error() != "spawnSync /nonexistent-gitx ENOENT" {
		t.Fatalf("%v", err)
	}
	out, err := ExecFileSync("/bin/sh", []string{"-c", "printf ok"}, SpawnSyncOptions{})
	if err != nil || string(out) != "ok" {
		t.Fatalf("%q %v", out, err)
	}
}
```

Live tests:
- `TestLiveSpawnSyncTimeout`: 40 scripted children (sleep, a CPU loop, ignoring SIGTERM via `trap '' TERM` then exiting after 0.5 s, output past maxBuffer before the timer, input never read), each with timeouts of 100–400 ms. Compare `{status, signal, error.message, stdout.length}` with Node's `spawnSync`.
- `TestLiveExecFileSync`: 60 cases (exit codes 0–3, empty and non-empty stderr, invalid UTF-8 on stderr, ENOENT, EACCES, an argument holding a newline). Compare the message or the stdout.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nodeproc/ -run 'TestSpawnSyncTimeout|TestExecFileSync'`
Expected: FAIL (unknown field `Timeout`, undefined `ExecFileSync`).

- [ ] **Step 3: Implement** by transcription of `SyncProcessRunner` (timer) and `checkExecSyncError`.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/nodeproc/ && GOOS=windows go vet ./internal/nodeproc/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveSpawnSyncTimeout|TestLiveExecFileSync|TestLiveSpawn$' -v > /tmp/s4c-t3.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t3.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodeproc difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): spawnSync timeout and execFileSync for slice 4c

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `cpSync` (the repair worktree's proof companion copy)

**Files:**
- Create: `internal/nodefs/cp.go`, `internal/nodefs/cp_test.go`, `difftest/cpsync_live_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (op `cpSyncTree`, appended).

**Interfaces:**
- Produces: `nodefs.CpSyncTree(src, dest string) error`, the port of `cpSync(src, dest, { recursive: true, dereference: false, errorOnExist: true, force: false })` (`repair-worktree.ts:111`).

**Gotchas:**
- **Port the JS path.** Node 22.22.2 runs `cpSync` in JS (`lib/internal/fs/cp/cp-sync.js`) with `force: false`. Port that path: `checkPathsSync` (with `ERR_FS_CP_EINVAL` "cannot copy … to a subdirectory of self", and the same-path check), then `getStatsForCopy` (lstat, because `dereference` is false), then `onDir`, `onFile` and `onLink`.
  - Directories: `mkdirSync(dest, srcStat.mode)`, then a copy of each entry from `readdirSync` order (byte order), then `chmodSync(dest, srcMode)`. The chmod comes after the children (`mkDirAndCopy`).
  - Files: `copyFileSync(src, dest, COPYFILE_EXCL)`. When `preserveTimestamps` is false (the default), `fileIsNotWritable` decides on `chmod u+w`, then `setDestMode`.
  - Symlinks: `readlinkSync` then `symlinkSync`. A relative link target is resolved against the source directory (`resolve(dirname(src), …)`), as `onLink` does when `verbatimSymlinks` is false.
  - A FIFO, socket or device raises `ERR_FS_CP_FIFO_PIPE`, `ERR_FS_CP_SOCKET` or `ERR_FS_CP_UNKNOWN`, with Node's exact message.
  - An existing destination file with `errorOnExist && !force` raises `ERR_FS_CP_EEXIST`: `Target already exists: cp returned EEXIST (<dest> already exists)`.
- **Error codes reach the stdout note.** `createRepairWorktree` catches a copy failure, cleans up, and rethrows `Could not finish repair worktree setup: <message>`. Each message must therefore be Node's text exactly. Copy the `SystemError` format (`<code>: <description>, <syscall> '<path>' -> '<dest>'`) from `nodefs.Error`.
- **What a bundle holds.** A proof bundle holds only regular files and directories, since the verifier rejects anything else first. The writer cases therefore reach only the file and directory branches. The live test covers symlinks, a FIFO, an unreadable file and an existing target.

- [ ] **Step 1: Write the failing test**

```go
// internal/nodefs/cp_test.go
func TestCpSyncTreeCopiesModesAndRefusesExisting(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	os.MkdirAll(filepath.Join(src, "runs"), 0o750)
	os.WriteFile(filepath.Join(src, "runs", "a.json"), []byte("{}\n"), 0o640)
	if err := CpSyncTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dst, "runs", "a.json")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(dst, "runs")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o750 {
		t.Fatalf("dir mode %v", info.Mode())
	}
	err := CpSyncTree(src, dst)
	if err == nil || !strings.HasPrefix(err.Error(), "Target already exists: cp returned EEXIST (") {
		t.Fatalf("%v", err)
	}
}
```

Live test `TestLiveCpSyncTree`: 200 generated trees (nesting depth 0–4; modes 0400–0777; empty files; names with spaces, `é` and newlines; symlinks with relative and absolute targets; a FIFO; a file mode 0000; a destination that exists as a file, as a directory, or as a subdirectory of the source). Run under umask 022 and compare the resulting tree (`describeTree`) and the error message with Node.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nodefs/ -run TestCpSyncTree`
Expected: FAIL (undefined: `CpSyncTree`).

- [ ] **Step 3: Implement** by transcription of `cp-sync.js` for exactly these options. Write no general `cp`.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/nodefs/ && GOOS=windows go vet ./internal/nodefs/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveCpSyncTree -v > /tmp/s4c-t4.log 2>&1; tail -5 /tmp/s4c-t4.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodefs difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): nodefs.CpSyncTree, the cpSync the repair worktree uses

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Slice 3b carry-overs: `ArtifactOptions`, the hard-guard helpers, `DraftInput`

**Files:**
- Create: `internal/bundle/prevention/hardguard.go`, `internal/bundle/prevention/hardguard_test.go`.
- Modify:
  - `internal/bundle/prevention/artifacts.go` (the two options, the restored `repairBaseTree` check), `internal/bundle/prevention/artifacts_test.go`;
  - `internal/incident/draft.go` (the `DraftInput` cleanup), `internal/incident/draft_test.go`;
  - `internal/cli/prevention.go` (the zero values for the new fields: no behaviour change);
  - `difftest/gen/node-oracle.ts` (`buildPreventionProofFromVerifiedArtifacts` gains `repairBaseTree` and `hardGuardArtifactDigests`; new ops `hardGuardDigestsFromStrings` and `writeHardGuardDigestSidecar`, appended);
  - `difftest/prevention_live_test.go` (extend `TestLivePreventionFromArtifacts`; new `TestLiveHardGuardDigests`).

**Interfaces:**
- Produces:
  - `prevention.ArtifactOptions` gains `RepairBaseTree string; HasRepairBaseTree bool; HardGuardArtifactDigests []string; HasHardGuardArtifactDigests bool`. `Has…` false is TS `undefined`.
  - `prevention.HardGuardDigestsFromStrings(values []string) []string`
  - `prevention.WriteHardGuardDigestSidecar(directory string, digests []string) (string, error)`
- Changes `incident.DraftInput`: the fields `Symptom`, `Language`, `Behavior` and `TimeoutSeconds` are deleted (`CreateDraft` ignores them; `incident start` passes them to the witness proposal directly). The doc comment is updated to say so, and to say which fields createIncidentDraft reads.

**Gotchas:**
- **The base-tree check.** With `HasRepairBaseTree`, restore TS's check (`prevention-from-artifacts.ts:258-261`), at the same place in the failure order: after the distinct-commit check. Its text is `repairBaseTree must equal the first-bad state's tree (repair base).` The input's `repairBaseTree` is the option's value, `options.repairBaseTree ?? introduction.after.tree`. Update the function comment, which says the check is not ported.
- **The hard-guard digests in the input.** With `HasHardGuardArtifactDigests`, the input gains `hardGuardArtifactDigests: [...digests]` after `codexThreadId` and before `repairedRunBindings`, in TS's spread order (`:226-228`). An empty slice with `Has…` is `[]`, not absent.
- **`HardGuardDigestsFromStrings`:**
  - Each value becomes `"sha256:" + canonical.SHA256Hex(value)`; a lone surrogate hashes as U+FFFD.
  - Duplicates are removed keeping the first, as `new Set` does.
  - The result is then sorted with `canonical.SortLocale`, since TS uses `localeCompare`. Every element is `sha256:` plus lowercase hex, so the order is plain ASCII, but use the locale sort anyway, as TS does.
- **`WriteHardGuardDigestSidecar`:**
  - It writes `JSON.stringify({digests}, null, 2) + "\n"` to `<dir>/hard-guard-digests.json` with flag `w` and the default mode (0666 under the umask): `nodefs.WriteFileUTF8(path, text, 0o666, false)`.
  - It then re-reads the file and returns `"sha256:" + hex(sha256(bytes))`.
  - Errors are Node's raw text.
- **No CLI path.** Neither hard-guard helper has a caller in `src/` (correction 5). Add `internal/bundle/prevention/hardguard.go` to `liveGatedFiles` as `TestLiveHardGuardDigests`.
- **Go callers of `DraftInput`.** Compile every one after the cleanup: `internal/cli/incident.go:260` and the tests listed by `grep -rn DraftInput internal difftest`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/bundle/prevention/hardguard_test.go
package prevention

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
)

func TestHardGuardDigestsDedupeAndSort(t *testing.T) {
	got := HardGuardDigestsFromStrings([]string{"b", "a", "b"})
	want := []string{"sha256:" + canonical.SHA256Hex("a"), "sha256:" + canonical.SHA256Hex("b")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestHardGuardSidecarDigestIsTheFileBytes(t *testing.T) {
	dir := t.TempDir()
	digest, err := WriteHardGuardDigestSidecar(dir, []string{"sha256:" + canonical.SHA256Hex("a")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hard-guard-digests.json"))
	if digest != "sha256:"+canonical.SHA256HexBytes(b) || string(b[:15]) != "{\n  \"digests\":" {
		t.Fatalf("%s %q", digest, b)
	}
}
```

```go
// internal/bundle/prevention/artifacts_test.go (added)
func TestRepairBaseTreeMustBeFirstBadTree(t *testing.T) {
	o := gitUnboundArtifactOptions(t) // existing fixture builder for the git-unbound base
	o.RepairBaseTree, o.HasRepairBaseTree = strings.Repeat("0", 40), true
	_, reasons := BuildFromArtifacts(o)
	if len(reasons) != 1 || reasons[0] != "repairBaseTree must equal the first-bad state's tree (repair base)." {
		t.Fatalf("%v", reasons)
	}
	o.HardGuardArtifactDigests, o.HasHardGuardArtifactDigests = []string{}, true
	o.RepairBaseTree = firstBadTree(t, o.Bundle)
	input, reasons := BuildFromArtifacts(o)
	if reasons != nil || input.Get("hardGuardArtifactDigests").Kind() != jsjson.Array {
		t.Fatalf("%v", reasons)
	}
	keys := input.Obj().Keys()
	if keys[len(keys)-2] != "hardGuardArtifactDigests" || keys[len(keys)-1] != "repairedRunBindings" {
		t.Fatalf("order %v", keys)
	}
}
```

Add the helpers `gitUnboundArtifactOptions(t) ArtifactOptions` and `firstBadTree(t, bundle) string` to `artifacts_test.go`. Build them from `difftest/testdata/bases/git-unbound` the way `difftest/prevention_live_test.go` `gitUnboundOptions` does: the third state as the repaired state, its three runs, the frozen witness digest, the homogeneous environment digest, and the `transitions[0].after.tree` of `investigation.json`.

Live tests:
- `TestLiveHardGuardDigests`: 500 generated string lists (empty strings, duplicates, astral and lone-surrogate characters) through both helpers, the sidecar into fresh directories (plus one that is a read-only directory, and one whose sidecar path is a directory). Compare the digests, the file bytes and modes, or the `THREW:` text.
- `TestLivePreventionFromArtifacts`: gains 60 variations with `repairBaseTree` (equal, different, empty) and `hardGuardArtifactDigests` (absent, empty, three digests, one invalid digest, which the writer's schema refuses later).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/prevention/ ./internal/incident/`
Expected: FAIL (undefined `HardGuardDigestsFromStrings`; unknown field `RepairBaseTree`).

- [ ] **Step 3: Implement** by transcription. Remove the four `DraftInput` fields and fix the callers.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go build ./... && go test ./internal/bundle/prevention/ ./internal/incident/ ./internal/cli/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveHardGuardDigests|TestLivePreventionFromArtifacts|TestLiveIncidentDraft' -v > /tmp/s4c-t5.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t5.log`
Expected: PASS. The prevention and incident writer goldens are untouched: run `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?(prevention|incident)-'` and expect PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/prevention internal/incident internal/cli difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): ArtifactOptions repairBaseTree and hard-guard digests; DraftInput cleanup

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Repair evidence packet, brief validation, response extraction

**Files:**
- Create: `internal/repair/brief.go`, `internal/repair/response.go`, `internal/repair/brief_test.go`, `internal/repair/response_test.go`, `difftest/repair_live_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (ops `createRepairEvidencePacket`, `validateRepairBrief`, `extractOpenAiResponseText` and `repairBriefJsonSchema`, appended; the last one stringifies the request body's `text` member from a live call recorded by the fake server).

**Interfaces:**
- Consumes: `gitproof.GitInvestigationResultSchema`, `canonical.DigestJSON`, `schema.*`, `bundle.ParseValue`.
- Produces:
  - Schemas: `repair.EvidencePacketSchema`, `repair.BriefSchema`, `repair.EvidencePacketVersion = "faultline.repair-evidence.v2"` and `repair.BriefVersion = "faultline.repair-brief.v1"`.
  - `repair.CreateEvidencePacket(investigation jsjson.Value) (jsjson.Value, error)`. The input is parsed with `GitInvestigationResultSchema` inside.
  - `repair.Validation{Valid bool; Errors []string; Brief jsjson.Value /* undefined = null */}`, with `func (v Validation) JSON() jsjson.Value`.
  - `repair.ValidateBrief(packet, brief jsjson.Value) Validation`
  - `repair.PacketDigestValid(packet jsjson.Value) bool`, which is `packetDigest === digestJson(withoutDigest(packet))`.
  - `repair.BriefJSONSchema() jsjson.Value`, the literal `repairBriefJsonSchema` (`repair-brief.ts:162-188`), in key order.
  - `repair.ExtractResponseText(response jsjson.Value) (string, error)`

**Gotchas:**
- **Evidence packet refusal** (`repair-brief.ts:82-85`). One test, one text. Every condition gives `A post-localization repair brief requires a completed Docker-isolated investigation with at least one stable transition.`:
  - `executionTrust !== "NATIVE_DOCKER"`;
  - `!dockerIsolated`;
  - `!isProof`;
  - `status !== "COMPLETED"`;
  - no transitions;
  - a falsy `witness?.witnessDigest`. An empty string is falsy too.
- **Facts.**
  - The ids are `E1…En`, in this order: the executed fact, then one per transition in array order, then the witness fact.
  - The statements interpolate numbers with JS `String(n)`: use `jsjson.FormatNumber`.
  - `transition.before.executionIds.length` is the count for both sides. Use exactly that member, not `after`'s.
- **Packet digest.**
  - `packetDigest` is `digestJson(unsigned)`, where `unsigned` has the keys `schemaVersion, investigationDigest, frozenWitnessDigest, recorder, nativeCodexInterception, facts, limitations`.
  - The final `RepairEvidencePacketSchema.parse` can throw a ZodError when a transition statement exceeds 2,000 characters or there are more than 256 facts. Keep the ZodError message (`internal/schema` gives zod 3's text).
- **`validateRepairBrief` order** (`repair-brief.ts:137-160`):
  1. The packet `safeParse`. On failure: `repair evidence packet is invalid: ${error.message}`, returned at once.
  2. The packet digest: `repair evidence packet digest does not match its canonical contents`, returned at once.
  3. The brief `safeParse`: `repair brief schema validation failed: ${message}`, returned at once.
  4. A different packet digest pushes `repair brief references a different evidence packet digest` and continues.
  5. Every cited id not among the packet's ids pushes `repair brief cites an unknown evidence ID: <id>`. Iterate in this order: `proposedInvariant`, `repairDirections…`, `hardEnforcement…`, `softGuidance…`, and within each the ids in order, duplicates included.
  6. `brief` is the parsed data (zod-stripped and ordered) only when there are no errors.
- **`ZodError.message`** is the JSON of the issues (zod 3: `JSON.stringify(issues, null, 2)`). `internal/schema` already produces it, so use the existing `ZodError` text helper the incident store uses.
- **`extractOpenAiResponseText`:**
  - Use `own()` (hasOwnProperty), not prototype lookups. In JSON every key is own, so `__proto__` is an ordinary key.
  - `nonEmptyText` is `typeof === "string" && length > 0`. A lone surrogate counts as non-empty.
  - Copy all eight error texts verbatim from `openai-response.ts:28-70`.
- **`repairBriefJsonSchema`** is sent to OpenAI byte for byte inside `JSON.stringify`. Build it with `jsjson.MustObject` in exactly TS's key order, and pin it against the oracle with `jsjson.Stringify`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/repair/response_test.go
package repair

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustParse(t *testing.T, s string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtractResponseText(t *testing.T) {
	cases := []struct{ in, text, err string }{
		{`{"output":[{"content":[{"type":"output_text","text":"{}"}]}]}`, "{}", ""},
		{`{"output_text":"x"}`, "x", ""},
		{`{"output_text":"x","output":[{"content":[{"type":"output_text","text":"y"}]}]}`, "", "OpenAI response output_text fast path disagrees with raw output content"},
		{`{"output":[{"content":[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]}]}`, "", "OpenAI response contains multiple output_text values; refusing an ambiguous structured result"},
		{`{"output":{}}`, "", "OpenAI response output must be an array when present"},
		{`{"output_text":""}`, "", "OpenAI response output_text fast path is missing non-empty text"},
		{`[]`, "", "OpenAI response must be an object"},
		{`{}`, "", "OpenAI response did not contain exactly one output_text value"},
		{`{"output":[{"content":[{"type":"output_text","text":""}]}]}`, "", "OpenAI response output_text content is missing non-empty text"},
		{`{"output":[1]}`, "", "OpenAI response output contains a non-object item"},
	}
	for _, c := range cases {
		got, err := ExtractResponseText(mustParse(t, c.in))
		if (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) || got != c.text {
			t.Errorf("%s: %q %v", c.in, got, err)
		}
	}
}
```

```go
// internal/repair/brief_test.go
package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func unboundInvestigation(t *testing.T) jsjson.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "difftest", "testdata", "bases", "git-unbound", "investigation.json"))
	if err != nil {
		t.Fatal(err)
	}
	return mustParse(t, string(b))
}

func TestEvidencePacketFactsAndDigest(t *testing.T) {
	packet, err := CreateEvidencePacket(unboundInvestigation(t))
	if err != nil {
		t.Fatal(err)
	}
	facts := packet.Get("facts").Items()
	if len(facts) != 4 || facts[0].Get("id").Str() != "E1" || facts[3].Get("id").Str() != "E4" ||
		!strings.HasPrefix(facts[1].Get("statement").Str(), "PASS_TO_FAIL transition: ") {
		t.Fatalf("%s", jsjson.Stringify(packet))
	}
	if !PacketDigestValid(packet) {
		t.Fatal("digest")
	}
}

func TestValidateBriefCitesUnknownIDs(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	brief := mustParse(t, `{"schemaVersion":"faultline.repair-brief.v1","evidencePacketDigest":"`+packet.Get("packetDigest").Str()+`",
		"proposedInvariant":{"statement":"s","evidenceIds":["E9","E1","E9"]},"repairDirections":[{"statement":"d","evidenceIds":["E2"]}],
		"prevention":{"hardEnforcement":[{"statement":"h","evidenceIds":["E3"]}],"softGuidance":[]},"uncertainties":["u"]}`)
	v := ValidateBrief(packet, brief)
	if v.Valid || len(v.Errors) != 2 || v.Errors[0] != "repair brief cites an unknown evidence ID: E9" || v.Brief.Kind() != jsjson.Undefined {
		t.Fatalf("%+v", v)
	}
}
```

Live tests in `difftest/repair_live_test.go`:
- `TestLiveRepairEvidencePacket`: every committed Git base, plus 400 mutations of `git-unbound`'s investigation (each refusal condition, long statements via 300 transitions, a `null` witness, numbers like `3e0`). Compare canonical JSON or `THREW:`.
- `TestLiveValidateRepairBrief`: 1,500 generated packet and brief pairs (each schema boundary, unknown and duplicate ids, a wrong digest, extra keys, `E0`, `E01`).
- `TestLiveExtractOpenAiResponseText`: 1,000 generated responses (spec 2.5).
- `TestLiveRepairBriefJSONSchema`: one comparison of `jsjson.Stringify(BriefJSONSchema())`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/repair/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/repair/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveRepairEvidencePacket|TestLiveValidateRepairBrief|TestLiveExtractOpenAiResponseText|TestLiveRepairBriefJSONSchema' -v > /tmp/s4c-t6.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t6.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repair difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): repair evidence packet, brief validation and response extraction

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Attestation library (`internal/attest`)

**Files:**
- Create: `internal/attest/attest.go`, `internal/attest/attest_test.go`, `internal/jsdate/jsdate.go`, `internal/jsdate/jsdate_test.go`, `difftest/attest_live_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (ops `createBundleAttestation`, `verifyBundleAttestation`, `writeBundleAttestation`, `verifyStoredBundleAttestation` and `isCanonicalIsoDate`, appended).

**Interfaces:**
- Consumes: `safedir.{ResolveSafeDirectorySegment, RelativeTrustedSystemPath}`, `nodefs.{Resolve, Join, Relative, IsAbsolute, ParseRoot, Exists, Mkdir, Lstat, Link, ReadText}`, `canonical.*`, `clock.{Now, NewUUID}`, `schema.*`.
- Produces:
  - `attest.DefaultStore() string`, which is `resolve(".faultline", "attestations")`.
  - `attest.CreationInput{ReceiptID, Subject, Issuer, BundleRootDigest string; SourceDigest, GitCheckpointDigest, WitnessDigest *string}`. A nil pointer is undefined. `issuedAt` is always undefined from the CLI, so it reads the clock.
  - `attest.Written{Directory, Path string; Attestation jsjson.Value; ReceiptDigest string}`
  - `attest.Write(store string, in CreationInput, root string) (Written, error)`, where `root` is the attestation root (`DefaultStore()` from the CLI).
  - `attest.Verification{Valid bool; Errors []string; ReceiptDigest *string; ExternalDigestStatus string; Attestation jsjson.Value /* undefined = absent */}`, with `JSON()` giving TS key order (`valid, errors, receiptDigest, externalDigestStatus, attestation?`).
  - `attest.VerifyStored(store, receiptID string, expected *string, root string) Verification`
  - `attest.Verify(record jsjson.Value, expected *string) Verification`
  - `jsdate.IsCanonicalISO(s string) bool`, which is `Number.isFinite(new Date(s).getTime()) && new Date(s).toISOString() === s`, for strings that already match `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`.

**Gotchas:**
- **Schemas** (`attestation.ts:28-73`):
  - `ReceiptIdSchema` regex message: `must use only letters, numbers, underscores, or hyphens and cannot be a path`.
  - `IdentifierSchema`: `min(1)`, `max(512)` (UTF-16 units), refine `value === value.trim()` (message `cannot start or end with whitespace`), refine no `[\u0000-\u001f\u007f]` (message `cannot contain control characters`). Both refines run even after an earlier check failed: zod 3 runs refinements only when the string checks are not aborted, and `min` and `max` do not abort. Pin this in the live test.
  - `TimestampSchema`: the regex, then the refine.
  - The digest message is `must be a sha256:<64-lowercase-hex> digest`.
- **`jsdate`.** Measured on Node 22.22.2:
  - `2026-02-30T…Z` parses to `2026-03-02` (not canonical);
  - `…T24:00:00.000Z` parses to the next day (not canonical);
  - `…T23:59:60…` and `…T25:…` give NaN.

  So canonical means: valid month 01–12, day within the month (leap years by the proleptic Gregorian rule, year 0000 included), hour 00–23, minute and second 00–59. Write this as a direct check, not as a general `Date.parse` port, with a comment citing the measurements, and a live test over 20,000 generated timestamps against `new Date`.
- **`issuedAt`** is `new Date().toISOString()`: one `clock.Now()` read in `createBundleAttestation`, formatted with `clock.ISO`.
- **Write order** (`attestation.ts:296-307`):
  1. `assertSafeAttestationStore(store, root)`, which creates directories.
  2. `createBundleAttestation`: zod parse, then the clock read, then the unsigned parse, then the digest.
  3. `receiptPath`: `ReceiptIdSchema.parse` again, then the escape check, whose text is `Attestation receipt path escaped its managed store.` and cannot be reached.
  4. `safeStoreDirectory(store, root, false)` again.
  5. `writeNewReceipt`:
     - `clock.NewUUID()` names `.attestation-<uuid>.tmp` in the store.
     - Open with `wx` and mode 0600, write, fsync, close.
     - Lstat the temporary file: `Attestation staging file is not a regular file.`
     - `link(tmp, dest)`. EEXIST gives `Attestation receipt already exists and cannot be overwritten: <dest>`. Any other error is Node's raw text.
     - In the `finally`: close if still open, then `existsSync(tmp) && unlinkSync(tmp)`.
- **Directory creation** in `ensureRealDirectory`:
  - It uses `mkdirSync(current)` with the default mode 0777 (correction 11): `nodefs.Mkdir(p, 0o777)`.
  - Its texts:
    - `Attestation path root must be a real directory: <root>`;
    - `Attestation store does not exist: <absolute>` (only when `create` is false);
    - `Attestation store cannot traverse a symbolic link or non-directory: <current>`.
  - `safeStoreDirectory` first checks `!storeDirectory || !storeDirectory.trim()`, giving `Attestation store directory must be non-empty.`, then `Attestation store must be inside the managed root: <root>`.
  - `isDescendantOrSame` allows `""`, so the store may be the root itself.
- **`verifyStoredBundleAttestation`:**
  - `failedVerification` re-runs `externalDigestStatus(expected, errors)`, which may push `externally supplied receipt digest is not a sha256 digest` after the first error.
  - The `JSON.parse` error inside the try becomes `attestation verification failed safely: <SyntaxError message>`. Use `jsjson`'s V8-compatible parse error text.
  - Reading uses `readFileSync(path, "utf8")`, with U+FFFD for bad bytes.
- **`verifyBundleAttestation`:**
  - The external status is computed first, so its error comes before the schema error.
  - `receiptDigest` in the result is the calculated digest.
  - `MISMATCH` pushes `externally supplied receipt digest does not match` only when `expected !== undefined`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/jsdate/jsdate_test.go
package jsdate

import "testing"

func TestIsCanonicalISO(t *testing.T) {
	for s, want := range map[string]bool{
		"2026-07-16T12:00:00.000Z": true, "2024-02-29T00:00:00.000Z": true, "0000-02-29T00:00:00.000Z": true,
		"2026-02-29T00:00:00.000Z": false, "2026-02-30T00:00:00.000Z": false, "2026-01-01T24:00:00.000Z": false,
		"2026-01-31T23:59:60.000Z": false, "2026-13-01T00:00:00.000Z": false, "2026-04-31T00:00:00.000Z": false,
		"2100-02-29T00:00:00.000Z": false, "2000-02-29T00:00:00.000Z": true,
	} {
		if IsCanonicalISO(s) != want {
			t.Errorf("%s", s)
		}
	}
}
```

```go
// internal/attest/attest_test.go
package attest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOnceAndVerify(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".faultline", "attestations")
	in := CreationInput{ReceiptID: "r1", Subject: "s", Issuer: "i", BundleRootDigest: "sha256:" + strings.Repeat("a", 64)}
	w, err := Write(root, in, root)
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyStored(root, "r1", &w.ReceiptDigest, root)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	if _, err := Write(root, in, root); err == nil || err.Error() != "Attestation receipt already exists and cannot be overwritten: "+w.Path {
		t.Fatalf("%v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("a staging file was left: %v", entries)
	}
}

func TestStoreOutsideRootRefused(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if _, err := Write(t.TempDir(), CreationInput{ReceiptID: "r1", Subject: "s", Issuer: "i", BundleRootDigest: "sha256:" + strings.Repeat("a", 64)}, root); err == nil ||
		!strings.HasPrefix(err.Error(), "Attestation store must be inside the managed root: ") {
		t.Fatalf("%v", err)
	}
}
```

Live tests in `difftest/attest_live_test.go`:
- `TestLiveAttestationCreate`: 2,000 generated inputs at every schema boundary. Compare canonical JSON or `THREW:` under the seeded clock.
- `TestLiveAttestationStore`: 300 store layouts, written and then verified:
  - symlinked segments, a store equal to the root, a store outside the root;
  - an existing receipt, or a receipt that is a symlink, a directory or a FIFO;
  - invalid JSON, a tampered digest;
  - an expected digest that is valid, invalid, or `""`.

  Compare the workspace tree, stdout-equivalent JSON and errors.
- `TestLiveCanonicalISO`: 20,000 timestamps.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/attest/ ./internal/jsdate/`
Expected: FAIL (packages do not exist).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/attest/ ./internal/jsdate/ && GOOS=windows go vet ./internal/attest/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveAttestation|TestLiveCanonicalISO' -v > /tmp/s4c-t7.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t7.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/attest internal/jsdate difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): bundle attestation create, write-once link and verify

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: `implementWitnessWithCodex` (`internal/codexloop/implement.go`)

**Files:**
- Create: `internal/codexloop/implement.go`, `internal/codexloop/implement_test.go`.

**Interfaces:**
- Consumes: `canonical.DigestJSON`, `schema.*`, `nodefs.{Resolve, Join, MkdirAll, WriteFileUTF8}`.
- Produces:
  - `codexloop.SchemaVersion = "faultline.codex-loop.v1"`
  - `codexloop.RunResult{ExitCode *int; Stdout, Stderr string; ThreadID *string}`. ThreadID is always nil from the CLI, which returns no `threadId`.
  - `codexloop.Runner func(args []string, cwd, input string) (RunResult, error)`. A nil Runner is "no runner". An error is a throw.
  - `codexloop.ImplementOptions{ProposalID string; Incident jsjson.Value; Behavior, OverlayOut, Repository string; Runner Runner}`
  - `codexloop.Implement(o ImplementOptions) (jsjson.Value, error)`. It returns the result object in TS key order: `schemaVersion, status, transport, proposalId, overlayPath, commandSuggestion, note, limitation`.

**Gotchas:**
- **Order** (`codex-loop.ts:114-184`):
  1. `IncidentPacketSchema.parse` (strict: `symptom`, `ciLog`, `repositoryLanguage` and `repositorySummary`, each `min(1)`). A ZodError throws to the CLI, before any write.
  2. `resolve(overlayOut)`.
  3. `writeOverlayTemplate`:
     - `mkdirSync(overlayOut, {recursive: true})` with the default mode.
     - Write `witness.mjs`, then `README.faultline.txt`, both with `writeFileSync(…, "utf8")`: flag `w` (overwrite), mode 0666 under the umask.
  4. Then the runner.
- **The template.** `safeBehavior` is `behavior.replaceAll("\n", " ").slice(0, 200)`, where 200 counts UTF-16 units and may split a surrogate pair. Encode the result with `jsstr.ToUTF8`, which writes a lone surrogate as U+FFFD, as Node does when writing a string.
- **The prompt** is five lines joined with `\n` (`:140-146`). Copy them byte for byte, including the `\"` escapes in line 2.
- **The runner call** is `runner(["exec", "--full-auto", prompt], overlayOut, prompt)`.
- **Results:**
  - `exitCode !== 0` (null included) gives `CODEX_UNAVAILABLE` with `transport: "CODEX_CLI"` and the note `Codex CLI exited ${exitCode}. Template retained for human edit.` A null interpolates as `null`.
  - A throw gives `CODEX_UNAVAILABLE` with `transport: "NONE"` and the note `Codex CLI could not be started: <message>`.
  - No runner gives `TEMPLATE_ONLY` with the note `Template written for blinded symptom digest ${digestJson(incident)}. Supply --with-codex runner to attempt Codex CLI drafting.`, where `incident` is the zod-parsed packet.
- **Clock.** No clock or UUID read.

- [ ] **Step 1: Write the failing test**

```go
// internal/codexloop/implement_test.go
package codexloop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func incident(t *testing.T) jsjson.Value {
	v, err := jsjson.Parse(`{"symptom":"s","ciLog":"l","repositoryLanguage":"Go","repositorySummary":"r"}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestImplementTemplateOnlyAndThrowingRunner(t *testing.T) {
	out := filepath.Join(t.TempDir(), "overlay")
	r, err := Implement(ImplementOptions{ProposalID: "demo-1", Incident: incident(t), Behavior: "a\nb", OverlayOut: out})
	if err != nil || r.Get("status").Str() != "TEMPLATE_ONLY" || !strings.HasPrefix(r.Get("note").Str(), "Template written for blinded symptom digest sha256:") {
		t.Fatalf("%v %s", err, jsjson.Stringify(r))
	}
	b, _ := os.ReadFile(filepath.Join(out, "witness.mjs"))
	if !strings.Contains(string(b), "// FaultLine witness overlay — behavior: a b\n") {
		t.Fatalf("%q", b)
	}
	throwing := func([]string, string, string) (RunResult, error) {
		return RunResult{}, errors.New("Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.")
	}
	r, _ = Implement(ImplementOptions{ProposalID: "demo-1", Incident: incident(t), Behavior: "b", OverlayOut: out, Runner: throwing})
	if r.Get("status").Str() != "CODEX_UNAVAILABLE" || r.Get("transport").Str() != "NONE" ||
		r.Get("note").Str() != "Codex CLI could not be started: Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex." {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}

func TestImplementNullExit(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o")
	run := func([]string, string, string) (RunResult, error) { return RunResult{}, nil }
	r, _ := Implement(ImplementOptions{ProposalID: "p", Incident: incident(t), Behavior: "b", OverlayOut: out, Runner: run})
	if r.Get("note").Str() != "Codex CLI exited null. Template retained for human edit." || r.Get("transport").Str() != "CODEX_CLI" {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/codexloop/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/codexloop/`
Expected: PASS. The behaviour is compared byte for byte by Task 19's writer cases.

- [ ] **Step 5: Commit**

```bash
git add internal/codexloop
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): implementWitnessWithCodex overlay template and Codex drafting

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Repair brief store (`internal/repair/store.go`)

**Files:**
- Create: `internal/repair/store.go`, `internal/repair/store_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (ops `writeRepairBriefArtifact` and `verifyRepairBriefArtifact`, appended), `difftest/repair_live_test.go` (`TestLiveRepairBriefStore`).

**Interfaces:**
- Consumes: Task 6 (`EvidencePacketSchema`, `BriefSchema`, `ValidateBrief`); `safedir.*`; `nodefs.{Resolve, Join, Dirname, ParseRoot, Relative, Exists, Mkdir, Lstat, ReadDirNames, ReadText, WriteFileSyncUTF8, SyncDir, Rename, RemoveAll}`; `clock.NewUUID`; `canonical.*`.
- Produces:
  - `repair.DefaultBriefRoot() string`, which is `resolve(".faultline", "repair-briefs")`.
  - `repair.Source{Kind string; Model string; HasModel bool}`
  - `repair.WrittenBrief{Directory string; Manifest jsjson.Value}`
  - `repair.WriteBriefArtifact(output string, packet, brief jsjson.Value, source Source) (WrittenBrief, error)`
  - `repair.BriefVerification{Valid bool; Errors []string; Manifest jsjson.Value /* undefined = null */; ExternalDigestStatus string}`, with `JSON()` in the key order `valid, errors, manifest, externalDigestStatus`.
  - `repair.VerifyBriefArtifact(directory string, expected *string) BriefVerification`

**Gotchas:**
- **Write order** (`repair-brief-store.ts:265-327`):
  1. `EvidencePacketSchema.parse(packet)`, which throws a ZodError.
  2. `ValidateBrief`. Failure: `Refusing to store an invalid inferred repair brief: <errors joined "; ">`.
  3. The source schema (`kind` enum, optional `model` 1–256).
  4. `prepareRepairBriefOutput(output)`, always against `DefaultBriefRoot()`, the cwd's root:
     - nesting, with the text `Repair brief output must be a child directory of <root>`;
     - `ensureRealDirectoryTree(root)`, then `ensureRealDirectoryTree(dirname(output))`, with 0700 directories;
     - an existing output: `Repair brief output already exists and will not be replaced: <output>`.
  5. The stage `.<last segment of output split on [\\/]>.<uuid>.tmp` in `dirname(output)`: the one `clock.NewUUID()` read.
  6. The manifest is built with `digestJson(brief)`, where `brief` is the validated, stripped one. The two limitation strings are at `:286-289`.
  7. `try`:
     - `mkdir(stage, 0700)`;
     - four private files (`wx`, 0600, write, fsync, close) in the order `README.md`, `evidence-packet.json`, `repair-brief.json`, `manifest.json`;
     - `syncDirectory(stage)`, which swallows errors;
     - verify the stage. Failure: `Refusing to publish an invalid repair brief package: …`;
     - re-check the output: `Repair brief output appeared during assembly and will not be replaced: <output>`;
     - `rename`, then `syncDirectory(dirname(output))`.
  8. `catch`: if the stage exists, remove it with `rmSync(recursive, force)`, then rethrow.
  - The README is the six lines of `:298-306` joined by `\n`, plus `\n`.
- **Verify order** (`:195-258`):
  - A missing directory returns at once with `Repair brief directory does not exist`.
  - `assertNoLinksOrSpecialFiles` throws inside the try and is caught as `Repair brief package verification failed safely: <msg>`.
  - Missing artifacts are reported in `EXPECTED_ARTIFACTS` insertion order. Unexpected artifacts come in `readdirSync` order (byte order), each followed by its regular-file check.
  - The manifest, then the packet (with the legacy v1 text when `schemaVersion === "faultline.repair-evidence.v1"`), then the brief.
  - The cross checks run only when all three parsed.
  - `readJson` failures are pushed as `Unable to read <label>: <msg>` and parse as `undefined`, which then fails its schema too: both errors appear.
  - `externalDigestStatus` starts as `MISMATCH` when expected is given, and becomes `MATCH` only after the manifest parsed.
- **The two `ensureRealDirectoryTree` messages** are this file's own:
  - `Repair brief root is not a real directory: <root>`;
  - `Repair brief output cannot traverse a symbolic link or non-directory: <current>`.

  Clone the helper from `internal/bundle/prevention/write.go:60-104` with these texts, as 3b did for prevention.

- [ ] **Step 1: Write the failing test**

```go
// internal/repair/store_test.go
package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func validBrief(t *testing.T, packetDigest string) string {
	return `{"schemaVersion":"faultline.repair-brief.v1","evidencePacketDigest":"` + packetDigest + `",
		"proposedInvariant":{"statement":"s","evidenceIds":["E1"]},"repairDirections":[{"statement":"d","evidenceIds":["E2"]}],
		"prevention":{"hardEnforcement":[{"statement":"h","evidenceIds":["E3"]}],"softGuidance":[]},"uncertainties":["u"]}`
}

func TestWriteThenVerifyBriefArtifact(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	chdir(t, t.TempDir())
	out := filepath.Join(DefaultBriefRoot(), "b1")
	w, err := WriteBriefArtifact(out, packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"})
	if err != nil {
		t.Fatal(err)
	}
	d := w.Manifest.Get("manifestDigest").Str()
	v := VerifyBriefArtifact(w.Directory, &d)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	if _, err := WriteBriefArtifact(out, packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"}); err == nil ||
		!strings.HasPrefix(err.Error(), "Repair brief output already exists and will not be replaced: ") {
		t.Fatalf("%v", err)
	}
	if _, err := WriteBriefArtifact(filepath.Join(t.TempDir(), "x"), packet, mustParse(t, validBrief(t, packet.Get("packetDigest").Str())), Source{Kind: "OFFLINE_INPUT"}); err == nil ||
		!strings.HasPrefix(err.Error(), "Repair brief output must be a child directory of ") {
		t.Fatalf("%v", err)
	}
}
```

Live test `TestLiveRepairBriefStore`, in a fresh cwd per case under the seeded clock:
- 400 write cases: valid and invalid briefs, models of 0/1/256/257 units, outputs equal to the root, outside it, nested, or through a symlink, and existing outputs;
- 400 verify cases on mutated packages: missing, extra and symlinked files, a FIFO, a nested directory, a tampered manifest, packet or brief, a legacy v1 packet, invalid JSON, invalid UTF-8, and each expected-digest kind.

Compare the trees and the JSON results.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/repair/ -run TestWriteThenVerifyBriefArtifact`
Expected: FAIL (undefined: `WriteBriefArtifact`).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/repair/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveRepairBriefStore -v > /tmp/s4c-t9.log 2>&1; tail -5 /tmp/s4c-t9.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repair difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): write-once repair brief package store and verifier

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Live repair brief client (`proposeRepairBriefWithGpt`)

**Files:**
- Create: `internal/repair/live.go`, `internal/repair/openai_release.go` (`//go:build !faultline_test`), `internal/repair/openai_test_url.go` (`//go:build faultline_test`), `internal/repair/live_test.go` (`//go:build faultline_test`).
- Modify: `difftest/repair_live_test.go` (`TestLiveProposeRepairBrief`, which drives both sides against Task 1's fake server).

**Interfaces:**
- Consumes: Task 6; `redaction.RedactValue`; `nodeproc.Getenv`.
- Produces:
  - `repair.ProposeBrief(packet jsjson.Value, model string) (jsjson.Value, error)`. It returns the validated brief.
  - `repair.responsesURL() string`. In a release build it returns the constant `https://api.openai.com/v1/responses`. Under `faultline_test` it returns `FAULTLINE_TEST_OPENAI_URL` when that is set, otherwise the constant.

**Gotchas:**
- **Order** (`repair-brief.ts:195-232`):
  1. `EvidencePacketSchema.parse(packet)`, which throws a ZodError.
  2. The re-digest check: `Refusing to send a repair packet with an invalid digest.`
  3. `apiKey = process.env.OPENAI_API_KEY`. An empty or unset value throws `OPENAI_API_KEY is required for a live GPT-5.6 repair brief.`
  4. `redactValue(packet)`, then `EvidencePacketSchema.parse(redacted)`.
  5. The POST.
  6. A response with `!ok` (status outside 200–299) throws `OpenAI Responses request failed: ${status} ${await response.text()}`.
  7. `JSON.parse(extractOpenAiResponseText(await response.json()))`:
     - a non-JSON response body throws V8's `SyntaxError` message for `response.json()`, which is `Unexpected token … in JSON at position …` or the Node 22 variant;
     - an extracted text that is not JSON throws `JSON.parse`'s message.

     Port both through `jsjson.Parse`'s V8 messages, and pin them in the live test.
  8. `validateRepairBrief(packet, output)`, using the unredacted packet. Failure: `GPT-5.6 repair brief was rejected: <errors joined "; ">`.
- **Request bytes.**
  - The headers are exactly `Authorization: Bearer <key>` and `Content-Type: application/json`, plus what the client adds on its own. Node's undici adds `accept: */*`, `accept-language: *`, `sec-fetch-mode: cors`, `user-agent: node`, `accept-encoding: gzip, deflate`. Go sets the same five headers explicitly, so the fake server's records could be compared. The fake records only `Authorization` and `Content-Type`, and KNOWN_DIFFERENCES notes the rest.
  - The body is `JSON.stringify({model, instructions, input: JSON.stringify(safePacket), text: {format: {type: "json_schema", name: "faultline_repair_brief", strict: true, schema}}})`, in that key order. The six instruction strings are joined by `" "` (`:213-220`).
  - The fake records the body, so it is compared byte for byte.
- **Response decoding.**
  - Node decodes the body as UTF-8, strips a leading BOM, and uses U+FFFD for bad bytes.
  - It reads no clock.
  - The fetch has no timeout, as in TS.
  - `Proxy: nil`; follow redirects as fetch does (up to 20, `redirect: "follow"`). The fake never redirects.
- **The release build** must not contain the override. No test checks for test hooks in a release binary today. Step 4's `go build` plus `grep` checks it, and Task 21 adds the same check to the `go` job as a step: `go build -o "$RUNNER_TEMP/fl" ./cmd/fl && ! grep -q FAULTLINE_TEST_OPENAI_URL "$RUNNER_TEMP/fl"`.
- **Clock.** No clock or UUID read here.

- [ ] **Step 1: Write the failing test**

```go
// internal/repair/live_test.go
//go:build faultline_test

package repair

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProposeBriefSendsSchemaAndRejectsHTTPError(t *testing.T) {
	packet, _ := CreateEvidencePacket(unboundInvestigation(t))
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(503)
		io.WriteString(w, "busy")
	}))
	defer srv.Close()
	t.Setenv("FAULTLINE_TEST_OPENAI_URL", srv.URL+"/v1/responses")
	t.Setenv("OPENAI_API_KEY", "fl-test-key")
	_, err := ProposeBrief(packet, "gpt-5.6")
	if err == nil || err.Error() != "OpenAI Responses request failed: 503 busy" {
		t.Fatalf("%v", err)
	}
	if !strings.HasPrefix(body, `{"model":"gpt-5.6","instructions":"You are preparing an evidence-bounded repair brief`) ||
		!strings.Contains(body, `"text":{"format":{"type":"json_schema","name":"faultline_repair_brief","strict":true,"schema":{"type":"object"`) {
		t.Fatalf("%s", body)
	}
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := ProposeBrief(packet, "gpt-5.6"); err == nil || err.Error() != "OPENAI_API_KEY is required for a live GPT-5.6 repair brief." {
		t.Fatalf("%v", err)
	}
}
```

Live test `TestLiveProposeRepairBrief`: 120 scripted replies through the fake server: valid; wrong citations; a wrong packet digest; multiple outputs; `output_text` disagreeing with the content; HTTP 400, 429 and 500 with bodies; a non-JSON body; a BOM; invalid UTF-8; an empty body. Use 4 models (`gpt-5.6`, `""`, astral, 300 units). Compare the request record and the result or `THREW:`.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/repair/ -run TestProposeBrief`
Expected: FAIL (undefined: `ProposeBrief`).

- [ ] **Step 3: Implement** with `net/http` and a client built once (no default proxy).

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/repair/ && go test -tags faultline_test ./internal/repair/ && go build -o /tmp/fl-release ./cmd/fl && ! grep -q FAULTLINE_TEST_OPENAI_URL /tmp/fl-release && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveProposeRepairBrief -v > /tmp/s4c-t10.log 2>&1; tail -5 /tmp/s4c-t10.log`
Expected: PASS, and the release binary holds no override string.

- [ ] **Step 5: Commit**

```bash
git add internal/repair difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): live GPT-5.6 repair brief through the Responses API

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Repair worktree (`internal/repair/worktree.go`)

**Files:**
- Create: `internal/repair/worktree.go`, `internal/repair/worktree_test.go`.
- Modify: `internal/gitinv/hardened.go` (export `ToGitPath`; keep `toGitPath` as its alias), `difftest/gen/node-oracle.ts` (ops `createRepairWorktree`, `removeRepairWorktree` and `collectRepairPatch`, appended), `difftest/repair_live_test.go` (`TestLiveRepairWorktree`).

**Interfaces:**
- Consumes: `gitinv.{HardenedGitText, RunHardenedGit, ToGitPath}`, `safedir.*`, `nodefs.CpSyncTree` (Task 4), `clock.NewUUID`.
- Produces:
  - `repair.Session{RepairID, ManagedRoot, WorktreePath, ProofReadonlyPath, InstructionsPath, ArtifactsPath, BaseCommit, Repository string}`
  - `repair.CreateWorktree(repository, baseCommit, bundleDirectory, outputDirectory string) (Session, error)`
  - `repair.Cleanup{Cleaned bool; Errors []string}`
  - `repair.RemoveWorktree(repository, worktreePath, managedRoot string, keepManagedRoot bool) Cleanup`. An empty managedRoot is undefined.
  - `repair.CollectPatch(worktreePath string) (patch string, err error)`

**Gotchas:**
- **Create order** (`repair-worktree.ts:48-149`):
  1. `GIT_OBJECT_ID` (`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`). Failure: `Repair base commit is not a Git object id: <c>`.
  2. `hardenedGitText(resolve(repository), ["rev-parse", "--show-toplevel"])`. Its error text is `Git rev-parse --show-toplevel failed: <detail>`, as `HardenedGitText` already gives.
  3. `isAbsolute` check: `Git did not return an absolute repository root.`
  4. `repairId = randomUUID().slice(0, 12)`: the first `clock.NewUUID()` read.
  5. The parent:
     - if `dirname(managedRoot)` is missing, `mkdirSync(parent, {recursive: true, mode: 0o700})`;
     - `resolveSafeDirectorySegment(parent)`; on failure: `Repair output parent is not a real directory: <parent>`;
     - `existsSync(managedRoot)`: `Repair output already exists and will not be replaced: <root>`.
  6. The managed root:
     - `mkdirSync(managedRoot, 0o700)`, not recursive;
     - its safe segment check: `Repair managed root is not a real directory: …`;
     - the four managed children: `worktree`, `proof-ro`, `instructions` and `artifacts`, with the label texts `repair worktree`, `proof mount`, `instructions` and `artifacts` + ` must be a child of the managed repair root.`. They cannot fail;
     - `mkdirSync` of `instructions`, then of `artifacts`, each 0700, not recursive.
  7. `rev-parse --verify --end-of-options <c>^{commit}` on `absoluteRepo`. On failure: `rmSync(safeRoot, recursive, force)`, then `Repair base commit is not available in the repository: <c>`.
  8. `worktree add --detach <toGitPath(worktree)> <c>`. On failure: rm the root, then `Could not create detached repair worktree: ${stderr.trim() || error || "Git failed."}`.
  9. `assertNotSymlink(worktree)`.
  10. `try`:
      - `cpSync(bundle, proof-ro, …)`;
      - `mkdirSync(<worktree>/.faultline-repair, 0o700)`;
      - `README.md`: the seven lines at `:120-129`, joined by `\n`, flag `w`, mode 0666.
  11. `catch`: `removeRepairWorktree({repository: absoluteRepo, worktreePath, managedRoot: safeRoot})`, where `keepManagedRoot` is undefined, so the `worktree` child is removed too. Then throw: the message itself if it starts with `Could not`, otherwise `Could not finish repair worktree setup: <msg>`.
- **Remove order** (`:155-202`):
  - If the worktree exists: `worktree remove --force <path>`. On failure, push `git worktree remove failed: …`, then `rmSync`. A failed `rmSync` pushes `Could not delete worktree directory after remove failure: …`.
  - Always: `worktree prune`. On failure, push `git worktree prune failed: …`.
  - Then the managed-root branch, only when `!keepManagedRoot`.
- **The git environment and its effects.**
  - Every git call is `runHardenedGit`: async, with the materialization `-c` list, `-C <repo>` and the hardened environment.
  - `worktree add` writes `.git/worktrees/worktree/{HEAD,ORIG_HEAD,commondir,gitdir,index}`. It writes `logs/HEAD` only when `core.logAllRefUpdates` is on, so the repair scenarios set it to `false` in `repo/.git/config`. Measured with git 2.54: no reflog then.
  - `.git/worktrees/worktree/index` is caught by the writer harness's `isGitIndex` rule.
- **`collectRepairPatch`:**
  - `diff --binary --full-index --no-ext-diff --no-textconv --no-renames`, run in the worktree.
  - Failure: `Could not collect repair patch: …`.
  - The patch is `stdout.toString("utf8")`, then written by the caller with `writeFileSync(…, "utf8")`. Invalid UTF-8 in a binary-safe diff cannot occur (`--binary` encodes in base85), but keep the decode-then-encode path.
- **Clock.** The UUID read in step 4 is the first UUID of the repair command. Task 16 relies on the order: repairId, then any overlay staging UUIDs (`materializeFrozenOverlays`), then the sandbox container name, per verifier run.

- [ ] **Step 1: Write the failing test**

```go
// internal/repair/worktree_test.go
package repair

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) (repo, commit string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(repo, "state.txt"), []byte("bad\n"), 0o644)
	run("add", "state.txt")
	run("commit", "-q", "-m", "bad")
	return repo, run("rev-parse", "HEAD")
}

func TestCreateCollectRemoveWorktree(t *testing.T) {
	repo, commit := initRepo(t)
	bundle := t.TempDir()
	os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("{}\n"), 0o644)
	out := filepath.Join(t.TempDir(), "repairs", "r1")
	s, err := CreateWorktree(repo, commit, bundle, out)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.WorktreePath, ".faultline-repair", "README.md")); !strings.HasPrefix(string(b), "# FaultLine isolated repair worktree\n\nBase commit: "+commit+"\n") {
		t.Fatalf("%q", b)
	}
	os.WriteFile(filepath.Join(s.WorktreePath, "state.txt"), []byte("good\n"), 0o644)
	patch, err := CollectPatch(s.WorktreePath)
	if err != nil || !strings.Contains(patch, "-bad\n+good\n") {
		t.Fatalf("%v %q", err, patch)
	}
	c := RemoveWorktree(s.Repository, s.WorktreePath, s.ManagedRoot, true)
	if !c.Cleaned {
		t.Fatalf("%v", c.Errors)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Fatal("worktree left behind")
	}
	if _, err := CreateWorktree(repo, "nothex", bundle, out+"x"); err == nil || err.Error() != "Repair base commit is not a Git object id: nothex" {
		t.Fatalf("%v", err)
	}
}
```

Live test `TestLiveRepairWorktree`: 80 cases, each in fresh repositories and roots on both sides under the seeded clock:
- an unknown base, a non-repository, a symlinked parent, an existing output;
- a bundle holding a symlink, which makes `cpSync` fail;
- a worktree path that git refuses (a pre-existing `.git/worktrees/worktree` lock);
- `--keep` variants.

Compare the results or `THREW:`, the trees with `.git` masked by the writer rules, and the recorded git argv.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/repair/ -run TestCreateCollectRemoveWorktree`
Expected: FAIL (undefined: `CreateWorktree`).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/repair/ ./internal/gitinv/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveRepairWorktree -v > /tmp/s4c-t11.log 2>&1; tail -5 /tmp/s4c-t11.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repair internal/gitinv difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): isolated detached repair worktree, patch collection and cleanup

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Codex CLI runner (`internal/codexloop/runner.go`)

**Files:**
- Create: `internal/codexloop/runner.go`, `internal/codexloop/runner_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (op `resolveCodexRunner`, run in a child process with a given `PATH` and `FAULTLINE_CODEX_BIN`, appended), `difftest/codex_live_test.go` (new: `TestLiveResolveCodexRunner`).

**Interfaces:**
- Consumes: Task 3 (`SpawnSyncOptions.Timeout`), `nodeproc.{SpawnSyncWith, Getenv}`, `nodefs.{Exists, IsAbsolute}`.
- Produces:
  - `codexloop.Resolution{Bin, Version, Source string}`
  - `codexloop.ResolveRunner() (Resolution, error)`
  - `codexloop.SpawnCodex(args []string, cwd, input string, timeout time.Duration) nodeproc.Result`. It resolves on every call, as `spawnCodex` does, and returns a resolution error as a thrown value.
  - `codexloop.CLIRunner(allowFlag bool, refusal string, timeout time.Duration) Runner`. This is the CLI's runner object (`cli-app.ts:1181-1196, 2704-2719`).
    - It checks `allowFlag` first. Without it, it returns the error `refusal`. The refusal texts are `Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.` (`witness implement`) and `Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex.` (`repair`).
    - It then calls `SpawnCodex`. A resolution error is returned as the runner's throw.
    - It maps the result to `RunResult{ExitCode: status, Stdout: string-or-"", Stderr: string-or-""}`. A spawn error does not throw: TS reads only `status`, `stdout` and `stderr`.

**Gotchas:**
- **Resolution** (`runners/codex-runner.ts:73-90`):
  - The override is `process.env.FAULTLINE_CODEX_BIN?.trim()`, the JS trim. An empty result means no override.
  - An absolute override that does not exist gives `FAULTLINE_CODEX_BIN points to a missing binary: <override>`. A relative override is not checked: it is probed as given and resolved by `spawnSync` against `PATH`.
  - `resolveOnPath`:
    - It splits `PATH` on `path.delimiter`, dropping empties.
    - For each directory and each candidate name (`codex` on POSIX; `codex.cmd`, `codex.exe`, `codex` on Windows) it builds `directory.replace(/[\\/]+$/, "") + (directory.includes("\\") ? "\\" : "/") + name` and checks `existsSync`, which follows symlinks and accepts a directory.
    - If nothing is found, it falls back to the bare name.
  - The "not found" branch cannot be reached (correction 8): leave it out, with a TS-cited comment.
- **The probe** (`:39-66`):
  - `spawnSync(bin, ["--version"], {encoding: "utf8", shell: false, windowsHide: true, env: {PATH, [ComSpec, SystemRoot on Windows when set]}})`. There is no cwd, so it is fl's. There is no timeout and no input; stdin is a pipe that is closed at once.
  - `result.error` gives `Codex CLI probe failed for <bin>: <error.message>. Set FAULTLINE_CODEX_BIN or install Codex on PATH.` The message is `spawnSync <bin> ENOENT`.
  - `status !== 0` (null included) gives a detail of `(stderr ?? "") + (stdout ?? "")`, JS-trimmed, then `Codex CLI --version failed for <bin>[: <detail>]. Set FAULTLINE_CODEX_BIN or install Codex on PATH.`
  - The version is `stdout.trim().split(/\r?\n/, 1)[0] ?? ""`. Empty gives `Codex CLI --version returned empty output for <bin>.`
  - The env object's key order is `PATH`, `ComSpec`, `SystemRoot`. On Windows `nodeproc` sorts it anyway.
- **The exec** (`:92-109`):
  - The spread order is `{encoding, shell, windowsHide, ...options, env: {PATH, ComSpec?, SystemRoot?, ...options.env}}`, and the CLI passes no `options.env`.
  - `cwd` is the worktree or the overlay directory. `input` is the prompt, as UTF-8.
  - `timeout` is 120,000 ms (`witness implement`) or 180,000 ms (`repair`). The default `maxBuffer` is 1 MiB, so a larger output gives ENOBUFS and status null, which the CLI treats as exit `null`.
  - `stdout` and `stderr` are decoded per stream with `jsstr.DecodeUTF8` (`encoding: "utf8"`).
- **The allow check comes first, inside the runner.** Without the flag, no probe and no spawn happen: the argv log shows no `codex` record.

- [ ] **Step 1: Write the failing test**

```go
// internal/codexloop/runner_test.go
package codexloop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "codex")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveRunnerProbeTexts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	dir := t.TempDir()
	t.Setenv("FAULTLINE_CODEX_BIN", "  "+filepath.Join(dir, "missing")+"  ")
	if _, err := ResolveRunner(); err == nil || err.Error() != "FAULTLINE_CODEX_BIN points to a missing binary: "+filepath.Join(dir, "missing") {
		t.Fatalf("%v", err)
	}
	bin := writeScript(t, dir, "echo nope >&2; exit 2\n")
	t.Setenv("FAULTLINE_CODEX_BIN", bin)
	if _, err := ResolveRunner(); err == nil || err.Error() != "Codex CLI --version failed for "+bin+": nope. Set FAULTLINE_CODEX_BIN or install Codex on PATH." {
		t.Fatalf("%v", err)
	}
	writeScript(t, dir, "printf '  codex-cli 1.2.3\\r\\nbuild\\r\\n'\n")
	r, err := ResolveRunner()
	if err != nil || r.Version != "codex-cli 1.2.3" || r.Source != "ENV" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCLIRunnerChecksAllowFlagFirst(t *testing.T) {
	t.Setenv("FAULTLINE_CODEX_BIN", "/nonexistent/codex")
	run := CLIRunner(false, "Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex.", 180*time.Second)
	if _, err := run([]string{"exec"}, t.TempDir(), "p"); err == nil || err.Error() != "Codex repair drafting requires explicit --allow-codex-full-auto in addition to --with-codex." {
		t.Fatalf("%v", err)
	}
}
```

Live test `TestLiveResolveCodexRunner`: 60 environments, each run on both sides in a child process with the given env:
- an override that is absent, blank, padded, relative, or absolute-missing;
- override scripts that fail, return empty, return CRLF, or are not executable (EACCES);
- `PATH` with trailing slashes, empty entries, a directory named `codex`, a symlinked `codex`, and no `codex` (the bare fallback, ENOENT).

Compare `{bin, version, source}` or the message.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/codexloop/ -run 'TestResolveRunner|TestCLIRunner'`
Expected: FAIL (undefined: `ResolveRunner`, `CLIRunner`).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/codexloop/ && GOOS=windows go vet ./internal/codexloop/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveResolveCodexRunner -v > /tmp/s4c-t12.log 2>&1; tail -5 /tmp/s4c-t12.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexloop difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): Codex CLI resolution, version probe and exec runner

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: Repaired-run helpers (`commitRepairedWorktreeState`, `collectRepairedPreventionRuns`)

**Files:**
- Create: `internal/bundle/prevention/repaired.go`, `internal/bundle/prevention/repaired_test.go`.
- Modify: `internal/nodefs/fs.go` (add `ReadDirTypes`, if `ReadDirNames` cannot carry the entry types), `difftest/gen/node-oracle.ts` (ops `commitRepairedWorktreeState` and `collectRepairedPreventionRuns`, the latter with a scripted `verifyOnce`, appended), `difftest/prevention_live_test.go` (`TestLiveCommitRepairedWorktreeState`, `TestLiveCollectRepairedRuns`).

**Interfaces:**
- Consumes: Task 3 (`Timeout`); `safegit.Doctor()`; `nodeproc.SpawnSyncWith`; `nodefs.{Tmpdir, ReadBytes, Lstat, RemoveAll}`; `clock.NewUUID`; `gitproof.{GitInvestigationResultSchema, FrozenWitnessSchema, StableExecutionCount}`.
- Produces:
  - `prevention.RepairedState{Commit, Tree string}`
  - `prevention.CommitRepairedWorktreeState(worktreePath, baseCommit string) (RepairedState, error)`
  - `prevention.Verdict{OK bool; Detail string; ExecutionID string; HasExecutionID bool}`, with `JSON()` in TS key order `ok, detail, executionId?`.
  - `prevention.CollectRepairedRuns(worktreePath, bundleDirectory, repairedCommit, repairedTree string, verifyOnce func() (Verdict, error)) (runs jsjson.Value, reasons []string, err error)`. `err` is a throw, a parse error for instance. `reasons` non-nil is `{ok: false}`.
  - `nodefs.ReadDirTypes(path string) ([]nodefs.Dirent, error)`, with `type Dirent struct{ Name string; IsDir, IsFile bool }`, in `readdirSync` order. Its error is Node's scandir text.

**Gotchas:**
- **The index file.** `temporaryIndexPath = join(tmpdir(), "faultline-repair-<uuid>.index")`: one `clock.NewUUID()` read, before any git call.
- **The git call.** Each call is `spawnSync("git", [...DOCTOR_SAFE_GIT_CONFIG, "-c", "core.attributesFile=/nonexistent/faultline-attributes", "-C", root, ...args], {encoding: "utf8", shell: false, windowsHide: true, timeout: 120_000, maxBuffer: 32 MiB, env})`.
  - The list is `safegit.TurnSnapshot()`, which equals Doctor plus `core.attributesFile`.
  - `env` is, in order: `PATH`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=<null device>`, `GIT_ATTR_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0`, `GIT_NO_REPLACE_OBJECTS=1`, `GIT_LFS_SKIP_SMUDGE=1`, `GIT_ALLOW_PROTOCOL=none`, `GIT_INDEX_FILE=<temp index>`, then on Windows `SystemRoot`, `ComSpec` and `PATHEXT` when set.
  - `GIT_INDEX_FILE` is the raw path. Use no `toGitPath`.
- **Errors.**
  - `run` throws `result.stderr || result.stdout || result.error?.message || "git <args joined> failed"`. The stderr text is untrimmed, a JS `||` on strings.
  - It returns `stdout.trim()`.
  - The `hash-object -w --stdin` call passes `input: readFileSync(file)`, the raw bytes. Its blob check uses the regex `^[a-f0-9]{40}([a-f0-9]{24})?$`, with the text `git hash-object --stdin did not return a blob id for <abs>`. Its error falls back to `git hash-object --stdin failed for <abs>`.
- **Order** (`:451-492`):
  1. `read-tree <base>`.
  2. `ls-files -z`: split on `\0`, drop empties, replace `\\` with `/`, and keep as a Set in order.
  3. `listWorktreeFiles(root)`:
     - `readdirSync(withFileTypes)` in byte order;
     - skip `.git`;
     - recurse into directories;
     - keep only `isFile()` entries. A symlink is not a file, so it is skipped silently: the check at `:461` cannot fire for a listed file, except in a race.
  4. Per file, in walk order:
     - the relative path, split on `sep` and joined with `/`;
     - lstat;
     - mode `100755` if any execute bit is set, else `100644`;
     - `hash-object`;
     - `update-index --add --cacheinfo <mode>,<blob>,<rel>`.
  5. Per indexed path not seen, in Set order: `update-index --force-remove -- <path>`.
  6. `write-tree`.
  7. `commit-tree <tree> -p <base> -m "faultline: repaired prevention state"`.
     - The result must match the object-id regex, or throw `git commit-tree did not return a Git object id for the repaired state.`
     - It needs `user.name` and `user.email` from the repository config (correction 2).
  8. `finally`: `rmSync(index, {force: true})`.
- **What the commit includes.** The worktree's `.faultline-repair/README.md` and every materialized overlay are committed: the walk does not skip them. This is TS behaviour, reproduced, and the reason for Task 2's blob and tree masks.
- **`collectRepairedPreventionRuns`** (`:486-540`):
  1. Read `witness/frozen.json` first, then `investigation.json`. Parse errors are thrown to the caller, which turns them into reasons.
  2. The homogeneous environment check: `Cannot collect repaired runs without a homogeneous environment digest.`
  3. Three attempts, each calling `verifyOnce()`:
     - `!ok` gives `Repaired-state execution <n>/3 failed: <detail>`;
     - an execution id that is not a string or does not match `^sha256:[a-f0-9]{64}$` gives `Repaired-state execution <n>/3 omitted a content-bound executionId; refusing PREVENTION_VERIFIED placeholder synthesis.`;
     - `runId = digestJson({schemaVersion: "faultline.prevention-repaired-run.v1", attempt, commit, tree, witnessDigest, environmentDigest, executionId, detail})`.
  4. Distinct run ids: `Collected repaired run digests were not distinct.` It cannot fire, because `attempt` is part of each digest. Port it with a TS-cited comment, and list the text in `unreachableLiterals` with that reason.
  - The run objects' key order is `runId, executionId, commit, tree, verdict, witnessDigest, environmentDigest, executionTrust, executionKind`.

- [ ] **Step 1: Write the failing test**

```go
// internal/bundle/prevention/repaired_test.go
package prevention

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("config", "user.name", "FaultLine Test")
	run("config", "user.email", "faultline@example.test")
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("bad\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "bad")
	return dir, run("rev-parse", "HEAD")
}

func TestCommitRepairedWorktreeState(t *testing.T) {
	dir, head := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("good\n"), 0o755)
	os.Remove(filepath.Join(dir, "gone.txt"))
	s, err := CommitRepairedWorktreeState(dir, head)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", dir, "ls-tree", s.Tree).Output()
	if !strings.Contains(string(out), "100755 blob ") || strings.Contains(string(out), "gone.txt") {
		t.Fatalf("%s", out)
	}
	msg, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%P %s", s.Commit).Output()
	if strings.TrimSpace(string(msg)) != head+" faultline: repaired prevention state" {
		t.Fatalf("%s", msg)
	}
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "faultline-repair-*.index")); len(entries) != 0 {
		t.Fatalf("index left: %v", entries)
	}
}
```

Live tests:
- `TestLiveCommitRepairedWorktreeState`: the same machine and the same clock second are not guaranteed, so compare the tree id exactly and the commit object through `cat-file -p` with the time masked. Run 120 worktrees: executable bits, nested directories, symlinks (skipped), empty files, a `.git` file (a linked worktree), CRLF files, non-UTF-8 names, deleted tracked files, no identity (the machine-dependent text, compared on the same machine), and an unknown base (a `read-tree` error).
- `TestLiveCollectRepairedRuns`: 200 scripted `verifyOnce` sequences (`ok` false at each attempt, missing, malformed or uppercase ids, details with astral characters) on `git-unbound` and on a heterogeneous base. Compare the runs or the reasons or `THREW:`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/prevention/ -run TestCommitRepaired`
Expected: FAIL (undefined: `CommitRepairedWorktreeState`).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/bundle/prevention/ ./internal/nodefs/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveCommitRepairedWorktreeState|TestLiveCollectRepairedRuns' -v > /tmp/s4c-t13.log 2>&1; tail -5 /tmp/s4c-t13.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/prevention internal/nodefs difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): repaired worktree commit and repaired run collection

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: GitHub provenance library (`internal/provenance`)

**Files:**
- Create: `internal/provenance/provenance.go`, `internal/provenance/provenance_test.go`, `difftest/provenance_live_test.go`.
- Modify: `difftest/gen/node-oracle.ts` (ops `createGithubProvenanceReceiptFromView`, `githubActionsIdentityFromEnvironment` (child process with a given env), `verifyGithubProvenanceBinding`, `verifyGithubArtifactAttestation` (with a scripted `gh`) and `writeGithubProvenanceReceipt`, appended).

**Interfaces:**
- Consumes: `proofview.LoadVerifiedGitProofView`; Task 3 (`ExecFileSync`); Task 7's `jsdate.IsCanonicalISO`; `schema.*` (including `Datetime()` and `Trim()`); `canonical.*`; `safedir.*`.
- Produces:
  - `provenance.DefaultRoot(workspace string) string`, which is `resolve(workspace, ".faultline", "provenance")`.
  - `provenance.IdentityFromEnvironment(workspace string) (jsjson.Value, error)`
  - `provenance.CreateReceipt(bundleDirectory string, ci jsjson.Value) (jsjson.Value, error)`
  - `provenance.WriteReceipt(outputFile string, receipt jsjson.Value, root string) (string, error)`
  - `provenance.ReadTrust(path string) (jsjson.Value, error)`
  - `provenance.VerifySigned(bundleDirectory, receiptFile, attestationBundleFile string, trust jsjson.Value) jsjson.Value`. It returns the result object in TS key order: `valid, assurance, errors, binding, artifactAttestation, limitation`. Here `binding` is `{valid, errors, receiptDigest, proofRootDigest, receipt?}` and `artifactAttestation` is `{valid, command, errors}`. The key order is TS's literal order, which differs per branch: copy it per branch.

**Gotchas:**
- **Identity** (`:233-264`):
  - `GITHUB_ACTIONS !== "true"` gives `GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt.`
  - `requiredGithubEnvironment` throws `GitHub Actions provenance requires <NAME>.` for a missing or blank value, and returns it trimmed. Its order: `GITHUB_SHA` (then `GitObjectIdSchema.parse`, a ZodError), then the tree, then in the object's order `GITHUB_REPOSITORY`, `GITHUB_WORKFLOW_REF`, `GITHUB_REF`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT` (through `Number()`, the `jsjson.ToNumber` port), `GITHUB_EVENT_NAME`.
  - The tree is `execFileSync("git", ["rev-parse", "<commit>^{tree}"], {cwd: resolve(workspace), encoding: "utf8", windowsHide: true, stdio: ["ignore", "pipe", "pipe"]}).trim()`. It inherits the full environment, with no `-c` list. On failure: `GitHub provenance could not resolve the checked-out commit tree: <execFileSync message>`.
  - Then `GithubActionsIdentitySchema.parse`, a ZodError. The run attempt must be a positive integer and the event `push`.
- **Receipt creation** (`:152-212`):
  - `loadVerifiedGitProofView(bundle)`. Its throws reach the CLI.
  - Per run, in order: a non-native-Docker or non-isolated run, or a null image, gives `GitHub provenance requires a verified native-Docker proof bundle.`, then `SandboxBindingSchema.parse`.
  - The bindings are deduplicated by canonical JSON in a `Map`, which keeps the first insertion, and sorted by `canonicalJson(a).localeCompare(canonicalJson(b))`.
  - `runIds` and `executionIds`: `sortedUnique` (Set, then a `localeCompare` sort).
  - `startedAt`: the first of an ascending `localeCompare` sort. `finishedAt`: the first of a descending one.
  - `createdAt`: `new Date().toISOString()`, the only clock read. It must pass `TimestampSchema`: zod `datetime({offset: true})` plus the canonical refine.
  - `manifestDigest` is `digestJson(view.manifest)`. `range` is `view.manifest.resolvedRange`.
- **Writing** (`:290-312`):
  - The nested check: `GitHub provenance output must be a file beneath <root>`.
  - `ensureRealDirectoryTree(root)`, then `ensureRealDirectoryTree(dirname(output))`, with 0700 directories. Their texts: `Provenance root must be a real directory: …` and `Provenance output cannot traverse a symbolic link or non-directory: …`.
  - An existing output: `GitHub provenance receipt already exists and will not be replaced: <output>`.
  - `writeFileSync(output, canonical + "\n", {mode: 0o600, flag: "wx"})`. EEXIST gives the same text; anything else is Node's raw error.
  - `GithubProvenanceReceiptSchema.parse(receipt)` runs inside the write call, before the open.
- **Reading** (`readRegularJson`):
  - lstat (Node's raw ENOENT text);
  - `<label> must be a regular non-symlink file`;
  - size over 1,000,000: `<label> exceeds the 1000000-byte limit`;
  - `<label> is not valid JSON: <SyntaxError message>`.
- **Binding** (`:351-386`):
  - Both safeParses run first, and both errors are pushed.
  - The digest check pushes and continues.
  - Then a try: the view, `createGithubProvenanceReceiptFromView(view, receipt.ci, receipt.createdAt)` (no clock read: `createdAt` is given), the canonical comparison text, then `trustErrors` in order.
  - The catch pushes `GitHub provenance binding verification failed safely: …` and returns `proofRootDigest: null` with `receipt`.
- **`gh`** (`:398-438`):
  - The command starts as `["attestation", "verify", resolve(receiptFile)]`.
  - The checks, in order: the trust schema, the source commit schema, the receipt regular file, the attestation bundle regular file, then `trustedRoot = resolve(dirname(resolve(bundleFile)), trust.trustedRootFile)` and its regular file. The CLI already made `trustedRootFile` absolute relative to the trust file (`cli-app.ts:2522`), so this resolve keeps it.
  - Then the pushes: `--repo`, `--bundle`, `--custom-trusted-root`, `--signer-workflow`, `--source-ref`, `--source-digest`, and `--deny-self-hosted-runners` when true.
  - The call is `spawnSync("gh", command, {encoding: "utf8", windowsHide: true})`, with the full inherited environment, no timeout and no input.
  - Results:
    - an error gives `Could not run gh: <message>`;
    - a non-zero status gives `detail = (stdout + "\n" + stderr).trim().slice(0, 8000)`, then `GitHub artifact attestation verification failed[: detail]`;
    - a throw gives `GitHub artifact attestation verification failed safely: <message>`.
  - The returned `command` is `["gh", ...command]` as far as it was built.
- **`verifySignedGithubProvenance`.** A failed receipt read gives the two "not attempted" texts at `:465` and `:476`. Copy them exactly.
- **Live-gating.** Add the identity and `gh` paths' error texts that the writer cases cannot reach (the EEXIST race, `Could not run gh` with a missing `gh` on PATH, which is reachable through `pathTools`) as the coverage gate demands. Prefer a writer case where one is possible.

- [ ] **Step 1: Write the failing test**

```go
// internal/provenance/provenance_test.go
package provenance

import (
	"strings"
	"testing"
)

func TestIdentityRequiresGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil ||
		err.Error() != "GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt." {
		t.Fatalf("%v", err)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SHA", "  ")
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil || err.Error() != "GitHub Actions provenance requires GITHUB_SHA." {
		t.Fatalf("%v", err)
	}
	t.Setenv("GITHUB_SHA", strings.Repeat("a", 40))
	if _, err := IdentityFromEnvironment(t.TempDir()); err == nil ||
		!strings.HasPrefix(err.Error(), "GitHub provenance could not resolve the checked-out commit tree: Command failed: git rev-parse "+strings.Repeat("a", 40)+"^{tree}\n") {
		t.Fatalf("%v", err)
	}
}

func TestVerifySignedUnreadableReceipt(t *testing.T) {
	r := VerifySigned(t.TempDir(), "/nonexistent/receipt.json", "/nonexistent/b.json", nil)
	if r.Get("valid").Bool() || r.Get("assurance").Str() != "NOT_VERIFIED" ||
		r.Get("artifactAttestation", "errors").Items()[0].Str() != "artifact attestation was not attempted" {
		t.Fatalf("%s", r.Get("errors").Items()[0].Str())
	}
}
```

Live tests in `difftest/provenance_live_test.go`:
- `TestLiveProvenanceReceipt`: every Git base (the native-Docker ones, `git-sample-self-incident` included) × 20 generated identities (edge timestamps, `runAttempt` strings like `1e0`, `01` and `0`, padded values, control characters). Compare the receipts or `THREW:`, under the seeded clock.
- `TestLiveProvenanceBinding`: 600 receipt and trust mutations (each schema field, digest tamper, each trust error, both schema failures together).
- `TestLiveGhAttestation`: 40 runs with scripted `gh` (pass, fail, long output, missing `gh`, missing trust root, a symlinked bundle, `denySelfHostedRunners` true and false). Compare the result objects and the recorded `gh` argv.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/provenance/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/provenance/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveProvenance|TestLiveGhAttestation' -v > /tmp/s4c-t14.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t14.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provenance difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): GitHub artifact provenance receipt, binding and gh verification

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 15: `fl attest create|verify`

**Files:**
- Create: `internal/cli/attest.go`, `internal/cli/attest_test.go`.
- Modify:
  - `internal/bundle/demo/verify.go`: add `VerifyWithManifest(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value)`; `Verify` calls it.
  - `internal/cli/cli.go`: a `case command == "attest":` line, appended before `topLevelCommands`.
  - `difftest/gen/scenarios.ts`: scenario `attest-bundles`, appended. Its bases are `git: git-unbound`, `demo: demo-replay`, `prevention: prevention-verified` and `turn: <a minimal directory whose manifest.json has schemaVersion faultline.turn-proof-bundle.v1>`, and its file is `notjson/manifest.json` with `{`.
  - `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl` and `.github/scripts/windows-writer-skips.txt` (appended).

**Interfaces:**
- Consumes: Task 7; `gitproof.VerifyWithManifest`; `demo.VerifyWithManifest`; `option`, `requiredOption`, `hasFlag`, `fail`, `printJSON`, `obj`.
- Produces: `func attestCommand(e *env, args []string) error`.

**Gotchas:**
- **`attestationStore(args)`** is `resolve(option("--store") ?? defaultAttestationStore())`. It is computed before the switch, so a cwd failure throws even for a usage error.
- **`proofBundleSummary`** (`cli-app.ts:2431-2459`):
  - It reads `manifest.json` and keeps `schemaVersion` only when it is a string. Every read or parse error is swallowed.
  - `faultline.git-proof-bundle.v1`: `gitproof.Verify(root, "", false)`. If invalid: `Git proof bundle is invalid: <errors joined "; ">`. The `witnessDigest` comes from `verification.manifest`, the parsed manifest.
  - `faultline.turn-proof-bundle.v1` throws ``Attestation create currently supports Git proof bundles only. Verify turn packages with `fl verify` (EXPERIMENTAL_TURN).``, with the backticks verbatim.
  - Anything else goes to the demo verifier: `Proof bundle is invalid: …`. A prevention package and a non-JSON manifest end here.
- **`create` order:**
  1. `requiredOption("--bundle")`.
  2. The summary (verification).
  3. Then, in the object literal's order, `--receipt`, `--subject` and `--issuer` (correction 11).
  4. The optional `--source-digest` and `--checkpoint-digest` go through `option()`, so a value in last position is undefined.
  5. The output is `{status: "ATTESTED", path, receiptDigest, bundleKind, limitation}`.
- **`verify`:**
  - Without a receipt id: `Usage: fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]`.
  - `option("--expect-digest")` may be undefined.
  - It prints the verification JSON and exits `valid ? 0 : 1`.
- **Default:** `Usage: fl attest create|verify ...`.

**Writer cases** (scenario `attest-bundles` unless named). Every `create` that reaches a git bundle is `posixOnly`, because the Git verifier runs git:
- `attest-create-git` (posixOnly; TS cross-verifies the receipt)
- `attest-create-git-with-digests` (`--source-digest` and `--checkpoint-digest` valid)
- `attest-create-git-bad-source-digest` (ZodError)
- `attest-create-demo`
- `attest-create-prevention` (refused)
- `attest-create-turn`
- `attest-create-notjson`
- `attest-create-missing-bundle`
- `attest-create-bad-receipt` (`a/b`)
- `attest-create-receipt-129`
- `attest-create-subject-padded`
- `attest-create-issuer-control`
- `attest-create-no-subject` (after verification: the order check)
- `attest-create-twice` (two invocations: "already exists")
- `attest-create-store-outside` (`--store /tmp/x` is a `<TMP>` path)
- `attest-create-store-symlink` (mutation: `.faultline/attestations` as a symlink)
- `attest-create-root-file` (mutation: `.faultline` is a file)
- `attest-create-custom-store` (`--store .faultline/attestations/team`)
- `attest-verify-ok` (create, then verify with `<STDOUT:0:/receiptDigest>`)
- `attest-verify-no-expect`
- `attest-verify-mismatch`
- `attest-verify-bad-expect`
- `attest-verify-missing`
- `attest-verify-tampered` (mutation after create: replace in file)
- `attest-verify-symlink-receipt`
- `attest-verify-invalid-json`
- `attest-verify-no-store`
- `attest-verify-no-id`
- `attest-unknown-action`
- `attest-no-action`

- [ ] **Step 1: Add the cases; generate the goldens; see them fail**

```bash
podman run --rm --userns=keep-id -u 1000 -v "$PWD":/w -w /w localhost/r73img sh -c 'pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && timeout 280 env FAULTLINE_WRITERS_ONLY="^attest-" pnpm exec tsx difftest/gen/gen-writers.ts' > /tmp/s4c-t15-gen.log 2>&1; tail -3 /tmp/s4c-t15-gen.log
go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?attest-' > /tmp/s4c-t15.log 2>&1; grep -c 'not yet ported: attest' /tmp/s4c-t15.log
```

Expected: the goldens hold only added `attest-` lines (`git diff --stat`). The replay fails with `not yet ported: attest`.

- [ ] **Step 2: Unit-test the order** in `internal/cli/attest_test.go`: a missing `--subject` on an invalid bundle reports the bundle error, not the missing option.

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestAttestCreateVerifiesBundleBeforeOptions(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"attest", "create", "--bundle", t.TempDir(), "--receipt", "r1"}, &out, &errb)
	if code != 1 || !strings.HasPrefix(errb.String(), "FaultLine error: Proof bundle is invalid: ") {
		t.Fatalf("%d %q", code, errb.String())
	}
}
```

- [ ] **Step 3: Implement** `attestCommand` and `demo.VerifyWithManifest`, and add the dispatch line.

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ ./internal/bundle/demo/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?attest-|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4c-t15.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t15.log | tail -20`
Expected: PASS. A mismatch is fixed in Go, never in the golden. Every receipt Go writes is verified by TS (Task 1's cross-check).

- [ ] **Step 5: Commit**

```bash
git add internal/cli internal/bundle/demo difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl attest create and verify with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 16: Repair orchestration and the frozen-witness verifier (`repairWithCodex`)

**Files:**
- Create: `internal/codexloop/repair.go`, `internal/codexloop/verifier.go`, `internal/codexloop/repair_test.go`.
- Modify:
  - `internal/clock/seeded.go`: add `ConsumedForTest() (ticks, uuids int)`, which returns the seeded counters.
  - `difftest/gen/seed.mjs`: expose the same two counters as `globalThis.__faultlineSeedCounters`. This is read only by the oracle op, so no output changes.
  - `difftest/gen/node-oracle.ts`: op `repairWithCodex` with an injected scripted runner and sandbox runner, appended. It returns the counters with the result.
  - `difftest/codex_live_test.go`: `TestLiveRepairWithCodex`.

**Interfaces:**
- Consumes:
  - Task 5 (`ArtifactOptions.RepairBaseTree` and `HasRepairBaseTree`);
  - Task 11 (`repair.CreateWorktree`, `RemoveWorktree`, `CollectPatch`);
  - Task 12 (`Runner`);
  - Task 13 (`CommitRepairedWorktreeState`, `CollectRepairedRuns`, `Verdict`);
  - `prevention.{WriteFromArtifacts, DigestRepairPatchFile, DefaultRoot, UpsertAgentsMd}`;
  - `gitinv.MaterializeFrozenOverlays(worktree, frozen, nil)`;
  - `sandbox.{CreatePlan, AuditPlan, Execute, NativeRunner, Runner}`;
  - `gitproof.{GitInvestigationResultSchema, FrozenWitnessSchema}`.
- Produces:
  - `codexloop.SelectRepairBaseCommit(investigation jsjson.Value) (string, error)`
  - `codexloop.VerifyCandidate func(worktree, baseCommit, patchPath, bundleDirectory string) (prevention.Verdict, error)`. A non-nil error is the verifier's rejection: TS reads and parses the bundle's `investigation.json` and `witness/frozen.json` outside its try.
  - `codexloop.FrozenWitnessVerifier(runner sandbox.Runner, injected bool) VerifyCandidate`
  - `codexloop.RepairOptions{BundleDirectory, ExpectRoot, Repository, OutputDirectory string; Invariant string; HasInvariant bool; WithCodex, InstructionsOnly, KeepWorktree bool; Runner Runner; VerifyCandidate VerifyCandidate; VerifyBundle func(dir, expectRoot string) (valid bool, errors []string, rootDigest *string)}`
  - `codexloop.RepairWithCodex(o RepairOptions) (jsjson.Value, error)`. It returns the result in TS key order: `schemaVersion, cleanupErrors, status, transport, worktreePath, instructionPath, managedRoot, baseCommit, patchPath, codexThreadId, nextCommands, note, limitation`. `baseRepairResult` spreads `cleanupErrors` first, and `...partial` then overwrites it with the same value. That is why `cleanupErrors` comes second: an object spread keeps the first key's position. A final `preventionExport` follows when set.

**Gotchas:**
- **Flow** (`codex-loop.ts:347-665`). Copy every status, note and limitation string verbatim.
  1. `verifyBundle(bundle, expectRoot)`. Invalid gives `BUNDLE_INVALID` with note `errors.join("; ") || "Proof bundle verification failed."`.
  2. Read and parse `investigation.json`, then `selectRepairBaseCommit`:
     - the first `PASS_TO_FAIL`'s `after.commit`;
     - else `resolvedRange?.descendant.commit` when truthy;
     - else `Verified investigation does not identify a repair base commit.`

     A throw gives `BUNDLE_INVALID` with the second limitation.
  3. `outputDirectory = resolve(…)`; the invariant default; `rootDigest = verification.rootDigest ?? expectRoot`; `repository = resolve(investigation.repository ?? options.repository)` (correction 3).
  4. `instructionsOnly`:
     - `mkdirSync(output, {recursive: true, mode: 0o700})`;
     - write `repair-instructions.md` (the lines at `:283-303`; the worktree line is `Isolated worktree: not created (instructions only)`; the proof path is `resolve(bundle)`);
     - return `REPAIR_INSTRUCTIONS_PREPARED`.
  5. The session:
     - `CreateWorktree`;
     - `instructions/repair-instructions.md`;
     - `artifacts/repair-meta.json` (`{schemaVersion, baseCommit, rootDigest, repairId}`, indented).

     A throw anywhere in the outer try gives `REPAIR_SETUP_FAILED`, with `managedRoot` set to `existsSync(outputDirectory) ? outputDirectory : null`.
  6. With Codex (`withCodex && runner`), `transport` becomes `CODEX_CLI`:
     - The prompt is the four sentences at `:457-462`, joined with `" "`.
     - The runner is called with `cwd = worktree`.
     - `codexThreadId = result.threadId ?? null`, which is always null.
     - `artifacts/codex-thread.json` gets `{threadId, exitCode, stdoutPreview: stdout.slice(0, 4096), stderrPreview: …}`, with the slices in UTF-16 units.
     - Exit not 0: `CODEX_UNAVAILABLE` (transport stays `CODEX_CLI`).
     - Otherwise: `CollectPatch`, then write `artifacts/repair.patch`, then `CODEX_DRAFT_RECORDED`.
     - With a verifier: write `artifacts/verification.json` (the verdict, indented, keys `ok, detail, executionId?`), then `REPAIR_CANDIDATE_VERIFIED` or `REPAIR_VERIFICATION_FAILED`.
     - Any throw inside: `CODEX_UNAVAILABLE`, transport `NONE`, note `Codex CLI could not be started: <msg>` (correction 7).
  7. Prevention export, only for `REPAIR_CANDIDATE_VERIFIED` with a session, a patch and a verifier:
     1. `CommitRepairedWorktreeState(worktree, baseCommit)`.
     2. `CollectRepairedRuns` with `verifyOnce`, the same verifier.
     3. On failure: `preventionExport = {ok: false, reasons}` and the note gains ` Prevention export fail-closed: <reasons joined "; ">`.
     4. On success:
        - `preventionOut = join(defaultPreventionProofRoot(), "from-repair-" + Date.now())`, the cwd's root. This is one `clock.NowMillis()` read, after the three verifier runs.
        - `repairBaseTree` is the first `PASS_TO_FAIL`'s `after.tree`, or undefined.
        - `WriteFromArtifacts` with `CodexThreadID` only when non-null (never, from the CLI).
        - On failure: reasons as above.
        - On success: `preventionExport = {ok: true, directory, rootDigest, classification}`.
        - For `PREVENTION_VERIFIED`: `UpsertAgentsMd(options.repository, body)`. This uses `options.repository`, the CLI's `--repo` or cwd, not the investigation's. Errors are swallowed.
        - `artifacts/prevention-export.json`, indented.
        - The note gains ` Prevention verified package written to <dir>.`.
     5. Any throw in the export gives `{ok: false, reasons: [message]}` with the same note suffix.
  8. Cleanup, unless `keepWorktree`:
     - `RemoveWorktree(session.repository, worktree, managedRoot, keepManagedRoot: true)`;
     - `cleanupErrors` gains its errors;
     - if not cleaned and the status is `REPAIR_WORKTREE_READY`, `CODEX_DRAFT_RECORDED` or `REPAIR_CANDIDATE_VERIFIED`: `WORKTREE_CLEANUP_FAILED`, and the note gains ` Worktree cleanup reported errors: …`.
  9. `worktreePath` is the path only with `keepWorktree`. `nextCommands` lists, in order: `Review <instructionPath>` if any, `Inspect <patchPath>` if any, the fixed human-review line, then the prevention verify line or the long fallback line (`:648-659`).
- **The verifier** (`:211-273`). Per call:
  1. Read the investigation and `witness/frozen.json`, both zod-parsed. A read or parse error escapes the verifier's try, because the reads come first: TS lets it reject. Inside `repairWithCodex` it is caught by the Codex try (note `Codex CLI could not be started: …`) or by the export try. Reproduce both placements.
  2. The image is the first run whose `sandbox.runtime.image` is a string matching `/@sha256:[a-f0-9]{64}$/`. None gives `{ok: false, detail: "Repair verification fail-closed: proof bundle runs do not record a digest-pinned Docker image."}`.
  3. Then a try:
     - `materializeFrozenOverlays(worktree, frozen)`, with no index inspector. It reads one UUID per overlay;
     - `createSandboxPlan({witness: {digest: frozenDigest, command}, sourceDirectory: worktree, mode: "DOCKER_ISOLATED", image})`, with no limits or env, so the defaults apply. It reads the container-name UUID;
     - `auditSandboxPlan`;
     - `executeSandboxPlan(plan, undefined)`: the native runner, executor `NATIVE_DOCKER`.
  4. A PASS with `PREDICATE_PASS` gives the `executionId` digest of the object at `:243-255`, keys in that order, with `stdoutDigest` and `stderrDigest` as `sha256:` plus `canonical.SHA256Hex` of the strings. Detail: `Frozen witness PREDICATE_PASS on repaired worktree (NATIVE_DOCKER).`
  5. Anything else: `Frozen witness did not PREDICATE_PASS (verdict=<v>, reason=<r>).` A throw gives `Repair verification fail-closed: <msg>`.
  - The CLI calls `createFrozenWitnessRepairVerifier()` with no runner, so `injected` is false.
- **Clock and UUID order** for a full `--with-codex` success:
  1. CLI: `Date.now()` for the default output name, only when `--output` is absent.
  2. `repairId` UUID.
  3. First verifier: overlay UUIDs, then the container UUID.
  4. The repaired-index UUID (`commitRepairedWorktreeState`).
  5. Verifier ×3: overlay and container UUIDs each.
  6. `Date.now()` for `from-repair-`.
  7. The prevention stage UUID.

  Go must read `internal/clock` in exactly this order. Test it with the seeded clock against TS (`TestLiveRepairWithCodex`).

- [ ] **Step 1: Write the failing test**

```go
// internal/codexloop/repair_test.go
package codexloop

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestSelectRepairBaseCommit(t *testing.T) {
	inv, _ := jsjson.Parse(`{"transitions":[{"kind":"FAIL_TO_PASS","after":{"commit":"a"}},{"kind":"PASS_TO_FAIL","after":{"commit":"b"}}],"resolvedRange":{"descendant":{"commit":"c"}}}`)
	if c, err := SelectRepairBaseCommit(inv); err != nil || c != "b" {
		t.Fatal(c, err)
	}
	inv, _ = jsjson.Parse(`{"transitions":[],"resolvedRange":null}`)
	if _, err := SelectRepairBaseCommit(inv); err == nil || err.Error() != "Verified investigation does not identify a repair base commit." {
		t.Fatal(err)
	}
}

func TestRepairBundleInvalidKeyOrder(t *testing.T) {
	r, err := RepairWithCodex(RepairOptions{BundleDirectory: t.TempDir(), ExpectRoot: "sha256:x", Repository: t.TempDir(), OutputDirectory: t.TempDir(),
		VerifyBundle: func(string, string) (bool, []string, *string) { return false, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	keys := r.Obj().Keys()
	if keys[0] != "schemaVersion" || keys[1] != "cleanupErrors" || keys[2] != "status" || r.Get("note").Str() != "Proof bundle verification failed." {
		t.Fatalf("%v %s", keys, jsjson.Stringify(r))
	}
}
```

Live test `TestLiveRepairWithCodex` drives both sides on the same scenario copies, with scripted Codex results (exit 0 with edits, exit 3, a throw, null) and scripted sandbox runners: PASS ×4, FAIL on the first run, PASS then a missing execution id, PASS then ERROR. It covers:
- `instructionsOnly`, `keepWorktree`;
- a heterogeneous base (export fail-closed);
- a base whose runs have no pinned image;
- an `investigation.repository` that does not exist (`REPAIR_SETUP_FAILED`).

It compares the result JSON (with Task 2's masks), the managed-root tree, and the clock and UUID consumption counts (`clock.ConsumedForTest()` against `__faultlineSeedCounters`).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/codexloop/ -run 'TestSelectRepairBaseCommit|TestRepairBundleInvalid'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/codexloop/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveRepairWithCodex -v > /tmp/s4c-t16.log 2>&1; tail -5 /tmp/s4c-t16.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexloop difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): repairWithCodex orchestration and the frozen-witness repair verifier

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 17: `fl provenance create|verify`

**Files:**
- Create: `internal/cli/provenance.go`, `internal/cli/provenance_test.go`.
- Modify:
  - `internal/cli/cli.go`: a `case command == "provenance":` line, appended.
  - `difftest/gen/scenarios.ts`: scenarios `provenance-ci` and `provenance-signed`, appended.
    - `provenance-ci`: the `git-unbound` base at `bundle/`, plus a git repository at `.` with one commit (for `GITHUB_SHA`'s tree), whose `HEAD` id is also written to `sha.txt`.
    - `provenance-signed`: `provenance-ci`, plus a TS-written `.faultline/provenance/ci-receipt.json` (built by the scenario's argv under the CI env), `trust.json` (`trustedRootFile: "root.json"`), `root.json` and `attestation.sigstore.json`.
  - `difftest/testdata/writers.json`, `golden/writers.jsonl` and `.github/scripts/windows-writer-skips.txt` (appended).

**Interfaces:**
- Consumes: Task 14; Task 1's `<FILE:sha.txt>` env placeholder and `fakeGh`.
- Produces: `func provenanceCommand(e *env, args []string) error`.

**Gotchas:**
- **`create` order** (`cli-app.ts:2497-2516`):
  1. `requiredOption("--bundle")`, resolved.
  2. `defaultGithubProvenanceRoot()`, from the cwd.
  3. The output: `resolve(option("--output") ?? join(root, "ci-receipt.json"))`.
  4. The identity, with the workspace as the cwd.
  5. The receipt: verification, then the clock.
  6. The write.
  7. Stdout keys: `status, path, receiptDigest, proofRootDigest, signing, limitation`. The `signing` text is at `:2509`.
- **`verify`:**
  - `requiredOption("--trust")`, resolved, then `ReadTrust`. A ZodError or a read error throws.
  - `resolvedTrust` is `{...trust, trustedRootFile: resolve(dirname(trustFile), trust.trustedRootFile)}`. The spread keeps the parsed key order, with `trustedRootFile` in its original position.
  - Then the `--bundle`, `--receipt` and `--attestation-bundle` `requiredOption`s, in the object literal's order.
  - It prints the result and exits `valid ? 0 : 1`.
- **Default:** `Usage: fl provenance create|verify ...`.
- **Harness.**
  - Every case is `posixOnly`, because of the fake `gh` and git.
  - The CI env is set explicitly: `GITHUB_ACTIONS=true`, `GITHUB_SHA=<FILE:sha.txt>`, `GITHUB_REPOSITORY=Mizore66/faultline`, `GITHUB_WORKFLOW_REF=Mizore66/faultline/.github/workflows/go.yml@refs/heads/main`, `GITHUB_REF=refs/heads/main`, `GITHUB_RUN_ID=123`, `GITHUB_RUN_ATTEMPT=1`, `GITHUB_EVENT_NAME=push`.
  - The `rev-parse` git call is not hardened, so the recorder logs it and the harness's `GIT_CONFIG_GLOBAL` applies to both sides alike.

**Writer cases:**
- `provenance-create-ok` (TS cross-verifies the receipt)
- `provenance-create-local` (no `GITHUB_ACTIONS`)
- `provenance-create-missing-sha`
- `provenance-create-bad-sha` (ZodError)
- `provenance-create-unknown-sha` (rev-parse fails: `Command failed: …`)
- `provenance-create-attempt-zero`
- `provenance-create-event-pr` (`pull_request`, ZodError)
- `provenance-create-padded-repo`
- `provenance-create-output-outside`
- `provenance-create-twice`
- `provenance-create-root-symlink`
- `provenance-create-bad-bundle`
- `provenance-create-no-bundle`
- `provenance-verify-ok` (fake gh `pass`)
- `provenance-verify-gh-fail`
- `provenance-verify-gh-long-fail`
- `provenance-verify-no-gh` (`pathTools: ["git", "sh", "cat"]`, no gh)
- `provenance-verify-deny-self-hosted`
- `provenance-verify-trust-relative-root`
- `provenance-verify-missing-root`
- `provenance-verify-wrong-ref`
- `provenance-verify-wrong-workflow`
- `provenance-verify-source-digest-mismatch`
- `provenance-verify-tampered-receipt`
- `provenance-verify-receipt-symlink`
- `provenance-verify-receipt-too-big`
- `provenance-verify-receipt-not-json`
- `provenance-verify-bad-trust`
- `provenance-verify-no-trust`
- `provenance-unknown-action`

- [ ] **Step 1: Add the scenarios and cases; generate the goldens (podman, uid 1000, `FAULTLINE_WRITERS_ONLY="^provenance-"`); see the replay fail with `not yet ported: provenance`**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/provenance-' > /tmp/s4c-t17.log 2>&1; grep -c 'not yet ported: provenance' /tmp/s4c-t17.log`
Expected: a count above 0.

- [ ] **Step 2: Unit test** the trust-root resolution and the `verify` option order:

```go
// internal/cli/provenance_test.go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceVerifyReadsTrustFirst(t *testing.T) {
	dir := t.TempDir()
	trust := filepath.Join(dir, "trust.json")
	os.WriteFile(trust, []byte(`{"schemaVersion":"faultline.github-artifact-attestation-trust.v1"}`), 0o644)
	var out, errb bytes.Buffer
	code := Run([]string{"provenance", "verify", "--trust", trust}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), `"path": [`) || strings.Contains(errb.String(), "Missing required option: --bundle") {
		t.Fatalf("%d %q", code, errb.String())
	}
}
```

- [ ] **Step 3: Implement**, and add the dispatch line.

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/provenance-|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4c-t17.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t17.log | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl provenance create and verify with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 18: `fl repair brief` (`--input`, `--live`) and `fl repair verify`

**Files:**
- Create: `internal/cli/repair.go` (the `verify` and `brief` branches, plus a stub that keeps `--bundle` as `not yet ported: repair --bundle` until Task 20), `internal/cli/repair_test.go`.
- Modify:
  - `internal/cli/cli.go`: a `case command == "repair":` line, appended.
  - `difftest/gen/scenarios.ts`: scenario `repair-brief`, appended. Its base is `bundle: git-unbound`. Its files:
    - `brief.json`: a valid brief citing E1–E3, whose `evidencePacketDigest` the scenario computes with TS's `createRepairEvidencePacket` on the base's investigation;
    - `brief-unknown-id.json`, `brief-wrong-digest.json`, `brief-secret.json` (a `GENERIC_SECRET_ASSIGNMENT` `password=…` value in a statement, built from pieces);
    - `reply-ok.json`, `reply-wrong-ids.json`, `reply-500.json`, `reply-not-json.json`, `reply-two-outputs.json` (the fake OpenAI replies);
  - `difftest/testdata/writers.json`, `golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt` (appended).

**Interfaces:**
- Consumes: Tasks 6, 9 and 10; `proofview.LoadVerifiedGitProofView`; `redaction.RedactValue`; `readJSONInput`.
- Produces: `func repairCommand(e *env, args []string) error`, with the `verify` and `brief` branches and a `--bundle` branch that Task 20 fills.

**Gotchas:**
- **Branch order** (`cli-app.ts:2672-2690`):
  1. `args[0] === "verify"`. Without a directory: `Usage: fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]`.
  2. `hasFlag("--bundle") && args[0] !== "brief"`: the Codex path (Task 20).
  3. `args[0] !== "brief"`: `Usage: fl repair --bundle <dir> --expect-root <digest> [--with-codex] | fl repair brief|verify ...`.
- **`brief` order** (`:2736-2801`):
  1. `live = hasFlag("--live")`; `input = option("--input")`.
  2. `live === Boolean(input)`, so an empty `--input ""` counts as absent: `Choose exactly one repair brief source: --live or --input <repair-brief.json>.`
  3. `bundle` and `investigation` via `option()`, with the same truthiness rule: `Choose exactly one proof source: --bundle <directory> or --investigation <verified-bundle>/investigation.json.`
  4. `bundleDirectory`: `dirname(resolve(investigation))` or `resolve(bundle)`.
  5. `loadVerifiedGitProofView(dir, option("--expect-root"))`. Its throws reach the CLI.
  6. For `--investigation`: `resolve(investigationFile) !== resolve(dir, manifest.artifacts.investigation)` gives `--investigation must identify the investigation.json artifact in the verified Git proof bundle.`
  7. `createRepairEvidencePacket(view.investigation)`.
  8. `redactValue`, then parse. A digest change gives the text at `:2770`, which cannot be reached: the packet carries no secret shapes, so mark it with a TS-cited comment and add it to `unreachableLiterals`.
  9. `model = option("--model") ?? "gpt-5.6"`.
  10. The candidate: `ProposeBrief`, or `readJsonInput(input)` with the text `Unable to read JSON input <file>: …`.
  11. `redactValue(candidate)`, then `validateRepairBrief(packet, redacted)`. Failure: `Repair brief was rejected: …`.
  12. The output: `option("--output") ?? join(defaultRepairBriefRoot(), "repair-" + packetDigest.slice(7, 23) + "-" + Date.now())`. `Date.now()` is read only when `--output` is absent, after the brief is validated.
  13. Write with source `{kind: "GPT-5.6", model}` or `{kind: "OFFLINE_INPUT"}`.
  14. Stdout keys: `status, classification, source, directory, artifactDigest, evidencePacketDigest, repairBriefDigest, privacy{packet, candidate}, limitation`. Exit 0.
- **The redaction report.** `privacy.candidate` shows the redaction report of the offline brief. A brief with a `password=…` statement gets a placeholder that is stored in the package, which is the `brief-secret` case. Its secret is built in the scenario from pieces (TruffleHog).
- **`--live` without a key.** `OPENAI_API_KEY` is dropped by `inheritedOut`. The case without a key leaves it unset, and every other `--live` case sets `env: {"OPENAI_API_KEY": "fl-test-key"}` and a `fakeOpenAI` reply.
- **`verify`.** `verifyRepairBriefArtifact(resolve(dir), option("--expect-digest"))`. It prints and exits `valid ? 0 : 1`. It has no cwd-root check.
- **Posix-only cases.** The brief cases run the Git verifier, so they are `posixOnly`. The `verify` cases on a committed package are cross-platform: build the package in the scenario.
- **The packet refusal cannot be reached through the CLI.** Every Git bundle the verifier accepts is a completed, Docker-isolated proof with at least one transition and a witness digest. So the `A post-localization repair brief requires …` text is reached only by `TestLiveRepairEvidencePacket`, and `internal/repair/brief.go` goes into `liveGatedFiles` under that test.

**Writer cases** (scenario `repair-brief`):
- `--input`:
  - `repair-brief-input-ok` (TS cross-verifies the package)
  - `repair-brief-input-investigation`
  - `repair-brief-input-investigation-wrong-path`
  - `repair-brief-input-expect-root-ok`, `-bad`
  - `repair-brief-input-unknown-id`, `-wrong-digest`, `-secret`
  - `repair-brief-input-bad-json`, `-missing-file`, `-empty` (`--input ""`: neither source)
  - `repair-brief-input-output` (custom output inside the root), `-output-outside`, `-output-exists`, `-root-symlink`
  - `repair-brief-tampered-bundle`
- Usage errors:
  - `repair-brief-both-sources`, `repair-brief-neither-source`
  - `repair-brief-both-proofs`, `repair-brief-neither-proof`
- `--live`:
  - `repair-brief-live-ok` (fake OpenAI `reply-ok`; the request body is recorded)
  - `repair-brief-live-model` (`--model gpt-test`)
  - `repair-brief-live-no-key`
  - `repair-brief-live-wrong-ids`, `-500`, `-not-json`, `-two-outputs`
- `verify`:
  - `repair-verify-ok` (brief, then verify with `<STDOUT:0:/artifactDigest>`), `-no-expect`, `-mismatch`
  - `repair-verify-missing`, `-extra-file`, `-tampered-brief`, `-legacy-packet`, `-symlink-file`
  - `repair-verify-no-dir`
- `repair-unknown` (`fl repair foo`)

- [ ] **Step 1: Add the scenario and cases; generate the goldens (podman, uid 1000, `FAULTLINE_WRITERS_ONLY="^repair-(brief|verify|unknown)"`); see the replay fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?repair-(brief|verify|unknown)' > /tmp/s4c-t18.log 2>&1; grep -c 'not yet ported: repair' /tmp/s4c-t18.log`
Expected: a count above 0.

- [ ] **Step 2: Unit test** the source rules:

```go
// internal/cli/repair_test.go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepairBriefSourceRules(t *testing.T) {
	cases := map[string][]string{
		"Choose exactly one repair brief source: --live or --input <repair-brief.json>.": {"repair", "brief", "--input", ""},
		"Choose exactly one proof source: --bundle <directory> or --investigation <verified-bundle>/investigation.json.": {"repair", "brief", "--live"},
		"Usage: fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]":                         {"repair", "verify"},
	}
	for want, args := range cases {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 1 || !strings.HasPrefix(errb.String(), "FaultLine error: "+want+"\n") {
			t.Errorf("%v: %d %q", args, code, errb.String())
		}
	}
}
```

- [ ] **Step 3: Implement**, and add the dispatch line.

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?repair-(brief|verify|unknown)|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4c-t18.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t18.log | tail -20`
Expected: PASS. The `--live` cases' `openai` records (request body included) match TS byte for byte.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl repair brief and fl repair verify with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 19: `fl witness implement`

**Files:**
- Modify:
  - `internal/cli/witness.go`: an `implement` case. `review` keeps `not yet ported: witness review`.
  - `internal/cli/witness_test.go`.
  - `difftest/gen/scenarios.ts`: scenario `witness-implement`, appended. Its files: `incident.json` (a valid packet), `incident-bad.json` (missing `ciLog`), `incident-extra.json` (an unknown key) and `not-json.json`.
  - `difftest/testdata/writers.json`, `golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt` (appended).

**Interfaces:**
- Consumes: Task 8 (`codexloop.Implement`) and Task 12 (`codexloop.CLIRunner`).
- Produces: the `implement` branch of `witnessCommand`.

**Gotchas:**
- **Order** (`cli-app.ts:1169-1203`):
  1. `witnessStore(args)` is computed first: the existing code already does this.
  2. `requiredOption("--incident")`, resolved, read and parsed with raw `readFileSync` and `JSON.parse`. The errors are Node's raw ENOENT or V8's SyntaxError text, not `readJSONInput`'s.
  3. Then, in the object literal's order: `requiredOption("--proposal-id")`, `option("--behavior") ?? "The human-reviewed predicate must hold."`, `resolve(requiredOption("--overlay-out"))`, `resolve(option("--repo") ?? cwd)`.
  4. The runner is present only with `--with-codex`. It is `CLIRunner(hasFlag("--allow-codex-full-auto"), "Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.", 120*time.Second)`.
  5. Exit 0 for `OVERLAY_DRAFTED` or `TEMPLATE_ONLY`, else 1.
- **`--repo`** is resolved but unused by TS. Resolve it anyway: `resolve` of a cwd failure throws.
- **The fakes and the argv log.**
  - A case with `--with-codex` and the allow flag records the `codex --version` probe and then the `codex exec --full-auto <prompt>` record (with STDIN, PATH and ENV).
  - The ENV list must be empty on both sides: only PATH is passed.
  - Without the allow flag, there is no `codex` record at all.

**Writer cases** (scenario `witness-implement`; the Codex ones are `posixOnly`):
- `witness-implement-template` (cross-platform)
- `witness-implement-default-behavior`
- `witness-implement-behavior-newlines` (`--behavior` with `\n` and a 250-unit astral string: the slice)
- `witness-implement-overwrite` (two invocations: the files are overwritten)
- `witness-implement-bad-incident`, `-extra-key`, `-not-json`, `-missing-incident-file`
- `witness-implement-no-proposal-id`, `-no-overlay-out`
- `witness-implement-codex-no-allow` (fakeCodex `edits`: no codex record)
- `witness-implement-codex-ok` (fakeCodex `edits`: `witness.mjs` rewritten, `OVERLAY_DRAFTED`)
- `witness-implement-codex-noop`
- `witness-implement-codex-exit-nonzero`
- `witness-implement-codex-killed` (`exited null`)
- `witness-implement-codex-probe-fail`, `-probe-empty`, `-probe-crlf`
- `witness-implement-codex-missing-bin` (`FAULTLINE_CODEX_BIN=<HOME>/nope`)
- `witness-implement-codex-on-path` (`fakeCodexOnPath`)
- `witness-implement-codex-not-on-path` (`pathTools: ["sh", "cat"]`: the bare `codex` gives ENOENT)
- `witness-implement-overlay-out-file` (mutation: `--overlay-out` names a file, so mkdir fails with Node's raw text)

- [ ] **Step 1: Add the scenario and cases; generate the goldens (podman, uid 1000, `FAULTLINE_WRITERS_ONLY="^witness-implement-"`); see the replay fail with `not yet ported: witness implement`**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?witness-implement-' > /tmp/s4c-t19.log 2>&1; grep -c 'not yet ported: witness implement' /tmp/s4c-t19.log`
Expected: a count above 0.

- [ ] **Step 2: Update `TestWitnessProposeSampleAndUnported`**, which pins `not yet ported: witness implement`. Only `review` stays unported, so change the test to that.

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?witness-implement-|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' > /tmp/s4c-t19.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t19.log | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl witness implement with Codex drafting and writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 20: `fl repair --bundle`

**Files:**
- Modify:
  - `internal/cli/repair.go`: the `--bundle` branch replaces Task 18's stub.
  - `internal/cli/repair_test.go`.
  - `difftest/gen/scenarios.ts`: scenarios `repair-repo` and `repair-heterogeneous`, appended.
    - `repair-repo`: the `git-unbound` repository built by `gitScenario(GOOD_BAD_REPAIRED)`, with `repo/.git/config` gaining `[user] name = FaultLine Test, email = faultline@example.test` and `[core] logAllRefUpdates = false` (corrections 2 and 3), and the frozen witness.
    - `repair-heterogeneous`: the `git-heterogeneous` repository with the same config.
  - `difftest/testdata/writers.json`, `golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt` (appended).

**Interfaces:**
- Consumes: Task 16 (`RepairWithCodex`, `FrozenWitnessVerifier`), Task 12 (`CLIRunner`), `gitproof.Verify`, `sandbox.NativeRunner()`.
- Produces: the `--bundle` branch of `repairCommand`.

**Gotchas:**
- **Options** (`cli-app.ts:2687-2730`), in the literal's evaluation order:
  - `requiredOption("--bundle")`, resolved;
  - `requiredOption("--expect-root")`;
  - `resolve(option("--repo") ?? cwd)`;
  - `resolve(option("--output") ?? join(".faultline", "repairs", "repair-" + Date.now()))`, with `Date.now()` read only without `--output`;
  - `withCodex`, `--instructions-only`, `--keep-worktree`.
- **`verifyBundle`** is `gitproof.Verify(dir, expectRoot, expectRoot !== undefined)`. The CLI always passes a string, so the root is always provided.
- **With Codex:**
  - `verifyCandidate` is `FrozenWitnessVerifier(sandbox.NativeRunner(), false)`.
  - The runner is `CLIRunner(hasFlag("--allow-codex-full-auto"), "Codex repair drafting requires …", 180*time.Second)`.
  - Without `--with-codex`, neither is set.
- **Exit codes:** 1 for `BUNDLE_INVALID`, `REPAIR_SETUP_FAILED`, `REPAIR_VERIFICATION_FAILED` and `WORKTREE_CLEANUP_FAILED`. 0 for every other status, `CODEX_UNAVAILABLE` included (spec 3.5).
- **`--bundle` beats the `brief` check.** `fl repair --bundle x verify` takes the `verify` branch first (`args[0]`). `fl repair --bundle x` with `args[0] = "--bundle"` takes the Codex branch.
- **Harness.**
  - Every case is `posixOnly`, because of git, the fake Docker and the fake codex.
  - The successful flows run `investigate git --repo repo --from HEAD~2 --to HEAD --proposal demo-1 --image <PINNED>` first, then `repair --bundle <STDOUT:0:/proofBundle/directory> --expect-root <STDOUT:0:/proofBundle/rootDigest> --repo repo …`, with fakeDocker `pass-fail`.
  - The repaired worktree's `state.txt` holds `good` after fake codex `edits`, so the verifier passes three times and the export writes `PREVENTION_VERIFIED` under `<WS>/.faultline/prevention-proofs/from-repair-<ms>`.
  - TS cross-verifies that package (Task 1).
  - Task 2's masks name the created README blob, the tree, the commit, the `exec:` and `run:` digests.

**Writer cases** (scenario `repair-repo` unless named):
- `repair-bundle-invalid` (`git-unbound` base with a wrong `--expect-root`)
- `repair-bundle-committed-base` (the committed `git-unbound` base: `REPAIR_SETUP_FAILED`, because the recorded repository is missing)
- `repair-bundle-no-expect-root`, `repair-bundle-no-bundle-value`
- `repair-bundle-instructions-only` (one invocation after investigate)
- `repair-bundle-worktree-ready` (no Codex: worktree created and removed)
- `repair-bundle-keep-worktree`
- `repair-bundle-output` (custom), `-output-exists`, `-output-parent-file`
- `repair-bundle-codex-no-allow` (`CODEX_UNAVAILABLE`, transport `NONE`, exit 0)
- `repair-bundle-codex-ok` (the full success: patch, verification, export, `AGENTS.md` in the cwd repo, cleanup)
- `repair-bundle-codex-ok-keep` (`--keep-worktree`: the worktree, its index and the created objects remain)
- `repair-bundle-codex-noop` (an empty patch; the verifier fails, because `state.txt` is still `bad`: `REPAIR_VERIFICATION_FAILED`, exit 1)
- `repair-bundle-codex-exit-nonzero` (`CODEX_UNAVAILABLE`, `codex-thread.json`)
- `repair-bundle-codex-killed` (`exited null`)
- `repair-bundle-codex-flood` (the preview slices)
- `repair-bundle-codex-probe-fail`
- `repair-bundle-codex-outside-edits` (`outside.txt` in the managed root)
- `repair-bundle-codex-symlink-edit`:
  - The fake Docker's `cat state.txt` follows the symlink to `good.txt` (`good`), so all four verifier runs pass.
  - `commitRepairedWorktreeState` skips the symlink (`isFile()` is false) and force-removes the tracked `state.txt` from the temporary index.
  - So the repaired tree has `good.txt` and no `state.txt`.
- `repair-bundle-codex-daemon-down` (fakeDocker `daemon-down` on the repair invocation only: the verifier fails)
- `repair-bundle-codex-heterogeneous` (scenario `repair-heterogeneous`, fakeDocker `pass-fail`: the export fails closed, with the homogeneous-environment reason)
- `repair-bundle-codex-prevention-root-file` (mutation: `.faultline/prevention-proofs` is a file, so the export write fails closed)

A repository config without `[user]` has no golden: git's error names the machine (correction 2). `TestLiveCommitRepairedWorktreeState` covers it on one machine.

- [ ] **Step 1: Add the scenarios and cases; generate the goldens (podman, uid 1000, `FAULTLINE_WRITERS_ONLY="^repair-bundle-"`); see the replay fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/repair-bundle-' > /tmp/s4c-t20.log 2>&1; grep -c 'not yet ported: repair --bundle' /tmp/s4c-t20.log`
Expected: a count above 0.

- [ ] **Step 2: Unit test** the exit-code table:

```go
// internal/cli/repair_test.go (added)
func TestRepairExitCodes(t *testing.T) {
	for status, want := range map[string]int{
		"BUNDLE_INVALID": 1, "REPAIR_SETUP_FAILED": 1, "REPAIR_VERIFICATION_FAILED": 1, "WORKTREE_CLEANUP_FAILED": 1,
		"CODEX_UNAVAILABLE": 0, "REPAIR_INSTRUCTIONS_PREPARED": 0, "REPAIR_WORKTREE_READY": 0, "CODEX_DRAFT_RECORDED": 0, "REPAIR_CANDIDATE_VERIFIED": 0,
	} {
		if got := repairExitCode(status); got != want {
			t.Errorf("%s: %d", status, got)
		}
	}
}
```

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && timeout 280 go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/repair-bundle-|TestWindowsWriterSkipsList' > /tmp/s4c-t20.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t20.log | tail -30`
Expected: PASS. Every repair-written prevention package verifies under TS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl repair --bundle with Codex drafting, verification and prevention export

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 21: Fault matrix, end to end without Node, CI, coverage gate, KNOWN_DIFFERENCES, docs and the full regeneration

**Files:**
- Modify:
  - Tests:
    - `difftest/writerfault_live_test.go`: the 4c writer paths, appended to `writerFaultPaths`.
    - `difftest/endtoend_test.go`: `TestEndToEndSlice4cWithoutNode`, appended.
  - CI: `.github/workflows/go.yml`, `.github/scripts/check-skips.sh` (only if a new allowed skip is needed: expected none).
  - Gates:
    - `difftest/coverage_test.go`: add `internal/codexloop`, `internal/repair`, `internal/attest`, `internal/provenance` and `internal/jsdate` to `gatedDirs`. Add the `liveGatedFiles` entries the tasks named: `internal/bundle/prevention/hardguard.go`, `internal/repair/brief.go`, `internal/repair/response.go` (`TestLiveExtractOpenAiResponseText`), `internal/nodefs/cp.go`. Add the `unreachableLiterals` the tasks named, each with its reason.
    - `difftest/covergate_test.go`: the `not yet ported: %s` waiver text drops `attest`, `provenance` and `repair`.
  - Docs:
    - `difftest/KNOWN_DIFFERENCES.md`;
    - the slice 4 spec (this plan's corrections, folded into Sections 2.2, 2.3, 2.6, 3.2 and 3.5);
    - `docs/superpowers/specs/2026-09-25-go-migration-overview.md` (PR 4c done; `captureAllowlistedEvent` and `prevention-from-turn` recorded as not ported, if 4a/4b have not done it);
    - this plan (tick the boxes).

**Interfaces:**
- Consumes: every earlier task.

**Fault matrix** (spec 2.6). The paths are relative to the case root. Renames are faulted through their source path (slice 3a CI: on x86_64, `strace -P` matches only `rename(2)`'s first argument). A group that never fires in the fault-free discovery run is dropped, not faked.
- The attestation link:
  - `{"attest-create-git", "attest-store-root", "ws/.faultline/attestations", ["mkdir"]}`
  - `{"attest-create-git", "attest-staged", "ws/.faultline/attestations/.attestation-*.tmp", ["open", "write", "fsync", "close", "link", "unlink"]}`
- The provenance receipt:
  - `{"provenance-create-ok", "receipt", "ws/.faultline/provenance/ci-receipt.json", ["open", "write", "close"]}`
  - `{"provenance-create-ok", "provenance-root", "ws/.faultline/provenance", ["mkdir"]}`
- The repair brief stage:
  - `{"repair-brief-input-ok", "brief-stage", "ws/.faultline/repair-briefs/.repair-*.tmp", ["mkdir", "rename"]}`
  - `{"repair-brief-input-ok", "brief-manifest", "ws/.faultline/repair-briefs/.repair-*.tmp/manifest.json", ["open", "write", "fsync", "close"]}`
  - `{"repair-brief-input-ok", "brief-root", "ws/.faultline/repair-briefs", ["mkdir"]}`
- `witness implement`'s two overlay files:
  - `{"witness-implement-template", "overlay", "ws/overlay/witness.mjs", ["open", "write", "close"]}`
  - `{"witness-implement-template", "overlay-readme", "ws/overlay/README.faultline.txt", ["open", "write", "close"]}`
- The repair managed root and artifacts:
  - `{"repair-bundle-codex-ok", "managed-root", "ws/.faultline/repairs/repair-*", ["mkdir"]}`
  - `{"repair-bundle-codex-ok", "repair-meta", "ws/.faultline/repairs/repair-*/artifacts/repair-meta.json", ["open", "write", "close"]}`
  - `{"repair-bundle-codex-ok", "repair-patch", "ws/.faultline/repairs/repair-*/artifacts/repair.patch", ["open", "write", "close"]}`
  - `{"repair-bundle-codex-ok", "repaired-index", "tmp/faultline-repair-*.index", ["unlink"]}`
  - `{"repair-bundle-codex-ok", "prevention-stage", "ws/.faultline/prevention-proofs/.from-repair-*.tmp", ["mkdir", "rename"]}`
  - `{"repair-bundle-codex-ok", "prevention-export", "ws/.faultline/repairs/repair-*/artifacts/prevention-export.json", ["open", "write", "close"]}`
- `repair-bundle-codex-ok` is a two-invocation case (investigate, then repair). Add it to `faultFirstInvocation`'s opposite: a new `faultLastInvocation` map, so only the repair invocation is faulted.
- Use errnos `ENOSPC`, `EIO` and `EACCES`, as before.

**End to end without Node** (spec 2.6, PR 4c, as reordered by correction 4): `TestEndToEndSlice4cWithoutNode`. PATH comes from `nodelessPath(t, "pass-fail", "cat", "sh")`, plus the `edits` fake codex through `FAULTLINE_CODEX_BIN`. The workspace is the `repair-repo` scenario. The steps:
1. `investigate git --repo repo --from HEAD~2 --to HEAD --proposal demo-1 --image <PINNED>`: `proofBundle.directory` and `rootDigest`.
2. `repair --bundle <dir> --expect-root <root> --repo repo --with-codex --allow-codex-full-auto`: exit 0, status `REPAIR_CANDIDATE_VERIFIED`, `preventionExport.ok`, `classification: "PREVENTION_VERIFIED"`.
3. `verify <preventionExport.directory> --expect-root <preventionExport.rootDigest>`: `Prevention verified`.
4. `repair brief --bundle <dir> --expect-root <root> --input brief.json`, then `repair verify <directory> --expect-digest <artifactDigest>`: `"valid": true`.
   - The brief must cite this bundle's packet. The investigation digest covers the repository path, so a committed brief would not match.
   - The test writes `brief.json` at run time, from `repair.CreateEvidencePacket` of the bundle's `investigation.json`.
5. `prevention write --from-bundle <dir> --expect-root <root> --repaired-runs <pkg>/repaired-runs.json --repaired-commit <prevention.json repaired.commit> --repaired-tree <… tree> --patch <managedRoot>/artifacts/repair.patch --repo repo`: `PREVENTION_VERIFIED`.
6. `attest create --bundle <dir> --receipt e2e-1 --subject repo --issuer ci`, then `attest verify e2e-1 --expect-digest <receiptDigest>`: exit 0, `MATCH`.

```go
// difftest/endtoend_test.go (appended)
func TestEndToEndSlice4cWithoutNode(t *testing.T) {
	ws := scenarioWorkspace(t, "repair-repo")
	codex := installFakeCodex(t, repoRoot(t), rootsOf(ws), "edits")
	env := map[string]string{"PATH": nodelessPath(t, "pass-fail", "cat", "sh"), "FAULTLINE_CODEX_BIN": codex}
	step := func(want int, args ...string) string {
		t.Helper()
		stdout, stderr, code := runFlIn(t, ws, env, args...)
		if code != want {
			t.Fatalf("fl %s: exit %d, want %d\n%s%s", strings.Join(args, " "), code, want, stdout, stderr)
		}
		return stdout
	}
	out := step(0, "investigate", "git", "--repo", "repo", "--from", "HEAD~2", "--to", "HEAD", "--proposal", "demo-1", "--image", pinnedImage)
	bundle, root := jsonField(out, "proofBundle", "directory"), jsonField(out, "proofBundle", "rootDigest")

	out = step(0, "repair", "--bundle", bundle, "--expect-root", root, "--repo", "repo", "--with-codex", "--allow-codex-full-auto")
	if jsonField(out, "status") != "REPAIR_CANDIDATE_VERIFIED" || jsonField(out, "preventionExport", "classification") != "PREVENTION_VERIFIED" {
		t.Fatalf("repair:\n%s", out)
	}
	pkg, pkgRoot, managed := jsonField(out, "preventionExport", "directory"), jsonField(out, "preventionExport", "rootDigest"), jsonField(out, "managedRoot")
	if v := step(0, "verify", pkg, "--expect-root", pkgRoot); !strings.Contains(v, "\nPrevention verified\n") {
		t.Fatalf("verify package:\n%s", v)
	}

	invText, err := os.ReadFile(filepath.Join(bundle, "investigation.json"))
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := jsjson.Parse(string(invText))
	packet, err := repair.CreateEvidencePacket(inv)
	if err != nil {
		t.Fatal(err)
	}
	brief := `{"schemaVersion":"faultline.repair-brief.v1","evidencePacketDigest":"` + packet.Get("packetDigest").Str() + `",` +
		`"proposedInvariant":{"statement":"state.txt stays good","evidenceIds":["E1"]},"repairDirections":[{"statement":"restore good","evidenceIds":["E2"]}],` +
		`"prevention":{"hardEnforcement":[{"statement":"add a CI check","evidenceIds":["E3"]}],"softGuidance":[]},"uncertainties":["inferred"]}`
	if err := os.WriteFile(filepath.Join(ws, "brief.json"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	out = step(0, "repair", "brief", "--bundle", bundle, "--expect-root", root, "--input", "brief.json")
	if v := step(0, "repair", "verify", jsonField(out, "directory"), "--expect-digest", jsonField(out, "artifactDigest")); !strings.Contains(v, "\"valid\": true") {
		t.Fatalf("repair verify:\n%s", v)
	}

	body, err := os.ReadFile(filepath.Join(pkg, "prevention.json"))
	if err != nil {
		t.Fatal(err)
	}
	commit, tree := jsonField(string(body), "repaired", "commit"), jsonField(string(body), "repaired", "tree")
	out = step(0, "prevention", "write", "--from-bundle", bundle, "--expect-root", root, "--repaired-runs", filepath.Join(pkg, "repaired-runs.json"),
		"--repaired-commit", commit, "--repaired-tree", tree, "--patch", filepath.Join(managed, "artifacts", "repair.patch"), "--repo", "repo")
	if !strings.HasSuffix(out, "\nPrevention verified\n") {
		t.Fatalf("prevention write:\n%s", out)
	}

	out = step(0, "attest", "create", "--bundle", bundle, "--receipt", "e2e-1", "--subject", "repo", "--issuer", "ci")
	if v := step(0, "attest", "verify", "e2e-1", "--expect-digest", jsonField(out, "receiptDigest")); jsonField(v, "externalDigestStatus") != "MATCH" {
		t.Fatalf("attest verify:\n%s", v)
	}
}
```

(`installFakeCodex` is Task 1's helper; `pinnedImage`, `runFlIn`, `jsonField` and `rootsOf` exist already. Add the import `github.com/Mizore66/faultline/internal/repair`.)

**CI** (spec 4.1):
- **`go`:**
  - The 4c writer cases need no change. Windows skips only the `posixOnly` cases listed in `windows-writer-skips.txt`, which `TestWindowsWriterSkipsList` enforces.
  - Add the release-binary step from Task 10: the untagged `fl` holds no `FAULTLINE_TEST_OPENAI_URL`.
- **`difftest-goldens`:** no change. It already regenerates scenarios and writers. Confirm that the fetch shim is loaded by `runCli`, not by a CI flag.
- **`difftest-live`:** the new `TestLive*` tests enter through the `-list 'TestLive'` derivation. Measure their total runtime locally and add a comment line listing them, as slices 3a and 3b did. If the total pushes the job past about 75 of its 90 minutes, move `TestLiveRepairWithCodex` and `TestLiveRepairWorktree` to `difftest-live-writers` and add them to its `elsewhere` list.
- **`difftest-live-faults`:** no workflow change. The matrix grows. Check the 60-minute `go test` timeout against a measured run of the new paths.
- **`difftest-live-replay`:** add `TestEndToEndSlice4cWithoutNode` to its `tests=` list.
- **`stack-live`:** add `TestLiveResolveCodexRunner` and `TestLiveCpSyncTree`. Both are platform-sensitive: `existsSync` on macOS, and `cpSync` modes and the `/private/var` realpath.

**KNOWN_DIFFERENCES** (spec 3.6):
- **Created Git object masks:** the `<COMMIT:…>`, `<TREE:…>` and `<BLOB:…>` rule, the loose-object and reflog rules, and the `exec:` and `run:` seeds (correction 1). The controller dedupes this with 4a/4b's `<COMMIT>` entry at merge.
- **Live repair brief HTTP:** TLS roots, the undici default headers, and proxy handling (correction 12).
- **Codex timeouts** are compared live, not as goldens (correction 9).
- **Identity-less `commit-tree`:** git's error names the machine (correction 2).
- **Not ported:** `captureAllowlistedEvent` and `prevention-from-turn`, only if 4a or 4b has not added it. The controller dedupes.

- [ ] **Step 1: Add the fault paths and the end-to-end test; run them**

Run: `go test -tags faultline_test ./difftest/ -run TestEndToEndSlice4cWithoutNode -v > /tmp/s4c-t21-e2e.log 2>&1; tail -5 /tmp/s4c-t21-e2e.log`
Expected: PASS.

Then the fault subset in podman as uid 1000:

```bash
podman run --rm --userns=keep-id -u 1000 --cap-add SYS_PTRACE -v "$PWD":/w -w /w localhost/r73img sh -c 'pnpm build && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run "TestLiveWriterFaults/(attest|provenance|repair|witness-implement)" -count=1' > /tmp/s4c-t21-faults.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s4c-t21-faults.log | tail -40
```

Expected: PASS. A mismatch is fixed in Go. A group that injects nothing is removed from its path's list, with a comment.

- [ ] **Step 2: CI, the gates and the docs.** Then run the full gated local check in the background with polled logs (the command is in 3a Task 13's report):

```bash
FAULTLINE_COVERAGE_GATE=1 go test -tags faultline_test ./difftest/ -count=1 -timeout 120m > /tmp/s4c-t21-gate.log 2>&1 &
# poll: tail -3 /tmp/s4c-t21-gate.log every few minutes; each poll is one short command
```

Expected: `ok`. A missed error site gets a writer case or a mutation template that reaches it, or an `unreachableLiterals` entry with its reason.

- [ ] **Step 3: Full regeneration with no diff.** In podman as uid 1000, from a clean tree, regenerate everything with no `FAULTLINE_WRITERS_ONLY`:

```bash
git status --porcelain   # must be empty before
podman run --rm --userns=keep-id -u 1000 -v "$PWD":/w -w /w localhost/r73img sh -c 'pnpm install --frozen-lockfile && pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts && pnpm exec tsx difftest/gen/gen-corpus.ts' > /tmp/s4c-t21-regen.log 2>&1 &
# poll the log (about 45 minutes); then:
git status --porcelain difftest/testdata
```

Expected: empty output. Every golden the tasks committed (each made with a `FAULTLINE_WRITERS_ONLY` filter) equals what a full run produces. Then run the full replay once on the result: `go test -tags faultline_test ./difftest/ -run TestWriterReplay -count=1 -timeout 90m`, in the background with polled logs. Expected: `ok`.

- [ ] **Step 4: Commit; hand back to the controller to push** (the controller pushes as Mizore66 and reads CI)

```bash
git add .github difftest docs
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "ci(go): slice 4c faults, end to end, CI, coverage gate and docs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Done criteria (spec 4.2, PR 4c):
1. `go test ./...` passes on all six runners, and every 4c writer case matches its golden.
2. `difftest-live`, `difftest-live-faults` and `stack-live` pass.
3. `TestEndToEndSlice4cWithoutNode` passes.
4. Every attestation, provenance receipt, repair brief and prevention package Go writes verifies under TS (the cross-checks of Task 1).
5. Point 5 (the hostile-verifier corpus) belongs to PR 4b.
6. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference above.
7. The overview marks PR 4c done.
8. `ArtifactOptions.RepairBaseTree` and `HardGuardArtifactDigests`, and the `incident.DraftInput` cleanup, are in (Task 5).

Adversarial review rounds of about 90 minutes each follow until a round finds no P0 or P1.
