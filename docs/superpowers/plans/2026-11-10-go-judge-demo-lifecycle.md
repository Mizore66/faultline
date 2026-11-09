# Judge-Demo Lifecycle Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete frozen `judge-demo` browser auto-open and clean SIGINT/SIGTERM shutdown with exact output and cross-platform process evidence.

**Architecture:** Reuse `openLocalDemoURL` and the existing dual-signal implementation in `internal/cli/browser.go`. Factor its existing subscription setup so `judge-demo` can preserve its current subscribe-before-start guarantee while `judge-proof` behavior stays unchanged. Wire the helpers into `judge_demo.go` with the initial bundle root in the stop summary; leave `serve --bundle` untouched.

**Tech Stack:** Go 1.27 standard library, existing `nodeproc`/`sigexit`, real process fixtures, pinned Node 22.22.2 live oracle.

**Spec:** `docs/superpowers/specs/2026-11-10-go-judge-demo-lifecycle-design.md`

## Global Constraints

- Work on a dedicated `judge-demo` lifecycle branch; no protected-source edit, workflow edit, automatic merge, release, oracle retirement, or evidence deletion.
- Use Go 1.27 and the standard library only. Never edit `src/`, `package.json`, `pnpm-lock.yaml`, or workflows.
- Preserve cached REPLAY bytes, export-only behavior, native process deadlines, verifier rules, HTTP security, artifact ordering, and `serve --bundle` behavior.
- Reuse `openLocalDemoURL`; do not create a second browser implementation or add a public `--serve-ms` option to `judge-demo`.
- Stop output is exactly `FaultLine judge-demo stopped. Bundle root: <initial-bundle-root>\n`, after close and with the initial root even after later RERUN.
- No platform skip, waiver, accept-either output, blanket normalization, production entropy override, real desktop/browser operation, or real-looking secret.
- Every task uses meaningful RED before production, then GREEN and focused race/vet. Every shell command stays below five minutes.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`; every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Each task receives independent spec/quality review before the next task. Final whole-branch review and hosted six-target acceptance are mandatory. Pending/skipped/failed/incomplete checks are not passes.

---

### Task 1: Early-Subscribing Dual-Signal Helper

**Files:**
- Modify: `internal/cli/browser.go:76-117`
- Test: new `internal/cli/judge_demo_lifecycle_unix_test.go`

**Interfaces:**
- Consumes: existing `awaitServeInterrupt(close func() error, serveMs *float64, onStop func())`, `sigexit.Intercept`, `sigexit.Die`.
- Produces: `func awaitJudgeDemoServeInterrupt(start func() (func() error, error), onStop func()) error`. It subscribes before `start`, returns a start error without close/summary, and otherwise performs the existing close/fail-open summary sequence.

- [ ] **Step 1: Write the subprocess RED tests**

Create a Unix test helper using the existing `TestServeBundleWaitHelper` pattern, not a standalone skipped child. Use `FAULTLINE_TEST_JUDGE_DEMO_WAIT_HELPER=fail|interrupt`; without the variable, return normally.

```go
func TestJudgeDemoServeWaitHelper(t *testing.T) {
    mode := os.Getenv("FAULTLINE_TEST_JUDGE_DEMO_WAIT_HELPER")
    if mode == "" { return }
    err := awaitJudgeDemoServeInterrupt(func() (func() error, error) {
        if mode == "fail" { return nil, fmt.Errorf("start failed") }
        if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil { return nil, err }
        fmt.Println("ready")
        if mode == "close-error" { return func() error { fmt.Println("closed"); return fmt.Errorf("close failed") }, nil }
        return func() error { fmt.Println("closed"); return nil }, nil
    }, func() { fmt.Println("stopped") })
    fmt.Printf("returned %v\n", err)
}
```

Add a real subprocess runner and assertions, not just the helper entry point:

```go
func TestJudgeDemoServeStartAndInterrupt(t *testing.T) {
    for _, mode := range []string{"fail", "interrupt", "close-error"} {
        cmd := exec.Command(os.Args[0], "-test.run=^TestJudgeDemoServeWaitHelper$", "-test.count=1")
        cmd.Env = append(os.Environ(), "FAULTLINE_TEST_JUDGE_DEMO_WAIT_HELPER="+mode)
        out, err := cmd.CombinedOutput()
        if err != nil { t.Fatalf("mode %s: %v\n%s", mode, err, out) }
        if mode == "fail" && string(out) != "returned start failed\n" { t.Fatalf("fail output %q", out) }
        if mode != "fail" && string(out) != "ready\nclosed\nstopped\nreturned <nil>\n" { t.Fatalf("%s output %q", mode, out) }
    }
}
```

Require `fail` to print `returned start failed\n` with no `closed`/`stopped`; require `interrupt` and `close-error` to print `ready\nclosed\nstopped\nreturned <nil>\n` and exit normally. The signal is sent inside `start`, so a helper that subscribes only after `start` dies instead of passing. The close-error case proves the close error is ignored and the summary still runs.

- [ ] **Step 2: Run RED**

Run:

```bash
go test -count=1 -run '^TestJudgeDemoServe(StartAndInterrupt|WaitHelper)$' ./internal/cli
```

Expected: compile failure, `undefined: awaitJudgeDemoServeInterrupt`.

- [ ] **Step 3: Extract the existing signal phases without changing judge-proof**

In `browser.go`, keep `awaitServeInterrupt` as the existing caller but split its body into subscription and completion helpers:

```go
func subscribeServeInterrupt(serveMs *float64) (chan os.Signal, <-chan time.Time) {
    signals := make(chan os.Signal, 8)
    signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
    sigexit.Intercept(syscall.SIGINT)
    sigexit.Intercept(syscall.SIGTERM)
    var timer <-chan time.Time
    if serveMs != nil && !math.IsInf(*serveMs, 0) && !math.IsNaN(*serveMs) && *serveMs >= 0 {
        timer = time.After(timeoutDuration(*serveMs))
    }
    return signals, timer
}

