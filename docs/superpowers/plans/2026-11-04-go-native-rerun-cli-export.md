# Native RERUN CLI Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver proposal-approved native RERUN through `judge-demo --export-only`, with a verified demo-v2 bundle and a digest-linked native execution sidecar.

**Architecture:** A thin `internal/cli` handler composes the existing cached/native analyses and demo-v2 writer. A focused demo-package sidecar writer validates the verifier result and atomically publishes complete provenance; no server or second executor is introduced.

**Tech Stack:** Go 1.27, standard library, existing `jsjson`, `canonical`, `clock`, `nodefs`, `demofixture`, and `bundle/demo` packages. No new production dependency or Node runtime.

**Spec:** `docs/superpowers/specs/2026-11-04-go-native-rerun-cli-export-design.md`

## Global Constraints

- Preserve `src/`, `package.json`, `pnpm-lock.yaml`, workflows, old corpora, cached REPLAY bytes, the existing demo-v2 writer/verifier, and the native two-second/250ms/90-second execution limits.
- No interactive server, `/api/rerun`, plain `serve`, general runner, real provider operation, release, or TS retirement.
- Work in place on `go-native-rerun-cli-export-20261104`, based on merged main `5ae63724d3d3a909eba30e57c75f25fda2625285` and spec commit `6d382172010a4c3fcaeb05b8d5a5131387f300f7`. Do not create a worktree or switch accounts/remotes.
- All shell/build/Git/network/evidence operations are serialized through the unchanged runner below, with a per-invocation aggregate deadline of at most 240 seconds and closed stdin. No detached work, watchdog reset, broad cleanup, silent retry, or historical artifact deletion.
- TDD: retain actual missing-feature RED before each implementation and run the same behavior GREEN afterward. Preserve failures, skips, and narrowed-test limitations. Cross-compilation is not native execution.
- No mask, accept-either output, length-only acceptance, tolerance widening, production entropy override, gate exemption, or skipped-test substitution for a pass.
- Every local commit uses personal identity and the intact TruffleHog hook. Stage only the task's reviewed paths; independently scan staged bytes and verify the hook's newly appended clean scan.
- No push, new PR, merge, release, protected-source change, or publication is authorized by this plan.

Runner prefix for every command shown as a payload:

```sh
python3 -I /Users/anasqumhiyeh/Documents/faultline/.superpowers/sdd/2026-11-02-go-slice5b2-interactive-renderer/run.py \
  --name UNIQUE-NAME --seconds 240 --cwd /Users/anasqumhiyeh/Documents/faultline --
```

Commit form for every task, after exact-path staging and both scans:

```sh
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com \
  commit -m "TASK-SUMMARY" -m "Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

## File map

| File | Responsibility |
| --- | --- |
| `internal/bundle/demo/native_evidence.go` | Verified sidecar envelope, destination checks, atomic publication, stale cleanup |
| `internal/bundle/demo/native_evidence_test.go` | Exact bytes/digests, destination and failure tests, `TestNativeEvidenceDiagnostics` |
| `internal/cli/judge_demo.go` | Proposal/approval/REPLAY/RERUN selection and export output |
| `internal/cli/judge_demo_test.go` | Exact CLI/artifact tests and `TestJudgeDemoNativeDiagnostics` |
| `internal/cli/cli.go` | Single `judge-demo` dispatch |
| `internal/cli/judge_preview_test.go` | Update the two old unported `judge-demo` expectations |
| `internal/cli/judge_demo_process_test.go` | Built-binary Node-free proposal and native export |
| `difftest/native_coverage_test.go` | Exact five-file evidence ownership and multi-package collection |
| `difftest/native_coverage_gate_test.go` | Adversarial multi-package/profile/ownership regressions |
| `difftest/KNOWN_DIFFERENCES.md` | Native approval/export and sidecar distinctions |
| `docs/superpowers/specs/2026-09-25-go-migration-overview.md` | Precise local-only status after qualification; no completion claim |
| `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md` | Dependency-map status after qualification; no completion claim |

### Task 1: Digest-linked native evidence writer

**Files:**
- Create: `internal/bundle/demo/native_evidence.go`
- Create: `internal/bundle/demo/native_evidence_test.go`

**Interfaces:**
- Consumes: `Written`, `demo.Verify`, `bundle.Result`, `canonical.DigestJSON`, `jsjson.Value`, `nodefs.AtomicWriteFile`, `nodefs.Lstat`, and `nodefs.Unlink`.
- Produces:

```go
func WriteNativeEvidence(
    written Written,
    verification bundle.Result,
    analysis, provenance jsjson.Value,
) (string, error)

