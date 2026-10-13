# `fl verify`: Go vs frozen TS

Evidence for the performance goal in the migration overview. This is not a gate.

- **Date:** 2026-10-04 (round-5 head; previously measured 2026-09-25 and 2026-09-27 on an x86 cloud container)
- **Machine:** Apple M2 Pro host, podman VM (libkrun), Debian bookworm linux/arm64 container limited to 2 CPUs (`--cpus 2`), bundles copied into the container's own filesystem
- **Versions:** Go 1.27.0, Node v22.22.2 (frozen TS, `node dist/cli.js`), git 2.39.5
- **Method:** each command run 20 times after two warm-up runs; median wall-clock time and median CPU time (user + system, children included) per run. `hyperfine` was not installed.

| Base | TS `node dist/cli.js verify` | Go binary `fl verify` | Speedup (wall / CPU) |
| --- | --- | --- | --- |
| `git-fully-bound` (largest Git base, 19 files, runs git) | 159 ms wall, 154 ms CPU | 40 ms wall, 39 ms CPU | 4.0× / 3.9× |
| `demo-rerun` (largest base, 124 files) | 128 ms wall, 118 ms CPU | 13 ms wall, 14 ms CPU | 9.8× / 8.4× |

The ratios move with machine load: on the same setup a reviewer measured 4.2× / 3.5× and 8.0× / 7.0× at `e79893c`, and 3.2× / 3.9× and 6.5× / 7.5× at `c1be976`, so read them as about 3–4× for the Git base and 6–10× for the demo base. The earlier x86 measurements (2.9× and 7.5× at the round-3 head) are superseded. Reading the bundle over a VM's shared folder (virtiofs) adds tens of milliseconds to both sides and hides most of Go's lead on the demo base; start-up alone (`fl verify /nonexistent`) is a few milliseconds.

Both implementations make the same 18 `git` calls for a Git base (init, bundle list-heads, verify, fetch, rev-parse, rev-list, diff). Those calls dominate Go's time and bound it: Go's 40 ms total includes all of them, so most of TS's 159 ms is Node startup, module loading and JS work rather than git. The ratios are Linux figures from this machine: where process creation is slow (a reviewer measured macOS with git 2.54 at 1083 ms TS vs 999 ms Go for `git-fully-bound`), git dominates both and the speedup mostly disappears; the demo base (no git) keeps a large ratio everywhere.

## Large JSON inputs

Parsing is no longer the bottleneck it was in round 3: ASCII fast paths in `jsjson.Parse` (UTF-16 conversion, escape-free strings, whitespace) bring a 200 MB whitespace document from 2.7 s to 0.44 s (Node `JSON.parse` 0.27 s) and a 72 MB string from 4.5 s to 0.41 s (Node 0.12 s) on this machine. Memory is still higher than Node's: Go holds the input as UTF-16 while parsing and builds one `Obj` per object, so on large JSON inputs Go needs 4 to 6 times Node's memory: the reviewers' 40 MB document of 5M small objects peaks at about 1.9 GB in Go against 0.5 GB in Node. This matters for correctness under a memory limit: the demo verifier reads `analysis.json` with no size cap, and at about 80 bytes of peak memory per input byte a demo bundle whose `analysis.json` is 80 MB gets Go killed by the OOM killer (exit 137, no output) in a 4 GB container where TS verifies it (KNOWN_DIFFERENCES.md). The git verifier's read limits (128 MiB per artifact) do not prevent it either.
