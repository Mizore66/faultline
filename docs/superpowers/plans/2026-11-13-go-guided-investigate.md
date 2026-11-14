# Go Guided Investigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port frozen guided `investigate --ci-log` / `--resume` with exact human-freeze, persistence, continuation, streams and interruption behavior.

**Architecture:** New `internal/guided` composes existing incident/witness/doctor/runtime/review APIs without creating an incident→proofview import cycle. Intake and freeze wait are independent; the state machine consumes both; the CLI owns options/session/config and one-shot SIGINT. Qualification adds permanent executed evidence and frozen comparisons, not a new provider path.

**Tech Stack:** Go1.27 standard library; existing jsjson/jsstr/clock/nodefs/nodeproc/incident/witness/proofview; frozen Node22.22.2 oracle only.

**Spec:** `docs/superpowers/specs/2026-11-13-go-guided-investigate-design.md`

## Global Constraints

- Branch `go-guided-investigate-20261113`, isolated worktree based on exact normal merge6b64f6c. User explicitly delegates design/spec decisions; no routine approval prompts.
- Keep protected TypeScript, package.json, pnpm-lock.yaml, dependencies, workflows, release and oracle retirement unchanged. New protected CI metadata changes require explicit separate authority; do not silently update skip/shard count guards.
- No real model/provider request. No automatic human approval/freeze. No host repository command execution in tests: only owned fixture repos/commands and existing sandbox policy.
- Exact frozen semantics; JS UTF-16/whitespace/Number, null versus absent, key/clock order, partial writes and late failures are observable. No encoding/json on canonical/digest paths.
- Every command below five minutes; Go timeout<=195s for local tests. Split aggregate tests into retained bounded windows; do not hide full-suite incompleteness.
- RED before implementation, GREEN, independent spec/quality review, fix/re-review; final whole-branch review. No new platform skip, coverage exemption or output mask to force acceptance.
- All agents leave changes uncommitted and do not stage/push or dispatch reviewers. Controller serializes exact-path integration. `.superpowers/` is untracked local evidence, never staged. Do not commit/push successor work without applicable authorization; no global Git/auth changes.
- Existing user-requested parallelism applies only to disjoint file owners. Tasks1/2 parallel; Task3 after both; Task4 after3; Task5 after4; Task6 last. No shared coverage/CLI writers in parallel.
- Prior PR231 post-main acceptance must be verified independently before successor external publication; failures take precedence over publication.

## File and ownership map

| Task | Source | Tests/evidence |
|---|---|---|
| 1 intake | `internal/guided/intake.go` | `internal/guided/intake_test.go`, `intake_head_test.go` |
| 2 freeze | `internal/guided/freeze.go` | `internal/guided/freeze_test.go`, `freeze_process_test.go` if needed |
| 3 composition | `internal/guided/guided.go` | `internal/guided/guided_test.go` |
| 4 CLI | `internal/cli/investigate_guided.go`, `guided_interrupt.go`, minimal `investigate.go` | `investigate_guided_test.go`, `guided_interrupt_test.go`, `investigate_guided_process_test.go`, narrow obsolete stub row in `investigate_test.go` |
| 5 qualification | additive `difftest/guided_*_test.go`, `difftest/gen/gen-guided-investigate.mjs`, guided fixture directory | shared coverage collector only by this owner; no shared writer templates requiring protected shard changes without authority |
| 6 closure | `difftest/KNOWN_DIFFERENCES.md`, migration overview and dependency map | local qualification/report/review packages in ledger |

## Agreed cross-task interfaces

Task1 defines in intake.go (no shared scaffolding file):

```go
type IntakeOptions struct {
    Repository, CILogPath, WitnessStore, DraftStore string
    Command, From, To, IncidentID, RuntimeAlias *string
}
type IntakeResult struct { Draft jsjson.Value; DraftPath string }
func CreateIntake(o IntakeOptions) (IntakeResult, error)
```

Private `intakeDeps` supplies runtime resolution, HEAD observation and existing proposal/draft calls for deterministic ordering/failure tests; normal defaults call the real existing modules. Do not export general process or endpoint overrides. Timeout is300 on this reachable path; ignored CLI timeout flag is not a new library setting.

Task2 defines in freeze.go:

