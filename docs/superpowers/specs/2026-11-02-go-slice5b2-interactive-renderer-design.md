# Go Slice 5b.2 — interactive incident renderer

## Intent and program boundary

Port the pure `src/ui.ts:124` `renderIncidentPage(analysis)` function, including its helpers and complete HTML/CSS/browser script, to Go. This is a library deliverable inside the remaining 5b work, not completion of 5b or Slice 5. The established 5b.1 cached-analysis/writer/static-preview contract remains unchanged. PR #217 completed 5c integration at `fd8e0dd8819fea90aa19a2a7241bfd1df8a16909`; both exact-merge main workflows and all 35 source-defined jobs completed successfully. That hosted metadata is not new semantic/native/security clearance.

The user has directed continuous delivery and expressly preauthorized proceeding after implementation plans. This design selects a reversible, parity-only unit under that standing direction; it does not infer approval of a native execution model, mutable-server security policy, protected-source changes, or release.

## Choice and alternatives

**Selected:** port the pure interactive renderer first. It consumes existing ordered analysis values, has no filesystem/process/network side effects, and admits complete byte-for-byte frozen-TS oracle comparisons. It gives later executor/server work a tested consumer without inventing execution provenance.

**Not selected now:** genuine RERUN first. Frozen execution uses Node, time-salted run IDs, measured duration/timestamps, and a Node-version-dependent environment digest. A Node-free replacement needs its own explicit execution/provenance design. The read-only research report is advisory, not an approved design; its proposed normalized comparisons are not an exception to the program's no-masking/full-output rules.

**Not selected now:** full judge-demo/plain serve at once. Those combine renderer, execution, bundle mutation, and unresolved Origin/Host/CSRF, concurrency, write ordering and lifecycle policies. Frozen TS permissiveness is not an approved security policy. No partial CLI dispatch will silently substitute cached behavior for these commands.

## Global constraints

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

## Library architecture

Add `RenderIncidentPage(analysis jsjson.Value) string` to `internal/proofview`. The input is an already constructed, valid DemoAnalysis-shaped ordered value; there is no nil/default analysis overload or new schema validator. Malformed/missing-field inputs outside that contract acquire no new validation guarantee. Unknown state IDs and arbitrary permitted string contents remain supported as in TS. Return the internal WTF-8 string; callers use existing JS-compatible UTF-8 encoding at an eventual transport boundary.

Keep formatting helpers private and incident-prefixed where collision would be possible. Split the source into `incident.go` (dynamic composition and friendly labels) and `incident_template.go` (literal document sections, styles and browser script). Use existing `EscapeHTML`, JS value/string/number helpers and `demofixture.StateLabel` where their semantics match. Do not modify shared page chrome or the static judge preview merely to share visually similar markup: `src/ui.ts` has its own historical template.

The function must not mutate input, consult locale/environment/time, read files, spawn processes, open a browser, start an HTTP server or write a bundle. It renders supplied CACHED or EXECUTED record fields without generating or certifying those records. Rendering retained RERUN analysis is historical-data rendering, not fresh execution.

## Exact rendering contract

Port every interpolation and constant segment, not a visually equivalent page:

- Only the first three contribution rows; every timeline row, minimization attempt and claim; source order retained, including empty collections.
- First stable `PASS_TO_FAIL` transition controls confirmation copy; unstable or reverse transitions do not. Grade A has its exact stronger package copy; other grades use the source fallback.
- All friendly checkpoint/timeline/state-caption/change/reason/attempt/claim mappings and fallbacks; JavaScript non-Unicode regexp ASCII word/digit and case rules plus JS whitespace, rather than Go's different regexp defaults.
- Exact badge label/class mapping, CACHED `saved result`, and JS number-to-string duration plus ` ms` for other records.
- Positive integer `changedLines.toLocaleString()` matches the captured Node default `en-US` decimal grouping, including large binary64 integers. Record and guard the oracle's resolved locale; do not consult Go host locale. Other numeric interpolations retain JS number coercion rather than grouped formatting.
- Exact five-character HTML escaping order; preserve Unicode, NUL, astral characters and lone surrogates through existing JS-compatible string handling.
- Complete HTML/CSS and both embedded browser scripts, including rerun POST/status/reload/error logic and wheel/keyboard/touch/nested-scroll/pin-start behavior, with exact whitespace and trailing bytes.

Historical copy such as `THE FIX CHECKS OUT` and `Checked both ways` remains verbatim even for cached inputs. This is a known frozen UI overstatement, not a new certification claim by the migration. This unit neither publishes a page nor exposes the rerun route. Changing that product copy needs a separate explicit decision rather than a silent parity fix.

## Frozen evidence

