# Post-submission — Hostile-verifier corpus parity for turn packages

**Status:** parked until Devpost is submitted and `SUBMISSION_FROZEN` is lifted.  
**Rule:** primarily tests (+ small verifier fixes if gaps are real bugs). Must not merge to `main` while freeze is active if the change is product code; test-only PRs may follow freeze policy.  
**Judging pin context:** implement against post-freeze `main`; never retarget or move `v0.1.10-buildweek`.

## Intent

Bring turn-proof offline verification to **corpus parity** with the COMMIT_PROOF hostile Git verifier (`tests/hostile-git-verifier-corpus.test.ts` / SEC-01), and close any real asymmetries in `src/turn-proof-bundle.ts` (for example catalog parse caps).

## Current baseline

### Git hostile corpus (keep as the parity oracle)

`tests/hostile-git-verifier-corpus.test.ts` already covers:

- malformed manifest JSON
- traversal catalog path (`../outside`)
- hash/root confusion (fake digest + mismatched `ROOT.sha256` / expected root)
- undeclared physical file (`sneaky.txt`)
- archive-limit catalog (> `MAX_GIT_PROOF_ARTIFACTS` 2048)
- oversized `hashes.txt` (> 4 MiB)

Related property fuzz: `tests/rigor-properties.test.ts` (absolute / Windows / nested / missing paths).

### Turn coverage today (incomplete vs Git)

`tests/turn-proof-bundle.test.ts` covers semantic/integrity cases (injected runner observations, legacy exit labels, rehashed contradiction, corrupted bytes, broken `hashes.txt`, external root mismatch) but **lacks** a dedicated hostile corpus mirroring the Git list above.

Known asymmetry to evaluate during implementation:

- `MAX_TURN_PROOF_ARTIFACTS = 2048` is enforced on physical `collectFiles`
- Git `parseHashCatalog` caps catalog entries; turn `parseHashCatalog` should match that fail-closed behavior

## Threat model (turn packages)

Attacker supplies a directory claiming to be a turn investigation proof bundle. Goals to deny:

1. Path escape / file overwrite on verify host
2. Accepting undeclared or substituted bytes
3. Resource exhaustion (catalog bombs, huge hash files, symlink cycles)
4. Downgrade / grade confusion (claiming stronger evidence than sealed)
5. Run-ID / root reuse tricks that make two different trees verify as one package
6. Contradictory outcomes that still verify as valid

Verifier must **fail closed** (valid=false, errors populated) without throwing unhandled exceptions for corpus cases.

## Required corpus cases (enumerate and implement)

Create `tests/hostile-turn-verifier-corpus.test.ts` (name may vary) with explicit cases:

| # | Case | Expect |
|---|------|--------|
| 1 | Missing required files (`manifest.json` / `hashes.txt` / `ROOT.sha256`) | fail closed |
| 2 | Extra undeclared physical file | fail closed |
| 3 | Traversal paths in catalog (`../outside`, nested `..`) | fail closed |
| 4 | Absolute / Windows-style paths (`/abs`, `C:/outside`, `dir\\escape`) | fail closed |
| 5 | Symlink or special file in bundle tree | fail closed |
| 6 | Duplicate JSON keys in manifest (if parser distinguishable; otherwise document parser behavior and add closest fail-closed case) | fail closed or documented reject |
| 7 | Oversized `hashes.txt` (over existing size cap) | fail closed |
| 8 | Catalog entry count over `MAX_TURN_PROOF_ARTIFACTS` | fail closed (fix parse cap if needed) |
| 9 | Root mismatch (`ROOT.sha256` vs hashes body; external `--expect-root`) | fail closed |
| 10 | Run-ID reuse / metadata contradiction (same run id, different sealed tree) | fail closed |
| 11 | Contradictory outcomes (PASS/FAIL labels disagree with sealed observations) | fail closed |
| 12 | Git pack / object tampering if turn bundles embed git objects — else N/A with note | fail closed or N/A |
| 13 | Downgrade attempts (strip grade fields / rename to imply COMMIT_PROOF) | fail closed |
| 14 | Archive / compression bombs if archives are accepted — else N/A (directory verifier only) | fail closed or N/A |
| 15 | Malformed JSON in manifest / investigation / frozen witness metadata | fail closed |

Parity requirement: every **applicable** Git hostile case has a turn analogue, or an explicit N/A justification in this doc and the test file header.

## Compatibility requirements

- Do not weaken Git hostile corpus
- Preserve existing turn semantic tests
- Keep artifact path safety rules aligned (reject absolute, backslash, NUL, `.`, `..`)
- Retain size caps unless a measured reason exists to change them (document any change)

## Acceptance criteria

1. New hostile turn corpus green in CI
2. Any verifier bug found while adding cases is fixed with a regression test
3. Catalog parse cap parity with Git if missing
4. Short note in `docs/security-model.md` or this file listing turn corpus coverage (honest: not a formal audit)

## Non-goals

- Full fuzzing farm / AFL
- Claiming "security audited"
- Promoting turn evidence grade
- Merging under `SUBMISSION_FROZEN` for `src/**` changes

## Rollback

Revert the test (and any verifier fix) PR. No pin involvement.
