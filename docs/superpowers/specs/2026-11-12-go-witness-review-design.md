# Go witness review JSON, browser server, and TTY — design

- **Date:** 2026-11-12
- **Status:** Architecture, compatibility, security, verification, and delivery design approved by the user. Written-spec review and implementation-plan approval remain separate gates. No implementation is authorized by this document.
- **Parent:** [Migration overview](2026-09-25-go-migration-overview.md) and [Slice 5 dependency map](2026-10-30-go-slice5a-cli-surfaces-design.md#5-remaining-slice-5-dependency-map).
- **Baseline:** UI hub merged through PR #228 at `fffd850f672f85739712aad383739a6e5eb29544`, with both post-merge main workflows successful.

## 1. Deliverable and boundary

Port the frozen `witness review` command to Go with byte-compatible JSON, browser, TTY, non-TTY, record, and lifecycle behavior. Use the existing Go witness stores and proofview transport, adding only the transport options needed for review forms and routing.

Included:

- `witness review <proposal-id> [--json | --tty | --browser | --two-step | --port <number>] [--store <directory>] [--draft-store <directory>]`.
- Review opening, integrity verification, exported snapshot verification, confirmation, approval, freeze, and atomic approve+freeze.
- Atomic and two-step browser forms, exact HTML presentation, strict draft binding, and local security policy.
- Real TTY selection, reviewer identity, y/yes/decline behavior, non-TTY fallback, and SIGINT lifecycle.
- Independent API, CLI, HTTP, terminal, process, store, and frozen-TypeScript evidence.

Not included:

- `witness propose --live`, provider/Responses API behavior, overlay-root input, or materializing overlays.
- Guided `investigate --ci-log` / `--resume`, automatic continuation after freeze, or resume interruption behavior.
- Re-porting existing proposal, direct approve/freeze, signing, verification, policy, or implement commands.
- Executing the reviewed command, claiming Docker execution/proof, authenticated reviewer signing, automatic browser opening, or a new UI framework.
- New routes, JSON modes, security checks, transactional semantics, user-facing redesign, help/diagnostic cutover, release packaging, oracle retirement, or TypeScript deletion.
- Protected TypeScript, workflow, package manifest, lockfile, or dependency changes.

The frozen implementation is the behavioral authority. Compatibility deviations require separate explicit approval and documentation; they are not introduced as improvements.

## 2. Frozen CLI contract

### 2.1 Selection and defaults

The witness store defaults to resolved `.faultline/witnesses`. The draft store defaults to `<cwd>/.faultline/incidents`. Browser port defaults to `0`; JavaScript `Number(...)` semantics are preserved and the value must be an integer from `0` through `65535`.

`--json` wins before port or draft processing, so an invalid port is irrelevant in JSON mode. The presence of `--port`, even without `--browser`, forces browser mode. Interactive means both stdin and stdout are TTYs. `forceTty || (!forceBrowser && interactive)` selects the TTY attempt, so `--tty` wins if combined with `--browser`. Unknown extra arguments retain the existing permissive CLI behavior.

`--two-step` or `FAULTLINE_WITNESS_TWO_STEP=1` selects separate Approve and Freeze forms. The default is one atomic Approve + Freeze action.

### 2.2 Modes and exit status

- `--json`: pretty two-space JSON plus a final newline; no server or terminal review. Exit 0, or ordinary CLI error 1.
- Interactive/default or successful forced TTY: print the humanized review and `y/N` prompt, then atomically approve+freeze on affirmative input. Approval exits 0; decline, EOF, or error exits 1.
- Browser, explicit port, or noninteractive without successful forced TTY: start the loopback server and print exact readiness text. No browser launch or stop summary. Ordinary server mode exits 0 after SIGINT.
- Forced `--tty` with non-TTY stdin or stdout: print the refusal to stderr, start the same server, and deliberately exit 1 after SIGINT.

Both server paths subscribe only to SIGINT, close/drain the server, ignore close errors, and print no stop summary. SIGTERM is not subscribed. A second SIGINT during close follows the existing default-action helper; Windows uses its real console default action. Do not substitute the dual-signal judge-demo lifecycle.

### 2.3 JSON output

Emit exactly:

- `status: "LOCAL_READ_ONLY_REVIEW"`.
- `review`: the complete `faultline.witness-review.v1` snapshot.
- `sensitivity`: the frozen warning that exact command and base64 overlay bytes are sensitive.

The JSON includes the full proposal, including `incidentPacket.ciLog`. Only the HTML presentation omits raw CI-log content.

## 3. Review model and persistence

### 3.1 Opening and verification

Add `internal/witness/review.go` with focused APIs for opening a review, verifying an exported snapshot without store I/O, and performing review actions. Values use the existing `jsjson` representation so key order, number formatting, and canonical digest behavior remain explicit.

Opening a review:

- Reads and validates the stored proposal without approving, freezing, executing a command, or materializing overlays.
- Requires the strictly blinded incident-packet shape.
- Verifies incident packet digest, exact UTF-8 command digest, case-insensitive duplicate overlay paths, every decoded overlay byte digest, canonical `localeCompare` overlay ordering, ordered overlay digest, and whole proposal digest.
- Computes `reviewDigest` over the unsigned review.

Error classes and text remain distinct:

- `UNBLINDED_OR_INVALID_PROPOSAL`: every read/schema/store failure collapses to the generic refusal.
- `PROPOSAL_INTEGRITY_FAILURE`: detailed joined integrity errors.
- `INVALID_REVIEW`, `REVIEW_CONFIRMATION_MISMATCH`, and `STALE_REVIEW`: exported review/action handling.

Existing `gitproof.VerifyProposal`, collation, digest, canonical-equality, and schema helpers are reused where their behavior matches. Review-specific error text is adapted at this boundary; shared store/verifier diagnostics are not changed. In particular, the review phrase `proposal packet` must not silently become the existing `persisted packet` text.

### 3.2 Approval, freeze, and confirmation

Approval and freeze require the exact `reviewDigest`. Immediately before writing, the currently stored proposal must canonically equal the reviewed proposal. Reviewer identity is trimmed and limited to 1–512 characters; the optional note is limited to 8000 characters. Input timestamps use the existing clock behavior.

Write-once approval and frozen records are never replaced. Existing safe-directory, non-symlink regular-file, path-containment, and mode `0600` behavior remains in the current Go store.

Atomic means one user action, not transactional rollback: approval is written first, then freeze is attempted. A freeze failure can leave an approval without a frozen record. Do not add rollback, automatic continuation, or binding writes beyond the frozen review behavior.

Existing approval/frozen records are schema-read to render status; GET does not become a stronger full-verification surface.

## 4. HTTP server and presentation

### 4.1 Shared transport extension

Extend `internal/proofview` through an opt-in server configuration rather than changing existing server defaults. Existing Git proof, incident, plain demo, and UI hub framing, path handling, and responses remain unchanged.

The review configuration supplies:

- Actual form-body bytes with a 32 KiB limit and Node-compatible zero/nonzero/chunked handling; existing routes may continue discarding bodies.
- Review-specific pathname processing: strip the query, collapse repeated slashes, and use the parsed pathname for absolute HTTP(S) request targets. Do not inherit the shared backslash, dot-segment, or scheme-relative normalization.
- Existing listener, connection accounting, response completion, and close/drain behavior.

After body receipt, each review request's synchronous handler section is serialized per server. This reproduces Node's synchronous read/verify/write sequence and prevents concurrent approvals from producing data races or multiple apparent successes. The underlying store's write-once checks remain mandatory even with serialization.

### 4.2 State and draft binding

Every GET and mutation reopens the proposal and rereads current approval/frozen state.

When a draft store is supplied:

- A missing draft file is treated as no linked draft.
- Other read/verification failures are fatal except `ENOENT`.
- The draft must have `review.witnessState === "PROPOSED"`.
- Proposal ID, proposal digest, incident packet digest, and command digest must match.

This strict binding is browser-only. The TTY catches every draft-read error and proceeds with no linked draft; it does not perform the browser's proposal-binding comparison.

### 4.3 Routes and forms

- `GET /`: current 200 page, or 409 refusal page.
- Atomic mode: form posts to `/approve-freeze`; direct legacy `/approve` and `/freeze` posts remain accepted.
- Two-step mode: forms post to `/approve`, then `/freeze`; `/approve-freeze` is 404.
- Everything else is 404 plain text `Not found\n`.
- Query strings are ignored; repeated slashes are collapsed.
- The form body limit is 32 KiB. URL-encoded decoding keeps the last duplicate key. No content-type enforcement is added.
- Atomic and approval actions require `acknowledged=exact-witness-reviewed`.

Token failure is 403. Acknowledgement, stale state, integrity, and write failures are 409. Do not add Host, Origin, Sec-Fetch-Site, CORS, or route policies absent from the frozen server.

### 4.4 Token and response headers

Generate 32 cryptographically random bytes once per server, hex encode them, and compare in constant time. Listen on TCP loopback `127.0.0.1`, using an ephemeral port by default. Production token randomness is not seeded or environment-overridden.

HTML responses, including failure pages, carry these exact header values:

- `Cache-Control: no-store`.
- CSP `default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`.
- `Referrer-Policy: no-referrer`.
- `X-Content-Type-Options: nosniff`.

Every dynamic value is escaped. Pages contain no scripts or external assets.

### 4.5 Presentation

Port the frozen humanization exactly:

- Decode overlays as UTF-8 only when re-encoding reproduces the bytes and the text contains no NUL; otherwise retain canonical base64.
- Browser displays complete UTF-8 text; binary remains base64. TTY truncates each UTF-8 overlay to its first 40 split lines.
- HTML omits raw CI-log content but displays the exact command and overlays.
- Digests are collapsed under `Evidence record`.
- Cage display is descriptive only: `Docker · network OFF · timeout Ns`; review does not establish execution or proof.
- Range display makes at most three synchronous Git calls, each with a 5-second timeout and 2 MiB buffer, shows at most 40 commits, and falls back to abbreviated endpoints when metadata is unavailable.
- Git timestamp parsing and relative-age output accept the frozen Git `%aI`/stored timestamp inputs and preserve the frozen fallback, including platform-independent formatting.

Use existing process execution, UTF-8, base64, trim, collation, digest, and HTML helpers where their behavior matches. Independent expectations cover gaps such as Git timestamp forms that a canonical-ISO-only parser cannot handle.

## 5. TTY contract

Extract only the existing Unix `isatty` and Windows `GetConsoleMode` terminal checks from `cmd/fl` into a small `internal/term` package. The remainder of `cmd/fl` handle classification, output writes, virtual-terminal behavior, and error mapping remains unchanged. CLI terminal detection is injectable for focused tests; real PTY/console evidence is still required.

The TTY flow:

- Prints the humanized review before prompting.
- Defaults reviewer identity from `git config --get user.name` in the current working directory, falling back to `local-reviewer`.
- Rejects an empty reviewer identity.
- Accepts only trimmed, case-insensitive `y` or `yes`; everything else declines without writing records.
- Prints `Frozen <id> · approvedBy <identity> · frozenDigest <digest>\n` after success.

Piped `yes` must never auto-approve. Test stdin-non-TTY and stdout-non-TTY independently, plus decline and EOF. Forced-TTY fallback prints the refusal to stderr and generic readiness to stdout, then exits 1 after SIGINT.

## 6. Files and ownership

Expected scope:

- Add `internal/witness/review.go` and focused tests.
- Add `internal/proofview/witness_review.go`, `witness_review_server.go`, and focused tests.
- Add narrowly scoped transport options/tests without changing existing server behavior.
- Add `internal/term` platform terminal detection and tests; adjust only the corresponding `cmd/fl` detection call sites.
- Add `internal/cli/witness_review.go`, terminal/lifecycle files and tests; change only the review dispatch seam in `internal/cli/witness.go`.
- Add independent API, HTML, CLI, HTTP, terminal, and process goldens/oracles under `difftest` and `difftest/testdata`.
- Update `difftest/KNOWN_DIFFERENCES.md`, the migration overview, and Slice 5 dependency map only with corresponding evidence.

Do not modify live proposal, overlay input, native execution, release, protected TypeScript, workflow, package manifest, or dependency files.

## 7. Verification

### 7.1 Independent model and store contracts

Use meaningful RED before production, then GREEN. Cover every review integrity check, generic versus detailed errors, exported review verification without store I/O, confirmation mismatch, stale current proposal, reviewer/note limits, write-once behavior, and approval-then-freeze partial failure.

Verify resulting approval and frozen records independently, including digests, modes, timestamps, and current-proposal equality. No expected value is generated from the Go implementation under test.

### 7.2 JSON, CLI, and diagnostics

Add propose-then-review writer/golden cases for exact JSON, stdout/stderr, exit status, store bytes, and precedence rules. Include missing ID, invalid stores, JSON with otherwise-invalid port, browser/port selection, and forced-TTY fallback.

Every new diagnostic has committed golden text and an actual replay through the coverage-instrumented `fl` binary. Merely reading a golden in a unit test is not position-coverage evidence. Preserve ordinary/tagged literal and native-coverage gates.

### 7.3 HTTP frozen-TypeScript comparison

Compare Go and frozen TypeScript status, headers, bodies, CLI output, and exit status. Normalize only actual ephemeral ports and each side's independently extracted token. Do not broadly mask digests, timestamps, or errors.

Cover empty and populated pages, atomic/two-step routes, legacy direct actions, token and acknowledgement failures, missing/mismatched drafts, oversized and chunked forms, duplicate keys, query strings, repeated slashes, absolute targets, malformed/stale state, concurrent mutations, close/drain, and SIGINT. Verify record state after every mutation. Static pages also have independently authored goldens.

### 7.4 Terminal and platform evidence

Use real PTY/console tests for terminal selection, y/yes/decline/EOF, and reviewer identity. Pipes alone prove only non-TTY behavior. Prove piped `yes` cannot approve, forced-TTY fallback exits 1, ordinary server SIGINT exits 0, and a second SIGINT follows the platform default.

SIGTERM remains default. Windows `TerminateProcess` is not presented as graceful SIGTERM evidence. Preserve CRLF command/overlay bytes, canonical base64, locale collation, non-symlink stores, modes, and hidden process behavior. Add no platform skip or waiver to substitute for native evidence.

### 7.5 Final gates and review

Run affected ordinary, `faultline_test`, race, vet, literal, position-coverage, native-coverage, live-oracle, and actual-process gates. Preserve all prior server, store, renderer, and writer behavior. Keep every shell command below five minutes and retain every failure, skip, timeout, and incomplete run.

Cross-compile affected packages and tests for Linux, macOS, and Windows on AMD64/ARM64, then require hosted execution on those targets. Compilation is not execution.

Every task receives independent spec/quality review on the controller's current model. A final whole-branch review on that same model covers spec compliance, security, correctness, test independence, compatibility, and deferred minors. Resolve Critical/Important findings before handoff.

## 8. Completion and authorization

This subproject is complete only when the frozen JSON, browser, TTY, non-TTY, persistence, lifecycle, security, and hosted acceptance contracts pass. It does not complete 5f, 5g, the rest of Slice 5, release readiness, or TypeScript retirement.

Work occurs on a dedicated branch/worktree from the stated baseline. No automatic merge, release, protected-source edit, evidence deletion, or oracle retirement is authorized.

Writing and committing this spec authorizes only written-spec review and then preparation of an implementation plan. Implementation requires approval of that plan. Publication, ready/merge transitions, and later subprojects retain separate authorization boundaries.