func RemoveNativeEvidence(bundleDirectory string) error
```

`WriteNativeEvidence` returns the absolute sidecar path. It requires `verification.Valid`, a non-nil `*verification.RootDigest`, and equality with `written.RootDigest`. Its exact top-level value is:

```go
jsjson.MustObject(
    "schemaVersion", jsjson.MakeString("faultline.native-demo-cli-evidence.v1"),
    "analysisDigest", jsjson.MakeString(analysisDigest),
    "bundleRootDigest", jsjson.MakeString(written.RootDigest),
    "provenance", provenance,
)
```

`analysisDigest` is `canonical.DigestJSON(analysis)` after the native API returns. Serialize with `jsjson.StringifyIndent(envelope, "  ") + "\n"` and derive the path as `nodefs.Resolve(written.Directory) + ".native-execution.json"`.

Use an unexported dependency structure so tests can force publication/removal failures without a production selector:

```go
type nativeEvidenceDeps struct {
    atomicWrite func(string, string) (string, error)
    lstat       func(string) (fs.FileInfo, error)
    unlink      func(string) error
}
```

Production uses `nodefs.AtomicWriteFile`, `nodefs.Lstat`, and `nodefs.Unlink`. A not-exist destination is a no-op for cleanup. Only regular non-symlink destinations are unlinked. For REPLAY removal, a symlink/directory/special file returns `Native evidence destination must be a regular non-symlink file: <path>`. For RERUN failure cleanup, those entries remain untouched. Invalid verification or digest construction returns before any destination operation or cleanup. Once destination publication has been attempted, any publication error triggers regular-file cleanup and returns `errors.Join(publicationErr, cleanupErr)`.

- [ ] **Step 1: Write failing exact writer tests.** Build a real REPLAY bundle with `Write` and `Verify`, then use a synthetic provenance object clearly labeled as writer input, not execution evidence. Assert complete sidecar bytes, top-level order, digest links, regular-file replacement, absent cleanup, symlink/directory refusal, invalid verifier/root refusal, and joined publication/removal failures. Include this emitter after exact assertions in every diagnostic child:

```go
observed, marshalErr := json.Marshal(err.Error())
if marshalErr != nil {
    t.Fatal(marshalErr)
}
t.Logf("NATIVE-DIAGNOSTIC %s", observed)
```

The parent is `TestNativeEvidenceDiagnostics`; each child compares the complete error and typed causes before logging. `TestNativeEvidenceWrite` independently recomputes the expected value with `canonical.DigestJSON` and compares every byte.

- [ ] **Step 2: Run RED.** Payload: `go test -json -count=1 -timeout=60s -run '^TestNativeEvidence' ./internal/bundle/demo`. Expect compilation failure because the exported functions are absent. Retain the complete event stream.

- [ ] **Step 3: Implement the minimal writer.** Implement the interfaces and dependency rules above. Do not modify `write.go`, `verify.go`, bundle schemas, or existing artifacts.

- [ ] **Step 4: Run GREEN and race.** Payload: `go test -json -count=1 -timeout=60s -run '^TestNativeEvidence' ./internal/bundle/demo`. Then payload: `go test -race -json -count=1 -timeout=90s -run '^TestNativeEvidence' ./internal/bundle/demo`. Require zero failures/skips and consume all events.

- [ ] **Step 5: Review and commit.** Independently review exact bytes, no-follow destination checks, cleanup ownership, and non-nil root binding. Stage only the two new files, scan, and commit `feat: add native execution evidence writer`.

### Task 2: Proposal-approved CLI export

**Files:**
- Create: `internal/cli/judge_demo.go`
- Create: `internal/cli/judge_demo_test.go`
- Modify: `internal/cli/cli.go:97-101` to dispatch `case command == "judge-demo": return judgeDemoCommand(e, rest)` before `judge-proof`.
- Modify: `internal/cli/judge_preview_test.go:262-279` to expect `not yet ported: judge-demo server` without export and `--approve-native requires --rerun-all.` for the second isolated case, whose arguments are exactly `[]string{"judge-demo", "--export-only", "--approve-native", "sha256:any-value"}`.

**Interfaces:**
- Consumes: Task 1's `WriteNativeEvidence` and `RemoveNativeEvidence`, `demofixture.CreateReplayAnalysis`, `demofixture.NativeWitnessProposal`, `demofixture.RunNativeAnalysis`, `demo.Write`, `demo.Verify`, `clock.Now`, `clock.ISO`, and existing option/output helpers.
- Produces: `func judgeDemoCommand(e *env, args []string) error`.

Implement this exact validation order:

```go
if hasFlag(args, "--native-proposal") {
    for _, flag := range []string{"--rerun-all", "--export-only", "--approve-native", "--output"} {
        if hasFlag(args, flag) {
            return fail("Native proposal inspection cannot be combined with execution or output options.")
        }
    }
    e.out(jsjson.StringifyIndent(demofixture.NativeWitnessProposal(), "  ") + "\n")
    return nil
}
if !hasFlag(args, "--export-only") {
    return fail("not yet ported: judge-demo server")
}
rerun := hasFlag(args, "--rerun-all")
if !rerun && hasFlag(args, "--approve-native") {
    return fail("--approve-native requires --rerun-all.")
}
if rerun && !hasFlag(args, "--approve-native") {
    return fail("Native RERUN requires --approve-native <proposal-digest>.")
}
```

For RERUN, call `requiredOption(args, "--approve-native")` after those checks. For output, preserve `judge-preview` semantics: default to `nodefs.Resolve(".faultline/bundles/judge-demo")`; if `option(args, "--output")` is present, use `requiredOption` and `nodefs.Resolve`. A trailing `--output` therefore retains the default. `--port` has no effect.

For REPLAY, use `CreateReplayAnalysis`. For RERUN, use `context.Background()` and `RunNativeAnalysis` with `NativeApproval{ProposalDigest: digest, ApprovedAt: clock.ISO(clock.Now())}`. Write with `demo.Write(output, analysis, demo.WriteOptions{})`, then `verification := demo.Verify(written.Directory, "", false)`. On invalid verification, return `Generated proof bundle failed integrity verification: ` plus `strings.Join(verification.Errors, "; ")`. Require non-nil `*verification.RootDigest == written.RootDigest` before any sidecar output, otherwise return `Generated proof bundle root digest differs from verified root`.

For REPLAY, call `RemoveNativeEvidence(written.Directory)`. For RERUN, call `WriteNativeEvidence(written, verification, result.Analysis, result.Provenance)`. Print only after these operations succeed:

```go
mode := "cached replay"
if rerun {
    mode = "executed"
}
e.out("FaultLine " + mode + " investigation prepared.\n")
e.out("Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n")
e.out("Bundle root: " + written.RootDigest + "\n")
e.out("Proof bundle: " + describeBundlePath(written.Directory) + "\n")
```

Between the first and self-consistency lines, print the first stable `PASS_TO_FAIL` transition as `Stable sample boundary: <beforeStateId> → <afterStateId>\n`, or `Stable sample boundary: not certified from cached replay\n`. For RERUN only, append `Native execution evidence: <sidecar-path>\n`.

- [ ] **Step 1: Write failing CLI tests.** Table-drive exact stdout/stderr/exit for inspection, every forbidden combination, server refusal, absent approval, trailing/empty/`--`-prefixed approval, wrong digest, REPLAY approval misuse, and both mode flags. Assert no output directory or child side effect for pre-execution failures. Use `TestJudgeDemoNativeDiagnostics` for negative emitters and `TestJudgeDemoExport` for successful artifacts.

The native success case runs `Run` with `--rerun-all --export-only --approve-native <actual-proposal-digest>` in a fresh directory. Require 39 ordered receipts, `provenance.status=COMPLETE`, exact current proposal approval, sidecar `analysisDigest=canonical.DigestJSON(analysis)`, matching non-nil verifier/writer roots, and complete bundle verification. REPLAY requires the retained root `sha256:fe85050935c1452f15c7cd8e3a17270ea3f541ae764aa6f4bed8cf4ed97554aa` and no sidecar.

Add a sidecar-failure case with a valid existing bundle and a directory at the reserved sidecar path. Require exit 1, empty stdout, the exact path error, a verified new bundle, and no recursive deletion of that directory.

- [ ] **Step 2: Run RED.** Payload: `go test -json -count=1 -timeout=120s -run '^TestJudgeDemo' ./internal/cli`. Expect undefined `judgeDemoCommand` and updated dispatch-expectation failures. Consume all failures before implementation.

- [ ] **Step 3: Implement the handler and dispatch.** Use only the interfaces and validation order above. Do not add server code, alter native analysis, or weaken existing writer checks.

- [ ] **Step 4: Run GREEN.** Payload: `go test -json -count=1 -timeout=150s -run '^(TestJudgeDemo|TestJudgePreviewDispatchIsolation)' ./internal/cli`. Require all parent/child tests to pass without skips; retain actual native execution evidence.

- [ ] **Step 5: Review and commit.** Independently review approval precedence, no-partial-success output, digest linkage, and sidecar failure ordering. Stage only the four CLI files, scan, and commit `feat: export native RERUN from judge-demo`.

### Task 3: Bind new files into both coverage gates

**Files:**
- Modify: `difftest/native_coverage_test.go`
- Modify: `difftest/native_coverage_gate_test.go`
- Modify: `difftest/KNOWN_DIFFERENCES.md`

**Interfaces:**
- Consumes: Task 1/2 passing diagnostic parents and the existing fresh event/profile validators.
- Produces: exact native ownership for the original three files plus `internal/cli/judge_demo.go` and `internal/bundle/demo/native_evidence.go`.

Append only those two paths to `nativeCoverageFiles`. Introduce an explicit package description:

```go
type nativeEvidencePackage struct {
    importPath        string
    sourceDirectory   string
    parents           []string
    diagnosticParents []string
}
```

Use three entries: demofixture with its existing three parents and `TestNativeDiagnostics` as its only diagnostic parent; CLI with `TestJudgeDemoNativeDiagnostics`; demo with `TestNativeEvidenceDiagnostics`. Keep the invariant children and three contract cases required only for demofixture.

Run two sequential subprocesses under one shared 180-second context:

```sh
go test -json -count=1 -timeout=150s -coverprofile=<native-profile> \
  -run='^(TestNativeDiagnostics|TestNativeDiagnosticInvariants|TestNativeAnalysisFullContract)$' \
  ./internal/demofixture -native-contract=<absolute-native-contract.jsonl>

