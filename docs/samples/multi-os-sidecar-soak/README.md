# Multi-OS sidecar soak table (TIME-01)

| Platform | Origin | Events | Turn-tree snapshots | Torn captures |
| --- | --- | --- | --- | --- |
| Windows | organic dogfood | 96 | 18 | 0 |
| Linux | seeded real ledger | 21 | 6 | 0 |
| macOS | seeded real ledger | 18 | 5 | 0 |

Verify:

```powershell
pnpm fl record verify --ledger docs/samples/multi-os-sidecar-soak/linux-ledger.json
pnpm fl record verify --ledger docs/samples/multi-os-sidecar-soak/macos-ledger.json
pnpm fl record verify --ledger docs/samples/faultline-self-sidecar-soak/ledger.json
```

Claim boundary: Windows row is organic maintainer dogfood. Linux/macOS rows are seeded via real SIDE_CAR-shaped lifecycle ledgers (valid hash chains). Not multi-developer calendar soak.
