# Go Witness Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port frozen `witness review` JSON, browser, TTY, and non-TTY behavior to Go while preserving exact review integrity, records, output, and lifecycle.

**Architecture:** Add a review API over the existing witness store, independent presentation functions, and an opt-in proofview transport configuration for form bodies and review paths. Add a small shared terminal detector and CLI mode selection. Serialize review request handlers after body receipt; do not change existing server defaults.

**Tech Stack:** Go 1.27 standard library, existing `jsjson`/`jsstr`/`nodefs`/`nodeproc`/witness stores, pinned Node 22.22.2 only as an oracle.

**Spec:** `docs/superpowers/specs/2026-11-12-go-witness-review-design.md`

## Global Constraints

- Work on `worktree-witness-review-spec-20261112`; no protected TypeScript, workflow, package manifest, lockfile, dependency, release, oracle-retirement, or evidence-deletion change.
- Use Go 1.27 and the standard library only; production executes no Node and performs no provider operation.
- Preserve existing witness commands, stores, servers, writer goldens, and `cmd/fl` output behavior.
- No command execution, overlay materialization, automatic browser opening, new route/JSON mode, rollback, or strengthened GET verifier.
- No platform skip, waiver, blanket output normalization, production entropy override, or new dependency. Normalize only actual ports and independently extracted review tokens in live comparisons.
- Every task uses meaningful RED before production, then GREEN and focused race/vet. Every shell command stays below five minutes.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`; every commit ends with `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- Each task receives independent spec/quality review on the controller's current model. Final whole-branch review and hosted six-target acceptance are mandatory. Pending/skipped/failed/incomplete checks are not passes.
- The user authorized implementation without further approval checkpoints. Publication and merge remain unauthorized.

---

### Task 1: Review Snapshot, Integrity, and Actions

**Files:**
- Create: `internal/witness/review.go`
- Test: `internal/witness/review_test.go`
- Test: `difftest/function_live_test.go` only if needed for a focused frozen-library comparison

**Interfaces:**
- Consumes: `ReadProposal`, `Approve`, `Freeze`, `FreezeOptions`, `gitproof` digest/collation/schema helpers, `canonical.DigestJSON`.
- Produces:
  - `const WitnessReviewSchemaVersion = "faultline.witness-review.v1"`
  - `type WitnessReviewError struct { Code, Message string }` with `Error() string`
  - `type WitnessReviewVerification struct { Valid bool; Errors []string }`
  - `OpenWitnessReview(storeDirectory, proposalID string) (jsjson.Value, error)`
  - `VerifyWitnessReview(input jsjson.Value) WitnessReviewVerification`
  - `ApproveWitnessReview(storeDirectory string, review, input jsjson.Value) (jsjson.Value, error)`
  - `FreezeWitnessReview(storeDirectory string, review, input jsjson.Value) (jsjson.Value, error)`
  - `ApproveAndFreezeWitnessReview(storeDirectory string, review, input jsjson.Value) (approval, frozen jsjson.Value, err error)`

- [ ] **Step 1: Write independent snapshot and failure tests**

Create a valid proposal with `witness.Propose`, then require this exact schema/safeguard sequence and a digest over the unsigned object:

```go
review, err := OpenWitnessReview(store, proposal.Get("proposalId").Str())
if err != nil { t.Fatal(err) }
for _, path := range [][]string{{"schemaVersion"}, {"mode"}, {"safeguards", "packet"}, {"safeguards", "command"}, {"safeguards", "overlays"}, {"safeguards", "approval"}, {"safeguards", "freeze"}} {
    if review.Get(path...).Kind() != jsjson.String { t.Fatalf("missing %v", path) }
}
unsigned := jsjson.NewObj()
for _, key := range review.Obj().Keys() {
    if key != "reviewDigest" { unsigned.Set(key, review.Obj().Field(key)) }
}
want := jsexc.Must(canonical.DigestJSON(unsigned))
if review.Get("reviewDigest").Str() != want { t.Fatalf("digest=%q want %q", review.Get("reviewDigest").Str(), want) }
```

Add table cases whose names and required error codes/text are explicit:

| Case | Required result |
| --- | --- |
| missing proposal | `UNBLINDED_OR_INVALID_PROPOSAL`; generic refusal only |
| malformed stored JSON | same generic refusal |
| packet digest mutation | `PROPOSAL_INTEGRITY_FAILURE`; `incident packet digest does not match the proposal packet` |
| command digest mutation | `command digest does not match the exact UTF-8 command bytes` |
| case-insensitive duplicate overlay | `duplicate overlay path: <path>` |
| wrong overlay bytes digest | `overlay byte digest does not match: <path>` |
| noncanonical overlay order | `overlay records are not in canonical path order` |
| wrong ordered overlay digest | `overlay digest does not match the ordered overlay byte digests` |
| wrong proposal digest | `proposal digest does not match the immutable proposal payload` |
| malformed exported review | `INVALID_REVIEW` with no store I/O |
| wrong review digest | `REVIEW_CONFIRMATION_MISMATCH` |
| changed current proposal | `STALE_REVIEW` |
| empty reviewer / >512 chars / >8000-char note | input refusal; no new record |

For the atomic partial-failure test, pre-create a frozen record while leaving approval absent, then invoke `ApproveAndFreezeWitnessReview`; approval succeeds, freeze fails write-once, the new approval remains, and the pre-existing frozen bytes remain unchanged. Separately create an approval first and invoke the atomic action again; the second approval fails write-once and the original approval bytes remain unchanged.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestOpenWitnessReview|TestVerifyWitnessReview|TestWitnessReviewActions)$' ./internal/witness
```

Expected: compile failure for the new review API.

- [ ] **Step 3: Implement the review API**

Build the unsigned object in this exact order, then sign with `reviewDigest`:

```go
unsigned := jsjson.NewObj()
unsigned.Set("schemaVersion", jsjson.MakeString(WitnessReviewSchemaVersion))
unsigned.Set("mode", jsjson.MakeString("LOCAL_READ_ONLY"))
unsigned.Set("proposal", proposal)
safeguards := jsjson.NewObj()
safeguards.Set("packet", jsjson.MakeString("STRICTLY_BLINDED_PROTOCOL_SHAPE"))
safeguards.Set("command", jsjson.MakeString("NOT_EXECUTED"))
safeguards.Set("overlays", jsjson.MakeString("NOT_MATERIALIZED"))
safeguards.Set("approval", jsjson.MakeString("EXPLICIT_HUMAN_ACTION_REQUIRED"))
safeguards.Set("freeze", jsjson.MakeString("EXPLICIT_HUMAN_ACTION_REQUIRED"))
unsigned.Set("safeguards", jsjson.MakeObject(safeguards))
```

Use a private `readReviewableProposal` that collapses only read/schema/store errors, and a private `proposalIntegrityErrors` that returns every integrity error in frozen order. Review actions parse/verify the exported snapshot, compare confirmation, reread and canonically compare the current proposal, then call the existing store methods. Atomic action calls approval first and freeze second without rollback.

- [ ] **Step 4: Run GREEN, race, and vet**

```bash
go test -count=1 -run '^(TestOpenWitnessReview|TestVerifyWitnessReview|TestWitnessReviewActions)$' ./internal/witness
go test -race -count=1 -run '^(TestOpenWitnessReview|TestVerifyWitnessReview|TestWitnessReviewActions)$' ./internal/witness
go vet ./internal/witness
go test -count=1 ./internal/witness
```

Expected: all pass; existing witness suites remain green.

- [ ] **Step 5: Review and commit**

Commit `feat: add local witness review API` with only Task 1 files after independent review.

---

### Task 2: Independent Humanized Text and HTML Presentation

**Files:**
- Create: `internal/proofview/witness_review.go`
- Test: `internal/proofview/witness_review_test.go`
- Create: `difftest/testdata/witness-review/atomic.html`, `two-step.html`, `tty.txt`

**Interfaces:**
- Consumes: `OpenWitnessReview` values, `EscapeHTML`, `nodeproc.ExecFileSync`, existing UTF-8/base64/collation/digest helpers.
- Produces:
  - `RenderWitnessReviewText(review jsjson.Value, draft *jsjson.Value) string`
  - `RenderWitnessReviewHTML(review jsjson.Value, draft *jsjson.Value, credential string, twoStep bool) string`
  - `ReadGitUserName() string`

- [ ] **Step 1: Hand-write independent expectations**

Use a review containing HTML metacharacters, a CI-log sentinel, exact command text, a UTF-8 overlay with 41 lines, and a binary overlay containing NUL. Hand-write complete expected HTML and TTY files before implementation. Require:

```go
text := RenderWitnessReviewText(review, draft)
if strings.Contains(text, "raw-ci-log-secret") { t.Fatal("unexpected raw CI log") }
if !strings.Contains(text, "line 40") || strings.Contains(text, "line 41") { t.Fatal("TTY overlay truncation differs") }
html := RenderWitnessReviewHTML(review, draft, "test_nonce-ABC123", false)
if strings.Contains(html, "raw-ci-log-secret") || strings.Contains(html, "<script>") { t.Fatal("unsafe HTML") }
if !strings.Contains(html, `action="/approve-freeze"`) { t.Fatal("wrong atomic form") }
```

The HTML files include exact CSP-relevant forms and the independently specified token `test_nonce-ABC123`. Add range-display fixtures for zero, one, and 41 commits, metadata failure fallback, CRLF bytes, and Git `%aI` timestamps with fractional seconds and numeric offsets.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestWitnessReviewText|TestWitnessReviewHTML|TestWitnessReviewRange|TestReadGitUserName)$' ./internal/proofview
```

