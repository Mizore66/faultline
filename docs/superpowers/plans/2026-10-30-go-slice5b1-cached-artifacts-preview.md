# Go Slice 5b.1 Cached Artifacts and Judge Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce exact cached demo analysis/bundles and a complete Node-free static `judge-preview`, preserving the frozen evidence boundary and approved output-symlink refusal.

**Architecture:** A pure `internal/demofixture` package constructs ordered JS values. The existing demo verifier package gains its writer, while `internal/proofview` gains static preview rendering/writing and the CLI gains only the `judge-preview` route. Additive frozen-TS captures drive permanent Go-only replay, live comparisons and native acceptance; no real executor or general server is introduced.

**Tech Stack:** Go 1.27, standard library, existing `jsjson`/`canonical`/`jsstr`/`jsexc`/`nodefs`/`safedir`/`clock`; Node 22.22.2 only for frozen-TS capture/live evidence.

**Spec:** [Approved Slice 5b.1 design](../specs/2026-10-30-go-slice5b1-cached-artifacts-preview-design.md). The user approved the written spec on 2026-10-30, separately from the earlier scope split.

**Current closure (updated 2026-11-01).** Slice 5b.1 is accepted and normally merged as [PR #216](https://github.com/Mizore66/faultline/pull/216), actual merge `ff5fe7b1120410e60c321f3ae620cf524c5694ec`, API time `2026-10-31T22:38:24Z`, under verified Mizore66 after separate publication/ready/conditional merge authorization. Reviewed head `6a8708391b4dbe9ec75d96c84ea832ab68c71564` passed four automatic attempt-1 workflows: push [Go](https://github.com/Mizore66/faultline/actions/runs/41241997560) and [Verify](https://github.com/Mizore66/faultline/actions/runs/41241997512), PR [Go](https://github.com/Mizore66/faultline/actions/runs/41242001085) and [Verify](https://github.com/Mizore66/faultline/actions/runs/41242001082). All 52 successful job logs were retained/audited; two expected main-push-only provenance skips remain skips. Both events closed six-native execution and standalone preview, full pinned-TS live/replay/no-drift, both unnarrowed Ubuntu amd64 positional gates, native Linux faults and the remaining Darwin scopes. Hosted completion closes the acceptance gaps below, not the earlier failed/incomplete local attempts or process/TDD deviations. Actual merge verification established ordered parents `647440884a6668042f7726f2cbade38886d00a41` then accepted head, tree equality and remote-main equality. An initial explicit personal-email merge request was rejected; a diagnosed same-account request using the established personal GitHub noreply identity succeeded normally. This is reviewed-head acceptance, not fresh post-merge CI, signed provenance, blanket Docker/Windows coverage or dependency-security clearance. Existing Zod/platform exclusions remain qualified; no admin bypass, release, protected-path edit or cleanup occurred. Only 5b.1 is complete: real RERUN, interactive rendering, full judge-demo and plain serve remain separate follow-up. Earlier pending/publication-unauthorized statements below are retained dated execution history, superseded only for this completed PR. No new implementation/merge/publication authority follows. See the [5c specification](../specs/2026-11-01-go-slice5c-serve-bundle-design.md) for separately gated next work.

**Historical local execution status (2026-10-31), retained rather than rewritten as a pass.** Local implementation, independent review and bounded final integration are complete in the working tree; full acceptance is not complete, and nothing is staged, committed or published. Tasks 1–5 passed their local independent task reviews, including Task 5's scoped fix/recheck. The one whole-branch source review completed; its supplied `transitions: ""` parity defect was fixed in the one final fix wave with a native behavioral red/green and exact pinned-TS error/full-tree confirmation. The same final reviewer's scoped recheck approved Spec Compliance and Code Quality (Critical 0, Important 0, one harmless cosmetic Minor parked; no remaining behavioral defect found). Fresh post-fix Darwin/arm64 integration recorded 883 untagged test/subtest PASS / 4 explicit SKIP, 411 affected tagged PASS / 1 explicit SKIP, and 126 selected differential/literal/table PASS / 0 SKIP; none failed. The pinned Node 22.22.2 linux/arm64 live comparison recorded 140 PASS / 0 SKIP, including 19 canonical writer requests, 92 supplemental supplied-value cases and 26 nonwriter requests; actual stopped-job source/image/binary provenance was verified. Counts overlap across suites and are not a unique-test total. Process/TDD deviations and historical failures remain recorded, not cured by passing checks. Remaining local evidence gaps: `TestGoldenReplay` timed out (exit 124 at 238.018 s); a full tagged `./difftest` attempt left partial output with no JSON receipt or suite completion; the earlier broader selection (270 PASS / 117 SKIP) is not full-suite acceptance; `TestWriterReplay`, `TestEndToEnd*`, `TestLiveWriterFaults` and `TestLiveInvestigate` did not complete locally. Pending and unwaived: exact-head hosted acceptance (including pinned-Node live/no-drift jobs), all six native Go targets, the unnarrowed ubuntu-24.04 `FAULTLINE_COVERAGE_GATE=1` positional gate, canonical pnpm/tsx generation in CI, native Linux+strace `TestLiveDemoArtifactFaults` rows and native Windows link behavior. Local container execution/cross-compilation is not six-native or Linux fault evidence. Publication is not authorized; genuine RERUN execution, the interactive renderer, full `judge-demo` and plain `serve` remain follow-up scope. The Task 5 checkboxes below record only local steps that ran; Task 1–4 boxes defer to their task reports/reviews, not a clean-TDD or full-acceptance claim. Plan-command correction: the bare `pnpm exec tsx difftest/gen/gen-demo-artifacts.ts` cannot succeed in a committed checkout (its first-capture mode refuses existing evidence), so CI runs `--recapture` (a complete fresh capture compared byte for byte, writing nothing) followed by `--check`.

## Global Constraints

### Compatibility

- Go 1.27 and the standard library are required.
- Do not use `encoding/json` on digest paths or substitute ordinary Go maps/sorting for JS insertion order and ICU collation.
- Keep schema versions `faultline.demo.v1` and `faultline.proof-bundle.v2` unchanged.
- No new blanket masks, alternate accepted outputs, length-only comparisons, Node-availability skips or broad coverage waivers are allowed.
- Preserve failures, skips, incomplete attempts and source provenance.

### Safety and authorization

- Keep `src/`, `package.json` and `pnpm-lock.yaml` unchanged.
- No dependency upgrades, real provider calls, real-looking secrets, evidence/worktree cleanup or global git/account changes are authorized.
- Use `fl-test-key` for any necessary scripted fake key.
- Every shell invocation stays under five minutes total, with owned process-group cleanup and no detached/watchdog-reset workaround, broad cleanup or silent retry.
- Local tests/builds/capture remain serialized under explicit HOST ownership when implementation begins.

### Scope and publication

- `judge-demo`, including cached `--export-only`, plain `serve`, real RERUN, interactive rendering, HTTP mutation/lifecycle, attachments and later Slice 5 behavior changes remain outside this plan.
- Production code must not read TS, `dist/`, testdata or a runtime package manifest, or execute Node/Git for these new paths. Historical `node`/`pnpm` strings remain literal content.
- The dangling-output-symlink refusal is approved as policy; capture actual pinned-TS behavior before implementing the guard, retain differences without normalization, and make no race-free-confinement claim.
- Plan approval/production authorization is required before executing any task. Commits, pushes, PRs, merges, releases and protected-path/unfreeze/deletion actions need separate applicable authorization; no task silently commits.
- Preserve personal Mizore66 identity, normal TruffleHog hooks and required attribution for any later authorized commit/PR. Never run plain `gh`, change global git configuration or fall back after a failed push.

---

## Execution setup, evidence and boundaries

Current normal checkout: `/Users/anasqumhiyeh/Documents/faultline`, branch `go-slice5b1-design-20261030`, baseline `647440884a6668042f7726f2cbade38886d00a41`. The existing overview/5a edits and new approved spec are intentional uncommitted documentation; do not reset them. No new worktree is requested. Do not reuse another implementation plan's workspace or the design-research directory as an execution ledger.

At execution, initialize this plan's own ignored workspace with the executing skill's `scripts/sdd-workspace` helper, create its identified ledger, and set `SLICE_WORKSPACE` to that helper's printed absolute path for the command payloads below. Record baseline, existing documentation fingerprints, protected files/prior corpus fingerprints, actual Go/Node versions, HOST owner and every command/actual exit. Dependencies/tools must already be authorized and available; a missing pinned runtime is a recorded blocker, not permission for global installation, dependency updates or another plan's container-volume reuse.

All `Run:` commands below are command payloads, not permission to run an unbounded shell. The controller must wrap each payload in an owned-process-group runner with an aggregate 240-second deadline, unique stdout/stderr log and JSON receipt, preserving the child's actual exit. On timeout terminate only that group, record timeout/failure and stop that check. Do not detach, reset deadlines, silently retry or split one still-running process across calls. Independent test selectors can be separate bounded invocations, recorded separately; an over-budget invocation never becomes a pass because a narrower check later passes. Existing heavy hosted gates run in CI, not through an over-budget local workaround.

No task writes production files until its meaningful red has been recorded. Undefined-symbol/compiler failure is an acceptable initial red for a genuinely new API, followed by an assertion red when an empty implementation would otherwise escape. Capture generation is a test-infrastructure deliverable: its red is validation rejecting absent/incorrect capture evidence, never a fabricated claim that TS behavior failed.

### File ownership

| Owner | Files | Responsibility |
| --- | --- | --- |
| Task 1 | `difftest/gen/gen-demo-artifacts.ts`, `difftest/gen/demo-artifacts-oracle.mjs`, `difftest/testdata/demo-artifacts/{baseline.json,judge-preview.html,golden.jsonl}` | Pinned TS capture, case setup, complete artifacts and honest dangling-link evidence |
| Task 2 | `internal/demofixture/{fixture.go,replay.go,analysis.go,fixture_test.go,analysis_test.go}` | Fixture/witness/analysis construction and exact native tests |
| Task 3 | `internal/bundle/demo/{write.go,write_output.go,write_report.go,write_test.go,write_special_unix_test.go}`, `difftest/demoartifactstest/{model.go,case.go}`, `difftest/demoartifactsdriver/main.go`, `difftest/demo_artifacts_fault_test.go` | Exact supplied-analysis writer, safety/replacement, tagged driver and Linux fault comparison |
| Task 4 | `internal/proofview/{judge_preview.go,judge_preview_output.go,judge_preview_test.go,judge_preview_mode_unix_test.go}`, `internal/cli/{judge_preview.go,judge_preview_test.go}`, `internal/cli/cli.go`, `difftest/release/judge_preview_test.go` | Static page/output safety, CLI routing, portable untagged actual-binary checks |
| Task 5 | `difftest/{demo_artifacts_test.go,demo_artifacts_live_test.go,coverage_test.go,covergate_test.go}`, `.github/workflows/go.yml`, `difftest/KNOWN_DIFFERENCES.md`, the overview/5a/5b.1 status docs | Permanent/live integration, explicit error-gate ownership, native CI and evidence closure; extends Task 3's test adapter after its handoff |

Task 1 precedes all production work. Task 2 precedes Tasks 3/4; Task 5 consumes all deliverables. Tasks 3/4 have disjoint production files but runtime checks still share HOST. A future execution choice must explicitly authorize any parallel implementation; this dependency graph is not that authorization. Task 5 alone owns shared CI/known-difference/status edits.

### Interfaces fixed by this plan

```go
// internal/demofixture — all returned JS objects are fresh, not shared mutable globals.
func CreateFrozenWitness() jsjson.Value
func CreateReplayAnalysis() jsjson.Value
func StateLabel(id string) string // known fixture label, otherwise the original ID

// internal/bundle/demo — nil ProofRoot means default; a pointer to "" means cwd.
type WriteOptions struct { ProofRoot *string }
type Written struct { Directory, ManifestDigest, RootDigest string }
func DefaultProofRoot() string
func Write(outputDirectory string, analysis jsjson.Value, options WriteOptions) (Written, error)

// internal/proofview — nil analysis means the deterministic REPLAY default.
type JudgePreview struct { Path string; Bytes int }
func RenderJudgePreviewPage(analysis *jsjson.Value) (string, error)
func DefaultJudgePreviewPath() string
func WriteJudgePreview(outputFile string) (JudgePreview, error)

// internal/cli — uses existing env/option/requiredOption/failure formatting.
func judgePreviewCommand(e *env, args []string) error
```

`jsexc.Try`/`bundle.Must` preserve the repository's JS exception model. Public filesystem/render APIs return errors at the same boundary as existing writer/preview APIs; internal helpers may throw `jsexc.Thrown`. Unexpected Go panics are never swallowed.

## Task 1: Capture the real frozen-TS artifact and preview contract

**Files:** Create the Task 1 files above. Read `src/{fixture.ts,engine.ts,proof-bundle.ts,judge-preview.ts,cli-app.ts}`, `difftest/gen/gen-cli-help.ts`, existing seed/fault adapters and unchanged retained demo bases. Do not edit those TS source files or existing goldens. This task has no Go production dependency.

**Interfaces:** Produce schema `faultline.demo-artifacts-oracle.v1`, the default preview bytes and versioned JSONL records consumed by Task 5. The capture adapter accepts one JSON request file and returns only its requested real TS call/process result plus complete fixture inventory; it never executes RERUN.

- [ ] **Step 1: Add capture validation and record the absent-evidence red.** The generator supports `--check`: it validates committed metadata, unique cases, nonempty byte captures, valid base64 and the exact expected request-ID set without writing evidence. Before capture exists, run:

```sh
pnpm exec tsx difftest/gen/gen-demo-artifacts.ts --check
```

Expected: nonzero for absent capture evidence, with that exit/log preserved. `--check` must not create a capture or quietly bless mismatched metadata.

The exact metadata is:

```json
{
  "schema": "faultline.demo-artifacts-oracle.v1",
  "sourceCommit": "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  "manifestCommit": "647440884a6668042f7726f2cbade38886d00a41",
  "manifestVersion": "0.1.4",
  "nodeVersion": "22.22.2",
  "cachedRuntimeVersion": "v22.22.2"
}
```

- [ ] **Step 2: Implement the pinned capture adapter and complete-byte inventory.** Keep scripts separate from case data; pass request/root paths as arguments. Oracle builds use the intact current-tree manifest and unchanged frozen source, not a tag checkout. Preflight actual Node `22.22.2`, manifest `0.1.4`, real CLI `FaultLine 0.1.4\n`, and environment digest `sha256:7de997bc873dc0e1909a6aa3bf7a5a40fe4958fbb0bb66b3c624e4c75074d334` before writing captures. Reuse `gen-cli-help.ts`'s project/CLI resolution, but never normalize versions or substitute its older metadata.

Use this evidence shape; analysis/renderer/library values use compact JSON text to retain JS string/property-order behavior. Artifact bytes are never normalized:

```ts
type Entry =
  | { path: string; kind: "directory" }
  | { path: string; kind: "file"; base64: string }
  | { path: string; kind: "symlink"; target: string };
type Request = {
  id: string;
  kind: "analysis" | "writer" | "render" | "cli";
  base?: "replay" | "rerun" | "unicode" | "lone-surrogate";
  mutation?: "extra-keys" | "missing-catalog" | "duplicate-id" |
    "sanitized-collision" | "no-boundary" | "escape-fallback" | "non-replay";
  setup?: "parent-file" | "output-file" | "output-directory" | "existing-preview" |
    "invalid-bundle" | "existing-bundle" | "linked-root" |
    "linked-parent" | "linked-output" | "dangling-output" | "linked-bundle-file";
  output?: string;
  proofRoot?: string;
  argv?: string[];
};
type Capture = {
  schema: "faultline.demo-artifacts-case.v1";
  request: Request;
  call: { valueJSON: string | null; error: string | null } | null;
  process: { stdout: string; stderr: string; exitCode: number } | null;
  before: Entry[];
  after: Entry[];
};
```

Paths in inventories are root-relative with `/`; sort inventory paths by byte order (ASCII case paths), include directories, use `lstat` and do not traverse links. Link targets in these fixtures are relative and stay under the case root. `before` is after setup but before the requested action. Serialize file bodies as base64, not hashes alone. Keep OS metadata/mode assertions in eligible native tests rather than hiding byte differences in platform-dependent capture metadata.

Only temporary case-root prefixes in call errors, returned directory fields and CLI stdout/stderr may use the harness's existing temporary-root placeholder convention. Never pass page/analysis/log/file bytes through a generic output mask; never strip timestamps or digests. Store raw TS dangling-link results even when Go's approved expectation differs.

The adapter invokes the actual modules; the essential dispatch is:

```js
const { createDemoAnalysis } = await import(new URL("../../dist/engine.js", import.meta.url));
const { writeProofBundle } = await import(new URL("../../dist/proof-bundle.js", import.meta.url));
const { renderJudgePreviewPage } = await import(new URL("../../dist/judge-preview.js", import.meta.url));
// Fresh case cwd is established before these calls. `analysis` is the replay
// result or parsed retained historical input selected by request.base.
const actions = {
  analysis: () => createDemoAnalysis("REPLAY"),
  writer: () => writeProofBundle(request.output ?? ".faultline/bundles/demo", analysis,
    request.proofRoot === undefined ? {} : { proofRoot: request.proofRoot }),
  render: () => request.id === "render-default" ? renderJudgePreviewPage() : renderJudgePreviewPage(analysis)
};
```

For `cli`, spawn the actual `dist/cli.js` with `['judge-preview', ...request.argv]`, capturing its complete streams/exit without adding a wrapper formatter. For library calls, catch only to store the actual error message and null value; successful `valueJSON` is the actual `JSON.stringify(result)`. `render` defaults through the real function in the default-render case, rather than always injecting analysis.

Use parsed `bases/demo-rerun/analysis.json`, `bases/demo-rerun-unicode-stdout/analysis.json` and `bases/demo-rerun-lone-surrogate-stdout/analysis.json` for historical inputs. Never call `createDemoAnalysis("RERUN")`. Extra-key mutation appends `"z-extra"` and `"a-extra"` to analysis, retaining insertion order; do not schema-parse it before writing. Missing-catalog removes the first catalog record, leaving references. Duplicate-ID appends a duplicate catalog record. Sanitized-collision appends two unreferenced records copied from the first record but IDs `collision:` and `collision?`; coverage passes and both produce `runs/collision_/result.json`, reaching the writer's real duplicate-path guard. No-boundary uses `transitions: []`. Escape/fallback sets witness behavior and one claim to `<>&\"'`, sets one displayed state ID to `<unknown-state>`, and retains REPLAY mode. Non-replay sets mode to RERUN on supplied data only.

- [ ] **Step 3: Cover this fixed minimum case matrix and capture the dangling-link decision first.** All output paths below are case-root-relative; absolute-path CLI coverage is added by expanding the case root into argv before spawning.

| Group | Required request IDs and input |
| --- | --- |
| Analysis/render | `analysis-replay`; `render-default`; `render-replay`; `render-rerun-refused`; `render-no-boundary`; `render-escape-fallback` |
| Writer success/data | `writer-replay`; `writer-rerun`; `writer-unicode`; `writer-lone-surrogate`; `writer-extra-keys`; `writer-replace` with existing valid demo output; `writer-custom-root` (`proofRoot: managed`, output `managed/demo`); `writer-empty-root` (`proofRoot: ""`, output `demo`) |
| Writer refusal | `writer-missing-catalog`; `writer-duplicate-id`; `writer-sanitized-collision`; `writer-root` (output `.faultline/bundles`); `writer-outside` (output `outside`); `writer-parent-file`; `writer-output-file`; `writer-invalid-existing`; `writer-linked-root`; `writer-linked-output`; `writer-linked-bundle-file` |
| Preview CLI success | `cli-default` (`[]`); `cli-custom` (`--output out/custom.html`); `cli-absolute`; `cli-upper-ext` (`out.HTML`); `cli-hidden-ext` (`.hidden.HTML`); `cli-double-dot-ext` (`..html`); `cli-trailing-output` (`--output`); `cli-ignored` (`--help ignored`); `cli-equals` (`--output=unused.html`); `cli-duplicate` (`--output first.html --output second.html`); `cli-overwrite` |
| Preview CLI refusal/security | `cli-empty-output` (`--output ""`); `cli-next-flag` (`--output --other`); `cli-extension` (`out.txt`); `cli-dotfile-no-ext` (`.html`); `cli-parent-file`; `cli-output-directory`; `cli-linked-parent`; `cli-linked-output`; `cli-dangling-output` |

This is 45 unique cases (6 analysis/render, 8 writer-success/data, 11 writer-refusal, 11 CLI-success and 9 CLI-refusal). Put setup/requests into the generator's explicit table once; the generated records carry requests for Go replay/live reuse. Defaults are writer output `.faultline/bundles/demo`/REPLAY/no root override, and CLI `--output out.html` for a named setup unless argv is shown. `writer-replace` supplies the retained RERUN analysis over a copied valid REPLAY output, making old/new bytes distinguishable. Writer parent-file uses `.faultline/bundles/blocked/demo` with a file at `.faultline/bundles/blocked`; CLI parent-file uses `blocked/out.html` with a file at `blocked`. Output-file setup writes `old output\n` at the writer output; invalid-bundle setup creates that directory with only `sentinel.txt` (`keep\n`); existing-bundle setup copies the unchanged REPLAY base. Preview overwrite starts with `old preview\n`; directory setup creates `out.html/sentinel.txt` with `keep\n`. For `cli-absolute`, keep `request.output` and the serialized argv operand as logical `absolute/out.html`; only the execution adapter expands that operand to the absolute case-root path before spawning. No temporary root is serialized into the request. Linked root points `.faultline/bundles` to in-fixture `actual-managed`; linked parent points `linked` to `actual-parent` and uses output `linked/out.html`; linked output points the requested output to an existing in-fixture `target.html` (`keep\n`) or a copied valid target bundle. Existing bundle interior-link setup replaces `witness/witness.json` with a relative link to copied original bytes in the same fixture. Link targets are always relative and confined to that fresh fixture. Prepare default output parents explicitly for setup, never by invoking either writer; do not consume the seeded UUID stream during setup. Each case has its own fresh empty root; setup and actions must not touch the real repository's docs or managed bundles. In `cli-dangling-output`, link `out.html` to the absent relative `target.html` inside that root, retain raw TS streams/exit and `before`/`after` including the link/target. Record whether Node follows it. The approved Go policy is refusal regardless; do not accept both Go outcomes. Permission/FIFO/rename fault cases belong to explicit eligible native/fault tests, not silent omissions or fake snapshots.

- [ ] **Step 4: Generate twice, compare exactly and validate the retained reference.** Run each bounded command separately after an authorized frozen TS build (`pnpm build`):

```sh
pnpm exec tsx difftest/gen/gen-demo-artifacts.ts
pnpm exec tsx difftest/gen/gen-demo-artifacts.ts --check
```

The first generation writes new evidence only. Preserve its fingerprints in this plan's workspace, generate again in fresh roots, and require identical metadata, HTML and JSONL bytes. Reject any changed pre-existing corpus/protected path. Compare freshly captured default HTML with `docs/judge-preview.html` and record equality or the exact mismatch; never replace the latter. Verify captured `writer-replay`'s 69-file artifact subtree byte-for-byte against the unchanged retained base, root `sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa`. Validation success is Task 1 green; production reds remain pending.

- [ ] **Step 5: Record the handoff, not a commit.** Record actual runtime/build provenance, exact 45 IDs, two capture fingerprints, dangling-link TS outcome and any mismatch/failure. Tasks 2–5 consume these captures unchanged. Independent task review is required at execution; the human's plan approval does not supply runtime evidence.

## Task 2: Construct the pure cached fixture and ordered analysis

**Files:** Create the Task 2 files in the ownership table. Read frozen `src/fixture.ts:9–113,208–226`, `src/engine.ts`'s REPLAY branches and existing `internal/{jsjson,canonical,jsstr,jsexc}`. Do not copy the executor, process spawning or Node runtime probing.

**Interfaces:** Produce `CreateFrozenWitness() jsjson.Value`, `CreateReplayAnalysis() jsjson.Value`, `StateLabel(string) string`. Private helpers are `fixtureStates() []jsjson.Value`, `makeState(id, contribution, session string, turn int, hunks []string, label string) jsjson.Value`, `replayRun(state, witness jsjson.Value) jsjson.Value` and `uniqueRunCatalog([]jsjson.Value) []jsjson.Value`. No RERUN mode argument or mutable exported object.

- [ ] **Step 1: Write exact native regression tests before production code.** Use real retained bytes; no generated expectations from the new implementation:

```go
func TestCreateReplayAnalysisExact(t *testing.T) {
    want, err := os.ReadFile("../../difftest/testdata/bases/demo-replay/analysis.json")
    if err != nil { t.Fatal(err) }
    a := CreateReplayAnalysis()
    got := []byte(jsstr.ToUTF8(jsjson.StringifyIndent(a, "  ") + "\n"))
    if !bytes.Equal(got, want) { t.Fatalf("analysis bytes differ\nwant=%s\ngot=%s", want, got) }
    digest, err := canonical.DigestJSON(a)
    if err != nil { t.Fatal(err) }
    if digest != "sha256:3abe08e047655ce6b42bac9e50d847a750f4d5b76d9ccacffdc986c96e6b0863" { t.Fatal(digest) }
    if len(a.Get("runCatalog").Items()) != 20 { t.Fatal("catalog count") }
    for _, r := range a.Get("runCatalog").Items() {
        if r.Get("executionKind").Str() != "CACHED" || r.Get("durationMs").Num() != 0 ||
            r.Get("executedAt").Str() != "2026-07-14T09:05:00.000Z" { t.Fatal(jsjson.Stringify(r)) }
    }
}
```

Also add `TestFrozenWitnessExact`, comparing full pretty witness bytes to `witness/witness.json`; `TestReplayFixtureStates` for all 20 state IDs/labels/digests; `TestReplayRunOutcomes` covering PASS/FAIL/ERROR and lone/combined hunk membership; `TestUniqueRunCatalog` for deduplication, locale order and exact conflicting-record error; `TestReplayAnalysisFreshValues` mutating one returned value and proving a later call still matches retained bytes. Tests in package `demofixture` can directly reach private algorithms without adding public test-only APIs.

- [ ] **Step 2: Record the red.** Run:

```sh
go test ./internal/demofixture -run 'Test(CreateReplayAnalysisExact|FrozenWitnessExact|ReplayFixtureStates|ReplayRunOutcomes|UniqueRunCatalog|ReplayAnalysisFreshValues)$' -count=1
```

Expected: new API undefined initially; once it compiles, an empty/hardcoded partial builder must fail the full-byte/state tests. Retain actual exits, not just a wrapper's status.

- [ ] **Step 3: Implement fixture constants and cached record construction.** Use ordered `jsjson.MustObject`/arrays, fresh per call. Port the 20 source rows without rewriting labels. Fix indexes as follows: 0–3 contributions; 4–11 timeline; 12 candidate; 13 reverted; 14 source-only; 15 renderer-only; 16 unresolved; 17 repaired; 18 prevention-first-bad; 19 prevention-last-good. `makeState` appends `treeDigest`, `fidelity`, `label` after the first five fields; tree digest input contains only `id`, `contributionId`, `turnOrdinal`, `hunks` in source insertion order.

The essential cached record code is:

```go
func replayRun(state, witness jsjson.Value) jsjson.Value {
    hunks := make(map[string]bool)
    for _, h := range state.Get("hunks").Items() { hunks[h.Str()] = true }
    verdict, reason, stdout, stderr := "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""
    if hunks["partial-renderer-hunk"] {
        verdict, reason, stdout, stderr = "ERROR", "FIXTURE_COMPILE_ERROR", "", "synthetic partial hunk does not compile\n"
    } else if hunks["settlement-display-default"] && hunks["settlement-display-boundary"] {
        verdict, reason, stdout, stderr = "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"
    }
    id := jsexc.Must(canonical.DigestJSON(jsjson.MustObject(
        "state", state.Get("id"), "witness", witness.Get("digest"), "replay", jsjson.MakeBool(true))))
    environment := jsjson.MustObject(
        "runtime", jsjson.MakeString("v22.22.2"), "packageManager", jsjson.MakeString("none"),
        "runner", jsjson.MakeString("built-in-sample-v1"), "network", jsjson.MakeString("disabled"),
        "credentials", jsjson.MakeString("redacted"))
    return jsjson.MustObject(
        "id", jsjson.MakeString(id), "stateId", state.Get("id"), "witnessDigest", witness.Get("digest"),
        "environmentDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(environment))),
        "verdict", jsjson.MakeString(verdict), "reasonCode", jsjson.MakeString(reason),
        "stdout", jsjson.MakeString(stdout), "stderr", jsjson.MakeString(stderr),
        "durationMs", jsjson.MakeNumber(0), "executionKind", jsjson.MakeString("CACHED"),
        "executedAt", jsjson.MakeString("2026-07-14T09:05:00.000Z"))
}
```

`CreateFrozenWitness` copies the ordered witness object and incident packet from `src/fixture.ts:22–28,94–112`, hashes the witness without `digest`, then appends `digest`. `StateLabel` looks up the original ID and returns its exact label or the original string. Do not implement `implementationFor`, `witnessProgram`, `verdictFor` or `executeFixtureState`.

- [ ] **Step 4: Implement the REPLAY engine with these explicit source-to-Go rules.** Build contribution/timeline records; find adjacent PASS→FAIL/FAIL→PASS transitions; for each transition record cached before/after runs, `stable: false`, and their two IDs. Collect those boundary records even though catalog deduplication removes them later. For minimization use states 14,15,16,12,13,19; attempts are ordered empty/source/renderer/partial/candidate/necessity with the exact source subset/note strings. Preserve budget `{used:6,max:50}`, candidate hunk order, sufficiency/candidate, necessity/reverted and `NOT_EXECUTED`. Prevention is states 19,18,17 with `verified:false`.

Use this real deduplication algorithm, not Go map iteration:

```go
func uniqueRunCatalog(records []jsjson.Value) []jsjson.Value {
    byID := map[string]jsjson.Value{}
    var ids []string
    for _, r := range records {
        id := r.Get("id").Str()
        previous, exists := byID[id]
        if exists && jsjson.Stringify(previous) != jsjson.Stringify(r) {
            jsexc.Throw(errors.New(jsexc.Concat("Run id collision with conflicting evidence: ", id)))
        }
        if !exists { ids = append(ids, id) }
        byID[id] = r
    }
    ids = canonical.SortLocale(ids)
    out := make([]jsjson.Value, 0, len(ids))
    for _, id := range ids { out = append(out, byID[id]) }
    return out
}
```

Construct the final analysis in approved spec Section 3.1's exact property order. Port literal fixture, grade reasons, claim statements and warning from the REPLAY arms of `src/engine.ts:120–179`; even awkward historical wording stays unchanged. Compute metrics using contribution/timeline state lists (expected sessions 4, turns 8, files 2, changedLines 12, implicatedHunks 2). `grade.value` is D; first three claims are UNKNOWN with empty evidence IDs, fourth INFERRED with first-bad run ID, fifth UNKNOWN. Never hash an entire state where the run ID expects `state.id`.

- [ ] **Step 5: Run green in both builds and record the result.** Run each separately:

```sh
go test ./internal/demofixture -count=1
go test -tags faultline_test ./internal/demofixture -count=1
```

Require full exact analysis/witness bytes, known digests and fresh-value isolation; no Node/Git invocation. Record environment digest as historical provenance, not a Go-host observation. Leave the new files uncommitted pending separate authorization.

## Task 3: Write exact supplied-analysis demo bundles and replace them safely

**Files:** Create `internal/bundle/demo/{write.go,write_output.go,write_report.go,write_test.go}` and `write_special_unix_test.go` (`//go:build unix`). Create the test-only tagged driver/package named under Step 5; Task 5 extends its case runner but does not change the production writer API. Rollback fault assertions belong in the Linux differential test specified below; there is no in-process filesystem fault seam.

**Consumes:** `demo`'s existing private `safeRunFileName`, `requiredDeclaredFiles`, `validateAnalysisCoverage`, `assertNoLinksOrSpecialFiles` and public `Verify`; `demofixture.CreateReplayAnalysis() jsjson.Value`; `nodefs`, `safedir`, `clock.NewUUID`, ordered JSON/canonical helpers. **Produces:** `Write(outputDirectory string, analysis jsjson.Value, options WriteOptions) (Written, error)`, `WriteOptions{ProofRoot *string}`, `Written{Directory, ManifestDigest, RootDigest string}`, `DefaultProofRoot() string`. Test-only shared request/results are in `difftest/demoartifactstest`, and the subprocess entry is `difftest/demoartifactsdriver/main.go`, both `//go:build faultline_test`; neither is a public `fl` command or reachable from release code.

- [ ] **Step 1: Add the full-tree writer red.** In package `demo`, compare exact physical paths and bytes, not only a digest:

```go
func TestWriteReplayExact(t *testing.T) {
    root := filepath.Join(t.TempDir(), "managed")
    out := filepath.Join(root, "demo")
    written, err := Write(out, demofixture.CreateReplayAnalysis(), WriteOptions{ProofRoot: &root})
    if err != nil { t.Fatal(err) }
    wantRoot := "sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa"
    if written.RootDigest != wantRoot { t.Fatal(written.RootDigest) }
    base := "../../../difftest/testdata/bases/demo-replay"
    want := map[string][]byte{}
    got := map[string][]byte{}
    for _, side := range []struct { root string; files map[string][]byte }{{base, want}, {out, got}} {
        err := filepath.WalkDir(side.root, func(path string, d fs.DirEntry, err error) error {
            if err != nil { return err }
            if d.IsDir() { return nil }
            if !d.Type().IsRegular() { return fmt.Errorf("non-regular artifact: %s", path) }
            rel, err := filepath.Rel(side.root, path)
            if err != nil { return err }
            b, err := os.ReadFile(path)
            if err != nil { return err }
            side.files[filepath.ToSlash(rel)] = b
            return nil
        })
        if err != nil { t.Fatal(err) }
    }
    if len(want) != 69 || !reflect.DeepEqual(got, want) { t.Fatal("complete demo artifact tree differs") }
    verified := Verify(out, written.RootDigest, true)
    if !verified.Valid || verified.CheckedFiles != 67 { t.Fatalf("%+v", verified) }
}
```

Add `TestWriteHistoricalInputsExact` reading the retained RERUN, Unicode-stdout and lone-surrogate-stdout analyses using `jsjson.Parse`, comparing every output path/body against their corresponding bases. Do not call the executor, parse supplied analysis through schema reconstruction, or require the lone-surrogate bundle's later verifier to accept it: capture and compare the actual TS verdict. Add `TestWriteManifestDigest` recomputing canonical manifest digest from `jsjson.Parse` of its file and proving it differs from a digest of pretty manifest bytes.

Run:

```sh
go test ./internal/bundle/demo -run 'TestWrite(ReplayExact|HistoricalInputsExact|ManifestDigest)$' -count=1
```

Expected: undefined new writer initially, then byte/assertion red before a partial implementation can pass.

- [ ] **Step 2: Implement ordered artifact assembly and its small helpers.** `write.go` owns the exported `jsexc.Try` wrapper, manifest, insertion-ordered `(path, body)` list, coverage check, hashes and result; `write_report.go` owns `report(analysis jsjson.Value) string` and `verifyInstructions` literal; `write_output.go` owns safe-output walk, physical writes and replacement. Use a `[]struct{path, body string}` plus a path-membership map, never Go map iteration for insertion order. A private `put(path,body)` normalizes `\\` to `/` and throws the exact duplicate-path error before appending. JSON is `jsjson.StringifyIndent(value, "  ")+"\n"`; physical/log encoding is through `nodefs.WriteFileUTF8` and hashes use `canonical.SHA256Hex(body)`.

The exact `VERIFY.md` literal has no final LF:

```go
const verifyInstructions = "# Verify this bundle\n\n" +
    "Run `fl verify <bundle-directory>` for structural self-consistency.\n\n" +
    "For tamper detection against an editor who could update hash metadata, pass an externally recorded root: `fl verify <bundle-directory> --expect-root <sha256:...>`.\n\n" +
    "Verification never executes repository code. Use the explicit judge rerun path to execute the reviewed built-in sample."
```

Assemble manifest fields in spec Section 4.1's order, with `investigationId = "sample-" + analysis.fixture.id`, canonical analysis digest, supplied witness digest, and `analysis.prevention.firstBad.environmentDigest`. `report` joins `src/proof-bundle.ts:159–179`'s exact lines with LF, selects the first **stable** PASS_TO_FAIL transition, and has no final LF. Put the seven base artifacts in source order and then result/stdout/stderr per catalog record. Compare locale-sorted actual names with `requiredDeclaredFiles(analysis)` using canonical JS arrays; retain the source's expected-file-set guard. Sort a separate path slice using `canonical.SortLocale` for hashes. Append one LF to hashes and `rootDigest+"\n"`; neither integrity file declares itself. Return canonical manifest digest only after publication succeeds.

- [ ] **Step 3: Port the full safety walk and test its observable ordering.** Preserve `src/proof-bundle.ts:102–132`: compute output/root relation with `safedir.RelativeTrustedSystemPath`; refuse empty, `..`-prefixed or absolute relative results; ensure managed root then output parent; inspect output path segments with the existing `Exists`/`Lstat` behavior; reject links, non-directory output and interior links/special entries; verify an existing directory and allow replacement only when valid. Reuse `safedir.EnsureRealDirectoryTree` with the exact proof-root message prefixes. Do not import the incomplete `gitproof.AssertSafeProofOutput` or add a general new hardening policy. Nil root calls `DefaultProofRoot`; non-nil empty string is resolved as cwd.

Add `TestWriteOutputSafety` table rows for root itself, outside root, valid custom root, explicit empty root versus omitted default, output file, parent file, invalid existing directory and valid existing bundle. For refusals compare exact errors and complete before/after inventory to captured TS; in particular coverage validation runs **after** safe-parent creation but before staging. `TestWriteCatalogAndCollision` checks missing references, duplicate IDs, sanitized collisions and preservation of appended ordered extra keys. `TestDemoWriterSymlinks` attempts in-fixture symlink creation, then checks root/output/interior links without following them; inability to create a link is a narrowly named eligibility skip, not a pass. The Unix-only `TestDemoWriterSpecialFile` creates a FIFO inside an existing output with `syscall.Mkfifo(..., 0o600)` and asserts refusal without reading it.

- [ ] **Step 4: Port replacement and cleanup literally, with no new stage verification.** Private `replaceDirectory(stage, output string)` allocates the backup UUID even when no old output exists. Use the existing Node-compatible rename/removal wrappers, preserving these catches:

```go
func replaceDirectory(stage, output string) {
    backup := output + ".backup-" + clock.NewUUID()
    hadExisting := nodefs.Exists(output)
    if hadExisting { bundle.Must(struct{}{}, nodefs.Rename(output, backup)) }
    if err := nodefs.Rename(stage, output); err != nil {
        if hadExisting && !nodefs.Exists(output) {
            bundle.Must(struct{}{}, nodefs.Rename(backup, output))
        }
        bundle.Throw(err)
    }
    if hadExisting { bundle.Must(struct{}{}, nodefs.RemoveAll(backup)) }
}
```

The outer stage block creates `<output>.staging-<clock.NewUUID()>` with `nodefs.MkdirAll(stage, 0o777)`, creates artifact parents with `nodefs.MkdirAll(parent, 0o777)`, writes files through `nodefs.WriteFileUTF8(path, body, 0o666, false)`, then writes hash/root and calls replacement. These requested modes match Node's unqualified stage/artifact writes, distinct from the managed-root 0700 walk. Catch using `bundle.Try`; if it failed and stage exists, remove stage with `bundle.Must` before rethrowing the original error. Thus restore/cleanup errors can replace earlier errors; do not aggregate. Direct native `TestWriteReplacement` writes two different valid supplied analyses successively, checks the new complete tree and confirms no staging/backup siblings remain. Do not claim rollback success from this success-only test.

- [ ] **Step 5: Add the tagged library driver and Linux fault comparison, not production fault switches.** Create:

```text
difftest/demoartifactstest/model.go        # tagged Request/Entry/Capture matching Task 1
difftest/demoartifactstest/case.go         # tagged fresh setup, mutations, real calls, inventories
difftest/demoartifactsdriver/main.go       # tagged one-request adapter, extended in Task 5
difftest/demo_artifacts_fault_test.go      # faultline_test && linux: actual strace comparisons
```

Define `Request`, `Entry`, `Capture` with Task 1's JSON names and optional-presence pointers where omission differs from `""`. Define `Prepare(root, repo string, req Request) error`, `Invoke(root, repo, cliBinary string, req Request) Capture`, and `Inventory(root string) ([]Entry,error)` in the tagged test package. Task 3 implements writer requests only, so it compiles and tests without the not-yet-created preview APIs; reject other request kinds as adapter failures at this stage. Writer success constructs an ordered `jsjson.MustObject` with lowercase `directory`, `manifestDigest`, `rootDigest` keys and sets `valueJSON` using `jsjson.Stringify`; do not marshal the exported Go result struct into capitalized keys. After Task 4, Task 5 adds analysis/render/actual-binary CLI dispatch without changing these interfaces. `Prepare` runs outside tracing and uses only copied retained input bytes; `Invoke` executes the requested real library or CLI call without setup/cleanup. The driver takes `--root <absolute case root> --repo <absolute checkout> --request <JSON file> [--cli <actual tagged fl binary>]`, changes cwd once, calls `Invoke`, writes one JSON Capture and exits nonzero only on adapter failure; the real call's error/exit is data. CLI requests require that explicit actual binary and execute it as a child with inherited `GOCOVERDIR`; library requests never need it. Task 5 passes `flBinary` for permanent/live CLI replay, and fault tracing follows its child with `strace -f`. Do not implement CLI requests with parent-process `cli.Run`, which would miss binary positional coverage. Task 1's `.mjs` adapter supports the same **invoke-only** path. No program accepts an arbitrary command/script, no adapter is imported into `cmd/fl`, and no new `FAULTLINE_TEST_*` production variable is introduced.

Build the helper once per test with `go build -tags faultline_test -o "$SLICE_WORKSPACE/demo-driver" ./difftest/demoartifactsdriver`. Pass the existing `FAULTLINE_TEST_SEED` before a fresh process; TS uses existing `--import difftest/gen/seed.mjs`. Only UUID order matters here: do not copy the Git live writer's sleep/clock-tick alignment. Preserve `GOCOVERDIR` if instrumented; `GOMAXPROCS=1` is an existing strace ordinal convention, not a CPU-load workaround.

`TestLiveDemoArtifactFaults` uses `oracle.Skip` and `requireStrace`, performs the Node/manifest/CLI/environment preflight, and traces only controlled case paths. Reuse `faultCalls`/discovery conventions, not the public CLI-only `runWithFault` command constructor. Discover per-side syscall ordinals from a no-fault trace; fail if the intended path/operation was not observed. Compare exact normalized library errors/CLI streams and the full resulting trees. Use `strace -P` for exact stage/backup/output paths; trace logs/request files stay outside the inventoried case root. Build deterministic stage/backup names from the existing seed stream, not a new writer option. One requested fault must demonstrably fire, or its test fails.

| Fault case | Actual injection / required resulting state |
| --- | --- |
| `stage-mkdir` | First mkdir of stage fails; no published output, partial stage removed if created |
| `stage-manifest` | open/write/close failures on staged manifest, ENOSPC/EIO/EACCES; no publication, outer stage cleanup |
| `stage-root` | open/write/close failures on staged ROOT.sha256; old output unchanged, stage removed |
| `move-old` | Rename old output to backup fails; old output remains, staged tree removed |
| `publish-new` | With no old output, stage→output rename fails; no output, stage removed |
| `publish-rollback` | With valid old output, output→backup succeeds then stage→output fails; backup→output succeeds and old bytes restored |
| `rollback-fails` | Filter to the three rename paths and fail publish plus restore (the second and subsequent matching rename calls after per-side discovery); error must identify **backup→output**, output absent, backup retained, stage removed |
| `backup-cleanup` | Publication succeeds, first unlink/rmdir in backup removal fails; new output installed, backup remains as actually left by TS; exact propagated cleanup error |
| `stage-cleanup` | Fail staged manifest write, also fail removal of that stage; exact removal error supersedes write error; actual partial stage retained |

Use EIO for rename/combined-cleanup state tests; the manifest/root rows run all three existing errnos. Task 3 registers only these writer fault rows. Task 5 adds `preview-write` after Task 4's real CLI exists: open/write/close failures on the requested HTML for all three errnos, exact empty success stdout/CLI failure, and full partial/unchanged bytes. Do not register an unimplemented preview row as a skipped/pass placeholder in Task 3. For removal choose the real operation named by each side's discovery (unlink/unlinkat/rmdir), including Node's real fallback/recursive sequence. Do not hardcode that TS and Go have identical syscall counts or use a synthetic panic to force these branches. Filter close injection carefully so only the target file close is eligible. Keep fault tests serial; if one invocation exceeds its local budget, preserve timeout and run independent named fault rows separately, never claim the narrowed union is an unnarrowed gate.

- [ ] **Step 6: Run green and record distinct evidence.** Each is a separately bounded invocation:

```sh
go test ./internal/bundle/demo -run 'Test(Write|DemoWriter)' -count=1
go test -tags faultline_test ./internal/bundle/demo -run 'Test(Write|DemoWriter)' -count=1
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest -run '^TestLiveDemoArtifactFaults$' -count=1 -v
```

The last command requires an eligible authorized Linux+strace+pinned-Node environment; the current Darwin host cannot establish it. At this task boundary it proves only the writer phase, not final preview-inclusive fault acceptance. Record unavailable evidence as pending, not passed; Task 5 reruns the completed fault suite and CI later runs the full selector. Preserve all fault trees/traces and failures. Independent review covers full-byte assembly, supplied-analysis boundary, exact safety ordering and real rollback outcomes.

## Task 4: Render/write the static judge preview and route the CLI

**Files:** Create `internal/proofview/{judge_preview.go,judge_preview_output.go,judge_preview_test.go,judge_preview_mode_unix_test.go}`, `internal/cli/{judge_preview.go,judge_preview_test.go}`, and `difftest/release/judge_preview_test.go`; modify only the dispatch switch in `internal/cli/cli.go`. Reuse the existing **untagged** helpers in `difftest/release/help_test.go`; do not create the nonexistent `cmd/fl/help_standalone_test.go` or duplicate binary builders.

**Consumes:** `demofixture.CreateReplayAnalysis() jsjson.Value`, `StateLabel(string) string`, private proofview chrome and `EscapeHTML`, existing CLI helpers, Task 1 HTML/cases. **Produces:** `RenderJudgePreviewPage(analysis *jsjson.Value) (string,error)`, `DefaultJudgePreviewPath() string`, `WriteJudgePreview(outputFile string) (JudgePreview,error)`, `JudgePreview{Path string; Bytes int}`, and `judgePreviewCommand(*env, []string) error`. The CLI handler does not depend on bundle writing or the test driver.

- [ ] **Step 1: Add full-page/default/guard renderer reds.** Compare complete encoded HTML to the freshly captured file:

```go
func TestJudgePreviewPageExact(t *testing.T) {
    want, err := os.ReadFile("../../difftest/testdata/demo-artifacts/judge-preview.html")
    if err != nil { t.Fatal(err) }
    got, err := RenderJudgePreviewPage(nil)
    if err != nil { t.Fatal(err) }
    if !bytes.Equal([]byte(jsstr.ToUTF8(got)), want) { t.Fatal("complete preview HTML differs") }
    a := demofixture.CreateReplayAnalysis()
    explicit, err := RenderJudgePreviewPage(&a)
    if err != nil || explicit != got { t.Fatal("explicit REPLAY differs", err) }
    a.Obj().Set("mode", jsjson.MakeString("RERUN"))
    _, err = RenderJudgePreviewPage(&a)
    if err == nil || err.Error() != "Judge preview only renders the deterministic REPLAY fixture." { t.Fatal(err) }
}
```

Add `TestJudgePreviewRenderVariants` using Task 1's exact rendered strings for no-boundary and escaping/fallback; include unknown boundary/state IDs, quotes/apostrophes/angle brackets and all three verdict badge classes. Complete-string comparison is primary; additional no-`<script`/API/execution-claim assertions are guardrails, not the byte oracle.

Run:

```sh
go test ./internal/proofview -run '^TestJudgePreview(PageExact|RenderVariants)$' -count=1
```

Expected: missing new API, then exact-page red until the renderer is fully ported.

- [ ] **Step 2: Port the renderer, using existing chrome without refactoring it.** In `judge_preview.go`, select default analysis only for nil pointer. Guard mode first, find the first PASS_TO_FAIL transition regardless of stability, label both IDs with `demofixture.StateLabel`, and escape the resulting boundary copy. Build last-good/first-bad/repaired rows in that order; badge class is lowercase verdict. Claims stay in array order. Port the five thresholds and the full HTML template from `src/judge-preview.ts:50–71`, retaining every line break/space and literal pnpm/Node reference. Use a raw Go template plus concatenations for dynamic values; do not parse/reformat HTML or derive production HTML from captured testdata.

These are the exact shared helper calls/constants at its boundaries:

```go
start := renderIncidentPageDocumentStart("deterministic judge preview")
idea := "Fixture sandbox only — not COMMIT_PROOF. Product Idea: pnpm fl judge-proof (sample root sha256:f85c446d…) or docs/self-incident-proof-preview.html."
notice := "NOT A LIVE DOCKER PROOF. This self-contained page is a read-only snapshot of FaultLine's built-in replay fixture. It did not run Docker, execute repository code, call an API, or verify a live repository. It makes no certified regression, provenance, or prevention claim."
chrome := renderIncidentPageProductChrome(chromeOptions{
    command: "fl judge-preview", ideaBeat: &idea, notice: &notice,
})
end := renderIncidentPageDocumentEnd("FaultLine static preview schema: faultline.judge-preview.v1. No code executes when this file is opened.", nil)
```

Join `start`, `chrome`, source template sections and `end` with the source's exact newlines, not an added terminal LF. Do not modify the existing CSS/chrome to accommodate a mismatch.

- [ ] **Step 3: Add output-policy tests, then implement the file writer.** `TestJudgePreviewOutputPolicy` covers new/nested/default/custom outputs, uppercase `.HTML`, `.hidden.HTML`, `..html`, rejection of `out.txt`, `.html` and missing/empty extension, existing directory/file parent and overwrite-in-place. The separate `TestJudgePreviewSymlinks` owns source-backed parent/output symlink cases. Record the output-policy red with `go test ./internal/proofview -run '^TestJudgePreview(OutputPolicy|Symlinks)$' -count=1` before implementing the writer; missing writer API or a full expected error/tree mismatch is the red. Node treats `..html` as extension `.html` (last dot at index 1), so that case is successful; `.html` alone is refused. Assert extension rejection happens before any parent creation. `TestJudgePreviewSymlinks` first attempts fixture-local symlink creation; check existing and dangling output links, linked parent, unchanged unrelated sentinel, and absent dangling target after refusal. Task 1's actual pinned TS result must already exist; do not implement this guard first and reverse-engineer its expected TS evidence later.

The no-follow guard and focused Node-compatible `.html` check are:

```go
output := nodefs.Resolve(outputFile)
base := nodefs.Relative(nodefs.Dirname(output), output)
dot := strings.LastIndexByte(base, '.')
if dot <= 0 || !strings.EqualFold(base[dot:], ".html") {
    jsexc.Throw(errors.New("Judge preview output must be an .html file."))
}
safedir.EnsureRealDirectoryTree(nodefs.Dirname(output),
    "Judge preview output must use a real directory: ",
    "Judge preview output cannot traverse a symbolic link or non-directory: ")
stat, err := nodefs.Lstat(output)
if err != nil {
    var ne *nodefs.Error
    if !errors.As(err, &ne) || ne.Code != "ENOENT" { jsexc.Throw(err) }
} else if stat.Mode()&fs.ModeSymlink != 0 || !stat.Mode().IsRegular() {
    jsexc.Throw(errors.New(jsexc.Concat("Judge preview output must be a regular non-symlink file: ", output)))
}
page := jsexc.Must(RenderJudgePreviewPage(nil))
jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(output, page, 0o644, false))
result := JudgePreview{Path: output, Bytes: len(jsstr.ToUTF8(page))}
```

The exported wrapper catches JS exceptions with `jsexc.Try`. Default is `nodefs.Resolve("docs", "judge-preview.html")`. Do not use `filepath.Ext` (`.html` would diverge), `os.IsNotExist` on a wrapped Node error, `Exists` as the output gate, staging/backup replacement or arbitrary symlink resolution.

In `judge_preview_mode_unix_test.go` (`//go:build unix`), isolate a child test process with `umask 0027`: new file mode is 0640 and new directories 0700; existing 0600 file remains 0600. Record before/after `os.FileInfo` and require `os.SameFile` for overwrite. Windows core tests assert bytes/truncation/type handling without POSIX-mode promises; no new permission skip is needed.

- [ ] **Step 4: Add exact CLI matrix reds and the thin command.** Use actual `cli.Run` in temp cwd with captured buffers. Parse/default selection stays identical to the existing commit-proof-preview handler:

```go
func judgePreviewCommand(e *env, args []string) error {
    output := proofview.DefaultJudgePreviewPath()
    if _, ok := option(args, "--output"); ok {
        value, err := requiredOption(args, "--output")
        if err != nil { return err }
        output = nodefs.Resolve(value)
    }
    written, err := proofview.WriteJudgePreview(output)
    if err != nil { return err }
    e.out("FaultLine deterministic static judge preview written.\n")
    e.out("Preview: " + written.Path + "\n")
    e.out("Bytes: " + strconv.Itoa(written.Bytes) + "\n")
    e.out("Limitation: this read-only replay snapshot is not a live Docker proof, a verified proof bundle, or a record of a fresh execution.\n")
    return nil
}
```

Add exactly `case command == "judge-preview": return judgePreviewCommand(e, rest)` to the switch. `TestJudgePreviewCLI` covers the 17 non-link Task 1 CLI cases' complete streams/exit and file bytes, with no eligibility skip. `TestJudgePreviewCLISymlinks` owns exactly `cli-linked-parent`, `cli-linked-output` and `cli-dangling-output` as named subtests, first attempting fixture-local link creation; only those subtests may report missing Windows symlink privilege. Together these cover all 20 CLI cases. Failure cases require **empty stdout**; use the unchanged real failure formatter, not a substring. `TestJudgePreviewDispatchIsolation` verifies `judge-demo`, `judge-demo --export-only`, and plain `serve` retain exact not-yet-ported errors; unknown top-level input and help/version behavior remain untouched. A test expecting trailing `--output` to fail is itself wrong.

Record the red before adding command/dispatch, then run:

```sh
go test ./internal/cli -run '^TestJudgePreview(CLI|CLISymlinks|DispatchIsolation)$' -count=1
```

- [ ] **Step 5: Add one actual-binary Node-free release test.** In untagged package `release`, `TestJudgePreviewStandalone` builds once with `buildCLIHelpRelease(t, "")`, reads captured HTML into test-process memory, then runs the binary from a fresh temp cwd with `cliHelpEmptyPathEnv(t)`. Use `runCLIHelpRelease(t,bin,cwd,env,"judge-preview",...)` for default, custom nested, ignored-equals and trailing-output cases; distinct fresh cwd per invocation. Assert exact four success lines with resolved platform path and captured UTF-8 byte count, empty stderr, exit 0 and full page bytes. The child has empty executable PATH, no source/testdata/manifest beside it; parent fixture reads are not child runtime dependencies. Also assert no `.faultline` bundle directory or stage/backup files were created. On default and custom output overwrite, verify complete bytes again; do not require Node or Git to discover the test repo.

- [ ] **Step 6: Run green, recording actual platform eligibility.** Separate bounded invocations:

```sh
go test ./internal/proofview -run '^TestJudgePreview' -count=1
go test ./internal/cli -run '^TestJudgePreview' -count=1
go test ./difftest/release -run '^TestJudgePreviewStandalone$' -count=1
go test -tags faultline_test ./internal/proofview ./internal/cli ./difftest/release -run '^TestJudgePreview' -count=1
```

All must be Node-free. These are local-host results, not six-target evidence; the unchanged native matrix actually executes them later. Record link-creation failures as unexecuted narrowly eligible cases and preserve them. Independent review checks frozen page/copy, no-follow policy, parser quirks and no partial public command.

## Task 5: Integrate permanent/live/coverage CI and close only this subproject

**Files:** Create `difftest/{demo_artifacts_test.go,demo_artifacts_live_test.go}`; extend Task 3's tagged `demoartifactstest` model/runner/driver and Linux fault test; modify `difftest/{coverage_test.go,covergate_test.go}` only for explicit corpus/gate ownership as specified below, `.github/workflows/go.yml`, `difftest/KNOWN_DIFFERENCES.md`, and the overview/5a/5b.1 completion docs. Do not modify old golden entries, protected source/manifests, general server/runner code or prior execution workspaces.

**Consumes:** Task 1's exact capture schema/case table and all production APIs above. **Produces:** `TestDemoArtifactsBaseline`, `TestDemoArtifactsReplay`, `TestLiveDemoArtifacts`, `TestLiveDemoWriter`, and `TestLiveDemoArtifactFaults`; exact baseline validation; full physical before/after comparison; actual TS cross-verification and explicit coverage ownership. Test package helper `demoartifactstest` is tagged only; its exported APIs are `Prepare`, `Invoke`, `Inventory` as fixed in Task 3. No new production interface.

- [x] **Step 1: Complete the test adapter, then implement strict loading and permanent replay with meaningful mismatch reds.** Extend Task 3's writer-only `Invoke` with real `CreateReplayAnalysis`, default/supplied `RenderJudgePreviewPage`, and actual-binary CLI execution. Add matching Task 1 mutation/setup handling without invoking the real executor. Keep `Prepare`, `Invoke`, `Inventory` signatures unchanged. Library calls return `call`/null `process`; CLI calls return `process`/null `call`, with process/adapter failures distinguished. Serialize library values with `jsjson.Stringify`, never `encoding/json` on Go result structs. For writer success explicitly assemble the frozen lowercase result keys in order:

```go
value := jsjson.MustObject(
    "directory", jsjson.MakeString(written.Directory),
    "manifestDigest", jsjson.MakeString(written.ManifestDigest),
    "rootDigest", jsjson.MakeString(written.RootDigest))
valueJSON := jsjson.Stringify(value)
```

Renderer success is `jsjson.Stringify(jsjson.MakeString(page))`; analysis success is `jsjson.Stringify(analysis)`. Execute cwd-dependent cases serially, restore cwd on returning from an in-process invocation, and never use `t.Parallel` around global cwd/umask changes. Follow `help_data_test.go` but use the new baseline's exact **six fields as declared in Task 1** rather than the old manifest commit/count assumptions. Validate exact key set, required schema/IDs, non-null arrays, unique case IDs, valid base64 and requested process/call discriminator. The case set is Task 1's fixed matrix, not the CLI-help count 67. Use a 32 MiB scanner limit for complete per-case file bytes; reject oversized/malformed/trailing-junk data rather than truncating it. Standard `encoding/json` may decode control metadata/envelopes only; analysis/retained values pass through `jsjson.Parse`, so WTF-8/order are not lost before a digest.

`TestDemoArtifactsBaseline` mutates each metadata field (changed/missing), adds an extra field, passes null/empty/truncated/trailing-junk records and duplicate/missing/unknown case IDs; every corruption must fail. `TestDemoArtifactsReplay` calls `Prepare` then `Invoke` in a fresh per-case root and compares the complete `Capture`, including all inventories and real call errors/process status. Root substitution is limited to returned directory paths and known error/CLI path locations. Materialize each known root-relative path using platform `nodefs.Resolve` before comparison; never replace `/` in arbitrary text/HTML/artifact bytes. For library success, parse `valueJSON` with `jsjson.Parse`, replace only the explicit `directory` field with the actual per-case root-resolved path, then re-stringify in original order. For CLI success, replace only its second `Preview: ...` line's captured output path. Refusal messages replace only their known fixture path suffix. This is platform path rendering, not permission to mask other differences.

Go's dangling-link expected record is **not** the TS success record: for that single known case, assert exact current failure formatting/code 1, empty stdout and an unchanged inventory/absent target. Retain and inspect the raw TS record separately. No accepting either behavior and no generic divergence normalizer. If Task 1 observed TS refusal, require exact parity instead and do not add a fictional known difference.

Meaningful red: before wiring replay to the real driver, an intentionally empty actual result must fail a complete case comparison. Before declaring baseline validation green, each corrupted fixture must fail. Never rewrite a capture to match the port.

Run:

```sh
go test -tags faultline_test ./difftest -run '^TestDemoArtifacts(Baseline|Replay)$' -count=1 -v
```

Node/Git/history are not required; tagged `TestMain` builds `flBinary` as usual. Symlink subcases attempt actual fixture-local links; Windows inability to create them is restricted to exactly those named cases in CI, while every ordinary case must run on all six targets.

- [x] **Step 2: Add live reconstruction, writer verification and reachability gates.** `TestLiveDemoArtifacts` owns the 26 analysis/render/CLI requests; `TestLiveDemoWriter` owns the 19 writer requests, avoiding duplicate live replay of the writer matrix. Each calls the actual `.mjs` adapter under actual pinned Node against its requests, first requiring TS→committed capture equality and then Go→TS equality except the explicitly asserted dangling-link policy. Use `oracle.Skip(t)` only for live-oracle tests; permanent/native tests never call it. Preflight Node/manifest/intact CLI/environment before cases. `TestLiveDemoWriter` runs all writer requests through actual module and Go library (supplied historical inputs only), compares returned digests/ordered text plus full trees, then runs both `demo.Verify` and real TS verification on each valid Go-written REPLAY/RERUN/Unicode bundle. Use existing `tsVerify(t)` helper and assert exact acceptance; do not invent validity for lone-surrogate or extra-key cases the frozen verifier rejects. Record those invalid-input writer outputs **and** exact subsequent verifier results as TS does.

Each live test accumulates its own actual oracle refusal results as JSON-quoted output text. At the end of `TestLiveDemoWriter`, call:

```go
requireReached(t, "internal/bundle/demo/write.go", outputs.String())
requireReached(t, "internal/bundle/demo/write_output.go", outputs.String())
```

At the end of `TestLiveDemoArtifacts`, independently accumulate renderer refusals (including `render-rerun-refused`) and call:

```go
requireReached(t, "internal/proofview/judge_preview.go", outputs.String())
```

Assign the two writer files to `TestLiveDemoWriter` and the renderer file to `TestLiveDemoArtifacts` in the existing `liveGatedFiles`; the renderer's non-REPLAY guard has no CLI input, so it must have real library live coverage. Add `internal/demofixture` to `gatedDirs`. `judge_preview_output.go` and CLI stay ordinarily position-gated through actual `flBinary` replay; replay must use that instrumented binary for CLI requests, **not** `cli.Run` in the parent test. Preserve `GOCOVERDIR` in child env and keep one-shot invocation (`runCLIHelpProcess`), never automatic retry to hide coverage metadata failures.

Extend `goldenText` to append exactly `difftest/testdata/demo-artifacts/golden.jsonl` to existing corpus text; this is additive corpus discovery, not a rewrite of old evidence. As now-reachable proof-bundle messages appear, remove stale **individual** `unreachableLiterals` entries (`child directory`, `root cannot traverse`, `output cannot traverse`, `output must be a directory`) and rerun their existing live owners: their `requireReached` also sees the new committed corpus. Do not delete old source/position tests or globally mark the new package unported.

For truly unreachable cases, document each source proof **in the existing table**, never a new exemption list: the fixture's private catalog conflict cannot occur from the fixed builder's public input (native unit test still exercises the private conflict algorithm); writer expected-file-set mismatch cannot occur after all seven fixed paths plus the same catalog's three paths have been inserted and collisions rejected; preview's filesystem-root non-directory message is unreachable on a normal supported host. The existing proof-root post-walk symlink message remains race-only because the same path was just validated. Verify each exact literal/scanner classification during implementation; do not add an exemption merely because a test missed a reachable branch. Do not make an artificial public mutator or synthetic panic to generate evidence. `TestGoldensReachEveryErrorLiteral` catches stale text rows; `TestUnreachableSitesTable` is a separate positional-table validation, not its substitute.

Run each separately:

```sh
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest -run '^TestLiveDemoArtifacts$' -count=1 -v
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest -run '^TestLiveDemoWriter$' -count=1 -v
go test -tags faultline_test ./difftest -run '^Test(GoldensReachEveryErrorLiteral|UnreachableSitesTable)$' -count=1
```

A narrowed local positional/coverage-window check is supplemental only. Full Ubuntu amd64 `FAULTLINE_COVERAGE_GATE=1` with no `-test.run` remains the CI acceptance gate; never combine full-gate flags with a narrowed selector and claim closure.

- [ ] **Step 3: Complete preview faults and register exact CI ownership without broadening skips.** Extend `TestLiveDemoArtifactFaults` with the `preview-write` row defined in Task 3, now invoking Task 4's actual CLI through the completed driver. Discover/fail open, write and close for each ENOSPC/EIO/EACCES, check full error/empty stdout/file tree, and require injection actually fired. Rerun `FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest -run '^TestLiveDemoArtifactFaults$' -count=1 -v` in an eligible Linux environment; record timeouts or narrower row runs separately from full-suite acceptance. In `.github/workflows/go.yml`:

1. Before the goldens job's porcelain/no-drift check, add `pnpm exec tsx difftest/gen/gen-demo-artifacts.ts` followed by its `--check`. Existing generation remains unchanged.
2. Main `difftest-live` automatically discovers `TestLiveDemoArtifacts` and `TestLiveDemoWriter`; leave them there unless measured duration requires an explicitly documented move. Put `TestLiveDemoArtifactFaults` in that job's `elsewhere` list **and** in `difftest-live-faults`'s named `tests=` list. This ensures the Linux+strace fault suite runs once where its requirements are installed and `check-skips.sh '^$'` enforces it. Update comments to distinguish the unchanged existing 654-case writer matrix from the additional demo-artifact matrix; do not invent a new combined count/duration.
3. The existing six-native `go` matrix runs new untagged internal/release tests and tagged replay automatically. Add only narrow Windows link-privilege eligibility names: `TestDemoWriterSymlinks`, `TestJudgePreviewSymlinks`, `TestJudgePreviewCLISymlinks/(cli-linked-parent|cli-linked-output|cli-dangling-output)`, and `TestDemoArtifactsReplay/(writer-linked-root|writer-linked-output|writer-linked-bundle-file|cli-linked-parent|cli-linked-output|cli-dangling-output)`. Do not allow a parent `TestJudgePreviewCLI` skip or skip ordinary cases after one link failure. Core ordinary replay/Node-free tests have no new skip. Native Unix-only FIFO/mode tests are compiled only where supported, not claimed executed on Windows. Preserve all existing skip guards and overrides.
4. Preserve the untagged/tagged release no-test-override guard. The new driver/package must be absent from the release binary's dependency graph; no `FAULTLINE_TEST_*` filesystem override is added. Keep protected-source guards, action pins, Docker and provenance behavior unchanged.

Do not add the library writer to public `judge-demo` merely to satisfy coverage. Do not force the new dedicated corpus into `writers.json`, grow the old fault table or edit `windows-writer-skips.txt` unnecessarily; Task 3's dedicated tagged driver supplies those library/fault paths.

- [x] **Step 4: Record precise known differences and run bounded integration.** Add the historical cached-runtime distinction, with exact environment/digest and fixed-Node live matching; no normalization. If actual Task 1 evidence confirms dangling-link following, add its precise retained TS outcome versus Go refusal and native absent-target regression. Neither entry implies arbitrary symlink hardening, fresh execution, a general server-security exception or release readiness.

Separate local checks (all under the execution runner):

```sh
go vet ./internal/demofixture ./internal/bundle/demo ./internal/proofview ./internal/cli ./difftest/release
go vet -tags faultline_test ./internal/demofixture ./internal/bundle/demo ./internal/proofview ./internal/cli ./difftest/demoartifactstest ./difftest/demoartifactsdriver ./difftest/release
go mod tidy -diff
go test ./internal/demofixture ./internal/bundle/demo ./internal/proofview ./internal/cli -count=1
go test -tags faultline_test ./difftest -run '^TestDemoArtifacts(Baseline|Replay)$' -count=1
go test ./difftest/release -run '^Test(CLIHelpStandalone|JudgePreviewStandalone)$' -count=1
```

Also run each of these separately under the same runner; preserve selectors/inner exits in the ledger:

```sh
go test -tags faultline_test ./internal/demofixture ./internal/bundle/demo ./internal/proofview ./internal/cli -count=1
go test -tags faultline_test ./difftest -run '^TestCLIHelp(Baseline|Replay)$' -count=1
go test -tags faultline_test ./difftest -run '^Test(GoldensReachEveryErrorLiteral|UnreachableSitesTable)$' -count=1
go build -o "$SLICE_WORKSPACE/fl-release" ./cmd/fl
go build -tags faultline_test -o "$SLICE_WORKSPACE/fl-tagged" ./cmd/fl
```

Then check the unchanged release-override invariant (each build above is independently bounded):

```sh
python3 -I - "$SLICE_WORKSPACE/fl-release" "$SLICE_WORKSPACE/fl-tagged" <<'PY'
from pathlib import Path
import sys
marker = b"FAULTLINE_TEST_OPENAI_URL"
release, tagged = (Path(p).read_bytes() for p in sys.argv[1:])
assert marker in tagged, "tagged control lost its override; release check proves nothing"
assert marker not in release, "release binary contains the test-only provider override"
print("RELEASE_OVERRIDE_GUARD_PASS")
PY
``` Run final pinned captures twice, require byte identity/no drift, and recheck full protected/prior-corpus fingerprints. No tool installation or container-environment reuse is implicit. A future authorized CI run must execute full six-native suites, permanent replay/positional gate, no-drift/live comparisons, cross-verification, Linux faults and the prior full gates at the exact reviewed head. Do not inherit 5a's hosted passes or label a local arm64 run six-platform acceptance.

- [ ] **Step 5: Review and evidence handoff without publication.** Require independent per-task spec/quality reviews and one whole-branch review at execution. Assemble a receipt mapping every spec requirement to test/capture/platform, with actual failures/skips/timeouts preserved. Mark **only 5b.1** complete when all required evidence is actually complete; if exact-head hosted/six-native evidence is still pending, say local implementation complete/hosted acceptance pending rather than complete acceptance. Keep judge-demo/serve and the real-execution/server follow-up explicitly open. Preserve all scratch/evidence and the branch. Stop before commit/push/PR/merge/release unless the user separately authorizes that action.

## Controller self-review before plan handoff

This is an inline documentation review, not implementation evidence. Check these only after the final plan/source consistency pass and docs-only validation:

- [x] Spec Sections 1–3 mapped to scope/interface constraints, Task 1 provenance/capture and Task 2 exact fresh cached values; no hidden execution/runtime data dependency.
- [x] Spec Section 4 mapped to Task 3 byte/validation-order/replacement/fault states and Task 5 independent TS verification; no pre-publication schema/stage verification added.
- [x] Spec Section 5 mapped to Task 4 complete page/parser/output/modes/extension/approved dangling-link policy, with raw TS evidence first.
- [x] Spec Section 6 mapped to Task 1 deterministic additive evidence, Tasks 3/5 library/fault/positional checks, Task 4 untagged standalone and actual six-native CI; no blanket masks/skips/waivers.
- [x] Spec Section 7 and all global constraints preserved; task interfaces/owners/test selectors checked, no incomplete steps or invented existing helper, docs-only state validated and production/publication still gated.

## Human handoff

This plan is proposed for review. Choose **1 — Sonnet subagent-driven (recommended)** or **2 — inline execution**, with explicit plan approval/production authorization, to begin. This choice does not authorize commits, pushes, PRs, merges, releases, protected-source changes or deleting evidence/worktrees.
