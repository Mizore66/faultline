# Later-turn Codex hero incident (First Bad Turn)

Optional **Codex / turn-level** demo beat: show an earliest recorded stable failure that is **not** limited to Turn 1.

Evidence grade remains `EXPERIMENTAL_TURN` (not `COMMIT_PROOF`). Lead the submission video with the Idea / `COMMIT_PROOF` path (`pnpm fl judge-proof`); use this doc as the experimental First Bad Turn follow-up. Mature Git dogfood: [faultline-self-incident.md](faultline-self-incident.md).

## What this incident demonstrates

```text
Session baseline  PASS
Turn 1            PASS
Turn 2            PASS
Turn 3            FAIL  ← earliest recorded stable PASS→FAIL
```

FaultLine attributes **Turn 3** as the earliest recorded stable failure-introducing turn when:

- a `SESSION_BASELINE_SNAPSHOT` exists and is stably PASS;
- the first stable PASS→FAIL is between **adjacent** recorded states;
- no observation gap / unstable intermediate spans that transition.

It does **not** claim unique semantic root cause or private Codex reasoning.

## Reproduce (no Docker — attribution + localization)

CI and local machines without Docker can still prove the attribution logic:

```powershell
pnpm vitest run tests/turn-investigation.test.ts -t "attributes Turn 3"
```

That end-to-end fixture builds a disposable repo + lifecycle ledger (baseline + three turns), freezes a structured witness, and asserts:

- one `PASS→FAIL` transition;
- `introduction.status === "ATTRIBUTED"`;
- `introduction.turnOrdinal === 3`;
- reason text: `Earliest recorded stable PASS→FAIL occurred at turn 3`.

## Produce an experimental turn package (Docker)

When a digest-pinned Node image and Docker daemon are available:

```powershell
pnpm fl -- runtime prepare node --yes
# After capturing a real sidecar ledger with baseline + turns (or regenerating the fixture ledger):
pnpm fl -- investigate turns `
  --repo <fixture-or-session-repo> `
  --ledger <ledger-with-baseline-and-turns.json> `
  --proposal <frozen-proposal-id> `
  --expect-digest sha256:<frozen-digest> `
  --image <digest-pinned-image>
pnpm fl -- verify .faultline\turn-proof-bundles\<dir> --expect-root sha256:<printed-root>
```

`fl verify` will label the package **EXPERIMENTAL_TURN (not COMMIT_PROOF)**.

## Optional Codex repair beat

After a verified Git or turn localization package exists:

```powershell
pnpm fl -- repair --bundle <proof-dir> --expect-root sha256:<root> --repo <repo>
# Opt-in draft inside an isolated worktree (fail-closed frozen-witness verify):
pnpm fl -- repair --bundle <proof-dir> --expect-root sha256:<root> --repo <repo> --with-codex --allow-codex-full-auto
```

## Relation to the Git self-incident

| Path | Role in the video |
| --- | --- |
| **This later-turn fixture / sidecar session** | Hero “First Bad Turn” story (`EXPERIMENTAL_TURN`) |
| [Git self-incident](faultline-self-incident.md) at `97c3290` | Mature `COMMIT_PROOF` fallback / proof-package depth |

Prefer the later-turn story for the opening beat; cut to the Git self-incident if judges ask for portable `COMMIT_PROOF`.
