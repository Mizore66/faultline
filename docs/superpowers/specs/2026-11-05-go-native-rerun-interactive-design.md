# Native interactive RERUN for judge-demo — design

- **Date:** 2026-11-05
- **Status:** Design approved by the user in three sections (scope/approval, HTTP security/concurrency, artifacts/verification); written specification approved on 2026-11-05. Implementation-plan approval remains a separate gate. Planning clarified body-framing and parser-level missing-Host behavior in Sections 4 and 7 without changing the approved security direction. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md), [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map), [Native execution/provenance](2026-11-03-go-native-demo-rerun-design.md), and [Native RERUN CLI export](2026-11-04-go-native-rerun-cli-export-design.md).
- **Baseline:** PR #220 source `e8425ecd4a6dff4b40f23d3a578182d0b03fddae`. Its exact-head acceptance and merge are still pending while this specification is drafted; the implementation plan must rebase on and record the actual merged main commit.

## 1. Deliverable and boundary

Add a usable loopback `judge-demo` server whose browser RERUN button executes the existing reviewed native Go model under an approval supplied explicitly at server startup. Reuse the accepted native executor, incident renderer, demo-v2 writer/verifier, native-evidence sidecar, and tested proofview HTTP transport.

Included:

- Initial cached REPLAY or approved native RERUN through the existing `judge-demo` CLI selection.
- Optional `--approve-native <proposal-digest>` on an interactive REPLAY launch, enabling native browser RERUN for that server's lifetime.
- Initial verified bundle publication and native sidecar/stale-sidecar handling identical in ordering to export-only.
- `GET /`, `GET /api/analysis`, and a secured single-flight `POST /api/rerun`.
- Interactive HTML with an approval-enabled or read-only RERUN control.
- Browser-triggered native execution, verified bundle/sidecar publication, and last-successful in-memory analysis replacement.
- SIGINT shutdown with active native cancellation and child reaping.

Not included:

- Automatic browser opening, the complete frozen TypeScript SIGTERM/stop-summary lifecycle, or a claim that full `judge-demo` parity is complete. A full-server follow-up owns those remaining behaviors.
- Plain `serve`, UI hub/discovery, witness review, guided investigation, tutorial/live demos, packaging, release, oracle retirement, or TS deletion.
- Arbitrary repository/program execution, a general sandbox, remote attestation, a new bundle schema, a sidecar HTTP reader, download route, provider operation, or cross-process output lock.
- A per-click approval dialog. The user explicitly selected startup approval for the server lifetime instead.

`src/`, `package.json`, `pnpm-lock.yaml`, workflows, historical corpora, cached REPLAY construction, native process deadlines, export-only behavior, and existing verifier rules remain unchanged.

## 2. Selected architecture

Extend `internal/proofview`; do not create a second HTTP transport or a new demo-server framework.

| Component | Responsibility |
| --- | --- |
| `internal/proofview/server.go` | Keep the existing exported read-only Git-proof API and behavior. Extract only package-private listener/connection plumbing needed to serve another handler with the same framing, timeouts, request-target handling, and connection accounting. |
| New `internal/proofview/incident_server.go` | Own incident-server configuration, random credential, Host/Origin/token policy, routes, response headers, single-flight state, last-successful analysis snapshot, and close cancellation. |
| `internal/proofview/incident.go` / `incident_template.go` | Preserve `RenderIncidentPage` default bytes. Add a separate interactive rendering entry point/options that supplies CSP nonce, RERUN token wiring, and enabled/read-only control state. |
| `internal/cli/judge_demo.go` | Retain thin option selection and approval timestamps; orchestrate native analysis, write, independent verification, sidecar publication, startup output, and SIGINT waiting. Supply one publication callback to the incident server. |
| `internal/demofixture/native_witness.go` | Expose one shared approval-validation entry point if needed so interactive REPLAY startup rejects a bad digest before output without duplicating the native APPROVAL diagnostic. `RunNativeAnalysis` must use the same validation. |
| `internal/cli/serve_bundle_wait.go` | Reuse the existing SIGINT-only wait pattern where practical without changing `serve --bundle`. Do not import judge-demo's future SIGTERM/stop-summary behavior prematurely. |

The publication callback returns the successful analysis and summary metadata only after native execution, bundle write, verification, and sidecar publication all finish. The server never selects an executable, repository, hunk, output path, or witness from a request.

