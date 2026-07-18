# Later-turn hero fixture summary

Reproducible expectation for Sol #42 / issue #38:

| State | Expected stable result |
| --- | --- |
| Session baseline | PASS |
| Turn 1 | PASS |
| Turn 2 | PASS |
| Turn 3 | FAIL |

| Field | Expected |
| --- | --- |
| First stable transition | PASS→FAIL between Turn 2 and Turn 3 |
| `introduction.status` | `ATTRIBUTED` |
| `introduction.turnOrdinal` | `3` |
| Claim language | Earliest recorded stable PASS→FAIL (not unique root cause) |
| Evidence grade | `EXPERIMENTAL_TURN` |

Executable check:

```powershell
pnpm vitest run tests/turn-investigation.test.ts -t "attributes Turn 3"
```

See [docs/later-turn-incident.md](../../docs/later-turn-incident.md).
