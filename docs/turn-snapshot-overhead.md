# Turn-tree snapshot overhead

Measured with `pnpm measure:turn-snapshot` (`scripts/measure-turn-snapshot.mjs`).

**Does recording slow Codex?** Each Stop that captures a turn tree pays this cost once. FaultLine snapshots at turn boundaries with **bounded overhead** — it is not claimed to be invisible.

**Staging model:** dirty trees seed a temporary index from `HEAD^{tree}`, then update only modified / newly untracked / deleted / policy-removed paths via `hash-object --stdin` + `update-index`. Full-cache hits (HEAD + policy + content fingerprint) still reuse the previous tree wholesale.

**CLI entrypoint:** production `pnpm fl` runs `node dist/cli.js` (compiled). `pnpm fl:dev` keeps `tsx` for local TypeScript. Judge blocks run `pnpm install` (prepare builds `dist`) then `pnpm fl`.

| Repository | Mode | Eligible files | Snapshot p50 (ms) | Snapshot p95 (ms) | Blobs added (sum) | Bytes stored (sum) | Secret rejections |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Cold CLI --help (node dist) | cold-cli-invocation | n/a | 55.4 | 62.6 | n/a | n/a | 0 |
| Cold CLI --help (tsx src) | cold-cli-invocation | n/a | 140.1 | 210.4 | n/a | n/a | 0 |
| Cold CLI judge-proof --export-only (node dist) | cold-cli-invocation | n/a | 3409.8 | 3629.8 | n/a | n/a | 0 |
| FaultLine | cold | 445 | n/a | n/a | 0 | 0 | 1 |
| Medium fixture (1,000) | cold | 1000 | 714.7 | 794.9 | 0 | 0 | 0 |
| Medium fixture (1,000) warm unchanged | warm-unchanged | 1000 | 457.4 | 549.5 | n/a | n/a | 0 |
| Medium fixture (1,000) one-file modified | one-file-modified | 1000 | 1962.3 | 2083.4 | n/a | n/a | 0 |
| Medium fixture (1,000) 100-file modified | multi-file-modified | 1000 | 21529.2 | 26698.3 | n/a | n/a | 0 |
| Medium fixture (1,000) restored-clean + one dirty | restored-clean | 1000 | 1498.7 | 1833.1 | n/a | n/a | 0 |
| Large fixture (1,800 under cap) | cold | 1800 | 736.3 | 758.3 | 0 | 0 | 0 |

## Restored-clean correctness

Measured mode `restored-clean`: one file restored to HEAD bytes while another file stays dirty.

- p50: 1498.7 ms
- p95: 1833.1 ms
- HEAD-blob equivalence for the restored path held during measurement: **yes**

## Method

- Trials per target: 6 (override with `FAULTLINE_SNAPSHOT_TRIALS`)
- Caps: 1 MiB/file, 32 MiB total, 2000 files (see `src/turn-snapshot.ts`)
- Quiescence: up to 4 dual-tree attempts with 50 ms delay
- Object quarantine: snapshot blobs land in `.git/faultline/objects` via `GIT_OBJECT_DIRECTORY` + Git alternates (not primary `.git/objects`)
- Cold: no `sessionCachePath`
- Warm unchanged / one-file / 100-file / restored-clean: content-fingerprint session cache (`faultline.turn-snapshot-cache.v2`)
- Cold CLI invocation: process wall time for `--help` / `judge-proof --export-only` via `node dist/cli.js` (and `tsx` baseline for `--help`)
- FaultLine row uses this checkout with default ignore rules plus reviewed `.faultlineignore` (fixture paths only; lockfiles remain eligible)
- Generated at: 2026-07-20T23:17:32.167Z

Regenerate: `pnpm measure:turn-snapshot`
