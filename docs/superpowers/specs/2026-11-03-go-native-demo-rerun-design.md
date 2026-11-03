# Native demo RERUN — execution and provenance design

## Context

PR218 supplied the pure incident-page renderer, not genuine execution. The frozen executable witness is JavaScript run by Node; a standalone Go binary cannot honestly reuse its execution identity. The user selected a separately identified native witness/runtime while preserving historical cached REPLAY bytes.

Implement a narrowly scoped native demo executor and RERUN library, with an implementation-private child entry in `fl`. It evaluates a reviewed Go model of the refund example under materialized configuration. It does **not** execute the original JavaScript, arbitrary repository code, or a sandboxed user program.

Separate ongoing work: qualify the actual PR218 main merge `db37e8694c88a9bdff5f4000152be6e7f89c71e4`. The last retained qualification is 12/35 dispositions: nine scoped passes, two platform exclusions, one old-source outcome; 23 pending. This is not a current completion claim. Plan mode defers evidence-writing collectors; resume the existing serialized collector after leaving read-only mode, without restarting the completed PR qualification.

## Scope and compatibility boundary

- Preserve `CreateFrozenWitness`, `historicalEnvironment`, `replayRun`, `CreateReplayAnalysis`, existing golden corpora, and ordinary CLI behavior. Only the demofixture package description needs adjustment to acknowledge its additive native API.
- Native-only differences: witness identity/approval/command/overlay/policy; native incident description; environment identity; versioned run-ID construction; fresh times and measured durations; real native parse errors; truthful Go-model summary/claims; fail-closed behavior on executor failures. Record exact fields and expected strings in the spec and `difftest/KNOWN_DIFFERENCES.md`. This is not permission for additional differences or output masks.
- Preserve the logical 20 states, hunk interaction, metrics, normal 39-invocation order, three three-per-side boundaries, six minimization attempts, and three prevention runs. PASS/FAIL child stdout/stderr/status remain exact historical executable-witness bytes; use the structured predicate reason codes, not cached REPLAY's reason codes.
- No full `judge-demo`, plain `serve`, writable HTTP, general sandbox, bundle-format change, release, cutover, TS retirement, or completion claim for all Slice 5. No edits to `src/`, `package.json`, or `pnpm-lock.yaml`.
- No checkout transition, commit, push, PR, merge, unfreeze, installation, or historical workspace cleanup follows from this design approval. Preserve the hook and personal-account requirements for any separately authorized publication.

## Recommended architecture

### 1. Reviewed native program and actual child execution

Add `internal/nativefixture`, independent of CLI and demofixture orchestration. It owns the reviewed evaluator, configuration materializer, native child entry, process lifecycle, and observed environment. Additive files in `internal/demofixture` own the native witness and analysis orchestration.

The child reads one fixed file, `native-config.json`, from its private working directory. The parent materializes only two reviewed configuration choices:

| Hunk | Native model change |
| --- | --- |
| `settlement-display-default` | display fallback changes from order currency to EUR |
| `settlement-display-boundary` | settlement assignment changes from order currency to display currency |
| `partial-renderer-hunk` | the reviewed partial textual edit leaves the assignment value missing, producing genuinely invalid JSON |

Use fixed reviewed text templates/edits, selected by allowed hunk names, never state-ID-to-verdict lookup. Unknown hunks or unsupported combinations fail before execution. The partial config is an explicitly synthetic native partial edit, not an assertion that the historical fixture contained conflict markers.

The two fields are `displayDefault` (`ORDER_CURRENCY` or `EUR`) and `settlementSource` (`ORDER_CURRENCY` or `DISPLAY_CURRENCY`). The child strictly decodes them, computes `completeRefund({currency: "USD"}, {})`, then independently evaluates the settlement-preservation predicate. Reject duplicate/unknown/missing fields, trailing data, wrong types, invalid enum values, and oversized input. Limit config input to 16 KiB. A token decoder may parse this input only; structured output and digest preimages always use `jsjson`/`canonical`, never `encoding/json` serialization.

Success emits exactly:

```text
settlement currency preserved
{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"}
```

Failure emits only the PREDICATE_FAIL JSON line on stdout and `expected settlement USD; received EUR\n` on stderr. Exit codes are 0 for predicate pass, 1 for predicate failure, and 3 for a real configuration parse/validation failure. Native parse diagnostics identify the native configuration decoder; no copied V8 messages. As fixed in implementation plan §2.2–2.3, configuration-error stderr is `native fixture config: ` followed by the nonempty actual decoder/validation cause on one line and exactly one LF (no CR). Synthetic reference diagnostics are labeled test inputs; live comparison retains the actual cause unchanged, not the scripted example.

