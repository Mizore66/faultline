# Native Demo RERUN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute the reviewed refund Go model in real native child processes and return a fully checked RERUN analysis with inspectable, explicitly non-attesting execution provenance.

**Architecture:** `internal/nativefixture` owns fixed configuration edits, the evaluator, private-child dispatch, and bounded self-execution. Additive `internal/demofixture` files own approval, witness/run identity, orchestration, and the returned provenance envelope. The frozen TypeScript engine supplies an independently captured orchestration reference; its output is never represented as evidence that Go or the original JavaScript executed.

**Tech Stack:** Go 1.27 (observed 1.27.3), standard library, existing ordered `jsjson`/`canonical`/`clock`/`witnessresult`; test-only pinned Node 22.22.2 and the existing TypeScript compiler.

**Spec:** `docs/superpowers/specs/2026-11-03-go-native-demo-rerun-design.md`

## Global Constraints

- Preserve `CreateFrozenWitness`, `historicalEnvironment`, `replayRun`, `CreateReplayAnalysis`, existing golden corpora, and ordinary CLI behavior.
- No edits to `src/`, `package.json`, or `pnpm-lock.yaml`.
- Keep `internal/nodeproc`, existing bundle writer/verifier, existing renderer, protected source, workflows/gates, and old corpora unchanged.
- Production standard library and existing internal packages only; production cannot require Node, a Go compiler, TS/dist, testdata, or a package manifest.
- Never serialize a digest preimage with `encoding/json`; it is allowed only for strict native configuration input decoding.
- Preserve the logical 20 states, hunk interaction, metrics, normal 39-invocation order, three three-per-side boundaries, six minimization attempts, and three prevention runs.
- PASS/FAIL child stdout/stderr/status remain exact historical executable-witness bytes; use structured predicate reason codes, not cached REPLAY reason codes.
- Child deadline two seconds; WaitDelay 250 ms; combined stream ceiling 1 MiB; config ceiling 16 KiB; whole-analysis deadline 90 seconds bounded by the caller; no retries.
- No timestamp sleeps/spins/bumps, tracee load alignment, production entropy override, masks, accept-either comparisons, or gate/tolerance weakening.
- Cross-build all six targets as compilation evidence only; actual native execution is required on Linux/macOS/Windows amd64 and arm64 before platform qualification is complete.
- All controller shell/build/network/evidence work remains serialized through the reviewed W/run.py with a maximum 240-second aggregate budget per invocation.
- No checkout transition, commit, push, PR, merge, unfreeze, installation, release, retirement, or historical cleanup is authorized by this plan. Keep all hook and personal-account requirements intact.

## Execution ownership and checkpoints

Work in place at `/Users/anasqumhiyeh/Documents/faultline` (R), current committed HEAD `0afacfe9d8f6f5cce562abfe857135db3615a4c9`. Do not create a worktree or change branches. W is `R/.superpowers/sdd/2026-11-02-go-slice5b2-interactive-renderer`. Read W/progress.md's LIVE OWNERSHIP before runtime/evidence operations. Dedicated Read/Write/Edit authoring is allowed; controller persistence and all runtime use W/run.py. Never access the three held judge-preview files listed in the dispatch.

Every command below is a **payload**, not permission to bypass the controller wrapper:

```sh
python3 -I /Users/anasqumhiyeh/Documents/faultline/.superpowers/sdd/2026-11-02-go-slice5b2-interactive-renderer/run.py --name native-UNIQUE-STAGE --seconds 240 --cwd /Users/anasqumhiyeh/Documents/faultline -- COMMAND
```

Use a distinct descriptive name for each invocation, at most 240 seconds including aggregate subprocesses. Short stages use shorter budgets. Large output is not semantically accepted until consumed with an escaped, source-bound index and any needed exact queries. Preserve every failure and incomplete attempt. Do not chain an over-budget broad suite into watchdog-reset fragments or detach it. If a required broad command cannot finish within the bound, retain the failure and report that gate unqualified; narrower successes do not replace it.

The ongoing actual-main CI qualification targets merge `db37e8694c88a9bdff5f4000152be6e7f89c71e4`, not these uncommitted changes. Its collector currently permits only the two native docs as dirty. Before any other repository edit, establish a separate retained local-scope manifest for the exact paths in this plan and explicitly adapt that collector's dirty-file assertion to that manifest. Preserve the previous collector, exact local committed HEAD, exact remote/account/API merge/parents/tree/run/event/main-branch assertions, and exact workflow-byte checks. Log local status and hashes; never describe a dirty tree as clean. This is an evidence-collection scope adjustment, not a change to acceptance gates. If that cannot be safely completed, pause the existing main scheduler with the blocker recorded; do not silently disable checks or start another observer.

Use inline execution to retain serialized runtime ownership. Read-only Sonnet review is permitted at meaningful task gates, but no worker shell/interpreter/network/evidence writer or subagent dispatch is authorized. A task's checkpoint replaces the generic skill's commit step: inspect diff, preserve RED/GREEN receipts, review scope, update the ledger, leave changes uncommitted.

## File map

| Files | Responsibility |
| --- | --- |
| `difftest/gen/gen-native-demo.mjs`, `native-demo-reference.mjs`, `native-demo-reference.test.mjs` | Frozen-engine loader, strict call transcript, independent native contract/ID/envelope constructor, capture/recapture/check |
| `difftest/testdata/native-demo/{baseline.json,inputs.jsonl,frozen-reference.jsonl,native-contract.jsonl}`, `README.md` | New immutable full-byte reference corpus and reproducibility instructions; no existing corpus changes |
| `internal/nativefixture/program.go`, `child.go`, corresponding `_test.go` | Fixed materialization edits, strict config decoder, actual refund evaluator/predicate, exact child protocol |
| `internal/nativefixture/process.go`, `process_signal_unix.go`, `process_signal_windows.go`, `provenance.go`, corresponding `_test.go` | Executable/environment observation, bounded exec, owned cleanup, raw capture and ordered byte-envelope helpers |
| `internal/clock/evidence.go`, `evidence_test.go`; comment only in `clock.go` | Separate unseeded elapsed-evidence timer; retain `Monotonic` output prohibition |
| `internal/demofixture/native_witness.go`, `native_run.go`, corresponding `_test.go` | Proposal/approval, final witness, identity and receipt construction, strict result validation |
| `internal/demofixture/native_analysis.go`, `native_analysis_test.go`, `native_reference_test.go`, `native_child_test.go` | Additive 39-call orchestration, proof checks, complete fixture/reference comparisons, test-binary child bootstrap |
| `internal/demofixture/fixture.go:1-4` | Package-description-only correction acknowledging additive native execution |
| `cmd/fl/native_sample.go`, `native_sample_test.go`; `cmd/fl/main.go:9` | Exact private bootstrap before `nodeArgv`, without user-facing RERUN/HTTP integration |
| `difftest/KNOWN_DIFFERENCES.md` | Exhaustive native-only contract differences and non-attestation limits |