```go
type FreezeOptions struct {
    ProposalID, WitnessStore, DraftStore string
    Port *float64
    PollInterval *time.Duration
    OnReviewReady func(string)
    Signal context.Context
}
type FreezeResult struct {
    FrozenDigest string
    ReviewURL *string
    AlreadyFrozen bool
}
func WaitForFreeze(o FreezeOptions) (FreezeResult, error)
```

Private `freezeDeps` permits fake verifier/timer/server close for deterministic order tests. Real-server tests must still cover actual form actions and listener drain. A nil Signal is background context; its cancellation is consumed only by this waiter.

Task3 defines in guided.go:

```go
type Options struct {
    Repository string
    CILogPath, ResumeID, Command, From, To, IncidentID *string
    Runtime, Image, ExpectDigest, LedgerFile, Output *string
    WitnessStore, DraftStore *string
    MaxStates, ReviewPort *float64
    Runner sandbox.Runner
    WaitForFreeze func(FreezeOptions) (FreezeResult, error)
    OnPhase func(string, *string)
    OnReviewReady func(string)
    Signal context.Context
}
func Run(o Options) (jsjson.Value, error)
```

Private `runDeps` wraps doctor/runtime/incident.Continue/intake/verification for exact phase/order tests. `Runner != nil` is supplied/injected and passes `Injected:true` to continuation even if it happens to be NativeRunner. Do not pass Signal to doctor/runtime/Git/continuation. Callback and error handling follow existing jsexc conventions.

Task4 adds `func guidedInvestigateCommand(e *env, args []string) error`; existing guided guard delegates to it. Add a private interrupt helper returning `(context.Context, func())`, with idempotent cleanup; signal.Notify/Stop registration is removed after the first SIGINT so later SIGINT retains normal behavior. Test its actual subprocess semantics, not just context cancellation.

---

### Task 1: Exact guided CI-log intake

**Consumes:** spec4.2; `incident.CreateDraft`, `incident.WriteDraft`, `witness.Propose`, runtimeimage resolution, Node-compatible filesystem/process and seeded clock.
**Produces:** IntakeOptions/IntakeResult/CreateIntake above.

- [ ] **1. Add RED symptom/command/ID/range tests.** Use independently specified strings, including a split surrogate boundary:

```go
func TestGuidedSymptom(t *testing.T) {
    got := deriveSymptom("first error\r\n fine \n ASSERT last \r\n trailing\n")
    if got != "ASSERT last" { t.Fatalf("got %q", got) }
}
```

Table cases: last matching versus last nonblank versus fallback; JS-only whitespace; CRLF; 500 UTF-16 units; original log4000 units preserving content; blank command exact default; supplied empty ID retained; generated base36 ID before createdAt/proposal clock calls. Use an owned two-commit Git repo for real baseline intake, with `.faultline` paths isolated in it. Assert full jsjson proposal/draft bytes/digests, modes, exact local range and command/policy.

- [ ] **2. Run meaningful RED.** `go test -count=1 -timeout=195s -run '^TestGuided(Intake|Symptom|Head)' ./internal/guided`. Missing package/symbol initially acceptable; after tests compile preserve behavioral RED for actual mutations. Do not write production before recording RED.
- [ ] **3. Implement minimal intake.** Copy exact safe Git config/env/argv and no-timeout/default-buffering behavior from frozen guided source. `nodeproc.SpawnSyncWith` handles sync result/error/UTF-8; no ordinary exec stdout/error shortcut. Use JS trim/UTF16 helpers, clock.NowMillis base36 and clock.ISO(clock.Now) in frozen order. Do not reuse incident-start diagnostic text. Runtime resolves before HEAD; HEAD reads despite explicit range; proposal writes before final draft.

After preliminary `incident.CreateDraft(input)` and `witness.Propose` with the complete Section4.2 packet/witness object succeed, bind the actual proposal before final persistence:

```go
binding := jsjson.NewObj()
binding.Set("proposalId", proposal.Get("proposalId"))
binding.Set("proposalDigest", proposal.Get("proposalDigest"))
binding.Set("incidentPacketDigest", proposal.Get("incidentPacketDigest"))
binding.Set("commandDigest", proposal.Get("witness", "commandDigest"))
input.Proposal = jsjson.MakeObject(binding)
draft, err := incident.CreateDraft(input)
if err != nil { return IntakeResult{}, err }
path, err := incident.WriteDraft(o.DraftStore, draft)
return IntakeResult{Draft: draft, DraftPath: path}, err
```

