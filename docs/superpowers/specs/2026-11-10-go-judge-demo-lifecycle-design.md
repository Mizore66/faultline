# Full judge-demo browser and shutdown lifecycle — design

- **Date:** 2026-11-10
- **Status:** Design approved by the user in architecture, behavior-contract, verification, and scope sections. Written-spec review and implementation-plan approval remain separate gates. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md), [interactive native RERUN design](2026-11-05-go-native-rerun-interactive-design.md), and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** interactive native RERUN merged through [PR #224](https://github.com/Mizore66/faultline/pull/224) at `0b430b94c6295bbb56d652646bce39daf88823e9`; merge-status documentation merged through [PR #225](https://github.com/Mizore66/faultline/pull/225) at `b662e4933b9d35b40c957384d2945291bbb1a7ea`.

## 1. Deliverable and boundary

Complete the remaining frozen `judge-demo` browser and shutdown lifecycle without changing the accepted native execution, artifact, or HTTP security contracts.

Included:

- Automatic local browser opening after successful interactive startup, with the frozen environment suppression rules and exact output.
- Clean first-SIGINT and first-SIGTERM shutdown, including active native cancellation, transport drain, exact stop summary, and exit 0.
- Default-action behavior for a second matching signal and settled behavior for the other signal after shutdown begins.
- Exact independent expectations and real-process coverage for browser, signal, and output behavior on supported platforms.
- Compatibility/status documentation after the corresponding acceptance exists.

Not included:

- Plain `serve`, the UI hub/discovery flow, witness review, or a shared lifecycle rewrite for those commands.
- A public `--serve-ms` option, browser-selection flag, remote URL, browser automation dependency, new framework, or new public Go API.
- Changes to startup approval, native analysis, bundle/sidecar schemas, independent verification, RERUN routes, single-flight rules, or export-only behavior.
- Protected TypeScript, workflow, package manifest, dependency, release, oracle-retirement, or TS-deletion changes.

## 2. Selected architecture

Reuse the existing frozen browser and dual-signal helpers rather than adding a second lifecycle implementation or generalizing the read-only bundle server's waiter.

| Component | Responsibility |
| --- | --- |
| `internal/cli/browser.go` | Already implements environment-gated browser launch, asynchronous opener completion/error reporting, and judge SIGINT/SIGTERM waiting. Reuse it directly and factor only its subscription setup so judge-demo can subscribe before startup while judge-proof behavior remains unchanged. |
| `internal/cli/judge_demo.go` | Preserve publication and startup ordering; invoke `openLocalDemoURL` after readiness; wait with `awaitServeInterrupt`; print the initial-root stop summary after close. |
| `internal/proofview` | Unchanged incident server, immutable snapshots, native cancellation, single-flight, and transport close semantics. |
| `internal/cli/serve_bundle_wait.go` | Unchanged. In particular, no browser, SIGTERM subscription, timer, or stop line is added to `serve --bundle`. |
| Tests and independent corpus | Fake opener behavior, real signal/process behavior, exact output, and frozen-TypeScript-derived expectations without new production test hooks. |

Use Go 1.27 and the standard library only. Production browser launch continues through the existing `nodeproc` shell helper; no new dependency or framework is introduced. The accepted package-private publication seams remain test-only and are not moved into exported production configuration.

## 3. Startup and browser contract

### 3.1 Ordering

Preserve the accepted startup sequence:

1. Select and publish cached REPLAY or approved native RERUN, including independent verification and native sidecar/stale-sidecar handling.
2. Print the existing publication summary.
3. If `--export-only`, return without a listener, browser launch, wait, or stop summary.
4. Start the loopback incident server and print the two existing readiness lines.
5. Invoke browser opening.
6. Wait for shutdown.

A publication or listen failure produces no browser launch, no signal wait, and no stop summary. Existing output and artifact effects before a listen failure are not rolled back.

### 3.2 Environment suppression

Port `shouldAutoOpenBrowser` exactly:

- `FAULTLINE_NO_BROWSER=1` suppresses opening.
- `CI=true` or `CI=1` suppresses opening.
- `GITHUB_ACTIONS=true` suppresses opening.
- Other values do not suppress opening.

Suppression prints exactly:

```text
Browser auto-open skipped in CI. Open <actual-loopback-URL> manually if needed.
```

The message goes to stdout and uses the actual ephemeral or configured loopback URL. Its wording remains “in CI” even when suppression came from `FAULTLINE_NO_BROWSER`; this is intentional frozen output, not a documentation rewrite.

### 3.3 Platform opener and failure

Without suppression, select exactly:

| Platform | Executable and arguments |
| --- | --- |
| macOS | `open <url>` |
| Windows | `cmd /c start "" <url>` (empty window-title argument) |
| Other supported systems | `xdg-open <url>` |

Run the opener asynchronously and reap it. Do not block HTTP readiness or native RERUN on opener completion, and do not terminate a browser process as part of server shutdown. Any launch or nonzero completion error writes exactly this line to stderr:

```text
FaultLine could not open a browser automatically. Open <url> manually.
```

Browser failure is fail-open: it does not close the server, change artifacts, alter exit status by itself, or suppress later RERUN. No success output is added.

## 4. Shutdown contract

### 4.1 First signal

Subscribe before the listener or readiness output can be exposed. On the first SIGINT or SIGTERM:

1. Mark shutdown settled so another signal cannot repeat the close/summary sequence.
2. Reject new runs and cancel the active native context through the existing incident server.
3. Wait for the active callback, reap native work, close the listener, and drain tracked connections.
4. Ignore the close error, matching the frozen fail-open stop path.
5. Print exactly:

```text
FaultLine judge-demo stopped. Bundle root: <initial-bundle-root>
```

6. Return success so the process exits 0 without an `ELIFECYCLE` wrapper diagnostic.

The summary always uses the root of the bundle published during initial startup. A later successful browser RERUN does not substitute its newer root. Failed or canceled publication retains the accepted artifact semantics; no rollback is invented.

### 4.2 Repeated and different signals

Match the two `process.once` listeners and settled guard:

- A second occurrence of the same signal after its listener has fired takes the platform default action.
- The other signal after shutdown has begun is ignored by the settled guard and does not run close or the summary again.
- A second matching signal that arrives only after close and summary completion cannot change the already successful result.

On POSIX, tests deliver real SIGINT/SIGTERM and distinguish clean exit from signal death. On Windows, native console Ctrl+C covers the supported graceful interrupt path. `TerminateProcess` remains default termination and is not represented as graceful SIGTERM delivery or as proof of Windows SIGTERM cleanup.

### 4.3 Active native execution and publication

The accepted incident-server close behavior remains authoritative. Shutdown during analysis cancels and reaps native execution. Shutdown during bundle write, verification, or sidecar publication waits for the callback to finish; context cancellation is not newly extended into those artifact operations. There is no stop-summary claim of cross-file atomicity.

## 5. Errors and compatibility

- Browser errors use only the exact stderr line above and remain independent of server success.
- Start errors return immediately with no browser launch, close, wait, or stop summary.
- Close errors are ignored after the first signal, and the summary still prints.
- No alternate accepted output, arbitrary timestamp/path normalization, new skip, broad HTTP waiver, production entropy override, or production fault switch is introduced.
- Existing REPLAY/export-only summaries, native execution evidence, verifier rules, HTTP security, and old golden/corpus bytes remain unchanged.
- New production diagnostics and new statement positions must satisfy the applicable literal and native-library coverage gates without exemptions.

Update `difftest/KNOWN_DIFFERENCES.md`, the migration overview, and the Slice 5 dependency map only with evidence actually obtained. Full judge-demo lifecycle completion must not be confused with plain `serve`, the remainder of Slice 5, release readiness, or TS retirement.

## 6. Expected files

Production and focused tests:

- Modify `internal/cli/browser.go` only to factor its existing subscription setup for an early-subscribing judge-demo caller; existing judge-proof behavior remains unchanged.
- Add focused judge-demo lifecycle tests without creating another browser or signal implementation.
- Modify `internal/cli/judge_demo.go`, `judge_demo_test.go`, and `judge_demo_server_process_test.go`.
- Add platform-specific signal tests only where real delivery is supported; do not add platform skips to make a red case pass.

Independent evidence:

- Add `difftest/judge_demo_live_test.go` with bounded independent lifecycle cases and pinned live TypeScript captures, reusing the existing fake openers and process helpers.
- Retain and compare exact startup, suppression, opener-failure, shutdown, and second-signal behavior for supported frozen-TypeScript oracle cases. Dynamic URLs, ports, roots, and process results must be derived from the actual case, not hidden by blanket normalization.
- Modify `difftest/KNOWN_DIFFERENCES.md` and the two existing migration status documents after implementation and acceptance.

`serve_bundle_wait.go`, `serve --bundle`, UI helpers, native executor/verifier code, `src/`, workflows, `package.json`, and `pnpm-lock.yaml` remain unchanged.

## 7. Verification

### 7.1 Independent browser expectations

Use fake platform openers to capture exact executable/argument selection and environment suppression. Cover successful launch, missing/failing opener, each suppression variable/value, actual ephemeral URL, and continued server operation after opener failure. No real browser or desktop session is required.

Recapture the supported frozen-TypeScript cases with pinned Node 22.22.2. Expected output must be independently retained and compared exactly; it must not be generated from the Go implementation.

### 7.2 Unit and real-process lifecycle

Cover:

- Startup/listen failure: no opener, wait, or stop summary.
- First SIGINT and first SIGTERM during idle serving.
- First SIGINT and first SIGTERM during real native execution, with child reaping and the accepted artifact state.
- Close error: summary and exit 0 remain fail-open.
- Second matching signal during a blocked close versus the other signal after settlement.
- Initial-root summary after a later successful native RERUN changes the bundle root.
- Exact stdout/stderr/exit status and no `ELIFECYCLE` output.
- Built `fl` from a fresh directory with Node and the Go compiler absent from controlled PATH.

Update existing process expectations only for the new contract: POSIX first-SIGTERM now closes and reports success, while Windows `TerminateProcess` retains its prior default-action result. Native Windows console Ctrl+C must continue through the owned console preparation and cleanup path.

### 7.3 Regression and acceptance gates

Use meaningful RED before production changes, then GREEN. Run affected ordinary/tagged suites, focused and package race checks, vet, literal/native coverage, independent generator no-drift, actual-process tests, and six-target cross-compilation. Every shell command remains below five minutes; retain failures, skips, timeouts, and incomplete attempts rather than calling them passes.

Require independent task/spec-quality review and a final whole-branch review. Hosted execution on all six targets and all required PR checks must pass before any separate ready/merge authorization. Cross-compilation is compilation evidence only.

## 8. Completion and authorization

This project is complete only when the browser and shutdown contract, independent expectations, native process behavior, coverage gates, reviews, and hosted acceptance all pass. It does not complete plain `serve`, the rest of Slice 5, release readiness, or TS retirement.

Writing or committing this spec authorizes only written-spec review and then preparation of an implementation plan. Implementation requires approval of that plan. Publication, ready/merge transitions, protected-source changes, release publication, branch/worktree/evidence deletion, and further subprojects retain separate authorization boundaries.