The existing proofview verifier call-site globals and renderer values are not assumed safe for concurrent mutation. Only one rerun/publication callback executes at a time, and GETs receive immutable independently owned analysis snapshots. No new package-global job or approval state is introduced.

Go 1.27 and the standard library are required. Digest/canonical JSON paths continue to use `jsjson`/`canonical`, not `encoding/json`. Production neither executes Node nor reads TypeScript, `dist`, testdata, or a runtime package manifest.

## 3. CLI selection and approval

### 3.1 Proposal and export-only precedence

Preserve the existing proposal-inspection and export-only contract byte for byte. In particular:

- `--native-proposal` rejects execution/output combinations before every other operation.
- Export-only `--approve-native` without `--rerun-all` still fails with `--approve-native requires --rerun-all.`
- Export-only RERUN still requires approval, and `--port` remains ignored.
- Existing first-exact-option semantics, ignored unrelated tokens, `--rerun-all` precedence over `--replay`, default/custom output handling, summary lines, and failure ordering remain unchanged.

The new optional-approval-with-REPLAY rule applies only to interactive invocation. It must not leak into export-only validation.

### 3.2 Interactive validation

For non-inspection, non-export `judge-demo`:

1. Determine `rerun` from `--rerun-all`, preserving its precedence over `--replay`.
2. If `--approve-native` is present, read its first exact value through `requiredOption`: absent, empty, or `--`-prefixed values use the existing missing-option diagnostic.
3. If RERUN is requested without approval, fail with `Native RERUN requires --approve-native <proposal-digest>.`
4. Resolve the default or supplied `--output` using the existing option behavior.
5. If approval is present, capture `ApprovedAt` once as `clock.ISO(clock.Now())` and validate the complete approval against `NativeWitnessProposal()` before initial output or child execution. A REPLAY launch therefore rejects an incorrect digest even though its initial analysis does not execute a child.
6. Construct cached REPLAY or call `RunNativeAnalysis` with that approval. Initial RERUN executes before output-path safety validation as in the frozen command.
7. Write, independently verify, require root equality, then publish native evidence for RERUN or remove stale native evidence for REPLAY.
8. Print the existing five summary lines plus the native-evidence line when applicable.
9. Coerce `--port` with existing JS Number semantics, defaulting to 4173 and allowing 0 for an ephemeral listener, then start the server. Output/bundle effects and the summary are not rolled back if listen fails.
10. Print the two launch lines below and wait for SIGINT. No automatic browser command or browser-suppression message is emitted in this slice.

```text
🚀 Launching FaultLine Judge Demo at <actual-loopback-URL>...
Press Ctrl+C to stop.
```

Without `--approve-native`, interactive REPLAY remains available as a read-only page and API. The RERUN control is disabled and `POST /api/rerun` returns the explicit 403 response in Section 4.3. Interactive native RERUN never uses the historical July JavaScript approval.

### 3.3 Server-lifetime authorization

The startup digest and `ApprovedAt` authorize every browser RERUN in that server process. Each run records that same authorization event but obtains fresh execution-entry/final timestamps, nonce, invocation receipts, environment observations, and analysis. This is not human authentication, a per-click event, an approval expiry mechanism, or permission for another process.

The approval is held in the server callback configuration, not in cookies or request bodies. No environment variable, production entropy override, or mutable global enables approval in tests.

### 3.4 Interim shutdown boundary

This slice installs SIGINT only. The first SIGINT prevents new runs, cancels the active native context, closes the listener, reaps native work, drains tracked connections under the existing transport rules, and exits successfully without a stop-summary line. A second SIGINT and SIGTERM retain the existing default-action behavior from the shared SIGINT wait pattern.

The full-server follow-up owns frozen judge-demo SIGTERM handling, forced-exit details, browser opening, and its stop-summary line. This slice must not claim those behaviors complete.

## 4. HTTP and interactive rendering

### 4.1 Routes and success responses

The listener is TCP4 `127.0.0.1` only. Query strings do not change route matching.

| Route | Success response |
| --- | --- |
| `GET /` | 200 interactive HTML rendered from the last successfully published analysis; `Content-Type: text/html; charset=utf-8`. |
| `GET /api/analysis` | 200 compact `jsjson.Stringify` analysis bytes without a trailing LF; `Content-Type: application/json; charset=utf-8`. |
| `POST /api/rerun` | After all publication steps: 200 compact `{"ok":true,"mode":"RERUN","generatedAt":"<analysis.generatedAt>"}` without a trailing LF; `Content-Type: application/json; charset=utf-8`. |