`input` is existing `incident.DraftInput` (draft.go:29–48); its Runtime/Proposal are jsjson.Value, while ID/Repository/Command/CreatedAt are strings. Construct packet/proposal fields in frozen source order using jsjson.NewObj, MakeString, MakeArray and MakeObject; persist through witness.Propose, never a new serializer or schema.

- [ ] **4. Extend error/order tests.** Paired absent/empty from/to; root/merge/equal ranges; read error; empty log schema failure; runtime failure before proposal; failed HEAD despite explicit range; exact stderr+spawn-error concatenation; no HEAD output; immutable draft collision leaving an independently verified orphan proposal. Inspect exact safe argv/env with controlled runner and exercise real owned Git repo. Never invoke target repo commands.
- [ ] **5. GREEN/race/vet.** Run focused intake, `go test -race -count=1 -timeout=195s -run '^TestGuided(Intake|Symptom|Head)' ./internal/guided`, `go vet ./internal/guided`. Preserve any command failure and fix only task paths.
- [ ] **6. Independent review.** Reviewer receives spec4.2, frozen source and task diff/report; fix/re-review. Leave source uncommitted for controller. Record exact RED/GREEN and limits in task-1-report.md.

### Task 2: Human freeze gate and polling lifecycle

**Consumes:** spec4.3; witness.VerifyFrozen and proofview.StartWitnessReviewServer.
**Produces:** FreezeOptions/FreezeResult/WaitForFreeze above. Disjoint from Task1.

- [ ] **1. Add RED gate/order tests.** Existing valid freeze returns exact digest, null URL, AlreadyFrozen=true without server/port/cancellation use. Otherwise server then readiness then immediate poll, sleep500ms only after invalid observation, signal checks before poll and during sleep. A verifier sequence and channel-controlled clock/server seam must prove ordering without time-based releases.

```go
ctx, cancel := context.WithCancel(context.Background())
cancel()
_, err := WaitForFreeze(FreezeOptions{ProposalID: "p", WitnessStore: store, Signal: ctx})
if err == nil || err.Error() != "Guided investigate cancelled before freeze." {
    t.Fatalf("err=%v", err)
}
```

Use a real proposed record so startup is valid; separately assert preexisting frozen record ignores that canceled signal.

- [ ] **2. Run RED.** `go test -count=1 -timeout=195s -run '^TestGuidedFreeze' ./internal/guided` before production.
- [ ] **3. Implement waiter.** Verify first; default ephemeral port; set TwoStep only from environment string1. OnReviewReady is invoked before the polling cleanup region, matching TS. Inside the polling region use deferred close that replaces return/error if close fails. Preserve invalid-observation retry and no overall timeout; context cancellation becomes exact frozen error, not context.Canceled. No browser/stdin approval.
- [ ] **4. Add real-server tests.** Parse actual form credential from page; execute atomic approve-freeze and environment-two-step approve then freeze; verify full witness chain/digest and returned URL. Cancel after actual readiness; observe server closure/listener refusal and joined goroutines. Error tests cover bind failure, invalid credential, failed verify until cancellation, close-error precedence and readiness-callback failure semantics. Do not weaken server mutation policy or change server production files.
- [ ] **5. GREEN/race/vet.** Focused normal and `-race` tests with195s timeouts, vet guided. Check both parallel tasks remain file-disjoint. Report task-2-report.md.
- [ ] **6. Independent review and fix loop.** Focus on exact try/finally boundary, drained lifecycle and no auto-freeze. Leave uncommitted.

### Task 3: Guided state-machine composition

**Consumes:** reviewed Tasks1/2 plus doctor.Run, runtimeimage.ResolveCuratedRuntime, incident.ReadDraftPath and incident.Continue.
**Produces:** Options/Run above and ordered result.

- [ ] **1. Add RED phase/result tests.** Private injected dependencies return fixed doctor/draft/freeze/continuation values and retain a call transcript. Assert complete compact/pretty jsjson results, exact fields/nulls/notes/next commands, and phase sequence. A blocked doctor must have only PREFLIGHT and no reads/writes.

