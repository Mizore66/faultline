# Verify this experimental turn investigation package

## Evidence grade

Experimental turn-level evidence — not yet a portable proof (`EXPERIMENTAL_TURN`).
This is not the same maturity tier as commit-path portable proof (`COMMIT_PROOF`).
Do not treat a turn package as TURN_PROOF / COMMIT_PROOF. See evidence-grade.ts promotion criteria (external validation, turn-boundary counterfactuals, prevention integration, platform soak).

## Handling warning

This package retains a frozen witness, Codex lifecycle ledger, turn-tree object pack, and bounded recorded evidence so it can be independently checked. Treat it as sensitive incident material and share it only with authorized reviewers.

Run FaultLine's turn package verifier with an externally retained root digest.

The verifier never executes the frozen witness. It validates every declared byte, reconstructs stable states and transitions from raw run facts, rebinds the lifecycle ledger to turn states, and confirms each recorded tree object is present in the portable pack.

An external root is necessary to detect an editor who rewrites the mutable hash catalog too.