Add a separate `difftest/testdata/incident-renderer/` corpus; do not alter existing corpora. Add a test-only generator `difftest/gen/gen-incident-renderer.mjs`. Capture real frozen `dist/ui.js` calls after a fresh build from verified frozen `src/` bytes; never compute expected HTML using the Go renderer. Before capture, verify Node 22.22.2, source-tree equality to the frozen commit, manifest version 0.1.4 and resolved `en-US` number locale. Record source and compiled-module hashes, retained-input hashes and generator hash.

The generator has three modes: initial capture (exclusive-create, refusing existing evidence), `--check` (read-only inventory/shape/hash/case validation without importing TS runtime), and `--recapture` (fresh real calls, full byte comparison, no evidence overwrite). Preserve capture scratch and failed attempts under an explicit controller-owned scratch argument, not shared `/tmp`; do not delete them. No seed/clock override is needed: create REPLAY analysis or parse existing retained RERUN analysis, then call only the pure renderer. Never invoke real RERUN.

Each JSONL row contains exact `id`, `analysisJSON`, `htmlJSON`, and `afterJSON` strings. JSON serialization preserves lone surrogates. Capture the full before/after input and require equality; expected HTML is a complete JSON string, not a hash/substring/projection. Retain a normal UTF-8 baseline HTML for inspection, cross-checked against the baseline row. Baseline metadata binds the exact ordered 24-case list and SHA-256/length of the corpus and HTML; Go tests also validate those bindings. Do not accept a self-consistent baseline alone as proof of provenance: review the generator and fresh recapture as separate evidence.

### Case matrix

The exact IDs, in order, are:

`replay`, `retained-rerun`, `no-regression`, `unstable-regression`, `first-stable-regression`, `empty-collections`, `contribution-truncation`, `unknown-state-labels`, `checkpoint-names`, `timeline-names`, `caption-names`, `verdicts-and-reasons`, `change-names`, `attempt-notes`, `claim-kinds-and-statements`, `grade-a`, `grade-b`, `grade-c`, `escaping`, `unicode`, `lone-surrogates`, `js-whitespace-and-case`, `large-integer-metrics`, `numeric-durations`.

Composite cases may append multiple valid rows to exercise helper branches; full pages still compare exactly. Include all five valid verdicts, four evidence kinds, both execution kinds, all source note/claim mapping branches and unchanged fallbacks. Include JS whitespace (NBSP, BOM and line separators), ASCII/non-ASCII case-fold traps, unknown IDs containing HTML metacharacters, ordinary and scientific JS numeric durations, and large grouped integer metrics. Baseline supplies grade D and cached/non-certified behavior. No fixture-only native execution assertion is permitted.

## Tests and qualification

Write tests before production code and retain the observed missing-feature RED before GREEN. Read JSON envelopes with `encoding/json` only as test metadata, and input/HTML strings through `jsjson.Parse` to preserve WTF-8. Table tests compare complete returned strings for all 24 cases, compare input serialization before/after, and compare the UTF-8 baseline HTML with the same transport encoding as TS. Repeated/concurrent calls must remain deterministic and input-preserving. Add explicit regressions for truncation, unsupported certification inference, label whitespace/case, numeric rendering and escaped hostile strings; whole-page goldens remain the primary parity assertion.

Run read-only corpus checks, fresh recapture, targeted `internal/proofview` and `internal/demofixture` suites, scoped race tests and vet/build. Run narrow Node-free test binaries from an empty working directory and empty PATH. Cross-compile those tests for all six native target pairs (linux/darwin/windows × amd64/arm64); cross-compilation is not native execution. Native execution and the broad suite are established by actual exact-head hosted jobs when local runtime would exceed the aggregate shell cap; never weaken the cap or count exclusions as passes. Existing Ubuntu AMD64 `FAULTLINE_COVERAGE_GATE=1`, native profiles, freeze/security checks and CI definitions remain unchanged.

Use a fresh task reviewer for corpus and implementation, then a broad final branch review. Review JS semantic edge cases, source-binding and test independence, input nonmutation and absence of newly reachable HTTP/CLI side effects. No claim of browser interaction execution comes from static byte parity; no browser redesign or new browser automation stack is introduced.

## Program documentation and exit criteria

Update only the relevant migration-overview and 5a dependency-map status rows to reflect completed 5c integration and the precise 5b.2 boundary. Do not bulk-check historical plan boxes. The new spec/plan record their own status and retained evidence; their existence is not implementation success.

5b.2 is complete only after the renderer and additive corpus exist, exact comparisons and scoped qualification pass, independent reviews are resolved and the branch's integration gates pass. Subsequent 5b work still owns genuine native RERUN/execution provenance, full judge-demo, plain serve, mutable HTTP policy and lifecycle. Later 5d–5l remain separate. Existing NONBLOCKING Zod advisory `GHSA-mrhx-c7xc-m3w5` remains unresolved; the original Darwin capture hang remains unexplained and its diagnostic is not a proven fix.
