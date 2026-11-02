# Go Slice 5b.2 Interactive Incident Renderer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an exact, pure Go port of the frozen interactive incident page, with independently captured complete-output evidence and no newly reachable command or server.

**Architecture:** `internal/proofview.RenderIncidentPage(jsjson.Value) string` consumes existing ordered analysis values. Private helpers compose separately retained literal template segments; a test-only frozen-TS capture generator supplies complete expected pages for 24 input variants. Genuine RERUN, mutable HTTP and command dispatch remain out of scope.

**Tech Stack:** Go 1.27, existing internal JS-compatibility packages, standard library; Node 22.22.2 only for test evidence authoring/recapture.

**Spec:** `docs/superpowers/specs/2026-11-02-go-slice5b2-interactive-renderer-design.md`

## Global Constraints

- Go 1.27; production dependencies are standard library plus existing internal Go packages only.
- Production must not execute Node or read `src/`, `dist/`, `testdata`, or a runtime package manifest.
- Frozen TS source is `ce99a793af0b020bf1b9cf21114f8d7897b37207`; oracle Node is `22.22.2`.
- Current-tree manifest/CLI is `0.1.4`; the frozen-source manifest is `0.1.3`. The live judging tag is distinct provenance.
- Preserve JavaScript string/number semantics, UTF-16/WTF-8, insertion order and exact observable output. No `encoding/json` on digest paths.
- `src/`, `package.json` and `pnpm-lock.yaml` are never modified.
- The TruffleHog pre-commit hook must never be bypassed. Never change global Git configuration.
- No real-looking secret is committed. Use only literal `fl-test-key` in fake credential values; no real provider operation.
- Every shell invocation stays under five minutes total. Use an owned runner with a 1–240-second aggregate deadline and bounded owned-process-group cleanup; no detached/watchdog-reset workaround, broad cleanup or silent retry.
- Preserve failures, skips, incomplete attempts, source provenance, workspaces and artifacts. Do not count them as passes.
- No masks, alternate outputs, length-only acceptance, gate waivers or production entropy override.
- No public release, auto-merge, admin bypass, protected unfreeze, oracle retirement, final deletion or cleanup.
- Work in place on `go-slice5b2-interactive-renderer-20261102`, based on merged main `fd8e0dd8819fea90aa19a2a7241bfd1df8a16909`; do not touch other worktrees.

## Controller ownership and setup

Standing user direction replaces routine approval pauses: continue from this plan into implementation and review. It does not expand protected/security/release authorization. All worker shell, interpreter, build, Git, network and child-process operations remain controller-owned and serialized. Workers use dedicated Read/Write/Edit only in their explicit allowed files; they do not dispatch subagents. A worker requiring a runtime check returns its exact request; the controller executes it through the runner and resumes the worker with the completed receipt.

Each task gets a fresh Sonnet implementer and an independent task reviewer. Record BASE before each dispatch. Workers do not commit; the controller performs verified task commits with the required personal identity and attribution. Never read an active report; wait for completion notification. Whole-branch review follows both task reviews. Keep this plan's workspace permanently for retained evidence despite generic cleanup guidance.

Use `.superpowers/sdd/2026-11-02-go-slice5b2-interactive-renderer/` for the ledger, runner, briefs, reports and retained command streams. Establish it with the SDD workspace script once; copy the existing reviewed bounded runner without changing behavior. Shell command examples below are PAYLOADS and must be wrapped by that runner, never run unbounded. Use unique names and independent 1–240-second aggregate deadlines. Never run the broad local suite if its established duration cannot fit; rely on actual exact-head hosted executions for that gate.

Commit form (after scoped verification, unchanged protected paths and intact hook):

