# Native Interactive RERUN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a secured loopback `judge-demo` server that runs the reviewed native witness under explicit startup approval and publishes verified bundle/sidecar evidence before updating the browser.

**Architecture:** Extend the tested `internal/proofview` transport rather than duplicate HTTP machinery. A stateful incident server owns credential/security checks, immutable snapshots, single-flight execution and cancellation; `internal/cli` supplies one native-run/write/verify/sidecar callback. Preserve the frozen pure renderer and export-only behavior.

**Tech Stack:** Go 1.27 standard library, existing `jsjson`/`jsstr`, `net/http`, crypto/rand, existing native executor and demo bundle writer/verifier.

**Spec:** `docs/superpowers/specs/2026-11-05-go-native-rerun-interactive-design.md`

## Global Constraints

- Do not implement until the user approves this plan and PR #220 has merged; record the resulting main commit in the execution ledger before Task 1.
- Work on `go-native-rerun-interactive-20261105`; no new worktree, release, protected-source edit, oracle retirement, branch/evidence deletion or automatic merge.
- Use Go 1.27 and the standard library only. Never edit `src/`, `package.json`, `pnpm-lock.yaml` or workflows.
- Preserve cached REPLAY bytes, all 24 existing pure-renderer pages, export-only output/validation, native process deadlines and existing verifier rules.
- No platform skip, waiver, accept-either output, blanket HTTP normalization, production entropy environment override, real provider operation or real-looking secret. Test credentials are fixed only through unexported constructors.
- Every task uses meaningful RED before production, then GREEN and focused race/vet. Retain failures and incomplete runs in `.superpowers/sdd/2026-11-05-go-native-rerun-interactive/progress.md`.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`; verify author before any separately authorized push. Never bypass TruffleHog or change global Git config.
- Every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Each task receives independent spec/quality review before the next task; a final whole-branch review is mandatory.
- Every shell command stays below five minutes. Hosted six-target execution, not cross-compilation, supplies platform acceptance. Pending/skipped/failed checks are not passes.

---

### Task 1: Shared Native Approval Validation

**Files:**
- Modify: `internal/demofixture/native_witness.go:97-103`
- Test: `internal/demofixture/native_witness_test.go`

**Interfaces:**
- Consumes: existing `NativeApproval`, `NativeWitnessProposal`, `clock.Now`, `NativeAnalysisError`.
- Produces: `func ValidateNativeApproval(approval NativeApproval) error`; `RunNativeAnalysis` retains its signature and exact errors.

- [ ] **Step 1: Write direct failing tests**

Add `TestValidateNativeApproval` with the current proposal digest and `ApprovedAt: "2026-11-05T12:00:00.000Z"`; require nil. Reject a digest of `sha256:` plus 64 zeros and the exact existing APPROVAL text. Also cover noncanonical timestamp `2026-11-05T12:00:00Z`.

```go
if err := ValidateNativeApproval(valid); err != nil { t.Fatal(err) }
err := ValidateNativeApproval(bad)
var nativeErr *NativeAnalysisError
if !errors.As(err, &nativeErr) || nativeErr.Kind != "APPROVAL" { t.Fatalf("%v", err) }
```

- [ ] **Step 2: Run RED**

Run `go test -count=1 -run '^TestValidateNativeApproval$' ./internal/demofixture`.
Expected: compile failure, undefined `ValidateNativeApproval`.

- [ ] **Step 3: Implement shared validation**

Extract the existing digest/timestamp check without changing its text:

```go
func ValidateNativeApproval(approval NativeApproval) error {
    return validateNativeApprovalDigest(approval, NativeWitnessProposal().Get("digest").Str(), clock.Now())
}

