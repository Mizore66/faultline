# FaultLine self-dogfood SIDE_CAR soak sample

Real Codex sidecar (`SIDE_CAR`) capture from **organic maintainer development**
on this repository — sessions rode along while doing real work (including CI
triage), not a planted one-turn demo. **Not** Cursor, **not**
`OBSERVED_EXTERNAL_TRANSPORT`, **not** the MUMBCS partner package.

## Why this exists

After hooks were trusted, Codex sessions on `Mizore66/faultline` produced
durable sidecar ledgers under `.git/faultline/recordings/`. This sample publishes
a **redacted verifying excerpt** plus private-ledger statistics so that organic
Windows soak progress can be cited without shipping local paths or the full
private event stream.

## Private session (retained locally)

| Field | Value |
| --- | --- |
| Platform | Windows |
| Transport | `SIDE_CAR` |
| Session | `019f7c98-39cd-72e1-9eb3-2a80f10fe1b8` |
| Events | **96** |
| Window | `2026-07-20T06:27:48.696Z` → `2026-07-20T07:41:37.187Z` (~74 min) |
| Head hash | `sha256:ef0c9a2f034f9388d84766237542cff5a16a422e4f04be912517c9d94e99d586` |
| Turns | 19 started / 18 completed |
| Turn-tree snapshots | 18 |
| Clean `WORKTREE_CHECKPOINT` | **0** (Stops reported `CHECKPOINT_SKIPPED_DIRTY`) |
| Local verify | `fl record verify --ledger .git/faultline/recordings/codex-60908cfa…json` → **valid** |

A second shorter session (`019f7cb6-70f5-79f2-8295-cd53cfcf5021`, 5 events) was
also recorded the prior evening; see `sample-meta.json` and the impact record.

## Public files

| File | Role |
| --- | --- |
| `ledger.json` | Redacted hash-chained excerpt (session + turns 1–2); paths → `/redacted/faultline-self-codex-dogfood` |
| `sample-meta.json` | Private stats + excerpt digests + claim boundary |

```powershell
pnpm fl record verify --ledger docs/samples/faultline-self-sidecar-soak/ledger.json
```

## Claim boundary

- **Is:** Organic Windows self-dogfood `SIDE_CAR` soak **slice** (genuine Codex
  development use) with a verified local ledger and public redacted excerpt.
- **Is not yet:** Multi-OS / multi-developer calendar soak (see Future work),
  or a `PREVENTION_VERIFIED` portable prevention package.
- Dirty Stops are honest: turn trees were captured; clean Git checkpoints were
  skipped because the worktree stayed dirty during development.

## Future work

- **macOS + Linux** trusted-hook sessions with the same status/verify protocol
- At least one **second developer** soak session
- Publish per-OS stability stats only after those collections exist (issue **#152**)

## Related

- Partner external `SIDE_CAR`: [../mumbcs-sidecar-ledger/](../mumbcs-sidecar-ledger/)
- Cursor / non-Codex: [../cursor-observed-transport/](../cursor-observed-transport/), [../observed-external-transport/](../observed-external-transport/)
- Impact record: [../../impact-validation-codex-self-dogfood.md](../../impact-validation-codex-self-dogfood.md)