func finishServeInterrupt(signals chan os.Signal, timer <-chan time.Time, close func() error, onStop func()) {
    listenerRan := map[syscall.Signal]bool{}
    select {
    case sig := <-signals:
        listenerRan[sig.(syscall.Signal)] = true
    case <-timer:
    }
    go func() {
        for sig := range signals {
            s := sig.(syscall.Signal)
            if listenerRan[s] { sigexit.Die(s) }
            listenerRan[s] = true
        }
    }()
    close()
    func() { defer func() { recover() }(); onStop() }()
}

func awaitServeInterrupt(close func() error, serveMs *float64, onStop func()) {
    signals, timer := subscribeServeInterrupt(serveMs)
    finishServeInterrupt(signals, timer, close, onStop)
}

func awaitJudgeDemoServeInterrupt(start func() (func() error, error), onStop func()) error {
    signals, timer := subscribeServeInterrupt(nil)
    close, err := start()
    if err != nil { return err }
    finishServeInterrupt(signals, timer, close, onStop)
    return nil
}
```

- [ ] **Step 4: Run GREEN and existing signal regressions**

```bash
go test -count=1 -run '^TestJudgeDemoServe(StartAndInterrupt|WaitHelper)$' ./internal/cli
go test -count=1 -run '^(TestServeBundle.*|TestJudgeDemoServeWaitHelper)$' ./internal/cli
go test -race -count=1 -run '^TestJudgeDemoServe(StartAndInterrupt|WaitHelper)$' ./internal/cli
go vet ./internal/cli
```

Expected: all pass; existing `awaitServeInterrupt` output/signal behavior is unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/browser.go internal/cli/judge_demo_lifecycle_unix_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: add early judge-demo signal subscription" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Browser and Stop-Summary Wiring

**Files:**
- Modify: `internal/cli/judge_demo.go:30-39,160-182`
- Test: `internal/cli/judge_demo_test.go:174-199`
- Test: `internal/cli/judge_preview_test.go:262-289`

**Interfaces:**
- Consumes: `awaitJudgeDemoServeInterrupt` and existing `openLocalDemoURL`.
- Produces: `judgeDemoOpenBrowser = openLocalDemoURL` and `judgeDemoAwaitServe = awaitJudgeDemoServeInterrupt`, package-private seams matching existing test style. Interactive startup calls the opener after readiness and stop prints the initial root.

- [ ] **Step 1: Write lifecycle wiring RED**

Add `TestJudgeDemoLifecycleWiring` in `judge_demo_test.go`. Replace `judgeDemoOpenBrowser` with a recorder and `judgeDemoAwaitServe` with a controlled start/close/summary driver:

```go
func TestJudgeDemoLifecycleWiring(t *testing.T) {
    isolatedEmpty(t)
    var opened string
    oldOpen, oldWait := judgeDemoOpenBrowser, judgeDemoAwaitServe
    judgeDemoOpenBrowser = func(_ *env, url string) { opened = url }
    judgeDemoAwaitServe = func(start func() (func() error, error), onStop func()) error {
        close, err := start()
        if err != nil { return err }
        if err := close(); err != nil { return err }
        onStop()
        return nil
    }
    defer func() { judgeDemoOpenBrowser, judgeDemoAwaitServe = oldOpen, oldWait }()
    var out, errb bytes.Buffer
    code := Run([]string{"judge-demo", "--port", "0"}, &out, &errb)
    if code != 0 || errb.Len() != 0 || opened == "" { t.Fatalf("code=%d opened=%q stderr=%q", code, opened, errb.String()) }
    wantSuffix := "FaultLine judge-demo stopped. Bundle root: " + replayRetainedRoot + "\n"
    if !strings.HasSuffix(out.String(), wantSuffix) { t.Fatalf("stdout=%q, want suffix %q", out.String(), wantSuffix) }
}
```

Also change `runJudgeDemoServer` and `TestJudgePreviewDispatchIsolation` to use the new await signature, invoke `onStop` after their controlled close, and replace the opener with a no-op. The preview expected suffix becomes:

```go
const suffix = "...\nPress Ctrl+C to stop.\nFaultLine judge-demo stopped. Bundle root: " + replayRetainedRoot + "\n"
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestJudgeDemoLifecycleWiring|TestJudgePreviewDispatchIsolation)$' ./internal/cli
```

Expected: compile failure because the two new seams are undefined.

- [ ] **Step 3: Wire the existing helpers**

In `judge_demo.go`, replace `judgeDemoAwaitInterrupt = awaitBundleInterrupt` with:

```go
judgeDemoOpenBrowser = openLocalDemoURL
judgeDemoAwaitServe  = awaitJudgeDemoServeInterrupt
```

Replace the interactive tail with:

```go
return judgeDemoAwaitServe(func() (func() error, error) {
    options := proofview.IncidentServerOptions{Analysis: publication.analysis, RerunEnabled: approved}
    if approved {
        options.Rerun = func(ctx context.Context) (jsjson.Value, error) {
            rerunPublication, err := publishJudgeDemo(ctx, output, true, approval)
            if err != nil { return jsjson.Value{}, err }
            return rerunPublication.analysis, nil
        }
    }
    server, err := judgeDemoStartIncidentServer(options, jsjson.ToNumber(port))
    if err != nil { return nil, err }
    e.out("🚀 Launching FaultLine Judge Demo at " + server.URL() + "...\n")
    e.out("Press Ctrl+C to stop.\n")
    judgeDemoOpenBrowser(e, server.URL())
    return server.Close, nil
}, func() {
    e.out("FaultLine judge-demo stopped. Bundle root: " + publication.written.RootDigest + "\n")
})
```

Do not alter publication, RERUN, export-only, or server code.

- [ ] **Step 4: Run GREEN and regressions**

```bash
go test -count=1 -run '^(TestJudgeDemoLifecycleWiring|TestJudgeDemoInteractive|TestJudgePreviewDispatchIsolation)$' ./internal/cli
go test -race -count=1 -run '^TestJudgeDemoLifecycleWiring$' ./internal/cli
go vet ./internal/cli
```

Expected: all pass; startup failure emits no opener/summary; stop suffix uses the initial root.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/judge_demo.go internal/cli/judge_demo_test.go internal/cli/judge_preview_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat: complete judge-demo lifecycle wiring" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Actual Process Lifecycle Evidence

**Files:**
- Modify: `internal/cli/judge_demo_process_test.go:20-32`
- Modify: `internal/cli/judge_demo_server_process_test.go:25-150,232-239,294-444`
- Test: extend `internal/cli/judge_demo_lifecycle_unix_test.go`

**Interfaces:**
- Consumes: Task 2's real browser/wait calls and existing `servefixture.Started` signal methods.
- Produces: real-process proof of suppression, stop summary, first SIGINT/SIGTERM, repeated-signal behavior, active native cancellation, and initial-root retention.

- [ ] **Step 1: Change process expectations and add repeated-signal RED cases**

Set the default controlled binary environment to suppress browser opening:

```go
env := []string{"PATH=", "FAULTLINE_NO_BROWSER=1"}
```

In `startJudgeDemoProcessWithEnv`, use 8 startup lines for replay and 9 for initial native. In `requireJudgeDemoStartup`, append:

```go
want += "Browser auto-open skipped in CI. Open " + p.url + " manually if needed.\n"
```

Store the verified initial root on `judgeDemoProcess` and require clean SIGINT/SIGTERM output to equal:

```go
p.startup + "FaultLine judge-demo stopped. Bundle root: " + p.root + "\n"
```

Change the existing `default terminate` case: POSIX now expects clean exit 0, no signal, and the stop summary; Windows retains default `TerminateProcess` behavior and startup-only output.

Add Unix subprocess cases to `judge_demo_lifecycle_unix_test.go` for:
- SIGINT followed by SIGINT during a channel-blocked close: raw SIGINT death and no stop summary.
- SIGTERM followed by SIGTERM during the same close: raw SIGTERM death and no stop summary.
- SIGINT followed by SIGTERM: the other signal is consumed, close completes, one summary prints, and the helper returns nil.

Use a helper process and channels; never use sleeps as synchronization. The close callback sends `closeStarted`, waits on `releaseClose`; the parent sends the second signal only after receiving `closeStarted`. Run the same helper before Task 2's wiring if useful, but do not change production in this task.

- [ ] **Step 2: Run actual-process verification**

This task adds test evidence only; production RED belongs to Tasks 1 and 2. Run:

```bash
go test -count=1 -run '^TestJudgeDemoInteractiveProcess$' ./internal/cli
go test -count=1 -run '^TestJudgeDemo.*Signal' ./internal/cli
```

Expected: the new process assertions expose any incorrect Task 1/2 behavior. No production change is allowed in this task; if either task is behaviorally wrong, return it to its owner instead of adding another fix here.

- [ ] **Step 3: Complete real SIGTERM and initial-root assertions**

Extend `requireJudgeDemoCleanInterrupt` with a shared clean-signal assertion and add a POSIX `first terminate` path using `p.terminate(t)`. In `approved replay performs two fresh native reruns`, require the final summary to contain `p.root` (the initial REPLAY root), not either new RERUN root.

Add a Unix active-native SIGTERM counterpart to the existing active-native SIGINT case using the same `native-config.json` barrier. Require non-successful POST, cleaned native config, unchanged pre-run tree, exit 0, and initial-root summary.

- [ ] **Step 4: Run GREEN, focused race, and vet**

```bash
go test -count=1 -run '^(TestJudgeDemoInteractiveProcess|TestJudgeDemo.*Signal)$' ./internal/cli
go test -race -count=1 -run '^TestJudgeDemo.*Signal$' ./internal/cli
go vet ./internal/cli
```

Expected: all pass on the host; no Windows graceful-SIGTERM claim is made from `TerminateProcess`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/judge_demo_process_test.go internal/cli/judge_demo_server_process_test.go internal/cli/judge_demo_lifecycle_unix_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test: prove judge-demo lifecycle processes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Independent Frozen-TypeScript Browser and Signal Comparisons

**Files:**
- Modify: `difftest/judge_live_test.go:130-158`
- Test: new `difftest/judge_demo_live_test.go`

**Interfaces:**
- Consumes: existing `judgeSides`, `judgeEnv`, `loopbackPort`, fake openers, and `oracle.Skip`.
- Produces: a generalized `startJudgeCommand` wrapper with unchanged `startJudge` behavior and new independent `judge-demo` comparisons.

- [ ] **Step 1: Add RED live comparison cases**

Refactor `startJudge` into a wrapper around this exact generalized helper, preserving its current argv/banner behavior:

```go
func startJudgeCommand(t *testing.T, repo string, side judgeSide, env []string, command, banner string, args ...string) (cmd *exec.Cmd, url string, out *strings.Builder, rest *bufio.Reader, stderr *lockedBuffer) {
    cmd = exec.Command(side.argv[0], append(append(side.argv[1:], command), args...)...)
    cmd.Dir = judgeWorkspace(t, repo)
    cmd.Env = env
    stdout, err := cmd.StdoutPipe()
    if err != nil { t.Fatal(err) }
    stderr = &lockedBuffer{}
    cmd.Stderr = stderr
    if err := cmd.Start(); err != nil { t.Fatal(err) }
    out = &strings.Builder{}
    rest = bufio.NewReader(stdout)
    for url == "" {
        line, err := rest.ReadString('\n')
        out.WriteString(line)
        if err != nil { t.Fatalf("%s: no page URL\n%s%s", side.name, out.String(), stderr.String()) }
        if u, ok := strings.CutPrefix(line, banner); ok { url = strings.TrimSuffix(u, "\n") }
    }
    return cmd, url, out, rest, stderr
}