`cmd/fl/main.go` calls a small helper in new `cmd/fl/native_sample.go` before `nodeArgv()`. The helper activates only for the exact private argument `--internal-native-demo-witness-v1` plus protocol marker `FAULTLINE_NATIVE_DEMO_CHILD=v1`. Otherwise existing dispatch is untouched. This is reachable functionality, not an authentication mechanism. It cannot select another program, filename, shell command, or repository.

### 2. Explicit witness approval and fresh identity

Expose a proposal and an approved execution API:

```go
func NativeWitnessProposal() jsjson.Value
func RunNativeAnalysis(ctx context.Context, approval NativeApproval) (NativeResult, error)
```

`NativeApproval` supplies the proposal digest and the caller-recorded approval time. Reject a different proposal digest, absent/malformed approval, or approval after execution begins. Freeze the witness before any localization/run. The caller is responsible for recording a real authorization event; this API does not authenticate a human. Future CLI/UI integration must obtain that event explicitly. Do not invent a historical approval date or reuse July's Node approval.

The proposal contains the fixed native evaluator/version, input values, predicate, config schema/templates, native incident packet, and witness policy. Its digest binds what the caller selected. Construct the final existing-schema witness with a distinct native ID, actual private-child command, overlay `["native-config.json"]`, `built-in-sample` policy, supplied `approvedAt`, and fresh digest. Bind the constant program/descriptor digest through the policy note and returned provenance without adding fields to existing bundle schemas.

Witness digest is constant across all states of one analysis; **per-state configuration hashes do not belong in it**. Environment preimage records the native runner version, Go runtime version, GOOS/GOARCH, observed executable-file SHA-256, and the actual minimal child environment. `network: disabled` describes the reviewed program's lack of network operations, not OS-enforced isolation; the policy note must say it is not a sandbox. No credentials are inherited.

### 3. Bounded process lifecycle and evidence

Resolve `os.Executable()` internally: no executable/path/runner supplied by external callers and no PATH lookup. Use a dedicated standard-library `os/exec` path for this fixed native helper. Do not modify/reuse `nodeproc` as if its Unix SIGTERM/readers-before-Wait behavior guaranteed hard termination; it already supports stdin, so lack of stdin support is not the rationale.

Create a fresh owned directory (0700 on Unix) and config file (0600); Windows ownership follows its native temp-directory access rules, not fictional POSIX ACL guarantees. Child stdin is closed. Supply only the protocol marker and required Windows `SYSTEMROOT`; derive/record the effective environment, including any additions made by `os/exec`. No inherited PATH, token, HOME, or test-control variable.

Each process has a two-second context deadline, direct owned-process Kill on cancellation, a bounded pipe-drain/WaitDelay of 250 ms, and a combined stdout/stderr ceiling of 1 MiB. Overflow cancels execution and is an error, never an accepted truncated run. The reviewed child does not create descendants; this is not a process-tree supervisor. Use a 90-second whole-analysis execution context, bounded additionally by the caller's context. No automatic retries.

Record wall timestamps through `clock.Now`/`clock.ISO`. Add a small unseeded evidence-duration timer in `internal/clock/evidence.go`, using monotonic `time.Now`/`time.Since`, returning floored elapsed milliseconds around Start/Wait. Clarify the clock comments: evidence durations are distinct from wall timestamps and from `clock.Monotonic`, whose output prohibition remains unchanged. No deadline-clock change or release entropy override.

Generate one fresh UUID nonce per analysis through the existing production clock/entropy path and a strictly incrementing invocation ordinal. A native versioned ID preimage contains schema version, nonce, ordinal, execution-attempt index, start timestamp, raw config digest, exit status/signal, and the complete RunRecord body excluding its own ID. Store the preimage in its receipt; canonical-digest it for the ID. Do not sleep, spin, advance timestamps, or require monotonically increasing wall time. Reject repeated native IDs even when their records agree, before using the existing catalog sorter.

After capturing evidence and reaping the child, remove only the directory this invocation created. Report cleanup failure; never delete a caller directory or old evidence. Failure receipts retain available streams and explicit truncation/error state; they are not successful RunRecords.

### 4. Orchestration, claims, and returned provenance