The success response deliberately retains the frozen three-field shape. It does not expose the bundle root, sidecar path, receipts, environment, local paths, or raw provenance. No route serves bundle files or diagnostics.

Every response includes `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`; HTML and API responses also include `Referrer-Policy: no-referrer`. The HTML CSP is exactly:

```text
default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-<server-credential>'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'
```

Unknown routes and methods return 404 `Not found` with `Content-Type: text/plain; charset=utf-8`, with normal HEAD body suppression. Shared documented framing/request-target differences remain narrow; this slice adds no blanket HTTP waiver.

### 4.2 Server credential and request trust

Generate one 32-byte cryptographically random credential per server with `crypto/rand`, encoded with `base64.RawURLEncoding`. Use it as both the HTML script nonce and the required custom RERUN header value. Production generates it internally; tests may supply a fixed credential through an unexported constructor dependency, never a production environment override.

Every request that reaches the handler must have a `Host` equal to the actual bound `127.0.0.1:<port>` authority. A mismatch, including a DNS name, receives 403 plain `Forbidden` before route handling. An HTTP/1.0 request without Host reaches the same 403. A malformed HTTP/1.1 request without Host can be rejected by the shared net/http parser with its existing 400 response before handler dispatch; that parser behavior is not relabeled a 403.

`POST /api/rerun` additionally requires:

- Header `X-FaultLine-Native-RERUN` whose single value equals the server credential.
- No request-body framing: no `Transfer-Encoding`, and either an absent `Content-Length` or exactly `Content-Length: 0`. Any positive Content-Length or any Transfer-Encoding, including a terminal zero-length chunk, receives 403 before the callback. The shared framing layer attaches a private zero/nonzero marker after validating framing; it does not expose decoded bodies to the handler, and no JSON request schema is invented.
- If `Origin` is present, exactly the server's `http://127.0.0.1:<port>` origin without a trailing slash.
- If `Sec-Fetch-Site` is present, exactly `same-origin`.

Absent Origin/fetch metadata is allowed for a non-browser local client that possesses the credential. A cross-origin browser cannot add the custom header without a successful preflight, and the server emits no CORS permission. Host checks prevent DNS rebinding from being treated as the loopback service.

No cookie, basic/bearer authentication, CORS wildcard, configurable public host, or request-selected callback is added.

### 4.3 RERUN refusal and failure responses

All JSON error bodies are compact, without a trailing LF, and use `Content-Type: application/json; charset=utf-8` plus the common no-store/nosniff/referrer headers.

For `POST /api/rerun`, evaluate Host and route matching first, then token/body/Origin/fetch security, then startup-approval state, then single-flight state, and finally execution/publication. Thus an invalid caller receives `Forbidden` without learning whether approval was configured or a run is active.

| Condition | Status and body |
| --- | --- |
| Server started without approval | 403 `{"ok":false,"error":"Native RERUN requires --approve-native <proposal-digest> at server startup."}` |
| Bad/missing token, positive Content-Length, any Transfer-Encoding, or invalid Origin/fetch metadata | 403 `{"ok":false,"error":"Forbidden"}` |
| Another native run is active | 409 `{"ok":false,"error":"native RERUN already running"}` |
| Native execution, cancellation, bundle write/verification, or sidecar publication failure | 500 `{"ok":false,"error":<JSON-string-encoded actual error message>}` |

The actual run/publication error is available only after Host/token/Origin checks and follows the frozen command's actionable-error behavior; unauthorized callers receive only `Forbidden`. This is not a public error-disclosure policy or a remote diagnostic API.

### 4.4 Single-flight, snapshots, and cancellation

A mutex-protected state machine has an idle/running/closing state and one active run context. Starting a run changes idle to running before any child executes. A second POST while running receives 409 immediately; no queue forms. GETs do not wait for the run and continue rendering the previous immutable analysis.

Client disconnect cancels that run's context. Server close changes the state to closing, rejects new runs, and cancels the active context. Native execution retains its existing two-second per-child and 90-second whole-analysis bounds. Close then waits for callback/HTTP completion under the existing connection rules; no detached rerun survives.

