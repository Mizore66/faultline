# Turn-tree snapshot overhead

Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).

**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once (bounded quiescence retries, ignore filters, secret scan, then throwaway index + `write-tree`).

| Repository | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections | Quiescence sleep calls |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| FaultLine | 172 | n/a | n/a | 0 | 0 | 1 | 0 |
| Medium fixture (1,000) | 1000 | 7079.6 | 8025.7 | 0 | 0 | 0 | 3 |
| Large fixture (1,800 under cap) | 1800 | 11006.5 | 14079.9 | 0 | 0 | 0 | 3 |

## Method

- Trials per target: 3 (override with `FAULTLINE_SNAPSHOT_TRIALS`)
- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)
- Quiescence: up to 4 dual-tree attempts with 50 ms delay
- FaultLine row uses this checkout with default ignore rules (`node_modules/`, `.faultline/`, `dist/`, …). If secret scanning refuses the tree, p50/p95 are omitted and the rejection is counted.
- Synthetic fixtures are tracked-file-only temp repos under the 2000-file hard cap (a 5 000-file tree is rejected by design).

Regenerate: `pnpm measure:turn-snapshot`
