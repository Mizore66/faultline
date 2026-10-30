# Post-submission - Broaden CDX-09 thread ID provenance

**Status:** parked until Devpost is submitted and `SUBMISSION_FROZEN` is lifted.  
**Rule:** touches `src/**` (and tests). Must not merge to `main` while freeze is active.  
**Judging pin context:** implement against post-freeze `main`; never retarget or move `v0.1.10-buildweek`.

## Intent

CDX-09 already binds **optional** Codex thread identifiers into Git proof manifests and prevention packages (identifiers only - never transcripts). Broaden that provenance so turn-proof packages and primary investigation CLI paths can carry the same digest-bound IDs consistently, with tamper tests and clear optional-vs-required semantics.

## Current baseline (do not regress)

| Surface | Field(s) | Required? | Notes |
|---------|----------|-----------|-------|
| Git proof `manifest.json` | `codex.witnessDraftThreadId`, `codex.repairThreadId` | Optional | Zod: min 1, max 160, `/^[A-Za-z0-9][A-Za-z0-9._:-]*$/`; root-bound |
| Turn proof `manifest.json` | same schema shape | Optional | Schema/write support exists; CLI investigate path does not pass `codex` today |
| Prevention `prevention.json` | `codexThreadId` | Optional | max 256; root-bound |
| Ledger `SESSION_STARTED` | `payload.codexThreadId` | Optional | IdentifierSchema (max 160) |
| Codex loop artifact | `.faultline/.../codex-thread.json` | Local runner output | May include previews; **must not** be copied wholesale into proof manifests |

Existing tests to preserve:

- `tests/git-proof-bundle.test.ts` - optional IDs bind; tamper fails closed; no transcript/prompt/messages text
- `tests/prevention-proof.test.ts` - `codexThreadId` round-trip + tamper
- `tests/backlog-closures.test.ts` - IDs may exist without transcript bodies

## Spec (implementation must define and enforce)

### 1. Which manifests / artifacts receive thread IDs

| Artifact | Fields | Writer path to wire |
|----------|--------|---------------------|
| Git investigation `manifest.json` | `codex.witnessDraftThreadId?`, `codex.repairThreadId?` | CLI / investigate git when IDs are known |
| Turn investigation `manifest.json` | same | `writeTurnInvestigationProofBundle` options from `fl investigate turns` / record loop |
| Prevention package `prevention.json` | `codexThreadId?` | already via `fl prevention write --codex-thread-id` |
| Lifecycle ledger | `SESSION_STARTED.payload.codexThreadId?` | already via `fl record init --thread` |

Do **not** embed thread IDs into `hashes.txt` as separate artifacts. Do **not** ship `codex-thread.json` preview blobs inside proof packages.

### 2. Optional vs required

- All thread ID fields remain **optional** for Build Week compatibility.
- If present, they are **integrity-bound** (changing the field without rebuilding the package root must fail verify).
- Absence is valid; verifiers must not require IDs.

### 3. Length and character rules

Unify on the Git/turn IdentifierSchema unless a documented exception is required:

- Length: 1..160 code units (prevention may keep max 256 only if already shipped - prefer documenting the asymmetry or converging in a follow-up)
- Pattern: `^[A-Za-z0-9][A-Za-z0-9._:-]*$`
- Reject empty string, whitespace, path separators, quotes, control characters

### 4. Repository / session binding

- Thread IDs are **opaque foreign keys** to Codex/session systems, not proof that the session mutated the tree.
- When recording: bind the ID observed for that session/run; do not invent IDs.
- Prefer one witness-draft ID and one repair ID max per manifest object.

### 5. Privacy and redaction

- Identifiers only - no prompts, messages, tool payloads, diffs, or stdout/stderr previews in manifests.
- Thread IDs are not treated as authentication credentials, but they are potentially sensitive correlation metadata. Store and log them minimally; never retain prompts, messages, payload previews, or account-linked context.
- Do not log full thread payloads in CI artifacts.
- Redaction scanners continue to apply to any adjacent text channels; IDs must not become a vehicle for smuggling content.

### 6. Tamper semantics

Verify must fail closed when:

- A present thread ID string is altered
- `codex` object keys are added/removed after sealing without re-hash
- Manifest JSON is re-serialized with different ID values but stale `ROOT.sha256`

### 7. Backward compatibility

- Packages without `codex` remain valid.
- Unknown future `codex.*` keys: follow existing manifest strictness (prefer reject-unknown on write; verify behavior must match current Zod schemas unless explicitly versioned).

### 8. Verifier behavior: absent or malformed IDs

| Case | Expected |
|------|----------|
| Field absent | Valid |
| Field `null` | Invalid (schema fail) |
| Empty string | Invalid |
| Over-long / bad charset | Invalid on write and on verify parse |
| Malformed JSON around `codex` | Invalid |

### 9. Exact regression / package-tampering tests (required)

Add or extend:

1. **Turn-proof CDX-09 bind + tamper** in `tests/turn-proof-bundle.test.ts` (parity with git-proof test).
2. **CLI wiring smoke**: when investigate/record supplies a thread ID, the written turn (and/or git) manifest contains it.
3. **Honesty**: presence of IDs never implies transcript retention (`tests/backlog-closures.test.ts` pattern).
4. **Negative**: malformed ID rejected before package write.

## Non-goals

- Do not promote `EXPERIMENTAL_TURN` to `TURN_PROOF`
- Do not claim Codex repair authorship from an ID alone
- Do not fetch remote Codex transcripts
- Do not merge under `SUBMISSION_FROZEN`

## Rollback

Revert the implementation PR. Optional fields disappear from new writes; old packages without IDs remain verifiable.