No new public CLI command, writable handler, generic runner, export-bundle integration, or acceptance workflow is part of the file map.

## Fixed wire contract (read with every task)

All JSON objects below use the displayed insertion order and `jsjson` stringification. Complete fixture files are indented two spaces plus one final newline; JSONL rows are compact plus one newline. SHA-256 strings use `sha256:` plus lowercase hexadecimal. Canonical JSON hashing uses existing `canonical.DigestJSON` semantics, not insertion-order stringify hashing. Raw blobs hash original bytes.

### Program, proposal, and native witness

`Program()` returns a fresh ordered descriptor with keys:

1. `schemaVersion: "faultline.native-demo-program.v1"`, `runner: "native-go-config-evaluator-v1"`.
2. `configFile: "native-config.json"`, `maxConfigBytes: 16384`, `fields: {displayDefault:["ORDER_CURRENCY","EUR"],settlementSource:["ORDER_CURRENCY","DISPLAY_CURRENCY"]}`.
3. `input: {order:{currency:"USD"},refund:{}}`, `predicate: "result.settlementCurrency === order.currency"`.
4. `baseConfig` and `edits` below; `stdoutPass`, `stdoutFail`, `stderrFail`, and `exitCodes:{pass:0,fail:1,configError:3}`.

The descriptor includes only fixed reviewed semantics, never a state ID/verdict table or a particular executable file digest. Its digest is separately bound in policy/provenance. Exact base bytes:

```json
{"displayDefault":"ORDER_CURRENCY","settlementSource":"ORDER_CURRENCY"}
```

There is one trailing LF. `edits` is an array of `{hunk,from,to}` in source, renderer, partial order:

| hunk | from | to |
| --- | --- | --- |
| `settlement-display-default` | `"displayDefault":"ORDER_CURRENCY"` | `"displayDefault":"EUR"` |
| `settlement-display-boundary` | `"settlementSource":"ORDER_CURRENCY"` | `"settlementSource":"DISPLAY_CURRENCY"` |
| `partial-renderer-hunk` | `"settlementSource":"ORDER_CURRENCY"` | `"settlementSource":` |

Allow the four subsets of source/renderer, either input order; partial is allowed alone. Reject duplicate/unknown hunks and any partial combination. Apply canonical edit order and require exactly one matching edit site. Preserve logical state tree digests unchanged.

The native incident packet has exactly these ordered keys/values:

```json
{
  "symptom": "A completed refund can overwrite settlement currency with display currency.",
  "referenceFixture": "parallel-settlement-regression (frozen JavaScript demo)",
  "model": "Reviewed Go refund model under native JSON configuration",
  "scope": "Built-in DEMO_SAMPLE only; not external repository execution."
}
```

`NativeWitnessProposal()` returns `{schemaVersion,program,programDigest,incidentPacket,witnessTemplate,digest}`. `schemaVersion` is `faultline.native-witness-proposal.v1`; `digest` hashes all preceding keys. `witnessTemplate` has existing Witness keys in existing order, except `approvedAt` and `digest` are absent. Its ID is `refund-settlement-currency-go-model`, version 1, unchanged behavior sentence, command `fl --internal-native-demo-witness-v1`, overlay `["native-config.json"]`, `proposedBeforeLocalization:true`, and the native incident digest. The human-readable command describes the actual fixed helper; receipts retain the actual executable path and argv, including a possible `.exe` suffix.

Policy keys retain existing order and schema enums: `network:"disabled"`, `credentials:"redacted"`, `runner:"built-in-sample"`, `timeoutSeconds:2`, then note `Only the reviewed Go model is executed. Program descriptor DIGEST. No network operations or inherited credentials; not an OS sandbox.` Here DIGEST is the actual descriptor digest, not a literal placeholder in implementation or expected fixtures. No native file/config digest is a state-tree digest or per-state witness digest.

Build final Witness in the historical key order, inserting caller `approvedAt` between `proposedBeforeLocalization` and `incidentPacketDigest`, and appending `digest`. Approval must be present, match the complete proposal digest, parse as RFC3339, equal `clock.ISO(parsed)` (UTC with exactly milliseconds), and be no later than the single wall timestamp taken at API entry. Reject before constructing an executor/spawning. The caller records real authorization; this check does not authenticate a person. The supplied timestamp is not replaced or inferred. Equal/backward wall clocks later in execution are allowed; do not demand sorted wall time.

### Capture, run ID, and envelope

Environment preimage order is `{schemaVersion,runner,runtime,goos,goarch,executableSHA256,environment,network,credentials,note}`. Schema is `faultline.native-demo-environment.v1`; runtime is actual `runtime.Version()`, platform is actual `runtime.GOOS/GOARCH`, `environment` is the actual effective ordered `KEY=value` array from `exec.Cmd.Environ()`. Network is `disabled`, credentials is `not-inherited`, note is `Reviewed fixed program behavior; not OS isolation or remote attestation.` Observe executable SHA-256 before execution, not a claim against binary replacement. Freeze this preimage for comparable calls; reject changed effective environment before Start.

Allowed effective environment: exactly `FAULTLINE_NATIVE_DEMO_CHILD=v1`, plus required nonempty Windows `SYSTEMROOT`. Use explicit non-nil `Cmd.Env`, not the parent environment; inspect `Cmd.Environ()` for additions, case-normalize/deduplicate Windows keys deterministically and reject any unexpected addition. No PATH, HOME, credentials, or seed variables. Missing required Windows system root fails before spawn. Verify against each actual command, not just an intended environment list.

RunRecord has **only** the existing ordered keys: `id,stateId,witnessDigest,environmentDigest,verdict,reasonCode,stdout,stderr,durationMs,executionKind,executedAt`. There is no new exit-code/state-tree field in RunRecord. `executedAt` is the observed post-Wait wall time. A run-ID preimage is `{schemaVersion,nonce,ordinal,executionAttempt,startedAt,configDigest,status,signal,record}` with schema `faultline.native-demo-run-id.v1`; `record` is the complete RunRecord body without ID, retaining its other key order. Status is normal exit integer or null if no ordinary exit; signal is the actual OS observation or null. The ID is the canonical digest of that entire preimage. Ordinal starts at 1 per analysis; attempt is 0 outside boundary probes and 1,2,3 on each side of a boundary. Nonce comes once from `clock.NewUUID()` in production, never wall-time uniqueness tricks.

`NativeResult` contains `Analysis` and `Provenance`. A failed result has `Analysis` equal to the zero/undefined `jsjson.Value`, not an uncertified partial analysis. Provenance order:

```text
schemaVersion,status,proposal,program,programDigest,approval,
environmentPreimage,environmentDigest,nonce,generatedAt,invocations
```

