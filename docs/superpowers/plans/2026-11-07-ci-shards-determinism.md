# FaultLine Targeted CI Shards and Deterministic Harnesses Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the CI critical path through serve stack and writer-replay splits while eliminating the close-EIO and fake_flood nondeterminism, without reducing any evidence.

**Architecture:** Separate serve stack probes as a whole suite. Generate four deterministic writer-replay manifests from source, use ordinary and `posix/` filters, and prove source-to-shard and actual terminal counts before aggregating them in the gate. Ubuntu coverage remains unnarrowed.

**Tech Stack:** Go 1.27 standard library, Python 3 standard library, POSIX shell/awk, Node 22.22.2 for test-only generators, GitHub Actions YAML.

**Spec:** `docs/superpowers/specs/2026-11-07-ci-shards-determinism-design.md`

## Global Constraints

- No implementation until the user approves this plan. Work on `ci-reuse-determinism-20261107`; no new worktree, release, protected-source edit or automatic merge.
- Do not change production behavior, production deadlines, oracle bytes, skip policy, `src/`, `package.json` or `pnpm-lock.yaml`.
- No `t.Parallel`, broad cache, serve evidence partition, retry-as-pass, accept-either result, longer timeout or coverage waiver.
- Preserve exactly 1,075 writer template cases plus `recorder-fake-docker`, 1,295 macOS/Linux golden invocations, 444 Windows compared invocations, and all 654 writer-fault leaves.
- Ubuntu AMD64 tagged coverage remains a single unnarrowed `difftest` process with empty `-run`; Ubuntu frozen-TS cross-verification remains unchanged.
- Every task has meaningful RED before implementation, focused GREEN, adversarial tests and independent spec/quality review. Record evidence/failures in `.superpowers/sdd/2026-11-07-ci-shards-determinism/progress.md`.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...` and footer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never bypass TruffleHog or change global Git config.
- Tasks 1, 2 and 4 may run in parallel with disjoint files. Tasks 3 and 5 edit `go.yml` sequentially. Task 6 is last.
- Every shell command stays below five minutes. Publication and merge remain separate decisions.

---

### Task 1: Deterministic close-EIO Injection

**Files:**
- Create: `cmd/fl/thread_affinity_faultline_linux.go`
- Modify: `difftest/writerfault_live_test.go:449-461`
- Modify: `difftest/faultordinal_test.go:62-79`
- Modify: `difftest/faultordinal_unit_test.go`

**Interfaces:**
- Produces: test-only `FAULTLINE_TEST_LOCK_OS_THREAD=1` behavior linked only by `faultline_test && linux`; `injectedOrdinal(trace string, n int) bool` counts completed parsed calls on the injected TID.

- [ ] **Step 1: Write failing parser and hook tests**

Extend `faultordinal_unit_test.go` with foreign-TID closes before the injected third close (pass), same-TID unfinished/resumed third close (pass once), orphan resumed/unterminated input (fail), and first-close/SIGABRT (fail). Add a compile-time test that the tagged CLI contains the hook but untagged does not, and that only Go `onlyCalls` rows export the variable.

- [ ] **Step 2: Run RED**

Run `go test -tags faultline_test -run '^(TestInjectedOrdinal|TestFaultOrdinal|TestWriterFaultThreadAffinity)' ./difftest`.
Expected: old combined-line counter rejects foreign-TID input; no hook exists.

- [ ] **Step 3: Implement thread affinity and TID validation**

Create:

```go
//go:build faultline_test && linux
package main
import (
    "os"
    "runtime"
)
func init() {
    if os.Getenv("FAULTLINE_TEST_LOCK_OS_THREAD") == "1" {
        runtime.LockOSThread()
    }
}
```

Immediately after the Go-side `script` is initialized, prepend the variable only for `onlyCalls`. Replace `injectedOrdinal` with `parseStrace` validation that rejects parse errors/SIGABRT, finds the first injected PID, counts only completed calls with that PID, and requires the first injected call to be ordinal n.

- [ ] **Step 4: Run GREEN and real fault case**

Run the parser tests, `FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -run '^TestLiveWriterFaults/turns-proof/turn-binding/close-EIO$' ./difftest`, and the tagged CLI thread-affinity test.
Expected: all pass with injected TID ordinal 3 and no combined-trace false failure.

- [ ] **Step 5: Review and commit**

Review that release builds never link the hook and no production behavior changes. Commit `test: make close-EIO injection thread deterministic` with required identity/footer.

---

### Task 2: Exact fake_flood and fake_slow Outcomes

**Files:**
- Modify: `difftest/testdata/fakegit/flood/git`
- Modify: `difftest/hardened_live_test.go:150-245`

**Interfaces:**
- Produces: `wantError string` on `hardenedCase`; flood exact `Git command exceeded bounded output limit`, slow exact `Git command exceeded bounded execution time`.

- [ ] **Step 1: Write failing exact-outcome tests**

Add `wantError` assertions for flood and slow, plus a synthetic both-timeout case that must fail rather than pass. Keep every existing stdout/stderr/exit comparison.

- [ ] **Step 2: Run RED**

Run `FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -run '^TestLiveHardenedGit/(fake_flood|fake_slow)$' ./difftest`.
Expected: current fixture/assertion does not pin the intended per-case outcome and can diverge under load.

- [ ] **Step 3: Implement the generator and exact assertions**

Use `exec dd if=/dev/zero bs=1048576 count=65 2>/dev/null`. Add `wantError string` to the case struct and require it immediately after `gotErr := deref(want.Error)` whenever nonempty, before the existing equality assertions.

- [ ] **Step 4: Run GREEN**

Run the same live cases and `FAULTLINE_NODE_ORACLE=1 go test -run '^TestLiveHardenedGit$' ./difftest`.
Expected: flood/slow exact errors on both TS and Go; any both-timeout mismatch fails.

- [ ] **Step 5: Review and commit**

Review identical 65 MiB, clean stderr and no alternate outcomes. Commit `test: pin hardened Git resource-limit outcomes` with required identity/footer.

---

### Task 3: Split Serve Stack Probes

**Files:**
- Modify: `.github/workflows/go.yml`
- Modify: `.github/scripts/go_workflow_test.py`

**Interfaces:**
- Produces: one whole `TestLiveServeBundleStackCallSites` job per native target, with its own log/list and no-skip checks; all other serve jobs retain their previous ownership.

- [ ] **Step 1: Add failing structural assertions**

Require a separate stack-probe job on all six targets, with the whole stack-call-site test, and require it absent from `serve-bundle-native`. Assert all 68+5 cases, the final evidence audit and standalone release test remain. Assert unique artifacts and no evidence partition/merge.

- [ ] **Step 2: Run RED**

Run `python3 -I .github/scripts/go_workflow_test.py`.
Expected: stack probes still embedded in `serve-bundle-native`.

- [ ] **Step 3: Implement the separate job**

Create a native stack-probe matrix with the same OS/GOOS/GOARCH values as serve-bundle-native. Move only `TestLiveServeBundleStackCallSites` into its test list/log. Preserve all sites/shapes, no-skip behavior and identity. Do not change evidence capture or final audit.

- [ ] **Step 4: Run structural checks**

Run the workflow test, Ruby `YAML.safe_load` and `git diff --check`.
Expected: one complete stack-probe suite per target and no duplicate serve evidence.

- [ ] **Step 5: Review and commit**

Review that no merge/audit redesign was introduced. Commit `ci: separate serve stack probes` with required identity/footer.

---

### Task 4: Replay Manifests and Source-to-Shard Guards

**Files:**
- Create: `difftest/gen/gen-writer-shards.mjs`
- Create: `difftest/testdata/writer-shards/shard-0.json`, `shard-1.json`, `shard-2.json`, `shard-3.json`
- Create: `difftest/writer_shards_test.go`
- Modify: `difftest/writer_test.go:1829-1879`

**Interfaces:**
- Manifest schema per shard: `{ "shard": 0, "ordinary": ["case-id"], "posix": ["case-id"], "golden": 332, "windowsPass": 97, "windowsSkip": 185, "windowsCompared": 111 }` with each shard's exact spec values.
- Produces: `writerShardForCase(id string) int`, `writerShardFilter(t *testing.T, shard int) string`, and actual selected/executed/compared summary accounting used by `TestWriterReplay`.

- [ ] **Step 1: Write failing source-to-shard and accounting tests**

Require all 1,075 source caseIDs exactly once across four manifests, the recorder only in shard 0's POSIX branch, and the exact per-shard table. Test hash stability, no duplicate/foreign IDs, and ordinary/POSIX branch correctness. Require summaries under filtering to use actual selected/executed/compared values, not `len(templates)` or `len(goldens)`.

- [ ] **Step 2: Run RED**

Run `go test -tags faultline_test -run '^TestWriterShard' ./difftest` and `node difftest/gen/gen-writer-shards.mjs --check`.
Expected: no manifests/helper and summary still prints total corpus size under filtering.

- [ ] **Step 3: Generate manifests and implement helpers/accounting**

The generator reads `writers.json` plus sorted `writers.d/*.json`, computes `SHA256(caseID)[0] mod 4`, partitions `PosixOnly`, assigns `recorder-fake-docker` only to shard 0, and writes the exact JSON schema plus golden/Windows counts. `--check` validates committed manifests without writing; recapture compares fresh bytes without overwriting.

`writerShardForCase` duplicates the hash rule in Go. `writerShardFilter` builds exact ordinary and `posix/` alternations and rejects an empty shard. Add compared-invocation counting beside each `compareWriter` call, and change the deferred summary to actual selected/executed/compared values while keeping the zero-selection refusal.

- [ ] **Step 4: Run GREEN and independent recapture**

Run the Go source-to-shard tests and generator `--check`; run recapture twice into separate fresh directories and compare every manifest byte.
Expected: all source cases exactly once, exact totals and deterministic bytes.

- [ ] **Step 5: Review and commit**

Review independent generation, no source-Go-derived expected values, and actual accounting. Commit `test: generate writer replay shard manifests` with required identity/footer.

---

### Task 5: Replay Terminal Guards and Workflow Shards

**Files:**
- Create: `.github/scripts/check-writer-replay-shards.py`, `.github/scripts/check_writer_replay_shards_test.py`
- Modify: `.github/workflows/go.yml`, `.github/scripts/go_workflow_test.py`

**Interfaces:**
- Produces: `check-writer-replay-shards.py SHARD MANIFEST LOG`, and `check-writer-replay-shards.py total MANIFEST_DIR LOG_DIR`.
- Total requires 1,075 unique template cases, recorder once, 1,295 macOS/Linux or 444 Windows compared invocations with exact per-shard totals.

- [ ] **Step 1: Write failing terminal and workflow tests**

Use manifest-shaped synthetic logs for all four shards. Test valid totals plus missing, duplicate, foreign, failed, unexpected skip, wrong golden count and parent-only summary. Require non-Ubuntu `go test` to use `-skip '^TestWriterReplay$'` for the remaining suite and four shard jobs with exact manifest filters. Ubuntu remains unnarrowed.

- [ ] **Step 2: Run RED**

Run `python3 -I .github/scripts/check_writer_replay_shards_test.py` and `python3 -I .github/scripts/go_workflow_test.py`.
Expected: no terminal guard or replay shard matrix.

- [ ] **Step 3: Implement the terminal guard**

Parse only terminal `TestWriterReplay/<ordinary>` and `TestWriterReplay/posix/<id>` lines. Reject non-PASS, duplicates, foreign IDs and unexpected skips. Use manifest expected IDs/counts, including the recorder. Aggregate all logs with the total rule and actual compared invocation summaries; do not trust parent PASS or printed corpus totals.

- [ ] **Step 4: Integrate the four non-Ubuntu jobs and aggregate gate**

Use a four-valued replay-shard matrix on every non-Ubuntu tagged leg. Each job reads its manifest, builds exact ordinary/POSIX filters, runs only `./difftest`, and invokes its terminal guard. The remaining non-Ubuntu suite uses `-skip '^TestWriterReplay$'`; Ubuntu has no skip or writer-shard filter. Update the final needs graph and structural inventory for the replay job ID and artifact set. `go-ci-gate` downloads all four replay artifacts into distinct directories and runs the total guard. Preserve artifact names and existing writer-fault total 654 handoff.

- [ ] **Step 5: Run GREEN and adversarial integration checks**

Run all script/workflow tests, Task 4 Go/source tests, generator check, Ruby YAML.safe_load and `git diff --check`.
Expected: every manifest/filter/terminal/aggregate case passes and every adversarial case fails.

- [ ] **Step 6: Review and commit**

Review source-to-terminal coverage, POSIX selection, Ubuntu exclusions and aggregate evidence. Commit `ci: shard writer replay outside coverage` with required identity/footer.

---

### Task 6: Final Review and Measured Acceptance

**Files:**
- Modify: execution ledger and relevant migration status documentation only when evidence exists.

- [ ] **Step 1: Run complete local qualification**

Run all script/workflow tests, source-to-shard and focused Go tests, generator checks, `go vet -tags faultline_test ./difftest`, Ruby YAML.safe_load and `git diff --check`. Run full `go test -count=1 -timeout=240s ./...` once and retain its actual result; do not hide a timeout.

- [ ] **Step 2: Independent whole-branch review**

Dispatch a fresh Sonnet reviewer for spec compliance, gate correctness, source/corpus completeness, test independence and deferred-minor triage. Fix every Critical/Important finding through one fix wave and one scoped re-review.

- [ ] **Step 3: Hosted acceptance and measurement**

After separately authorized publication, require every exact-head code-path check on all six targets, exact expected skips only, all replay/fault totals, artifacts and final audits. Measure critical path and total job minutes against PR 1's 70.1 and 583.5; do not claim ~45 minutes if the data disagrees. Post-merge main must pass. Validate subsequent docs-only gates separately from code acceptance.

- [ ] **Step 4: Commit documentation and stop**

Record actual evidence and open caveats, commit with required identity/footer, then stop. Interactive RERUN and any further optimization get separate spec/plan/authorization cycles.
