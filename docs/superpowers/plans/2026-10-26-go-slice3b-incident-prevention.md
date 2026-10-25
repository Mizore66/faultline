# Go Slice 3b (incident, prevention) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs `incident suggest|start|status|continue` and `prevention verify|write` (`--input` and `--from-bundle`). For each, stdout, stderr, exit status, the files it writes (with their modes) and the git and Docker argv are byte-identical to frozen TS `node dist/cli.js`. Writer goldens and live Node-oracle tests prove it.

**Architecture:** Each TS module becomes one Go file:
- `internal/incident`: intake (`suggest`), draft schemas and creation, draft store, and continuation.
- `internal/bundle/prevention`: gains the writer, the `--from-bundle` builder and the `AGENTS.md` upsert, next to slice 1's verifier.

`continue` reuses slice 2's investigation and Git proof bundle writer exactly as `fl investigate git` calls them (`internal/cli/investigate.go:105-263`). The slice 3a writer harness is extended with no new mechanisms. It gets:
- incident scenarios (upstream state, root/merge HEAD);
- argv chains (`start` → `witness approve` → `freeze` → `status` / `continue`);
- a `--from-bundle` scenario built on slice 2's `git-unbound` bundle base.

**Tech Stack:** Go 1.27 (standard library only), TypeScript generators run with the repo's `tsx`, Node 22.22.2, and git. The only Docker involved is the fake Dockers.

**Spec:** [docs/superpowers/specs/2026-10-20-go-slice3-runtime-design.md](../specs/2026-10-20-go-slice3-runtime-design.md) (PR 3b scope). Read it before any task, together with the [slice 3a plan](2026-10-20-go-slice3a-runtime-doctor-init.md) and the [slice 2 spec](../specs/2026-10-14-go-slice2-git-path-design.md). This plan reuses the slice 1–3a packages and helpers by name.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- **JSON.**
  - Never use `encoding/json` on a value that is hashed, compared canonically, written or printed.
  - `JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`.
  - `canonicalJson` is `canonical.CanonicalJSON` and `digestJson` is `canonical.DigestJSON`.
  - Files written by the incident store, the witness store and the prevention package are canonical JSON plus `\n`. Stdout is `JSON.stringify(…, null, 2)` in insertion order.
- **Frozen TS.**
  - `src/`, `package.json` and `pnpm-lock.yaml` are never modified.
  - The oracle is `node dist/cli.js` built from the current `src/`, which equals `ce99a79` (`v0.1.10-buildweek`), on Node 22.22.2.
  - Final goldens come from podman `localhost/r73img` (Node 22.22.2) running as uid 1000. Running as root changes the results of permission-dependent cases.
- **Strings.**
  - JS strings are WTF-8 (`internal/jsstr`); TS `slice`/`length` count UTF-16 units.
  - `localeCompare` is `canonical.SortLocale` or `canonical.LocaleCompare`. A bare `.sort()` is `jsstr.CompareUTF16`.
  - JS regexes are `jsre.MustCompile(source, flags)`. A regex with a lookahead (`SAFE_REF_NAME`, `SAFE_GIT_REVISION`) gets a hand-written matcher with a unit test against the TS regex.
  - Every ported function carries a comment naming its TS source.