Schema is `faultline.native-demo-execution.v1`; status is `COMPLETE` or `ERROR`; approval is `{proposalDigest,approvedAt}`. Unknown pre-execution facts are null, never fabricated. `generatedAt` uses the actual final clock wall timestamp, including failure exits where available. Each invocation receipt is ordered:

```text
ordinal,stateId,executionAttempt,config,expectedPartial,executable,argv,cwd,
environment,startedAt,executedAt,durationMs,status,signal,stdout,stderr,
accepted,runId,runRecord,runIdPreimage,failureKind,cleanupError
```

Config/stdout/stderr blobs are `{encoding:"base64",data,sha256,complete}`; `data` is standard padded base64 of retained original bytes. For config and complete streams `complete:true`; overflow's retained stream prefixes are explicitly incomplete. A pre-Start config write failure does not claim the attempted bytes were observed from disk. Retain attempted raw config and the explicit failure kind. `runId`, `runRecord`, and `runIdPreimage` are null for rejected invocations; accepted partial parse runs may have an ERROR verdict but are still accepted executions. Cleanup errors reject that invocation even if its protocol passed. Record ordinal/state for a failed preparation attempt, with unavailable process facts null.

Raw errors use typed errors and stable machine categories, with OS error detail retained separately: `APPROVAL`, `MATERIALIZE`, `ENVIRONMENT`, `PREPARE`, `SPAWN`, `CANCELLED`, `DEADLINE`, `SIGNAL`, `OUTPUT_LIMIT`, `PIPE_DRAIN`, `PROTOCOL`, `UNEXPECTED_PARSE`, `DUPLICATE_ID`, `PROOF`, `CLEANUP`, `PANIC`. Never concatenate error text into accepted stdout/stderr. Preserve primary and cleanup errors via `errors.Join` where both occur.

### Exact native-only analysis copy

| Path | Native value/change |
| --- | --- |
| `fixture.title` | `Parallel settlement-currency regression — Go model` |
| `fixture.summary` | `A reviewed Go model evaluated under materialized configuration, with a real native witness and a two-edit interaction.` |
| `minimization.attempts[3].note` | `The partial native configuration cannot parse and is unresolved, not proof.` |
| `minimization.attempts[4].note` | `Applying the two edits to last-good reproduces the approved native witness failure.` |
| `minimization.attempts[5].note` | `Removing the two edits from first-bad restores the approved native witness pass.` |
| `claims[0].statement` | `The approved native witness passes before and fails after Session Cedar turn 5.` |
| `warning` | `Executed reviewed Go model. This does not execute the original JavaScript or external repository code and is not a general-purpose sandbox.` |

Witness/environment/run IDs/times/streams/native parse reason and the separate envelope differ exactly as above. Fixture ID remains `parallel-settlement-regression`, fidelity remains `DEMO_SAMPLE`; other accurate engine orchestration copy and all historical REPLAY bytes remain unchanged. Accepted native PASS/FAIL reasons are `PREDICATE_PASS`/`PREDICATE_FAIL`. Designated partial parse reason is `FIXTURE_PARSE_ERROR`, verdict ERROR, minimization UNRESOLVED. Proof failure produces no analysis rather than historical unconditional certification.

---

### Task 1: Independent orchestration and native-contract evidence

**Files:** Create the three `difftest/gen/` modules and the new native-demo corpus/README in the file map. Do not modify production Go yet or any old corpus.

**Interfaces:** Test-only ESM exports in `native-demo-reference.mjs`:

```js
// bindings: { sourceCommit, sourceFiles, compiledFiles, nodeVersion, compilerVersion }
export async function frozenAnalysis({ compiledRoot, calls, generatedAt, witness });
export function nativeCopyContract(frozenAnalysisValue);
export async function nativeContract({ proposal, approval, environmentPreimage, nonce, generatedAt, captures });
export function verifyResult({ expected, actualBytes });
```

Each `calls` entry is `{state,executionAttempt,record}` containing the complete logical state object and complete input RunRecord. Each `captures` entry has all raw observations needed for the fixed receipt contract, with byte blobs encoded as base64. `nativeContract` independently constructs proposal/witness/run body/preimages/receipts/envelope from the input descriptor/captures and the documented contract, then calls the frozen engine for analysis assembly. It must validate—not trust—hashes, IDs, state tuples, and execution order. No Go program supplies expected output. `verifyResult` compares the **entire one expected byte string** using `Buffer.equals`; failure prints the first differing offset and fails.

- [ ] **1.1 Bind source/toolchain before capture.** Read the existing incident-renderer generator's source graph/exclusive-capture approach. Verify frozen commit `ce99a793af0b020bf1b9cf21114f8d7897b37207`, all `src/` bytes, and the pinned Node executable `/Users/anasqumhiyeh/.cache/t12-node/x/node-v22.22.2-darwin-arm64/bin/node`; retain compiler version and fresh compiled graph hashes. Compile from the trusted repo, not a downloaded-data cwd. Do not replace Node with floating 22/24 or mutate protected source. All capture targets must be absent; each scratch/capture/recapture directory is fresh and retained.

- [ ] **1.2 Write loader invariant tests first.** Use `node:test` and `assert/strict`. These actual test shapes are required; define the fixture helpers in the test module from the frozen graph and scripted input, not Go output:

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { frozenAnalysis, nativeCopyContract, nativeContract, verifyResult } from './native-demo-reference.mjs';

// The driver supplies explicit absolute test-input and bound-compiled-root paths.
const syntheticInput = () => {
  assert.ok(process.env.NATIVE_REFERENCE_INPUT);
  return JSON.parse(readFileSync(process.env.NATIVE_REFERENCE_INPUT, 'utf8'));
};
const compiledRoot = () => {
  assert.ok(process.env.NATIVE_REFERENCE_COMPILED_ROOT);
  return process.env.NATIVE_REFERENCE_COMPILED_ROOT;
};