go test -json -count=1 -timeout=150s -coverprofile=<export-profile> \
  -run='^(TestJudgeDemoNativeDiagnostics|TestNativeEvidenceDiagnostics)$' \
  ./internal/cli ./internal/bundle/demo
```

Do not pass `-native-contract` to CLI/demo binaries. Decode both complete streams with per-package start/end, running, passing, and diagnostic maps keyed by package and test. Reject foreign packages, duplicate/completed-package events, skips/failures, missing parents/children, wrong emitters, malformed records, and pass-without-run.

Add `nativeProfileFor(data, expectedFiles)` to require every expected file while retaining only that subset. The demofixture subset is the original three; the export subset is the new two. This ignores demofixture counters incidentally emitted by CLI's imports instead of allowing them to replace the original evidence. Merge the disjoint subsets before `validateNativeEvidence`. Keep `nativeProfile(data)` as the all-five wrapper used by adversarial tests.

- [ ] **Step 1: Write adversarial RED tests.** Add cases for a missing CLI parent, wrong CLI emitter package, a demofixture flag-bearing invocation attempted for CLI, missing export profile file, zero-hit `judge_demo.go`, missing `native_evidence.go` diagnostic, duplicate package completion, and foreign counter ownership. Update existing fixtures so all five files are required. Assert rejected merges leave original counters unchanged. Run `go test -tags faultline_test -json -count=1 -run '^TestNativeCoverage' ./difftest` and retain actual failures.

- [ ] **Step 2: Implement multi-package collection.** Add the package allowlist, two commands, per-package event accounting, and disjoint profile subsets. Keep success-only caching and before/after `nativeSourceBinding` checks.

- [ ] **Step 3: Run focused GREEN.** Payload: `go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest`. Require every test to pass without skips and no foreign literal/counter ownership.

- [ ] **Step 4: Record exact native distinctions.** In `KNOWN_DIFFERENCES.md`, document `--native-proposal`, `--approve-native`, export-only server refusal, the `faultline.native-demo-cli-evidence.v1` schema/path, digest linkage, sidecar exclusion from bundle hashes, and publication-failure cleanup. Do not edit old golden/corpus bytes.

- [ ] **Step 5: Review and commit.** Independently review multi-package completion and exact ownership, then stage only the three difftest files, scan, and commit `test: cover native RERUN export evidence`.

### Task 4: Node-free evidence, platform checks, and final qualification

**Files:**
- Create: `internal/cli/judge_demo_process_test.go`
- Modify after local qualification only: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify after local qualification only: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`

