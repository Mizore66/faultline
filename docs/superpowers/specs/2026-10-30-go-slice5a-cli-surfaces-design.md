# Go migration slice 5a — help, quickstart and version

- **Date:** 2026-10-30
- **Status:** Complete and merged as [PR #215](https://github.com/Mizore66/faultline/pull/215), merge `647440884a6668042f7726f2cbade38886d00a41`, on 2026-10-30 under verified Mizore66 after separate ready/merge authorization. The approved implementation and additive 67-case corpus passed local checks and independent review. All four hosted workflows at reviewed head `707a28a8691d5dd8a13f3b94faa6f819ac34a406` succeeded on attempt 1: 52 successful jobs and two expected main-push-only provenance skips; all successful logs were audited. This is reviewed-head acceptance, not fresh merge-commit CI or signed-attestation evidence. Release publication remains unauthorized; new subprojects require their own approvals.
- **Parent:** [Go migration overview](2026-09-25-go-migration-overview.md), especially the compatibility contract and Slice 5 decisions.
- **Baseline:** Slice 4 merged through [PR #214](https://github.com/Mizore66/faultline/pull/214) at `6f2450549acb002f04a73dd3c1e3091299d7d59f`.
- **Oracle:** Node 22.22.2, source unchanged from `ce99a793af0b020bf1b9cf21114f8d7897b37207`; current-tree manifest version `0.1.4`. The source pin's own manifest is `0.1.3`; see Section 3.

## 1. Scope and boundaries

5a ports the existing fast CLI surface, without adding commands or changing proof semantics. At its Slice 4 baseline the Go dispatcher recognized these names but returned `not yet ported`; the no-argument case also needed replacing. The completed slice makes those paths usable in the standalone Go binary while the TypeScript oracle is still available.

| Surface | Frozen TS behavior | Go requirement |
| --- | --- | --- |
| No arguments; `help`, `--help`, `-h` | `usage`, unless a help argument selects advanced help | Same selection, exact output bytes, exit 0 |
| `help advanced`; advanced argument on a help alias | `advancedUsage` | Same selection on all three help aliases |
| `advanced` | `advancedUsage` | Exact output bytes, exit 0 |
| `quickstart` | `printQuickstart()` | Exact output bytes, exit 0 |
| `--version`, `-V` | `FaultLine <manifest version>\n` | Current-tree oracle value by default; embedded metadata as specified below |

The source of truth is `src/cli-help.ts`: `usage`, `advancedUsage`, `printQuickstart`, and `tryFastCliPath`. Do not rewrite its wording, reflow it, or improve its instructions while porting. Its Node/pnpm/JS-entry references remain intentionally unchanged until the separate Node-free behavior cutover.

**Out of scope:** server/UI flows, demos and tutorial, witness review/live proposals, guided investigation, new report-mode `--json` flags, diagnostic-hint changes, the doctor Node check, hook-command changes, release packaging/publication, dependency updates, and TS/oracle deletion. No files under `src/`, `package.json`, or `pnpm-lock.yaml` change.

The two no-caller helpers (`captureAllowlistedEvent` and `prevention-from-turn`) are not silently added to 5a or claimed ported. Their final disposition is a later compatibility-closure decision. PR #195 batch staging remains a post-parity performance item under the [approved Slice 4 spec](2026-10-27-go-slice4-turns-codex-design.md).

## 2. CLI contract and architecture

### 2.1 Argument selection

Preserve the actual fast-path rules, not an expanded interpretation of the help text:

- For `help`, `--help`, or `-h`, use advanced help when the first remaining argument is exactly `advanced`, or any remaining argument is exactly `--advanced`.
- Otherwise those aliases print normal help. Unrecognized extra arguments are ignored as TS ignores them: `help ignored advanced` is normal help, while `help ignored --advanced` is advanced help.
- `advanced`, `quickstart`, `--version`, and `-V` ignore extra arguments, matching TS.
- Standalone top-level `--advanced` is not a new alias. Unknown commands and all already-ported commands retain their current dispatch/error behavior.

Matching is case-sensitive. Preserve punctuation, Unicode, spaces, line breaks and final newlines. Every successful path writes only its defined text to stdout, writes nothing to stderr, and exits 0. Do not add a banner, color, progress text, environment-dependent advice, or an extra blank line.

### 2.2 Components and data flow

Keep the existing `cmd/fl` entry point and `internal/cli` dispatch architecture. Put the help strings and selection logic in a focused `internal/cli/help.go`, using the existing `env.out` JS-to-UTF-8 output handling. Replace the empty-argument stub and route the existing fast-path names without changing the remaining command handlers.

Use one small `internal/buildinfo` package with a string variable `Version`, defaulting to `0.1.4`. The CLI version path reads that value. A linker stamp uses the same value, with no duplicate version constants or runtime environment override; the stamp target is `github.com/Mizore66/faultline/internal/buildinfo.Version`. No public version flag beyond the existing `--version`/`-V` is added.

The normal data flow is arguments → existing dispatcher → help selection or build version → `env.out`. These paths must not initiate repository discovery, doctor probes, browser/server startup, provider requests, or external commands. Do not read a runtime source tree, built JS file or package manifest. Existing process bootstrap and signal handling remain unchanged.

### 2.3 Error handling and intentional later changes

Help argument handling remains permissive exactly where TS is permissive. Do not introduce validation errors for ignored arguments. Unknown top-level inputs continue through the existing failure formatter, including its current `Next: pnpm fl help` and command-proxy hints.

Overview Slice 5 decision 2 is still binding: after help exists, a separate behavior-cutover subproject changes those hints to the binary form, with golden updates and live-oracle normalization. 5a needs no new diagnostic normalization. That later change must retain original oracle bytes, add explicit Go-target expectations, and normalize only the intended expected-TS diagnostic fields; it must assert the actual new Go text exactly, not accept either spelling or normalize arbitrary artifact content. The doctor, hook and affected help-text changes must be reviewed together under decision 4.

## 3. Version provenance and standalone behavior

The source pin, submission tag and CLI manifest version are not interchangeable:

| Reference | Manifest `version` | Meaning |
| --- | --- | --- |
| `ce99a793af0b020bf1b9cf21114f8d7897b37207` | `0.1.3` | Immutable frozen-source identity; also the locally cached tag target |
| Live remote `v0.1.10-buildweek` → `89b8e2348037ece69faf47b6e230a78c3bab6f44` (observed 2026-10-30) | `0.1.4` | Submission-tag checkout with identical `src/`; not the corpus baseline |
| `5c683c3` (`chore(release): 0.1.4`) | `0.1.4` | Existing manifest bump after the source pin |
| Baseline `6f2450549acb002f04a73dd3c1e3091299d7d59f` | `0.1.4` | Current-tree oracle manifest |

The explicit commit/manifest values were verified by local Git inspection; hosted pinned-tag checkout and read-only personal-remote `ls-remote` independently establish the different live tag target. The cached tag is not authoritative for remote identity. There are no changed `src/` paths between the source pin, live tag target and this baseline. `sourceCommit` declares that frozen-source identity, verified by the source no-diff invariant; capture executes a fresh current-tree build with the separately declared manifest, not a checkout of the source pin or tag. In `.github/workflows/go.yml`, the goldens job checks out the current revision at line 95 and builds TS at line 108; the main live job does the same at lines 135 and 148. Neither checkout specifies the submission tag. The other oracle jobs likewise have current-revision checkout/build pairs: investigate 272/285, writers 304/317, replay 336/349, faults 391/404, and stack 481/491 (baseline line numbers). Thus normal 5a parity builds print exactly `FaultLine 0.1.4\n`; this is a checked current-tree baseline, not an inference from a tag name.

### 3.1 Baseline artifact and preflight ownership

Add `difftest/testdata/cli-help/baseline.json`, deterministic UTF-8 JSON with exactly these fields:

| Field | Required value |
| --- | --- |
| `schema` | `faultline.cli-help-oracle.v1` |
| `sourceCommit` | `ce99a793af0b020bf1b9cf21114f8d7897b37207` |
| `manifestCommit` | `6f2450549acb002f04a73dd3c1e3091299d7d59f` (the baseline commit containing the oracle manifest) |
| `manifestVersion` | `0.1.4` |
| `nodeVersion` | `22.22.2` |

A dedicated `difftest/gen/gen-cli-help.ts` captures the new CLI-help cases into `difftest/testdata/cli-help/golden.jsonl` and reproduces this metadata. Do not force help commands through `gen-goldens.ts`'s verifier-only invocation path. Before writing either file, the new generator must verify actual Node version, the intact oracle manifest version and the oracle CLI's `FaultLine 0.1.4\n` output. A mismatch fails without rewriting existing evidence. Add this generator to the `difftest-goldens` job before its final no-drift check.

The new `TestLiveCLIHelp` in `difftest/help_live_test.go` uses the same committed metadata to preflight the actual oracle Node/manifest/CLI version before comparing commands. It belongs to the main `difftest-live` job's existing `TestLive*` discovery (`go.yml:209-212`), not a separately pinned checkout. A checkout of source pin `ce99a79` with its manifest (`0.1.3`) must fail this preflight; version strings are never normalized into equality. The generator and test do not silently adopt a changed manifest as a new baseline.

Permanent `TestCLIHelpReplay` in `difftest/help_test.go` compares the unstamped Go binary's complete output directly with the committed TS-captured goldens, including both default-version aliases. It validates the baseline metadata without running any oracle preflight. It must not read the TS manifest, start Node, require Git history, or require the oracle after retirement. A Go-only assertion that a constant equals `0.1.4` is not a substitute for this captured-output comparison.

### 3.2 Standalone and stamped versions

The Go binary carries its version. It does not reproduce TS's deployment-time manifest dependency: `faultLineVersion()` reads TS's own package manifest and returns `unknown` when that file is missing, malformed, or has a non-string version. Go keeps its embedded value in those circumstances. During implementation, add this precise standalone-metadata difference to `difftest/KNOWN_DIFFERENCES.md`; do not weaken the ordinary intact-oracle comparison.

A separately built test binary stamped with a clearly test-only version must print that exact stamp through both aliases. Test it independently of default-version goldens. The binary must retain its embedded version when run outside the repository, beside an unrelated changed/malformed manifest, or with no nearby manifest. These tests must not modify protected oracle files.

5a does not select or publish the first public Go release. The later distribution spec owns proposing its version and obtaining maintainer approval; binaries and npm platform packages must then share that approved version. Candidate stamping is not publication authorization.

5k/5l stamp release candidates through the linker value; they do not rewrite the unstamped parity default or the permanent version goldens merely because a release version differs or the TS manifest is retired. Default replay stays on the unstamped build and the historical captured baseline, while stamped candidates have their own exact-version assertions. A future change to the unstamped default requires a separately reviewed baseline transition that retains the original oracle evidence; it is not an implicit part of packaging or deletion.

## 4. Evidence and acceptance criteria

The items below define the implementation acceptance contract. The status above records completed reviewed-head acceptance; this documentation update does not claim fresh test execution.

### 4.1 Direct CLI tests

Exercise the existing CLI API and actual built binary where process isolation matters:

| Group | Required assertions |
| --- | --- |
| Default/normal help | No arguments and each help alias; exact `usage` bytes, empty stderr, exit 0 |
| Advanced selection | Each help alias with first argument `advanced`; each with `--advanced` in a later position; direct `advanced`; exact advanced text |
| Argument boundaries | Case-sensitive selectors; `help ignored advanced` stays normal; ignored extra args retain TS behavior; standalone `--advanced` stays an unknown-command failure |
| Quickstart/version | Exact quickstart; both version aliases with default and ignored arguments; separately stamped binary uses its exact stamp |
| Dispatch isolation | Unknown-command output/exit and representative already-ported command dispatch remain unchanged; no new runtime probes or external tool dependency |

Tests compare complete stdout and stderr bytes and exit codes, not substring/length-only output or several accepted variants. Normal and advanced help differ in content; the tests must detect selecting the wrong one.

### 4.2 Permanent and live oracle evidence

Append cases to the existing differential corpus/generator rather than replacing old goldens. Cover normal/advanced help, alias and argument boundaries, quickstart, and default version. Capture using actual Node 22.22.2 and the declared current-tree manifest. Preserve all previous golden entries and artifacts unchanged, including diagnostic text that this slice does not own.

The declared version metadata must be generated deterministically and checked by regeneration/no-drift. Live comparisons must check exact stdout, stderr and exit code under the same inputs. No new help-output mask, version normalization, error-hint rewrite or skip-allowlist expansion is allowed to make 5a pass. The standalone version and stamp differences are explicit direct tests, not ordinary oracle mismatches suppressed by the harness.

### 4.3 Node-free and platform checks

Run the built help/version paths from outside the repository with Node absent from the controlled PATH. Do not merely unset a Node-specific variable while leaving the executable available. Prove the commands work without runtime TS files or a package manifest. Keep test changes isolated from protected source/manifests.

Use the existing six native targets (linux, darwin, windows × amd64, arm64), affected untagged and `faultline_test` tests, permanent replay, live-oracle/no-drift jobs and the release no-test-override guard. Each target's fast-path smoke must actually run there; cross-compilation alone is not native evidence. Record platform eligibility and existing skips accurately. Full positional coverage remains Ubuntu amd64 evidence unless another platform's enabled gate is actually demonstrated.

The later implementation plan will specify exact selectors and bounded local commands after reading the relevant tests. Every local shell invocation must fit the aggregate sub-five-minute limit with owned process cleanup; do not detach or reset a watchdog to finish an over-budget test.

### 4.4 Done criteria

1. Every in-scope CLI path matches the declared intact TS oracle, with exact output/exit tests and the standalone-version difference explicitly documented.
2. New deterministic goldens, oracle metadata, regeneration/no-drift and live comparisons pass without changing prior golden expectations or weakening guards. Default replay compares the unstamped Go version output directly with the TS-captured golden; separately stamped candidates never replace that baseline.
3. Node-free and native six-platform fast-path checks pass, with default/stamped builds distinguished and all failures/skips retained in the evidence.
4. Existing command/error behavior, the frozen paths, artifact semantics and release test-override boundaries remain intact; the implementation receives independent spec/quality review.
5. The overview reflects actual Slice 4 closure and 5a status. No remaining Slice 5 subproject, release readiness or TS retirement is claimed complete by 5a.

## 5. Remaining Slice 5 dependency map

This is ownership and ordering for later specs, not implementation approval for the whole program. Keep each subsystem independently testable; split a row further if it is too large for one reviewable PR.

| Subproject | Owned scope | Dependency / gate |
| --- | --- | --- |
| 5a | Help, aliases, advanced, quickstart, version | Complete; reviewed-head acceptance and merged PR #215 |
| 5b.1 | Cached REPLAY fixture/analysis, demo-v2 writer, complete judge-preview | [Focused design](2026-10-30-go-slice5b1-cached-artifacts-preview-design.md) approved; [implementation plan](../plans/2026-10-30-go-slice5b1-cached-artifacts-preview.md) local implementation, independent review/final fix and bounded post-fix integration complete; full acceptance pending, including hosted six-native/Linux/positional/canonical-generation gates and long/full-tagged gaps (uncommitted; see the plan's status paragraph) |
| 5b follow-up | Genuine RERUN execution, interactive renderer, full judge-demo and plain serve | Own spec/approval; execution provenance, mutation security and HTTP/lifecycle evidence; judge-demo including export-only remains unported until then |
| 5c | `serve --bundle`, minimization/repair/prevention attachments and panels | Existing verifiers; hostile attachment and HTTP tests |
| 5d | UI hub, artifact discovery and turn view | Verified rendering/attachments from 5c |
| 5e | Witness review JSON, server, TTY and non-TTY | Witness/draft APIs; terminal and HTTP lifecycle tests |
| 5f | Witness live proposals and overlay input | Scripted local HTTP; request/response/error equality; no real provider operations |
| 5g | Guided investigate `--ci-log` / `--resume` | 5e plus incident/session APIs; resume/interruption behavior |
| 5h | Tutorial, demo live-git and demo full | Shared server path and existing proof pipeline; clock/UUID determinism |
| 5i | Compatibility closure and Windows recorder/fake portability | Explicit helper disposition; close Windows differential gaps before release |
| 5j | Node-free behavior in unreleased Go: doctor, hints, hooks and affected help | Local built-binary tests with Node absent; explicit golden differences/normalization; no deployed cutover |
| 5k | Candidate artifacts, npm wrapper, both Actions, plugin and Codespaces | Approved release version; static musl+cgo Linux signal/runtime checks; six native artifact smokes; no publishing |
| 5l | Final oracle evidence, CI/toolchain cutover and TS retirement | Full parity, preserved golden/security/fault coverage, explicit protected-path/unfreeze/deletion authorization |

5e and 5f are functionally independent; this does not authorize parallel implementations. Start Windows portability before final packaging rather than treating it as a last-minute release exception. The existing native Windows runtime tests do not close the POSIX-recorder writer gaps.

For 5j, the Go `cmd/fl` build target already exists: generated hooks must execute the actual locally built binary without Node. Existing deployed TS integrations are not redirected to a nonexistent release. 5k validates candidate artifacts and integration readiness before deployed cutover; a published asset or npm package still requires separate authorization after the program's full-parity gates.

Distribution specs must cover both `action.yml` and `actions/proof/action.yml`, version-pinned/checksum-verified binary acquisition, npm launcher exit/signal propagation (including disabled install scripts), and publisher/package registrations requiring maintainer action. Do not quietly add public helper commands or assume jq exists on every self-hosted runner. Preserve the overview's static musl+cgo Linux decision and test actual artifact signal behavior; a cgo-free cross-build is not that evidence.

Before 5l, inventory TS tests, oracle adapters, mixed-language generator files, scripts, benchmarks and security guarantees as preserve, port, historical artifact or explicitly retire. Keep permanent goldens and meaningful native/property/fault/HTTP tests. Remove obsolete oracle jobs and skip expectations together, rather than replacing lost coverage with permanent green skips. Do not assume TS dependency removal resolves the open advisory without a fresh final security review.

## 6. Review and authorization

The user separately approved the design/documentation direction, this written spec, and then the detailed implementation plan on 2026-10-30. Their execution choice, “1 and parallelize the executions”, authorizes parallel Sonnet implementation within 5a. Disjoint file ownership and dependency barriers preserve the contract; runtime verification remains serialized under explicit HOST ownership.

No commit or push is implied by spec approval. The later explicit 5a publication and PR #215 ready/merge authorizations were exercised for that PR only; they do not cover 5b.1 or another future PR. Protected-source changes, unfreeze/retirement, new ready/merge transitions, publication and external publisher setup retain their separate authorization requirements. Preserve personal Mizore66 identity, the normal TruffleHog hook and the required attribution for any subsequently authorized commit/PR. No real Codex/OpenAI operation is part of this work.