```go
want := []string{"PREFLIGHT", "INTAKE:ci-log", "WITNESS_REVIEW", "RUNTIME_SELECTION", "LOCALIZATION"}
if !reflect.DeepEqual(phases, want) { t.Fatalf("phases=%q", phases) }
```

Separately require PROOF_EXPORT only after fake continuation reports successful verified publication. Injected observations must be classified nonproof by the real continuation tests.

- [ ] **2. Run RED.** `go test -count=1 -timeout=195s -run '^TestGuidedRun' ./internal/guided`.
- [ ] **3. Implement Run in frozen order.** Resolve repo/stores; emit phases; retain single doctor snapshot; fresh intake or immutable resume; broad missing-error wait classification; expected/observed digest agreement; full MATCH revalidation; draft/config/runtime image precedence; late native readiness; existing continuation; exact output projection. Use jsjson objects in Section6 key order.
- [ ] **4. Cover frozen quirks and late failures.** Resume repo mismatch before witness; expected mismatch before runtime; corrupt chain without missing error throws; missing companion waits; initial canceled signal with existing freeze proceeds; draft image mismatch wins over Docker; missing image wins over Docker; continuation failures preserve earlier phase lines/files; no runtime mapping; nonproof exact resume command; proof exports only real verified existing continuation result. No mutation of stored draft after freeze.
- [ ] **5. GREEN/race/vet and integration.** `go test -count=1 -timeout=195s ./internal/guided ./internal/incident ./internal/witness`; focused guided race; vet. Use real witness records for binding tests, not only mocks. Preserve injected-runner evidence distinctions.
- [ ] **6. Independent spec/quality review, fix/re-review, task-3-report.md.** No CLI writer in parallel with this task.

### Task 4: CLI wrapper, session behavior and one-shot signals

**Consumes:** reviewed guided.Run and existing CLI option/config/session helpers.
**Produces:** fully wired guided command, exact streams/exit and portable signal lifecycle.

- [ ] **1. Add RED wrapper/dispatch tests.** Update only obsolete guided-not-ported expectations. Include git/turn conflict before both-mode refusal, paired ranges even on resume, first flag/equals/trailing/empty/flag-as-value semantics, invalid session binding despite explicit overrides, `--id` vs resume identity, config image precedence, ignored flags, JS Number port/max-states. Keep all existing git/turns dispatch cases unchanged.
- [ ] **2. Add RED output/session tests.** Controlled guided.Run seam records all Options and invokes callbacks. Assert complete stdout/stderr/exit with jsjson pretty formatting. Verify post-result binding for defined wrapper ID+preexisting digest even on PREFLIGHT_BLOCKED, no write on propagated throw/rejection, no auto-generated-ID write, no new observed-digest binding. Add an already-aborted signal with an otherwise returning guided result: binding/output must still run (no wrapper ctx.Err() suppression). Inspect exact persisted bytes/digests.

```go
// Callback behavior must remain observable before a returned error.
o.OnPhase("PREFLIGHT", nil)
return jsjson.Value{}, errors.New("scripted guided refusal")
```

Expected CLI stderr combines that exact phase then standard failure/hint; stdout is empty. Use the existing CLI env/test harness API rather than inventing a second parser.

- [ ] **3. Run RED.** `go test -count=1 -timeout=195s -run '^(TestGuidedInvestigate|TestGuidedInterrupt|TestInvestigate)' ./internal/cli`.
- [ ] **4. Implement narrow wrapper and lifecycle.** Existing investigate guided branch delegates to new function. Parse/store/config resolution in spec order; install signal handler immediately before Run; cancel only waiter signal; unregister after first SIGINT; cleanup idempotent and joins owned goroutine. Keep an unconsumed registration through post-result binding, JSON serialization/output and exit-code assignment, not only through guided.Run. Preserve standard error wrapper and explicit exit1 for blocked/nonproof results. Use existing private seams only where needed for in-process tests.
- [ ] **5. Add real process tests.** Build actual fl outside repo; use owned fake doctor tools/owned Git fixtures and portable servefixture process ownership. Assert actual review readiness, HTTP approval/freeze, first-SIGINT cancellation cleanup and state retention, resume with same immutable records, default SIGTERM, no browser/stdin approval. Test first/second SIGINT disposition in a child test process using the real interrupt helper and an explicit first-signal-consumed barrier; do not add a production acknowledgment hook or assume a delay proves consumption. Do not claim second-signal delivery if process already exited.
- [ ] **6. GREEN, bounded regressions/race/vet.** Focused command/lifecycle tests, existing investigation/incident/witness CLI regressions, focused CLI race. Respect five-minute aggregate cap; don't run all slow CLI race tests in one invocation. Verify no provider/target-repo command execution.
- [ ] **7. Independent review and fix loop.** Include actual Windows launcher preparation/cleanup path and exact no-extra-output assertions. Task-4-report.md retains runtime vs compile limits.