**Interfaces:**
- Consumes: the completed `judge-demo` command and real self-executing native child.
- Produces: `TestJudgeDemoBuiltBinaryNodeFree`, which builds `cmd/fl` and verifies proposal plus native export outside the repository.

- [ ] **Step 1: Add the built-binary test.** Build `fl` into a fresh test directory using `go build -o <fresh>/fl ./cmd/fl` from the module root. Run the binary by absolute path in a separate empty directory with `PATH=""`; on Windows retain only the required system/temp entries needed by the host, while the native child's own environment remains the established minimal vector. Pass no seed, Node, Go compiler, repository, TS, dist, testdata, or package-manifest path.

First run `--native-proposal` and compare exact JSON. Then run native export with the actual digest and a managed output below the fresh directory. Require exit 0, empty stderr, 39 receipts, a valid bundle, and a complete digest-linked sidecar. Do not skip when Node is absent; absence is the test condition.

- [ ] **Step 2: Run affected ordinary/tagged suites.** Payloads, separately: `go test -json -count=1 -timeout=195s ./internal/cli ./internal/bundle/demo ./internal/demofixture ./internal/nativefixture ./internal/clock ./cmd/fl`, then the same packages with `-tags faultline_test`. Require zero failures/skips and retain full events.

- [ ] **Step 3: Run live, race, vet, and coverage checks.** Run the complete `TestLiveNativeRunner` with `FAULTLINE_NODE_ORACLE=1`; the focused five-file gate selection from Task 3; race on `./internal/cli ./internal/bundle/demo ./internal/demofixture ./internal/nativefixture`; and `go vet -tags faultline_test` on all affected packages plus `./difftest`. Keep every invocation below 240 seconds.

