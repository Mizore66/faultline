# Native RERUN CLI export — design

- **Date:** 2026-11-04
- **Status:** Approved design after explicit scope, approval-UX and evidence-output choices; remaining design alternatives resolved using the user's standing choice of the recommended option.
- **Baseline:** PR219 merged at `5ae63724d3d3a909eba30e57c75f25fda2625285`; accepted source `ae5ce48339ca7411bb16b6cbcce9f5d6e7444a7d` passed all four hosted workflows before the authorized merge.
- **Parent:** [Native demo RERUN execution/provenance](2026-11-03-go-native-demo-rerun-design.md), [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).

## 1. Deliverable and boundary

Add the first user-facing genuine RERUN surface through `judge-demo --export-only`. Reuse PR219's reviewed native model, real private-child execution, checked analysis and complete native provenance. Do not create another executor or claim that the original JavaScript ran.

Included:

- `judge-demo --native-proposal` for inspection without execution or filesystem output.
- Cached `judge-demo --export-only` using the unchanged REPLAY analysis.
- `judge-demo --rerun-all --export-only --approve-native <digest>` with explicit proposal-bound approval.
- Existing `--output <managed-bundle-directory>` behavior and the default `.faultline/bundles/judge-demo`.
- A verified demo-v2 bundle plus a separate, atomically published native-evidence sidecar.

Not included:

- The interactive `judge-demo` server, browser opening, `/api/analysis`, `/api/rerun`, mutable HTTP, or plain `serve`.
- A general repository/program runner, new sandbox, real provider operation, bundle-schema change, release, or TS retirement.
- A claim that the sidecar is authenticated, remotely attested, or covered by the existing demo-v2 integrity file set.

`src/`, `package.json`, `pnpm-lock.yaml`, workflows, existing corpora, cached REPLAY construction, the native process deadlines, and existing verifier rules remain unchanged.

## 2. Architecture

Keep `internal/cli` as a thin orchestration layer:

1. Parse the existing `judge-demo` options with the established first-occurrence `option`/`hasFlag` semantics.
2. Supply `demofixture.CreateReplayAnalysis()` for REPLAY or `demofixture.RunNativeAnalysis` for approved RERUN.
3. Write through `internal/bundle/demo.Write`, then verify with the existing verifier.
4. Publish the native sidecar only after bundle verification, using `nodefs.AtomicWriteFile`.
5. Print the export summary only after every required artifact operation succeeds.

A focused `internal/bundle/demo/native_evidence.go` owns sidecar serialization, path construction, publication, and stale-sidecar handling. It does not execute analysis or alter the demo-v2 writer/verifier.

No new execution/export service package is introduced. A future interactive server can reuse the same library and sidecar writer after its separate HTTP security/lifecycle design.

## 3. CLI selection and approval

### 3.1 Proposal inspection

`fl judge-demo --native-proposal` writes exactly the complete `NativeWitnessProposal()` value as two-space-indented JSON plus one LF, then exits 0. It constructs no executor, spawns no child, creates no output, and records no approval.

Combining inspection with `--rerun-all`, `--export-only`, `--approve-native`, or `--output` fails before output or execution with:

```text
Native proposal inspection cannot be combined with execution or output options.
```

Unrelated ignored tokens retain the existing parser behavior. This inspection-combination check precedes the export-only/server refusal and every other option-validation step.

### 3.2 Export-only requirement

Every non-inspection `judge-demo` invocation in this slice requires `--export-only`. Without it, fail before approval, execution, bundle output, or server creation with:

```text
not yet ported: judge-demo server
```

The existing CLI failure formatter adds its normal prefix and help hint. `--port` does not enable a server and remains ignored for an export-only invocation, as it is for frozen export-only.

### 3.3 REPLAY

Without `--rerun-all`, export uses the unchanged cached REPLAY analysis and requires no approval. `--approve-native` without `--rerun-all` is rejected before analysis or writes:

```text
--approve-native requires --rerun-all.
```

If both `--rerun-all` and `--replay` are present, the existing `--rerun-all` precedence remains; native approval is required.

### 3.4 Native RERUN approval

RERUN requires a nonempty value from the first exact `--approve-native` token. The three input cases are distinct: an absent flag produces the new diagnostic below; a present token with no following value, an empty value, or a following token that begins with `--` (including the literal `--`) uses the existing `Missing required option: --approve-native` behavior. A nonempty value proceeds to approval construction.

```text
Native RERUN requires --approve-native <proposal-digest>.
```

After option validation, the CLI obtains the caller's authorization wall time once and calls:

```go
RunNativeAnalysis(ctx, NativeApproval{
    ProposalDigest: suppliedDigest,
    ApprovedAt:     clock.ISO(clock.Now()),
})
```

