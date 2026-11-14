# Go guided investigation — Slice 5g design

- Date: 2026-11-13
- Base: PR #231 normal merge `6b64f6c9e52dce71c3001df0614379ac2457c901`.
- Status: design selected under the user's explicit standing permission to proceed without design/spec approval prompts; implementation and hosted acceptance are not yet complete.
- Local status (updated 2026-11-14): candidate for native qualification on isolated capture branch `go-guided-native-capture-20261114` (base `6b64f6c`); not merged, not fully accepted, no remote CI result yet. Active POSIX corpus is 29 FULL / 0 PENDING with a passing Node-free Go replay (Darwin); the older 24 + 5 corpus is history. Final independent whole-branch spec/quality review found no Critical or Important defect; the actual-CLI second-SIGINT case passed and was independently approved on Darwin only. Mandatory gates remain open: native Docker, native Windows/Linux and platform signal behavior, full unnarrowed hosted gate and native Windows corpus capture (Stage A prepared, under review, no native result). Known narrow signal-parity limits remain disclosed; this is implementation readiness, not full acceptance. See the plan's qualification appendix.
- Frozen authority: `ce99a793af0b020bf1b9cf21114f8d7897b37207`, especially `src/guided-investigate.ts` and `src/cli-app.ts:1953–2043`. Protected TypeScript remains unchanged. Oracle runtime Node 22.22.2.

## 1. Goal and scope

Port `fl investigate --ci-log <file>` and `fl investigate --resume <id>` as the frozen local human-reviewed workflow: preflight → intake/resume → human freeze gate → runtime selection → existing incident continuation → verified proof export when genuinely established. Preserve complete stdout/stderr, exit status, filesystem effects, immutable bindings, and observable error/order/clock behavior. No model/provider call belongs to this flow.

This is one reviewable subproject with separately implemented intake, freeze-wait, orchestration, CLI lifecycle, and qualification layers. Existing witness review, incident continuation, Docker sandbox, Git investigation and bundle writing remain their current owners. The new package is `internal/guided`: `proofview` imports `incident`, so putting orchestration that imports proofview in incident would introduce a cycle.

Out of scope: tutorial/demo (5h), new report flags, new command validation, automatic approval/freezing, provider integration, arbitrary shell fallback, new store/checkpoint formats, changed proof/security semantics, Node-check removal/hint/hook cutover (5j), dependency/workflow changes, release publication, protected source/oracle deletion, and unrelated refactoring. No public release or future merge is authorized by this spec.

## 2. Design selection

Three approaches were considered: embed everything in CLI (too coupled and hard to test ordering); add guided composition to incident (creates an import cycle with the review server); or introduce a small orchestration package over existing incident/witness/proofview APIs. Select the third. The CLI owns flag parsing, session/config inheritance, streams and one-shot SIGINT lifecycle. Guided owns the state machine and a signal consumed only by its freeze waiter. Existing modules retain all canonical serialization, digest verification and proof export.

No behavior improvements are silently applied. In particular, comments/readiness text describing separate approval/freeze clicks do not override the actual atomic default review UI. Session-binding quirks and late failures remain observable compatibility requirements.

## 3. CLI contract and precedence

Use existing first-occurrence exact `option` semantics: a following empty string or another flag is a value; a trailing flag has no value; equals spelling is not parsed. Guided routing is selected when either ci-log or resume has a following token. Preserve this order:

1. Reject a leading `git` or `turns` combined with guided mode.
2. Reject simultaneously supplied ci-log and resume values.
3. Resolve repository from `--repo` or cwd, not Git-root discovery.
4. Require paired definedness of from/to, even on resume where their values are ignored.
5. Choose wrapper session ID `--id ?? resumeId`; resolve/read session facts if defined, even when explicit digest/ledger overrides exist. Malformed stored bindings must still fail.
6. Resolve image from explicit `--image`, else top-level config `image`; retain existing config/error behavior.
7. Coerce optional max-states/port with JS Number semantics; preserve absent versus supplied-empty values. Resolve explicit stores against cwd.
8. Install a one-shot SIGINT handler immediately before invoking guided. Its wrapper try/finally scope includes the guided call, post-result binding writes, JSON serialization/output and exit-code assignment; remove any still-registered handler only when that entire scope returns or throws.