### Task 5: Independent corpus, oracle, coverage and platform qualification

**Consumes:** complete reviewed command and source pin. Sole owner of shared difftest coverage files.
**Produces:** executable permanent guided parity evidence and exact native qualification.

- [ ] **1. Add independent frozen capture.** Dedicated `difftest/gen/gen-guided-investigate.mjs` using frozen source/CLI under Node22.22.2, fixed seed and controlled own environment/tools. Verify actual Node/pin/manifest provenance before capture. Write only additive `difftest/testdata/guided-investigate/` baseline/cases/golden/retained outputs; refuse accidental overwrite without explicit recapture mode. No Go-derived expected output. Preserve old corpus byte-for-byte.
- [ ] **2. Add actual covered CLI diagnostic replay.** New `difftest/guided_replay_test.go` executes flBinary (not cli.Run) on deterministic refusal/preflight/session/runtime paths and compares committed exact streams/exit/tree. Forward GOCOVERDIR only to Go subprocesses, isolate HOME/TMP/API env and resolve actual workspace before path expectations. Derive only owned dynamic path tokens; no blanket slash/case/error normalization. Add negative controls for missing/stale/tampered fixture or omitted execution.
- [ ] **3. Add source-bound library evidence where CLI cannot reach paths.** Put internal/guided in gatedDirs and existing positional instrumentation; remove obsolete unported literal exemption. Extend existing native-library collector with exact source/package/test/profile bindings for actual direct guided error mutations if necessary. Retain emitted diagnostics plus real coverage counters; do not insert fixture strings without executed producer. Freeze close/poll/library-only invariants need real tests, not unreachable waivers.
- [ ] **4. Add frozen live/process comparisons.** `TestLiveGuidedInvestigate` uses independently recreated fixture rounds at the same test-owned absolute workspace slot when repository-path-derived draft digests require identical input paths. Before recreating the slot, retain each round's complete immutable snapshot in its own evidence directory; never discard capture evidence. Use the same controlled fixtures/seed, actual Node CLI and fl. Compare all streams/exit/argv/persistent tree/modes and store digests. Do not mask or rewrite arbitrary digests to compensate for different repository inputs. Cases without path-derived digest effects may use independent roots with exact owned-root normalization. Exercise preflight-blocked, fresh intake, canceled review then resume, frozen fast path, runtime/expected digest conflicts, partial persistence and nonproof. Register in existing TestLive discovery; no workflow changes. Use only approved root/ephemeral-address normalization and independently match time/clock order. Split expensive scenarios into individually bounded selections, not skips.
- [ ] **5. Native proof/portability.** Use existing native sandbox and owned small Git fixture for successful proof; independently verify bundle/root and no injected provenance. If Docker unavailable, record the exact non-execution and retain the hosted gate as pending; injected/fake results are never proof acceptance. Compile all six targets outside tree; execute native fixtures in existing hosted gates when publication is authorized. Native Windows signals/process tests reuse shared preparation; unavailable symlink privileges are not a new blanket skip.
- [ ] **6. Run final focused gates.** Normal/tagged affected packages; guided/CLI focused race/vet; TestGoldensReachEveryErrorLiteral and exact source/profile collector controls; existing writer replay metadata guards without changing protected totals unless explicitly authorized; frozen recapture twice with independent output directories and byte-compare. Full unnarrowed positional coverage remains its hosted single process; local windows explicitly report only their owned packages.
- [ ] **7. Independent review and fix loop.** Review source-to-corpus independence, execution bindings, normalization scope and every platform claim. Task-5-report.md keeps failed/incomplete attempts, command caps and open hosted gates.

