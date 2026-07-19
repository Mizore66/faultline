# Cursor-observed transport sample (honest non-Codex)

This directory is **Cursor-port testimony**: lifecycle checkpoints shaped like
agent stops, recorded under `OBSERVED_EXTERNAL_TRANSPORT`.

It is **not** a Codex `SIDE_CAR` / `SessionStart` / `Stop` hook capture.
Do not rebadge this ledger as Codex nativity.

## Provenance

- Generator: `scripts/generate-cursor-observed-transport-sample.mjs`
- Transport: `OBSERVED_EXTERNAL_TRANSPORT`
- Editor context: Cursor (agent turns → digest-only prompt/output + clean Git checkpoints)
- Checkpoints: real clean worktree captures, redacted to `/redacted/cursor-observed-workspace`

## Why this exists

Codex hook capture and Cursor agent sessions look similar operationally, but
FaultLine's claim boundary forbids minting `SIDE_CAR` ledgers from Cursor.
When dogfooding FaultLine from Cursor (this Build Week workflow), use this
path — or `fl record … --transport OBSERVED_EXTERNAL_TRANSPORT` — so judges
see honest provenance.

Related:

- Generic non-Codex sample: [../observed-external-transport/](../observed-external-transport/)
- Real Codex sidecar excerpt: [../mumbcs-sidecar-ledger/](../mumbcs-sidecar-ledger/)
- Claim rules: [../../security-model.md](../../security-model.md#provenance-honesty-never-fake-codex-ledgers)

## Verify

```powershell
pnpm fl record verify --ledger docs/samples/cursor-observed-transport/ledger.json
```
