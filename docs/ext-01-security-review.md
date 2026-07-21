# EXT-01 independent security review

**Backlog:** EXT-01 / [#149](https://github.com/Mizore66/faultline/issues/149)  
**Reviewer:** Chang Kai Zhe (independent external reviewer; same partner responsible for the MUMBCS external turn-path validation)  
**Review date:** 2026-07-21  
**Reviewed `main` commit:** `faa98c0757e14250edb4a27dbada94fac9fa5d21`  
**Signed judging release in scope at review time:** `v0.1.9-buildweek` → `3ca1bb8adaebf163fe633642a0a4ba20dfb7810a`  
**Current judging pin (post-disposition):** `v0.1.10-buildweek` — re-cut so judges receive FL-SEC-001/002/003 fixes (`14ad89d` and follow-ons). `v0.1.9-buildweek` is retained historical and must not move.

This page is the retained public-safe summary and disposition record. Full private review artifacts (command log, threat model, PoC fixtures) stay with the reviewer/maintainer handoff and are not required to cite the findings below.

## Scope and method

Independent review of hostile-repository / host-Git execution, Docker proof isolation, witness immutability, proof-package integrity, lifecycle ledgers, repair evidence, secret handling, local UI binding, and CI/CD supply chain.

Method highlights:

- Fresh pull of `main` at the reviewed commit; frozen lockfile install
- Typecheck, build, package smoke, `pnpm audit --prod`, coverage matrix, judge-demo export, proof verification, targeted security Vitest batch
- Code inspection across Git, Docker, verifier, overlay, ledger, repair, UI, and workflow paths
- Benign local adversarial fixtures for host-Git and witness-result channels

Limits recorded by the reviewer: Docker unavailable on the review machine (Docker integration tests not run); full `pnpm test` interrupted without a final summary after long quiet tails.

## Findings

| ID | Severity | Summary |
| --- | --- | --- |
| FL-SEC-001 | Critical | Unhardened Git in `captureGitCleanCheckpoint` allowed repository-local `core.fsmonitor` host command execution during `fl record checkpoint` / attach / stdin capture |
| FL-SEC-002 | High | Proof-bearing `PREDICATE_PASS` / `PREDICATE_FAIL` accepted from ordinary stdout without exit-code consistency, so relayed repository output could forge PASS after a failing run |
| FL-SEC-003 | Medium | Root composite `action.yml` used mutable `actions/setup-node@v5` / `pnpm/action-setup@v6` tags while CI workflows already pin SHAs |

## Disposition

| ID | Status | Fix |
| --- | --- | --- |
| FL-SEC-001 | **Fixed** on `fix/security-review-fl-sec-001-003` | Default checkpoint capture uses hardened host-command Git overrides + null system/global config; regression in `tests/fl-record-checkpoint-fsmonitor.test.ts` |
| FL-SEC-002 | **Mitigated** on the same branch | Proof-bearing PASS/FAIL require exit-code consistency; conflicting structured records fail closed as `HARNESS_ERROR`; regression in `tests/witness-stdout-spoof.test.ts`. Residual: a full wrapper-owned / nonce-authenticated result channel remains future hardening beyond this disposition |
| FL-SEC-003 | **Fixed** on the same branch | Root `action.yml` pinned to the same SHAs as `verify.yml`; `tests/workflow-pins.test.ts` covers root `action.yml` |

Hackathon / controlled-demo posture from the review: acceptable for trusted fixtures and reviewed witnesses once Critical/High items are fixed and regression-tested. Production claim for arbitrary untrusted repositories remains out of scope until residual witness-channel hardening is complete.

## Claims this record supports

- An independent external security review was performed and attributed.
- Findings were written down with severity and reproduction.
- Disposition was published with shipped fix commits and regression tests.

## Claims this record does not support

- That every recommended long-term redesign (dedicated protected witness-result channel) is already implemented.
- That Docker integration was re-validated on the reviewer's machine (Docker was unavailable there).
- That multi-OS soak or other people-gated EXT items are closed by this review alone.
