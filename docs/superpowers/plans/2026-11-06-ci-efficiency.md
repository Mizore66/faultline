# FaultLine CI Efficiency PR 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce FaultLine's pre-merge critical path toward 60 minutes through PR/main triggers, scoped docs gates, ordinary/tagged separation, and process-safe long-suite shards without reducing evidence.

**Architecture:** Python-standard-library and POSIX-shell guards sit beside the workflows and are tested with synthetic inputs before integration. The six-OS matrix splits untagged/tagged jobs, writer faults split into six exact caseID jobs, and stack/live splits into three whole-test jobs. All writers to `.github/workflows/go.yml` are serialized.

**Tech Stack:** GitHub Actions YAML, Go 1.27, Python 3 standard library, POSIX shell/awk, existing Go test/oracle harnesses.

**Spec:** `docs/superpowers/specs/2026-11-06-ci-efficiency-design.md`

## Global Constraints

- This plan implements PR 1 only. PR 2 (cache/reuse/flaky harnesses) gets a separate plan after PR 1's measurements.
- No implementation until the user approves this plan. Work on `ci-efficiency-20261106`; no new worktree, protected-source edit, release or automatic merge.
- Do not change production code, production deadlines, oracle bytes, skip policy, `src/`, `package.json` or `pnpm-lock.yaml`.
- No `t.Parallel`, accept-either result, retry-as-pass, longer timeout, blanket path filter or coverage waiver. Preserve one unnarrowed tagged `difftest` process for coverage.
- Every task has meaningful RED before implementation, focused GREEN, adversarial guard tests and independent spec/quality review. Record all evidence/failures in `.superpowers/sdd/2026-11-06-ci-efficiency/progress.md`.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...` and footer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never bypass TruffleHog or change global Git config.
- Dependency rule: Tasks 1 and 3 may run in parallel with disjoint files. Tasks 2, 4 and 5 are sequential because they edit `go.yml`. Task 6 is last.
- Every shell command stays below five minutes. Publication, ready/merge and PR 2 remain separate decisions.

---

### Task 1: Scope Classifier and Gate Guards

**Files:**
- Create: `.github/scripts/ci_scope.sh`, `.github/scripts/ci_scope_test.py`
- Create: `.github/scripts/ci_gate.py`, `.github/scripts/ci_gate_test.py`

**Interfaces:**
- Produces: `ci_scope.sh EVENT BASE HEAD [WORKDIR]`, printing `scope=code|docs-only` then one `path=<repo-relative-path>` per changed path.
- Produces: `python3 -I .github/scripts/ci_gate.py --scope SCOPE --event EVENT --ref REF --needs-json JSON`.
- Gate behavior: code requires every needed result `success`, except `signed GitHub CI provenance == skipped` outside `refs/heads/main`; docs-only ignores heavy-job results but prints `CI-SCOPE docs-only` and relies on Task 1's validated classifier.

- [ ] **Step 1: Write failing classifier tests**

Use `unittest` and a fresh temporary Git repository per test. Create commits with local test-only `git -c user.name=CI Test -c user.email=ci@example.invalid commit`, never global config. Cover docs-only `docs/superpowers/specs/x.md`, code `internal/x.go`, mixed changes, `difftest/KNOWN_DIFFERENCES.md`, root allowlist files, empty diff, all-zero before, invalid revision and `git diff --check` failure. Require invalid/empty/mixed cases to return `scope=code`, never docs-only.

```python
result = subprocess.run([script, "pull_request", base, head], cwd=repo, text=True, capture_output=True)
self.assertEqual(result.returncode, 0, result.stderr)
self.assertIn("scope=docs-only\n", result.stdout)
self.assertIn("path=docs/superpowers/specs/x.md\n", result.stdout)
```

Add `ci_gate_test.py` cases for code all-success, code failure, code cancelled, unexpected skip, expected non-main provenance skip, main provenance skip rejected, docs-only with heavy jobs skipped, and malformed/missing needs JSON. Require nonzero status for every invalid case.

- [ ] **Step 2: Run RED**

Run `python3 -I .github/scripts/ci_scope_test.py && python3 -I .github/scripts/ci_gate_test.py`.
Expected: missing script/executable failures. Do not create workflow YAML yet.

- [ ] **Step 3: Implement the classifier and gate**

`ci_scope.sh` resolves `BASE...HEAD` for `pull_request`, `BASE..HEAD` for main push, treats zero/missing before as code, selects code for every other event (including `workflow_dispatch`), and fails closed to code on any Git/classification error. Docs-only requires at least one changed path, all entries exactly in `docs/**`, `README.md`, `LICENSE`, `NOTICE`, `CHANGELOG.md`, plus successful `git diff --check`. Print deterministic sorted paths.

`ci_gate.py` parses the complete needs object and applies the interface exactly:

```python
if args.scope == "docs-only":
    print("CI-SCOPE docs-only")
    raise SystemExit(0)
if args.scope != "code":
    raise SystemExit("unknown scope")
for name, job in needs.items():
    result = job.get("result")
    allowed_provenance_skip = name == "signed GitHub CI provenance" and result == "skipped" and args.ref != "refs/heads/main"
    if result != "success" and not allowed_provenance_skip:
        raise SystemExit(f"required job {name}: {result}")
print("CI-SCOPE code")
```

- [ ] **Step 4: Run GREEN and adversarial tests**

Run the Step 2 command plus `sh -n .github/scripts/ci_scope.sh` and `python3 -I -c 'from pathlib import Path; [compile(p.read_text(), str(p), "exec") for p in (Path(".github/scripts/ci_gate.py"), Path(".github/scripts/ci_gate_test.py"))]'`. Mark `ci_scope.sh` executable in the commit.
Expected: all fixture classes pass; malformed input never exits 0.

- [ ] **Step 5: Review and commit**

Review fail-closed behavior and exact skip semantics. Commit `ci: add scope and gate guards` with the required identity/footer.

---

### Task 2: Split Six-OS Ordinary and Tagged Jobs

**Files:**
- Modify: `.github/workflows/go.yml:7-105`

**Interfaces:**
- Consumes: existing six OS labels and skip scripts.
- Produces: untagged/tagged matrix legs and logs consumed by Task 6's needs list; Ubuntu tagged leg alone sets `FAULTLINE_COVERAGE_GATE=1`.

- [ ] **Step 1: Add failing workflow assertions**

Create a small Python standard-library test that reads `go.yml` and requires twelve OS/variant matrix legs, not six: the existing six OS values each combine with both `untagged` and `tagged` variants. Require untagged commands `go vet ./...` and `go test -count=1 -timeout 30m -v ./...`; tagged commands `go vet -tags faultline_test ./...` and `go test -tags faultline_test -count=1 -timeout 120m -v ./...`. Require one release-override check per OS and coverage only on Ubuntu tagged. This test guards structure; it does not replace hosted execution.

- [ ] **Step 2: Run RED**

Run the new workflow assertion test.
Expected: six rather than twelve variants and coverage still attached to the combined job.

- [ ] **Step 3: Split the jobs without changing test commands**

Add a two-valued `variant` matrix under the existing `go` job rather than duplicate its YAML. Untagged legs own `go mod tidy -diff`, untagged vet/test, Linux default globalopen, release override and their own logs. Tagged legs own tagged vet/test and their own logs. Apply the existing OS-specific skip expression and serve-skip audit independently to both. Keep `TestMain`/coverage unnarrowed and do not add `t.Parallel`.

- [ ] **Step 4: Run structural checks**

Run the new test, `git diff --check`, and a local YAML parser available in the repository's pinned toolchain (if no parser is already present, use Ruby's standard `YAML.safe_load` rather than install a dependency).
Expected: twelve valid matrix legs, no duplicate OS/variant tuple, all original OS names present and all skip scripts retained.

- [ ] **Step 5: Review and commit**

Review command/timeout preservation and per-OS log independence. Commit `ci: split ordinary and tagged Go tests` with the required identity/footer.

---

### Task 3: Writer-Fault Leaf Count Guard

**Files:**
- Create: `.github/scripts/check-writer-fault-shards.sh`
- Create: `.github/scripts/check_writer_fault_shards_test.py`

**Interfaces:**
- Produces: `check-writer-fault-shards.sh SHARD EXPECTED LOG [LOG...]`.
- SHARD is `core`, `runtime-init`, `incident-prevention`, `turns`, `attest-repair`, `record-sidecar`, or `total`; EXPECTED is respectively 105, 114, 147, 75, 111, 102, or 654.

- [ ] **Step 1: Write failing synthetic-log tests**

Generate actual terminal-shaped lines such as `    --- PASS: TestLiveWriterFaults/core/label/close-EIO (0.01s)`. Create six logs with the exact shard counts. Test that all six pass individually and together. Add missing leaf, duplicate leaf, foreign caseID, failed leaf, skipped leaf, wrong expected count, duplicate across two shards, and parent-only `654` summary cases.

```python
proc = subprocess.run([guard, "turns", "75", log], text=True, capture_output=True)
self.assertEqual(proc.returncode, 0, proc.stderr)
proc = subprocess.run([guard, "total", "654", *six_logs], text=True, capture_output=True)
self.assertEqual(proc.returncode, 0, proc.stderr)
```

- [ ] **Step 2: Run RED**

Run `python3 -I .github/scripts/check_writer_fault_shards_test.py`.
Expected: missing guard failure.

- [ ] **Step 3: Implement exact leaf validation**

Use awk to parse only `--- PASS|FAIL|SKIP: TestLiveWriterFaults/<caseID>/<label>/<call>-<errno>` terminals. Reject any non-PASS, duplicate, foreign caseID or wrong count. In `total`, reject duplicate leaves across logs and require 654 unique leaves. Ignore parent lines and the summary's raw `len(matrix)` output; never use that summary as proof.

- [ ] **Step 4: Run GREEN**

Run the unit tests and `sh -n .github/scripts/check-writer-fault-shards.sh`. Mark the guard executable in the commit.
Expected: all six positive manifests pass; every adversarial case fails with a specific diagnostic.

- [ ] **Step 5: Review and commit**

Review that a narrowed parent PASS cannot hide a dropped case. Commit `ci: validate writer fault shard counts` with the required identity/footer.

---

### Task 4: Six Writer-Fault Workflow Shards

**Files:**
- Modify: `.github/workflows/go.yml:423-559`
- Modify: `.github/scripts/check-writer-fault-shards.sh` only if review finds an integration mismatch; Task 3 tests must be rerun.

**Interfaces:**
- Consumes: Task 3 guard and the spec's exact caseID sets/counts.
- Produces: six writer jobs and shard-suffixed evidence consumed by Task 6's gate.

- [ ] **Step 1: Add failing shard-membership assertions**

Extend the workflow test to read the six shard names and require exactly the spec's caseID sets and expected counts: core 105, runtime-init 114, incident-prevention 147, turns 75, attest-repair 111, record-sidecar 102. Assert no caseID repeats and all six count guards run, including `total 654` across all six logs.

- [ ] **Step 2: Run RED**

Run the workflow assertion test.
Expected: one writer-fault job and no leaf guard.

- [ ] **Step 3: Implement the six jobs**

Use the existing writer-fault job ID with six matrix legs rather than duplicate job definitions. Each leg installs strace, uses an exact first-segment `-run` alternation for its caseID set, preserves the no-skip check with `TestLiveWriterFaults` listed, and invokes its leaf guard. Keep `difftest-live`'s `TestLiveWriterFaults` exclusion unchanged.

Only core owns Docker setup/pull, `TestLiveRealDocker`, `TestLiveRealDockerProjectBuild`, `TestLiveDemoArtifactFaults` and GLOBALOPEN native controls. Make those steps conditional to core. Suffix every uploaded artifact by shard and include that shard's raw writer log. Task 6's `go-ci-gate` downloads all six logs into distinct directories and runs the total guard; no seventh writer job is added.

- [ ] **Step 4: Run structural and synthetic integration checks**

Run the workflow assertions, the Task 3 unit tests and `git diff --check`. Feed each shard's own synthetic log plus all six to the total guard.
Expected: exact disjoint membership, one Docker/GLOBALOPEN owner and unique artifact names.

- [ ] **Step 5: Review and commit**

Review that no test runs twice and no parent-level summary substitutes for leaf evidence. Commit `ci: shard writer fault coverage` with the required identity/footer.

---

### Task 5: Three Stack/Live Shards and Duplicate Removal

**Files:**
- Modify: `.github/workflows/go.yml:561-641`

**Interfaces:**
- Consumes: the spec's three whole-test groups and existing platform skip expression.
- Produces: platform-process, turn-incident and context-sidecar jobs on both stack targets.

- [ ] **Step 1: Add failing membership and full-set assertions**

Require the three exact groups from the spec, each on `ubuntu-24.04-arm` and `macos-latest`. Assert `TestLiveServeBundleStackCallSites` is absent from stack-live but remains in serve-bundle-native. Assert all 150 snapshot cases, 120 turn sessions and 103 stores remain unnarrowed, `FAULTLINE_TURNSNAP_CASES` and every case-limit variable are explicitly unset, and TimSort/stack-depth reporting appear only in platform-process.

- [ ] **Step 2: Run RED**

Run the workflow assertion test.
Expected: one combined stack-live job and the duplicate stack-call-site test.

- [ ] **Step 3: Implement the three jobs**

Use the existing `stack-live` job ID with a three-valued `suite` matrix over both OS targets. Use whole top-level alternations exactly as the spec lists. Keep each shard's list/log and existing macOS skip expression. Do not narrow internal cases or move any attestation/provenance group out of `difftest-live`. Keep TimSort and measured stack-depth reporting once in platform-process.

- [ ] **Step 4: Run structural checks**

Run the workflow assertions and `git diff --check`.
Expected: three valid jobs per target, no duplicate stack-call-site execution, no narrowed case environment.

- [ ] **Step 5: Review and commit**

Review process-local state and full-set guards. Commit `ci: shard stack and turn live tests` with the required identity/footer.

---

### Task 6: Trigger/Gate Integration and Acceptance Measurement

**Files:**
- Modify: `.github/workflows/go.yml`, `.github/workflows/verify.yml`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md` only to record measured local/hosted status without claiming PR 2 complete
- Modify: execution ledger and new `.github/scripts/ci_gate_test.py` fixtures if integration adds a job result name

**Interfaces:**
- Consumes: Tasks 1–5 job IDs and guards.
- Produces: PR + main triggers, scope outputs, final `go-ci-gate`/`verify-ci-gate`, and measured before/after evidence.

- [ ] **Step 1: Add failing trigger and needs-graph assertions**

Require both workflows to keep `pull_request`, restrict push to `main`, and preserve Verify's `workflow_dispatch`. Require every heavy job to have `needs: [scope]` and `if: needs.scope.outputs.scope == 'code'`. Require each final gate to use `if: always()` and include every job in its needs graph, including the new split jobs. Check for missing, stale or duplicate needs IDs.

- [ ] **Step 2: Run RED**

Run the workflow assertions.
Expected: unrestricted feature push and no scope/final-gate graph.

- [ ] **Step 3: Integrate the classifier and gates**

Each `scope` job checks out full history, invokes Task 1 with PR base/event-head or main before/sha, writes `scope` to `GITHUB_OUTPUT`, prints sorted changed paths, and performs docs validation on docs-only. Heavy jobs run only for code. Final gates pass the complete `toJSON(needs)` object and the actual event/ref to `ci_gate.py`; preserve only the named non-main provenance skip. `go-ci-gate` also downloads all six writer logs into separate directories and runs `check-writer-fault-shards.sh total 654 ...`; docs-only gates skip artifact download. No third-party paths action is introduced.

- [ ] **Step 4: Run complete local validation**

Run all script unit tests, workflow assertions, `sh -n` on new shell scripts, Python compile checks, YAML parse and `git diff --check`. Run local `go test -count=1 ./internal/cli ./internal/proofview` as a smoke check that workflow edits did not alter the working tree's Go code. Expected: all local checks pass; hosted tests remain mandatory.

- [ ] **Step 5: Independent review and commit**

Dispatch fresh spec/quality review for trigger correctness, needs completeness, skip semantics, shard membership and evidence preservation. Resolve every Critical/Important issue and re-review. Commit `ci: gate pull request and main workflows` with the required identity/footer.

- [ ] **Step 6: Hosted acceptance and measurement**

After separately authorized publication, require the exact PR head's code-path gate and every non-exempt check to pass on all six targets. Audit actual leaf totals, artifacts, provenance exceptions and any skipped job. Measure run start/end and per-job durations against baseline runs `42065630748` and `42065640883`; record critical path and total compute without claiming the ~60-minute target if the data disagrees. Post-merge main must pass too. A later docs-only PR/commit must show both scoped gates and `CI-SCOPE docs-only`, not be represented as code-test acceptance.

Stop after accepted PR 1. PR 2 gets a new plan based on these measurements.
