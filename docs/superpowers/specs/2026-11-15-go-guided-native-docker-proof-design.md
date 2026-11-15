# Guided native Linux/Docker proof qualification

- Date: 2026-11-15.
- Base: `3cc648f9b0947ceb87cec4ef1799926f61dbbd51`, with both native Windows capture/default-path jobs accepted in run [43726508255](https://github.com/Mizore66/faultline/actions/runs/43726508255).
- Status (2026-11-16): the design was implemented and the proof PASSED. Real Linux Docker run [43740441666](https://github.com/Mizore66/faultline/actions/runs/43740441666) at `80019e222e46fa6efd5b91ebdb8333da217ee589` succeeded, and an independent data audit ACCEPTED artifact 14172137333 (archive SHA256 `58bfdb0a5f2c066e777198203666aae2cb43bd0df5a0c5b806534fb092033db2`, 186 list hashes valid) as a scoped fixture qualification: 6 `NATIVE_DOCKER`/`DOCKER_ISOLATED` runs (3 PASS, 3 FAIL), 1 transition, `COMMIT_PROOF`, a 14-file bundle with root `sha256:493a2bf697531a564e3128e5927c0b9becd29e4ed3927807588cca8b29b6ed60`. Earlier run 43739802457 failed in setup before the test with 0 artifacts (log and metadata preserved, not a pass). One fixture on linux/amd64 only; full exact-head Go/Verify, six-platform CI, packaging, release and TS retirement remain open. A draft PR is the next step (not yet created); no ready or merge authority. Original 2026-11-15 status (bounded design selected; no proof result claimed) is historical.
- Parent authority: guided design `2026-11-13-go-guided-investigate-design.md`, Section 7's still-open real native Docker proof requirement.

## Scope

Add an executable real-Docker integration test and a read-only-token, single Linux job that exercises the ordinary built Go CLI end-to-end: owned two-commit repository, CI-log intake, real loopback human review/freeze, actual isolated Docker localization, portable proof export and independent bundle verification. This is qualification of existing behavior, not a new product feature or a substitute for full Go/Verify PR CI.

Local Docker is absent; Podman is not relabeled as Docker. The driver may be cross-compiled locally but only the hosted Linux run can establish native Docker success. A draft PR may be opened after proof acceptance, with merge and release still separately gated.

Excluded: runtime/TypeScript/manifests/dependency edits, old Go/Verify workflow or skip/shard-guard changes, fake Docker/recorders/injected runners, real providers, arbitrary target repositories, public helper commands, image builds or daemon configuration changes, branch/worktree/evidence deletion, releases and oracle retirement.

## Driver boundary

Create `internal/cli/investigate_guided_native_linux_test.go`, package `cli`, build constraint `linux && faultline_native_docker`, test `TestNativeGuidedInvestigateDocker`. Ordinary and existing `faultline_test` suites acquire neither a Docker dependency nor a new skip. Under the native tag, absent Docker/image/prerequisites fail loudly; no `t.Skip`.

Reuse the existing portable process ownership, HTTP parsing and ordinary CLI build helpers, but not `guidedProcessEnv` (which supplies fake Node/no Docker) or the empty-PATH `builtBinaryEnv`. Build `./cmd/fl` without `faultline_test` or native test tags, run it from a separate owned working directory, and remove fixture/provider/recorder/test override variables from its environment. Preserve the real Docker and Node PATH. Inspect actual Docker daemon/image identity and record versions, executable paths and the image digest.

Use the existing CI-approved multi-platform image index:

`node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402`

The sandbox's existing `--pull=never`, network-none, read-only, unprivileged and resource-limiting flags remain unchanged. CI pulls the exact digest before the test; no image build or privileged daemon replacement.

## Fixture and human action

Create one owned Git repository with fixed author/committer dates and exactly two commits changing only `state.txt` from `good\n` to `bad\n`. Inspect existing real-Docker fixture/materialization permission handling so uid 65534 can read the source; do not change production permissions or host configuration.

The witness command is a self-contained shell predicate, not an overlay:

```sh
if [ "$(cat state.txt)" = bad ]; then
  echo 'FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}'
  exit 1
fi
echo 'FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}'
```

Invoke the ordinary binary with `investigate --ci-log <owned-log> --repo <repo> --from <good> --to <bad> --id native-1 --port 0 --command <predicate> --image <pinned-image>`. Prefer the existing managed default proof output under `.faultline/git-proof-bundles`; an explicit output must obey that root and the real `--output` option.

Read actual stderr review readiness. Before human action, assert only pending draft/proposal exist, with no approval/freeze/proof bundle. GET the real page, extract its actual token and review digest through existing helper conventions, then POST `/approve-freeze` with `approvedBy`, `acknowledged=exact-witness-reviewed`, token and digest. This is explicit fixture human action over HTTP, not a production auto-freeze shortcut.

## Acceptance

The CLI exits 0 with `GUIDED_PROOF_BUNDLE_READY`, retains a matching frozen-witness digest, reaches Docker-ready native continuation and publishes an independently verified proof bundle with external root MATCH. Assert actual schema fields from the source, not guessed names. In the investigation and each sandbox result, require NATIVE_DOCKER, DOCKER_ISOLATED/native image identity, isProof and a PASS_TO_FAIL transition; reject actual injected/unsafe trust classifications.

For this one-file two-state fixture the source's three stable executions per state should yield six runs: three consistent PASS/exit0 for the good commit and three FAIL/exit1 for the bad commit. Confirm this through the existing algorithm before hardcoding the count. No permissive count adjustment to force success. Inspect every run rather than only a summary string.

Invoke a second ordinary `fl verify <bundle> --expect-root <root>` process, require `Integrity: VALID` and `External root: MATCH`, and preserve ROOT/hash/manifests/witness-chain data. Verify real approval/frozen records link the fixture reviewer. Record container inventory before/after and fail on new leftovers; failure cleanup may remove only clearly owned new test containers, never unrelated containers, images, caches or evidence.

All stdout/stderr/versions/input fixture/commands/approval chain/bundle/exit receipts are retained in an explicit evidence root. Process handles are disposed and goroutines joined on every path. An absent bundle, failure, timeout, skip or incomplete process is never a proof pass.

## CI and review

Create only `.github/workflows/guided-native-docker-proof.yml`, trigger on the isolated proof branch's relevant source/workflow paths plus manual dispatch, one `ubuntu-24.04` job, `contents: read`, no secrets or repository writes. Reuse exact existing action pins, Go 1.27.4 and Node 22.22.2. No pnpm install/build is required. Preflight Docker/Node/Git, pull and inspect the exact image, enumerate the named test, execute with strict timeout/log/exit handling, and run the existing no-skip/required-PASS guard. Artifacts upload even on failure, with source SHA and structured outcome receipts; retention/hash errors fail closed.

Before publication: source/compile/static controls and an independent spec/quality/security review. Native execution then supplies the real acceptance evidence. Preserve every failed attempt. After exact-head proof success, publish the authorized draft PR on this branch for full six-platform/coverage/security/fault CI; do not merge, mark ready, release or retire TypeScript automatically.