func startJudge(t *testing.T, repo string, side judgeSide, env []string, args ...string) (cmd *exec.Cmd, url string, out *strings.Builder, rest *bufio.Reader, stderr *lockedBuffer) {
    return startJudgeCommand(t, repo, side, env, "judge-proof", "FaultLine COMMIT_PROOF page: ", args...)
}
```

Add `startJudgeDemo` calling `startJudgeCommand(..., "judge-demo", "🚀 Launching FaultLine Judge Demo at ", ...)`, trimming `...\n` from the returned URL.

Add `TestLiveJudgeDemoSignalStop` for SIGINT/SIGTERM with `FAULTLINE_NO_BROWSER=1`, and `TestLiveJudgeDemoBrowserOpen` for fake opener exits 0 and 1. For each side, compare exact stdout/stderr/opener log/process result after replacing only the actual port with `<PORT>`. Require the initial-root stop summary, exit 0, no `ELIFECYCLE`, and exact browser-failure presence/absence.

- [ ] **Step 2: Run RED with the frozen oracle**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLiveJudgeDemo(SignalStop|BrowserOpen)$' ./difftest
```

Expected: compile failure until `startJudgeDemo` exists, then behavioral differences until Task 1–3 are correct. Never capture expected output from the Go side.

- [ ] **Step 3: Run GREEN and existing judge-proof live regressions**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLiveJudgeDemo(SignalStop|BrowserOpen)$' ./difftest
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^(TestLiveJudgeSignalStop|TestLiveBrowserOpen)$' ./difftest
```

Expected: exact TS/Go matches on Unix; existing judge-proof comparisons remain unchanged.

- [ ] **Step 4: Commit**

```bash
git add difftest/judge_live_test.go difftest/judge_demo_live_test.go
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test: compare judge-demo lifecycle with frozen TS" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Compatibility Status and Final Qualification