```sh
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "<task summary>" -m "Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

Only the commit summary is variable. Use explicit paths for staging. Do not use `--no-verify`, global config changes, broad staging, automatic cleanup or a different account/remote. A failed push stops publication.

## File map

| File | Responsibility |
|---|---|
| `difftest/gen/gen-incident-renderer.mjs` | Frozen-TS pure-render capture, provenance binding, read-only checking and exclusive recapture comparison |
| `difftest/testdata/incident-renderer/baseline.json` | Source/runtime/locale/input/generator pins, exact case inventory and corpus/HTML hashes/lengths |
| `difftest/testdata/incident-renderer/golden.jsonl` | Complete input, output and unchanged-input captures |
| `difftest/testdata/incident-renderer/incident.html` | Baseline transport UTF-8 HTML, identical to its golden row |
| `difftest/testdata/incident-renderer/README.md` | Provenance, commands, immutable-evidence and historical-only boundaries |
| `internal/proofview/incident_test.go` | Independent corpus checks, full-page comparison, nonmutation/repetition/concurrency and semantic edge regressions |
| `internal/proofview/incident.go` | Public pure renderer and private dynamic helpers |
| `internal/proofview/incident_template.go` | Exact frozen literal segments and scripts |
| Migration overview and 5a dependency map | Only precise 5c completion / 5b.2 boundary status corrections |

### Task 1: Independent frozen interactive-renderer evidence

**Files:** Create the five generator/corpus files in the file map. Do not change old corpora, production Go, CI, protected sources or generated distribution sources. Read `src/ui.ts`, `src/domain.ts`, `src/fixture.ts` and only relevant existing capture conventions (`difftest/gen/gen-demo-artifacts.ts`, `demo-artifacts-oracle.mjs`). The old generator's masking and scratch deletion are NOT permission to use either here.

**Interfaces:**
- Consumes: real `dist/engine.js` `createDemoAnalysis("REPLAY")`, real `dist/ui.js` `renderIncidentPage(analysis)`, retained `difftest/testdata/bases/demo-rerun/analysis.json` as historical JSON input only.
- Produces: JSONL rows `{id: string, analysisJSON: string, htmlJSON: string, afterJSON: string}` in the spec's exact ordered 24-case matrix; baseline metadata and normal UTF-8 baseline HTML. Exports `caseIDs` for test-only checker reuse if useful; no production import.
- CLI: `node difftest/gen/gen-incident-renderer.mjs [--check|--recapture] --scratch ABSOLUTE_FRESH_DIRECTORY`. `--check` needs no scratch, must not import TS modules or write anything. Initial capture and recapture require a new empty directory; refuse a nonempty directory. Do not delete it.

- [x] **Step 1: Implement read-only validation before capture.** Use `node:assert/strict`, `node:crypto`, `node:fs`, `node:path`, `node:url` and `node:child_process` only. Validate exact argument grammar and case IDs, canonical JSONL, input/output field types and round trips, input equality, source/runtime metadata, lengths and hashes. All subprocess calls carry finite timeouts and fit the controller's aggregate deadline. For metadata pins use SHA-256:

```js
const sha256 = bytes => createHash("sha256").update(bytes).digest("hex");
const serialize = value => JSON.stringify(value);
const captureRow = (id, analysis, render) => {
  const analysisJSON = serialize(analysis);
  const htmlJSON = serialize(render(analysis));
  const afterJSON = serialize(analysis);
  assert.equal(afterJSON, analysisJSON, `${id}: renderer mutated input`);
  return { id, analysisJSON, htmlJSON, afterJSON };
};
```

- [x] **Step 2: Demonstrate missing-evidence failure.** Controller runs the generator's `--check` before any corpus files exist. Expected: nonzero with absent evidence, not successful empty inventory. Preserve receipt/streams. No production code has been written.

- [x] **Step 3: Implement the exact 24-case request matrix and capture.** Use the complete IDs from the spec. Fresh-clone each analysis with `JSON.parse(JSON.stringify(base))`. Make cases independent. Unknown state IDs themselves provide custom labels because `fixtureStateById` falls back to the ID. Exercise every helper mapping and fallback, first-three-only rows, all five verdicts/four evidence kinds, empty collections, stable transition filtering, grades A–D, hostile text in all renderable string paths, UTF-16/lone-surrogate behavior, JS whitespace/case and binary64 number coercion/grouping. Keep mutations consistent with valid DemoAnalysis value types. Do not invoke RERUN or seed any production entropy.

```js
const sample = JSON.parse(JSON.stringify(replay));
sample.fixture.title = `title <>&"'`;
sample.contributionRuns[0].stateId = "<unknown>&\"'";
sample.claims[0].statement = "<script>not markup</script>";
const row = captureRow("escaping", sample, renderIncidentPage);
assert.equal(JSON.parse(row.htmlJSON).includes("&lt;script&gt;not markup&lt;/script&gt;"), true);
```

Preflight compares the complete current `src/` tree to frozen Git source, rejects working-tree source drift, checks Node `22.22.2`, manifest `0.1.4`, and `Intl.NumberFormat().resolvedOptions().locale === "en-US"`. The controller freshly builds frozen TS before capture; bind the actual `dist/ui.js`, transitive fixture/engine/incident-page module hashes, source blobs, generator and retained input. Initial capture checks all outputs absent before exclusive creates. Recapture compares all complete row/HTML bytes; it cannot overwrite pins or normalize output.

- [x] **Step 4: Capture, validate and independently recapture.** Controller locates the already-installed Node 22.22.2 toolchain without installing/upgrading anything, verifies/builds frozen TS, runs initial capture in a fresh workspace child, then `--check`, then `--recapture` with a different fresh scratch child. Read full completed outputs and receipts. Expected: 24 exact cases; all afterJSON strings equal input; second capture byte-identical. Failure is preserved and investigated, never retried silently.

- [x] **Step 5: Review, verify and commit evidence.** Independent task reviewer receives the task brief, completed report and full diff file; both SPEC and QUALITY verdicts are required. Resolve blocking findings and recheck only affected contracts. Controller stages only the generator, corpus and associated plan/spec, verifies protected paths unchanged and commits with personal identity, intact hook and attribution. This delivers evidence, not an implemented renderer.

### Task 2: Pure Go renderer with complete parity and regression coverage

**Files:** Create `internal/proofview/incident_test.go`, `incident.go`, `incident_template.go`. Modify only relevant status rows in `docs/superpowers/specs/2026-09-25-go-migration-overview.md` and `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`. Update this plan's checkboxes only after the associated step really passes. No CLI/server/analysis/writer/shared-chrome/CI changes.

**Interfaces:**
- Consumes: Task 1's exact JSONL schema and baseline pins, `jsjson.Value`, existing `EscapeHTML`, JS number/string helpers and `demofixture.StateLabel`.
- Produces: `func RenderIncidentPage(analysis jsjson.Value) string`, pure and input-preserving, with exact frozen HTML/CSS/script bytes.

- [x] **Step 1: Write full-output tests first.** Read the existing `internal/proofview/judge_preview_test.go` only for conventions. Decode JSONL envelopes with test-only `encoding/json`; parse nested input/HTML with `jsjson.Parse` (check actual existing parser signature before use). Validate exact ordered IDs, hashes, lengths, no duplicate/missing rows and all input-before/after pairs. Store no expected HTML derived from Go. The core assertion is:

```go
before := jsjson.Stringify(analysis)
got := RenderIncidentPage(analysis)
if got != expectedHTML {
    t.Fatalf("%s: complete incident page differs from frozen TS", id)
}
if after := jsjson.Stringify(analysis); after != before {
    t.Fatalf("%s: renderer mutated analysis", id)
}
```

Check the existing `Stringify` return signature and use its actual API; do not invent an adapter that silently changes strings. Also compare baseline UTF-8 bytes via `jsstr.ToUTF8`, repeat every call, and run independent parsed values concurrently. Supplement complete pages with precise branch regressions; do not replace complete equality with substrings or lengths.

- [x] **Step 2: Observe RED before implementation.** Controller runs `go test ./internal/proofview -run '^TestIncident' -count=1 -timeout=120s`. Expected: missing `RenderIncidentPage` feature (or a behavioral failure if a test-local registration seam is used). Fix test infrastructure errors first; preserve every failed attempt. No production implementation is allowed until the missing-feature RED is recorded.

- [x] **Step 3: Port literal sections and dynamic helpers.** Keep source literal bytes authoritative instead of redesigning shared styles. After RED, extracting literal segments is mechanical and can use this bounded controller-owned recipe (the worker supplies the exact command request; do not execute it in an unbounded shell):

```python
from pathlib import Path
import re
source = Path("src/ui.ts").read_text()
start = source.index("  return `<!doctype html>") + len("  return `")
end = source.index("`;", start)
body = source[start:end]
parts = re.split(r"(\$\{[^{}]*\})", body)
assert "".join(parts) == body
out = ["package proofview\n\n"]
for i, part in enumerate(parts):
    if part.startswith("${"):
        continue
    assert "`" not in part
    out.append(f"const incidentLiteral{i} = `" + part + "`\n\n")
