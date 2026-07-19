# Turn-tree snapshot overhead

Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).

**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once. Unchanged dirty trees can reuse a content-fingerprint session cache; porcelain status alone is never enough for reuse.

**CLI entrypoint:** production `pnpm fl` runs `node dist/cli.js` (compiled). `pnpm fl:dev` keeps `tsx` for local TypeScript. Judge blocks run `pnpm install` (prepare builds `dist`) then `pnpm fl`.

| Repository | Mode | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Cold CLI --help (node dist) | cold-cli-invocation | n/a | 45.8 | 52.4 | n/a | n/a | 0 |
| Cold CLI --help (tsx src) | cold-cli-invocation | n/a | 122.7 | 125.5 | n/a | n/a | 0 |
| Cold CLI judge-proof --export-only (node dist) | cold-cli-invocation | n/a | 2564.6 | 2571.9 | n/a | n/a | 0 |
| FaultLine | cold | 232 | n/a | n/a | 0 | 0 | 1 |
| Medium fixture (1,000) | cold | 1000 | 2924.7 | 3023.1 | 0 | 0 | 0 |
| Medium fixture (1,000) warm unchanged | warm-unchanged | 1000 | 363.4 | 380.1 | n/a | n/a | 0 |
| Medium fixture (1,000) one-file modified | one-file-modified | 1000 | 2836.3 | 2951.1 | n/a | n/a | 0 |
| Medium fixture (1,000) 100-file modified | multi-file-modified | 1000 | 2989.7 | 3113.9 | n/a | n/a | 0 |
| Large fixture (1,800 under cap) | cold | 1800 | 5022.8 | 5073.3 | 0 | 0 | 0 |

## Method

- Trials per target: 4 (override with `FAULTLINE_SNAPSHOT_TRIALS`)
- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)
- Quiescence: up to 4 dual-tree attempts with 50 ms delay
- Object quarantine: snapshot blobs land in `.git/faultline/objects` via `GIT_OBJECT_DIRECTORY` + Git alternates (not primary `.git/objects`)
- Cold: no `sessionCachePath`
- Warm unchanged / one-file / 100-file modified: content-fingerprint cache (`faultline.turn-snapshot-cache.v2`)
- Cold CLI invocation: process wall time for `--help` / `judge-proof --export-only` via `node dist/cli.js` (and `tsx` baseline for `--help`)
- FaultLine row uses this checkout with default ignore rules plus reviewed `.faultlineignore` (fixture paths only; lockfiles remain eligible)
- Generated at: 2026-07-19T16:19:33.491Z

Regenerate: `pnpm measure:turn-snapshot`