On resume, `--id` changes session lookup/post-result binding identity but not which draft `--resume` loads. Do not repair this. Guided ignores command-file, timeout-seconds, runtime-mapping, latest, minimize, two-step, tty, browser and json flags. Existing git/turns routes and diagnostics remain unchanged.

After guided returns, write a session binding only when wrapper incident ID and the previously resolved expected digest both exist (including a PREFLIGHT_BLOCKED result). Resolve optional ledger path then. The frozen wrapper's alternative top-level `frozenDigest` branch never fires for guided results, which contain `retainedFrozenDigest`; do not add automatic binding of a newly observed digest. Propagated exceptions/rejections, including waiter cancellation rejection, prevent this post-result write. Merely aborting the signal during another phase or before an already-frozen fast path does not: if guided otherwise returns, post-result work still runs. Do not add a wrapper ctx.Err() check that suppresses it. Auto-generated IDs do not imply wrapper binding writes.

## 4. State machine and persistence

### 4.1 PREFLIGHT

Resolve repository/stores, emit PREFLIGHT, run existing doctor once and retain its complete report and proof-ready snapshot. Only Node readiness blocks immediately, matching `doctor.CliExitCode`. A blocked result is ordered exactly as Section 6 with null draft/path/review/digest/continuation, `nextCommands` equal to `["Install Node.js 22+", "fl doctor --repo ."]` and the exact frozen note. Do not remove Node dependency in 5g. Missing Git, Docker or cleanliness may permit intake/review and fail later; do not rerun doctor after intake mutates the workspace.

### 4.2 INTAKE / resume

Fresh intake:

- Validate paired from/to before reading the log at the library boundary too.
- Read the resolved log with existing Node UTF-8 semantics; add no file-kind, ancestor/symlink, or byte-limit restriction.
- Trim command with JS whitespace semantics; missing/blank command uses exactly `node -e "console.log('Replace this command with the failing CI predicate after human review'); process.exit(1)"`.
- ID is supplied incident ID or `guided-` plus base-36 Date.now(), consuming the same seeded clock calls as TS. The reachable guided entry uses timeout 300; CLI timeout-seconds is not forwarded.
- Resolve a supplied runtime alias locally before draft creation, even when an image exists. Never pull an image.
- Read local Git HEAD/parents even with an explicit range. Use exactly the intake safe-config list, minimal environment, hidden shell-free sync child, default buffering and no added process timeout from `guided-investigate.ts:16–35,117–147`. Preserve its two distinct exact failure diagnostics rather than incident-start's different wording.
- Explicit range applies only when both strings are truthy; otherwise use exactly one local HEAD parent. Preserve existing root/merge/equal-range refusals.
- Create preliminary draft, persist a HUMAN proposal, recreate draft with proposal binding, then write the immutable draft. Preserve proposal-before-draft partial effects.

Packet construction: split log by CRLF/LF, JS-trim/filter lines, choose last `/fail|error|expected|assert/i` match, else last nonblank line, else frozen fallback; slice symptom to 500 UTF-16 units. Packet log is first 4000 UTF-16 units of the original untrimmed log. Preserve lone-surrogate behavior. Language is Unknown; exact range summary, behavior, empty overlays and network-disabled/credentials-redacted policy match TS. This boundary does not scrub log/command secrets; policy text is not a redaction claim. Empty log still fails the existing packet schema.

Resume emits `INTAKE (resume <id>)`, reads/verifies the existing draft, and requires resolved draft.repository to equal resolved requested repository. No reread of log/HEAD/command, range replacement, or draft update. Draft stays PENDING_HUMAN_REVIEW/NOT_FROZEN after human freeze; progress is reconstructed from immutable records, not a new checkpoint file.

Stores remain existing formats: draft at `<draftStore>/drafts/<id>.json`; witness proposals/approvals/frozen records in their existing directories; mutable session binding in `<draftStore>/bindings/<id>.json`; existing file/directory modes, write-once enforcement and canonical digests. Defaults are repository `.faultline/incidents` / `.faultline/witnesses`.

