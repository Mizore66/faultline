# FaultLine CI targeted shards and deterministic harnesses — design

- **Date:** 2026-11-07
- **Status:** Design and written specification approved by the user on 2026-11-07 after scope, split, harness and acceptance choices. Implementation-plan approval remains a separate gate. No implementation is authorized by this document.
- **Baseline:** PR #221 merged at `e6dd62324e3fbcd3827b0967d2e2138a4b754bcf`; PR #222 merged at `89a5d07cbba29be072a8019805ed41983145b722`. Both post-merge main workflows succeeded, and both docs-only gates printed `CI-SCOPE docs-only`.
- **Scope selected:** deterministic harness corrections plus two targeted splits. No broad caching, no serve evidence partition, no production deadline change and no coverage waiver.

## 1. Measurements and rationale

PR 1's accepted run `42236958991` had 54 checks, 53 successes and one expected provenance skip. Its critical path was 70.1 minutes versus 85.8 and 79.2 minutes at baseline; per-event job time was 583.5 versus 590.5 and 599.3 minutes. The next bottlenecks and unnecessary costs are:

- `TestLiveServeBundleStackCallSites` consumed 2781.41 seconds (46.4 minutes) inside the 59.5-minute Windows ARM serve job. `TestLiveServeBundle` itself consumed only 325.28 seconds, so moving stack probes does not require evidence partitioning.
- `TestWriterReplay` consumed 2509.72 seconds (41.8 minutes) inside the 3996.399-second macOS Intel tagged `difftest` package. The non-writer residual was 1486.679 seconds.
- All 23 `pnpm build` steps totaled only 2.5 minutes. Broad TypeScript build caching is not worth new provenance/lifecycle complexity in this PR.
- `turn-binding/close-EIO` missed its intended third close because strace ordinals are per-thread while validation counted a combined trace. `fake_flood` reached different resource limits on TS and Go under load and could false-pass when both timed out.

Objectives:

1. Reduce the critical path toward approximately 45 minutes through independent whole-case/suite jobs, while publishing actual rather than assumed durations.
2. Preserve every test, invocation, native execution and evidence obligation. More parallel jobs are acceptable only with source-to-shard and terminal-count guards.
3. Remove the two proven nondeterministic harness behaviors without changing production, limits, oracle bytes or expected outcomes.

Non-goals: broad caches, `node_modules` sharing, `t.Parallel`, serve evidence partitioning, longer deadlines, retry-as-pass, accept-either outcomes, weaker assertions, or a blanket summary/count waiver.

## 2. Serve stack-probe split

Move `TestLiveServeBundleStackCallSites` out of `serve-bundle-native` into a separate whole-suite job on every existing native target.

- Keep every stack site and every shape together in that job. Do not filter individual sites or shapes; its refit consumes accumulated per-site results.
- Leave `TestLiveServeBundleEvidenceIdentity`, `TestLiveServeBundle`, `TestLiveServeBundleBinding`, signals, replay, HTTP, startup interrupt tests and `TestServeBundleStandalone` in their existing ownership.
- Keep the existing final evidence audit and all 68 original plus 5 supplemental cases, raw evidence directories, identity and artifact paths unchanged. No partial evidence directory is produced, so no merge is necessary.
- Keep the Windows console diagnostic separate, including its final process-wide console-state mutation.
- Give the new stack-probe job its own log/list and apply the existing no-skip and result-count checks. Its artifact name must not collide with serve-bundle evidence artifacts.

The serve leg should drop to approximately 13 minutes, while the new stack-probe leg runs in parallel. This estimate is not an acceptance claim.

## 3. Non-Ubuntu writer-replay shards

### 3.1 Boundary and mapping

Use four independent jobs per non-Ubuntu tagged leg, assigned by:

```text
shard = SHA256(UTF-8(caseID))[0] mod 4
```

Whole caseIDs stay together because records, mutation state and relocation names accumulate across invocations. Cases have independent physical roots, `HOME`, `TMPDIR`, recorders, argv logs, seeds and clock offsets. `tsCrossVerified` and `retainedRoots` are process-local. No workspace or mutable replay state is shared between shard processes.

Keep the Ubuntu AMD64 tagged coverage process completely unnarrowed with empty `-run`. Its coverage gate and native-evidence cache remain intact. The Ubuntu frozen-TS cross-verification job also remains unchanged; its positive-count summary is not a shard-union guard.

Run the remaining non-writer difftests once per non-Ubuntu leg, with `TestWriterReplay` excluded only there. `TestGoldenReplay`, `TestWriterSameDirectory`, thread/end-to-end companions and other replay tests retain their independent ownership.

### 3.2 Exact corpus and shard totals

| Shard | Template cases | Golden invocations on macOS/Linux | Windows PASS cases | Windows POSIX SKIP cases | Windows compared invocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| 0 | 282 | 332 | 97 | 185 | 111 |
| 1 | 281 | 332 | 105 | 176 | 113 |
| 2 | 255 | 314 | 99 | 156 | 112 |
| 3 | 257 | 317 | 99 | 158 | 108 |
| **Total** | **1,075** | **1,295** | **400** | **675** | **444** |

