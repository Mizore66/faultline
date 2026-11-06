# FaultLine CI efficiency and determinism — design

- **Date:** 2026-11-06
- **Status:** Design and written specification approved by the user on 2026-11-06 after trigger, delivery, scope, gate/cost and acceptance choices. Implementation-plan approval remains a separate gate. No workflow edit is authorized by this document.
- **Baseline:** PR #220 merged normally at `1f128e015a757b24fc8583b463f91f02d92fbee8`; both post-merge main workflows succeeded with all 35 check runs successful. Accepted pre-merge head `ecd8416d58c3395f0c3591bbc85f6d7ea3d8008f` had 70 check runs: 68 successful and two expected provenance skips.
- **Delivery:** Two PRs. PR 1 changes triggers, scoped gates and measured job shards. PR 2 adds safe caching/build reuse and deterministic flaky-harness fixes, then reassesses serve-bundle evidence sharding.

## 1. Measured problem and objectives

The accepted push Go run `42065630748` and PR Go run `42065640883` each had 22 jobs. Their longest successful jobs were:

| Job | Push minutes | PR minutes |
| --- | ---: | ---: |
| `go test (macos-15-intel)` | 85.8 | 79.2 |
| `serve --bundle native (windows-11-arm)` | 59.5 | 58.3 |
| `stack thresholds vs Node (macos-latest)` | 53.2 | 56.5 |
| `serve --bundle native (macos-15-intel)` | 49.1 | 46.4 |
| `go test (macos-latest)` | 46.5 | 46.2 |
| `difftest live writer faults and real Docker` | 38.0 | 35.2 |
| `difftest live` | 36.6 | 40.5 |

The macOS Intel `go test` step alone consumed 83.9 minutes because ordinary and tagged `./...` runs are sequential in every six-OS job. Windows ARM's native live/replay/evidence step consumed 56.5 minutes. macOS stack-live's platform step consumed 51.8 minutes, and writer faults consumed 36.5 minutes. Repeating the full matrix on both feature push and PR doubles compute and often serializes waits.

Objectives:

1. Run the complete pre-merge matrix once per PR, retain complete post-merge main verification, and reduce the critical path toward approximately 60 minutes without reducing evidence.
2. Split long sequential work along process-safe boundaries. More parallel jobs are acceptable: the design rises from 70 to approximately 108 check runs across PR + main (54 per event: six additional ordinary/tagged legs, five additional writer legs, four additional stack legs, and two scope/gate jobs in each of two workflows), so success is measured by wall time and determinism, not fewer boxes.
3. Make documentation-only changes explicit scoped work rather than pay the full code-test price or mislabel skipped tests as passes.
4. Remove nondeterministic harness behavior that forces whole new CI heads. No retry, longer production deadline, accept-either result, weaker assertion or coverage waiver is an optimization.

Non-goals: changing production behavior, native process deadlines, oracle bytes, test semantics, release policy, branch-protection settings (main is currently unprotected), or the accepted serve-bundle evidence model without a safe artifact merge/audit design.

## 2. PR 1 — triggers and scoped CI gates

### 2.1 Trigger policy

For both `.github/workflows/go.yml` and `.github/workflows/verify.yml`:

- Keep full `pull_request` execution.
- Restrict `push` execution to `main`.
- Preserve Verify FaultLine's existing `workflow_dispatch` and its unfreeze/provenance semantics. Do not add a manual shortcut around PR or main acceptance.

A feature branch therefore receives full checks when its PR is opened/synchronized, not a duplicate full matrix on every direct feature push. Main receives the same complete post-merge checks. This changes the old four-workflow feature-head model to two workflows by design; every required job within those workflows remains mandatory for code changes.

### 2.2 Shared scope classifier

Add a reviewed standard-shell classifier under `.github/scripts/`, invoked by a `scope` job in each workflow with full history available. It uses:

- PR: base SHA through event head SHA, matching the three-dot unique-change rule while preserving the existing checkout/merge-ref distinction.
- Main push: `event.before` through `event.sha`. An all-zero or missing before SHA selects full code CI.
- Any classification error, empty output, revision failure or unexpected path: full code CI, never docs-only.

Docs-only is exactly this allowlist:

```text
docs/**
README.md
LICENSE
NOTICE
CHANGELOG.md
```

Every other path, including `difftest/KNOWN_DIFFERENCES.md`, `.github/**`, `internal/**`, `src/**`, `package.json`, `pnpm-lock.yaml`, testdata and root build files, selects code CI. No content-based attempt is made to judge whether code is consequential.

The classifier emits `docs-only` or `code` and the exact changed-path list. It validates docs-only changes with `git diff --check` and the required base/head evidence. It does not compile, test or claim code coverage for docs-only changes.

### 2.3 Gate semantics

Each workflow has a final gate (`go-ci-gate` and `verify-ci-gate`; together, the logical `ci-gate`):

| Scope | Heavy jobs | Gate requirement |
| --- | --- | --- |
| Code or mixed | Run normally | Every required job is successful, except the existing explicitly named main-only provenance skips on non-main events. Any failure, cancellation or unexpected skip fails the gate. |
| Docs-only | Report `skipped` as out of scope | The classifier and docs validation succeed and the gate prints `CI-SCOPE docs-only` with the exact changed paths. These skips are not called test passes. |

`go-ci-gate` also requires all new shard-count guards. `verify-ci-gate` retains existing audit/security/provenance conditions. Both gates use `if: always()` and inspect every needed job's actual result rather than assuming a green containing job proves every step ran.

No third-party paths action or branch-protection dependency is added. Test the classifier with code, docs-only, mixed, empty, initial-push, revision-error and deliberately misclassified inputs. A docs-only result is not code-test acceptance and cannot authorize a code merge.

## 3. PR 1 — six-OS ordinary/tagged split

Split the current `go test (${{ matrix.os }})` into two matrix legs per OS with explicit names such as `go test (ubuntu-24.04, untagged)` and `go test (ubuntu-24.04, tagged)`. A two-valued `variant` matrix under one job ID is preferred to duplicating the entire job definition.

Untagged leg:

- `go mod tidy -diff`, `go vet ./...`, `go test -count=1 -timeout 30m -v ./...`.
- On Linux only, the default-built `difftest/testdata/globalopen` package and its 15-minute bound.
- `Release binary holds no test override` once per OS, still building both tagged and untagged binaries.
- Its own log and the existing OS-specific skip/serve-skip guards.

Tagged leg:

- `go vet -tags faultline_test ./...`, `go test -tags faultline_test -count=1 -timeout 120m -v ./...`.
- Its own log and the same applicable skip/serve-skip guards.
- `FAULTLINE_COVERAGE_GATE=1` only on `ubuntu-24.04`, not on other OS legs.

The tagged `difftest` invocation retains empty `-run`: its process-wide coverage gate and native-evidence cache must not be narrowed. Do not split tests inside a process with `t.Parallel`; package tests mutate process-wide environment, cwd, umask, seeded clock and shared helpers.

Expected effect: the 83.9-minute sequential macOS Intel step becomes two independently scheduled legs. Actual acceptance measures both durations instead of assuming an exact halving.

## 4. PR 1 — writer-fault shards

Run six Ubuntu AMD64 writer-fault jobs with whole caseID sets. Each job installs strace and runs `TestLiveWriterFaults` with an exact first-segment alternation. The sets are disjoint and total 654 cases:

| Shard | caseID set | Cases |
| --- | --- | ---: |
| core | `propose-input`, `policy-freeze`, `ratify`, `freeze`, `ledger-bind`, `minimize-git`, `investigate-two-states`, `commit-proof-preview` | 105 |
| runtime-init | `runtime-resolve-node`, `runtime-mapping-nested-output`, `init-yes-dirty`, `init-standing`, `init-cli`, `doctor-security` | 114 |
| incident-prevention | `incident-start-command`, `incident-continue-unapproved`, `incident-continue-inherit-digest`, `prevention-write-bundle-ok`, `prevention-write-input-summary`, `prevention-write-input-agents-append` | 147 |
| turns | `turns-proof`, `turns-minimize` | 75 |
| attest-repair | `attest-create-git`, `provenance-create-ok`, `repair-brief-input-ok`, `witness-implement-template`, `repair-bundle-codex-ok` | 111 |
| record-sidecar | `record-init`, `record-checkpoint`, `codex-snapshot-clean`, `codex-gc-force`, `codex-gc-alternates-other-entries`, `sidecar-hook-session-quiet`, `sidecar-hook-full-turn` | 102 |

Do not shard by errno or syscall: whole caseID groups preserve discovered paths and sidecar receipt/stage discovery. `difftest-live` continues to exclude `TestLiveWriterFaults`, so it never duplicates these jobs.

The existing parent test/skip checks are insufficient under narrowing because the current summary prints the matrix's total length. Add a tested guard that parses actual leaf terminal results and proves, per shard:

1. Exactly the table's number of distinct `caseID/label/call-errno` leaves passes.
2. No leaf fails, skips, is absent, is duplicated or belongs to another shard.
3. The six shard manifests have exactly 654 unique leaves and exactly match the source table; they do not infer completeness from `len(matrix)` or a parent PASS.

The `core` job alone owns Docker setup/pull, `TestLiveRealDocker`, `TestLiveRealDockerProjectBuild`, `TestLiveDemoArtifactFaults`, and the GLOBALOPEN native controls. Their setup steps are conditional to core. All six retain the no-skip guard with `TestLiveWriterFaults` listed. Uploaded artifacts use shard suffixes rather than collide on `globalopen-proof`. Each shard retains its raw log as an artifact; after all six, `go-ci-gate` downloads the six logs into distinct directories and runs the total 654-leaf uniqueness guard before reporting success. Docs-only gates do not download code-test artifacts.

## 5. PR 1 — stack/live shards

Remove `TestLiveServeBundleStackCallSites` from the stack-live list only. It already runs in `serve-bundle-native` on the identical `ubuntu-24.04-arm` and `macos-latest` targets, so this removes duplicate native oracle work without losing a platform run.

Split the remaining stack-live tests into three whole-top-level jobs per target:

| Shard | Exact tests |
| --- | --- |
| platform-process | `TestLiveStackThresholdsMatchNode`, `TestLiveSpreadLimitsMatchNode`, `TestLiveRimrafDepthMatchesNode`, `TestLiveStdoutMatchesNode`, `TestLiveSignalsMatchNode`, `TestLiveNofileMatchesNode`, `TestLiveGroupSignalMatchesNode`, `TestLiveStderrMatchesNode`, `TestLiveJudgeServer`, `TestLiveJudgeSignalStop`, `TestLiveBrowserOpen`, `TestLiveSeedPreload`, `TestLiveSpawn`, `TestLiveHardenedGit`, `TestLiveMaterialize`, `TestLiveHostCmd`, `TestLiveResolveCodexRunner` |
| turn-incident | `TestLiveInvestigateTurns`, `TestLiveTurnSnapshotCapture`, `TestLiveIncidentSuggest`, `TestLiveIncidentDraft` |
| context-sidecar | `TestLiveProjectContext`, `TestLiveSidecarHooks`, `TestLiveCpSyncTree` |

Do not narrow internal cases: all 150 turn-snapshot cases, 120 investigate-turn sessions, 421 sidecar sequences and 103 stores remain required. Keep every sidecar store and `FAULTLINE_CP_CASES` unset, and explicitly unset all case-narrowing environment variables in each shard. The attestation/provenance groups that require a shared process stay in `difftest-live`, not split across stack jobs.