### 4.3 WITNESS_REVIEW and waiter

Emit WITNESS_REVIEW and verify the entire chain with optional expected digest. Valid frozen chain bypasses server/port use and retains supplied/observed digest, reviewUrl null. Otherwise enter waiting only if any verification error matches `missing|not been frozen` case-insensitively; this deliberately includes certain missing-companion corruption cases. Other invalid states throw the exact frozen aggregate diagnostic.

Waiter independently checks an existing freeze before server startup. Otherwise reuse `proofview.StartWitnessReviewServer`, default port0, default atomic UI; pass TwoStep only when `FAULTLINE_WITNESS_TWO_STEP=1`, not the ignored CLI two-step flag. Emit readiness callback; immediately poll, then every500ms, no overall timeout. Invalid observations retry. A canceled signal rejects `Guided investigate cancelled before freeze.`; check signal before each poll and during sleep. Initial valid-freeze fast path ignores an already canceled signal. Close/drain server when leaving the polling try/finally-equivalent region; close failure replaces the pending return/error as in JS finally. Preserve callback/startup error ordering. In particular, OnReviewReady runs before that cleanup region: if it throws, the frozen waiter rejects without closing the already-started server. Pin this through a controlled server seam and explicitly owned test cleanup; do not move defer immediately after startup and silently alter this behavior.

Human actions alone approve/freeze. No browser, stdin/readline, implicit consent, queued execution, or default approval. After return, require supplied expected digest to equal observed digest, then reverify chain against retained digest and require external MATCH.

### 4.4 RUNTIME_SELECTION → LOCALIZATION → PROOF_EXPORT

Emit RUNTIME_SELECTION. Image precedence: immutable draft runtime (refuse any differing supplied/config image), supplied/config image, resolve runtime alias, otherwise exact missing-runtime error. Image resolution precedes the late Docker-ready check. The doctor snapshot must be READY for native CLI continuation; a controlled injected runner can bypass this but remains INJECTED_RUNNER and cannot yield proof.

Emit LOCALIZATION and call existing `incident.Continue` with retained digest, draft, repository, witness store, selected image, optional ledger/max-states/output and explicit injected-runner classification. Do not forward guided runtime-mapping. Existing continuation owns full draft/packet/command/proposal binding, ledger verification, native sandbox, homogeneous-environment rules, localization, managed proof root, write, and independent bundle/root verification. Preserve repository-relative proof root `.faultline/git-proof-bundles` and existing names.

Emit PROOF_EXPORT only after continuation returns PROOF_BUNDLE_READY. Nonproof appends the exact frozen resume command including observed digest/image; proof retains continuation next commands. No auto-serving/browser.

## 5. Architecture and test seams

- `internal/guided/intake.go`: intake options/result plus exact log/symptom/HEAD/draft/proposal composition. Private dependency seams permit deterministic runtime/HEAD/failure-order tests; normal defaults are real existing adapters. No provider dependency.
- `internal/guided/freeze.go`: freeze options/result, optional review port/callback/signal/poll interval, real review-server adapter and deterministic private poll/server seams for failure-order tests.
- `internal/guided/guided.go`: Run options, ordered jsjson result and phase callbacks; private doctor/runtime/intake/wait/continuation seams for ordering without exporting production endpoint overrides.
- `internal/cli/investigate_guided.go` and narrow lifecycle helpers: wrapper behavior, complete streams/exit and one-shot SIGINT handling; minimal existing dispatch edit.
- Additive guided permanent fixtures, executable diagnostic replay, pinned frozen-TS live/process oracle and coverage integration. Reuse existing portable process ownership instead of ad-hoc Windows console launching.

Use Go1.27 stdlib only; `jsjson`/`jsstr`, existing clock, nodefs/nodeproc, canonical/witness/incident APIs. Pointers distinguish absent vs empty options. Cancellation context is a freeze-wait signal, NOT cancellation of doctor/Git/runtime/continuation. Any helper types are internal application interfaces, not new CLI behavior.