### Task 6: Closure documentation and whole-branch review

- [x] **1. Update known differences/status precisely.** Record no intended guided semantics change; document only actual unavoidable/approved differences with exact evidence if discovered. Correct stale previous-project local-only wording using verified merge/CI receipts, distinguish prerequisite main acceptance from current local-only5g. Do not claim full Slice5/release/retirement completion.
- [x] **2. Run bounded final suite selections.** Separate affected ordinary/tagged package groups under195s each; exact guided replay/literal/coverage gates; focused races; six-target compilation. Retain output logs outside source and accurately report any failure/incompleteness. Never bypass hook or change tests to suppress failures. Completed locally with the skips, optional echo launch failure and mandatory hosted gaps recorded below; this checkbox does not mean full hosted acceptance.
- [x] **3. Whole-branch independent spec and quality review.** Base6b64f6c through complete tracked diff; all new untracked source files included. Fix/re-review important issues before declaring local delivery ready. No publication during review. Final completed review found no Critical or Important defect; failed API attempts are not approvals.
- [x] **4. Retain local delivery.** Record branch, file list, tests, known failures and unexecuted hosted requirements in ledger. Preserve worktree/evidence. Publication and future merge remain separately authorized; no automatic push/release/deletion. User's no-design/spec-prompts permission means make routine design/plan choices, not erase protected/outward-action boundaries.

## Controller self-review and execution ruling

All spec sections map to tasks: CLI3→4; intake4.1/4.2→1/3; waiter4.3→2; state/runtime/continuation4.4→3; output/lifecycle6→3/4; permanent/oracle/gates7→5; status/qualification→6. Required interfaces are defined once above and task implementations must not drift silently. Tests may introduce private fixture/dependency helpers within their owner files; the plan does not create production scaffolding before RED.

Execution choice is inherited from the user's established subagent-driven/fan-out preference and latest autonomous instruction: parallelize Tasks1/2 only after the spec is internally consistent and independently reviewed; review each before Task3. Controller owns shared index and review dispatch; implementers never spawn reviewers. Do not ask the sleeping user to choose an execution style or approve this spec/plan.

## Local qualification and status appendix (updated 2026-11-14)

Status: Tasks 1-5 implemented locally with scoped qualification; Task 6 local closure is recorded. Public status: candidate for native qualification on isolated capture branch `go-guided-native-capture-20261114` (base `6b64f6c`), not merged and not fully accepted; no remote CI result exists yet. The authorized scope is isolated-branch commits/push and a narrow Windows capture CI only, not merge, release or TS deletion. Stale unmerged-status wording for PRs #226-#231 was corrected using verified merges and hosted run references. The final completed independent whole-branch review found no Critical or Important defect and judged both spec compliance and quality implementation-ready, not fully accepted. Task 6 checkboxes record bounded local execution and retained delivery only; all unrun native/hosted/permanent-graph requirements remain open.