`posix/recorder-fake-docker` is assigned exactly once to shard 0. It adds one PASS on macOS/Linux and one allowed SKIP on Windows, producing 1,076 terminal case results including the recorder.

POSIX cases are named `TestWriterReplay/posix/<id>`. Every shard therefore needs separate exact branches for `TestWriterReplay/<ordinary-id>` and `TestWriterReplay/posix/<posix-id>`. `recorder-fake-docker` appears only in shard 0's POSIX branch. An entirely empty filter check is not sufficient to prove shard completeness.

### 3.3 Mandatory guards

1. **Source-to-shard guard:** recompute assignments from `loadWriterTemplates`, require every source caseID exactly once across generated filters, reject foreign/duplicate IDs and require the recorder exactly once. Test missing, extra, duplicate and malformed manifests.
2. **Per-shard terminal guard:** count unique leaf results and require the table's case totals. macOS/Linux requires every selected case to PASS. Windows requires exactly the listed POSIX cases to SKIP and every non-POSIX case to PASS. Count the recorder separately in shard 0.
3. **Aggregate union guard:** across all shard logs require 1,075 unique template cases, no duplicates and the recorder once; require 1,295 compared golden invocations on macOS/Linux or 444 on Windows with the per-shard totals above. A parent PASS or the existing positive-count cross-verification summary cannot satisfy this guard.
4. **Correct execution accounting:** report actual selected/executed cases and compared invocations, not total `len(templates)` and `len(goldens)` under filtering. Keep selection distinct from execution on Windows because POSIX selection precedes the skip.

The existing writer-fault leaf and source-to-shard guards are models, not replacements for these new replay-specific checks.

## 4. Deterministic harness corrections

### 4.1 `turn-binding/close-EIO`

Add a non-`_test.go`, `faultline_test && linux`-only CLI thread-affinity hook that calls `runtime.LockOSThread()` only when `FAULTLINE_TEST_LOCK_OS_THREAD=1`. Enable that variable only for Go `onlyCalls` fault rows, covering the three `turn-binding/close-*` leaves without changing scheduling for other tests. Release builds do not link the hook and remain unchanged.

Keep `GOMAXPROCS=1` and `when=3` injection. Replace combined-line ordinal validation with the existing parser:

- Reject `SIGABRT` and parse failures.
- Find the first injected record's PID/TID.
- Count only completed parsed calls on that same TID, merging unfinished/resumed records once.
- Require the first injected call on that TID to be ordinal 3; `faultOrdinals` remains `ts: 3, goSide: 3`.

Add synthetic regressions for foreign-TID closes before the injected third close (pass), same-TID unfinished/resumed third close (count once and pass), orphan resumed/unterminated calls (fail), and existing first-close/SIGABRT failures. Do not change turn-binding production code.

### 4.2 `fake_flood` and resource-limit assertions

Replace the flood generator with:

```sh
exec dd if=/dev/zero bs=1048576 count=65 2>/dev/null
```

This preserves the exact 65 MiB payload and direct `exec` signal delivery while reducing generator overhead. Suppressing dd statistics keeps exact stderr comparisons clean. Both production resource limits and their priority remain unchanged.

Add an optional exact `wantError` to hardened cases and require it before the independent TS/Go equality check:

- `fake flood`: `Git command exceeded bounded output limit`
- `fake slow`: `Git command exceeded bounded execution time`

This closes the both-timed-out false-pass hole without accepting either outcome.

### 4.3 Windows CLI-help timeout

Leave the existing 5-second release-process timeout unchanged. Available evidence establishes a cold-start timeout, not its cause. No longer timeout, retry or warm-up is authorized by this spec.

## 5. Verification and acceptance

Required focused evidence:

- Source-to-shard and terminal/aggregate guard tests with valid full manifests and every missing/duplicate/foreign/malformed class.
- POSIX and ordinary filter-selection tests, including recorder-once and no empty shard.
- Repeated exact close-EIO cases demonstrating injected TID ordinal 3 and no combined-trace false failure.
- Flood/slow cases proving exact per-case resource-limit errors on both TS and Go, including a both-timeout case that must fail rather than pass.
- Serve stack-probe suite remains whole, and the existing final serve evidence audit still proves all 68+5 cases and identity.

Required hosted evidence:

- All six native targets, existing expected skips only, no new deadline or production change.
- All 1,075 template cases plus recorder, 1,295 golden invocations on macOS/Linux and 444 on Windows, and all 654 writer-fault leaves.
- Ubuntu AMD64 coverage remains unnarrowed and full; Ubuntu frozen-TS cross-verification remains positive and exact.
- Exact-head PR CI and post-merge main CI, with before/after critical-path and total-compute measurements against PR 1's 70.1 minutes and 583.5 job-minutes.
- Independent task and whole-branch reviews. Historical failures remain recorded and are never erased by a retry.

Interactive RERUN remains queued behind the accepted CI work. This design does not authorize implementation, publication, merge, release, protected-source edits or oracle retirement. Those remain separate decisions.
