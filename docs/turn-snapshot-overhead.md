# Turn-tree snapshot overhead

Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).

**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once. Unchanged dirty trees can reuse a content-fingerprint session cache; porcelain status alone is never enough for reuse.

| Repository | Mode | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| FaultLine | cold | 214 | n/a | n/a | 0 | 0 | 1 |
| Medium fixture (1,000) | cold | 1000 | 8965.2 | 14013.2 | 0 | 0 | 0 |
| Medium fixture (1,000) warm unchanged | warm-unchanged | 1000 | 1354.6 | 1630.3 | n/a | n/a | 0 |
| Medium fixture (1,000) one-file modified | one-file-modified | 1000 | 8726.8 | 10615.4 | n/a | n/a | 0 |
| Medium fixture (1,000) 100-file modified | multi-file-modified | 1000 | 9318.8 | 12792.6 | n/a | n/a | 0 |
| Large fixture (1,800 under cap) | cold | 1800 | 10047.2 | 10949.4 | 0 | 0 | 0 |

## Method

- Trials per target: 6 (override with `FAULTLINE_SNAPSHOT_TRIALS`)
- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)
- Quiescence: up to 4 dual-tree attempts with 50 ms delay
- Object quarantine: snapshot blobs land in `.git/faultline/objects` via `GIT_OBJECT_DIRECTORY` + Git alternates (not primary `.git/objects`)
- Cold: no `sessionCachePath`
- Warm unchanged / one-file / 100-file modified: content-fingerprint cache (`faultline.turn-snapshot-cache.v2`)
- FaultLine row uses this checkout with default ignore rules plus reviewed `.faultlineignore` (fixture paths only; lockfiles remain eligible)
- Generated at: 2026-07-19T14:00:23.055Z (post object-quarantine measurement)
- **FaultLine cold row:** timings are `n/a` because secret scanning rejected one eligible blob in this checkout (`Secret rejections = 1`). That is intentional honesty — the scanner worked; fixture rows below still carry measured timings.

Regenerate: `pnpm measure:turn-snapshot`