A successful publication replaces the in-memory analysis atomically under the mutex. Failed or canceled publication leaves the previous analysis visible. If cancellation races with completed filesystem publication, the filesystem result is not rolled back or misreported as atomic; tests must exercise the defined ordering rather than invent a rollback.

### 4.5 Interactive HTML

`RenderIncidentPage(analysis)` remains the frozen pure renderer and retains all existing corpus bytes. A separate interactive entry point returns the same complete page except for explicitly owned interactive differences:

- Both inline scripts carry the CSP nonce.
- The RERUN script supplies `X-FaultLine-Native-RERUN` and retains its disable/running/success/reload/error behavior.
- With approval, the button retains the frozen `Run the demo again` label.
- Without approval, the control is disabled and its status says that native RERUN requires startup `--approve-native`; it emits no request.

Dynamic values are inserted through existing JS/HTML escaping and WTF-8-safe conversion. No inline event handler, external asset, service worker, new browser automation dependency, or visual redesign is introduced. Full-page golden comparisons cover enabled and disabled variants with fixed test credentials.

## 5. Artifact publication and failures

### 5.1 Initial startup

Initial REPLAY and native RERUN reuse the export artifact sequence exactly: analysis, `demo.Write`, `verifyGeneratedBundle`, then `WriteNativeEvidence` for native or `RemoveNativeEvidence` for replay. No server listens and no launch line appears before these operations and summary output succeed.

The sidecar remains a sibling with the existing `faultline.native-demo-cli-evidence.v1` envelope, because this is still the `judge-demo` CLI's native execution evidence. Its analysis digest binds the final analysis, its bundle root binds the independently verified root, and provenance contains the complete native envelope. It is not included in `hashes.txt` or `ROOT.sha256` and is not authenticated by the demo-v2 verifier.

### 5.2 Browser RERUN

For each accepted POST, the callback performs in order:

1. `RunNativeAnalysis` using the startup approval and request context.
2. `demo.Write` to the server-captured output directory.
3. Independent `demo.Verify` and written/verified root equality.
4. `WriteNativeEvidence` using the exact returned analysis/provenance and verified root.
5. Atomically replace the server's analysis snapshot.
6. Return the frozen success JSON.

No success response or page update occurs before every artifact operation completes. No replay is substituted for native execution.

### 5.3 Failure windows

- Analysis failure publishes neither bundle nor sidecar and leaves the prior page/analysis.
- Bundle write/verification failure publishes no new sidecar and leaves the prior page/analysis.
- Sidecar failure occurs after a verified new bundle may already have replaced the old one. Use the existing reserved-file cleanup and joined errors. Leave the prior page/analysis, return 500, and do not secretly delete the new bundle or claim cross-file atomicity.
- A replacement/backup cleanup error can likewise leave published artifacts despite a 500. Tests assert actual filesystem state, not an assumed rollback.
- No browser REPLAY route exists, so browser execution never removes a native sidecar through the replay stale-removal path.

The server does not retry automatically. Separate server processes sharing one output remain unsupported and are not made safe by the in-process single-flight guard.

## 6. Files and compatibility documentation

Expected production scope:

- `internal/proofview/server.go`, new `incident_server.go`, and focused tests.
- `internal/proofview/incident.go` / `incident_template.go` and interactive full-page tests while preserving existing default goldens.
- `internal/cli/judge_demo.go`, `judge_demo_test.go`, and actual-process/server tests; reuse `serve_bundle_wait.go` without changing its existing command behavior.
- `internal/demofixture/native_witness.go` only for shared approval validation, with direct tests proving `RunNativeAnalysis` behavior is unchanged.
- `difftest/KNOWN_DIFFERENCES.md`: optional interactive approval with REPLAY, read-only without approval, native rather than Node execution, security headers/Host/Origin/token rules, single-flight/cancellation, post-write verification/sidecar evidence, and the interim no-auto-open/SIGINT-only boundary.
- Migration status documentation only after corresponding acceptance exists.

Do not alter unrelated proofview rendering, `serve --bundle`, the existing five-file native coverage ownership merely to avoid new diagnostic obligations, old corpora, protected TypeScript, workflows, or release files. New production diagnostics must satisfy the applicable literal/coverage gates rather than acquiring waivers.

## 7. Verification and acceptance