Port the RERUN control flow from frozen `src/engine.ts` into new `internal/demofixture/native_analysis.go`. Reuse existing `fixtureStates`, state indexes, `transitionKind`, `attempt`, ordered `jsjson` construction, canonical digest helpers, and `uniqueRunCatalog` after the stricter native uniqueness check. Do not refactor the cached analysis to share an executor.

Reuse `witnessresult.Resolve`, `ConsistentWithExitCode`, and `Classify`, with stricter native checks for exact allowed transcript/status combinations. Do not use `witnessresult.Format` if it would add the marker absent from historical stdout. No unstructured exit-zero fallback.

A real parse failure on the designated partial configuration becomes EXECUTED/ERROR with `FIXTURE_PARSE_ERROR` and minimization UNRESOLVED. A parse failure on a valid configuration is a materializer/executor failure. Spawn failure, timeout, signal, panic, overflow, malformed/conflicting protocol, or inconsistent status aborts and returns a typed error with partial provenance but **no analysis**. Before returning analysis, verify stable boundary evidence, FAIL/PASS sufficiency/necessity, and PASS/FAIL/PASS prevention. Unsupported proof conditions return an error rather than inheriting unconditional historical certification prose.

`NativeResult` contains the analysis and a separately versioned `faultline.native-demo-execution.v1` provenance envelope. Include the witness proposal/program/config templates, environment preimage, nonce, and ordered invocation receipts. Each receipt binds state ID, actual config bytes and digest, executable/argv/cwd, observed start/end/duration/status/signal, complete captured streams and their hashes, and the run-ID preimage. Encode raw config/stream bytes losslessly as base64 in the envelope, with SHA-256 over the original bytes; successful RunRecord strings must match the valid-UTF-8 native protocol exactly. An overflow receipt explicitly marks its captured prefix incomplete and cannot produce an accepted RunRecord. Link every successful catalog entry to exactly one receipt; retain invocation order independently of the sorted catalog.

Native fixture ID remains `parallel-settlement-regression`; title is `Parallel settlement-currency regression — Go model`; summary is `A reviewed Go model evaluated under materialized configuration, with a real native witness and a two-edit interaction.` Witness ID is `refund-settlement-currency-go-model`, version 1, and runner identity is `native-go-config-evaluator-v1`. Replace `frozen witness` with `approved native witness` in the native-only attempt/claim text; replace the partial-attempt note with `The partial native configuration cannot parse and is unresolved, not proof.` The native warning is `Executed reviewed Go model. This does not execute the original JavaScript or external repository code and is not a general-purpose sandbox.` Other normal orchestration copy stays identical where accurate. Never say Node or the original JS ran. The new incident packet describes the Go model and references the historical sample without inventing a native CI excerpt. Keep DEMO_SAMPLE fidelity; existing state tree digests remain logical state-tuple digests, not hashes of native source/config files. This is inspectable process evidence from a trusted local implementation, **not remote attestation**. An executable-file hash is an observation before execution, not proof against a malicious/replaced executable. Do not claim the existing bundle writer persists this envelope or its verifier authenticates execution; bundle integration remains a separate design.

## Critical paths

| Area | Files |
| --- | --- |
| New execution subsystem | `internal/nativefixture/{program,child,process,provenance}.go` and focused tests |
| Additive native analysis | `internal/demofixture/native_{witness,analysis}.go` and tests; package-description-only adjustment in `fixture.go` |
| Private bootstrap | `cmd/fl/main.go`, new `cmd/fl/native_sample.go`, focused dispatch/child tests |
| Evidence duration | new `internal/clock/evidence.go`, tests, and precise existing comment adjustment |
| Design and reference evidence | `docs/superpowers/specs/2026-11-03-go-native-demo-rerun-design.md`, subsequent implementation plan, `difftest/gen/` test-only reference driver, new native/orchestration corpus, `difftest/KNOWN_DIFFERENCES.md` |

Keep `internal/nodeproc`, existing bundle writer/verifier, existing renderer, protected source, workflows/gates, and old corpora unchanged.

## Verification and acceptance