test('frozen engine consumes all 39 complete calls', async () => {
  const input = syntheticInput();
  const result = await frozenAnalysis({ compiledRoot: compiledRoot(), ...input.engine });
  assert.equal(result.runCatalog.length, 39);
  assert.equal(result.transitions.length, 3);
  assert.equal(result.minimization.attempts.length, 6);
});
test('missing or extra invocation fails capture', async () => {
  const { engine } = syntheticInput();
  await assert.rejects(() => frozenAnalysis({ compiledRoot: compiledRoot(), ...engine, calls: engine.calls.slice(1) }));
  await assert.rejects(() => frozenAnalysis({ compiledRoot: compiledRoot(), ...engine, calls: [...engine.calls, engine.calls[0]] }));
});
test('full byte comparison rejects a changed execution timestamp', () => {
  const expected = Buffer.from('{"executedAt":"2026-11-03T00:00:00.000Z"}\n');
  assert.throws(() => verifyResult({expected, actualBytes: Buffer.from('{"executedAt":"2026-11-03T00:00:00.001Z"}\n')}));
});
```

The two environment variables above are **test driver inputs only**; production reads neither. The test helper must reject absent values rather than silently skip. The generator supplies them explicitly for its own test subprocess. Add loader tests for wrong state/hunks/tree digest/attempt, witness disagreement, a blocked import, repeated IDs, a changed copy anchor, and inconsistent envelope blob/hash/ID input. No test accepts two different byte strings.

- [ ] **1.3 Observe missing-reference RED.** Payload: pinned Node `--experimental-vm-modules --test difftest/gen/native-demo-reference.test.mjs` with explicit test inputs. Expected missing module/export error before the reference implementation exists. Retain that failed attempt; syntax/toolchain failures are not an acceptable RED substitute. Run generator `check` before capture and retain its explicit missing-corpus failure too.

- [ ] **1.4 Implement and prove the trusted loader.** Use `vm.SourceTextModule` for freshly compiled unchanged `engine.js`, `fixture.js`, and its frozen dependency graph. Only when the importing module is the engine, replace the fixture dependency with a `vm.SyntheticModule` exporting the **real frozen fixture's** values/functions, overriding `executeFixtureState` with strict transcript consumption. The native profile additionally overrides `createFrozenWitness` to return the explicitly supplied native witness; the unadapted profile retains the real frozen witness. Every call asserts complete input state bytes, expected attempt, and witness digest, then returns a fresh clone of the scripted record. Check exhaustion after analysis creation. Do not transform engine source or import candidate Go code. Allow only bound frozen modules and an explicit reviewed list of built-in imports discovered in that graph; reject unbound modules/packages. The frozen fixture may be evaluated for its constants without invoking its child executor. Supply a context-local Date for the single prescribed `generatedAt`; do not monkey-patch the host runtime or candidate clocks. Bind compiled bytes before creating modules.

`nativeCopyContract` changes only the seven exact paths in the copy table, checking each historical value against its frozen literal first; reject unexpected structure/copy. This is a separately named expected-output contract, not a candidate normalizer. The Go output is never passed through it. Independently implement canonical JSON digesting from the documented frozen rules (or the bound frozen canonical module), SHA-256/base64 over raw bytes, RunRecord body/ID preimage order, receipt order, and envelope order. Native inputs are valid ASCII/integer contracts; do not broaden the comparator with Unicode or time masks.

- [ ] **1.5 Capture deterministic full outputs and independently recapture.** `gen-native-demo.mjs` has exact modes `capture --compiled-root ABS --out ABS`, `recapture --compiled-root ABS --out ABS`, `check`, and `verify-live --compiled-root ABS --input ABS --expected-out ABS`. Capture and recapture refuse existing target directories. Retain unadapted frozen analysis, native complete result, entire input transcript, source/runtime/compiler bindings, and complete hashes. JSONL case rows have `{id,input,expected}`; expected is the entire serialized output string, not selected fields. At least cases `ordinary`, `equal-wall-time`, `backward-wall-time` use fixed explicitly synthetic input observations and a canonical UUID nonce. All cases preserve 39 invocations, exact stream contents, and one expected string each. Equal/backward cases test identity construction, not actual clock control. The partial synthetic stderr is labeled as scripted test input, never as an observed Go diagnostic. Later live replay uses the actual decoder stderr unchanged.

The strict ordinary call sequence is 4 contributions; turns 1–8; before/after probes for turns 4/5, 6/7, 7/8, each side attempts 1,2,3; source-only, renderer-only, partial, candidate, reverted, last-good; then prevention last-good, first-bad, repaired. It is 4+8+18+6+3=39. All non-boundary attempts are 0. The descriptor/program does not contain this state/verdict schedule.

Run `check`, independently recapture into another fresh directory, compare every complete expected output byte, and exercise the corruption tests. A loader/import/canonicalization failure blocks this task: diagnose and preserve it; do not substitute hand-produced Go goldens. Checkpoint reference-only diff and document synthetic-versus-live limits before Task 2.

### Task 2: Reviewed config model and exact private child protocol

**Files:** Create `internal/nativefixture/program.go`, `program_test.go`, `child.go`, `child_test.go`. No process runner or analysis yet.

**Interfaces produced:**

```go
const ChildArg = "--internal-native-demo-witness-v1"
const ChildEnvName = "FAULTLINE_NATIVE_DEMO_CHILD"
const ChildEnvValue = "v1"
const ConfigFile = "native-config.json"
const Runner = "native-go-config-evaluator-v1"
type Materialized struct { Data []byte; Partial bool }
func Program() jsjson.Value
func Materialize(hunks []string) (Materialized, error)
func DispatchChild(args []string, marker string, stdout, stderr io.Writer) (code int, handled bool)
```

`args` excludes argv[0]. Dispatch requires exactly one matching argument and the exact marker. `DispatchChild` reads only `ConfigFile` in the current cwd; it has no file/executable/repository selector. Production wrapper supplies `os.Args[1:]` and `os.Getenv(ChildEnvName)`. `Program()` and `Materialize` return fresh data, not shared mutable globals.

- [ ] **2.1 Add missing-feature tests.** Materialization cases compare complete bytes (including LF), not the returned partial flag alone. Example actual test:

```go
func TestNativeMaterializeTwoEditInteraction(t *testing.T) {
    got, err := Materialize([]string{"settlement-display-boundary", "settlement-display-default"})
    if err != nil { t.Fatal(err) }
    want := "{\"displayDefault\":\"EUR\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n"
    if string(got.Data) != want || got.Partial { t.Fatalf("%q partial=%v", got.Data, got.Partial) }
    partial, err := Materialize([]string{"partial-renderer-hunk"})
    if err != nil { t.Fatal(err) }
    if string(partial.Data) != "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":}\n" || !partial.Partial {
        t.Fatalf("%q partial=%v", partial.Data, partial.Partial)
    }
}
```

Add cases for none/source/renderer, input order equivalence, duplicate and unknown hunks, partial combinations, and fresh-value mutation. Compare full descriptor bytes/digest to Task 1's independent expected descriptor. Run `go test ./internal/nativefixture -run 'TestNative(Materialize|Program)' -count=1 -v`; expected RED is undefined interfaces, not a malformed test file.

- [ ] **2.2 Implement literal edits and real refund semantics.** Use the fixed base and reviewed replacement table, exactly-once edit matching, canonical edit order, and copied output bytes. Child decoder uses `json.Decoder.Token` with tracked keys; require an opening object, exactly the two known keys with string enum values, closing object, then EOF. Read at most 16 KiB + 1 to detect overflow; reject a nonregular config handle, a symlink config entry, missing/read errors, invalid UTF-8, duplicate keys (including escaped equivalents), unknown/missing fields, arrays/null/numbers/bools/nesting, bad enums, and any trailing non-whitespace value. Reject unsupported config before evaluation. Error text begins `native fixture config: ` and includes the actual decoding/validation cause, with one LF. Do not call `encoding/json.Marshal` or serialize digest input with its encoder.

Use typed internal values (`displayDefault`, `settlementSource`), `completeRefund(orderCurrency string, refundDisplay *string, config modelConfig) refundResult`, then a separate preservation comparison against order currency. For missing display choose order currency or EUR according to config; choose settlement from order or computed display according to config. No hunk/state ID/verdict is available inside the evaluator. `refundResult` contains display and settlement currency strings. Tests exercise the function independently of the predicate so a tautological hardcoded pass/fail is detected.

- [ ] **2.3 Test exact child outputs before implementing dispatch.** Use a test-file TestMain to intercept `DispatchChild` before `m.Run()`; no production environment fault switch. Build/run actual current test executable with fixed arg/marker from fresh directories containing each config. In these child-only tests, use a bounded standard-library `exec.CommandContext`; Task 3 will test the production runner. The PASS expectation is:

```go
const passOut = "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n"
const failOut = "{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"
const failErr = "expected settlement USD; received EUR\n"
```

Check status 0/1, complete exact stdout/stderr, and no marker added by `witnessresult.Format`. For partial config require status 3, empty stdout, and full stderr equal to a test-only independent standard-library decoder probe's actual cause prefixed by `native fixture config: `. The probe consumes the same malformed fixed JSON but does not call production parser/materializer/evaluator. Retain the observed diagnostic under the bound Go toolchain; do not invent a V8 error. Unit validation errors have fixed full expected strings for the decoder's own validation messages.

- [ ] **2.4 Implement exact dispatch and run GREEN.** Serialize the two structured protocol objects with `jsjson` in protocol/outcome order, plus LF. Emit only the defined success text or failure diagnostic. Propagate writer failures as a nonaccepted abnormal child exit (not status 0/1); status 3 is reserved for configuration errors. Negative cases: absent/wrong marker, no/extra/wrong args, help combined with the private arg. These return `handled=false` without file access, leaving ordinary CLI dispatch untouched. Run `go test ./internal/nativefixture -count=1 -v`; require complete output checks for all four normal subsets and partial plus strict-decoder cases. Checkpoint tests/code/evidence; no commit.

### Task 3: Bounded real self-execution and raw process evidence

**Files:** Create nativefixture process/provenance/platform helpers/tests and clock evidence timer/tests. Modify only the precise clock comments; preserve release/test clock implementations.

**Interfaces consumed:** Task 2 constants and `Materialized`. **Interfaces produced:**

```go
// internal/clock/evidence.go, independent of seeded wall-clock code:
type EvidenceTimer struct { started time.Time }
func StartEvidenceTimer() EvidenceTimer
func (t EvidenceTimer) Milliseconds() int64
// Private pure helper for exact flooring tests:
func evidenceMillis(elapsed time.Duration) int64

