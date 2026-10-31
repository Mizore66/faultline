# Go migration slice 5b.1 — cached demo artifacts and static judge preview

- **Date:** 2026-10-30
- **Status:** Artifact-first split and this written specification separately approved by the user on 2026-10-30. Their initial “approved” authorized the detailed implementation plan, including the Section 5.3 refusal policy; subsequent “approved-1 with parallelization” authorized implementation, not publication. **Execution status (2026-10-31): local implementation, independent task/whole-branch review, the one final fix/scoped recheck and bounded post-fix integration are complete; full acceptance remains pending.** The plan was executed on branch `go-slice5b1-design-20261030` with raw pinned-TS capture before the dangling-output guard (TS followed the link; Go refuses by approved policy), additive evidence in `difftest/testdata/demo-artifacts`, permanent replay, live/fault tests and CI registration. Nothing is staged, committed, pushed or published. Actual post-fix results, explicit skips, retained process deviations and long/full-tagged gaps, and the unexecuted hosted, six-native, positional, canonical pnpm/tsx and native Linux+strace gates are recorded in the [plan's status paragraph](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md). Local completion is not full subproject acceptance. Genuine RERUN execution, the interactive renderer, full `judge-demo` and plain `serve` remain open follow-up work.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md) and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** Merged [PR #215](https://github.com/Mizore66/faultline/pull/215), commit `647440884a6668042f7726f2cbade38886d00a41`. Its reviewed head `707a28a8691d5dd8a13f3b94faa6f819ac34a406` passed hosted acceptance; this is not a fresh CI claim for the merge commit.
- **Oracle:** Frozen source `ce99a793af0b020bf1b9cf21114f8d7897b37207`, Node 22.22.2, current-tree manifest `0.1.4`. Source-pin manifest `0.1.3` and the observed live submission tag are distinct provenance; retain the [5a distinction](2026-10-30-go-slice5a-cli-surfaces-design.md#3-version-provenance-and-standalone-behavior).

## 1. Deliverable and boundaries

This first subdivision of 5b delivers deterministic cached analysis, demo-v2 bundle writing and the complete static `judge-preview` command. It is independently useful and testable without solving live execution or mutable HTTP serving.

| Included | Completion boundary |
| --- | --- |
| Cached fixture and REPLAY analysis | Reconstruct the frozen fixture, witness, cached records and analysis with exact values, ordering and digests; no process execution |
| `faultline.proof-bundle.v2` writer | Write supplied demo analysis with exact artifacts, output safety, verified-existing-output replacement and failure/rollback semantics |
| `judge-preview` renderer, file writer and CLI | Exact static HTML and command behavior, including parser quirks and the explicitly approved safety difference |

**Not included:** `judge-demo`, even cached `--export-only`; plain `serve`; genuine RERUN execution; `src/ui.ts`'s interactive incident page; `/api/analysis` or `/api/rerun`; server/browser/signal lifecycle. Those command dispatches remain explicitly not yet ported, rather than offering a partial command that silently disables RERUN or serving. No public demo-export command is added to expose the library writer.

The remaining 5b follow-up owns the real executor, execution/environment provenance, interactive renderer, full `judge-demo` and plain `serve`. It must separately settle mutating-server authorization, Origin/Host/CSRF policy, mutation/write failure ordering, concurrency and HTTP/lifecycle parity. No acceptance of the TS server's cross-origin mutation behavior, generic HTTP-wire exception or native-execution strategy is implied here.

Other ownership remains unchanged: 5c owns `serve --bundle` and attachments; 5d the UI hub; 5h tutorial/live-git/full demos; 5j intentional help/doctor/hook/hint changes; 5k/5l distribution and retirement. Preserve frozen preview copy, including Node/pnpm references, rather than redesigning the page or applying a generic visual style guide.

## 2. Architecture and interfaces

Use the existing JS-value, filesystem and exception conventions. Go 1.27 and the standard library are required. Do not use `encoding/json` on digest paths or substitute ordinary Go maps/sorting for JS insertion order and ICU collation.

| Component | Responsibility and interface boundary | Dependencies |
| --- | --- | --- |
| New `internal/demofixture` | Pure fixture lookup, frozen witness and cached REPLAY analysis construction; return ordered `jsjson.Value` data and label lookups, with no mode-selecting RERUN API | `canonical`, `jsjson`, JS string semantics |
| Existing `internal/bundle/demo` | Add the writer and its result containing absolute directory, canonical manifest digest and bundle root digest; take supplied ordered analysis and an optional proof root, defaulting to `.faultline/bundles` under cwd | Existing package-private coverage/path/verifier helpers; `canonical`, `nodefs`, `safedir`, `clock` |
| Existing `internal/proofview` | Add preview render/default-path/write functions beside private incident-page chrome; render a supplied analysis or the default cached fixture and return path plus UTF-8 byte count when writing | `demofixture`, existing HTML escaping/chrome, `nodefs`, `safedir` |
| Existing `internal/cli` | Thin `judge-preview` handler using existing option helpers, output and failure formatting | Preview writer only; no server/browser dependency |

The dependency direction is CLI → preview → fixture; the bundle writer accepts data without depending on CLI, preview or a live runner. Keep complete ordered values at the writer boundary: schema parsing that strips properties or reconstructs key order must not silently change supplied artifacts. The detailed plan will fix concrete function signatures and focused source/test files within these boundaries.

Keep the demo writer in the demo package so its private verifier helpers need not become general public APIs. The existing Git writer deliberately omits replacement of existing demo outputs; it is not a complete safety implementation for this slice. Port the missing demo-specific behavior without broad writer/server refactoring. Do not extract an HTTP framework or export page chrome merely for this preview.

Production data is constructed/embedded in Go. It must not read `src/`, `dist/`, repository testdata, a package manifest or Node at runtime. Use existing `clock` UUID support for staging names and existing test-only facilities for deterministic/fault tests; no production seed or filesystem-fault override is added.

## 3. Cached fixture and provenance contract

### 3.1 Values and semantics

Port the REPLAY branches of `src/fixture.ts` and `src/engine.ts`, including the incident packet, 20 fixture states, contribution/timeline order, frozen witness, cached run creation, transition detection, six minimization attempts, prevention data, grade, claims and warning. Do not implement the generated `.mjs` programs or `executeFixtureState` in this slice.

Preserve state digest input construction, witness digest input without its appended digest, and cached run IDs derived from `{ state: input.id, witness: witness.digest, replay: true }` (the state ID string, not the whole fixture-state object). Use canonical JSON for digests, but ordinary ordered JS JSON for the record identity/conflict checks that TS performs with `JSON.stringify`. Deduplicate identical records by ID, reject conflicting records with the frozen collision error, and locale-sort the catalog by ID.

The resulting analysis has 20 unique catalog records, each `executionKind: CACHED`, `durationMs: 0` and `executedAt: 2026-07-14T09:05:00.000Z`. `generatedAt` has the same fixed value. Cached transitions are not stable; minimization termination is `NOT_EXECUTED`; prevention is not verified; the grade is D. Preserve all original claim kinds, warning text and fixture terminology. An ERROR/compile-error cached record is still cached data, not an executed compiler observation.

Construct top-level analysis properties in this order: `schemaVersion`, `mode`, `generatedAt`, `fixture`, `witness`, `metrics`, `contributionRuns`, `timelineRuns`, `transitions`, `runCatalog`, `minimization`, `prevention`, `grade`, `claims`, `warning`. Preserve nested insertion order and optional-property presence too. Canonically equal JSON alone is not sufficient for pretty artifact parity.

### 3.2 Historical environment, not the Go host

Frozen TS derives the fixture environment digest from `process.version`, including for REPLAY. For this cached artifact, Go uses the explicit historical environment captured under Node `v22.22.2`:

| Field | Historical value |
| --- | --- |
| `runtime` | `v22.22.2` |
| `packageManager` | `none` |
| `runner` | `built-in-sample-v1` |
| `network` | `disabled` |
| `credentials` | `redacted` |

This yields `sha256:7de997bc873dc0e1909a6aa3bf7a5a40fe4958fbb0bb66b3c624e4c75074d334`. It is cached fixture provenance, never a measurement of a current Go runtime or proof that Node ran on the current host. Do not probe Node, substitute the Go version, make this environment user-configurable, or relabel cached records EXECUTED.

During implementation, document this narrow runtime-provenance distinction in `difftest/KNOWN_DIFFERENCES.md`: Go retains the declared historical cached environment, whereas TS recomputes its fixture digest from its running Node version. Ordinary live acceptance remains pinned to Node 22.22.2 and compares exact digests; no runtime/digest normalization is allowed. The future real-execution spec owns fresh runtime provenance.

### 3.3 Retained artifact baseline

`difftest/testdata/bases/demo-replay/` is already the retained frozen-TS artifact oracle. It contains 67 declared files and 69 physical files including `hashes.txt` and `ROOT.sha256`.

| Identity | Exact value |
| --- | --- |
| Bundle root | `sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa` |
| Analysis digest | `sha256:3abe08e047655ce6b42bac9e50d847a750f4d5b76d9ccacffdc986c96e6b0863` |
| Witness digest | `sha256:3d0d826bb1fd9901b290475201303c560a961df34e2faa374cc66310cb70327c` |

Research statically verified the declared hashes, root, counts, cached labels and historical environment. That is not implementation execution evidence. Go acceptance must newly construct the analysis and bundle and compare every file's full bytes and the exact file set against this unchanged base. A root-only, length-only or canonical-JSON-only comparison is insufficient.

## 4. Demo-v2 writer contract

### 4.1 Supplied analysis and emitted bytes

Port `writeProofBundle` and its report/instructions from `src/proof-bundle.ts`. The writer consumes supplied analysis; it does not run or certify evidence. It can serialize retained historical RERUN analyses as inputs without implementing RERUN, changing their execution labels or claiming fresh execution. Preserve the TS writer's validation boundary: check catalog coverage/duplicate IDs, but do not add a full pre-publication schema/semantic verification step that TS does not perform.

The manifest preserves the frozen order and values of `schemaVersion`, `investigationId`, `fixtureId`, `generatedAt`, `analysisDigest`, `witnessDigest`, `environmentDigest`, `mode`, `integrityScope`. Its environment digest comes from `analysis.prevention.firstBad.environmentDigest`. Keep schema versions `faultline.demo.v1` and `faultline.proof-bundle.v2` unchanged.

| Artifact group | Exact encoding |
| --- | --- |
| `manifest.json`, `analysis.json`, `witness/witness.json`, `minimization/attempts.json`, `prevention/three-state.json`, each `runs/<safe-id>/result.json` | `JSON.stringify(value, null, 2)` semantics plus one LF, preserving insertion order |
| Each run's `stdout.log` and `stderr.log` | Exact JS-string-to-UTF-8 bytes, including replacement of lone UTF-16 surrogates as Node encodes them |
| `report.md`, `VERIFY.md` | Frozen Markdown bytes, with no appended final LF |
| `hashes.txt` | All declared file paths in frozen `localeCompare` order; each line `<64 lowercase hex>  <relative path>\n` |
| `ROOT.sha256` | `sha256:` plus SHA-256 of exact `hashes.txt` bytes, then one LF |

The two integrity files do not list/hash themselves. `manifestDigest` hashes the canonical manifest value, not its pretty on-disk bytes. Keep `safeRunFileName`'s replacement per UTF-16 unit, not per Unicode scalar. Detect sanitized path collisions with the frozen duplicate-path error. Check the expected declared file set exactly.

### 4.2 Output safety and validation order

Resolve output and optional proof root with existing Node-compatible/trusted-system-path handling. The default root is `<cwd>/.faultline/bundles`. Output must be a strict child of the managed root, not the root itself or an escaping sibling. Preserve TS's order of directory creation, output checks, coverage validation and staging; a refused operation can already have created managed parent directories.

Reject user-controlled symlink/non-directory ancestors and links/special files inside an existing output. Only verified macOS system aliases `/var` → `/private/var` and `/tmp` → `/private/tmp` receive existing `safedir` treatment; do not resolve arbitrary links to make them appear safe. Existing output must be a valid demo-v2 bundle under the existing verifier before replacement, otherwise fail with `Proof bundle output can only replace a verified FaultLine bundle: <output>`.

Reuse existing validation/error formatting, including `Cannot write incomplete proof bundle: ...`, duplicate catalog IDs, missing referenced runs, `Proof bundle attempted to write the same path twice: ...` and the expected-file-set error. Preserve reachable failure precedence and side effects, not merely one message substring. Do not weaken the verifier to accept new writer output.

### 4.3 Stage, publish, rollback and cleanup

Stage beside the output as `<output>.staging-<UUID>`. Write artifact files, then the sorted hash list and root. TS does not verify this stage before publication; `judge-demo`'s later verification is not part of this library call and that CLI remains unported.

For replacement, allocate `<output>.backup-<UUID>`, rename an existing output aside, then rename the stage into place. If stage publication fails and the old output was moved and output is absent, attempt to restore the backup. On successful publication remove the old backup recursively. On writer failure attempt to remove the remaining stage and propagate the error using existing exception conventions.

Tests must distinguish failure before publication, failed publication with successful rollback, rollback failure, and backup-removal failure after publication. Do not promise that every failure leaves the old output in place: a cleanup failure can occur after the new output is installed, and restoration itself can fail. A failed restoration replaces the publication error; a failed stage removal in the outer catch can replace the error it was cleaning up. A backup-removal error propagates after publication. Preserve these resulting filesystem states and error precedence, rather than aggregating errors into new output. Random names do not affect successful bundle bytes; fault captures can use the existing test-only deterministic UUID stream, not a new runtime override.

## 5. Complete static judge-preview contract

### 5.1 Rendering and honesty boundary

Port `src/judge-preview.ts` using existing incident-page chrome. The renderer defaults to the pure cached analysis and rejects any supplied non-REPLAY analysis with `Judge preview only renders the deterministic REPLAY fixture.` Preserve fixture label fallback, first pass-to-fail boundary/no-boundary copy, three-state rows, claim order, HTML escaping and all shared chrome/CSS bytes.

The page is self-contained and static: no script, API request, browser launch, server or embedded fresh-execution claim. Preserve every frozen warning and the final footer. Do not use the separate interactive `src/ui.ts` renderer. The checked-in `docs/judge-preview.html` is a reference candidate only: its bytes/size must be compared with a fresh pinned-oracle capture before it can be identified as matching. Do not replace it or adopt it as the capture without that verification.

### 5.2 CLI selection and successful output

Default output is `<cwd>/docs/judge-preview.html`; no repository discovery is involved. Use the exact first-occurrence option semantics from `src/cli-app.ts`, not a new argument parser:

| Arguments after `judge-preview` | Behavior |
| --- | --- |
| No exact `--output` token | Default output; unrelated tokens, including `--output=<path>`, do not select an output |
| First `--output` followed by an ordinary nonempty value | Resolve that first value; later duplicates and unrelated suffixes are ignored |
| First `--output` is the final token | `option()` is undefined, so use the default path; do not invent a missing-value error |
| First `--output` followed by an empty string or a token starting `--` | Existing `Missing required option: --output` error |

The normal writer resolves the path, requires a case-insensitive `.html` extension, validates/creates parent directories, checks output type, renders and writes. On success exit 0, empty stderr and exactly these four stdout lines, each ending in LF:

```text
FaultLine deterministic static judge preview written.
Preview: <absolute resolved output path>
Bytes: <decimal UTF-8 byte length of the exact page>
Limitation: this read-only replay snapshot is not a live Docker proof, a verified proof bundle, or a record of a fresh execution.
```

Errors use the existing CLI failure formatter and exit behavior. No success lines are printed before a successful write. Do not change the current `Next: pnpm fl help` hint or add flags. Unknown top-level commands and all other dispatches remain unchanged.

### 5.3 Filesystem policy and approved narrow safety difference

Require `.html` before creating directories. Create missing parent segments mode `0700`, subject to platform/umask behavior, using existing real-directory checks and only the trusted macOS system aliases. Refuse a non-directory or user-controlled symlink parent. Preserve exact messages:

- `Judge preview output must be an .html file.`
- `Judge preview output must use a real directory: <path>`
- `Judge preview output cannot traverse a symbolic link or non-directory: <path>`
- `Judge preview output must be a regular non-symlink file: <path>`

Existing regular files are overwritten in place, not atomically replaced. New files request mode `0644`; existing file modes are retained. Do not add backup/staging semantics to this single-file writer or silently change overwrite behavior.

**Approved Go safety difference:** refuse an output symlink whether its target exists or is dangling. TS guards `lstatSync(output)` with `existsSync(output)`, which raises a source-backed concern that a dangling symlink can evade the type check and be followed by `writeFileSync`. No runtime reproduction has been performed during design. The Go implementation will use an explicit no-follow output-entry check, treat only a genuinely absent entry as new, propagate other filesystem failures, and apply the existing regular-non-symlink error to dangling links.

Before implementing that guard, capture actual pinned-TS behavior in an isolated temporary fixture whose link target is also inside that fixture; retain exact output/exit/filesystem results. Add a Go native regression requiring refusal and proving the missing target remains absent and pre-existing fixture contents unchanged. If TS follows the dangling link, record this precise divergence in `difftest/KNOWN_DIFFERENCES.md` with the retained oracle case; if the runtime already refuses it, retain that result and assert refusal without inventing a divergence. Do not normalize the two outcomes into a match or silently overwrite the TS capture. The approved Go policy is refusal in either case; implementation remains gated on the separate plan/production authorization.

This is an output-entry safety check, not a claim of race-free path confinement. It does not introduce a general hard-link policy or authorize unrelated filesystem hardening. Full parent-race/open-handle guarantees would require a separate design rather than being inferred from `lstat`.

## 6. Permanent oracle evidence and verification

These are implementation acceptance requirements, not tests run while writing this spec. No new blanket masks, alternate accepted outputs, length-only comparisons, Node-availability skips or broad coverage waivers are allowed.

### 6.1 Capture ownership and provenance

Add a dedicated `difftest/gen/gen-demo-artifacts.ts` and additive evidence under `difftest/testdata/demo-artifacts/`: `baseline.json`, `judge-preview.html`, and `golden.jsonl`. The generator imports the actual frozen fixture/engine/writer/preview modules for library cases and invokes the real TS CLI for command cases; do not derive expected output from Go. Capture writer cases with complete output file-set/content evidence, reusing unchanged retained bases by explicit reference where appropriate.

The baseline metadata declares `schema: faultline.demo-artifacts-oracle.v1`, `sourceCommit: ce99a793af0b020bf1b9cf21114f8d7897b37207`, `manifestCommit: 647440884a6668042f7726f2cbade38886d00a41`, `manifestVersion: 0.1.4`, `nodeVersion: 22.22.2` and `cachedRuntimeVersion: v22.22.2`. The source identity and manifest commit are distinct: capture builds current-tree TS with unchanged frozen sources, not a tag checkout. The existing frozen-source guard/no-diff invariant remains binding.

Before writing evidence, fail on a different actual Node version, manifest version or intact CLI version (`FaultLine 0.1.4\n`). Also validate the cached environment digest against Section 3.2. Do not silently adopt an oracle drift as a new baseline. Two fresh captures must produce identical committed evidence. Dynamic errors use only existing narrowly identified root/temporary-path handling; deterministic fault UUIDs use existing test-only seeds. No analysis field, digest, timestamp, arbitrary HTML or artifact string is generically masked.

Add generation to the existing no-drift CI gate and live comparison to the existing oracle jobs without replacing old corpus paths, weakening source guards or editing protected files. Permanent Go replay validates metadata and compares the built Go implementation directly to retained evidence without Node, TS files, a manifest or Git history.

### 6.2 Native and differential coverage

| Area | Required assertions |
| --- | --- |
| Fixture/analysis | Exact analysis values and pretty bytes, all IDs/digests, 20 CACHED records, catalog order/deduplication/conflict handling, unstable transitions, non-executed minimization/prevention/grade/claims |
| Writer success | Full 69-file REPLAY equality; canonical manifest return digest; Go and frozen TS verification of valid Go-written bundles; exact serialization of supplied historical RERUN and Unicode/lone-surrogate inputs without executing a fixture |
| Writer refusal/faults | Outside/root output; symlink/non-directory ancestry and existing links/special files; invalid existing bundle; complete valid replacement; missing/duplicate catalog IDs and sanitized collisions; staging/write/rename/rollback/cleanup failures with resulting trees checked |
| Preview render/write | Complete captured HTML, REPLAY guard, HTML escaping/label fallback and absent boundary, exact UTF-8 count, extension/case rules, new/nested/overwrite output, type/parent/link refusal and approved dangling-link policy |
| CLI/parser | Exact stdout/stderr/exit plus written page; default/custom paths, ignored tokens, first duplicate, trailing bare flag, empty/next-flag error, extension/write failures, dispatch isolation |

Use meaningful reachable inputs and existing test-only filesystem fault seams rather than synthetic panics solely to tick coverage. Deliberately invalid supplied analyses are not required to become valid bundles: compare the writer's actual validation/emission boundary and subsequent verifier rejection with TS, without adding pre-publication checks. Any genuinely unreachable literal requires individual source-backed adjudication through the existing positional gate; this spec grants no new exemption list.

Implement permanent replay in focused `difftest` tests (for example `demo_artifacts_test.go`) and pinned live comparisons in their corresponding live test file. A minimal library harness may exercise non-CLI writer/renderer APIs using established differential conventions; it must be test-only and must not become a public export command. The detailed plan owns exact signatures, selectors and test fixture schemas.

### 6.3 Node-free execution and six native targets

Run the actual built `judge-preview` binary outside the repository, with Node/pnpm absent from a controlled PATH and no runtime source/testdata/package manifest available. Check the page and full stdout/stderr/exit, not merely successful process creation. Direct fixture/writer/renderer tests likewise run without invoking Node or Git. Preserve historical literal Node/pnpm text as content; it is not an executable dependency.

New core tests must be portable and untagged where possible, with affected `faultline_test` variants and permanent replay also covered. Native execution is required on linux/darwin/windows × amd64/arm64; cross-compilation is not evidence of execution. Follow platform filesystem behavior and existing narrowly justified eligibility for symlink/special-file/permission probes, recording unexecuted cases as such rather than passes. No new Node-dependent skip is acceptable for core native evidence. Existing Linux-only fault/positional gates remain additional Linux evidence, not six-platform coverage by assertion.

Preserve the release no-test-override guard, all prior goldens, existing no-new-skip/coverage gates and existing command behavior. Keep the Zod advisory and Docker applicability qualifications separate: successful artifact/preview checks do not resolve dependency security or establish any Docker execution.

## 7. Completion and authorization

The implementation is complete only when exact cached analysis/bundle/preview/CLI comparisons, declared safety differences, rollback/fault tests, additive no-drift/live evidence, Node-free checks and all six native targets have passed with independent implementation review. Preserve failures, skips, incomplete attempts and source provenance. Update the roadmap to mark only 5b.1 complete, leaving real RERUN and server follow-up explicitly open.

This written specification was self-reviewed and separately approved by the user on 2026-10-30, including the Section 5.3 refusal policy. The [detailed implementation plan](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md) was approved and executed in the working tree (see the status above: implementation present, local verification incomplete, review and hosted acceptance pending). Commit, push, PR, merge, release and protected-path/unfreeze/deletion permissions remain separate and unexercised. Do not auto-commit these documents.

Keep `src/`, `package.json` and `pnpm-lock.yaml` unchanged. No dependency upgrades, real provider calls, real-looking secrets, evidence/worktree cleanup or global git/account changes are authorized. Use `fl-test-key` for any necessary scripted fake key. Preserve personal Mizore66 identity and normal TruffleHog hooks for any later authorized publication, with the required commit/PR attribution. Every shell invocation stays under five minutes total, with owned process-group cleanup and no detached/watchdog-reset workaround, broad cleanup or silent retry. Local tests/builds/capture remain serialized under explicit HOST ownership when implementation begins.