Scoped evidence (Darwin only):
- Tasks 1-4 independently reviewed; unit and process tests pass.
- Strict frozen Node 22.22.2 versus Go: 29 cases, 75 steps, 5 groups, complete streams, argv and trees; `.git` compared through a semantic index view; permanent captures omit `.git` explicitly.
- Historical oracle corpus (dated; superseded as active POSIX corpus, snapshots retained): 24 FULL plus 5 PENDING localization declarations; recaptures at two depths stable.
- Current POSIX corpus (Task 7, fix 1): 29 FULL, 0 PENDING, 75 steps. Node-free Go replay 29 PASS, 0 skips, parent-confirmed; 12 typed-digest mutation rejections and 13 generator controls reviewed and addressed. Darwin only.
- Task 8 Stage A (native Windows capture): workflow, runner and harness prepared, under independent review; POSIX recapture twice identical, selftests 13/13, Windows cross-vet compile-only. No native or remote CI result. I3 (caps, modes, paths, executables) OPEN; real frozen-Node captures on Windows AMD64 and ARM64 are needed before any Go-only native pass, and missing native baselines fail, not skip.
- The counts below predate Task 7/8 and are historical, not a fresh full-suite claim.
- Frozen orchestration projection: 26 cases over scripted dependencies with tamper controls and a behavioral mutation (RED, then GREEN). Not end-to-end or native proof; excludes fresh intake.
- Native diagnostic supplement: 19 children (15 errors, 3 success projections, 1 blank-HEAD detail); narrow literal gate plus four native commands pass. Not the full unnarrowed hosted positional gate.
- Final ordinary full-tree sweep covered all 62 packages in three bounded groups: 54 package passes and eight with no test files; 2,431 passing test/subtest events, zero failures, four existing skips (three opt-in Node checks and one filesystem capability check). Fresh complete tagged suites for seven affected packages had 1,247 passing events, zero failures/skips. Final guided corpus/native/literal controls had 114 passing events, zero failures/skips, including all four native diagnostic commands. Tagged vet passed.
- Extra opt-in existing Node controls passed Number and V8-sort checks; the echo control failed before launch because baseline `oracle.Start` requires absent `pnpm`. That failed aggregate run is retained separately, not counted as a pass or suppressed through a new skip, shim or dependency change.
- Fresh exact-current-source builds compiled for all six `CGO_ENABLED=0` targets; not native execution and not static-musl-plus-cgo release evidence.
- Actual-CLI second SIGINT passed and was independently approved on Darwin only: partial POST/100-Continue body barrier, first SIGINT closes/drains, second takes default death, exact streams and no record mutation; focused 1.25 s, process 3.3 s, race 9.1 s, vet and Windows compile-vet pass. Disabling second Notify in a built-CLI overlay timed out at 31.8 s as expected. Native Windows/Linux remain unrun.
- Final independent whole-branch spec/quality review found no Critical or Important defect. Earlier Opus/Fable attempts failed API500 before verdict and are not approvals. The known permanent-intercept cleanup window and narrow buffered-signal handoff/disposition limits remain disclosed, not waived.
- Two local shared-code fixes (review pre-action humanization; cancellation error constructor and note sink placement) keep parity and strict coverage. They are not approved differences or waivers.

OPEN gates (unchecked): native Docker proof (fake-box cases are not proof); native Windows and Linux execution, Windows mode semantics and platform signal behavior including inherited-ignored dispositions; full unnarrowed hosted gate; native Windows corpus capture and import (I3). The Darwin-only second-SIGINT approval and completed whole-branch review do not close these gates or establish a full signal cutover. No publication, merge, release, oracle retirement or security-clearance claim; the Zod advisory caveat remains.

Process and evidence history (retained): early test expectation and compile mistakes; shared-package RED collisions; literal coverage-gate failures fixed without waivers; ENOSPC and resource holds; an original worker deleted temporary evidence without authorization (disclosed; only logs and raw snapshots for affected runs survive, so invalidated runs were replaced by fresh retained runs and not relabeled as originals); a user-authorized one-time Go build-cache cleanup freed about 36 GiB (no modules, fuzz data, source or evidence); serial low-concurrency recovery afterward. The detailed ledger is local, git-ignored and not published.

## Continued execution — user direction 2026-11-14

The user explicitly requested continued execution through the remaining work, not another local-checkpoint stop. The earlier local closure is historical, not completion of Task 5 or Slice 5. Existing outward-action/protected-source/release/deletion boundaries remain; continue available local implementation without routine design prompts.

### Task 7: Full permanent guided graph replay

**Files:** extend `difftest/gen/gen-guided-investigate.mjs`, `difftest/guided_live_corpus_test.go`, `difftest/guided_live_test.go` only as needed for shared fixture helpers; add `difftest/guided_permanent_replay_test.go`; update independently recaptured `difftest/testdata/guided-investigate/oracle/` metadata and captures. No runtime or protected source changes in this task.

**Consumes:** existing 29 cases/75 steps, 24 FULL captures, five explicit PENDING localization declarations, exact same-slot live harness and Node-derived unsigned draft templates.

**Produces:** all 29 full permanent expectations, including the five localization graphs, and executable Go-only replay of every case without a Node runtime, frozen dist or pnpm. Scripted fakebox execution is parity evidence, never native Docker proof.

