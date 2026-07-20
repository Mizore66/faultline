# MUMBCS sidecar ledger sample (W0-2 / external-01)

This is a **real Codex sidecar (`SIDE_CAR`) lifecycle ledger** from the permissioned MUMBCS external-01 run — not a Fixture, not Cursor rebadged as Codex, and not `OBSERVED_EXTERNAL_TRANSPORT`.

## Why this exists

Fable’s W0-2 asked for an organic Codex-runtime recording artifact. Self-dogfood was blocked by exhausted Codex credits. Per Fable’s explicit alternative: **the partner’s retained sidecar ledger does double duty** as independent Codex-runtime validation (stronger than dogfood because it is external).

## Provenance

| Field | Value |
| --- | --- |
| Source package root | `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` |
| Frozen witness | `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133` |
| Session | `mumbcs-protocol-001` |
| Transport | `SIDE_CAR` |
| Events | 11 — `SESSION_STARTED`, baseline snapshot, turns 1–3 start/complete/tree snapshot |
| Local corroboration | `fl verify <partner-package> --expect-root sha256:831885ed…` → **Integrity: VALID · External root: MATCH** (2026-07-19) |

Full case study: [../../external-case-study-mumbcs.md](../../external-case-study-mumbcs.md). Handoff digests: [../../partners/mumbcs-later-turn-handoff/](../../partners/mumbcs-later-turn-handoff/).

## What is shipped here

| File | Role |
| --- | --- |
| `ledger.json` | Hash-chained lifecycle ledger from the turn package |
| `sample-meta.json` | Digests + event inventory (no private source) |

## What is **not** shipped

- `source/trees.pack` / MUMBCS worktree bytes (Path B — private; re-verify needs collaborator package)
- Screenshots / terminal captures (consent-approved for video but not in-tree)

## Related honesty samples

- Maintainer organic Windows self-dogfood `SIDE_CAR` (multi-OS = future work): [../faultline-self-sidecar-soak/](../faultline-self-sidecar-soak/)
- Non-Codex editor checkpoints (when credits unavailable): [../observed-external-transport/](../observed-external-transport/) (`OBSERVED_EXTERNAL_TRANSPORT`)
- CI later-turn fixture (not MUMBCS): [../later-turn-ledger/](../later-turn-ledger/) (`RECORDED_REDACTED_FIXTURE`)

## Claim boundary

Cite this as **Codex-runtime sidecar capture on an external repo** bound to the verified `EXPERIMENTAL_TURN` package. Do not call it an organic production outage or FaultLine self-dogfood.