## 6. Output and lifecycle

Phase stderr bytes: `FaultLine guided investigate: PHASE (detail)\n`, omitting parentheses when detail absent. Readiness bytes exactly:

`FaultLine local witness review: <url>\nApprove, then Freeze (separate clicks). This command continues automatically after freeze. Press Ctrl+C to cancel and resume later with --resume <id>.\n`

Retain that historical text even though the default UI is atomic. Result is two-space JS JSON plus one newline, ordered `status, doctor, proofReady, draft, draftPath, reviewUrl, retainedFrozenDigest, continuation, nextCommands, note`. Exact notes and next-command strings are frozen-source literals. Only GUIDED_PROOF_BUNDLE_READY exits0; blocked/nonproof exits1. Throwing produces no result JSON, retains prior phase lines, and uses current standard failure text including `Next: pnpm fl help`.

SIGINT handler is installed once, consumed on first interrupt, and only aborts the freeze-wait signal. An interrupt during other phases does not cancel their work; later waiter may observe it, while an already-frozen path can continue. A second SIGINT after consumption restores ordinary platform disposition. SIGTERM keeps its default behavior. Do not reuse the exit0 review waiter or judge-demo dual-signal lifecycle. No stop summary.

## 7. Verification and delivery

Every layer is test-first with independent spec/quality review. Intake and waiter may execute in parallel with disjoint source/test ownership; orchestration waits for both; CLI waits for orchestration; shared corpus/gates have one owner. Fix/re-review before advancing; final whole-branch review before delivery.

Required evidence:

1. Exact direct tests for Section3 precedence and Sections4–6 phases/results/errors/partial writes. Include UTF-16 boundary slicing, JS whitespace, empty/supplied flags, generated-ID clock order, bad runtime/HEAD/draft writes, no provider calls, and immutable digest checks.
2. Real review HTTP actions for atomic/environment-two-step modes, existing-freeze port bypass, corrupt/missing chain distinction, signal-controlled wait/cancellation and server drain. No sleeps used as synchronization assumptions; test barriers observe real readiness/state.
3. Real built CLI process interruption/resume: retained files, first/second SIGINT, default SIGTERM, no stdin/browser approval. Use platform-native process helper and bounded cleanup; no platform skip added to avoid failure.
4. Additive frozen Node22.22.2 oracle comparisons of complete streams, exit, relevant external argv, complete workspace tree/modes and digests; normalize only independently generated owned roots and actual ephemeral addresses, with explicit proof of any clock normalization. Permanent replay remains executable after oracle retirement. Expected output comes from TS, not Go output. Verify new literal and positional coverage with real emitted counters, including hermetic subprocess GOCOVERDIR propagation.
5. Native Docker proof-ready acceptance using safe owned fixture repositories when available; injected/fake observation runs remain nonproof and cannot substitute for portable proof. Six-target native hosted suites, full unnarrowed Ubuntu coverage, golden no-drift and final gates remain required. Cross-compilation is compilation only. Every local shell command below five minutes; split costly suites into bounded, retained windows without claiming a window is the full gate.

Permanent orchestration projection may additionally capture the unchanged frozen guided function body over explicitly scripted dependency modules, pinning exact result/error, phases and forwarded options. A generic Go dependency adapter replays the same fixed scripts without Node. This is independent composition evidence only: it does not replace full live command/tree comparisons, portable intake/store templates, existing continuation/writer byte goldens, or actual native proof qualification. Cases whose permanent end-to-end bundle graph cannot yet be reified remain explicitly pending; no retirement/completion claim may silently erase that gap.

No new waiver, skip, arbitrary output mask, oracle rewrite or expected-byte substitution to force parity. Workflow/protected CI metadata edits need separate explicit authorization if new corpus integration actually requires them. Retain all failed/incomplete attempts and do not count retries/skips as original passes.

Post-merge main CI for prerequisite PR231 is monitored independently and must be accepted before successor publication. At local completion, record accurate differences/status without claiming all Slice5 complete. Future external publication/integration and protected-path changes retain their explicit authority boundaries.