func validateNativeApprovalDigest(approval NativeApproval, digest string, startedAt time.Time) error {
    approvedAt, err := time.Parse(time.RFC3339Nano, approval.ApprovedAt)
    if approval.ProposalDigest != digest || err != nil || clock.ISO(approvedAt) != approval.ApprovedAt || approvedAt.After(startedAt) {
        return &NativeAnalysisError{Kind: "APPROVAL", Err: errors.New("approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry")}
    }
    return nil
}
```

`newNativeSession` calls `validateNativeApprovalDigest(approval, proposal.Get("digest").Str(), startedAt)` before `deps.newExecutor()`.

- [ ] **Step 4: Run GREEN and regressions**

Run `go test -count=1 -run '^(TestValidateNativeApproval|TestNativeApproval.*|TestNativeProposal.*|TestNativeWitness.*|TestNativeAnalysis.*)$' ./internal/demofixture` and `go test -race -count=1 -run '^TestValidateNativeApproval$' ./internal/demofixture`.
Expected: all pass; invalid approval still creates no executor/child.

- [ ] **Step 5: Review and commit**

Record review, then stage only the two Task 1 files and commit `fix: share native approval validation` with the required identity/footer.

---

### Task 2: Independent Interactive Renderer Contract

**Files:**
- Modify: `internal/proofview/incident.go`, `internal/proofview/incident_template.go`
- Test: `internal/proofview/incident_test.go`, new `interactive_incident_test.go`
- Modify: `difftest/gen/gen-incident-renderer.mjs`
- Create: `difftest/testdata/incident-renderer/interactive-enabled.html`, `interactive-disabled.html`, `interactive-baseline.json`

**Interfaces:**
- Consumes: `RenderIncidentPage(analysis) string`, `EscapeHTML`, `jsjson.Stringify`.
- Produces: `type incidentInteraction struct { credential string; enabled bool }` and `func renderInteractiveIncidentPage(jsjson.Value, incidentInteraction) string` for Task 3.

- [ ] **Step 1: Create independent expected pages before production**

Extend the existing pinned-Node `gen-incident-renderer.mjs` so it reads frozen `difftest/testdata/incident-renderer/incident.html`, never Go output. With fixed credential `test_nonce-ABC123`, it replaces exactly two `<script>` opens with nonce-bearing opens. Enabled output replaces `fetch("/api/rerun",{method:"POST"})` with:

```ts
fetch("/api/rerun",{method:"POST",headers:{"X-FaultLine-Native-RERUN":"test_nonce-ABC123"}})
```

Disabled output replaces `<button id="rerun" type="button">` with its disabled form, replaces the status text with `native RERUN requires startup --approve-native`, and removes the complete rerun-listener script so no `/api/rerun` fetch remains. The generator writes full bytes and fails if any literal count differs. `interactive-baseline.json` records schema `faultline.native-interactive-renderer.v1`, source corpus path/hash, credential and SHA-256 for both pages.

- [ ] **Step 2: Write failing full-page tests**

Read both expected files and compare complete strings, not fragments:

```go
corpus := incidentTestLoad(t)
analysis, err := jsjson.Parse(corpus.rows[0].AnalysisJSON)
if err != nil { t.Fatal(err) }
for _, c := range []struct{ name, path string; enabled bool }{
    {"enabled", "interactive-enabled.html", true},
    {"disabled", "interactive-disabled.html", false},
} {
    t.Run(c.name, func(t *testing.T) {
        expected, err := os.ReadFile(filepath.Join("../../difftest/testdata/incident-renderer", c.path))
        if err != nil { t.Fatal(err) }
        want := string(expected)
        got := renderInteractiveIncidentPage(analysis, incidentInteraction{credential: "test_nonce-ABC123", enabled: c.enabled})
        if got != want { t.Fatalf("interactive page differs: got %d want %d bytes", len(got), len(want)) }
    })
}
```

Also assert disabled output contains no `/api/rerun` fetch and the existing `TestIncidentPageExact` remains unchanged.

- [ ] **Step 3: Run RED**

Run `go test -count=1 -run '^TestInteractiveIncidentPage$' ./internal/proofview`.
Expected: compile failure, undefined `renderInteractiveIncidentPage`/`incidentInteraction`. Do not regenerate expected pages to make the test pass.

- [ ] **Step 4: Implement minimal interactive rendering**

Build on `RenderIncidentPage` with guarded single-purpose replacements. Use `EscapeHTML(credential)` for nonce attributes and `jsjson.Stringify(jsjson.MakeString(credential))` for the JavaScript string. Require the expected literal counts before replacement. Keep `RenderIncidentPage` itself untouched. The enabled fetch includes the custom header; disabled output has a disabled button, exact read-only status and no rerun listener.

- [ ] **Step 5: Run GREEN and generator no-drift**

Run `go test -count=1 -run '^(TestInteractiveIncidentPage|TestIncidentPageExact|TestIncidentTimelineNamesAndJSTraps)$' ./internal/proofview` and `go test -race -count=1 -run '^(TestInteractiveIncidentPage|TestIncidentPageExact)$' ./internal/proofview`. Run `node difftest/gen/gen-incident-renderer.mjs --check`, then two `--recapture --scratch <separate-fresh-directory>` runs and compare both interactive files byte for byte.
Expected: all tests pass; check/recapture succeeds and both recaptures produce identical full files.

- [ ] **Step 6: Review and commit**

Review generator independence, full-page equality, escaping and unchanged old corpus. Commit `feat: add interactive incident rendering` with only Task 2 files and the required identity/footer.

---

### Task 3: Shared Transport and Secured Incident Routes

**Files:**
- Modify: `internal/proofview/server.go`, `internal/proofview/framing.go`
- Create: `internal/proofview/incident_server.go`
- Test: `internal/proofview/server_test.go`, new `incident_server_test.go`, `internal/proofview/framing_test.go`

**Interfaces:**
- Produces: `func startNodeServer(port float64, routes func(string) http.HandlerFunc) (*Server, error)` and `func nodeHandler(next http.HandlerFunc) http.HandlerFunc`.
- Produces: `type NativeRerunFunc func(context.Context) (jsjson.Value, error)`.
- Produces: `type IncidentServerOptions struct { Analysis jsjson.Value; RerunEnabled bool; Rerun NativeRerunFunc }`.
- Produces: `func StartIncidentServer(IncidentServerOptions, float64) (*IncidentServer, error)`, `URL() string`, `Close() error`; unexported `startIncidentServer(options, credential string, port float64)` for fixed-credential tests.
- Framing: private `bodyMarker = "Faultline-Node-Body"` with values `zero` or `nonzero`; caller-supplied copies are stripped like `expectMarker`.

- [ ] **Step 1: Add failing transport characterization and security tests**

Prove `startNodeServer` supplies the actual ephemeral URL to the route factory. Add raw HTTP cases for valid Host, DNS Host, HTTP/1.0 missing Host, malformed HTTP/1.1 missing Host, zero Content-Length, positive Content-Length and terminal `0\r\n\r\n` chunked input. Expected valid requests reach the handler; DNS/HTTP1.0 missing Host receive 403, while malformed HTTP/1.1 retains the existing parser 400. Add exact route/headers/body cases for GET `/`, GET `/api/analysis`, no-approval 403, bad token/origin/fetch metadata 403, unknown 404 and one immediate successful native callback.

- [ ] **Step 2: Run RED**

Run `go test -count=1 -run '^(TestStartNodeServer|TestIncidentServerHTTP|TestNodeBodyMarker)$' ./internal/proofview`.
Expected: undefined new constructor/marker failures. Existing server tests remain the regression baseline.

- [ ] **Step 3: Extract shared transport without behavior changes**

Move listener validation, URL construction, `http.Server` setup and goroutine startup from `StartGitProofServer` into `startNodeServer`. It wraps routes with the existing `responseFinished` and `expectMarker` behavior:

```go
func nodeHandler(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        c := r.Context().Value(connKey{}).(*nodeConn)
        defer func() { c.responseFinished(w.Header().Get("Connection") == "keep-alive") }()
        switch r.Header.Get(expectMarker) {
        case "continue":
            w.WriteHeader(http.StatusContinue)
        case "fail":
            nodeHeaders(w.Header(), r)
            writeNodeBody(w, r, http.StatusExpectationFailed, "")
            return
        }
        next(w, r)
    }
}

