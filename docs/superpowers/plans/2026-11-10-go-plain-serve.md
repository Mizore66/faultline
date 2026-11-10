# Plain Serve Demo Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port plain `serve` with cached REPLAY startup, built-in Go demo RERUN, exact legacy HTTP/output behavior, and SIGINT-only lifecycle.

**Architecture:** Add a real built-in Go fixture evaluator and `CreateRerunAnalysis`, independent of the reviewed native witness. Serve a separate plain-demo handler on the existing Go proofview transport; CLI supplies analysis/write callbacks and `demo.Write` without independent verification or a native sidecar. Leave `serve --bundle` and the secured native incident server unchanged.

**Tech Stack:** Go 1.27 standard library, existing `jsjson`/`canonical`/`clock`/`nodeproc`, pinned Node 22.22.2 as test oracle only.

**Spec:** `docs/superpowers/specs/2026-11-10-go-plain-serve-design.md`

## Global Constraints

- Work on a dedicated plain-serve branch; no protected-source edit, workflow edit, automatic merge, release, oracle retirement, or evidence deletion.
- Use Go 1.27 and the standard library only. Never edit `src/`, `package.json`, `pnpm-lock.yaml`, or workflows; production executes no Node.
- Preserve `CreateReplayAnalysis` bytes, `serve --bundle`, native execution/verifier rules, export-only behavior, and old golden/corpus bytes.
- Plain RERUN uses only the built-in Go sample, not `RunNativeAnalysis`, startup approval, independent post-write verification, or native sidecar publication/removal.
- Listener is TCP4 `127.0.0.1` only, with the frozen plain-demo Host/Origin/token policy (none), exact route-specific headers, and no new 409 response.
- No browser opening, stop summary, SIGTERM subscription, platform skip, waiver, accept-either output, blanket normalization, production entropy override, or real provider/desktop operation.
- Every task uses meaningful RED before production, then GREEN and focused race/vet. Every shell command stays below five minutes.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`; every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Each task receives independent spec/quality review before the next task. Final whole-branch review and hosted six-target acceptance are mandatory. Pending/skipped/failed/incomplete checks are not passes.

---

### Task 1: Built-In Go Fixture Execution

**Files:**
- Create: `internal/demofixture/fixture_execute.go`
- Test: `internal/demofixture/fixture_execute_test.go`

**Interfaces:**
- Consumes: `fixtureStates`, `CreateFrozenWitness`, `clock.Now`, `clock.NowMillis`, `clock.StartEvidenceTimer`, `clock.TempSuffix`, `digest`, `nodefs` temp/filesystem helpers.
- Produces: `func executeFixtureState(input, witness jsjson.Value, executionAttempt int) jsjson.Value`, returning one EXECUTED run record with actual built-in Go observations.

- [ ] **Step 1: Write direct execution RED tests**

Cover pass, failure, unresolved/compile error, and fresh identity:

```go
func TestExecuteFixtureState(t *testing.T) {
    states := fixtureStates()
    witness := CreateFrozenWitness()
    for _, c := range []struct{ index int; verdict, reason, stdout, stderr string }{
        {idxLastGood, "PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""},
        {idxFirstBad, "FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"},
        {idxUnresolved, "ERROR", "FIXTURE_COMPILE_ERROR", "", "partial fixture does not compile\n"},
    } {
        record := executeFixtureState(states[c.index], witness, 0)
        if record.Get("verdict").Str() != c.verdict || record.Get("reasonCode").Str() != c.reason ||
            record.Get("stdout").Str() != c.stdout || record.Get("stderr").Str() != c.stderr { t.Fatalf("record %s", jsjson.Stringify(record)) }
        if record.Get("executionKind").Str() != "EXECUTED" || record.Get("environmentDigest").Str() == digest(historicalEnvironment()) { t.Fatalf("execution identity %s", jsjson.Stringify(record)) }
        if record.Get("id").Str() == "" || record.Get("executedAt").Str() == replayExecutedAt || record.Get("durationMs").Num() < 0 { t.Fatalf("observations %s", jsjson.Stringify(record)) }
    }
    first := executeFixtureState(states[idxLastGood], witness, 0)
    second := executeFixtureState(states[idxLastGood], witness, 0)
    if first.Get("id").Str() == second.Get("id").Str() { t.Fatal("fresh executions reused an ID") }
}
```

Also require the temporary sample directory to be absent after each call and no native sidecar or external process to be used. Assert `witness.command` is not changed by this low-level function; Task 2 owns the analysis descriptor.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^TestExecuteFixtureState$' ./internal/demofixture
```

Expected: compile failure, `undefined: executeFixtureState`.

- [ ] **Step 3: Implement the bounded built-in evaluator**

Use a pure-Go sample evaluator for the three outcomes, with no external process:

```go
type builtInOutcome struct{ verdict, reason, stdout, stderr string }

func evaluateBuiltInSample(hunks map[string]bool) builtInOutcome {
    if hunks[unresolvedHunk] {
        return builtInOutcome{"ERROR", "FIXTURE_COMPILE_ERROR", "", "partial fixture does not compile\n"}
    }
    if hunks[sourceHunk] && hunks[rendererHunk] {
        return builtInOutcome{"FAIL", "WITNESS_ASSERTION_FAILED", "", "expected settlement USD; received EUR\n"}
    }
    return builtInOutcome{"PASS", "WITNESS_SATISFIED", "settlement currency preserved\n", ""}
}
```

`executeFixtureState` must:
1. Create `faultline-sample/<state>-<pid>-<millis>-<TempSuffix>` under the system temp root and defer complete cleanup.
2. Run only that pure evaluator under a two-second bound. A timeout returns ERROR/RUNNER_ERROR with actual stderr, never a pass.
3. Record actual stdout/stderr, monotonic duration, current `executedAt`, and environment `{runtime: runtime.Version(), packageManager:"none", runner:"built-in-sample-v1", network:"disabled", credentials:"redacted"}`.
4. Capture `started := clock.Now()` and use its actual nanosecond value in the ID preimage so same-millisecond fresh executions cannot collide; use `clock.ISO(started)` for `executedAt`.
5. Hash `{state:<id>,witness:<digest>,executionAttempt,started,stdout,stderr}` with `digest` for the ID.

No input repository, network, credential, external executable, or native analysis call is allowed.

- [ ] **Step 4: Run GREEN, race, and vet**

```bash
go test -count=1 -run '^TestExecuteFixtureState$' ./internal/demofixture
go test -race -count=1 -run '^TestExecuteFixtureState$' ./internal/demofixture
go vet ./internal/demofixture
```

Expected: all pass, fresh IDs, actual Go environment, cleaned temp directories.

- [ ] **Step 5: Commit**

```bash
git add internal/demofixture/fixture_execute.go internal/demofixture/fixture_execute_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: execute built-in demo fixture in Go" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Full RERUN Analysis Construction

**Files:**
- Modify: `internal/demofixture/analysis.go`
- Test: `internal/demofixture/analysis_test.go`

**Interfaces:**
- Consumes: Task 1 `executeFixtureState`, existing replay builder and `uniqueRunCatalog`.
- Produces: `func CreateRerunAnalysis() jsjson.Value`; `CreateReplayAnalysis` remains byte-identical.

- [ ] **Step 1: Write full-contract RED**

Keep the existing REPLAY digest test unchanged. Add `TestCreateRerunAnalysis` requiring:

```go
analysis := CreateRerunAnalysis()
if analysis.Get("mode").Str() != "RERUN" || analysis.Get("grade", "value").Str() != "A" { t.Fatal("RERUN grade/mode") }
if analysis.Get("minimization", "termination").Str() != "BIDIRECTIONALLY_VALIDATED" || !analysis.Get("prevention", "verified").Bool() { t.Fatal("validation not complete") }
if analysis.Get("generatedAt").Str() == replayExecutedAt { t.Fatal("stale generatedAt") }
if analysis.Get("witness", "command").Str() != "built-in-go-sample" { t.Fatal("descriptor claims Node") }
for _, tr := range analysis.Get("transitions").Items() {
    if tr.Get("kind").Str() != "PASS_TO_FAIL" { continue }
    if !tr.Get("stable").Bool() || len(tr.Get("boundaryRunIds").Items()) != 6 { t.Fatalf("unstable transition %s", jsjson.Stringify(tr)) }
}
if analysis.Get("warning").Str() != "Executed built-in sample. The runner only materializes the reviewed fixture; it is not a general-purpose sandbox." { t.Fatal("wrong warning") }
```

Require the first three claims to be EXECUTED, DERIVED, DERIVED with evidence IDs; every catalog record to be EXECUTED with a unique nonempty ID and actual Go environment; no native sidecar fields; and `jsjson.Stringify(CreateReplayAnalysis())` unchanged from its pre-task value.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^TestCreateRerunAnalysis$' ./internal/demofixture
```

Expected: compile failure, `undefined: CreateRerunAnalysis`.

- [ ] **Step 3: Generalize the existing analysis builder**

Refactor the existing body into `createDemoAnalysis(mode string)` without changing REPLAY output, then expose:

```go
func CreateReplayAnalysis() jsjson.Value { return createDemoAnalysis("REPLAY") }
func CreateRerunAnalysis() jsjson.Value { return createDemoAnalysis("RERUN") }
```

For RERUN only:
- Call `executeFixtureState(state,witness,0)` for base runs.
- For each transition endpoint, execute attempts 1,2,3; stable requires three distinct IDs, EXECUTED kind, equal verdict/state/witness/environment, and PASS/FAIL verdicts. Include all six boundary IDs.
- Use fresh `clock.ISO(clock.Now())` for `generatedAt`; set `witness.command` to `built-in-go-sample` in the RERUN value tree.
- Use termination `BIDIRECTIONALLY_VALIDATED`, verified prevention when lastGood PASS/firstBad FAIL/repaired PASS, grade A only when the first stable regression exists, and the RERUN warning from the test.
- Replace the first three claims with the frozen EXECUTED/DERIVED/DERIVED statements and actual evidence IDs.
- Keep `uniqueRunCatalog` collision detection and locale sorting.

- [ ] **Step 4: Run GREEN and regressions**

```bash
go test -count=1 -run '^(TestCreateRerunAnalysis|TestReplay.*|TestCreateReplayAnalysis.*)$' ./internal/demofixture
go test -race -count=1 -run '^TestCreateRerunAnalysis$' ./internal/demofixture
go vet ./internal/demofixture
```

Expected: full RERUN contract passes and REPLAY bytes are unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/demofixture/analysis.go internal/demofixture/analysis_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: construct built-in demo RERUN analysis" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Plain-Demo HTTP Server

**Files:**
- Create: `internal/proofview/demo_server.go`
- Test: `internal/proofview/demo_server_test.go`

**Interfaces:**
- Consumes: existing `startNodeServer`, `requestPathname`, `nodeHeaders`, `writeNodeBody`, `RenderIncidentPage`.
- Produces: `PlainDemoRerunFunc`, `PlainDemoServerOptions`, `StartPlainDemoServer`, `URL`, `Close` exactly as specified.

- [ ] **Step 1: Write route/security/header RED tests**

Use `rawExchange` with a fixed initial analysis and a callback returning a second analysis. Require:
- `GET /?x=1`: 200, unchanged pure-renderer page, HTML/no-store/nosniff, no CSP or Referrer-Policy.
- `GET /api/analysis`: 200 compact current JSON and no-store.
- Unknown route and POST `/other`: 404 `Not found` with only the plain text/header behavior; HEAD gets no body.
- Host `example.invalid` still serves, proving no new Host security check.
- POST `/api/rerun`: exact success JSON with the second generatedAt and no-store; current page/API now show the second analysis.
- Callback error: exact 500 JSON with `Content-Type: application/json; charset=utf-8`, no `Cache-Control` header, and prior analysis retained.

```go
reply := plainDemoExchange(t, s, "POST /api/rerun HTTP/1.1\r\nHost: example.invalid\r\nConnection: close\r\n\r\n")
if reply.status() != "HTTP/1.1 200 OK" || reply.decoded() != `{"ok":true,"mode":"RERUN","generatedAt":"2026-11-10T00:00:00.000Z"}` { t.Fatalf("%s\n%s", reply.head, reply.body) }
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^TestPlainDemoServerHTTP$' ./internal/proofview
```

Expected: compile failure, `undefined: StartPlainDemoServer`.

- [ ] **Step 3: Implement the plain-demo handler**

Implement a `PlainDemoServer` holding `server`, `runMu` for callback serialization, `mu` for analysis/activeDone state, `analysis`, `rerun`, and `activeDone`. The route handler sets only the specified headers, calls `nodeHeaders`, and uses `writeNodeBody`. It does not call the secured incident authorization path.

RERUN execution holds `runMu` through the callback, serializing POSTs without a 409. Do not hold `mu` while the callback runs: GETs can render/stringify the previous analysis during it. Replace analysis under `mu` only after success. Set `activeDone` under `mu` before the callback and close it afterward. `Close` snapshots `activeDone`, waits for it, then delegates to `Server.Close`, preventing zero-body POSTs from being treated as idle while the callback still runs.

Success response is emitted only after callback success. Error response leaves analysis unchanged and has no extra cache/security headers.

- [ ] **Step 4: Add concurrency/close GREEN coverage**

Start one callback blocked on `release`. Start a second POST and require no second callback and no 409 before release; GETs retain the previous analysis. After release, both complete without queueing another callback. In a separate case, start `Close` during the callback and require it to return only after callback completion/transport drain.

Run:

```bash
go test -count=1 -run '^(TestPlainDemoServerHTTP|TestPlainDemoServerConcurrency|TestPlainDemoServerClose)$' ./internal/proofview
go test -race -count=1 -run '^(TestPlainDemoServerHTTP|TestPlainDemoServerConcurrency|TestPlainDemoServerClose)$' ./internal/proofview
go vet ./internal/proofview
```

- [ ] **Step 5: Commit**

```bash
git add internal/proofview/demo_server.go internal/proofview/demo_server_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: serve plain demo analysis and RERUN" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Plain Serve CLI and Output Safety

**Files:**
- Modify: `internal/bundle/demo/write_output.go:30-68`
- Create: `internal/cli/serve.go`
- Modify: `internal/cli/cli.go:127-130`
- Test: new `internal/cli/serve_test.go` and `internal/cli/serve_process_test.go`

**Interfaces:**
- Consumes: `CreateReplayAnalysis`, `CreateRerunAnalysis`, `demo.Write`, `StartPlainDemoServer`, existing `awaitBundleInterrupt`.
- Produces: `demo.AssertSafeProofOutput(outputDirectory string) string` and `plainServeCommand(e,args) error`.

- [ ] **Step 1: Write CLI RED tests**

Cover default/custom/nested output, invalid/occupied/zero port, output refusal, startup no-write, callback write failure, and exact startup/stop output. Replace the server starter with a fixture that captures options and returns a controlled URL/close. The core startup expectation is:

```go
"FaultLine incident page: http://127.0.0.1:<port>\nPress Ctrl+C to stop.\n"
```

Require `Analysis.mode == REPLAY` and no startup bundle/sidecar writes. Invoke the captured Rerun callback and require `CreateRerunAnalysis` followed by `demo.Write` with default options, returning analysis only after write success. A write error returns the same error to the server and does not replace analysis.

Change the dispatch test so bare `serve` selects `plainServeCommand` while `serve --bundle` still selects `serveBundleCommand`.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestPlainServe|TestJudgePreviewDispatchIsolation)$' ./internal/cli
```

Expected: compile failure, undefined output wrapper/command or bare `serve` still returns `not yet ported`.

- [ ] **Step 3: Implement output wrapper and CLI callback**

In `write_output.go`:

```go
func AssertSafeProofOutput(outputDirectory string) string {
    return assertSafeProofOutput(outputDirectory, defaultProofRoot())
}
```

In `serve.go`, resolve output, call the wrapper before listening, create REPLAY, and use package-private seams matching existing style:

```go
plainServeWriteBundle = demo.Write
plainServeStartServer = proofview.StartPlainDemoServer
plainServeAwaitInterrupt = awaitBundleInterrupt
plainServeAssertOutput = demo.AssertSafeProofOutput
```

The Rerun callback calls `CreateRerunAnalysis`, then `plainServeWriteBundle(output, analysis, demo.WriteOptions{})`, returning analysis only on success. Start the server, print the two exact lines, and return `server.Close` to `plainServeAwaitInterrupt`. Add bare-serve dispatch after the existing `--bundle` case and before the generic unported case.

- [ ] **Step 4: Add actual-process GREEN evidence**

Build `fl`, run outside the repository with Node and Go absent and `FAULTLINE_NO_BROWSER=1`, require exact startup with no bundle write, perform real RERUN GET/POST/GET, verify the new demo bundle with `demo.Verify`, repeat RERUN with different fresh evidence, then SIGINT and require exit 0/no summary/no stderr. Test occupied port and output refusal without process leaks.

Run:

```bash
go test -count=1 -run '^(TestPlainServe|TestJudgePreviewDispatchIsolation)$' ./internal/cli
go test -race -count=1 -run '^TestPlainServe.*$' ./internal/cli
go vet ./internal/cli ./internal/bundle/demo
```

- [ ] **Step 5: Commit**

```bash
git add internal/bundle/demo/write_output.go internal/cli/serve.go internal/cli/cli.go internal/cli/serve_test.go internal/cli/serve_process_test.go internal/cli/judge_preview_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: add plain serve demo command" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Independent Frozen-TypeScript Comparisons

**Files:**
- Test: new `difftest/serve_demo_live_test.go`

**Interfaces:**
- Consumes: existing `judgeSides`, `judgeEnv`, `loopbackPort`, fake process helpers and `oracle.Skip`.
- Produces: exact plain-serve startup, HTTP, RERUN and SIGINT comparisons with declared built-in Go execution differences.

- [ ] **Step 1: Write RED live comparisons**

Add Unix `TestLivePlainServeLifecycle` and `TestLivePlainServeHTTP` using `judge-demo`-style start helpers but command `serve`. Compare startup, route bodies/headers, success/failure JSON, and SIGINT exit exactly after replacing only the actual ephemeral port and independently verified side-specific execution values.

For each side, verify its actual bundle with `demo.Verify`; never require TS and Go roots/IDs/timestamps/durations/environment digests to be equal. Compare their structural roles and protocol semantics, and require each side's printed/generated analysis to match its own verified artifacts.

- [ ] **Step 2: Run RED with the frozen oracle**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLivePlainServe(Lifecycle|HTTP)$' ./difftest
```

Expected: missing helper or behavioral mismatch; never capture expected output from Go.

- [ ] **Step 3: Run GREEN and existing serve regressions**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLivePlainServe(Lifecycle|HTTP)$' ./difftest
go test -count=1 -run '^(TestServeBundle.*|TestPlainServe.*)$' ./internal/cli ./internal/proofview
```

Expected: exact declared behavior matches; `serve --bundle` remains unchanged.

- [ ] **Step 4: Commit**

```bash
git add difftest/serve_demo_live_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test: compare plain serve with frozen TS" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Compatibility Status and Final Qualification

**Files:**
- Modify: `difftest/KNOWN_DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`
- Modify: execution ledger only; do not bulk-check historical plans.

- [ ] **Step 1: Record exact plain-serve boundaries and local-only status**

Document unauthenticated loopback-only demo RERUN, built-in Go evaluator and runtime/command identity, no startup write, serialized callbacks, exact route-specific headers, no independent verify/sidecar, and SIGINT-only lifecycle. Plain `serve` does not complete `serve --bundle`, UI/discovery, the rest of Slice 5, release readiness, or TS retirement. Until hosted acceptance completes, status is local qualification only.

- [ ] **Step 2: Run affected ordinary/tagged/race/vet gates**

```bash
go test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -tags faultline_test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -race -json -count=1 -timeout=195s -run '^(TestPlainServe.*|TestPlainDemoServer.*|TestCreateRerunAnalysis|TestExecuteFixtureState)$' ./internal/cli ./internal/proofview ./internal/demofixture
go vet -tags faultline_test ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl ./difftest
```

Retain full JSON events and every failure/skip. Known aggregate proofview and full CLI race limitations are not claimed fixed.

- [ ] **Step 3: Run focused coverage, live oracle, and platform compilation**

```bash
go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -json -count=1 -timeout=195s -run '^TestLivePlainServe(Lifecycle|HTTP)$' ./difftest
```

Cross-compile `internal/cli`, `internal/proofview`, and `internal/demofixture` for all six targets into distinct fresh paths. Compilation is not native execution evidence.

- [ ] **Step 4: Final independent review and handoff**

Dispatch a fresh whole-branch reviewer for spec compliance, security, correctness, test independence, and deferred-minor triage. Resolve Critical/Important findings through the task owner and re-review. Verify protected-path immutability, clean staged diff, TruffleHog, commit identity/attribution, and retained failures. Commit `docs: record plain serve status` with the required identity/footer.

Stop at local delivery. Draft publication, ready/merge, and every later Slice 5 subproject require separate authorization and plan gates.
