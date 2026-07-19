# Publishable MUMBCS x FaultLine summary

Consent status: MUMBCS may be named. Retained digests and this redacted summary may be published. Consent also allows screenshots and terminal captures to be published when held outside this repo; this FaultLine tree does **not** ship screenshot or terminal-capture assets. Consent was provided in the Codex thread on 2026-07-19.

Full structured record: [external-case-study-mumbcs.md](external-case-study-mumbcs.md). Impact record: [impact-validation-external-01.md](impact-validation-external-01.md).

## Short version

In one consented external protocol validation on `monashblockchain/MUMBCS`, FaultLine froze a human-reviewed witness over production slug validation and replayed recorded turn states in Docker. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This validates the external workflow of freeze, replay, locate, and verify; it does not claim FaultLine caught a naturally occurring production bug, agent intent, unique root cause, or prevention.

## Re-verification (Path B)

Public readers should treat in-repo digests + consent + this summary as the publishable evidence set. Byte-level re-verify of `.faultline/turn-proof-bundles/turns-1784447895435/` requires MUMBCS collaborator access or a private redacted handoff. FaultLine does not ship that package.

## Evidence to cite

| Evidence | Value |
| --- | --- |
| Repository | `monashblockchain/MUMBCS` |
| Branch | `faultline/later-turn-demo` |
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

## Honest caveat

The verified proof package uses the scripted sidecar-hook capture. A separate native Codex UI replay reproduced the Turn 1 PASS, Turn 2 PASS, Turn 3 FAIL sequence live, but that delegated worktree task did not create a fresh sidecar ledger. Treat native Codex UI replay as **demonstration-only** when no fresh ledger is written; the retained scripted-hook proof bundle is the verification evidence. Optional screenshots (if used in video/Devpost) live outside this repo and are not required to cite the digests above. See [codex-sidecar.md](codex-sidecar.md#delegated-worktrees--demo-only-when-no-ledger).

## Approved quote

> "MUMBCS can be named, the screenshots can be published, all consent is given."

## Required criticism

Native Codex UI task replay worked visually, but sidecar recording did not produce a fresh native ledger in the delegated worktree; hook/worktree behavior needs clearer support or operator guidance.