### 7.1 CLI and approval

Test exact stdout/stderr/exit for every existing proposal/export case plus interactive:

- REPLAY with/without approval; initial native RERUN; replay/rerun precedence; first-option and missing/empty/flag-like values.
- Wrong approval rejected before child/output on both REPLAY and native startup.
- No-approval read-only startup; default/custom/nested output; default/zero/invalid/occupied port.
- Startup bundle verification and native sidecar/stale removal; summary-before-listen behavior; no browser process.
- Actual SIGINT during idle and active execution, second-signal/default SIGTERM behavior, child reaping, and no stop summary in this slice.

### 7.2 Independent interactive expectations

Preserve all 24 existing renderer cases unchanged for the default pure entry point. Add an independently authored native-interactive contract corpus for full enabled/disabled page bytes with fixed credentials and representative REPLAY/RERUN inputs. Expected values must not be generated by the Go renderer under test and must not offer alternate accepted outputs.

Capture exact HTTP status, body bytes, headers, and connection behavior from the explicit contract. Fixed test credentials and an actual validated ephemeral port are the only declared dynamic values; do not normalize arbitrary errors, paths, digests, timestamps, or outputs. Existing frozen TypeScript evidence guides legacy shapes but cannot supply native approval/security behavior that it never contained.

### 7.3 Hostile HTTP and state

Cover Host variants, DNS rebinding, HTTP/1.0 missing Host, malformed HTTP/1.1 missing Host parser behavior, missing/duplicate/wrong token, cross-site Origin/fetch metadata, absent metadata with valid token, positive Content-Length and chunked bodies including a terminal zero-length chunk, unknown methods/routes, HEAD, query strings, security headers, no-store on every failure, and absence of CORS permission.

Exercise concurrent POSTs from separate clients and tabs: exactly one native run executes, others receive 409, GETs retain the previous snapshot, and no queue/background rerun remains. Race-test state transitions. Exercise disconnect and close during real child execution, publication failure, and close/publication races. Assert actual process/artifact state without sleeps/load races or detached test children.

### 7.4 Real native and artifact evidence

Run an actual approved browser POST through the real private child and all 39 invocations. Audit the complete analysis and sidecar, proposal-bound startup approval, fresh execution observations, 39 ordered receipts, catalog links, analysis digest, verified root, and sidecar exclusion from bundle integrity files. Repeat a second approved run in the same server and require distinct fresh evidence under the same startup authorization.

Test execution failure before writes, bundle failure before sidecar, sidecar failure after verified bundle publication, stale sidecar refusal, cleanup/joined errors, and prior-page retention. Assert the real filesystem for each window. Do not use length-only comparisons, digest exemption, accept-either output, or a production fault flag.

### 7.5 Regression, platform, and review gates

Run affected ordinary/tagged suites, existing renderer and read-only server goldens, native lifecycle/live-oracle groups, focused literal/native coverage gates, race, vet, diff/scope checks, and actual process HTTP/CLI tests. Existing full-suite local proofview timeout and tolerated Zod advisory remain explicit caveats, not passes.

Build `fl` and exercise REPLAY plus approved interactive native RERUN from a fresh empty directory outside the repository with Node and the Go compiler absent from controlled PATH and no runtime TS/dist/testdata/manifest. Cross-build all six targets as compilation evidence only; require actual Linux/macOS/Windows AMD64/ARM64 execution through source-defined hosted jobs. Use portable Windows process/console controls rather than Unix-only signal claims.

Obtain independent task-level spec/quality reviews and a final whole-branch review. All four exact-head workflows and every required check must pass before any separate ready/merge authorization. Pending, skipped, failed, or incomplete checks are never counted as passes.

## 8. Completion and authorization

This slice is complete only when the interactive approval/CLI contract, secured HTTP behavior, single-flight/cancellation rules, verified bundle/sidecar evidence, independent expectations, Node-free checks, independent reviews, and all six native executions are accepted. It does not complete full `judge-demo` lifecycle/browser parity, plain `serve`, the rest of Slice 5, release readiness, or TS retirement.

Writing or committing this spec authorizes only the user's written-spec review and then preparation of an implementation plan. The user must approve that plan before implementation. Publication, ready/merge transitions, releases, protected-source changes, oracle retirement, branch/worktree/evidence deletion, and further subprojects retain their own authorization boundaries.