// internal/nativefixture/process.go:
type Capture struct {
    Config Materialized
    Executable string
    Argv, Environment []string
    Cwd string
    StartedAt, ExecutedAt time.Time
    DurationMs int64
    Status *int
    Signal string
    Stdout, Stderr []byte
    Incomplete bool
    FailureKind, CleanupError string
}
type ExecutionError struct { Kind string; Err error }
func (e *ExecutionError) Error() string
func (e *ExecutionError) Unwrap() error
// Executor fields are private; no caller-selectable program or cwd.
type Executor struct {
    deps processDeps
    executable, executableSHA256 string
    environment []string
    environmentPreimage jsjson.Value
}
func NewExecutor() (*Executor, error)
func (e *Executor) Environment() jsjson.Value
func (e *Executor) Run(ctx context.Context, input Materialized) (Capture, error)
func RawBlob(data []byte, complete bool) jsjson.Value
```

The private `Executor` fields bind the dependencies, observed executable path/hash, cloned effective environment, and its preimage. `Environment()` returns a fresh preimage object. `RawBlob` implements the ordered blob contract exactly. `Capture` is raw evidence, not a trusted verdict. Its zero timestamps/status/signal are represented as null in receipts. Snapshot input slices to prevent caller mutation changing recorded bytes.

Private fault seams for tests in the same package are:

```go
type processDeps struct {
    executable func() (string, error)
    command func(context.Context, string, ...string) *exec.Cmd
    mkTemp func(string, string) (string, error)
    writeFile func(string, []byte, fs.FileMode) error
    removeAll func(string) error
}
func newExecutorWithDeps(d processDeps) (*Executor, error)
```

`NewExecutor` binds exactly `os.Executable`, `exec.CommandContext`, `os.MkdirTemp`, `os.WriteFile`, `os.RemoveAll`. These are not runtime options and cannot be selected by environment or public API. Test substitutes live only in `_test.go` and do not run arbitrary external commands. Platform helper `observedSignal(*os.ProcessState) string` returns a real Unix signal description where present; Windows returns empty when no signal API observation exists, never a fabricated SIGKILL. Status stays an actual observed exit code/null.

- [ ] **3.1 Write timer and raw-blob RED tests.** Assert `evidenceMillis(999*time.Microsecond)==0`, `evidenceMillis(time.Millisecond)==1`, `evidenceMillis(1999*time.Microsecond)==1`; no sleeps or upper-bound timing races. Assert a live timer yields nonnegative elapsed and is not tied to seeded wall reads. Raw blob test uses bytes `{0,0xff,'\n'}` and exact expected base64 `AP8K`; independently SHA-256 the original bytes with `crypto/sha256`. Run focused clock/nativefixture tests and retain undefined-interface RED. Implement timer via `time.Now`, `time.Since(...)/time.Millisecond`; leave `clock.Monotonic` behavior/output prohibition unchanged and clarify that evidence durations use this separate API.

- [ ] **3.2 Write real process and owned-cleanup RED tests.** Test `NewExecutor().Run` with all four subsets and partial through the Task 2 TestMain, exact complete streams/status. Independently hash `os.Executable` and compare the environment preimage. After each return require the recorded private cwd no longer exists and a separate caller-owned sentinel directory/file is unchanged. On Unix inspect cwd/config mode before deletion through a test-only `removeAll` wrapper delegating to `os.RemoveAll`; assert 0700/0600. Windows asserts creation/ownership behavior without claiming POSIX ACL enforcement. Put fake parent credentials, PATH, HOME, and seed markers into the parent environment for this test; assert none occurs in the actual child environment/capture. Require explicit stdin EOF in a test-only self-exec helper.

- [ ] **3.3 Implement the fixed lifecycle.** Observe/hash the actual executable and create the minimal environment once in the factory. Allocate a fresh owned temp directory; write `native-config.json` with 0600 and no shell; use fresh copied bytes. Create `context.WithTimeout(ctx,2*time.Second)` and `exec.CommandContext` for the observed executable with exactly `ChildArg`. Set explicit nonnil environment, private cwd, nil stdin (closed/null input), `WaitDelay=250*time.Millisecond`, and cancellation that calls direct owned `Process.Kill` (not SIGTERM). Set command stdout/stderr to two writers sharing one mutex/1 MiB counter. On reaching the limit, retain only the allowed prefix, mark incomplete, cancel immediately, and reject even if Wait later reports status 0/1. The writer should report the write as consumed after recording/cancelling to avoid an unbounded retry in the copying goroutine; cancellation still terminates the child. No descendants are produced by the reviewed program.

Start the evidence timer and record wall start immediately before Start, wait/reap once, record wall end/duration immediately after Wait, then perform owned cleanup. Preserve context cancellation/deadline, `exec.ErrWaitDelay`, output-limit, signal, spawn, abnormal status and cleanup causes distinctly. On ordinary `*exec.ExitError` with observed status 1 or 3 and no infrastructure failure, return the capture without an infrastructure error; classification comes later. Unknown nonzero status rejects. On any panic after child creation, kill and Wait the owned child before error return, capture available evidence, and remove only the owned directory. Do not blanket-recover runtime fatal errors or claim to supervise a process tree. Use `errors.Join` to retain simultaneous primary/cleanup errors. No retries or broad process cleanup.

- [ ] **3.4 Add and exercise deterministic fault seams.** The test command seam selects test-only self-exec argv, not a production marker-controlled fault. Blocking helpers wait on pipe reads for stdin/deadline tests; overflow helpers write bytes until killed; an explicit abnormal-exit helper exits 7. Test cancellation after a readiness pipe handshake, default two-second timeout without sleeps, signal on Unix, spawn failure from the seam, prepare/write failure, partial output, oversize combined stdout+stderr, pipe-drain deadline via an owned test helper/descriptor and bounded teardown, and cleanup failure. All helper PIDs/handles are owned, reaped/closed in test cleanup, never global-killed. If any helper creates an additional process solely for pipe-lifecycle testing, retain its PID and explicitly kill/wait it; this is not evidence the production child has descendants. Tests assert no accepted truncated result, no surviving owned child, and no accidental deletion. For pre-Start cancellation require no spawn attempt. Keep exact two-second/250 ms production limits; test-level observation allowance may account for scheduling without changing runtime limits.

- [ ] **3.5 Run GREEN/race and checkpoint.** Payloads separately: `go test ./internal/nativefixture ./internal/clock -count=1 -v`; `go test -race ./internal/nativefixture ./internal/clock -count=1`; `go test -tags=faultline_test ./internal/clock -count=1`. Check full output/status/skips and the protected-path diff. Update evidence with raw captures and lifecycle limitations; no commit.

### Task 4: Explicit native approval, run identities, and receipts

**Files:** Create `internal/demofixture/native_witness.go`, `native_witness_test.go`, `native_run.go`, `native_run_test.go`. Keep cached functions untouched.

**Interfaces consumed:** `nativefixture.Program/Materialize/NewExecutor/RawBlob`, the Capture/Executor API, `clock.ISO/Now/NewUUID`, `witnessresult.Resolve/ConsistentWithExitCode/Classify`, existing `digest`/`strArray`, Task 1 contract corpus. **Interfaces produced:**

```go
type NativeApproval struct { ProposalDigest string; ApprovedAt string }
type NativeResult struct { Analysis jsjson.Value; Provenance jsjson.Value }
type NativeAnalysisError struct { Kind string; Ordinal int; StateID string; Err error }
func (e *NativeAnalysisError) Error() string
func (e *NativeAnalysisError) Unwrap() error
func NativeWitnessProposal() jsjson.Value