The supplied digest must equal `NativeWitnessProposal().Get("digest").Str()`. Invalid or changed approval is rejected through the existing native `APPROVAL` diagnostic before `NewExecutor` is constructed or any child is executed. The CLI's `ApprovedAt` records only this authorization event. Native entry, invocation, and final timestamps are separate observations; tests must not equate them with `ApprovedAt`, although the existing no-later-than-analysis-entry rule still applies. The CLI flag is the caller-recorded authorization event; it is not human authentication and does not reuse the historical JavaScript approval.

## 4. Bundle and native-evidence sidecar

### 4.1 Demo-v2 output

Use the resolved `--output` value or the frozen default. The existing writer owns managed-root checks, link/special-file refusal, replacement of only a verified existing bundle, staging, rollback, and exact artifact bytes. Verify the resulting directory with the existing verifier before reporting or publishing native evidence.

REPLAY retains the established artifact set, exact bytes, stable-boundary wording, and self-consistency result. Native RERUN retains the existing schema and obtains its dynamic analysis values only from the checked `NativeResult.Analysis`.

### 4.2 Sidecar path and schema

For native RERUN, the sidecar path is the absolute verified bundle directory plus the literal suffix `.native-execution.json`. It is a sibling of the bundle, not a declared bundle file. Its value has exactly this top-level insertion order:

```json
{
  "schemaVersion": "faultline.native-demo-cli-evidence.v1",
  "analysisDigest": "sha256:<canonical-analysis-digest>",
  "bundleRootDigest": "sha256:<verified-bundle-root-digest>",
  "provenance": "<complete NativeResult.Provenance value>"
}
```

`analysisDigest` is computed as `canonical.DigestJSON(result.Analysis)` after `RunNativeAnalysis` has returned its final analysis, including the API's final `generatedAt`. The exact same `result.Analysis` value is passed to `demo.Write`. `bundleRootDigest` is `verification.RootDigest` from `demo.Verify(written.Directory, "", false)` and must equal `written.RootDigest`; publication requires `verification.Valid`.

`provenance` is the actual `faultline.native-demo-execution.v1` object, not a string. These fields let a consumer detect a sidecar/analysis/bundle mismatch instead of trusting a filename. This slice adds no sidecar reader, automatic consumer, or authentication claim.

Serialize the complete value as two-space-indented JSON plus one final LF. Before publication, inspect an existing destination with a no-follow `Lstat` and reject symlinks, directories, and special files; only an absent destination or regular non-symlink file proceeds. Publish with `nodefs.AtomicWriteFile`: an exclusive fresh sibling stage, complete write/fsync/close, rename, best-effort directory sync, and owned-stage cleanup. The final file requests mode `0600`, deliberately more restrictive than ordinary bundle files because it contains raw invocation paths, environment observations, and receipts. On Windows this is a native file-access request, not a fictional POSIX ACL guarantee; the same suffix and no-follow type checks apply on every platform. The bundle's already-checked parent supplies the managed-directory boundary. This is not a claim of race-free path confinement.

### 4.3 Failure ordering and stale evidence

For RERUN:

1. Approval/execution failure publishes neither bundle nor sidecar and prints no success lines.
2. Bundle write/verification failure publishes no new sidecar and prints no success lines.
3. Sidecar publication occurs only after bundle verification. If it fails, attempt to remove the reserved destination using the same regular-non-symlink-only rule so an old or partially published sidecar cannot silently remain beside the new bundle. Return the publication error joined with any removal error, and print no success lines. The verified new bundle may already have replaced the old one; do not claim cross-file atomicity or secretly delete that bundle. A symlink, directory, or special-file refusal is not recursively cleaned up.
4. Only after sidecar publication print all summary lines.

For both RERUN failure cleanup and REPLAY stale removal, an absent destination is a successful no-op. Only a confirmed not-exist result is ignored; every other inspection or removal error fails.

For REPLAY, no native sidecar is produced. After the replacement bundle verifies, remove a reserved existing sidecar so it cannot be mistaken for evidence of the new cached analysis. Removal is limited to a regular non-symlink file using `nodefs.Unlink`; directories, links, and special files fail rather than being recursively deleted. A removal failure returns an error and no success lines, although the verified REPLAY bundle may already be published.

### 4.4 Successful output

REPLAY preserves the frozen five lines: prepared mode, stable-boundary qualification, verified declared-file count, bundle root digest, and absolute bundle path. Native RERUN uses those same lines with actual native values and appends exactly:

```text
Native execution evidence: <absolute-sidecar-path>
```

No success text is printed before all required artifact operations finish. Proposal mode prints only the proposal JSON.

## 5. Files and ownership

Expected production scope:

- New `internal/cli/judge_demo.go`: option selection, approval, export orchestration, and exact output.
- Modify the single `judge-demo` dispatch in `internal/cli/cli.go`.
- New `internal/bundle/demo/native_evidence.go`: sidecar envelope and safe publication/stale handling.
- Focused tests beside both production files and native CLI process/integration tests.
- `difftest/KNOWN_DIFFERENCES.md`: the new inspection/approval/export surface, sidecar schema/path, and existing native execution distinctions.
- The existing native coverage collector and its adversarial tests, only as needed to bind the two new production files to freshly executed diagnostics and counters.
- Migration status documentation only when the corresponding acceptance evidence actually exists.

No change is authorized to the three existing native analysis files merely to move CLI responsibilities into their coverage ownership. No old corpus, frozen source, workflow, release file, or server implementation is included.

## 6. Verification and acceptance

### 6.1 CLI and approval

Test exact stdout, stderr, and exit status for proposal inspection, forbidden combinations, server refusal, missing/empty/wrong approval, REPLAY export, and successful native export. Verify first-option precedence, ignored tokens, `--rerun-all`/`--replay` precedence, default/custom outputs, and no success output on failure. Missing/wrong approval must fail before any child or output write.

The successful native CLI test uses the real private child and actual clock/UUID paths. Validate 39 ordered receipts, the current proposal approval/digest, actual environment/executable observations, complete catalog links, analysis digest, verified bundle root, and the complete sidecar value. Existing native contract tests remain the full semantic audit; CLI tests prove that the exact library result reaches both output artifacts.

### 6.2 Export safety and failure behavior

Cover default and nested outputs, existing verified replacement, invalid existing output, link/special-file refusal, sidecar regular-file replacement, sidecar directory/link refusal, stage/write/rename failures, and REPLAY stale-sidecar removal/refusal. Assert the resulting filesystem as well as the error. In particular, distinguish execution failure, bundle failure before sidecar publication, sidecar failure after verified bundle publication, and stale-removal failure after REPLAY publication.

REPLAY continues to match its retained full 69-file bundle. The sidecar must not appear in `hashes.txt` or `ROOT.sha256`; independently verify its complete bytes and explicit analysis/bundle links. Mutate each digest in test copies and require the independent linkage check to reject the mismatch; this validates detectability without inventing a production reader. Exercise failed sidecar publication with successful and failed reserved-file removal, requiring joined errors and no success output. No mask, accept-either output, length-only comparison, or sidecar exemption is allowed.

### 6.3 Coverage and regression gates

Extend the fresh native evidence integration only to `internal/cli/judge_demo.go` and `internal/bundle/demo/native_evidence.go`, in addition to the existing three native analysis files. Add those exact two paths to `nativeCoverageFiles`; do not move any CLI/writer code into the original three files.

Generalize the existing collector's single-package expectations into an explicit package/parent-test allowlist: the existing demofixture parents plus `TestJudgeDemoNativeDiagnostics` in `internal/cli` and `TestNativeEvidenceDiagnostics` in `internal/bundle/demo`. Those tests use their own exact error assertions and emit the same `NATIVE-DIAGNOSTIC <json-string>` records only after each emitting case passes. The `go test -json -coverprofile` invocation includes all three packages, and the existing event/profile validators require the named parents and emitting children to pass in their own packages. Native-only text and positive profile counters are then accepted for each exact owned path at every non-exempt error site, with unchanged before/after source bindings. No production diagnostic rewrite or instrumentation hook is added.

Keep all cached CLI goldens and replay counters for unrelated files. Add adversarial cases for wrong package/emitter, missing parent, renamed test, skipped/failed test, missing literal, zero counters, stale exemption, and foreign counter/text ownership. Do not add these files to `liveGatedFiles`, blanket-exempt new errors, or weaken the original literal/source-position obligations.

Run affected ordinary/tagged suites, native lifecycle and full live-oracle groups, focused coverage gates, race, vet, and diff/scope checks. Preserve the prior deadline and timing-sensitive nonreproduction records rather than calling them fixed by this CLI work.

### 6.4 Node-free and platform evidence

Build the actual `fl` binary and run proposal plus successful native export from a fresh empty working directory outside the repository, with a controlled PATH lacking Node and the Go compiler and no runtime TS/dist/testdata/package manifest. The self-executed native child retains its existing minimal environment and fixed executable path. Verify complete output artifacts, not only process creation.

Cross-build all six targets as compilation evidence only. Require actual Linux/macOS/Windows AMD64/ARM64 execution of the new CLI/native tests through the source-defined hosted jobs. Obtain independent spec/quality review and exact-head CI before any separate publication or merge decision. Skips, failures, and incomplete jobs remain explicit.

## 7. Completion and authorization

This slice is complete only when the CLI contract, exact bundle/sidecar evidence, safety/failure behavior, coverage ownership, Node-free checks, independent review, and all six native executions are accepted. It does not complete interactive RERUN, full `judge-demo`, plain `serve`, all of Slice 5, or release readiness.

Writing this spec does not authorize implementation, a push, a new PR, a merge, a release, protected-source edits, or security-control changes. Those remain separate decisions except where the user later gives specific authorization.