Expected: undefined renderer functions.

- [ ] **Step 3: Implement presentation**

Keep humanization private and make the two exported renderers consume the same projected view. UTF-8 text is accepted only when decode/re-encode round-trips and contains no NUL. TTY emits the first 40 split lines; HTML emits the full text. Escape every dynamic value. HTML omits raw `ciLog`, uses the frozen shared chrome, includes no scripts, and selects exactly one of:

```html
<form method="post" action="/approve-freeze">
<form method="post" action="/approve">
<form method="post" action="/freeze">
```

`ReadGitUserName` runs `git config --get user.name` in the current directory and returns `local-reviewer` on failure. Range metadata uses at most three Git calls, each with `Timeout: 5*time.Second` and `MaxBuffer: 2<<20`, and at most 40 displayed commits.

- [ ] **Step 4: Run GREEN, race, and vet**

```bash
go test -count=1 -run '^(TestWitnessReviewText|TestWitnessReviewHTML|TestWitnessReviewRange|TestReadGitUserName)$' ./internal/proofview
go test -race -count=1 -run '^(TestWitnessReviewText|TestWitnessReviewHTML|TestWitnessReviewRange|TestReadGitUserName)$' ./internal/proofview
go vet ./internal/proofview
```

Expected: exact independent files pass with no regenerated expectations.

- [ ] **Step 5: Review and commit**

Commit `feat: render witness review surfaces` with only Task 2 files after independent review.

---

### Task 3: Opt-In Form Transport and Browser Server

**Files:**
- Modify: `internal/proofview/server.go`, `internal/proofview/framing.go`
- Create: `internal/proofview/witness_review_server.go`
- Test: `internal/proofview/server_test.go`, `internal/proofview/framing_test.go`, `internal/proofview/witness_review_server_test.go`

**Interfaces:**
- Consumes: Tasks 1–2, `startNodeServer`, `nodeHeaders`, `writeNodeBody`, existing store reads.
- Produces:
  - `type WitnessReviewServerOptions struct { StoreDirectory, ProposalID, DraftStore string; TwoStep bool }`
  - `StartWitnessReviewServer(options WitnessReviewServerOptions, port float64) (*WitnessReviewServer, error)`
  - `URL() string`, `Close() error`
  - unexported `startWitnessReviewServer(options WitnessReviewServerOptions, credential string, port float64)` for fixed-token tests

- [ ] **Step 1: Add transport and HTTP RED tests**

First prove every existing server test passes unchanged. Add raw requests for Content-Length and chunked forms, exactly 32768 bytes, 32769 bytes, duplicate keys, zero body, absolute target, repeated slashes, and a backslash/dot-segment path. Existing transports retain their old body marker and pathname behavior; review receives actual bytes and only its own path normalization.

Add exact route/security cases:

| Request | Required result |
| --- | --- |
| GET `/` | current 200 page with all four security headers |
| bad/missing token | 403 |
| wrong acknowledgement | 409 |
| atomic `/approve-freeze` | approval then frozen record |
| atomic direct `/approve`, `/freeze` | accepted legacy actions |
| two-step `/approve-freeze` | 404 `Not found\n` |
| two-step `/approve`, `/freeze` | sequential records |
| unknown route | 404 `Not found\n` |
| missing linked draft | unlinked page |
| malformed or mismatched linked draft | 409 refusal; no record |
| stale review between open and post | `STALE_REVIEW`; no new record |

