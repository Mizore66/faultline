# Plain `serve` demo server — design

- **Date:** 2026-11-10
- **Status:** Design approved by the user in architecture, behavior, legacy HTTP, verification, and scope sections. Written-spec review and implementation-plan approval remain separate gates. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md), [interactive native RERUN design](2026-11-05-go-native-rerun-interactive-design.md), [judge-demo lifecycle design](2026-11-10-go-judge-demo-lifecycle-design.md), and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** judge-demo lifecycle merged through [PR #226](https://github.com/Mizore66/faultline/pull/226) at `9d28c7331921788c0d593a0ea3c6662cf1d90854`.

## 1. Deliverable and boundary

Port the frozen plain `serve` command: an in-memory cached REPLAY incident server with demo RERUN mutation, using Go only. Plain `serve` is a separate command from the already accepted read-only `serve --bundle` and secured native interactive `judge-demo`.

Included:

- Default/custom `--output` and `--port`, including an ephemeral port.
- In-memory REPLAY startup without bundle publication.
- `GET /`, `GET /api/analysis`, and synchronous demo `POST /api/rerun`.
- Demo bundle writing through `demo.Write` after a successful RERUN analysis.
- Exact route-specific output, headers, failure behavior, and SIGINT-only lifecycle.
- Independent frozen-TypeScript comparisons and real process/filesystem tests.

Not included:

- Native execution, startup approval, native sidecar publication/removal, independent post-write verification, or the secured incident-server credential policy.
- Automatic browser opening, stop summary, SIGTERM subscription, or a public timer option.
- Plain `serve` artifact discovery, UI hub, witness review, tutorial/live demos, release, oracle retirement, or TS deletion.
- Changes to `serve --bundle`, native executor/verifier rules, protected TypeScript, workflows, dependencies, or package manifests.

## 2. Selected architecture

Use a separate plain-demo handler on the existing Go proofview transport. `startNodeServer` is a Go `net/http` helper named for Node-compatible behavior; it starts no Node process. Do not add a legacy mode to the secured native `IncidentServer` and do not create another transport.

| Component | Responsibility |
| --- | --- |
| New `internal/proofview/demo_server.go` | Plain-demo routes, exact legacy headers, serialized RERUN state, current-analysis snapshots, and close delegation. |
| New `internal/cli/serve.go` | Option selection, output safety, initial REPLAY creation, RERUN publication callback, startup output, and SIGINT waiting. |
| `internal/bundle/demo/write_output.go` | Export `AssertSafeProofOutput(outputDirectory string) string` around the existing check and default proof root; behavior unchanged. |
| `internal/proofview/server.go` | Continue supplying the existing transport only; no route or lifecycle rewrite. |
| `internal/cli/serve_bundle.go` | Unchanged read-only verified-bundle command. |

Produced server interface:

```go
type PlainDemoRerunFunc func() (jsjson.Value, error)

type PlainDemoServerOptions struct {
    Analysis jsjson.Value
    Rerun    PlainDemoRerunFunc
}

func StartPlainDemoServer(options PlainDemoServerOptions, port float64) (*PlainDemoServer, error)
func (s *PlainDemoServer) URL() string
func (s *PlainDemoServer) Close() error
```

The CLI callback creates demo RERUN analysis, calls `demo.Write` with default options, and returns that analysis only after the write succeeds. The proofview package remains independent of CLI/native publication policy.

Use Go 1.27 and the standard library only. No production Node execution or new public command beyond plain `serve` is introduced.

## 3. Startup contract

1. Resolve `--output` using existing option behavior, defaulting to `.faultline/bundles/judge-demo`.
2. Create cached REPLAY analysis in memory with `demofixture.CreateReplayAnalysis()`.
3. Call `demo.AssertSafeProofOutput` before listening. It may inspect the path but must not create a bundle or write any file.
4. Coerce `--port` with existing JS Number semantics, defaulting to 4173 and allowing 0.
5. Start the plain-demo server on TCP4 `127.0.0.1`.
6. Print exactly:

```text
FaultLine incident page: <actual-loopback-URL>
Press Ctrl+C to stop.
```

7. Wait through the existing `awaitBundleInterrupt` helper.

A validation or listen failure returns immediately without browser launch, bundle publication, or stop summary. Output-path effects before a listen failure are not rolled back.

No startup verification, sidecar cleanup, browser opening, or native child execution occurs. `serve --bundle` keeps its independent dispatch, validation, and lifecycle.

## 4. HTTP contract

### 4.1 Routes and success responses

Query strings do not change route matching.

| Route | Success behavior |
| --- | --- |
| `GET /` | 200 current analysis rendered by the unchanged pure `RenderIncidentPage`; `Content-Type: text/html; charset=utf-8`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`. |
| `GET /api/analysis` | 200 compact current analysis JSON; `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store`. |
| `POST /api/rerun` | After demo analysis creation and `demo.Write`: 200 compact `{"ok":true,"mode":"RERUN","generatedAt":"<analysis.generatedAt>"}`; `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store`. |

Unknown routes and methods return 404 `Not found` with `Content-Type: text/plain; charset=utf-8`. No additional headers are added for blanket consistency. No bundle-file, download, diagnostic, or sidecar route exists.

The interactive incident renderer remains unchanged; plain serve does not add a nonce, credential header, or native startup-approval text to the page.

### 4.2 Legacy trust boundary

The user explicitly selected frozen-TypeScript compatibility for this command. The listener is loopback-only but performs no Host, Origin, Sec-Fetch-Site, token, or startup-approval check. No CSP or `Referrer-Policy` header is added.

This is intentional plain-demo behavior, not a relaxation of the secured native `judge-demo` server or read-only `serve --bundle`. It executes only the built-in demo analysis/writer; it does not select a repository, executable, hunk, witness, provider, or native sidecar.

### 4.3 RERUN failure responses

A demo analysis/write failure returns 500 with compact `{"ok":false,"error":<JSON-string-encoded actual error message>}` and `Content-Type: application/json; charset=utf-8`. Do not add no-store, nosniff, referrer, or other headers to this failure path.

The previous in-memory analysis remains visible. Filesystem effects already completed by `demo.Write` are asserted as they exist; there is no invented rollback, independent verification, or cross-file atomicity claim. Existing stale native-evidence files are left untouched.

## 5. Concurrency and lifecycle

A mutex serializes RERUN callbacks like Node's synchronous handler. Concurrent POSTs wait rather than receiving a new 409 response. GETs receive the current analysis under the same state protection; no renderer or JSON value is mutated concurrently.

A successful callback replaces the in-memory analysis before returning the success response. A failed callback leaves it unchanged. Server close rejects no special new route response and does not introduce native single-flight semantics.

The first SIGINT cancels no native work (none exists), closes/drains the shared transport, ignores the close error, and returns success without a stop-summary line. A second SIGINT takes the existing default action. SIGTERM retains default behavior. Windows `TerminateProcess` remains default termination, not graceful SIGTERM evidence.

## 6. Files and compatibility documentation

Expected scope:

- Add `internal/proofview/demo_server.go` and focused plain-demo server tests.
- Add `internal/cli/serve.go`, `serve_test.go`, and actual-process tests.
- Modify the CLI dispatch only to select plain `serve`; `serve_bundle.go` and its tests remain unchanged.
- Modify `internal/bundle/demo/write_output.go` only to export the existing output-safety check.
- Add `difftest/serve_demo_live_test.go` with independent frozen-TypeScript comparisons.
- Modify `difftest/KNOWN_DIFFERENCES.md`, the migration overview, and Slice 5 dependency map after corresponding acceptance exists.

New production diagnostics and statement positions must satisfy the applicable literal/coverage gates without exemptions. Do not alter native evidence ownership or secure native incident behavior to make this command pass.

## 7. Verification

### 7.1 Options and startup

Test exact stdout/stderr/exit for default/custom/nested output, default/zero/invalid/occupied port, output-safety refusal, and listen failure. Require no startup bundle write, no browser process, no sidecar operation, and no stop summary. Built-binary tests use a fresh directory with Node and Go absent from controlled PATH.

### 7.2 Independent HTTP and filesystem expectations

Compare Go with frozen TypeScript for exact startup, route status/body/headers, query strings, unknown methods, success/failure JSON, and SIGINT shutdown. Substitute only the actual ephemeral port and independently verified case-specific values; do not derive expected output from Go or normalize arbitrary errors/digests/timestamps.

Cover:

- Initial pure-renderer page and current analysis API.
- Successful RERUN analysis replacement and exact demo bundle state.
- Analysis/write failure with prior analysis retention and actual filesystem state.
- Concurrent POST serialization with no 409 and no response/page mutation before callback success.
- Repeated RERUNs with fresh demo evidence under the same server and no native sidecar changes.

### 7.3 Regression and acceptance gates

Use meaningful RED before production changes, then GREEN. Run affected ordinary/tagged/race/vet/literal/native-coverage gates, pure-renderer and read-only-server regressions, pinned live-oracle comparisons, actual-process tests, and six-target cross-compilation. Retain failures, skips, timeouts, and incomplete attempts; no platform skip or waiver is added. Every shell command stays below five minutes.

Require independent task/spec-quality reviews and a final whole-branch review. Hosted execution on all six targets and all required PR checks must pass before separate ready/merge authorization. Cross-compilation is compilation evidence only.

## 8. Completion and authorization

This project is complete only when the plain-demo option, HTTP, RERUN, lifecycle, independent expectation, native process, coverage, review, and hosted acceptance contracts pass. It does not complete `serve --bundle`, UI/discovery, the rest of Slice 5, release readiness, or TS retirement.

Writing or committing this spec authorizes only written-spec review and then preparation of an implementation plan. Implementation requires approval of that plan. Publication, ready/merge transitions, protected-source changes, release publication, branch/worktree/evidence deletion, and further subprojects retain separate authorization boundaries.