// Private seams: not exported, not selected through runtime environment.
type nativeExecutor interface {
    Environment() jsjson.Value
    Run(context.Context, nativefixture.Materialized) (nativefixture.Capture, error)
}
type nativeDeps struct {
    newExecutor func() (nativeExecutor, error)
    now func() time.Time
    nonce func() string
}
// nativeSession owns the frozen witness/environment, ordinal, receipts, seen-ID set.
type nativeSession struct {
    proposal, approval, witness, program jsjson.Value
    programDigest string
    environmentPreimage jsjson.Value
    environmentDigest, nonce string
    executor nativeExecutor
    ordinal int
    receipts []jsjson.Value
    seenIDs map[string]struct{}
}
func newNativeSession(approval NativeApproval, deps nativeDeps, startedAt time.Time) (*nativeSession, error)
func (s *nativeSession) run(ctx context.Context, state jsjson.Value, attempt int) (jsjson.Value, error)
func (s *nativeSession) acceptNativeID(id string, record jsjson.Value) error
func (s *nativeSession) provenance(status string, generatedAt time.Time) jsjson.Value
```

Declare concrete private fields: proposal, approval, witness, program, programDigest, environmentPreimage/digest, nonce, executor, next ordinal, ordered receipt slice, seen-ID set. On approval/executor-creation failure Task 5 constructs the same versioned ERROR envelope with available proposal/approval and unavailable facts null; it does not fake a session. `newNativeSession` validates approval **before calling** any dependency that resolves/executes a child. Nonce acquisition uses the production entropy path only after approval is accepted.

- [ ] **4.1 Write proposal/approval RED.** Compare complete proposal and final witness to Task 1 native-contract fixtures. Test missing/wrong digest, empty/non-UTC/malformed/non-millisecond/future approval, and zero executor calls for each rejected approval. The approval test time is injected through private `nativeDeps`, not a production env override. Mutating one returned proposal must not affect the next. Require the program digest in policy and no per-config digest in Witness. Run `go test ./internal/demofixture -run '^TestNative(Proposal|Approval|Witness)' -count=1 -v`, retain missing-interface RED.

- [ ] **4.2 Implement proposal/approval and freeze identities.** Build ordered `jsjson` values from the fixed contract; canonical-digest proposal body without digest, witness body without digest, and environment preimage. Parse/compare approval as specified. Store immutable per-analysis witness/environment values and one nonce. Do not reuse the July Node witness, approval, environment, or command. Document caller responsibility for real authorization and that the API does not authenticate a person.

- [ ] **4.3 Write run/receipt RED using deterministic test captures.** A `_test.go` scripted executor supplies complete captures from Task 1 input fixtures; it asserts every materialized config byte and consumes its script exactly. Compare the **entire** RunRecord, ID preimage, receipt, and blob hashes, not only verdict. Test distinct IDs with identical start/end wall milliseconds and with backwards wall timestamps. Test duplicates with both identical/conflicting records before the existing deduping catalog sorter can hide them. To exercise this branch without a production hash override, call a private `acceptNativeID(id string, record jsjson.Value) error` helper on the session's seen set; it must reject the second ID regardless of record equality. Define that helper in `native_run.go`, used by `run` immediately after digest construction.

- [ ] **4.4 Implement strict native classification and receipt assembly.** Increment ordinal for every attempted call, materialize the state's hunk names, and invoke the executor once. Keep raw capture even on failure. Validate same frozen executable/environment and exact allowed effective env for every capture. For ordinary configs: valid UTF-8; status exactly 0 or 1; signal absent; complete streams; exact full PASS/FAIL bytes from the program contract. Resolve the protocol, require a structured nonnil result, enforce exit consistency, and use `Classify`. Reject missing/conflicting/duplicate-extra protocol, markers, trailing noise, unexpected stderr, or output/status disagreement—even if generic Resolve would tolerate it. For the one known partial config require actual materialized invalid JSON, status 3, empty stdout, full native config diagnostic with real cause, no signal/infrastructure failure; produce EXECUTED/ERROR/FIXTURE_PARSE_ERROR. Status 3 on any valid config is UNEXPECTED_PARSE. Do not derive verdict from state ID, normalize streams, use cached REPLAY reasons, or accept unstructured exit zero.

Build the RunRecord body in existing key order; compute the versioned ID preimage/digest; reject duplicate native ID; insert ID first in the final RunRecord; attach full record/preimage to accepted receipt. On failure append the rejected receipt with `accepted:false`, null record/ID/preimage, failure kind and available evidence, then return typed error. Accepted ERROR partial executions retain complete raw bytes and IDs. `RawBlob` always hashes original bytes. Receipt environment/argv/cwd are actual capture observations; unavailable facts are null. A cleanup failure cannot yield an accepted run. Guard returned values against accidental shared-object mutation.

- [ ] **4.5 Run focused GREEN and negative matrix.** Cases include malformed/missing/conflicting protocol, a duplicate valid protocol line, status 0/fail, status 1/pass, stderr noise, wrong config digest/bytes, unexpected parse failure, invalid UTF-8, overflow prefix, changed environment/witness, signal, spawn/deadline/cancel/cleanup failure, and panic from a test dependency. Require typed error, retained rejected receipt, and no analysis from the eventual API. Run ordinary/tagged native-focused tests, plus unchanged complete REPLAY tests; inspect diff/checkpoint. Do not alter old `uniqueRunCatalog` semantics or golden hashes.

### Task 5: Additive 39-call RERUN, private fl bootstrap, and qualification

**Files:** Create the native analysis/reference/child tests and CLI bootstrap files in the map; modify only `cmd/fl/main.go` before `nodeArgv`, package-description lines 1–4 of demofixture/fixture.go, and append exact differences documentation.

**Interfaces consumed:** Task 4 approval/result/error/session APIs; existing `fixtureStates`, index constants, `transitionKind`, `attempt`, `uniqueRunCatalog`; Task 1 reference. **Interface produced:**

```go
func RunNativeAnalysis(ctx context.Context, approval NativeApproval) (NativeResult, error)
// Unexported test seam; production RunNativeAnalysis always supplies real deps.
func runNativeAnalysis(ctx context.Context, approval NativeApproval, deps nativeDeps) (NativeResult, error)
// cmd/fl/native_sample.go:
func runNativeSample() (code int, handled bool)
```

Public function binds `now:clock.Now`, `nonce:clock.NewUUID`, and a closure wrapping `nativefixture.NewExecutor()` as `nativeExecutor`; no caller runner/path/env/clock argument. TestMain in the demofixture test package uses the same `DispatchChild` with real process args/marker before running tests. Private `runNativeSample` does the same for the actual CLI.

- [ ] **5.1 Write complete expected-output RED before analysis code.** Read each Task 1 `native-contract.jsonl` row, reconstruct only its explicitly synthetic test dependencies, call private `runNativeAnalysis`, and compare the complete serialized `{analysis,provenance}` to its one `expected` string. No field stripping or candidate-to-native profile transform. A minimal assertion shape (the named fixture helper is defined in `native_reference_test.go` and loads the new corpus) is:

```go
func TestNativeAnalysisFullContract(t *testing.T) {
    for _, tc := range nativeContractCases(t) {
        t.Run(tc.ID, func(t *testing.T) {
            got, err := runNativeAnalysis(context.Background(), tc.Approval, tc.Deps)
            if err != nil { t.Fatal(err) }
            actual := jsjson.MustObject("analysis", got.Analysis, "provenance", got.Provenance)
            encoded := []byte(jsstr.ToUTF8(jsjson.StringifyIndent(actual, "  ") + "\n"))
            if !bytes.Equal(encoded, []byte(tc.Expected)) {
                t.Fatalf("complete native result differs\nwant=%s\ngot=%s", tc.Expected, encoded)
            }
            tc.AssertConsumed(t)
        })
    }
}
```

Define test helper type `nativeContractCase` with `ID string`, `Approval NativeApproval`, `Deps nativeDeps`, `Expected string`, `AssertConsumed func(*testing.T)`; `nativeContractCases(*testing.T) []nativeContractCase` parses inputs using existing test JSON helpers or `jsjson.Parse`. No test helper is linked into production. Require RED from undefined `runNativeAnalysis`, not missing evidence or malformed test code.

- [ ] **5.2 Implement additive orchestration and proof checks.** Capture API-entry wall time once, check nil/cancelled context fail-closed, apply `context.WithTimeout(ctx,90*time.Second)`, validate approval/create session, then execute the exact 39-call order from Task 1. Port engine control flow in a new file; never wrap cached REPLAY in an executor abstraction. Reuse existing state tuples/hunk constants/transitionKind/attempt/digest helpers. Require each transition's three before/three after observations to be distinct-ID EXECUTED PASS/FAIL, same state per side, same witness/environment, consistent per-side verdict matching timeline boundaries. Exactly the expected three transitions must exist. Require source-only/renderer-only/empty PASS, partial ERROR/FIXTURE_PARSE_ERROR, candidate FAIL, reverted PASS; enforce nonempty two-edit candidate and sufficiency/necessity identities. Require prevention PASS/FAIL/PASS with common witness/environment and correct states. Reject before constructing grade/claims if these predicates fail. Stable first regression must be turns 4→5. Use stricter seen-ID validation before `uniqueRunCatalog` sorting; catalog length and receipt coverage are exactly 39.

Return fresh ordered analysis with existing schema/mode RERUN, actual final generatedAt, unchanged metrics/order/copy except the exhaustive native table, grade A only after all proof checks, and the separate COMPLETE envelope. On any call/preparation/proof/panic failure return zero/undefined Analysis and ERROR provenance with completed/rejected receipts, no retries and no unconditional certification. On panic ensure lower-level runner has reaped/cleaned any owned child before returning; convert safe recovered error context to a typed PANIC failure and preserve existing receipts. Do not fabricate missing observations. API errors before session creation have zero invocations and known proposal/approval, other facts null.

- [ ] **5.3 Implement and test the real CLI private bootstrap.** New helper delegates to `nativefixture.DispatchChild(os.Args[1:], os.Getenv(nativefixture.ChildEnvName), os.Stdout, os.Stderr)`. At the beginning of `main`, before `nodeArgv`, insert:

```go
if code, handled := runNativeSample(); handled {
    os.Exit(code)
}
```

Build untagged `fl` into a fresh evidence directory using `go build -o ABS ./cmd/fl`. Run actual binary with marker/arg/config from a fresh empty cwd for each complete subset and partial, checking entire stdout/stderr/status. Run negative marker/arg combinations and ordinary CLI help tests to prove dispatch is unchanged. The marker is protocol selection, not access control. Keep full judge-demo, public RERUN/serve integration and writable HTTP out of scope.

- [ ] **5.4 Run real 20-state and unseeded 39-process acceptance.** Demofixture real-child test iterates every existing logical state through production self-exec and checks complete streams/status against hunk semantics; it does not hardcode state→verdict in production. A real `RunNativeAnalysis` test supplies caller-recorded approval explicitly just before invoking the API, not a historical timestamp. Independently inspect all 39 receipt blob hashes, exact configs, executable file hash, actual env, status/signal, durations, ID preimages, nonce/ordinal sequence, and one-to-one catalog linkage. Write the actual result and complete reference input to fresh W artifacts through controller-owned tests; outputs contain no credentials because none is inherited.

Run `gen-native-demo.mjs verify-live` with pinned Node against that complete observed input. It independently reconstructs IDs/envelope, replays unchanged engine orchestration with real frozen states plus the explicit native expected-copy contract, and compares entire observed analysis bytes including IDs/times/streams. Retain input, reconstructed expected output, candidate output, and mismatch result. This comparison validates orchestration from observations; separate real-child tests prove actual process execution. Never claim frozen TS emitted the provenance envelope or original JavaScript ran.

Fault proof tests alter only test captures at representative contribution/boundary/minimization/prevention ordinals: inconsistent stable-side verdict, mismatched state/witness/env, insufficient candidate, failed necessity, failed repair, partial unexpected PASS, duplicate ID, early cancellation, and panic. Each returns typed error plus partial provenance and **no analysis**. Assert strict transcript exhaustion on success and no further calls after failure.

- [ ] **5.5 Prove Node-free release execution and regressions.** Build untagged nativefixture/demofixture test binaries and actual `fl`; use test-only absolute corpus flag `-native-demo-corpus=ABS` where needed. Run from fresh empty directories with environment allowlist, empty PATH, no Node/Go/TS/dist/testdata/manifest in cwd; do not run the compiler from that cwd. Execute real-child/39-run tests and CLI helper cases. Demonstrate release ignores `FAULTLINE_TEST_SEED` by two independent runs (fresh nonce/IDs) while actual children inherit no seed. No exact timestamps are assumed for unseeded runs. Ordinary and existing tagged clock tests remain green; timer remains unseeded in either build. Tests use their own binary bootstrap, not a hidden production fallback when linked into arbitrary programs.

Separately run unchanged full REPLAY bytes/hashes and all 13 `TestIncident*` top-level tests/24 exact page subtests; relevant CLI/proofview/bundle regression suites; `go vet` for affected packages; focused `go test -race` for changed packages; source-defined broad ordinary and `faultline_test` suites with original skip accounting. Review actual `go.yml` commands before execution; do not infer broad gate success from a package subset. Do not change timeouts/guards or explain the existing Darwin timeout as fixed by this feature. Preserve old Zod/Docker/Colima and historical timing debt.

- [ ] **5.6 Qualify platforms and inspect the final change.** Cross-build binary and relevant test binaries for linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64, windows-arm64 in separate bounded retained stages with CGO disabled; record compilation ONLY. Execute all new real-process/approval/provenance/orchestration/bootstrap tests natively on each available target; record exact source/diff hash, binary hashes, Go/runtime/OS/arch, commands, original statuses, complete test names/counts/skips. A missing host is pending/excluded, never passed; actual execution on all six is required for full platform qualification. Existing main CI at db37e869 cannot qualify these new changes. No new hosted run/publication follows without separate authorization.

Append exhaustive native differences to `KNOWN_DIFFERENCES.md`: reviewed Go model identity, approvedAt/proposal contract, child command/config overlay, native env/binary hash, new run ID preimages, measured time/duration, real parse failure/reason, exact seven copy changes, fail-closed proof behavior, separate non-attesting envelope not persisted/authenticated by existing bundle writer/verifier. Keep old entries intact. Change demofixture package description only to distinguish pure cached API from explicit native child execution. Confirm protected paths/dependencies/workflows/old corpora untouched via complete diff and hashes. Obtain independent read-only Sonnet review of actual code/diff/evidence for model transcription, approval, lifecycle, digest/copy contracts, and claims. Resolve verified findings with retained tests; reverify changed scope. Checkpoint local delivery with precise outstanding platform/broad gaps; no commit/push/PR/merge/release.

## Completion matrix and plan self-review

| Spec requirement | Implementation/evidence obligation |
| --- | --- |
| Fixed reviewed model, strict config, real parse failure | Task 2; actual protocol/subset tests and independent decoder diagnostic |
| Approval/proposal/program/fresh witness/environment | Task 4; reject-before-spawn tests and full contract |
| Minimal env, self-exec, bounds, kill/reap/cleanup | Task 3; real process and owned fault tests, Task 5 bootstrap |
| Separate evidence duration, unchanged deadline/entropy behavior | Task 3 timer plus Task 5 release/tag regressions |
| Fresh nonce+ordinal, full ID preimage, strict uniqueness | Task 4 full bytes/equal-backward cases and Task 5 linkage audit |
| Additive 39-call flow, honest proof/claims/failure result | Task 5 complete independent contract/live transcript and fault matrix |
| Independent expected bytes before product code | Task 1 frozen loader, unadapted output, native contract and independent recapture; Task 2 missing-feature RED |
| Node-free deployment, preserved cached/renderer output | Task 5 isolated release executions and full unchanged regressions |
| Six native platforms, broad gates, no gate weakening | Task 5 qualification; unavailable execution is explicitly pending, not substituted by cross-builds |
| Actual-main CI separate, no publication authority | Ownership section; existing collector/scheduler only, local delivery boundary |

Self-review before first product edit: verify every table/path/signature above agrees with the approved spec and between tasks; check the reference constructor is independent of Go outputs; check no receipt/new field leaks into an old schema; check the materializer has no state-verdict mapping. The exact symbols defined here are the interfaces later tasks consume; test helpers/fault seams remain test-only or unexported. Never present unchecked boxes as completed work. Preserve exact source/capture bindings and every failed attempt. The next executable step is Task 1's independent reference-loader tests, not native product code.