Start two concurrent atomic posts with the same token and acknowledgement. Require exactly one successful mutation and valid write-once records; never allow duplicate approval files or partial response corruption.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestNodeBodyMarker|TestWitnessReviewServerHTTP|TestWitnessReviewServerDraftBinding|TestWitnessReviewServerConcurrentMutation)$' ./internal/proofview
```

Expected: undefined review server or missing form-body behavior; old server tests stay green.

- [ ] **Step 3: Implement opt-in transport and server**

Add a private server-options type and a new helper while retaining `startNodeServer` as the default wrapper:

```go
type nodeServerOptions struct {
    captureFormBody bool
    reviewPaths     bool
}
func startNodeServerWithOptions(port float64, options nodeServerOptions, routes func(string) http.HandlerFunc) (*Server, error)
```

The framing layer stores captured bytes in request context only when `captureFormBody` is true. The review handler obtains them with a private `nodeFormBody(r *http.Request) []byte`. Generate production credentials with `crypto/rand` and `hex.EncodeToString`; only the unexported test constructor accepts a fixed credential.

Parse form fields with last-key-wins and the exact 32 KiB boundary. Serialize the post-body handler with a per-server mutex. Every action verifies token, acknowledgement, review digest, current proposal, and draft binding before store writes. Use the frozen status/header/body ordering and one action's approval-then-freeze semantics.

- [ ] **Step 4: Run GREEN and regressions**

```bash
go test -count=1 ./internal/proofview
go test -race -count=1 -run '^(TestNodeBodyMarker|TestWitnessReviewServerHTTP|TestWitnessReviewServerDraftBinding|TestWitnessReviewServerConcurrentMutation)$' ./internal/proofview
go vet ./internal/proofview
```

Expected: review and all old proofview tests pass, including concurrent mutation and transport close.

- [ ] **Step 5: Review and commit**

Commit `feat: serve witness review approval` with only Task 3 files after independent review.

---

### Task 4: Shared Terminal Detection and TTY Approval

**Files:**
- Create: `internal/term/term_unix.go`, `internal/term/term_windows.go`, `internal/term/term_test.go`
- Modify: only the terminal-check call sites in `cmd/fl/stdout_unix.go`, `cmd/fl/stdout_windows.go`
- Create: `internal/cli/witness_review_tty.go`
- Test: `internal/cli/witness_review_tty_test.go`, `internal/cli/witness_review_tty_process_test.go`

**Interfaces:**
- Consumes: Tasks 1–2 and existing `internal/term.IsTerminal`.
- Produces:
  - `func IsTerminal(file *os.File) bool` in package `term`
  - `type WitnessReviewTTYOptions struct { StoreDirectory, ProposalID string; Draft *jsjson.Value; ApprovedBy, Note, ApprovedAt, FrozenAt *string; Stdin io.Reader; Stdout io.Writer; StdinIsTTY, StdoutIsTTY bool }`
  - `RunWitnessReviewTTY(options WitnessReviewTTYOptions) (approval, frozen jsjson.Value, approvedBy string, err error)`
  - `NonTtyReviewRefusalMessage(reviewURL string) string`

- [ ] **Step 1: Write terminal and TTY RED tests**

Test both non-TTY messages verbatim, then table-drive answers `y`, ` YES `, `n`, empty input, and EOF. Only the first two write records. Require exact prompt/success text and reviewer fallback:

```go
approvedBy := "Test Reviewer"
approval, frozen, actor, err := RunWitnessReviewTTY(WitnessReviewTTYOptions{
    StoreDirectory: store, ProposalID: id, ApprovedBy: &approvedBy,
    Stdin: strings.NewReader(" YES \n"), Stdout: &stdout,
    StdinIsTTY: true, StdoutIsTTY: true,
})
if err != nil || actor != approvedBy { t.Fatalf("actor=%q err=%v", actor, err) }
if frozen.Get("proposal", "proposalId").Str() != id || approval.Get("approvedBy").Str() != actor { t.Fatal("wrong records") }
```

Add actual Unix PTY cases for approve/decline and piped-`yes` refusal. Add Windows console detection coverage through the existing console harness; do not use `TerminateProcess` as graceful SIGTERM evidence.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/term
go test -count=1 -run '^(TestWitnessReviewTTY|TestNonTtyReviewRefusalMessage)$' ./internal/cli
```

