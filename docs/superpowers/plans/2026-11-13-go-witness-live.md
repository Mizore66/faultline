# Go Witness Live Proposals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port frozen `witness propose --live` and model-selected overlay input with exact provider, redaction, store, output, and error behavior, plus the approved GPT-6.1 default/source deviation.

**Architecture:** Extract repair’s fetch-compatible transport into neutral `internal/openai`, add a separate live model boundary and overlay reader in `internal/witness`, then wire the existing CLI proposal path. Use scripted loopback servers only and preserve all existing repair, store, and writer behavior.

**Tech Stack:** Go 1.27 standard library, existing `jsjson`/`jsstr`/`nodefs`/`nodeproc`/redaction/witness stores, pinned Node 22.22.2 as oracle only.

**Spec:** `docs/superpowers/specs/2026-11-13-go-witness-live-design.md`

## Global Constraints

- Work on `worktree-witness-live-spec-20261113`; no protected TypeScript, dependency, release, oracle-retirement, or evidence-deletion change. Workflow edits require separate explicit approval.
- Use Go 1.27 and the standard library only. Production executes no Node and performs no real provider operation.
- Default provider model is `gpt-6.1`; output `source` is `GPT-6.1`, including custom models. An explicitly empty model remains empty. Frozen API-key error text remains GPT-5.6.
- No application retries, new timeout options, provider SDK, command execution, overlay materialization, review changes, or guided-investigation changes.
- Preserve repair transport, existing proposal/store behavior, writer goldens, and Node path/error semantics. Do not strengthen overlay root ancestors, dot normalization, input caps, source selection, or TOCTOU behavior.
- No platform skip, waiver, blanket normalization, production endpoint override, or new dependency. Loopback endpoint override exists only in `faultline_test`.
- Every task uses meaningful RED before production, then GREEN and focused race/vet. Every shell command stays below five minutes.
- Commit only with `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`; every commit ends with `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- Each task receives independent spec/quality review on the controller's current model. Final whole-branch review and hosted six-target acceptance are mandatory. Pending/skipped/failed/incomplete checks are not passes.
- Publication and merge require separate authorization.

---

### Task 1: Neutral OpenAI Transport Extraction

**Files:**
- Create: `internal/openai/transport.go`, `internal/openai/response.go`, `internal/openai/url_test.go`, `internal/openai/url_release.go`
- Modify: `internal/repair/live.go`, `internal/repair/response.go`
- Test: `internal/openai/transport_test.go`, `internal/openai/response_test.go`
- Test: existing `internal/repair/*_test.go` and `difftest/repair_live_test.go`

**Interfaces:**
- Consumes: existing unexported repair transport and `ExtractResponseText`.
- Produces:
  - `func PostJSON(endpoint, authorization string, body []byte) (status int, text string, err error)` in `internal/openai`.
  - `func ExtractResponseText(response jsjson.Value) (string, error)` in `internal/openai`.
  - `func ResponsesURL() string` with production fixed endpoint and `FAULTLINE_TEST_OPENAI_URL` only in tagged builds.

- [ ] **Step 1: Add neutral-package characterization tests**

Move the existing raw/fast-path/conflicting/missing response tests to `internal/openai/response_test.go` without changing expected text. Add a loopback POST test asserting exact authorization/content-type/body and a non-2xx response whose text is returned without converting it to a transport error:

```go
status, text, err := openai.PostJSON(server.URL, "Bearer test-key", []byte(`{"model":"gpt-6.1"}`))
if err != nil || status != http.StatusUnprocessableEntity || text != `{"error":"scripted"}` {
    t.Fatalf("status=%d text=%q err=%v", status, text, err)
}
```

Add build-tagged endpoint tests for empty, loopback, and arbitrary test URLs. Production builds must always return `https://api.openai.com/v1/responses`.

- [ ] **Step 2: Run RED and repair baseline**

```bash
go test -count=1 ./internal/openai ./internal/repair
```

Expected: `internal/openai` is undefined while the current repair tests pass before extraction.

- [ ] **Step 3: Extract transport without behavior changes**

Move only neutral fetch/header/redirect/compression/body-decoding code and response extraction. Keep repair-specific packet schemas, instructions, brief body generation, and `ProposeBrief` in repair. Change its POST path to:

```go
status, text, err := openai.PostJSON(openai.ResponsesURL(), "Bearer "+apiKey, []byte(body))
if err != nil { return jsjson.Value{}, err }
if status < 200 || status > 299 {
    return jsjson.Value{}, errors.New(jsexc.Concat("OpenAI Responses request failed: ", strconv.Itoa(status), " ", text))
}
parsed, err := jsjson.Parse(text)
if err != nil { return jsjson.Value{}, err }
outputText, err := openai.ExtractResponseText(parsed)
```

`PostJSON` reads the response body using the existing decoder and returns status/body even for non-2xx. Transport/body failures return the existing `fetch failed`/`terminated` errors.

- [ ] **Step 4: Run GREEN and transport regressions**

```bash
go test -count=1 ./internal/openai ./internal/repair
go test -race -count=1 ./internal/openai
go vet ./internal/openai ./internal/repair
go test -tags faultline_test -count=1 -run '^(TestLiveRepair|TestRepair.*)$' ./difftest
```

Expected: all pass with unchanged repair output and errors.

- [ ] **Step 5: Review and commit**

Commit `refactor: share OpenAI response transport` with only Task 1 files after independent review.

---

### Task 2: Blind Live Model Boundary and Exact Requests

**Files:**
- Create: `internal/witness/live.go`
- Test: `internal/witness/live_test.go`
- Test: `difftest/function_live_test.go` only for a concrete frozen-library comparison need

**Interfaces:**
- Consumes: Task 1 transport/response extraction, existing redaction, bundle/schema helpers, canonical digest.
- Produces:
  - `type LiveWitnessOptions struct { APIKey, Model *string }`
  - `ProposeWitnessWithGpt(packetInput jsjson.Value, options LiveWitnessOptions) (jsjson.Value, error)`
  - Result object keys `proposal`, `incidentPacketDigest`, `redaction`.

- [ ] **Step 1: Add request/schema/response RED tests**

Use a loopback recorder returning a valid six-field response. Assert exact request JSON, including `model: "gpt-6.1"`, instructions, nested string input, format name/strictness, and the complete provider schema. Verify the secret input is absent from the request and the result contains the redacted packet digest and first redaction report.

Add these exact cases:

| Case | Required result |
| --- | --- |
| nil model | `gpt-6.1` request |
| pointer to empty model | empty model request |
| nil API key with env unset | frozen GPT-5.6 key refusal; zero requests |
| pointer to empty API key with env set | same refusal; zero requests |
| extra blind-packet key | schema refusal; zero requests |
| non-2xx | `OpenAI Responses request failed: <status> <body>` |
| raw output text | parsed six-field proposal |
| fast path | same proposal |
| matching raw and fast path | same proposal |
| missing/empty/multiple/conflicting text | frozen extraction error; no proposal |
| malformed outer/model JSON | parse/schema error; no proposal |
| >5 overlays, >8 notes, non-integer codes/timeouts | provider schema refusal |
| storage-only oversized packet | allowed through model boundary |

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestProposeWitnessWithGpt|TestLiveWitnessRequest|TestLiveWitnessResponse)$' ./internal/witness
```

Expected: undefined live function or request behavior.

- [ ] **Step 3: Implement the separate live boundary**

Define options and defaulting explicitly:

```go
type LiveWitnessOptions struct { APIKey, Model *string }

model := "gpt-6.1"
if options.Model != nil { model = *options.Model }
apiKey, present := nodeproc.Getenv("OPENAI_API_KEY")
if options.APIKey != nil { apiKey, present = *options.APIKey, true }
if !present || apiKey == "" {
    return jsjson.Value{}, errors.New("OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one.")
}
```

Use a dedicated four-field blind schema before redaction and again after redaction. Construct request fields in `model`, `instructions`, `input`, `text` order. Call `openai.PostJSON`, then parse/extract/validate exactly one structured proposal. Return an object in `proposal`, `incidentPacketDigest`, `redaction` order.

- [ ] **Step 4: Run GREEN, race, and vet**

```bash
go test -count=1 -run '^(TestProposeWitnessWithGpt|TestLiveWitnessRequest|TestLiveWitnessResponse)$' ./internal/witness
go test -race -count=1 -run '^(TestProposeWitnessWithGpt|TestLiveWitnessRequest|TestLiveWitnessResponse)$' ./internal/witness
go vet ./internal/witness
go test -count=1 ./internal/witness ./internal/openai ./internal/repair
```

Expected: all pass; existing witness and repair suites stay green.

- [ ] **Step 5: Review and commit**

Commit `feat: add live witness model boundary` with only Task 2 files after independent review.

---

### Task 3: Model Overlay Input and Path Safety

**Files:**
- Create: `internal/witness/overlay_input.go`
- Test: `internal/witness/overlay_input_test.go`
- Test: `internal/witness/overlay_input_windows_test.go` for native junction/reparse-point cases

**Interfaces:**
- Consumes: `nodefs.Resolve`, `Join`, `Relative`, `Lstat`, and byte reads.
- Produces:
  - `type ModelOverlay struct { Path, BytesBase64 string }`
  - `ReadModelOverlayInput(root string, paths []string) ([]ModelOverlay, error)`

- [ ] **Step 1: Add overlay RED tests**

Use independent byte expectations for nested binary/CRLF content. Table-drive exact accepted/rejected behavior:

| Case | Required result |
| --- | --- |
| `a/./b` | normalized `a/b` and exact bytes |
| `a/../b` | normalized `b` and exact bytes |
| empty/backslash path; absolute path escaping the root | refusal |
| absolute path resolving inside the root | accepted, normalized to the relative path |
| missing/file/symlink root | refusal |
| parent regular file or symlink | refusal |
| leaf missing/directory/symlink | refusal |
| duplicate selection | reread twice before store duplicate handling |
| case-alias selection | both reads returned for store duplicate handling |
| >1,400,000 base64 chars or >512 path | read here; storage refuses later |
| Windows junction/reparse parent | same refusal as symlink parent |

Require root ancestors to retain frozen acceptance and sequential read order with a call recorder.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^TestReadModelOverlayInput$' ./internal/witness
```

Expected: undefined overlay reader.

- [ ] **Step 3: Implement normalized overlay reads**

Resolve the root once and require its own non-symlink directory `lstat`. For each supplied path, reject backslashes, resolve beneath root, and validate the non-empty relative result. Walk each parent with `Lstat`; require real directories, then require a regular non-symlink leaf. Read bytes and append:

```go
ModelOverlay{
    Path: filepath.ToSlash(relative),
    BytesBase64: base64.StdEncoding.EncodeToString(raw),
}
```

Do not add a byte cap, root-ancestor check, dot-segment ban, or parallel reads.

- [ ] **Step 4: Run GREEN, race, vet, and Windows compilation**

```bash
go test -count=1 -run '^TestReadModelOverlayInput$' ./internal/witness
go test -race -count=1 -run '^TestReadModelOverlayInput$' ./internal/witness
go vet ./internal/witness
GOOS=windows GOARCH=amd64 go test -c -o /tmp/witness-overlay-amd64.test.exe ./internal/witness
GOOS=windows GOARCH=arm64 go test -c -o /tmp/witness-overlay-arm64.test.exe ./internal/witness
```

Expected: all local cases pass and Windows test binaries compile; native Windows execution remains hosted.

- [ ] **Step 5: Review and commit**

Commit `feat: read model overlay inputs` with only Task 3 files after independent review.

---

### Task 4: CLI Selection, Persistence, and Goldens

**Files:**
- Modify: `internal/cli/witness.go:60-67,249-287`
- Create: `internal/cli/witness_live.go`
- Test: `internal/cli/witness_live_test.go`, `internal/cli/witness_live_process_test.go`
- Modify: `internal/cli/witness_test.go` only to remove the stale unported expectation
- Create: `difftest/witness_live_replay_test.go`, `difftest/testdata/witness-live/diagnostics.jsonl`
- Modify: `difftest/testdata/writers.json`, writer goldens/manifests only from independently recaptured frozen output

**Interfaces:**
- Consumes: Tasks 1–3, existing `witness.Propose`, `readJSONInput`, option/store/clock helpers.
- Produces: `witnessProposeLive(e *env, args []string, storeDirectory string) error` and persisted live dispatch.

- [ ] **Step 1: Add CLI RED cases**

Assert all selection rows and exact operation order with a scripted provider and real temporary stores:

```go
for _, tc := range []struct {
    name string
    args []string
    wantRequests, wantStores int
}{
    {"live incident ignores input", []string{"--live", "--incident", "incident.json", "--input", "offline.json", "--proposal-id", "p1", "--overlay-root", "overlays"}, 1, 1},
    {"live preview has no store", []string{"--live"}, 1, 0},
    {"input stays offline", []string{"--live", "--input", "offline.json"}, 0, 1},
    {"sample without input", []string{"--incident", "incident.json"}, 0, 0},
} {
    stdout, stderr, exitCode, requests, stores := runWitnessLiveWithProvider(t, tc.args, validLiveProposalJSON)
    if requests != tc.wantRequests || stores != tc.wantStores {
        t.Fatalf("requests=%d want %d; stores=%d want %d; exit=%d stdout=%q stderr=%q", requests, tc.wantRequests, stores, tc.wantStores, exitCode, stdout, stderr)
    }
}
```

Add a test helper `runWitnessLiveWithProvider(t *testing.T, args []string, responseFixture string) (stdout, stderr string, exitCode, requests, stores int)` that uses `FAULTLINE_TEST_OPENAI_URL`, the existing CLI harness, and actual proposal files. Define `validLiveProposalJSON` as `{"behavior":"Preserve refund currency.","command":"node witness.mjs","overlayFiles":[],"expectedExitCode":0,"timeoutSeconds":30,"safetyNotes":[]}`. Cover missing incident/proposal ID/root/key, provider-before-overlay failure, second-invocation write-once refusal, and custom/default/empty models.

Require success keys `status`, `source`, `store`, `proposalId`, `proposalDigest`, `incidentPacketDigest`, `privacy` with `source: "GPT-6.1"`, and complete record bytes/modes/digests. Assert `expectedExitCode` and `safetyNotes` are absent. Recapture all new writer goldens with pinned Node 22.22.2 and independently recompute shard totals.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 -run '^(TestWitnessProposeLive|TestWitnessLiveProcess)$' ./internal/cli
go test -tags faultline_test -count=1 -run '^TestWriterReplay$' ./difftest
```

Expected: `not yet ported: witness propose --live` and missing new golden output.

- [ ] **Step 3: Implement CLI operation order**

Read incident first, then require proposal ID and overlay root. Call the Task 2 model boundary, redact the original incident again, call Task 3 with generated paths, construct the existing proposal input with `proposalOrigin: "MODEL"`, then call `witness.Propose`. Print success keys in exact order with `GPT-6.1`.

Keep preview and offline/sample branches separate. Do not preflight overlay root, store expected exit code/safety notes, or change frozen API-key text. Every new diagnostic gets committed text and actual replay through the covered `fl` binary.

- [ ] **Step 4: Run process/golden GREEN checks**

```bash
go test -count=1 -run '^(TestWitnessProposeLive|TestWitnessLiveProcess)$' ./internal/cli
go test -race -count=1 -run '^TestWitnessProposeLive$' ./internal/cli
go vet ./internal/cli
go test -tags faultline_test -count=1 -run '^(TestGoldensReachEveryErrorLiteral|TestWitnessLiveReplay)$' ./difftest
```

Expected: all pass, exact request/store bytes match, and each new diagnostic executes through covered `fl`.

- [ ] **Step 5: Review and commit**

Commit `feat: add live witness proposals` with only Task 4 files after independent review.

---

### Task 5: Frozen Oracle, Compatibility Records, and Final Qualification

**Files:**
- Create: `difftest/witness_live_oracle_test.go`
- Modify: `difftest/KNOWN_DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-25-go-migration-overview.md`
- Modify: `docs/superpowers/specs/2026-10-30-go-slice5a-cli-surfaces-design.md`
- Modify: execution ledger only

- [ ] **Step 1: Add frozen-TypeScript loopback comparisons**

Use the twin fake OpenAI servers to compare exact request and result behavior for raw/fast-path success, non-2xx, malformed responses, schema failures, and transport failures. Compare exact CLI output, store bytes/modes, provider call count, and overlay reads. Include root/path failures, duplicate/case-alias paths, over-limit storage, and second-invocation refusal.

Normalize only actual ephemeral ports and independently generated temp roots. Do not mask secrets, model names, digests, timestamps, or errors. Assert `gpt-6.1` request default and `GPT-6.1` output as the approved deviation.

- [ ] **Step 2: Run live RED/GREEN and focused regressions**

```bash
FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -run '^TestLiveWitnessProposal' ./difftest
go test -count=1 -run '^(TestProposeWitnessWithGpt|TestReadModelOverlayInput|TestWitnessProposeLive)' ./internal/witness ./internal/cli
go test -count=1 ./internal/openai ./internal/repair
```

Expected: differences fail until corrected; old repair/witness behavior stays green.

- [ ] **Step 3: Run final gates**

```bash
go test -json -count=1 -timeout=195s ./internal/witness ./internal/openai ./internal/repair ./internal/cli ./cmd/fl
go test -tags faultline_test -json -count=1 -timeout=195s ./internal/witness ./internal/openai ./internal/repair ./internal/cli ./cmd/fl
go test -race -json -count=1 -timeout=195s -run '^(TestProposeWitnessWithGpt.*|TestReadModelOverlayInput.*|TestWitnessProposeLive.*)$' ./internal/witness ./internal/cli
go vet -tags faultline_test ./internal/witness ./internal/openai ./internal/repair ./internal/cli ./cmd/fl ./difftest
go test -tags faultline_test -json -count=1 -timeout=195s -run '^(TestNativeCoverage.*|TestGoldensReachEveryErrorLiteral|TestJudgeSites.*|TestValidateSiteTable|TestSiteExemptIsKeyedByFileAndLiteral|TestUnreachableSitesTable|TestNarrowedGateIsRefused)$' ./difftest
```

Retain full JSON and every failure/skip. Complete every writer shard using existing deterministic filters or smaller case-ID subsets under the cap; do not claim an unrun aggregate. Run `go test -p 2 -count=1 -timeout=195s ./...` as the final ordinary suite and retain the known default-parallel proofview limitation separately.

Cross-compile affected packages/tests into distinct fresh paths for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, and windows/arm64. Compilation is not execution.

- [ ] **Step 4: Record exact status and final review**

Document selection, provider/redaction, overlay, persistence, GPT-6.1 deviation, and local/hosted status without deleting historical failures. Verify protected-path immutability, clean staged diff, TruffleHog, commit identity/attribution, and retained failures.

Dispatch a fresh whole-branch reviewer on the controller's current model for spec compliance, security, correctness, test independence, and deferred-minor triage. Resolve Critical/Important findings and re-review. Commit `docs: record witness live proposal status` with the required identity/footer.

Stop at local delivery. Publication, ready/merge, 5g guided investigation, and later subprojects require separate authorization.
