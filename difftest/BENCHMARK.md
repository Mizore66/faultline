# `fl verify`: Go vs frozen TS

Evidence for the performance goal in the migration overview. This is not a gate.

- **Date:** 2026-09-25
- **Machine:** Intel(R) Xeon(R) Processor @ 2.10GHz, 4 vCPUs, Linux 6.18 (cloud container)
- **Versions:** Go 1.27.0, Node v22.22.2 (frozen TS at `faa98c0`, `node dist/cli.js`), git 2.43.0
- **Method:** each command run 20 times in a shell loop, wall-clock mean per run. `hyperfine` was not installed.
  Go is also measured in-process with `go test ./difftest/ -run '^$' -bench Verify -benchtime 20x`.

| Base | TS `node dist/cli.js verify` | Go binary `fl verify` | Go in-process (benchmark) | Speedup (binary) |
| --- | --- | --- | --- | --- |
| `git-fully-bound` (largest Git base, 19 files, runs git) | 368 ms | 71 ms | 86 ms/op | 5.2× |
| `demo-rerun` (largest base, 124 files) | 191 ms | 16 ms | 11 ms/op | 11.7× |

Both implementations make the same 18 `git` calls for a Git base (bundle list-heads, verify, fetch, rev-parse, rev-list, diff). Those calls dominate Go's time and bound it: Go's 71 ms total includes all of them, so at least ~300 ms of TS's 368 ms is Node startup, module loading and JS work rather than git. The ratios are Linux figures from this machine: where process creation is slow (a reviewer measured macOS with git 2.54 at 1083 ms TS vs 999 ms Go for `git-fully-bound`), git dominates both and the speedup mostly disappears; the demo base (no git) keeps a large ratio everywhere.
