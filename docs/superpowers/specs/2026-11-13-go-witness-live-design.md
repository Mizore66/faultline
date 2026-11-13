# Go witness live proposals and overlay input — design

- **Date:** 2026-11-13
- **Status:** Architecture, compatibility, security, verification, and delivery design approved by the user, including the GPT-6.1 deviation. Written-spec review and implementation-plan approval remain separate gates. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md) and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** witness review follow-up merged through PR #230 at `30559d18479615aac189a99fb82c3a05ba4b9fb1`, with both post-merge main workflows successful.

## 1. Deliverable and boundary

Port frozen `witness propose --live` and model-selected `--overlay-root` input to Go. Generate and persist proposals using scripted local HTTP only, with exact selection, redaction, request/response, overlay, store, output, and error behavior.

Included:

- Live preview with the built-in incident and persisted live proposals from `--incident`.
- Four-field blind packet validation, redaction, API-key checks, Responses API requests, and response extraction.
- Separate provider-boundary and storage schemas, overlay reading, and existing proposal persistence.
- Independent request/response/error, overlay, store, CLI, and process evidence.

Not included:

- Real provider operations, provider SDK dependencies, application retries, or new timeout options.
- Review, approval, freeze, command execution, overlay materialization, or proof claims.
- Guided `investigate --ci-log` / `--resume` (5g), direct non-live proposal changes, or re-porting existing review behavior.
- Stronger path/no-follow rules, input-size limits, source-selection validation, model-output redaction, or other product tightening.
- Protected TypeScript, dependency, release, or oracle-retirement changes. Workflow edits require separate explicit approval.

The frozen implementation is the behavioral authority except for the explicitly approved deviation in §2. Any other deviation requires separate approval and documentation.

## 2. Approved GPT-6.1 deviation

The default provider model is **`gpt-6.1`**, replacing frozen `gpt-5.6` only when the option is nullish. An explicitly empty `--model` remains empty.

Persisted success output uses **`source: "GPT-6.1"`**, including when `--model` is customized. The source label is not derived from the custom model. No other request, schema, output, or persistence behavior changes as part of this deviation.

## 3. CLI selection and operation order

### 3.1 Source selection

- `--live --incident <truthy-file>` uses the persisted live-model path and ignores `--input`.
- `--live` without a truthy incident and without input calls the model with the fixed built-in refund incident, prints the preview, and writes no store record.
- `--live --input proposal.json` without a truthy incident uses the offline persisted-input path and makes no provider call.
- No live flag and no input prints the deterministic sample, even if an incident was supplied.
- Offline input ignores `--overlay-root`. Unknown extra arguments retain permissive behavior.

### 3.2 Persisted path order

Preserve this exact sequence and its side effects:

1. Read and JSON-parse `--incident`, wrapping failures as `Unable to read JSON input <supplied>: <message>`.
2. Require `--proposal-id`.
3. Require `--overlay-root`, without stating or resolving it yet.
4. Validate/redact the incident, check the API key, and perform the provider request.
5. Redact the original incident again.
6. Read every model-selected overlay sequentially.
7. Build and write the proposal.
8. Print success JSON.

An invalid or missing overlay root is discovered after a successful provider request. Required-argument and blind-schema failures make zero HTTP requests. Proposal files and store directories are not created before the existing `Propose` boundary.

A second identical live invocation performs another provider/read cycle before immutable-record refusal.

## 4. Blinding, redaction, and provider boundary

### 4.1 Input and redaction

The AI-boundary blind schema accepts exactly four non-empty strings and rejects extra keys: `symptom`, `ciLog`, `repositoryLanguage`, and `repositorySummary`. It has no maximum lengths. Do not apply storage limits at this stage.

The live flow parses the blind packet, calls the existing redactor, reparses the redacted value against the same schema, then checks `options.apiKey ?? OPENAI_API_KEY`. A missing/empty key produces:

`OPENAI_API_KEY is required for a live GPT-5.6 witness proposal. The judge demo does not require one.`

The frozen error text is retained; it does not become GPT-6.1.

Only the redacted packet is sent to the provider. Return the redacted packet digest and first redaction report. The persisted branch independently redacts the original incident again and uses the first report as output `privacy`.