func startNodeServer(port float64, routes func(string) http.HandlerFunc) (*Server, error) {
    if math.IsNaN(port) || port != math.Trunc(port) || port < 0 || port > 0xFFFF {
        return nil, errors.New("options.port should be >= 0 and < 65536. Received type number (" + jsjson.FormatNumber(port) + ").")
    }
    address := "127.0.0.1:" + strconv.Itoa(int(port))
    listener, err := net.Listen("tcp4", address)
    if err != nil {
        code := nodefs.ErrnoCode(err)
        return nil, errors.New("listen " + code + ": " + nodefs.Describe(code) + " " + address)
    }
    url := "http://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
    handler := nodeHandler(routes(url))
    s := &Server{
        url: url, listener: listener, stop: make(chan struct{}),
        conns: map[*nodeConn]struct{}{}, drained: make(chan struct{}),
    }
    s.srv = &http.Server{
        Handler: handler, DisableGeneralOptionsHandler: true,
        MaxHeaderBytes: maxHeadBytes + 4096, MaxHeaderValueCount: maxHeadBytes,
        ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
            return context.WithValue(ctx, connKey{}, conn)
        },
        ConnState: func(conn net.Conn, state http.ConnState) {
            c := conn.(*nodeConn)
            switch state {
            case http.StateIdle:
                c.idled()
            case http.StateClosed, http.StateHijacked:
                c.destroy()
            }
        },
    }
    go s.srv.Serve(nodeListener{listener, s})
    go s.checkConnections()
    return s, nil
}
```

`StartGitProofServer` retains its pre-rendered page and exact route responses through this helper.

- [ ] **Step 4: Add framing marker and incident server**

At `endHead`, emit `zero` only when there is no Transfer-Encoding and Content-Length is absent or zero; emit `nonzero` otherwise. Generate a 32-byte credential using `crypto/rand` + `base64.RawURLEncoding` in exported `StartIncidentServer`; tests call the unexported constructor with `test_nonce-ABC123`.

Implement exact Host checks, token as one header value, zero marker, Origin/fetch checks and the spec's response ordering. Use `requestPathname`, `nodeHeaders`, `writeNodeBody` and `jsstr.ToUTF8`. Use the Task 2 renderer. JSON errors use `jsjson.Stringify(jsjson.MakeString(message))`; success has exactly `ok`, `mode`, `generatedAt`. No CORS or artifact route exists.

- [ ] **Step 5: Run GREEN and read-only regressions**

Run `go test -count=1 ./internal/proofview` and `go test -race -count=1 -run '^(TestStartNodeServer|TestIncidentServerHTTP|TestNodeBodyMarker)$' ./internal/proofview`.
Expected: all pass, including existing Git-proof raw HTTP/framing suites and every security case.

- [ ] **Step 6: Review and commit**

Review transport invariants, parser behavior, security ordering and exact bytes. Commit `feat: secure native incident server routes` with the required identity/footer.

---

### Task 4: Single-Flight State and Cancellation

**Files:**
- Modify: `internal/proofview/incident_server.go`
- Test: `internal/proofview/incident_server_test.go`, new `incident_server_race_test.go`

**Interfaces:**
- Consumes: Task 3 constructors and `NativeRerunFunc`.
- Produces: one active context/cancel function per `IncidentServer`; idempotent `Close` that cancels native work before draining the Task 3 transport.

- [ ] **Step 1: Write deterministic concurrency/cancellation tests**

Use channel-controlled callbacks, never sleeps:

```go
started := make(chan struct{})
release := make(chan jsjson.Value, 1)
rerun := func(ctx context.Context) (jsjson.Value, error) {
    close(started)
    select {
    case value := <-release: return value, nil
    case <-ctx.Done(): return jsjson.Value{}, ctx.Err()
    }
}
```

Start one raw POST, wait for `started`, then require a second POST to return 409 and GET `/api/analysis` to return the old bytes. Release the first and require the new snapshot. Add disconnect and `Close` cases that select on `ctx.Done()`, verify the callback returns, listener closes and no goroutine remains. Closing while idle also rejects a later run without starting a callback.

- [ ] **Step 2: Run RED**

Run `go test -race -count=1 -run '^(TestIncidentServerSingleFlight|TestIncidentServerDisconnect|TestIncidentServerClose)$' ./internal/proofview`.
Expected: concurrent requests both invoke callbacks or Close blocks because no cancellation/state guard exists.

- [ ] **Step 3: Implement single-flight state**

Add mutex-protected `analysis`, `running`, `closing`, `cancel`, `closeOnce` and `closeErr`. After security/approval checks, atomically reject busy/closing with the existing 409 body or install `context.WithCancel(r.Context())`. Run the callback outside the lock. Clear active state in defer; replace analysis only after callback success. Client cancellation flows through request context. `Close` sets closing, cancels once, then calls the shared transport close once and returns its error.

- [ ] **Step 4: Run GREEN under race**

Run the Step 2 command repeatedly once, plus `go test -race -count=1 ./internal/proofview`.
Expected: one callback per accepted run, deterministic 409s, prior snapshot while busy, cancellation/reaping and no race reports.

- [ ] **Step 5: Review and commit**

Review lock scopes, close/publication races and owned teardown. Commit `feat: serialize and cancel interactive reruns` with the required identity/footer.

---

### Task 5: CLI Integration and Real Native Artifacts

**Files:**
- Modify: `internal/cli/judge_demo.go`
- Test: `internal/cli/judge_demo_test.go`, `internal/cli/judge_demo_process_test.go`
- Create: `internal/cli/judge_demo_server_process_test.go`

**Interfaces:**
- Consumes: Tasks 1–4, `demo.Write`, `verifyGeneratedBundle`, `demo.WriteNativeEvidence`, `demo.RemoveNativeEvidence`, `awaitBundleInterrupt`, `jsjson.ToNumber`.
- Produces: `type judgeDemoPublication struct { written demo.Written; verification bundle.Result; analysis jsjson.Value; sidecar string }` and `func publishJudgeDemo(context.Context, string, bool, demofixture.NativeApproval) (judgeDemoPublication, error)`; interactive callback returns analysis only after all publication steps.

- [ ] **Step 1: Add CLI validation/startup failing tests**

Keep every existing proposal/export test unchanged. Add interactive cases for REPLAY with/without approval, initial native RERUN, wrong digest before child/output, missing/empty approval, replay/rerun precedence, default/custom output and invalid port. Require no `not yet ported: judge-demo server`. No-approval startup serves read-only HTML and returns exact 403 on POST; approved startup prints existing summary lines then:

```text
🚀 Launching FaultLine Judge Demo at <actual-URL>...
Press Ctrl+C to stop.
```

- [ ] **Step 2: Add real built-binary failing acceptance**

Build `fl`, launch outside the repository with controlled PATH lacking Node/Go and `--port 0`. Read startup lines until the actual URL. Parse the nonce from the HTML, send an empty POST with `X-FaultLine-Native-RERUN`, and require the frozen success JSON. Verify 39 receipts, final analysis, verified root and complete sidecar. Run a second POST and require distinct fresh evidence under the same startup approval. Add idle/active SIGINT and default SIGTERM process cases using existing portable process helpers.

- [ ] **Step 3: Run RED**

Run `go test -count=1 -run '^(TestJudgeDemoInteractive|TestJudgeDemoInteractiveProcess)$' ./internal/cli`.
Expected: existing `not yet ported: judge-demo server` failure; no fake success artifacts are written.

- [ ] **Step 4: Implement shared publication and interactive CLI**

Extract the existing analysis → write → verify/root match → sidecar/stale-removal sequence without altering export output. Interactive approval is optional for REPLAY and required for RERUN. Capture `ApprovedAt` once and validate through Task 1 before initial output/execution. Start `proofview.StartIncidentServer`; its callback invokes native analysis and the shared publication helper with the request context. Update summary only from actual results. Use `awaitBundleInterrupt` and `jsjson.ToNumber(port)`; do not auto-open a browser or print a stop summary.

- [ ] **Step 5: Run GREEN and artifact failure regressions**

Run `go test -count=1 -run '^(TestJudgeDemo|TestJudgePreviewDispatchIsolation)' ./internal/cli` and the real process test. Add induced publication failures through package-private seams only: execution before writes, bundle failure before sidecar, sidecar failure after bundle publication and stale-sidecar refusal. Require prior page retention, actual filesystem state and no success response. Run `go test -race -count=1 -run '^(TestJudgeDemoInteractive|TestJudgeDemoOutputRefusal)$' ./internal/cli`.

- [ ] **Step 6: Review and commit**

Audit the real 39-child evidence and all old export goldens before commit `feat: launch interactive native judge demo` with the required identity/footer.

---

### Task 6: Compatibility Records and Final Qualification

**Files:**
- Modify: `difftest/KNOWN_DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`
- Modify: execution ledger only; do not bulk-check historical plans.

- [ ] **Step 1: Record exact boundaries and local-only status**

Document startup approval with REPLAY, read-only without approval, native identity, Host/Origin/token/body rules, single-flight/cancellation, verified sidecar, parser-specific missing-Host behavior, and interim no-auto-open/SIGINT-only lifecycle. Status must say local qualification only with hosted acceptance pending until CI actually passes. Interactive RERUN is not full judge-demo, plain serve or Slice 5 completion.

- [ ] **Step 2: Run affected ordinary/tagged/race gates**

```bash
go test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -tags faultline_test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -race -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo
go vet -tags faultline_test ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl ./difftest
```

Retain full JSON events and every failure/skip. Do not claim the known local full-suite proofview timeout is fixed.

- [ ] **Step 3: Run focused coverage and platform compilation**

Run the focused native/literal selection:

```bash
go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest
```

Cross-compile the three affected packages into distinct fresh paths:

```bash
builds=$(mktemp -d /tmp/faultline-interactive-builds.XXXXXX)
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}; arch=${target#*/}
  for pkg in cli proofview demofixture; do
    suffix=""; if [ "$os" = windows ]; then suffix=".exe"; fi
    GOOS="$os" GOARCH="$arch" go test -c -o "$builds/$os-$arch-$pkg.test$suffix" "./internal/$pkg" || exit 1
  done
done
```

These are compilation only; actual six-target execution remains hosted.

- [ ] **Step 4: Final independent review and handoff**

Dispatch a fresh Sonnet whole-branch reviewer for spec compliance, security, correctness, test independence and deferred-minor triage. Resolve every Critical/Important finding through the task owner and re-review. Verify protected-path immutability, clean staged diff, TruffleHog, commit identity/attribution and retained failures. Commit `docs: record interactive native RERUN status` with the required identity/footer.

Stop at local delivery. Draft publication, ready/merge, further full-server/plain-serve work and every later Slice 5 subproject require their own authorization and plan gates.
