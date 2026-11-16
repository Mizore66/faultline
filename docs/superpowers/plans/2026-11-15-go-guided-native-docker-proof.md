# Guided Native Docker Proof Qualification Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish real native Linux/Docker end-to-end guided proof evidence, then publish the authorized draft PR for full repository qualification.

**Architecture:** A Linux-only explicitly tagged Go integration test drives the ordinary built CLI, owns the Git/HTTP/Docker fixture and audits every proof run plus a second verifier process. One separate read-only-token workflow executes that test on a real Docker-enabled Ubuntu runner, preserving all diagnostics even on failure. Existing Go/Verify workflows and ordinary test eligibility remain unchanged.

**Tech Stack:** Go1.27.4 standard library and existing internal APIs/test helpers; Node22.22.2 for the existing doctor requirement; Docker with the existing digest-pinned Node22 Alpine image.

**Spec:** `docs/superpowers/specs/2026-11-15-go-guided-native-docker-proof-design.md`

**Status (2026-11-16):** Task 1 and Task 2 steps 1-4 are done: run 43740441666 at `80019e222e46fa6efd5b91ebdb8333da217ee589` PASSED and the independent data audit ACCEPTED it as a scoped fixture qualification (artifact 14172137333, 186 list hashes valid; 6 runs 3 PASS/3 FAIL, 1 transition, `COMMIT_PROOF`, 14-file bundle, root `sha256:493a2bf697531a564e3128e5927c0b9becd29e4ed3927807588cca8b29b6ed60`). Earlier run 43739802457 failed in setup with 0 artifacts and is preserved, not a pass. Step 5 (draft PR) and full exact-head CI remain pending; no ready or merge authority.

## Global Constraints

- Work in place on isolated branch `go-guided-linux-proof-20261115`, base3cc648f9b0947ceb87cec4ef1799926f61dbbd51; do not touch peer5i worktrees/index.
- User authorized ONE narrowly scoped Linux/Docker qualification workflow and draft-PR publication AFTER actual proof success. No merge/ready/release/TS-oracle retirement authority.
- No runtime/TS/manifests/dependencies/oldGoVerify workflows/skip-shard guards/generator/nativeexpected-corpus edits. No providers, fake Docker/recorders/injected runner or arbitrary repositories.
- Ordinary `fl` is unseeded and untagged. Native test is `linux && faultline_native_docker`, not `faultline_test`; absent prerequisites are failures, not skips.
- Image exactly `node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402`; existing isolation/resource/security flags unchanged; no image build/privileged daemon replacement.
- Every local shell<=240s, Go<=195s; serialize heavy commands under coordinated HOST ownership. All temp/logs under `$CLAUDE_JOB_DIR/tmp`, preserve all attempts and evidence, no cache/evidence deletion or branch/worktree cleanup.
- Controller alone performs explicit-path stage/normal-hook commit/verified-Anas-identity personal-remote push. Stop on push failure. GitHub only prefixed Mizore66 GH_TOKEN, never switch active account.

## File structure

| File | Responsibility |
| --- | --- |
| `internal/cli/investigate_guided_native_linux_test.go` | New native-tag test, owned fixture/process/HTTP human action, typed proof and independent verifier assertions, retained evidence |
| `.github/workflows/guided-native-docker-proof.yml` | New one-job Linux qualification, pinned setup/pull/preflights, exact test execution/no-skip/receipt/artifact guards |
| The spec/plan above | Bounded qualification design and execution contract |
| Existing six Windows-closure status documents | Factual scoped Windows completion and remaining Linux/fullCI gates; separate docs owner, no source overlap |

### Task 1: Native proof driver and narrow Linux workflow

**Consumes:** ordinary `./cmd/fl`, existing `servefixture.StartWithOptions`, CLI test process/build helpers, real Docker sandbox and `fl verify`.

**Produces:** `TestNativeGuidedInvestigateDocker` under explicit native tag and an independently reviewed workflow ready for actual hosted execution. No local native proof is claimed without Docker.

- [x] **1. Verify source contract before writing assertions.** Read the guided wrapper, incident continuation serialization, gitinv native proof/runs/verdicts and bundle verifier schemas. Use actual field names and managed `--output` rules. Reuse existing native image/permission conventions, not fake `guidedProcessEnv` or empty-PATH `builtBinaryEnv`. Inspect Git materialization of `.faultline` outputs before assuming cleanliness behavior.
- [x] **2. Add the native driver with no ordinary-suite Docker dependency.** The file starts exactly:

```go
//go:build linux && faultline_native_docker

package cli
```

The test's absent-prerequisite branch is a hard failure:

```go
docker, err := exec.LookPath("docker")
if err != nil {
    t.Fatalf("native guided proof requires a real Docker CLI: %v", err)
}
```

Use the spec's exact image and predicate marker. Create good/bad commits with fixed dates; build ordinary fl outside repo; use real PATH/Node/Docker and scrub test/provider/recorder overrides; preflight image digest/daemon and capture versions. Start `investigate --ci-log` at port0, assert pending chain/no freeze, parse real review form and post explicit fixture approval. Wait/join the process, parse the complete result, require proof-ready/native trust/isolation/per-run outcomes/transition and valid external witness/root bindings. Confirm the source's expected six executions before asserting six. Use existing jsjson helpers for result/canonical data. No expected-output masks or permissive status/count fallback.
- [x] **3. Add second verifier and retained diagnostics.** Run ordinary `fl verify <actual-directory> --expect-root <actual-root>` in a second process and require:

```text
Integrity: VALID
External root: MATCH
```

Retain source fixture, stdout/stderr, approval/frozen chain, complete proof bundle, ROOT/hash/manifest and Docker/version/exit receipts under an explicit evidence directory. Verify no newly owned containers remain; failure cleanup may remove only positively identified new test containers and must record cleanup. No blanket prune/remove or deletion of proof evidence.
- [x] **4. Create the one-job workflow.** Reuse current pinned actions (`checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd`, `setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`, `setup-node@a0853c24544627f65ddf259abe73b1d18a591444`, existing pinned upload-artifact action). Trigger only relevant pushes on `go-guided-linux-proof-20261115` plus workflow_dispatch. One `ubuntu-24.04` job, `contents: read`, Go1.27.4/Node22.22.2, no pnpm step, no secrets/repository write. Fail preflight instead of skipping and pull/inspect the exact digest.

The actual test command and terminal guard must bind the exact named test:

```sh
go test -tags faultline_native_docker -count=1 -p 1 -timeout=195s -v \
  -run '^TestNativeGuidedInvestigateDocker$' ./internal/cli
```

Require the named PASS, final PASS, explicit exit0 and no FAIL/SKIP through the existing check-skips script. Do not infer success from an empty selector. Always upload logs and the evidence root, with exact source/run/image/versions and outcome receipt; hashing/retention failure fails closed. Cover failure and cancellation terminals as well as success.
- [x] **5. Test-first/static and compilation checks locally.** No local Docker is available: do NOT relabel Podman. Add retained static workflow integrity negative controls for absent named execution, missing/no-skip guard, wrong image/tag/pins/permissions, and missing always-upload/exit binding. Compare pre-feature RED where meaningful, not invented product-behavior RED. Cross-compile the native CLI test binary for linux/amd64 and linux/arm64 with the custom tag into fresh output paths, and demonstrate ordinary/tagged suite eligibility remains unchanged. Compile is not execution. Run bounded relevant ordinary regressions only when HOST available; no install or new skip/shim. Source hashes and exact commands/exit/stderr required.
- [x] **6. Independent spec/quality/security review.** Include all new source/workflow/docs and actual schema/buildtag/real-runner assertions. Resolve Important defects before publication. Native acceptance is still pending. Workers never stage/commit/push or spawn reviewers.

### Task 2: Hosted native acceptance, closure and draft PR

**Consumes:** reviewed test/workflow and unchanged branch source hashes.

**Produces:** authenticated exact-head native proof evidence or an honest retained failure; draft PR only after the actual proof passes.

- [x] **1. Publish reviewed bounded branch.** Controller checks clean/protected scope, normal hook, actual Anas author+committer, exact personal SSH remote and prefixed GH login; commit explicit paths and normal-push only this branch. Never force/fallback on push failure. Do not push Windows closure docs back to the capture-only branch.
- [x] **2. Watch exact-head workflow with all terminal states.** Use one event-driven monitor with >=30s API interval; no manual status-poll loop or agent wait timers. Retain successes, failures, cancellations and timeouts separately. No retry is the original run's pass.
- [x] **3. Authenticate and audit actual proof.** Download artifacts into new empty directories, SHA/size/head/run-bind and safe-ZIP-check before data extraction; no downloaded code execution. Audit complete result/run catalog, native classifications, stable PASS/FAIL/transition, real Docker/image identity, frozen chain, independent verification/root/hash linkage and no owned container leftovers. A fake, injected, skipped or incomplete record is not acceptance. Fix/review/publish scoped defects if actual CI fails; preserve every attempt.
- [x] **4. Close precise status only after evidence.** Mark this real native Linux/Docker fixture qualified, not full six-platform/full GoVerify/main/Slice5/release parity. Retain Windows qualification and all known limits/historical failures. No docs-only source-edit rerun loop: serialize status and future PR head deliberately.
- [ ] **5. Publish the authorized DRAFT PR.** Only after native proof acceptance, push the intended complete head and create a draft PR into main using verified Mizore66. PR description links scoped Windows/default and Linux proof runs, retained caveats and pending full gates; ends with required generated-by footer. Monitor/fix full required exact-head Go/Verify checks. Do not mark ready, merge, bypass, release or retire TS without new applicable authorization.

## Self-review

The spec's boundaries map to Task1.1–1.4 (ordinary CLI/real fixture/HTTP/native proof/second verification), Task1.5–1.6 (compile/static/independent review), and Task2 (actual hosted evidence and conditional PR). Source/predicate/image are explicit; actual serialized field names must be verified from existing types, not copied from an uncertain exploration note. Local missing Docker never becomes a skip/pass. Test-only buildtag plus explicit workflow selection keeps ordinary jobs independent without hiding the new gate. Capture/default Windows evidence remains immutable and no production/runtime change is selected.