1. **Independent full-output reference, before implementation.** Bind the exact frozen TS source `ce99a793af0b020bf1b9cf21114f8d7897b37207` and pinned Node 22.22.2. Reuse the existing generator's source/compiled-module/hash/exclusive-capture patterns, not its renderer-only corpus. A test-only module loader runs the unchanged compiled engine with the real frozen state definitions and a strictly consumed 39-call transcript; intercept execution and time only inside that test context. Retain the entire input transcript, entire serialized analysis, source/toolchain bindings, and a fresh independent byte-identical recapture. Label scripted records as synthetic test inputs, never live execution. Fail capture on unexpected calls/imports or incomplete consumption. Prove loader feasibility before relying on it; a failure blocks this evidence step rather than silently substituting a Go-generated golden.

   Keep an unadapted full frozen-engine reference. Define the separately versioned native contract with an explicit, exhaustively checked native-copy profile; it is not an assertion that frozen TS emitted native provenance. Capture a complete expected native analysis/result for deterministic test dependencies, using an independent test-only reference constructor for the envelope/ID preimages. Each case has exactly one expected byte string. Never strip fields, rewrite candidate output, accept either of two outputs, or derive expectations from the Go implementation under test. For an actual unseeded native run, also replay its complete observed run transcript through the independent analysis reference and compare the entire analysis bytes, including the original IDs/times/streams. Reference input does not itself prove those records were executed: the process tests below supply that separate evidence.

2. **Real native child and complete provenance.** Test all four complete hunk combinations, the partial config, and all 20 logical states through an actual re-exec using the same child implementation. Compare complete stdout, stderr, and status, not only verdicts. Use test-file TestMain interception for library self-exec tests; independently build `fl` and exercise its actual private bootstrap. Verify every receipt's raw config, ID preimage, full RunRecord, environment preimage, binary-file hash, and catalog linkage. Independently hash the real executable; do not replace production provenance with a fixed test digest. Compare full deterministic contract fixtures, then validate the real unseeded 39-process run and its complete reference output. Equal/backward wall-time unit cases must still yield distinct native IDs, without sleeps or fabricated times.

3. **Fail-closed lifecycle tests.** Cover rejected approval before any spawn; unknown hunks; duplicate/unknown/missing/trailing config input; malformed/conflicting/missing protocol; status/outcome contradiction; unexpected parse error; spawn failure; cancellation/deadline; signal/abnormal exit; output overflow; duplicate/conflicting IDs; missing proof conditions; and cleanup error. Each failure returns no analysis and retains the available non-success receipt. A syntactically valid lie from a malicious replacement binary is not promised detectable—no attestation claim. Use unexported fault seams and test-only blocking-I/O helpers for lifecycle faults, never production overrides or tracee sleep/load alignment. Verify owned child exit, bounded pipe closure, no retained live child, and cleanup ownership.

4. **Regression and Node-free deployment checks.** Run the complete old demofixture REPLAY byte tests unchanged, all 13 renderer tests/24 exact pages, clock tests in ordinary and existing test-tag configurations, private-dispatch negative tests, and relevant CLI/proofview/bundle regressions. Build release-mode native test/CLI binaries and run from fresh empty directories with empty PATH and no Node, Go compiler, TS/dist, testdata, or package manifest available. Test-only fixture input may be supplied by an absolute path; it is not a production dependency. Demonstrate that release builds ignore test seed variables. Use scoped RED/GREEN, race, vet, and the source-defined broad gates; no narrow-success substitution for broad acceptance.

5. **Platform and evidence qualification.** Cross-build all six targets as compilation evidence only. Require actual native Linux/macOS/Windows amd64 and arm64 execution of the new real-process tests, with versions, original status, test names/counts, skips, and retained logs accounted for. Do not alter workflows, skip guards, existing stack tolerance, or timeout gates to obtain success. Preserve every failure/partial attempt and all old corpus bytes. Independent review checks model transcription, provenance claims, new native-only differences, process ownership, and protected-path immutability. All controller shell/build/network/evidence work remains serialized through the reviewed W/run.py with a maximum 240-second aggregate budget per invocation; split bounded stages, never detach/reset watchdogs. Hosted collection/publication remains subject to its existing authorization boundaries.

## Delivery sequence and approval boundary

1. Approve this architecture and native-only compatibility contract. Persist the approved design in the named spec without inventing additional decisions; review scope/consistency. No automatic commit.
2. Use `superpowers:writing-plans` to produce the detailed test-first implementation tasks. Establish independent expected bytes and missing-feature RED before production code.
3. Implement the native program/process lifecycle and witness/provenance; then the additive orchestrator and private bootstrap. Test seams remain unexported, with fakes in test files; the production factory always launches the approved fixed child.
4. Complete scoped and broad verification, independent review, and native platform qualification. Stop at local delivery unless publication is separately authorized.
5. Resume/finish actual-main CI qualification independently, preserving exclusions/debt and requiring actual main freeze/provenance execution. Cancel its existing scheduler at closure or a genuine blocker; do not duplicate it.
