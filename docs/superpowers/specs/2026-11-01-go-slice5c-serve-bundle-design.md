# Go migration slice 5c — read-only bundle serving and verified attachments

- **Date:** 2026-11-01
- **Status:** The user separately approved the design direction (“ye”) and this written specification (“yes”) on 2026-11-01. The [five-task implementation plan](../plans/2026-11-01-go-slice5c-serve-bundle.md) passed final documentation checks. The user subsequently explicitly authorized proceeding after the finished plan and continued Slice 5 delivery through passing CI and normal merge without routine permission prompts. Local implementation and both independent task-level gates for Tasks 1–5 are complete. The independent whole-branch architecture/security/correctness review accepts local readiness for publication and exact-head CI qualification with no verified findings; it does not certify runtime or native acceptance. Hosted acceptance remains pending: all six native targets, strict seven-site stack probes, pinned regeneration/no drift and the unnarrowed Ubuntu amd64 positional gate must pass before normal merge or declaring 5c complete. Protected-source/security/evidence constraints remain mandatory; no public release or oracle retirement is authorized.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md) and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** [PR #216](https://github.com/Mizore66/faultline/pull/216), normally merged by Mizore66 at `2026-10-31T22:38:24Z`, commit `ff5fe7b1120410e60c321f3ae620cf524c5694ec`. Its tree equals accepted head `6a8708391b4dbe9ec75d96c84ea832ab68c71564`. Reviewed-head acceptance is not post-merge CI evidence or completion of all 5b/Slice 5.
- **Oracle:** Frozen source `ce99a793af0b020bf1b9cf21114f8d7897b37207`, Node 22.22.2, current-tree manifest `0.1.4`. The source pin's manifest `0.1.3` and the observed live submission tag remain distinct provenance as explained in the [5a spec](2026-10-30-go-slice5a-cli-surfaces-design.md#3-version-provenance-and-standalone-behavior).

## 1. Deliverable and boundaries

Port the existing `serve --bundle` branch, optional verified minimization/repair/prevention attachments and their privacy-minimized Git proof panels. Reuse the read-only Git proof server already ported for `judge-proof`. A viewer inspects retained evidence without executing repository code, rerunning a witness or creating a new proof.

| Included | Completion boundary |
| --- | --- |
| Attachment-aware proof loader | Verify the base package, independently verify each supplied attachment, check its incident binding and retain matching parsed snapshots before rendering |
| Git proof panels | Exact frozen TS HTML for absent/present attachments, proof distinctions, citation summaries and privacy omissions |
| `serve --bundle` CLI | Exact parser/validation precedence, startup/errors, read-only HTTP and source-specific signal lifecycle |
| Permanent evidence | Additive TS captures, Go-only replay, real-verifier hostile tests, live HTTP/CLI comparisons and native standalone checks |

**Not included:** plain `serve`, genuine RERUN, interactive incident rendering, full `judge-demo` (including cached export-only), UI hub/discovery, witness review/live proposals, guided investigation, tutorial/live demos, new JSON modes, help/doctor/hook changes, distribution, provider operations or oracle retirement. The [5b follow-up](2026-10-30-go-slice5b1-cached-artifacts-preview-design.md#1-deliverable-and-boundaries) remains separate; 5d can consume this slice's verified view only after its acceptance. No mutation/CSRF/concurrency policy for a future writable server is approved here.

Attachments remain outside the immutable base Git-proof root. Loading one does not rewrite the base package or retroactively include it under that root. Verification establishes the applicable integrity and linkage checks, not model intent, a unique semantic cause or external trust when no retained root was supplied.

## 2. Architecture

Extend existing boundaries rather than create a second viewer/server framework. An isolated replacement viewer package would duplicate established rendering and transport contracts; combining mutable demo work would couple unrelated execution/security decisions to this read-only port.

| Component | Responsibility | Interface constraint |
| --- | --- | --- |
| `internal/proofview` model/loader | Add optional attachment records/options and focused attachment loading beside the existing base loader | Preserve the current two-argument `LoadVerifiedGitProofView` callers; add an explicit attachment-aware entry point. Zero/absent attachments retain existing behavior |
| `internal/bundle/prevention` | Expose parsed manifest/body from the same verifier invocation | Add an artifact-returning sibling sharing one verification implementation with existing `Verify`; preserve `bundle.Result` and all existing callers/results |
| `internal/proofview` renderer | Pure projection of verified in-memory data | Reuse existing escaping, JS string handling and page chrome; no file reads or process execution from rendering |
| Existing read-only server | Render once before listen and serve the resulting page | Reuse `StartGitProofServer`, URL and Close; no new routes, watcher, per-request reload or HTTP framework |
| `internal/cli` | Thin exact-flag dispatcher, options and SIGINT-only wait | Reuse existing parser/output/failure conventions, not the broader judge lifecycle unchanged |

The detailed implementation plan will fix exact Go types/signatures and focused file splits within these boundaries. It must not introduce duplicate verifier logic. The prevention sibling returns the existing verdict plus the manifest/body parsed by that invocation, not data obtained by a later read. Attachment loading requires a valid verdict and all required values; invalid or partial values never reach rendering. Regression tests must prove existing `Verify` verdicts, errors, classification, root and external status remain identical.

Go 1.27 and the standard library are required. Preserve ordered JS values, UTF-16/WTF-8 behavior, canonical digests, JS number semantics and the existing Git CLI dependency. No `encoding/json` on digest paths. Production must not execute Node or read `src/`, `dist/`, testdata or a runtime package manifest. No new Git command behavior, proof schema, production entropy override or artifact writer is needed.

## 3. Verified loading and bindings

### 3.1 Base Git proof

Preserve `src/git-proof-view.ts:89-137` and the existing Go sequence, not a simplified verify-then-read:

1. Resolve the bundle and verify it with the caller's optional expected root; require a valid manifest and root.
2. Read/schema-parse investigation and frozen witness, then verify again pinned to the first verified root.
3. Read/schema-parse them again, then perform the third verifier pass pinned to that root.
4. Require matching roots, canonical manifests and both parsed presentation snapshots; only then load attachments in minimization → repair → prevention order.

Retain the original verification's external-root status, the final verifier's checked-file count/manifest and the second parsed presentation snapshot. Later internally pinned passes must not turn an original `NOT_PROVIDED` into a displayed `MATCH`. `NOT_PROVIDED` means self-consistency without an externally retained root, not externally anchored trust. Investigation linkage digests use the schema-parsed investigation already held in the View, not the raw JSON read from disk.

All attachment options are independently optional at the library boundary. Empty options return three absent attachments without reading their paths. An expected digest without its corresponding path remains unused there, as TS does; the public CLI has stricter pair checks. Evaluation stops at the first failure, with no partially successful View.

### 3.2 Minimization

Use the existing standalone minimization file verifier, including its non-symlink regular-file requirement, 128 MiB cap, schema, semantic and expected-digest checks. Require a valid parsed result and digest. Then enforce:

- A non-null witness summary with both `frozenDigest == frozenWitness.frozenDigest` and `witnessDigest == frozenWitness.witnessDigest`.
- Non-null before/after states and a base investigation `PASS_TO_FAIL` transition with the same before commit/tree and after commit/tree. Do not substitute state indices, run IDs or execution IDs for those comparisons.
- A second valid file verification with identical result digest and canonical parsed result. Return the second result/digest/external status.

Do not additionally gate attachment on `proof.isProof`, result status or a new certification condition. A valid but uncertified minimization can attach; the panel must say it is not certified. Preserve linkage-error ordering and source error strings from `src/incident-attachments.ts:94-141`.

### 3.3 Repair

Preserve `src/incident-attachments.ts:153-219`: **verify → read/parse/validate snapshot 1 → match/bind → verify → read/parse/validate snapshot 2 → verify**. The existing artifact verifier remains authoritative for exact file set, links/special files, schemas, manifest digest, packet/brief integrity and citations.

The adapter's additional reads parse `evidence-packet.json` and `repair-brief.json` with the existing schemas and `ValidateBrief`. Retain the source's read-error wrapper and payload-validation errors. Match manifest evidence-packet digest to `packet.packetDigest` and manifest repair-brief digest to canonical digest of the parsed brief. Bind packet investigation digest to canonical digest of the parsed base investigation and packet frozen-witness digest to `frozenWitness.frozenDigest`.

Both later verifier manifests must canonical-match the first; both parsed packet/brief snapshots must canonical-match; the final manifest must match snapshot 2. Return the final manifest/status and snapshot 2 contents. Do not replace this with one verifier call, unchecked rereads, raw-JSON digests or a new attachment-specific safety policy. The source's extra reads do not independently lstat; retain the surrounding verification/recheck contract without claiming universal race-free filesystem confinement.

### 3.4 Prevention

Preserve `src/incident-attachments.ts:221-256,272-282`. Require the verified base Git proof root when a prevention path is supplied. The first prevention verification must be valid and return manifest, body and root. Bind:

| Body field | Required base value |
| --- | --- |
| `originalProofRoot` | Root produced by successful base verification, not a digest recomputed from unverified input |
| `frozenWitnessDigest` | `frozenWitness.witnessDigest` |
| Optional `investigationDigest` | Canonical digest of the parsed base investigation, only when the field is present |

The repair/prevention frozen-witness comparison is deliberately asymmetric: repair uses **frozenDigest**, prevention uses **witnessDigest**. Preserve it and test with different values; do not silently correct it during this parity port.

Run the prevention verifier a second time and require the same root and canonical body. Return the second manifest/body/root/status, with no adapter file reread. Root integrity covers the manifest; do not add a separate manifest/repaired-runs equality condition absent from TS. The existing verifier still enforces all classification-specific semantics, including grounded repaired-run requirements. Both `PREVENTION_EVIDENCE_SUMMARY` and `PREVENTION_VERIFIED` can attach if valid; do not promote a summary or reject it solely for that classification.

### 3.5 Failures and deterministic rechecks

Copy source error messages, fallback detail and precedence exactly; use existing JS exception/filesystem/schema conventions. Attachment success cannot contain a digest `MISMATCH`. Schema-error stack call-site behavior must be compared on the actual new CLI load path, including repair reread parsing; a passing `repair verify` comparison does not prove that call site. No new broad stack/output normalization is approved.

Use real files and real verifiers for acceptance cases. For controlled changes between verification/read boundaries, a narrow package-private dependency seam is permitted: production callers supply fixed real verifier/read functions; tests supply staged snapshots/results. No exported setter, mutable release-global override, environment switch or production seed. Seam tests prove ordering and fail-closed decisions, not actual filesystem race exclusion; retain separate real-verifier integration tests. No sleep/load/thread-scheduling race acceptance.

## 4. Rendering and privacy

Port supplied branches from `src/git-proof-view.ts:259-330,334-369` without redesigning copy, CSS or layout. Existing absent-attachment pages, `judge-proof` and `commit-proof-preview` output must remain byte-identical.

| Panel | Exact projection boundary |
| --- | --- |
| Minimization | `BIDIRECTIONALLY CERTIFIED` iff `proof.isProof`; otherwise `NOT CERTIFIED AS COUNTERFACTUAL PROOF`. Candidate count, recorded/not-established 1-minimality, sufficiency/necessity status, attachment digest and external status; no unit paths/patches/diagnostics |
| Repair | `INFERRED`, citation-validated guidance, never executed repair/model intent. Invariant reference count, repair-direction count, deduplicated citation IDs and artifact identity/status; omit free-form guidance |
| Prevention package | Exact `PREVENTION_VERIFIED` versus `PREVENTION_EVIDENCE_SUMMARY` wording, last-good/first-bad/repaired commit+verdict summary and root; never upgrade classification |
| Prevention guidance | If no prevention package but repair exists, show inferred hard/soft guidance counts and citations. If neither exists, retain the absent branch |
| Hero summary | Preserve the source check of minimization+repair only: prevention alone does not select the supplied-artifacts summary |

Citation summaries deduplicate then use JS default string sort (UTF-16 order), not ICU `localeCompare`; invariant reference counts retain the original array length rather than the deduplicated summary length. Preserve escaping, short-digest/commit slicing, whitespace and final-newline behavior. Test `<`, `>`, `&`, quotes and Unicode in every permitted attacker-controlled presentation field.

Do not expose private repository paths, witness behavior/command/overlay text, incident-packet contents, reviewer identity, raw outputs/patches, free-form repair/prevention statements or diagnostics. Assertions must compare the entire HTML and independently assert that distinctive private sentinels are absent. A digest/count-only summary is not permission to dump a parsed attachment elsewhere in the document or an HTTP endpoint.

## 5. CLI contract

### 5.1 Selection and validation order

Select this handler only for top-level `serve` with an exact `--bundle` argument. Without that exact flag, including only `--bundle=value`, preserve the existing explicit not-yet-ported plain-serve behavior. Reuse `hasFlag`, `option` and `requiredOption`: first exact occurrence wins; equals forms are not parsed as flags; required values reject absent/empty/`--`-prefixed values. Do not introduce a general parser or unknown-option rejection.

Match `src/cli-app.ts:2950-2984` in order:

1. Require and resolve `--bundle`.
2. Parse supplied required values for `--minimization`, `--repair`, `--prevention`, `--expect-minimization`, `--expect-repair`, `--expect-prevention`, in that order, before any pair check.
3. Check minimization, repair, prevention path/digest pairing, in that order.
4. Resolve attachment paths and load the verified View with optional `option(--expect-root)`. This root option does not acquire `requiredOption` semantics.
5. Only after loading, coerce `option(--port) ?? "4173"` using JS Number semantics and start the existing server.

Thus a missing later option value can precede an earlier pair mismatch, pairing precedes bundle I/O, and invalid bundle/attachment errors precede an invalid/occupied port. A missing `--port` value defaults; an empty supplied port coerces to zero; another flag consumed as its value follows existing Number/listen behavior, not a new missing-option error.

Pair failures are exactly:

```text
A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>.
A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>.
A shareable prevention attachment requires both --prevention <prevention-proof-directory> and --expect-prevention <retained-root-digest>.
```

Default port is 4173; zero permits an ephemeral listener. Retain numeric port range/type/listen errors from the existing server. Extra `--output`, browser flags or `--serve-ms` do not add behavior to this branch. No new public command, host flag or test timeout is introduced. Preserve existing CLI failure formatting, stdout/stderr, exits and hints; intentional hint changes remain 5j work.

### 5.2 Startup and signals

Successful stdout is exactly:

```text
FaultLine read-only Git proof page: <URL>
Press Ctrl+C to stop.
```

Both lines end in LF; `<URL>` is the actual `http://127.0.0.1:<bound-port>`, not literal placeholder text. No browser, additional banner, stop-summary line or successful stderr output.

The TS branch registers **once(SIGINT) only**, then closes the server and resolves the wait. Do not reuse the judge helper unchanged: it also catches SIGTERM, supports a timer and prints a stop summary. Preserve inherited signal/default-action conventions and default SIGTERM behavior rather than treating SIGTERM as graceful. While Close waits on a held connection, a second SIGINT takes the source's restored default action; SIGTERM is not a separately installed listener. Ordinary completed SIGINT shutdown exits successfully without added output. Cover error/default/inherited dispositions through the actual process, not just a callback invocation.

## 6. HTTP and lifecycle boundary

Render the complete page once, before listening. Bind only `127.0.0.1`. GET with pathname `/` returns 200 and the captured HTML. Other routes/methods return 404 `Not found` (with normal HTTP HEAD body suppression). No `/api/analysis`, `/api/rerun`, download, artifact, diagnostic or mutation endpoint exists. After startup, requests do not reopen or reverify the input files; test that changing a copied input does not replace the served snapshot.

| Response | Required application headers |
| --- | --- |
| 200 | `Content-Type: text/html; charset=utf-8`; `Cache-Control: no-store`; CSP below; `Referrer-Policy: no-referrer`; `X-Content-Type-Options: nosniff` |
| 404 | `Content-Type: text/plain; charset=utf-8`; `Cache-Control: no-store`; `X-Content-Type-Options: nosniff` |

CSP is exactly `default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.

Reuse existing request-target handling, framing/Expect/parser limits, keepalive timeouts and Close semantics. Close destroys idle connections and waits for active ones; silent/partial-header and incomplete-body connections differ as in the existing Node-compatible server. Do not add a graceful-shutdown deadline or immediate forced close to make tests finish. Own all test clients/processes and arrange explicit bounded teardown.

The shared transport's already documented exceptions in [KNOWN_DIFFERENCES](../../../difftest/KNOWN_DIFFERENCES.md#the-judge-proof-server-and-browser-opener) apply narrowly: header order, HTTP/1.0 response version, chunk boundaries, documented malformed/absolute/invalid request targets and specified untested timeout coverage. Sharing the server is not a blanket HTTP waiver. The judge command's browser opener, timer warning and SIGINT+SIGTERM listener descriptions do not apply to this command. Any implementation documentation update must distinguish shared transport from command-specific lifecycle without expanding the accepted differences.

## 7. Permanent oracle evidence and tests

### 7.1 Artifact and harness ownership

Add a dedicated `difftest/testdata/serve-bundle/` corpus rather than modify historical evidence. `difftest/gen/gen-serve-bundle.ts` owns actual pinned-TS capture, exact no-write recapture/check modes and regeneration registration. Store `baseline.json`, `golden.jsonl`, full page bytes under `pages/` and the input fixtures/index needed for independent replay under this new root. The detailed plan fixes their case formats and test adapter; it must enumerate them before implementation, not infer expected values from Go output.

`baseline.json` has exactly these provenance fields:

| Field | Value |
| --- | --- |
| `schema` | `faultline.serve-bundle-oracle.v1` |
| `sourceCommit` | `ce99a793af0b020bf1b9cf21114f8d7897b37207` |
| `manifestCommit` | `ff5fe7b1120410e60c321f3ae620cf524c5694ec` |
| `manifestVersion` | `0.1.4` |
| `nodeVersion` | `22.22.2` |

`manifestCommit` records the baseline containing that manifest, not a claim that capture checks it out. Current-tree capture/live preflight checks actual Node, manifest and CLI version; source identity relies on the preserved frozen-source guard/no-diff invariant. Fail a mismatched runtime/manifest without rewriting existing evidence. A changed source baseline requires a separately reviewed provenance transition. Retain exact fixture bytes, expected roots/attachment digests and file inventories so replay never reconstructs its expected answer using the new Go implementation.

`difftest/serve_bundle_test.go` owns permanent tagged Go-only replay and metadata validation; `difftest/serve_bundle_live_test.go` owns actual CLI/HTTP comparison and pinned-oracle preflight. `difftest/release/serve_bundle_test.go` owns untagged actual-binary standalone checks. Focused proofview/prevention/CLI package tests own direct contracts and deterministic boundary tests. Reuse existing tagged adapters/helpers where appropriate, without exposing a public testing command or changing production input handling.

Committed pages are full bytes. CLI failures capture complete stdout/stderr/exit; success cases capture startup, served page and controlled shutdown outcome. Normalize only declared dynamic case roots, the startup URL's actually validated ephemeral port and HTTP Date value under existing harness conventions; do not replace arbitrary numbers, digests, artifact strings or diagnostics. Assert Date format/presence and all other headers. Compare real concrete startup URL against the listener before any capture token substitution. Platform-specific filesystem/signal expectations need source/oracle-derived platform cases, not Linux error text applied to Windows.

Replay validates committed metadata directly; it must not start Node, read TS/manifest files, require Git history or run oracle preflight. A trusted Git executable remains available for the existing proof verifier. Permanent fixture replay and direct tests survive oracle retirement. Add the generator to hosted regeneration/no-drift checks; recapture must compare without overwriting committed evidence. Existing testdata bytes remain unchanged.

### 7.2 Required behavioral coverage

| Dimension | Required cases and assertions |
| --- | --- |
| Base/combination parity | Base only and all eight absent/present attachment combinations; retained-root provided/absent/mismatch; unchanged existing judge/preview output; exact full page/output/status |
| Minimization | Both proof states, null witness/endpoints, either digest mismatch, exact commit/tree/kind mismatch, valid non-proof attachment, expected digest provided/absent/mismatch, changed result/digest on recheck |
| Repair | Parsed investigation/frozen binding, valid legacy packet support under existing schemas, manifest/content mismatch, invalid citations, read/parse/validation failure, changed intermediate/final manifest or either parsed snapshot; exact 3-verify/2-read sequence |
| Prevention | Both classifications, base root/witness/investigation mismatch, absent optional investigation digest, missing base root at library boundary, final root/body change; grounded repaired-run validation still enforced |
| Privacy/rendering | Full TS bytes plus distinctive omitted-field sentinels, escaping/Unicode, counts versus deduplication, citation ordering, prevention-over-repair precedence, prevention-only hero-summary quirk |
| Hostile files | Real verifier rejection of symlink roots/children, special files, wrong path types, extra/missing artifacts, malformed/oversized inputs and retained-digest mismatch according to each source verifier; failure before listen and no repository execution |
| CLI precedence | Exact/equals/duplicate flags, missing and empty values, all incomplete pairs, competing failures, relative paths, optional root quirks, default/zero/invalid/occupied port, ignored timer/browser options and unchanged plain-serve refusal |
| HTTP/lifecycle | GET root/query/path variants, unknown/mutation routes, HEAD/POST/OPTIONS, all security/framing headers, HTTP/1.0/close/keepalive and inherited narrow parser cases; startup failure, ordinary/repeated SIGINT, default SIGTERM and held-connection close behavior |

The four existing TS attachment tests mock minimization/repair verifiers and do not establish prevention, hostile filesystem or snapshot-change coverage. They are source guidance, not substitutes for these tests. Rejecting at the library layer alone does not establish actual CLI failure-before-listen behavior. Callback/seam tests do not replace actual process/server checks.

### 7.3 Native, Node-free and acceptance gates

Run affected untagged/tagged suites, permanent replay, live-oracle/no-drift checks, release no-test-override checks and the existing full exact-head hosted matrices. All six native targets are required: linux/darwin/windows × amd64/arm64. Cross-builds or emulated local containers are not native acceptance. The positional gate remains the unnarrowed Ubuntu amd64 `FAULTLINE_COVERAGE_GATE=1` run, not an assertion that other platforms enabled it.

Standalone tests run the actual built binary outside the repository with Node absent from controlled PATH and no runtime TS/testdata/manifest access. Supply real trusted Git and necessary trusted tools: unlike pure `judge-preview`, an empty PATH is not this verifier's contract. Test direct library/HTTP behavior portably on all targets. The existing `difftest/judge_live_test.go` is Unix-only; its Windows skips do not establish this command's parity. The plan must separately own native Windows CLI/server/termination evidence and platform-appropriate console mechanics; Unix signal tests cannot silently stand in for those. Existing unrelated Windows recorder/PEM/browser gaps remain qualified 5i obligations, not permission to waive this slice's new supported behavior.

Keep exact skip identities/reasons and runtime eligibility; no Node-availability skip, alternate accepted output, length-only comparison, coverage reduction or new broad mask. Preserve historical failures/incomplete attempts. Tests use local scripted fakes only where necessary and `fl-test-key` for any fake key; no real provider operation or real-looking secret. No change to production entropy for deterministic testing.

Implementation uses TDD: record a meaningful failing test before each production change, then verify the minimal implementation and affected regressions pass. Capture-infrastructure checks must not fabricate a claim that the TS oracle failed. Every local shell/build/interpreter invocation stays under five minutes total with owned process-group cleanup, retained stdout/stderr/receipt and no detached/watchdog-reset workaround, broad cleanup or silent retry. Controller owns HOST/runtime serialization. Full over-budget acceptance runs belong on hosted CI. Independent task reviews and one whole-branch review are required under the later approved implementation plan.

## 8. Completion and authorization

5c is complete only when the loader/bindings/panels/CLI satisfy Sections 3–6, additive permanent and live evidence agree with frozen TS, real-verifier hostile/recheck tests pass, six-native standalone/HTTP evidence and exact-head hosted/positional gates close, and independent review accepts the implementation without weakening existing guards. This specification is not evidence that any of those feature gates have run.

The user separately approved the design direction and written specification on 2026-11-01. Written-spec approval initially permitted planning only. After final plan checks the user explicitly authorized implementation when the plan was finished, continued Slice 5 delivery, publication/CI and normal merge without further routine permission prompts. This new instruction supplies authority, not PR #216. Exact-head acceptance/review/native/security gates remain mandatory; public release, protected-source/unfreeze changes, oracle retirement and evidence cleanup are not part of this 5c scope.

`src/`, `package.json` and `pnpm-lock.yaml` are never modified under this slice. Preserve prior goldens and all evidence/workspaces. Work in the current normal checkout on the dedicated documentation branch; no new worktree was requested. When implementation is approved, its plan owns its own execution workspace, not another plan's ledger. Never bypass TruffleHog, change global Git configuration, use the work account/plain repository `gh`, or fall back to another account/remote after a failed push. Required attribution applies to any separately authorized commit or PR.