Each shard gets its own list/log and the existing platform skip expression. Run measured stack-depth reporting and TimSort only in platform-process. Existing macOS skip identities remain exact.

## 6. PR 2 — safe reuse and deterministic harnesses

### 6.1 Build and artifact reuse

Adopt reuse only where identity is checked and semantics remain unchanged:

- Build the frozen-TypeScript `dist` once per source/toolchain digest and distribute that artifact with its digest/provenance to oracle jobs. Do not rebuild from one source while presenting another source's artifact. `pnpm install` remains frozen and cached.
- Reuse a same-OS/arch tagged `fl` through the existing `FAULTLINE_FL_BINARY` contract on noncoverage difftest jobs. Never substitute it for Ubuntu's instrumented coverage binary or an untagged release assertion.
- Prebuild default GLOBALOPEN helper/fixture binaries once per job and pass the documented override paths to native controls and the writer matrix. Hook-tagged lifecycle controls retain their distinct build.
- Keep setup-go caching. Do not introduce a cross-OS Go test binary cache or an unverified shared `node_modules` archive.

Measure upload/download overhead against repeated builds. Reject reuse when it costs more than it saves or weakens source identity.

### 6.2 Flaky harness corrections

- **close-EIO:** make fault injection and validation agree on process-wide syscall identity. If locking the driver goroutine to one OS thread is the selected mechanism, mirror the existing test-only `runtime.LockOSThread` pattern for the entire syscall sequence; otherwise use a process-wide ordinal approach. Do not change turn-binding production. Prove repeated exact close-EIO cases without raw combined-trace ordinals that vary by thread layout, unfinished/resumed lines or aborted traces.
- **fake_flood/resource limits:** separate output-rate and wall-time controls so the fake flood deterministically reaches the intended bounded-output result on both TS and Go, while the slow fake reaches bounded execution time with empty output. Keep exact expected bytes/status/errors and no alternate accepted outcome. Do not lengthen production limits.
- **Release-process timeouts:** diagnose the Windows cold-start evidence before changing anything. A longer timeout, retry or warm-up that hides first-run regressions is not an accepted default fix.

Each correction gets focused adversarial/stress evidence plus the complete hosted suite. Historical failures remain in the ledger and are not erased by a later green run.

### 6.3 Serve-bundle reassessment

Only after PR 1 measures the new critical path, consider splitting native serve-bundle live/replay work. A valid design must upload disjoint raw evidence parts, merge them without overwriting or deleting earlier captures, and run one final `TestServeBundleEvidenceAudit` over the complete 68+5 case set and binary/head identity. The standalone release test and console diagnostic remain separate. If artifact merging or identity cannot be proved, leave serve-bundle jobs intact.

## 7. Acceptance and sequencing

PR 1 must demonstrate:

- PR and main trigger behavior, with no duplicate feature-push matrix and unchanged post-merge main coverage.
- All code-path checks green on all six targets, existing expected skips only, no new deadline or production change.
- Exactly 654 unique writer-fault leaves, all required stack cases, complete coverage and evidence, unique artifacts and one Docker/GLOBALOPEN owner.
- A measured successful-run critical path near 60 minutes, with before/after job and total wall/compute comparisons. More parallel checks are reported honestly.
- Classifier fixtures proving docs-only cannot masquerade as code-tested acceptance.

PR 2 must demonstrate byte/provenance-equivalent artifacts, beneficial measured reuse, deterministic focused harness evidence and another complete hosted matrix. Any serve-bundle split needs the complete merged audit; otherwise its status remains unchanged.

Both PRs receive independent spec/quality review, exact-head CI and normal post-merge verification. Interactive RERUN implementation remains queued behind this CI spec/plan and the user's selected execution sequence. No workflow edit, publication, merge, release, protected-source change or oracle retirement follows from design approval alone.
