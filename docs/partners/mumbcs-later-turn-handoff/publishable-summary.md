# Publishable MUMBCS x FaultLine summary: production-code regression caught

Consent status: MUMBCS may be named. Screenshots, terminal captures, retained digests, and this summary may be published. Consent was provided in the Codex thread on 2026-07-19.

## Short version

In one consented external run on `monashblockchain/MUMBCS`, FaultLine caught a regression in production slug validation: the frozen witness stayed green through Turn 2 and failed after Turn 3 changed `src/lib/slugify.ts`. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This shows FaultLine can freeze expected production behavior, replay the agent-assisted change sequence, and name the first bad turn.

## Evidence to cite

| Evidence | Value |
| --- | --- |
| Repository | `monashblockchain/MUMBCS` |
| Branch | `faultline/later-turn-demo` |
| Production file | `src/lib/slugify.ts` |
| Regression | `isValidSlug("")` became accepted after Turn 3 |
| Proposal id | `mumbcs-check-slug` |
| Frozen witness | `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133` |
| Witness overlay hash | `sha256:6a22d755f3cba45149eb4caf7a41cc7260112887c2b67b813a4d7a4760f0ece0` |
| Docker image | `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2` |
| Proof root | `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` |
| Verification result | `valid: true`, `externalRootStatus: MATCH` |
| Located transition | Turn 2 PASS -> Turn 3 FAIL |
| Attribution | `introduction.status: ATTRIBUTED`, `introduction.turnOrdinal: 3` |
| Evidence grade | `EXPERIMENTAL_TURN` |
| Runs | 12 Docker-isolated runs, 3 executions per stable state |
| Sidecar recording | `SIDECAR_RECORDINGS_READY`, 11 events |

## Suggested public wording

FaultLine caught a production-code regression in MUMBCS. A frozen witness over `src/lib/slugify.ts` stayed PASS through Turn 2 and flipped to FAIL at Turn 3, where empty slugs became incorrectly accepted. The proof bundle is retained offline and verifies with `externalRootStatus: MATCH`.

## Precise claim boundary

This is a production-code regression caught during a consented capture session. The evidence supports "FaultLine caught and localized the regression to Turn 3." It does not separately prove agent intent, prevention, or that the regression reached the deployed live website.

The verified proof package uses the sidecar-hook capture. A separate native Codex UI replay reproduced the Turn 1 PASS, Turn 2 PASS, Turn 3 FAIL sequence live, but that delegated worktree task did not create a fresh sidecar ledger. Present the native Codex UI screenshots as live demonstration evidence and the retained proof bundle as verification evidence.

## Approved quote

> "MUMBCS can be named, the screenshots can be published, all consent is given."

## Required criticism

Native Codex UI task replay worked visually, but sidecar recording did not produce a fresh native ledger in the delegated worktree; hook/worktree behavior needs clearer support or operator guidance.
