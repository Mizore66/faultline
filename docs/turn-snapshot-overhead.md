# Turn-tree snapshot overhead

Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).

**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once. Unchanged dirty trees can reuse a content-fingerprint session cache; porcelain status alone is never enough for reuse.

| Repository | Mode | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| FaultLine | cold | 116 | 963.9 | 1172.7 | 35 | 104448 | 0 |
| Medium fixture (1,000) | cold | 1000 | 2831.5 | 3078.7 | 0 | 0 | 0 |
| Medium fixture (1,000) warm unchanged | warm-unchanged | 1000 | 348.4 | 382.7 | n/a | n/a | 0 |
| Medium fixture (1,000) one-file modified | one-file-modified | 1000 | 2739.0 | 2911.4 | n/a | n/a | 0 |
| Medium fixture (1,000) 100-file modified | multi-file-modified | 1000 | 2885.6 | 2912.0 | n/a | n/a | 0 |
| Large fixture (1,800 under cap) | cold | 1800 | 4808.6 | 4923.3 | 0 | 0 | 0 |

## Method

- Trials per target: 6 (override with `FAULTLINE_SNAPSHOT_TRIALS`)
- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)
- Quiescence: up to 4 dual-tree attempts with 50 ms delay
- Cold: no `sessionCachePath`
- Warm unchanged / one-file / 100-file modified: content-fingerprint cache (`faultline.turn-snapshot-cache.v2`)
- FaultLine row uses this checkout with default ignore rules plus reviewed `.faultlineignore` (fixture paths only; lockfiles remain eligible)

Regenerate: `pnpm measure:turn-snapshot`