- [ ] **1. RED:** add assertions that all 29 captures are FULL and all named digest/template dependencies resolve from independently captured preimages; add the Go-only replay entry point. Demonstrate the current five pending declarations fail behaviorally. Retain RED logs.
- [ ] **2. Extend frozen capture:** discover every path-dependent digest through explicit supported record/artifact rules; derive expected preimages only from actual frozen Node records, validate each original SHA, and retain bounded dependency-graph templates across steps. Do not replace arbitrary hex strings, drop fields, mask digests or call Go for expected values. Detect missing, cyclic, duplicate and tampered dependencies. Preserve previous corpus/evidence before promotion.
- [ ] **3. Complete Node-free execution:** reuse owned fixture/process/HTTP/environment primitives to run actual built Go CLI for all 29 cases. Prepare seeded records from independently captured/reified data or existing real Go APIs without Node; never derive expected output from the run under test. Compare complete streams, exit/signal, HTTP actions, external argv and persistent tree bytes/kind/mode/digests under the already documented exclusions. Existing default failure-retention guarantees stay intact.
- [ ] **4. GREEN and sensitivity:** pinned Node22.22.2 recapture into two new independent/depth-varying roots; prove normalized capture stability, all29 FULL and exact pins. Run Go-only replay with Node and pnpm unavailable, targeted template/tamper controls, then strict live comparison for all five changed continuation cases. Mutate a graph expectation/dependency through an isolated overlay/copy and demonstrate real rejection, then GREEN without mutation. Serialize heavy Go/capture work and retain every failed attempt.
- [ ] **5. Independent spec and quality review:** include generator, template graph validation, real Go-only execution, fixture provenance, exact comparison and retention. Fix/re-review before marking this local graph gap closed. Do not close native/hosted gates by inference.

### Follow-on execution queue

After Task 7, address native-proof/portability qualification using available owned environments, then continue separately specified 5h tutorial/demo, 5i compatibility, 5j Node-free behavior and 5k candidate packaging. Prepare local evidence and exact prerequisite requirements even where Docker/native hosts are unavailable. 5l protected cutover/retirement, release publication and new remote integration require their existing explicit authority; do not silently treat generic completion wording as authorization for deletion or protected-path changes.

### Task 8: Authorized isolated native Windows capture CI (Task 7 portability fix)

The user explicitly approved committing/pushing an isolated capture branch and adding narrowly scoped native Windows capture/qualification CI. Current capture branch is `go-guided-native-capture-20261114`; HEAD/base remains6b64f6c until reviewed commits. This specific exception permits new `.github/workflows/guided-oracle-capture.yml`, not edits to existing Go/Verify workflows, skip/shard guards, frozen TS/manifests/lock, merge/release/deletion or unrelated5h publication.

- [ ] **1. Prepare capture-only branch workflow:** two genuine Windows AMD64/ARM64 runners; exact pinned existing actions, Go1.27.4, Node22.22.2, frozen-lockfile build and source/pin/native-architecture checks. Only capture-branch push trigger; read-only token, no secrets/repository writes; no full PR matrix merely to capture expectations.
- [ ] **2. Native capture capability:** all29 frozen Node cases at two fresh roots per target, complete outputs/actions/argv/tree and validated preimages; actual Windows modes/path/executable forms, portable real console/signal control. Preserve POSIX corpus unchanged; do not fabricate Windows data, normalize away differences or add skips. Explicit measured platform identity and exact corpus selection reject foreign/missing expectations.
- [ ] **3. Validate/review stage A:** retain local checks, generator positive/negative controls and source snapshots; independent scoped spec/quality/security review of workflow and harness before controller-only scoped stage/commit/push. No native pass inferred from prepared YAML.
- [ ] **4. Run/retrieve actual CI:** controller verifies authorized author/committer, normal secret-scanning hook, Mizore66 GH/SSH identity and exact origin; push only isolated capture branch, stop on push failure. Monitor all terminal workflow states. Retain successful/failed narrow artifacts and authenticate their run/commit/source/runtime provenance before importing from an isolated download directory.
- [ ] **5. Import/qualify native expectations:** independently review actual Windows captures, select per target without fallback masks, run actual Go-only replay/comparator controls and guided process/signal tests on the native runners. Native Docker/Linux/full-hosted acceptance remain separate until executed. Re-review I3 and new fix breakage; then update precise status and continue5h implementation while immutable CI work proceeds when ownership permits.