**Files:**
- Modify: `difftest/KNOWN_DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`
- Modify: execution ledger only; do not bulk-check historical plans.

- [ ] **Step 1: Record exact lifecycle boundaries and local-only status**

Document browser suppression/open failure, first-signal close/drain/summary, repeated-signal behavior, initial-root summary, and Windows `TerminateProcess` limitation. Full `judge-demo` lifecycle is distinct from plain `serve` and the remainder of Slice 5. Until hosted acceptance completes, status remains local qualification only; retain every local failure/skip/incomplete run.

- [ ] **Step 2: Run affected ordinary/tagged/race/vet gates**

```bash
go test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -tags faultline_test -json -count=1 -timeout=195s ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl
go test -race -json -count=1 -timeout=195s -run '^(TestJudgeDemo.*|TestServeBundle.*|TestJudge.*)$' ./internal/cli ./internal/proofview
go vet -tags faultline_test ./internal/cli ./internal/proofview ./internal/demofixture ./internal/nativefixture ./internal/bundle/demo ./internal/clock ./cmd/fl ./difftest
```

Retain full JSON events and every failure/skip. The known aggregate proofview timeout and full CLI race limitation must not be claimed fixed.

- [ ] **Step 3: Run focused coverage, live oracle, and platform compilation**

```bash
go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestLiveJudgeDemo.*|TestLiveJudgeSignalStop|TestLiveBrowserOpen)$' ./difftest
```

Cross-compile `internal/cli`, `internal/proofview`, and `internal/demofixture` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, and windows/arm64 into fresh distinct paths. These are compilation only; actual execution is hosted.

- [ ] **Step 4: Final independent review and handoff**

Dispatch a fresh whole-branch reviewer for spec compliance, security, correctness, test independence, and deferred-minor triage. Resolve Critical/Important findings through the task owner and re-review. Verify protected-path immutability, clean staged diff, TruffleHog, commit identity/attribution, and retained failures. Commit `docs: record judge-demo lifecycle status` with the required identity/footer.

Stop at local delivery. Draft publication, ready/merge, and every later Slice 5 subproject require separate authorization and plan gates.