Redaction recursively produces a fresh locale-sorted object and a `LIMITED` digest-bearing occurrence report, never a complete-secret-discovery claim. Overlay bytes and generated behavior/command text are not redacted.

### 4.2 Request

Extract repair’s fetch-compatible HTTP behavior into a neutral `internal/openai` package without changing repair semantics. Use a fixed production Responses API endpoint; any loopback override exists only in `faultline_test`.

POST with bearer authorization and JSON content type. No application retries or new timeout options are added. Preserve the existing undici-like headers, redirects, compression, decoding, and error behavior rather than substituting default `net/http` semantics.

The ordered JSON body contains:

- `model`.
- `instructions`: the frozen joined clauses requiring an executable witness from a blinded packet, forbidding candidate knowledge/verdicts/culprits/causal claims, and requiring structured output only.
- `input`: `JSON.stringify(redactedPacket)` as a JSON string, not an object.
- `text.format`: `type: "json_schema"`, `name: "faultline_witness_proposal"`, `strict: true`, and the provider schema.

The provider schema requires all six fields and disallows additional properties:

- `behavior`, `command`: strings.
- `overlayFiles`: string array, maximum 5.
- `expectedExitCode`: integer 0–255.
- `timeoutSeconds`: integer 1–120.
- `safetyNotes`: string array, maximum 8.

There are no provider-schema string minimums or array minimum items. Response validation additionally requires non-empty behavior and command. Empty overlay/safety-note arrays and empty path/note strings therefore pass this boundary until later overlay/store processing.

### 4.3 Response and errors

Any 2xx response is accepted. Non-2xx produces `OpenAI Responses request failed: <status> <body>`. Successful bodies are JSON-parsed regardless of content type.

Use the existing response extractor for exactly one non-empty raw `output[].content[].text` of type `output_text`, the SDK `output_text` fast path, or matching raw and fast-path values. Missing, empty, multiple, conflicting, or malformed candidates fail closed. Response status and output item type are not additionally restricted.

Parse and validate the extracted text against the strict six-field schema. Preserve transport, decoding, schema, and extraction error text and evaluation order.

## 5. Overlay input

Implement `internal/witness/overlay_input.go` using existing Node-compatible filesystem/path helpers, not generic `filepath` substitutions.

The root itself must be a real, non-symlink directory identified by `lstat`. Ancestors of that root are not additionally rejected.

For every model-selected path:

- Resolve the target beneath root and require a non-empty relative path.
- Reject absolute/escaping paths, every backslash, and relative results beginning `..`.
- Preserve internal normalization: `a/./b` becomes `a/b`, and `a/../b` becomes `b`; this is not a syntactic dot-segment ban.
- Require every parent beneath root to be a real non-symlink directory and the leaf to be a regular non-symlink file.
- Read raw bytes sequentially and return canonical base64 with normalized `/` path separators.

Duplicate selections reread the same file before the store’s duplicate-path refusal. No overlay-input byte cap is added. Storage later limits base64 text to 1,400,000 characters and normalized path length to 512. Preserve the frozen lstat/read TOCTOU window rather than substituting a stronger open-at/no-follow protocol.

## 6. Persistence and output

Pass the existing proposal writer:

- Caller `proposalId`.
- `proposalOrigin: "MODEL"`.
- The redacted incident packet.
- Generated behavior and command.
- Bytes read from generated `overlayFiles`.
- Hard-coded `network: "disabled"` and `credentials: "redacted"`.
- Generated `timeoutSeconds`.

`expectedExitCode` and `safetyNotes` are required and validated, then discarded. They are neither stored nor printed.

The storage schema is intentionally stricter: maximum packet field lengths are 32,000 / 500,000 / 200 / 8,000 respectively, so persistence can fail after provider success. Existing storage behavior handles case-insensitive duplicate paths, canonical base64, locale sorting, exact command/overlay/incident/proposal digests, timestamps, and write-once records.

Canonical JSON plus newline is written with mode `0600` and `wx`; store directories use `0700`. Existing records are never replaced. Full input parsing and duplicate checks occur before store creation, and all selected overlays have already been read.

Persisted success prints two-space JSON plus newline with keys in order:

`status: "PROPOSED"`, `source: "GPT-6.1"`, resolved `store`, `proposalId`, `proposalDigest`, `incidentPacketDigest`, `privacy`.

The no-incident live preview instead prints `proposal`, `incidentPacketDigest`, and `redaction`, with no `status`, `source`, `store`, or `proposalDigest`, and writes nothing.

Success exits 0. Every read, validation, key, fetch, response, overlay, or store failure exits 1 with no success JSON and `FaultLine error: <message>\nNext: pnpm fl help\n` on stderr.

## 7. Files and ownership

Expected scope:

- Add neutral `internal/openai` transport files by extracting existing repair behavior; adjust repair call sites without changing its tests or output.
- Add `internal/witness/live.go`, `overlay_input.go`, and focused tests.
- Add `internal/cli/witness_live.go` and process/golden tests; change only live proposal dispatch/seams in `internal/cli/witness.go` and the stale unported expectation.
- Add loopback request/response/error, overlay, writer, and native process evidence under `difftest` and `difftest/testdata`.
- Update `difftest/KNOWN_DIFFERENCES.md`, the migration overview, and Slice 5 dependency map only with corresponding evidence.

Do not modify guided investigation, review, native execution, release, protected TypeScript, package manifests, lockfiles, or dependencies.

## 8. Verification

### 8.1 Selection, request, and response

Cover every source-selection row, required-argument order, default/custom/empty model, custom store, and proof that argument/blind-schema failures make zero HTTP requests.

Assert exact request bytes and field order, complete provider schema, nested redacted `input` string, authorization/content-type, Unicode/lone-surrogate escaping, and absence of secrets. Cover raw, fast-path, matching-both, missing/empty/multiple/conflicting candidates, non-2xx empty/BOM/invalid-UTF-8 bodies, malformed outer/model JSON, model-schema limits, and transport errors.

### 8.2 Persistence and side effects

Assert exact stdout/stderr/exit, request records, seeded timestamps, complete proposal bytes/modes, redacted packet, normalized/sorted overlays, and all digests. Require `source: "GPT-6.1"` and explicit absence of `expectedExitCode` and `safetyNotes`.

Prove provider success precedes overlay failures, no proposal file exists on earlier failures, store directories are not created before `Propose`, and a second identical invocation repeats provider/read work before write-once refusal.

### 8.3 Overlay and platform behavior

Cover nested binary/CRLF, empty, absolute, escaping, backslash, dot-normalized, duplicate/case-alias, and over-limit paths. Exercise missing/file/symlink roots, parent file/symlink, missing/directory/symlink leaves, and sequential rereads.

Use native Windows junction/reparse-point comparisons and raw Node filesystem error text. Preserve CRLF bytes, canonical base64, locale collation, non-symlink stores, modes, and hidden process behavior. Do not add platform skips to replace native evidence.

### 8.4 Final gates and review

Use meaningful RED before production, then focused GREEN, race, vet, ordinary/tagged suites, literal/position/native coverage, actual processes, and frozen-TypeScript loopback comparisons. Every new diagnostic has committed golden text and actual covered-`fl` replay; golden reads alone are not position coverage.

Preserve all repair transport tests and prior writer cases. Regenerate manifests with independently recomputed totals if cases are added. Keep every command below five minutes and retain every failure/skip/timeout/incomplete run.

Cross-compile affected packages/tests for Linux, macOS, and Windows on AMD64/ARM64, then require hosted execution. Compilation is not execution.

Every task receives independent spec/quality review on the controller's current model. A final whole-branch review on that model covers spec compliance, security, correctness, test independence, and deferred minors.

## 9. Completion and authorization

This subproject is complete only when frozen selection, redaction, request/response/error, overlay, persistence, lifecycle, and hosted acceptance contracts pass, including the explicitly approved GPT-6.1 model/source deviation. It does not complete 5g, the rest of Slice 5, release readiness, or TypeScript retirement.

Work occurs on a dedicated branch/worktree from the stated baseline. No real provider operation, automatic merge, release, protected-source edit, evidence deletion, or oracle retirement is authorized.

Writing and committing this spec authorizes only written-spec review and then preparation of an implementation plan. Implementation requires approval of that plan. Publication, ready/merge transitions, and later subprojects retain separate authorization boundaries.