- **Time and child processes.**
  - Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock`.
  - Child processes start only through `nodeproc.Spawn` or `hostcmd.Run` (async) or `nodeproc.SpawnSyncWith` (sync), matching the TS call site.
- **Commits.**
  - `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`. Never plain `gh`.
  - End each commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
  - TruffleHog pre-commit hook: never bypass it.
- **Output.** `Next: pnpm fl help` and the TS `next` strings stay verbatim.
- **Shell commands.** Each must finish in under 5 minutes: `timeout 280 … > log; tail`. The full TestWriterReplay takes over 40 minutes, so run filtered subsets locally. CI runs the full suite.

## Corrections to the spec found while planning

Fold these into the spec in Task 9:

1. **Three git environments in incident.**
   - `suggest` uses `safegit.Doctor()` and an async runner (5 s, 64 KiB, SIGKILL). Its env is the hardened set plus `GIT_PROTOCOL_FROM_USER=0`, with `GIT_CONFIG_GLOBAL` set.
   - `start` reads HEAD with `safegit.HostCommand()` (that is `INTAKE_SAFE_GIT_CONFIG`) through `spawnSync`. Its env has no `GIT_CONFIG_GLOBAL`, no `GIT_ATTR_NOSYSTEM` and no `GIT_PROTOCOL_FROM_USER` (`cli-app.ts:402-433`).
   - `start` reads HEAD even when `--from`/`--to` are given.
2. **`INTAKE_SAFE_GIT_CONFIG` is `HOST_COMMAND_SAFE_GIT_CONFIG`.** `internal/safegit` gets an `Intake()` alias with a live equality test. No new list.
3. **`continue`'s binding write comes first.** The session binding is written before any verification, so it persists when the continuation later fails (`cli-app.ts:545-556`).
4. **`continue`'s output differs from `investigate git`'s.**
   - Its proof root is `<draft repository>/.faultline/git-proof-bundles`, not the cwd default.
   - Its `proofBundle` key order is `directory, rootDigest, externalRootStatus, lifecycle`.
   - It drops `investigation` from the output.
   - Its verification failure text is `Generated incident proof bundle failed verification: …`.
5. **`prevention write --input` is ordinary.** With `grounding: "VERIFIED"` input it also yields `PREVENTION_VERIFIED` and runs the `AGENTS.md` upsert, so the upsert is not specific to `--from-bundle`.

## File Structure

```
internal/safegit/safegit.go              + Intake() (alias of HostCommand, TS INTAKE_SAFE_GIT_CONFIG)
internal/nodefs/fs.go                    openWriteErr: wx EISDIR→EEXIST rewrite on Windows only (3a carry-over)
internal/incident/schemas.go             src/incident.ts:11-131 schemas, regexes, SAFE_GIT_REVISION matcher
internal/incident/draft.go               src/incident.ts:155-300 range resolution, createIncidentDraft, verify
internal/incident/store.go               src/incident-store.ts (safe dirs, write-once draft, verified read)
internal/incident/intake.go              src/incident-intake.ts (runner, real-dir checks, upstream/parent, suggest)
internal/incident/continue.go            src/incident-continue.ts (binding assert, runFrozenIncidentContinuation)
internal/bundle/prevention/write.go      src/prevention-proof.ts:139-520 (prepare output, stage, write, publish)
internal/bundle/prevention/artifacts.go  src/prevention-from-artifacts.ts:53-329
internal/bundle/prevention/agentsmd.go   src/agents-md.ts
internal/cli/incident.go                 cli-app.ts:171-242, 380-693 (suggest/start/status/continue, head read, id)
internal/cli/prevention.go               cli-app.ts:2539-2665
difftest/gen/scenarios.ts                + incident scenarios, prevention-from-bundle scenario
difftest/testdata/writers.json           + incident-* and prevention-* cases
difftest/incident_live_test.go, prevention_live_test.go
```

---

### Task 1: 3a carry-overs and `safegit.Intake`

**Files:**
- Modify: `internal/nodefs/fs.go:208-228`, `internal/nodefs/nodefs_test.go`, `internal/safegit/safegit.go`, `internal/safegit/safegit_test.go`, `difftest/gen/node-oracle.ts` (op `safeGitLists` gains `intake`), `difftest/function_live_test.go` (`TestLiveSafeGitLists` compares it)

**Interfaces:**
- Produces: `safegit.Intake() []string`, which returns a fresh slice equal to `HostCommand()`.

**Gotchas:**
- **Windows-only rewrite.** `openWriteErr` must rewrite EISDIR→EEXIST for `wx` only when `runtime.GOOS == "windows"`. On Linux a path ending in `/` gives EISDIR even with `O_EXCL` (final re-review). Fix the comment's Unix claim to match.
- **Test both platforms.** The unit test must cover both branches. The mapping is a pure function of `(err, exclusive, goos)`: split out `openWriteCode(errno, exclusive bool, goos string) string` and test it on every OS.
- **TS name.** `INTAKE_SAFE_GIT_CONFIG` is in `src/cli-app.ts:380-399`. Confirm by reading it that it spreads `HOST_COMMAND_SAFE_GIT_CONFIG` with nothing added. If it adds anything, `Intake()` becomes its own list, and you must say so in the report.

- [ ] **Step 1: Write the failing tests**

```go
// internal/nodefs/nodefs_test.go
func TestOpenWriteCodeByPlatform(t *testing.T) {
	cases := []struct {
		exclusive bool
		goos, want string
	}{
		{false, "windows", "EISDIR"}, {true, "windows", "EEXIST"},
		{false, "linux", "EISDIR"}, {true, "linux", "EISDIR"},
		{true, "darwin", "EISDIR"},
	}
	for _, c := range cases {
		if got := openWriteCode(c.exclusive, c.goos); got != c.want {
			t.Errorf("%v %s: %s, want %s", c.exclusive, c.goos, got, c.want)
		}
	}
}
```

```go
// internal/safegit/safegit_test.go
func TestIntakeIsHostCommand(t *testing.T) {
	if !slices.Equal(Intake(), HostCommand()) {
		t.Fatal("Intake must equal HostCommand")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nodefs/ ./internal/safegit/ -run 'TestOpenWriteCodeByPlatform|TestIntakeIsHostCommand'`
Expected: FAIL (undefined: `openWriteCode`, `Intake`).

- [ ] **Step 3: Implement**

Add `openWriteCode(exclusive bool, goos string) string`, which returns `"EEXIST"` only for `exclusive && goos == "windows"` and otherwise `"EISDIR"`. Call it from `openWriteErr` with `runtime.GOOS`. Add `Intake()`. Extend the oracle op and `TestLiveSafeGitLists` with `intake: [...INTAKE_SAFE_GIT_CONFIG]`, after checking that it is exported. If it is not exported, extract it from `dist/cli-app.js` the way the 3a ops extract unexported helpers.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/nodefs/ ./internal/safegit/ && GOOS=windows go vet ./internal/nodefs/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveSafeGitLists -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodefs internal/safegit difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "fix(go): wx EISDIR→EEXIST only on Windows; safegit.Intake

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Incident draft schemas, creation and store

**Files:**
- Create: `internal/incident/schemas.go`, `internal/incident/draft.go`, `internal/incident/store.go`, `internal/incident/draft_test.go`, `internal/incident/store_test.go`
- Modify: `internal/witness/binding.go` (export the safe-directory helpers if the store reuses them; keep binding's own error text), `difftest/gen/node-oracle.ts` (ops `createIncidentDraft`, `verifyIncidentDraft`, `resolveIncidentRange`, `incidentStoreRoundTrip`), `difftest/incident_live_test.go` (new)

**Interfaces:**
- Consumes: `witness.IncidentDraftIDSchema`, `witness.DefaultIncidentDraftStore`, `gitproof.CommandDigest`, `gitproof.WitnessDigestSchema`, `gitinv.DigestPinnedImage`, `safedir.*`, `canonical.*`, `schema.*`, `clock.*`.
- Produces:
  - `incident.DraftSchema schema.Schema`
  - `incident.CreateDraft(in DraftInput) (jsjson.Value, error)`, the port of `createIncidentDraft` (247-287). `DraftInput` mirrors its options: `ID, Repository, Range jsjson.Value (ancestor, descendant, source), Command string, Symptom, Language, Behavior string, TimeoutSeconds float64, Runtime jsjson.Value (nil = null), Proposal jsjson.Value (nil = no binding)`.
  - `incident.VerifyDraft(draft jsjson.Value) (jsjson.Value, error)`
  - `incident.ResolveRange(head string, parents []string, from, to string) (jsjson.Value, error)`, the port of `resolveIncidentRange` (185-222).
  - `incident.WriteDraft(storeDir string, draft jsjson.Value) (string, error)` and `incident.ReadDraft(storeDir, id string) (jsjson.Value, error)`
  - `incident.SafeGitRevision(s string) bool`

**Gotchas:**
- **Schemas.**
  - Every schema is `.strict()`.
  - `IncidentReviewSchema` is a plain `z.union` (116). `internal/schema` has no plain union, so implement it as an ordered try-each. For a tampered draft read, the error must render zod's `invalid_union` issue. Check `internal/schema` for an existing union helper first. If it lacks one, add a minimal `Union` that reproduces zod 3's `invalid_union` (`unionErrors`), and test it against the oracle.
- **`SAFE_GIT_REVISION`** is `^(?!-)[^\0\r\n]{1,512}$`. Length is counted in UTF-16 units (`jsstr.Length`), and the first unit must not be `-`.
- **Range errors** (copy them verbatim):
  - `incident range ancestor and descendant must be different`
  - `HEAD is a root commit; select an explicit range instead of guessing a base`
  - `HEAD has multiple parents; select an explicit range instead of guessing a parent`
- **Store.**
  - `ensureSafeDirectory` creates one segment at a time from the volume root, with mode 0700, and lstat-checks each segment.
  - The draft is written to `<store>/drafts/<id>.json` with `wx` and mode 0600. It is canonical JSON plus `\n`, re-parsed and re-verified before the write.
  - Messages: `Incident draft store path must be a real non-symlink directory: <p>` and `Immutable incident draft already exists: <path>`.
  - `ReadDraft` checks `drafts/` with `assertSafeExistingDirectory`, then lstats the file. A missing file gives Node's raw lstat ENOENT text.
  - The draft path uses `safedir.ResolveTrustedSystemPath`, which on macOS turns `/var` into `/private/var`.
- **Clock.** `createdAt` is a single `clock.Now()` read inside `CreateDraft`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/incident/draft_test.go
package incident

import "testing"

func TestResolveRangeRefusals(t *testing.T) {
	head := "1111111111111111111111111111111111111111"
	if _, err := ResolveRange(head, nil, "", ""); err == nil || err.Error() != "HEAD is a root commit; select an explicit range instead of guessing a base" {
		t.Fatalf("root: %v", err)
	}
	two := []string{"2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"}
	if _, err := ResolveRange(head, two, "", ""); err == nil || err.Error() != "HEAD has multiple parents; select an explicit range instead of guessing a parent" {
		t.Fatalf("merge: %v", err)
	}
	if _, err := ResolveRange(head, nil, "abc", "abc"); err == nil || err.Error() != "incident range ancestor and descendant must be different" {
		t.Fatalf("degenerate: %v", err)
	}
}

func TestSafeGitRevision(t *testing.T) {
	for s, want := range map[string]bool{"HEAD~1": true, "-x": false, "a\nb": false, "": false} {
		if SafeGitRevision(s) != want {
			t.Errorf("%q", s)
		}
	}
}
```

Store test: write a draft, read it back and compare it canonically. A second write fails with the exact "already exists" text. A symlinked `drafts` parent is refused. Port `tests/incident-store.test.ts:23, 37, 49`.

Live test, `TestLiveIncidentDraft`:
- 1,500 generated draft inputs: every field at its schema boundary, plus NUL, CR/LF, astral and lone-surrogate strings, tagged and untagged images, 0/1/2/17 parents, and timeouts 0/1/3600/3601.
- 300 tampered drafts for `VerifyDraft`.
- Compare canonical results or `THREW:<message>`, under the seeded clock.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/incident/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** by transcription. Every function carries its TS source comment.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/incident/ ./internal/witness/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveIncidentDraft -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/incident internal/witness difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): incident draft schemas, creation and write-once store

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Incident intake (`suggest`)

**Files:**
- Create: `internal/incident/intake.go`, `internal/incident/intake_test.go`
- Modify: `internal/hostcmd/hostcmd.go` (add `Options.Env []string`, where nil inherits, if the intake runner needs it; keep existing callers unchanged), `difftest/gen/node-oracle.ts` (op `suggestIncidentRanges` with an injected scripted runner), `difftest/incident_live_test.go`

**Interfaces:**
- Consumes: `safegit.Doctor()`, `gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})` plus `GIT_PROTOCOL_FROM_USER=0` after `GIT_ALLOW_PROTOCOL`, `hostcmd.Run`, `nodefs.{RealpathSync,Lstat,Stat,Relative,IsAbsolute}`.
- Produces:
  - `incident.IntakeRunner func(args []string) hostcmd.Result`
  - `incident.NodeIntakeRunner(repo string) IntakeRunner`
  - `incident.SuggestRanges(repo string, run IntakeRunner) (jsjson.Value, error)`, which returns the intake object (schema `faultline.incident-intake.v1`).

**Gotchas:**
- **Runner.**
  - Timeout 5,000 ms, cap 65,536 bytes, SIGKILL. A runner that throws is treated as a null exit.
  - `usable` means exit 0, not timed out and not over the cap.
  - Unlike the doctor and runtime runners, output is joined as bytes and decoded once at the end. `normalizeRunnerResult` (285-302) also re-checks the byte cap.
  - Check that `hostcmd` decodes per chunk. If it does, the intake runner needs its own byte-joined decode: either add a `hostcmd.Options.DecodeOnce bool` or build the runner on `nodeproc.Spawn`. Pick whichever matches `createNodeIncidentIntakeRunner` (148-216) exactly, and justify the choice in the report.
- **Call order.** Copy the nine-call sequence and its conditions exactly. They are listed in the research notes, which are quoted below so the brief carries them:
  1. `rev-parse --is-inside-work-tree`, which must print `true`.
  2. `rev-parse --show-toplevel`, which must be absolute, is then realpath'd, and must contain the repository.
  3. `rev-parse --verify --end-of-options HEAD^{commit}`.
  4. `rev-parse --symbolic-full-name @{upstream}`.
  5. Only when the ref matches `SAFE_REF_NAME`: `rev-parse --verify --end-of-options <ref>^{commit}`.
  6. `merge-base <head> <upstream>`.
  7. Only when the merge base is not HEAD: `merge-base --is-ancestor <mb> <head>`.
  8. `rev-parse --verify --end-of-options HEAD^`.
  9. `merge-base --is-ancestor <parent> <head>`.

  The duplicate-range check comes after call 9. Calls 1 and 2 run `-C <realpath repo>`; the rest run `-C <root>`.
- **`SAFE_REF_NAME`** has a lookahead, so write a hand-written matcher, with a unit test against the TS regex over generated refs.
- **`isInsideDirectory`** (241-283) checks `relative()` first. It then walks dev/ino up to 256 levels, and on Windows it skips the walk when ino is 0.
- **Messages.** Copy every observation reason, candidate reason and error text verbatim from `src/incident-intake.ts`, including the `${op}` and `upstreamRef` interpolations.
- **Order.** Candidates are always upstream, then parent; observations are always `[upstream, parent]`. There is no sorting.

- [ ] **Step 1: Write the failing test**

```go
// internal/incident/intake_test.go
package incident

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/hostcmd"
)

func code(n int) *int { return &n }

func TestSuggestRefusesNonWorktree(t *testing.T) {
	run := func(args []string) hostcmd.Result {
		return hostcmd.Result{ExitCode: code(0), Stdout: "false\n"}
	}
	_, err := SuggestRanges(t.TempDir(), run)
	if err == nil || err.Error() != "The requested directory is not inside a non-bare Git worktree." {
		t.Fatalf("%v", err)
	}
}

func TestSuggestUnavailableGit(t *testing.T) {
	run := func(args []string) hostcmd.Result { return hostcmd.Result{TimedOut: true} }
	_, err := SuggestRanges(t.TempDir(), run)
	if err == nil || !strings.HasPrefix(err.Error(), "FaultLine could not safely inspect local Git state while ") {
		t.Fatalf("%v", err)
	}
}
```

Port every case in `tests/incident-intake.test.ts`. The fake-runner cases go in as unit tests. The real-git cases (a local tracking ref with an unreachable remote; a malformed upstream kept out of later argv) use a temporary repo with `update-ref refs/remotes/origin/<b>` and `branch.<b>.remote`/`merge` config.

Live test, `TestLiveIncidentSuggest`:
- 1,000 scripted runner sequences, covering every exit, timeout and cap combination and malformed outputs at each of the nine calls.
- 60 real repositories:
  - with and without upstream;
  - upstream ahead, behind and diverged;
  - detached HEAD;
  - root commit and merge HEAD;
  - a symlinked repo path;
  - a subdirectory as `--repo`.
- Compare the canonical result or `THREW:` message, plus the argv sequence.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/incident/ -run TestSuggest`
Expected: FAIL (undefined: `SuggestRanges`).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/incident/ ./internal/hostcmd/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveIncidentSuggest -v > /tmp/s3b-t3.log 2>&1; tail -5 /tmp/s3b-t3.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/incident internal/hostcmd difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): incident intake (suggest) with the hardened async git runner

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `fl incident suggest` and `fl incident start`

**Files:**
- Create: `internal/cli/incident.go`, `internal/cli/incident_test.go`, `difftest/testdata/fakegit/<name>/git` (only where a failure mode is not already covered: check the existing `slow`, `flood`, `killed`, `bad-oid`, `eacces` and similar first)
- Modify: `internal/cli/cli.go` (dispatch `incident`), `difftest/gen/scenarios.ts` (scenarios `incident-repo` (two commits), `incident-upstream` (tracking ref ahead/behind), `incident-root` (one commit), `incident-merge` (merge HEAD), `incident-command-file` (files: regular, symlink, over 32,000 bytes, invalid UTF-8)), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: Tasks 2–3; `witness.Propose(store, input)`; `runtimeimage.ResolveCuratedRuntime(requested, run)` through the `runtimeRunner` seam in `internal/cli/runtime.go`; `nodeproc.SpawnSyncWith`; `safegit.Intake()`; `option`, `requiredOption`, `hasFlag`, `fail`.
- Produces: `func incidentCommand(e *env, args []string) error`, covering `suggest` and `start`. Tasks 6 and 7 add `status` and `continue` to the same switch.

**Gotchas:**
- **`start` order** (`cli-app.ts:589-693`):
  1. `--repo`.
  2. Command input. The exactly-one check for `--command`/`--command-file` comes before the empty-value check, and the file is read at this step.
  3. `--from`/`--to`, both or neither.
  4. Id generation. Only when `--id` is missing: `clock.Now()`, then `clock.NewUUID()`. The id is `incident-` + the first 17 ISO digits + `-` + the first 12 UUID characters.
  5. The `--runtime` versus `--image` conflict.
  6. Timeout: `Number(x ?? "300")` followed by `Number.isInteger`, ported with `jsjson.ToNumber`. Bounds 1–3600.
  7. Docker inspect, only for `--runtime`.
  8. The git HEAD read, which runs even with explicit `--from`/`--to`.
  9. The draft's `createdAt`.
  10. `witness.Propose`, whose `proposedAt` is the next clock read.
  11. The draft write.
- **`--command-file` validation** (`incidentCommandInput` 214-242):
  - The path must be a regular file: lstat refuses symlinks and directories with "must be a real regular file, not a symbolic link or directory".
  - Read errors use Node's lstat/open text inside `Unable to read --command-file ${file}: ${msg}`.
  - The 32,000-byte cap is checked after reading.
  - The UTF-8 check is a fatal decode: `--command-file must contain valid UTF-8 without an ambiguous byte encoding.` Check what TS uses (a `TextDecoder` with `fatal: true`? a BOM check?) and match it exactly.
- **HEAD read.**
  - The spawn is `git <Intake> -C <repo> show -s --format=%H%n%P HEAD`, with env `PATH`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0`, `GIT_NO_REPLACE_OBJECTS`, `GIT_LFS_SKIP_SMUDGE`, `GIT_ALLOW_PROTOCOL=none` (plus Windows `SystemRoot`/`ComSpec`/`PATHEXT`), utf8 and the default maxBuffer.
  - Output handling: drop `\r`, split on `\n`, JS-`trim` the first line, and split the parents on JS `/\s+/`.
  - Copy both error texts from 402-433.
- **Writes.**
  - Witness directories are created first, then the proposal (0600 `wx`), then the draft directories one segment at a time, then the draft.
  - A failure after the proposal leaves the proposal on disk, as in TS.
- **Output.** Both commands print with `JSON.stringify` indentation in TS's key order. `suggest`'s order is `status, schemaVersion, repository, repositoryRoot, head, automaticSelection, selectionReason, candidates, observations, limitations, next`.
- **Harness.**
  - `start` cases need real git, and `--runtime` cases need fakeDocker `runtime-digest`.
  - The harness must not inject `HOME` or `GIT_*` into the HEAD read. It already gives TS and Go the same env, so check that the recorder does not add any.

**Writer cases** (all `posixOnly`; scenario in parentheses):

- `suggest`:
  - `incident-suggest-upstream` (incident-upstream)
  - `incident-suggest-parent-only` (incident-repo)
  - `incident-suggest-root` (incident-root)
  - `incident-suggest-merge` (incident-merge)
  - `incident-suggest-not-a-repo` (empty)
  - `incident-suggest-subdir` (`--repo` = a subdirectory)
  - `incident-suggest-symlink-repo`
  - `incident-suggest-no-repo` (usage error)
- `start`, basic:
  - `incident-start-command` (incident-repo)
  - `incident-start-explicit-range`
  - `incident-start-id`
  - `incident-start-generated-id` (seeded)
  - `incident-start-runtime-node` (fakeDocker runtime-digest)
  - `incident-start-runtime-unknown`
  - `incident-start-image`
  - `incident-start-image-tagged` (zod refusal)
  - `incident-start-runtime-and-image`
- `start`, refusals and edge cases:
  - `incident-start-timeout-0`, `-3601`, `-0x10` (accepted, as TS accepts it), `-1e3`
  - `incident-start-from-only`
  - `incident-start-both-command-flags`
  - `incident-start-neither-command-flag`
  - `incident-start-command-file`
  - `-command-file-symlink`, `-command-file-dir`, `-command-file-big`, `-command-file-invalid-utf8`
  - `incident-start-root` (no explicit range: root refusal)
  - `incident-start-merge`
  - `incident-start-twice` (two invocations, same `--id`: proposal "already exists")
  - `incident-start-draft-store-symlink`
  - `incident-start-custom-stores`
  - `incident-start-symptom-empty` (zod)
- Usage errors: `incident-unknown-subcommand`, and one case per usage error.

- [ ] **Step 1: Add the writer cases and run the replay to see them fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/incident-(suggest|start)'`
Expected: FAIL (`not yet ported: incident`, once the goldens exist).

- [ ] **Step 2: Write `internal/cli/incident_test.go`**

The test covers the id format, the timeout parsing (`" 5 "`, `"0x10"`, `"1e3"` accepted; `"1.5"` and `"0"` refused) and the command-input precedence:

```go
package cli

import (
	"regexp"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func TestGeneratedIncidentIDShape(t *testing.T) {
	clock.ResetForTest("incident-id")
	id := generatedIncidentID()
	if !regexp.MustCompile(`^incident-\d{17}-[0-9a-f-]{12}$`).MatchString(id) {
		t.Fatal(id)
	}
}
```

(Put this test in a `//go:build faultline_test` file, because `clock.ResetForTest` exists only under that tag.)

- [ ] **Step 3: Implement the handler; generate the goldens**

Use podman `localhost/r73img` as uid 1000; the 3a Task 10 and 11 reports give the exact command. Regenerate all writer goldens and confirm that the `git diff` of `writers.jsonl` shows only added `incident-` lines.

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/incident-|TestWindowsWriterSkipsList|TestGoldensReachEveryErrorLiteral' -v > /tmp/s3b-t4.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s3b-t4.log | tail -20`
Expected: PASS. A mismatch is fixed in Go, never in the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl incident suggest and fl incident start with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Incident continuation (`internal/incident/continue.go`)

**Files:**
- Create: `internal/incident/continue.go`, `internal/incident/continue_test.go`

**Interfaces:**
- Consumes:
  - `witness.VerifyFrozen(store, id, expected string, expectedProvided bool) gitproof.WitnessVerification`; use `.ErrorsValue()`, `.ApprovalValue()` and `.JSON()`.
  - `witness.ReadFrozen(store, id) (jsjson.Value, error)`
  - `gitproof.ReadVerifiedCodexLifecycleLedger(path) (jsjson.Value, error)`
  - `gitinv.InvestigateGitRange(request jsjson.Value, runner, false)`; see the call at `internal/cli/investigate.go:200-208`
  - `gitproof.WriteGitInvestigationProofBundle(output, result, frozen, gitproof.WriteOptions{ProofRoot, LifecycleLedger})` and `gitproof.Verify(dir, root, true)`
  - `clock.NowMillis()`
- Produces:
  - `incident.AssertFrozenWitnessBinding(draft, frozen jsjson.Value) error`
  - `incident.ContinueOptions{Repository, Store string; Draft jsjson.Value; ExpectedFrozenDigest, Image, LedgerFile, Output string; MaxStates *float64; Runner sandbox.Runner}`
  - `incident.Continue(o ContinueOptions) (jsjson.Value, error)`, the port of `runFrozenIncidentContinuation`. It returns the result object: either the `INVESTIGATION_NOT_PROOF` shape or the internal `PROOF_BUNDLE_READY` object, which the CLI reshapes.

**Gotchas:**
- **Flow** (`incident-continue.ts:76-191`):
  1. `VerifyFrozen`. If it fails: `Incident cannot continue until its witness is human-approved and frozen intact: ${errors.join("; ")}`.
  2. `ReadFrozen`, then `AssertFrozenWitnessBinding`:
     - If `review.witnessState !== "PROPOSED"`, throw "Incident draft has no proposal binding and cannot continue to investigation."
     - Otherwise collect mismatches in the order `proposal identifier`, `proposal digest`, `blinded incident-packet digest`, `exact command digest`, `exact command bytes`, and throw `Frozen witness does not match the immutable incident draft binding: ${errs.join(", ")}.`
  3. If the draft has a runtime and its image differs: "--image must match the digest-pinned runtime recorded in the immutable incident draft."
  4. No image at all: the text at :116.
  5. Ledger read, if a ledger file is given.
  6. `InvestigateGitRange` with `{repository, range: {ancestor, descendant} (no source), frozenWitness, expectedFrozenDigest, sandbox: {mode: "DOCKER_ISOLATED", image}, maxStates?}`. No runtime mapping.
  7. The `incident` object: `{id, draftDigest, range (with source), runtime: draft.runtime ?? {requested: "explicit", image}, frozenDigest, frozenDigestExternalStatus}`.
  8. Not proof: return `{status: "INVESTIGATION_NOT_PROOF", incident, investigation, proofBundle: null, next: [2 strings :150-151]}`.
  9. Proof:
     - `proofRoot = resolve(repository, ".faultline", "git-proof-bundles")`.
     - Output = `resolve(--output ?? join(proofRoot, "incident-" + id + "-" + (commit.slice(0, 12) ?? "unknown") + "-" + Date.now()))`. `NowMillis` is read only on this path, after the investigation. Port the commit-slice expression from `investigate.go:224-229`.
     - Write the bundle, verify it with the root, and on failure throw `Generated incident proof bundle failed verification: …`.
- **The undefined-digest check** at :101 cannot be reached from the CLI. Mark it `// coverage gate: unported caller` only if the gate demands it; otherwise drop it with a TS-cited comment, as Task 8 of 3a did for its dead checks.

- [ ] **Step 1: Write the failing test**

```go
// internal/incident/continue_test.go
package incident

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestAssertBindingRefusesUnbound(t *testing.T) {
	draft := jsjson.MustParse(`{"review":{"witnessState":"NOT_PROPOSED"}}`)
	err := AssertFrozenWitnessBinding(draft, jsjson.MustParse(`{}`))
	if err == nil || err.Error() != "Incident draft has no proposal binding and cannot continue to investigation." {
		t.Fatalf("%v", err)
	}
}

func TestAssertBindingListsMismatchesInOrder(t *testing.T) {
	draft, frozen := mismatchedBindingFixture(t) // builds a real draft + frozen witness with digest and command differing
	err := AssertFrozenWitnessBinding(draft, frozen)
	if err == nil || !strings.Contains(err.Error(), "proposal digest, blinded incident-packet digest") {
		t.Fatalf("%v", err)
	}
}
```

(Check `jsjson.MustParse` exists; otherwise use the parse helper the package tests already use. Write `mismatchedBindingFixture` in the test file with `CreateDraft` and `witness.Propose`/`Approve`/`Freeze` on a temp store.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/incident/ -run TestAssertBinding`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/incident/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/incident
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): incident continuation on the slice 2 investigation and bundle writer

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `fl incident status` and `fl incident continue`

**Files:**
- Modify: `internal/cli/incident.go`, `internal/cli/incident_test.go`, `difftest/gen/scenarios.ts` (scenario `incident-two-states`: slice 2's `git-two-states` repository plus `state.txt` and a `witness.mjs` overlay command, so `pass-fail` fake Docker yields a proof), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: Task 5; `witness.ResolveInheritedSessionFacts`, `sessionFlagOverrides(args, draftStore, id)` (`internal/cli/investigate.go:58`), `witness.WriteSessionBinding`, `gitinv.ResolveConfiguredImage`, `sandbox.NativeRunner()`.
- Produces: the `status` and `continue` branches of `incidentCommand`, plus `loadIncidentCommandContext(args) (incidentContext, error)`.

**Gotchas:**
- **Shared context** (447-462):
  - `--repo` goes through `option()`. When it is absent or last, the lookup repo is `resolve(cwd)`.
  - The draft store is `--draft-store` or `<lookupRepo>/.faultline/incidents`.
  - The repository is `resolve(draft.repository)`. If it differs from a given `--repo`: "Incident continuation refuses a --repo value that differs from the immutable incident draft repository." This text also applies to status.
  - The witness store default is `join(repository, ".faultline", "witnesses")`, the draft's repository. Do not use `witnessStore(args)`.
- **status:**
  - Takes only the literal `--expect-digest` and does no binding inheritance.
  - If the witness is invalid, match `jsre.MustCompile("missing|not been frozen", "i")` against the errors: any match gives `REVIEW_REQUIRED`, otherwise `INVALID_FROZEN_WITNESS`.
  - If valid: `ReadFrozen` plus `AssertFrozenWitnessBinding` inside a try. A throw sets `bindingError` and `INVALID_FROZEN_WITNESS`. Otherwise `READY_TO_INVESTIGATE` when the digest status is `MATCH`, else `RETAIN_DIGEST_REQUIRED`.
  - Output keys: `status, incident{id, draftDigest, range, runtime|null, witnessStore, draftStore}, frozenWitness{valid, frozenDigest, externalDigestStatus, approval, errors, [bindingError]}, next, limitations`.
  - Copy the `next` strings from 503-512. The READY one appends ` --image <digest-pinned-image>` only when `draft.runtime` is undefined.
  - Exit 1 only for `INVALID_FROZEN_WITNESS`. Status writes nothing.
- **continue:**
  1. Usage error if there is no id.
  2. Load the context.
  3. `ResolveInheritedSessionFacts`; flags win.
  4. If there is still no digest: `Missing required option: --expect-digest (no local session binding for ${id}). Freeze a witness first or pass --expect-digest explicitly.`
  5. Image = `option("--image")`, else `gitinv.ResolveConfiguredImage(repo)` (the config can throw), else `draft.runtime?.image`. Do not use `requireImageOrConfig`.
  6. `WriteSessionBinding` before calling `incident.Continue`.
  7. Print:
     - Not proof: the whole object, exit 1.
     - Proof: `{status, incident, proofBundle{directory, rootDigest, externalRootStatus, lifecycle}, next, limitations}`, exit 0. The second limitation is always the "supplied external … matched" variant.
  - Continue ignores unknown flags such as `--unsafe-local`, as TS does.
- **Harness.**
  - Chains run in one workspace: `incident start --id demo-1 …`, then `witness approve demo-1 …`, then `witness freeze demo-1`, then `incident status demo-1`, then `incident continue demo-1`.
  - The frozen digest comes from the binding that freeze writes, or `<STDOUT:n:/frozenDigest>` when a case passes it explicitly.
  - Reuse slice 2's `pass-fail` fake Docker and the relocation masks for the `incident-<id>-<desc12>-<ms>` bundle name; the seeded clock fixes `<ms>`.

**Writer cases** (all `posixOnly`, fakeDocker `pass-fail` unless named):

- `incident-status-*`:
  - `review-required` (after start)
  - `retain-digest` (after approve and freeze, no `--expect-digest`)
  - `ready` (with `--expect-digest`)
  - `ready-no-runtime` (image hint in next)
  - `invalid-frozen` (tampered frozen record)
  - `binding-mismatch` (frozen witness for a different command, via a mutation of the proposal before approve)
  - `repo-mismatch`
  - `missing-draft`
  - `bad-id`
- `incident-continue-*`:
  - `proof` (`incident-two-states`; bundle written; `fl verify` on it as a last invocation)
  - `not-proof` (all-pass scenario variant: exit 1, full object)
  - `inherit-digest` (no `--expect-digest`; binding from freeze)
  - `no-digest` (no binding, no flag)
  - `no-image`
  - `image-mismatch-runtime`
  - `config-image` (`.faultline/config.json` image used)
  - `unapproved`
  - `ledger` (slice 2 `git-ledger-full` base)
  - `bad-ledger`
  - `max-states`
  - `output` (custom `--output`)
  - `output-exists`
  - `daemon-down` (fakeDocker `daemon-down`)
  - `unknown-flag` (`--unsafe-local`, ignored)
- Usage cases.

- [ ] **Step 1: Add the cases; see them fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/incident-(status|continue)'`
Expected: FAIL.

- [ ] **Step 2: Unit test** `loadIncidentCommandContext`'s repo-mismatch text and the witness-store default (the draft's repository, not the cwd) in `internal/cli/incident_test.go`.

- [ ] **Step 3: Implement; generate goldens** (podman, uid 1000; confirm only added lines).

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/posix/incident-|TestGoldensReachEveryErrorLiteral|TestWindowsWriterSkipsList' > /tmp/s3b-t6.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s3b-t6.log | tail -20`
Expected: PASS. Every bundle written by a `continue` case must verify VALID under the Go and TS verifiers (the writer harness's existing cross-check; confirm it covers bundles under `.faultline/git-proof-bundles`).

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl incident status and continue with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Prevention writer, `--from-bundle` builder and `AGENTS.md`

**Files:**
- Create: `internal/bundle/prevention/write.go`, `internal/bundle/prevention/artifacts.go`, `internal/bundle/prevention/agentsmd.go`, `internal/bundle/prevention/write_test.go`, `internal/bundle/prevention/agentsmd_test.go`
- Modify: `internal/bundle/prevention/verify.go` (export `ValidateSemantics` and `ValidateRepairedRuns`, or call them in-package), `difftest/gen/node-oracle.ts` (ops `writePreventionProof`, `upsertFaultLineAgentsMd`, `buildPreventionProofFromVerifiedArtifacts`), `difftest/prevention_live_test.go` (new)

**Interfaces:**
- Consumes: `prevention.Verify`, `BodySchema`, `ManifestSchema`, `RepairedRunsSchema`; `gitproof.Verify`, `GitInvestigationResultSchema`, `FrozenWitnessSchema`, `ByteDigest`; `nodefs.{WriteFileSyncUTF8, SyncDir, Rename, Basename, Mkdir, MkdirAll, WriteFileUTF8, RemoveAll, Exists}`; `safedir.RelativeTrustedSystemPath`; `clock.{NowMillis, NewUUID}`; `canonical.{CanonicalJSON, DigestJSON}`.
- Produces:
  - `prevention.DefaultRoot(cwd string) string`
  - `prevention.Write(input jsjson.Value, output string) (Written, error)` with `type Written struct{ Directory, RootDigest, OriginalProofRoot, FrozenWitnessDigest, Classification string }`. Match TS's return fields; check `writePreventionProof`'s return object.
  - `prevention.BuildFromArtifacts(o ArtifactOptions) (jsjson.Value, []string)`, which returns the input or the failure reasons. `ArtifactOptions{Bundle, ExpectedRoot string; RepairedRuns jsjson.Value; RepairedCommit, RepairedTree, PatchDigest, CodexThreadID string; HasThreadID bool}`.
  - `prevention.WriteFromArtifacts(o ArtifactOptions, output string) (Written, []string)`. Writer exceptions become `[message]`.
  - `prevention.DigestRepairPatchFile(path string) (string, error)`
  - `prevention.UpsertAgentsMd(repo string, body jsjson.Value) (status string, err error)`, where status is `CREATED`, `UPDATED` or `UNCHANGED`.

**Gotchas:**
- **Writer order** (`prevention-proof.ts:398-520`):
  1. Grounded checks.
  2. Fixed limitation strings.
  3. `BodySchema.parse` of the spread object. Note the spread quirks: a string spreads to index keys, `null` spreads to `{}`, and `[...x]` throws on a non-iterable.
  4. Semantics: `Refusing to store an invalid prevention proof: a; b`.
  5. Repaired-runs check: `Refusing to store inconsistent repaired run bindings: …`.
  6. Prepare output:
     - The output must nest strictly under the root, checked with `relativeTrustedSystemPath`. Output equal to the root is rejected.
     - `ensureRealDirectoryTree` runs on the root, then on `dirname(output)`, creating missing directories with mode 0700.
     - It refuses an existing output.
  7. `clock.NewUUID()` names the stage `.<basename>.<uuid>.tmp` in `dirname(output)`.
  8. Digests and manifest.
  9. Inside a try:
     - `Mkdir` the stage with 0700, not recursive.
     - Write `README.md` (exact lines 488-497 plus `\n`), `prevention.json` and `manifest.json`, plus `repaired-runs.json` when bindings exist. Each JSON file is canonical plus `\n`; each write is `wx`, 0600, fsync, close.
     - Best-effort `SyncDir` of the stage.
     - Verify the stage. On failure: `Refusing to publish an invalid prevention proof: …`.
     - Re-check the output: `…appeared during assembly and will not be replaced: `.
     - Rename, then best-effort `SyncDir` of the parent.
  10. On catch: if the stage exists, `RemoveAll` it, then rethrow.
  - Clone the prepare-output helpers from `gitproof/write.go:63-157`, using the prevention error texts.
- **`--from-bundle` build order** (`prevention-from-artifacts.ts:163-297`): copy the failure order from the research notes into the code comments.
  1. `gitproof.Verify` with the root, always. Failures give `["Proof bundle verification failed", ...errors]`.
  2. zod-parse the investigation and the frozen record.
  3. Docker-isolated proof-grade.
  4. HOMOGENEOUS.
  5. The first `PASS_TO_FAIL`.
  6. `bindStableState` before and after.
  7. The repaired checks: count, distinct ids, commit and tree per run, trust and verdict, witness, environment.
  8. Distinct repaired commit.
  9. `repairBaseTree` equals `after.tree`.
  - `investigationDigest` is `DigestJSON` of the zod-parsed value, which strips unknown keys.
- **`AGENTS.md`** (`agents-md.ts`):
  - Markers are `<!-- FAULTLINE:BEGIN -->` and `<!-- FAULTLINE:END -->`, each found with the first `indexOf`.
  - Both present, END after BEGIN: replace the block, stripping one leading `\r?\n` from the suffix.
  - Empty or whitespace-only (JS `trim`): the block alone.
  - Otherwise: `replace(/\s*$/, "") + "\n\n" + block`.
  - The file is read as UTF-8 with U+FFFD for bad bytes.
  - If the result equals the existing content: `UNCHANGED`, no write. Otherwise `MkdirAll` the dirname and write with flag `w`, mode 0644.
  - `CREATED` exactly when `existing.length === 0`, so a whitespace-only file reports `UPDATED`.
  - `assertDigestOnly` refuses non-digest values. Copy its text.

- [ ] **Step 1: Write the failing tests**

```go
// internal/bundle/prevention/agentsmd_test.go
package prevention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertAgentsMdCreatesThenUnchanged(t *testing.T) {
	repo := t.TempDir()
	body := groundedBodyFixture(t) // a PREVENTION_VERIFIED body from the slice 1 base fixtures
	if s, err := UpsertAgentsMd(repo, body); err != nil || s != "CREATED" {
		t.Fatalf("%s %v", s, err)
	}
	if s, _ := UpsertAgentsMd(repo, body); s != "UNCHANGED" {
		t.Fatal(s)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(string(b), "<!-- FAULTLINE:BEGIN -->") {
		t.Fatal(string(b))
	}
}

func TestUpsertAgentsMdWhitespaceIsUpdated(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("  \n"), 0o644)
	if s, _ := UpsertAgentsMd(repo, groundedBodyFixture(t)); s != "UPDATED" {
		t.Fatal(s)
	}
}
```

Writer test: port `tests/prevention-proof.test.ts`'s six cases (summary write and verify, grounded VERIFIED, tamper and root mismatch, codexThreadId tamper, refusals, grounded without bindings) and `tests/agents-md.test.ts`'s three. Add stage-cleanup tests: make the output parent read-only after prepare, and assert that no `.*.tmp` is left behind.

Live tests:
- `TestLivePreventionWrite`: 800 generated inputs (summary and grounded; valid and invalid semantics; spread quirks) into fresh roots. Compare the written tree bytes and modes, or the `THREW:` text, under the seed.
- `TestLiveAgentsMd`: 1,000 generated existing contents (markers in all orders, CRLF, whitespace-only, invalid UTF-8, nested markers).
- `TestLivePreventionFromArtifacts`: the `git-unbound` base bundle with 300 generated repaired-runs and flag variations. Compare the input object or the reasons.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/prevention/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/bundle/prevention/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLivePreventionWrite|TestLiveAgentsMd|TestLivePreventionFromArtifacts' -v > /tmp/s3b-t7.log 2>&1; tail -5 /tmp/s3b-t7.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/prevention difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): prevention proof writer, --from-bundle builder and AGENTS.md upsert

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: `fl prevention verify` and `fl prevention write`

**Files:**
- Create: `internal/cli/prevention.go`, `internal/cli/prevention_test.go`
- Modify: `internal/cli/cli.go` (dispatch `prevention`), `difftest/gen/scenarios.ts` (scenario `prevention-from-bundle`: base `git-unbound` bundle plus `repaired-runs.json`, built from the bundle's third state with commit, tree, the three runs' `runId`/`executionId`, frozen `witnessDigest` and `environmentDigest = distinctDigests[0]`; and `prevention-input`: the slice 1 `sampleInput` and `groundedPreventionInput` as JSON files), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: Task 7; `prevention.Verify`; `printJSON`, `obj`, `option`, `requiredOption`, `fail`.
- Produces: `func preventionCommand(e *env, args []string) error`.

**Gotchas:**
- **verify:**
  - A missing target is a usage error.
  - stdout keys: `valid, schemaVersion, classification (null when unparsed), claim, rootDigest|null, externalRootStatus, errors`.
  - Trailing line: `Prevention verified\n` (valid and VERIFIED), `Prevention evidence summary\n` (valid), nothing when invalid.
  - Exit `valid ? 0 : 1`.
- **write, shared:**
  - The exactly-one check for `--input`/`--from-bundle` is judged by `option()`, so a last-position flag counts as absent. Text: `fl prevention write requires exactly one of --input <json> or --from-bundle <git-proof-bundle>.`
  - The output is computed before anything is read: `resolve(--output ?? join(resolve(".faultline", "prevention-proofs"), "prevention-" + Date.now()))`. `NowMillis` is read only when `--output` is absent.
- **write --input:**
  - Raw `readFileSync` and `JSON.parse` errors, not `readJSONInput`'s.
  - Writer exceptions go to stderr with exit 1.
  - Output keys: `status, claim, directory, rootDigest, originalProofRoot, frozenWitnessDigest, agentsMd?`, with no `classification`. Trailing line by status.
- **write --from-bundle:**
  1. `requiredOption --repaired-runs`.
  2. A raw read and parse.
  3. `runs` must be an array.
  4. Flags in this order: `--expect-root`, `--repaired-commit`, `--repaired-tree`, `--patch` (`Repair patch does not exist: <abs>`), `--codex-thread-id` (optional).
  5. `BuildFromArtifacts`.
  - Failure: stdout `{status: "PREVENTION_EXPORT_FAILED", reasons}`, exit 1, no trailing line.
  - Success: keys `status, claim, directory, rootDigest, originalProofRoot, frozenWitnessDigest, classification, agentsMd?`, then `Prevention verified\n`, exit 0.
- **`AGENTS.md`:**
  - Runs only for `PREVENTION_VERIFIED`, in the repo `resolve(--repo ?? cwd)`.
  - Every error is swallowed, and `agentsMd` is then omitted.
- **Harness:**
  - `--from-bundle` cases are `posixOnly`, because the Git proof verification runs git.
  - `--expect-root` comes from `<FILE:bundle/ROOT.sha256>`.
  - The success case writes `AGENTS.md`, which the tree digest captures.

**Writer cases:**
- `prevention-verify-*`: `summary`, `verified`, `root-ok`, `root-bad`, `tampered`, `missing`, `no-target` (cross-platform, on the slice 1 bases).
- `prevention-write-input-*`:
  - `summary`, `grounded` (`AGENTS.md` created)
  - `agents-existing` (marker replace), `agents-whitespace` (`UPDATED`), `agents-is-dir` (swallowed; `agentsMd` omitted)
  - `invalid-semantics`, `output-exists`, `output-outside-root`, `output-equal-root`, `default-output` (seeded `Date.now()`)
  - `bad-json`, `missing-file`, `root-symlink`
- `prevention-write-bundle-*` (posixOnly):
  - `ok` (`PREVENTION_VERIFIED`, `AGENTS.md`, then `fl verify` on the package)
  - `fail-to-pass-only` (`git-two-states-bundle` base: "requires a stable PASS_TO_FAIL transition")
  - `wrong-root`, `tampered-bundle`, `runs-not-array`, `runs-count`, `runs-duplicate`, `runs-wrong-commit`, `repaired-commit-same`, `base-tree-mismatch`
  - `missing-patch`, `missing-repaired-runs`, `bad-thread-id`, `with-thread-id`
- `prevention-write-both-flags`, `prevention-write-neither-flag`, `prevention-unknown-subcommand`.

- [ ] **Step 1: Add the cases; see them fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?prevention-'`
Expected: FAIL (`not yet ported: prevention`).

- [ ] **Step 2: Unit test** the `--input` / `--from-bundle` exactly-one rule with a last-position flag in `internal/cli/prevention_test.go`.

- [ ] **Step 3: Implement; generate the goldens** (podman, uid 1000; confirm only added lines).

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/(posix/)?prevention-|TestGoldensReachEveryErrorLiteral|TestWindowsWriterSkipsList' > /tmp/s3b-t8.log 2>&1; grep -E '^(---|ok|FAIL)' /tmp/s3b-t8.log | tail -20`
Expected: PASS. Every prevention package Go writes must verify under both the Go and TS verifiers; add it to the writer cross-check if the cross-check only knows Git bundles.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl prevention verify and write with writer goldens

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: End to end, faults, CI, docs and the done check

**Files:**
- Modify:
  - Tests: `difftest/endtoend_test.go` (`TestEndToEndSlice3bWithoutNode`) and `difftest/writerfault_live_test.go` (3b writer paths).
  - CI and gates: `.github/workflows/go.yml`, `.github/scripts/check-skips.sh`, `difftest/covergate_test.go` and `difftest/coverage_test.go` (gate `internal/incident`, the new prevention files and the new cli files; shrink the `not yet ported: %s` waiver).
  - Docs: `difftest/KNOWN_DIFFERENCES.md`, the slice 3 spec (the five corrections above), `docs/superpowers/specs/2026-09-25-go-migration-overview.md` (slice 3 done), and this plan (tick the boxes).

**End to end without Node** (spec 4.2 point 3, 3b): `TestEndToEndSlice3bWithoutNode` uses the 3a test's PATH setup, with no `node` and the `pass-fail` fake Docker. It runs:
1. `incident start` on an `incident-two-states` repository;
2. `witness approve`;
3. `witness freeze`;
4. `incident continue`;
5. `prevention write --from-bundle` with repaired runs built from the bundle's own third state;
6. `fl verify` on the package.

Every step must succeed with the expected status, and the final verify must report `Prevention verified`.

**Fault matrix** (spec 3.3):
- Writer paths:
  - incident `drafts/<id>.json` and its directories;
  - witness proposal written by `start`;
  - session binding written by `continue`;
  - the `continue` bundle stage and rename;
  - prevention stage directory, stage files, rename, parent fsync;
  - `AGENTS.md`.
- Syscall groups: `open`, `write`, `fsync`, `rename`, `mkdir`, `unlink`. Use path discovery from a fault-free TS trace, as 3a Task 12 does. A group that never fires is dropped, not faked.

**CI:**
- New live tests go into `difftest-live`'s `-list` derivation.
- New writer cases need no CI change.
- Check every job's time budget against a local measurement. The new cases add roughly 10 minutes to the replay.

**KNOWN_DIFFERENCES:** add only what a task recorded. Expected: none.

- [ ] **Step 1: Add the end-to-end test and the fault paths; run them**

Run: `go test -tags faultline_test ./difftest/ -run TestEndToEndSlice3bWithoutNode -v` and, in podman as uid 1000, the fault subset for the new paths.
Expected: PASS; fix any mismatch in Go.

- [ ] **Step 2: CI, gate and docs**; the full gated local run goes in the background with polled logs (3a Task 13's report has the command).

- [ ] **Step 3: Commit; hand back to the controller to push** (the controller pushes as Mizore66 and reads CI).

```bash
git add .github difftest docs
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "ci(go): slice 3b CI, coverage gate, docs and end to end

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Done criteria (spec 4.2, PR 3b):
1. `go test ./...` passes on all six runners, and every incident and prevention writer case matches its golden.
2. `difftest-live` and `stack-live` pass.
3. `TestEndToEndSlice3bWithoutNode` passes.
4. Every Git proof bundle and prevention package Go writes verifies under both the Go and TS verifiers.
5. `KNOWN_DIFFERENCES` is complete.
6. The overview marks slice 3 complete.

Adversarial review rounds of about 90 minutes each follow until a round finds no P0 or P1.