path = Path("internal/proofview/incident_template.go")
assert not path.exists()
path.write_text("".join(out))
print("literal segments", len(parts), "source bytes", len(body.encode()))
```

Read/review the extracted segments and full expression list; every interpolation is implemented in source order in `incident.go`. Use builders/concatenation rather than `html/template`, which would alter escaping/script behavior. Private helpers implement exactly `src/ui.ts:5-116`; source `124-145` defines preparation and `146-277` defines composition. Preserve all source conditions and copied text including known overstatements; no new certification inference.

For the integer-only locale interpolation, use binary64 shortest fixed formatting and ASCII 3-digit grouping, not `int64` narrowing. All other numeric interpolation uses existing JS-compatible number-to-string behavior. A candidate grouping algorithm to validate against the captured large-number case is:

```go
func incidentGroupedInteger(n float64) string {
    digits := strconv.FormatFloat(n, 'f', -1, 64)
    var out strings.Builder
    for i := range digits {
        if i > 0 && (len(digits)-i)%3 == 0 {
            out.WriteByte(',')
        }
        out.WriteByte(digits[i])
    }
    return out.String()
}
```

It receives only valid positive integer metrics. If the frozen TS capture rejects a numeric assumption, correct the port and retain the failing evidence; never adjust the golden to Go. For ASCII patterns with `/i`, do not use Unicode `strings.EqualFold` or RE2 `(?i)` blindly: ASCII-only lowercasing plus exact JS whitespace spans avoids long-s/Kelvin false matches. Preserve JS UTF-16/WTF-8 strings through existing helpers. No custom runtime/entropy/config seam is exposed.

- [x] **Step 4: Observe GREEN and run scoped qualification.** Controller runs targeted proofview/demofixture tests, scoped race tests, vet and build, then corpus `--check` and a fresh independent `--recapture`. Run a compiled targeted test binary from a fresh empty cwd with `PATH` empty (use an absolute corpus path supplied by a test flag/helper rather than reading production testdata). Cross-compile the same test binary for all six native targets in separate bounded commands, with `CGO_ENABLED=0`; run the Darwin ARM64 binary locally. Mark cross-compilation as compilation only, not native execution. Keep source CI definitions, coverage gate and native profiles unchanged. Actual exact-head hosted runs own broader/native gates that cannot fit the local cap. Consume every completed stdout/stderr/receipt fully; retain failures.

- [x] **Step 5: Correct program status, task review and commit.** Status rows say 5c integrated via PR217/exact merge and 5b.2 library-only; genuine RERUN, full judge-demo/plain serve and remaining slices are still outstanding. Independent task reviewer checks full diff/spec/quality and exact evidence, including numeric/regexp/WTF-8 edges, source pins, nonmutation, no new dispatch/routes and protected drift. Resolve blocking issues using RED/GREEN and scoped re-review. Controller commits scoped files with personal identity, attribution and intact hook after verification.

## Whole-branch acceptance and delivery

- [ ] Broad independent branch review covers both tasks against this spec; one focused correction/re-review cycle addresses verified findings. Do not confuse metadata/cross-builds with semantic/native/security evidence.
- [ ] Controller verifies current branch/base/tree, protected sources, prior corpus immutability, clean tracked state, personal remote/identity, exact artifact/corpus bindings and unchanged gate definitions. Do not use old 5c CI as new branch qualification.
- [ ] Under standing delivery authorization, push the new branch once and open a draft PR as Mizore66, with the required attribution footer. Only `GH_TOKEN=$(gh auth token -u Mizore66) gh ...` is allowed; first API identity must print Mizore66. Failed push stops/report; no fallback/force/auto/admin. Preserve all workspaces.
- [ ] Source-bind exact-head hosted jobs and qualify actual scoped outputs before any normal merge decision. Carry existing NONBLOCKING Zod GHSA and unexplained original Darwin hang accurately. No release/protected unfreeze/retirement/cleanup.

## Local task closure (2026-11-02; hosted acceptance pending)

Task 1 evidence was independently reviewed and committed at `28056d1b00f88a4eb4e9e90fc0196e620fe92517`. Task 2 received independent SPEC-compliant / QUALITY-Approved verdicts with no blocking findings, then committed at `d2781ab346e6ee0a29ff24a2f352f3c3fd7dd642` using the personal identity and intact pre-commit hook. The controller verified the six reviewed file bindings, unchanged protected paths/gates/dependencies and all 2,833 prior corpus paths before staging. Whole-branch review and new exact-head hosted acceptance remain separate requirements above.

Retained evidence is in this plan's workspace, especially `task-2-controller-evidence.md` and `task-2-review-report.md`. Accepted feature RED `1793607350509603000` was only undefined `RenderIncidentPage`; the earlier illegal-BOM infrastructure failure `1793607041098259000` was preserved and corrected before production. GREEN `1793607580956794000`, scoped regressions/race/vet/build `1793607590805832000`, Darwin ARM64 empty-cwd/empty-PATH native execution `1793607626809515000`, five additional target compilation receipts and fresh complete-byte frozen recapture `1793607680566861000` all passed. Commit-time incident/static-preview regression `1793608163194469000` also passed. Cross-builds remain compilation only; static byte parity does not prove browser interaction.

The pre-implementation broad local proofview run failed at its Go 120-second alarm (`1793604306503517000`). A narrow active attachment subcase and static preview passed separately; neither explains the original Darwin capture hang nor establishes broad acceptance. The hosted suite budget and gates were not weakened. Worker no-op shell-boundary violations are disclosed in the ledger and credited as no runtime evidence. Existing NONBLOCKING Zod advisory `GHSA-mrhx-c7xc-m3w5` remains unresolved. Genuine RERUN, full judge-demo/plain serve and later Slice 5 subprojects remain open; no release, protected unfreeze, retirement or cleanup occurred.

## Self-review / recovery

The two tasks share only the corpus schema and case IDs: Task 1 produces frozen data; Task 2 reads it without changing it. Each task is independently reviewable. No production code precedes Task 2 RED. No test claims runtime/browser interaction from byte parity. A missing native platform is not a pass. New execution/HTTP design is deliberately excluded rather than guessed.

Ledger rulings must record any change to these constraints, task interfaces or evidence scope. After compaction, read this plan's ledger first; completed tasks are not re-dispatched. Never restart old CI observers or the cancelled PR217 cron.