Expected: missing terminal package and TTY function.

- [ ] **Step 3: Implement detection and TTY flow**

Move only the `isatty` ioctl and `GetConsoleMode` checks into `internal/term`; `cmd/fl` calls `term.IsTerminal(file)` at the same decision points and retains every other behavior.

The TTY function checks stdin then stdout, opens the review, calls `RenderWitnessReviewText`, resolves reviewer identity, writes `Approve & freeze as "<identity>"? [y/N] `, reads one line, and accepts only trimmed case-insensitive `y`/`yes`. Decline returns `Approval declined; no records were written.` without calling the store action. Success calls `ApproveAndFreezeWitnessReview` and writes the exact frozen summary.

- [ ] **Step 4: Run GREEN and process checks**

```bash
go test -count=1 ./internal/term ./cmd/fl
go test -count=1 -run '^(TestWitnessReviewTTY|TestNonTtyReviewRefusalMessage)$' ./internal/cli
go test -race -count=1 -run '^(TestWitnessReviewTTY|TestNonTtyReviewRefusalMessage)$' ./internal/cli
go vet ./internal/term ./internal/cli ./cmd/fl
```

Expected: all pass; piped input never approves.

- [ ] **Step 5: Review and commit**

Commit `feat: add interactive witness review` with only Task 4 files after independent review.

---

### Task 5: CLI Mode Selection, JSON Goldens, and Server Processes

**Files:**
- Modify: `internal/cli/witness.go:81-85`
- Create: `internal/cli/witness_review.go`
- Test: `internal/cli/witness_review_test.go`, `internal/cli/witness_review_process_test.go`
- Modify: `difftest/writer_test.go`, `difftest/gen/gen-writers.ts`, `difftest/testdata/writers.json`
- Create: `difftest/testdata/witness-review/cli-diagnostics.jsonl`

**Interfaces:**
- Consumes: Tasks 1–4, `awaitBundleInterrupt`, existing option/store helpers.
- Produces: `witnessReviewCommand(e *env, args []string, proposalID, storeDirectory string) error` and `witness review` dispatch.

- [ ] **Step 1: Add exact CLI RED cases**

Test the complete selection matrix with fake terminal/server seams:

```go
for _, tc := range []struct {
    name string
    args []string
    stdinTTY, stdoutTTY bool
    wantMode string
    wantExit int
}{
    {"json ignores bad port", []string{"--json", "--port", "bad"}, false, false, "json", 0},
    {"tty wins browser", []string{"--tty", "--browser"}, true, true, "tty", 0},
    {"port forces browser", []string{"--port", "0"}, true, true, "server", 0},
    {"interactive default", nil, true, true, "tty", 0},
    {"stdin pipe falls back", nil, false, true, "server", 0},
    {"stdout pipe falls back", nil, true, false, "server", 0},
    {"forced tty fallback exit", []string{"--tty"}, false, true, "server-fallback", 1},
} {
    mode, stdout, stderr, code := runWitnessReviewWithFakes(t, tc.args, tc.stdinTTY, tc.stdoutTTY)
    if mode != tc.wantMode || code != tc.wantExit {
        t.Fatalf("mode=%q want %q; exit=%d want %d; stdout=%q stderr=%q", mode, tc.wantMode, code, tc.wantExit, stdout, stderr)
    }
}
```

Define `runWitnessReviewWithFakes(t *testing.T, args []string, stdinTTY, stdoutTTY bool) (mode, stdout, stderr string, exitCode int)` in the same test file. It invokes `witnessReviewCommand`, records whether the JSON, TTY, server, or forced-fallback seam ran, and restores every package-private seam during `t.Cleanup`.

