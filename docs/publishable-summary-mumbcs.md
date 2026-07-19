# Publishable MUMBCS x FaultLine summary

Consent status: MUMBCS may be named. Screenshots, terminal captures, retained digests, and this redacted summary may be published. Consent was provided in the Codex thread on 2026-07-19.

Full structured record: [external-case-study-mumbcs.md](external-case-study-mumbcs.md). Impact record: [impact-validation-external-01.md](impact-validation-external-01.md).

## Short version

In one consented external protocol validation on `monashblockchain/MUMBCS`, FaultLine froze a human-reviewed witness over production slug validation and replayed recorded turn states in Docker. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This validates the external workflow of freeze, replay, locate, and verify; it does not claim FaultLine caught a naturally occurring production bug, agent intent, unique root cause, or prevention.

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

The verified proof package uses the scripted sidecar-hook capture. A separate native Codex UI replay reproduced the Turn 1 PASS, Turn 2 PASS, Turn 3 FAIL sequence live, but that delegated worktree task did not create a fresh sidecar ledger. Present the native Codex UI screenshots as demonstration evidence and the retained proof bundle as verification evidence.

## Approved quote

> "MUMBCS can be named, the screenshots can be published, all consent is given."

## Required criticism

Native Codex UI task replay worked visually, but sidecar recording did not produce a fresh native ledger in the delegated worktree; hook/worktree behavior needs clearer support or operator guidance.
