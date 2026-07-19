# Later-turn ledger sample (recorded fixture)

This directory holds a **carefully redacted recorded fixture**, not a live organic
dogfood capture from a production Codex session.

## Provenance

- Generator: `scripts/generate-later-turn-ledger-fixture.mjs`
- Shape: sidecar-compatible multi-turn lifecycle ledger (`faultline.codex-lifecycle-ledger.v1`)
- Privacy: prompt/tool argument text is never stored — only digests and tool names
- Path labels are redacted to `/redacted/application`

## Contents

| File | Role |
| --- | --- |
| `ledger.json` | Schema-valid lifecycle ledger (2 turns + tool attribution) |
| `trees.bundle` | Git bundle containing tree objects referenced by snapshots |
| `fixture-meta.json` | Digests / head hash for quick integrity checks |

## Use with FaultLine

```bash
# Verify the ledger offline
pnpm exec tsx -e "import { verifyCodexLifecycleLedgerFile } from './src/ledger.ts'; console.log(verifyCodexLifecycleLedgerFile('docs/samples/later-turn-ledger/ledger.json'))"

# Clone trees for minimize / prove drills
git clone docs/samples/later-turn-ledger/trees.bundle /tmp/later-turn-trees

# Point investigate turns at the fixture ledger (still needs a frozen witness + image)
pnpm fl investigate turns --repo /tmp/later-turn-trees \
  --ledger docs/samples/later-turn-ledger/ledger.json \
  --proposal <id> --expect-digest <digest> --image <digest-pinned-image>
```

This sample supports CI drills for turn minimization (PASS→FAIL) without claiming
`TURN_PROOF` or a live external impact case.