Require the JSON top-level key order `status`, `review`, `sensitivity`, two-space indentation, final newline, and no server start. Add propose-then-review writer goldens for valid JSON, missing ID, generic invalid proposal, and integrity failure. The committed diagnostics include the exact port refusal and replay through `runFl`.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestWitnessReviewCommand|TestWitnessReviewProcess)$' ./internal/cli
go test -tags faultline_test -count=1 -run '^TestWriterReplay$' ./difftest
```

Expected: `not yet ported: witness review` and missing golden output.

- [ ] **Step 3: Implement CLI selection and lifecycle**

Keep `--json` before port/draft processing. Resolve defaults and flags exactly as frozen. Start the server through `awaitBundleInterrupt` and return its result. For forced-TTY fallback, print `NonTtyReviewRefusalMessage("")` to stderr, print generic readiness to stdout, then set `e.exitCode = 1` after the server closes. Do not add a stop summary or browser launch.

Use package-private terminal and server seams so tests select behavior without real desktop operations. Production passes actual `os.Stdin`/stdout detection through `internal/term` and the real server constructor.

- [ ] **Step 4: Run process and golden GREEN checks**

Build `fl`, run outside the repository with Node absent, and verify exact JSON, noninteractive server startup/SIGINT, forced-fallback exit 1, and real record bytes. Run:

```bash
go test -count=1 -run '^(TestWitnessReviewCommand|TestWitnessReviewProcess)$' ./internal/cli
go test -race -count=1 -run '^TestWitnessReviewCommand$' ./internal/cli
go vet ./internal/cli
go test -tags faultline_test -count=1 -run '^TestGoldensReachEveryErrorLiteral$' ./difftest
```

Expected: all pass; new diagnostics execute through covered `fl`, not only golden-text reads.

- [ ] **Step 5: Review and commit**

Commit `feat: port witness review command` with only Task 5 files after independent review.

---

### Task 6: Live Oracle, Compatibility Records, and Final Qualification

**Files:**
- Create: `difftest/witness_review_live_test.go`, `difftest/witness_review_replay_test.go`
- Modify: `difftest/KNOWN_DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`
- Modify: execution ledger only

- [ ] **Step 1: Add frozen-TypeScript live comparisons**

Add Unix HTTP comparisons using existing judge process helpers. Compare exact status, headers, bodies, CLI output, and exit status; normalize only `127.0.0.1:<actual-port>` and each side's independently extracted token. Include:

```text
atomic GET, two-step GET, bad token, wrong acknowledgement,
legacy direct actions, missing/mismatched drafts, duplicate form keys,
32768/32769-byte forms, chunked forms, query/repeated-slash/absolute targets,
concurrent atomic posts, stale review, SIGINT close and forced-fallback exit.
```

Verify approval/frozen records independently after every mutation. Add real PTY y/yes/decline/EOF, piped-`yes` refusal, and first/second SIGINT tests. Add equivalent Windows console coverage through the existing harness rather than a skip.

- [ ] **Step 2: Run live RED/GREEN and focused regressions**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLiveWitnessReview' ./difftest
go test -count=1 -run '^(TestWitnessReview|TestOpenWitnessReview|TestVerifyWitnessReview)' ./internal/witness ./internal/proofview ./internal/cli
```

Expected: live differences fail until corrected; old witness/server behavior remains green.

- [ ] **Step 3: Run final gates**

```bash
go test -json -count=1 -timeout=195s ./internal/witness ./internal/proofview ./internal/cli ./internal/term ./internal/incident ./cmd/fl
go test -tags faultline_test -json -count=1 -timeout=195s ./internal/witness ./internal/proofview ./internal/cli ./internal/term ./internal/incident ./cmd/fl
go test -race -json -count=1 -timeout=195s -run '^(TestWitnessReview.*|TestOpenWitnessReview.*|TestVerifyWitnessReview.*)$' ./internal/witness ./internal/proofview ./internal/cli
go vet -tags faultline_test ./internal/witness ./internal/proofview ./internal/cli ./internal/term ./internal/incident ./cmd/fl ./difftest
go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest
```

Retain full JSON events and every failure/skip. Run `go test -count=1 -timeout=195s ./...` as the final ordinary suite. Cross-compile affected packages and tests into distinct fresh paths for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, and windows/arm64. Compilation is not execution.

- [ ] **Step 4: Record exact status and final review**

Document actual mode boundaries, security policy, draft behavior, record semantics, and local/hosted status without deleting historical failures. Verify protected-path immutability, clean staged diff, TruffleHog, commit identity/attribution, and retained failures.

Dispatch a fresh whole-branch reviewer on the controller's current model for spec compliance, security, correctness, test independence, and deferred-minor triage. Resolve Critical/Important findings and re-review. Commit `docs: record witness review status` with the required identity/footer.

Stop at local delivery. Publication, ready/merge, 5f live proposals, and every later subproject require separate authorization.
