# `fl verify`: Go vs frozen TS

Evidence for the performance goal in the migration overview. This is not a gate.

- **Date:** 2026-09-27 (re-measured at the round-3 head; first measured 2026-09-25)
- **Machine:** Intel(R) Xeon(R) Processor @ 2.10GHz, 4 vCPUs, Linux 6.18 (cloud container)
- **Versions:** Go 1.27.0, Node v22.22.2 (frozen TS at `faa98c0`, `node dist/cli.js`), git 2.43.0
- **Method:** each command run 20 times in a shell loop, wall-clock mean per run. `hyperfine` was not installed.
  Go is also measured in-process with `go test ./difftest/ -run '^$' -bench Verify -benchtime 20x`.

| Base | TS `node dist/cli.js verify` | Go binary `fl verify` | Go in-process (benchmark) | Speedup (binary) |
| --- | --- | --- | --- | --- |
| `git-fully-bound` (largest Git base, 19 files, runs git) | 303 ms | 106 ms | 94 ms/op | 2.9× |
| `demo-rerun` (largest base, 124 files) | 209 ms | 28 ms | 15 ms/op | 7.5× |

The first measurement (2026-09-25: 71 ms and 16 ms for Go, 5.2× and 11.7×) predates the ICU-faithful collation iterator, which loads its tables on first use and compares incrementally, and the Node-style process start-up (signal state, descriptor flags). Start-up alone (`fl verify /nonexistent`) is about 4 ms; git calls still dominate the Git base.

Both implementations make the same 18 `git` calls for a Git base (bundle list-heads, verify, fetch, rev-parse, rev-list, diff). Those calls dominate Go's time and bound it: Go's 106 ms total includes all of them, so at least ~200 ms of TS's 303 ms is Node startup, module loading and JS work rather than git. The ratios are Linux figures from this machine: where process creation is slow (a reviewer measured macOS with git 2.54 at 1083 ms TS vs 999 ms Go for `git-fully-bound`), git dominates both and the speedup mostly disappears; the demo base (no git) keeps a large ratio everywhere.

## Large JSON inputs

Parsing is no longer the bottleneck it was in round 3: ASCII fast paths in `jsjson.Parse` (UTF-16 conversion, escape-free strings, whitespace) bring a 200 MB whitespace document from 2.7 s to 0.44 s (Node `JSON.parse` 0.27 s) and a 72 MB string from 4.5 s to 0.41 s (Node 0.12 s) on this machine. Memory is still higher than Node's: Go holds the input as UTF-16 while parsing and builds one `Obj` per object, so the reviewers' 40 MB document of 5M small objects peaks at about 2.7 GB in Go against 0.7 GB in Node. This matters for correctness under a memory limit: the demo verifier reads `analysis.json` with no size cap, and at about 80 bytes of peak memory per input byte a demo bundle whose `analysis.json` is 80 MB gets Go killed by the OOM killer (exit 137, no output) in a 4 GB container where TS verifies it (KNOWN_DIFFERENCES.md). The git verifier's read limits (128 MiB per artifact) do not prevent it either.
