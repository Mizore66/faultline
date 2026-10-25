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

## `fl investigate git` (slice 2)

`BenchmarkInvestigateGit` (`difftest/benchmark_test.go`) times the writer case `investigate-many-states`: `fl investigate git` over the 12 states of the `git-many-states` scenario (36 witness runs, three per state) with the `pass-fail` fake Docker on `PATH` behind the recorders, seeded like the writer replay. Building each workspace is not timed. Run with `go test -tags faultline_test ./difftest/ -run '^$' -bench BenchmarkInvestigateGit -benchtime 10x`; besides ns/op it reports the median, fastest and slowest of the 10 runs.

- **Date:** 2026-10-15 (Task 19 head)
- **Machine:** Apple M2 Pro host, podman VM (libkrun), Debian bookworm linux/arm64 container limited to 2 CPUs (`--cpus 2`), the worktree copied into the container's own filesystem
- **Versions:** Go 1.27.2, Node v22.22.2 (frozen TS, `node --import difftest/gen/seed.mjs dist/cli.js`), git 2.39.5
- **Method:** three benchmark runs of 10 iterations each.

| Command | TS | Go | Speedup |
| --- | --- | --- | --- |
| `investigate git` on `git-many-states` (12 states, 36 runs) | median 2,093–2,107 ms (runs 2,054–2,172 ms) | median 1,945–1,953 ms (runs 1,917–2,011 ms) | about 1.07× |

The command is dominated by its children, which are the same on both sides: 320 `git` calls (the writer golden records them: materializing 12 worktrees, fingerprints, the bundle and its verification) and 36 fake-Docker shell scripts behind the recorder. Go saves Node's start-up and module loading (about 100 ms here) and little else, so the ratio is close to 1 and would fall further with a real Docker daemon, whose container start-up takes hundreds of milliseconds per run. On the macOS host itself (not in the VM) both sides take about 52 s per run (Go 50.8–51.9 s over 10 runs, TS 51.8–52.2 s over 3): there process creation for the git and shell children dominates completely, as for `verify` above.

## `fl doctor` and `fl init --yes` (slice 3a)

`BenchmarkDoctor` and `BenchmarkInitYes` (`difftest/benchmark_test.go`) run on a 2,000-file dirty repository built by the benchmark. It has 1,989 committed files (`package.json` and 1,988 small `.js` files in 20 directories), 100 of them modified, and 10 untracked files. Both commands run with the `ready` fake toolchain (fake `node` and `docker`, a `git` wrapper over the real git) behind the recorders, seeded like the writer replay.
- `doctor` writes nothing, so every run uses the same workspace.
- `init --yes` runs on a fresh copy each run; copying is not timed. Its pre-warm snapshot covers exactly 2,000 eligible files, the turn-snapshot limit: the 1,999 files plus the `.faultlineignore` it writes. The benchmark fails unless doctor reports the 110 changed paths and init reports `PREWARMED`.

Run with `go test -tags faultline_test ./difftest/ -run '^$' -bench 'BenchmarkDoctor|BenchmarkInitYes' -benchtime 10x`.

- **Date:** 2026-10-25 (Task 12 head)
- **Machine:** Apple M2 Pro host, podman VM (libkrun), Debian bookworm linux/arm64 container limited to 4 CPUs (`--cpus 4`), the worktree copied into the container's own filesystem, run as uid 1000
- **Versions:** Go 1.27.2, Node v22.22.2 (frozen TS, `node --import difftest/gen/seed.mjs dist/cli.js`), git 2.39.5
- **Method:** three benchmark runs of 10 iterations each.

| Command | TS | Go | Speedup (median) |
| --- | --- | --- | --- |
| `doctor` | median 89.0–91.7 ms (runs 87.2–98.3 ms) | median 29.9–33.0 ms (runs 29.0–33.7 ms) | about 2.8–3.0× |
| `init --yes` | median 3,491–3,508 ms (runs 3,453–3,638 ms) | median 3,303–3,336 ms (runs 3,276–3,419 ms) | about 1.05× |

`doctor` makes five child calls on both sides: three `git` calls (`--version`, `rev-parse --show-toplevel`, `status --porcelain=v1 -z`) and two to the fake Docker. Go's lead there is mostly Node's start-up and module loading.

`init --yes` is dominated by its children, which are the same on both sides: 467 `git` calls, as the recorder logs them. Almost all come from the pre-warm snapshot. It writes the throwaway tree twice, to confirm quiescence, with a 50 ms sleep between the two writes. Each write runs `hash-object -w --stdin` and `update-index --cacheinfo` once per dirty path. The ratio is therefore close to 1, as for `investigate git`, and it falls further with more dirty paths: with 500 modified files instead of 100, one run took 13.9 s in Go and 14.3 s in TS.

On the macOS host itself (not in the VM; Node 22.23.3, three runs each) process creation dominates: `doctor` took a median of 684 ms in Go and 785 ms in TS, and `init --yes` 86.1 s in Go and 88.4 s in TS.
