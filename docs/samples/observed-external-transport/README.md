# Observed external transport sample (honest non-Codex checkpoints)

This directory is an **honestly labeled** lifecycle ledger for observed
checkpoints recorded outside Codex hooks. It is **not** a Codex
`SessionStart` / `Stop` sidecar capture.

## Provenance

- Generator: `scripts/generate-observed-external-transport-sample.mjs`
- Transport label: `OBSERVED_EXTERNAL_TRANSPORT`
- Checkpoints: real clean Git worktree checkpoints captured in a temporary
  sample repo, then redacted to `/redacted/observed-external-workspace`
- Prompt/output fields are digests only (no private editor content)

## What this is for

When Codex credits are unavailable, FaultLine still supports recording
reviewer-supplied observed lifecycle facts via `fl record`. Use this sample
(and the transport label) so those facts are never mistaken for Codex hook
events. See [security-model.md](../../security-model.md#provenance-honesty-never-fake-codex-ledgers).

## What this is not

- Not `SIDE_CAR` / `CODEX_CLI` / `CODEX_APP` hook provenance
- Not the MUMBCS external-01 proof package
- Not `docs/samples/later-turn-ledger/` (`RECORDED_REDACTED_FIXTURE` for CI drills)
- Not an organic production incident

## Contents

| File | Role |
| --- | --- |
| `ledger.json` | Schema-valid lifecycle ledger (`OBSERVED_EXTERNAL_TRANSPORT`) |
| `sample-meta.json` | Head hash + explicit `notCodexHooks: true` |

## Record your own (honest)

```powershell
pnpm fl record init --session my-observed --repo . --transport OBSERVED_EXTERNAL_TRANSPORT --actor you@example.com
pnpm fl record attach --ledger .faultline\recordings\my-observed.json --repo . --turn turn-1 --ordinal 1 --prompt-digest sha256:<64hex> --output-digest sha256:<64hex> --contribution "non-Codex editor stop" --checkpoint
pnpm fl record verify --ledger .faultline\recordings\my-observed.json
```

README / Devpost wording if you cite this path: *Development continued on a
non-Codex editor; these are honestly labeled observed checkpoints, not Codex
hook events.*