- [ ] **Step 4: Cross-build all six targets.** For `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, run a separate `go test -c -o <fresh-target-dir>/<package-name>.test[.exe] ./internal/cli`, `./internal/bundle/demo`, `./internal/demofixture`, and `./internal/nativefixture` for each package. Use distinct package-name paths so `bundle/demo` and `demofixture` cannot overwrite one another. Record these as compilation only. Actual execution remains a hosted obligation on all six targets.

- [ ] **Step 5: Review final delta and record only local status.** Obtain independent spec/quality review of every changed file and resolve blocking findings. Update the two migration documents to say only that CLI export and sidecar are locally implemented with the actual scoped results and hosted six-target acceptance pending. Do not claim full `judge-demo`, interactive RERUN, all Slice 5, release readiness, or hosted completion.

- [ ] **Step 6: Scan and commit the final local deliverable.** Verify exact allowed paths, clean staged diff, and unchanged protected files. Scan staged bytes and the intact hook, then commit `test: verify native RERUN export without Node`. Stop at local delivery; later publication requires separate explicit authorization.

## Self-review checklist

- Spec sections 1–3 map to Task 2's exact proposal/approval/server-refusal tests and handler.
- Spec section 4 maps to Task 1's digest-linked writer, Task 2's verified export ordering, and Task 4's artifact checks.
- Spec section 5 maps to the explicit file map; no production file outside it is edited.
- Spec section 6 maps to Task 3's five-file gates and Task 4's Node-free, six-target compilation, hosted-execution, race, vet, and review requirements.
- Function names are consistent: `WriteNativeEvidence`, `RemoveNativeEvidence`, `judgeDemoCommand`, `TestNativeEvidenceDiagnostics`, `TestJudgeDemoNativeDiagnostics`, and `TestJudgeDemoBuiltBinaryNodeFree`.
- No step claims that a local green run resolves the earlier native deadline or cross-stream nonreproduction, and no step merges or publishes the branch.
