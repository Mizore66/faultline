# Go migration slice 2 — Git path, witness store, sandbox

- **Date:** 2026-10-14
- **Status:** Approved design (brainstorming session, 2026-10-14)
- **Parent:** [Go migration overview](2026-09-25-go-migration-overview.md); builds on [slice 1](2026-09-25-go-slice1-verify-design.md)
- **TS oracle:** frozen TS at `faa98c0` (`v0.1.9-buildweek`), run as `node dist/cli.js`, the same oracle as slice 1

## 1. Scope and layout

### Commands

| Command | TS entry | Notes |
| --- | --- | --- |
| `investigate git` | `investigateCommand` (`src/cli-app.ts`, git branch), `investigateGitRange` | All flags: `--repo --from --to --proposal --expect-digest --image --store --draft-store --max-states --runtime-mapping --ledger --ledger-binding-output --output`, plus the new thread-ID flags (Section 3.6). Writes the Git proof bundle and the incident session binding. |
| `minimize git`, `minimize verify` | `minimizeCommand`, `src/git-minimization.ts` | |
| `judge-proof`, `commit-proof-preview` | `judgeProofCommand`, `src/judge-proof.ts`, `src/git-proof-view.ts`, `src/server.ts` | Static HTML export and the local server. |
| `witness propose` (template mode), `approve`, `freeze`, `sign`, `verify`, `policy freeze`, `policy apply`, `resolve-autonomous`, `ratify` | `witnessCommand`, `src/witness-lock.ts`, `src/authenticated-witness-approval.ts`, `src/standing-approval-policy.ts`, `src/autonomous-session.ts` | They share the witness store, so they are ported together. |
| `ledger bind`, `ledger verify` | `src/ledger-binding.ts` | `investigate git --ledger` already needs the binding. |

Every other subcommand keeps throwing `not yet ported: <name>`, as in slice 1.

### Pulled forward from slice 3

`investigate git` and `minimize git` cannot produce a proof without Docker execution (`src/git-investigation.ts` → `createSandboxPlan` / `executeSandboxPlan` → `docker run`; an injected runner is forced to non-proof). Slice 2 therefore also ports:

- `src/sandbox.ts`: Docker plan, execution, audit and cleanup;
- `src/git-materialization.ts`, `src/safe-overlay.ts`, `src/environment-fingerprint.ts`;
- `src/redaction.ts` and `src/secret-allowlist.ts`;
- `src/config.ts` image resolution, `src/runtime-mapping-file.ts`, `src/incident-binding.ts`, `src/evidence-grade.ts` and `src/turn-proof-promotion.ts` as far as these commands reach them.

Slice 3 keeps `runtime`, `doctor` and project init.

### Out of scope

- `witness propose --live` (OpenAI): slice 5. Without `--live` the command works; with it, Go throws `not yet ported: witness propose --live`.
- `witness review` (TTY and review server), guided `investigate --ci-log` / `--resume`, `demo live-git`: slice 5.
- `witness implement` (Codex): slice 4.

### Layout

New packages, one Go file per TS module as in slice 1:

```
internal/clock         Now, NewUUID; seeded in test builds (Section 2.1)
internal/witness       store, propose, approve, freeze, sign, verify, policy, autonomous
internal/sandbox       plan, Docker runner, audit, cleanup
internal/redaction     detectors, allowlist
internal/gitinv        materialization, overlay, fingerprint, investigateGitRange
internal/minimize      minimizeGitDiff, result writer and verifier
internal/proofview     judge-proof and preview HTML, local server
internal/bundle/gitproof   gains the writer next to the slice-1 verifier, reusing its schemas
```

All writers use slice 1's `canonical`, `jsjson`, `schema`, `nodefs` and `nodeproc`.

## 2. Differential harness for writers

Slice 1 compared stdout. Slice 2 also compares what each command writes and which processes it starts.

### 2.1 Seeded clock and IDs

Frozen TS stamps `new Date()`, `Date.now()` and `crypto.randomUUID()` into what it writes (`proposedAt`, `approvedAt`, `frozenAt`, `generatedAt`, run `executionNonce` and timings, bundle and minimization directory names), and those values feed digests and the bundle root.

- **TS:** the harness runs `node --import difftest/gen/seed.mjs dist/cli.js …`. The preload replaces `Date` (constructor without arguments and `Date.now`) and `crypto.randomUUID` with a sequence derived from `FAULTLINE_TEST_SEED`: the clock starts at a fixed instant and advances 1 ms per read; UUIDs are version-4-shaped values from a seeded generator. `src/` is not modified.
- **Go:** every writer takes time and UUIDs from `internal/clock`. Under the `faultline_test` build tag it reads `FAULTLINE_TEST_SEED` and produces the same sequence; release builds ignore the variable.
- Both sides must read values in the same order. The port keeps TS's call order; a mismatch shows up as a digest difference in replay and is fixed in Go.

### 2.2 Fake Docker

A `docker` script on `PATH` (`difftest/testdata/fakedocker/<name>/docker`, like slice 1's fake gits) records each argv it receives and plays back a scripted result: exit status, stdout and stderr, and per commit a witness outcome (pass, fail, crash, timeout). The sandbox plan (argv, environment policy digest, container name, limits) is therefore compared byte for byte without a daemon.

### 2.3 What a case compares

1. stdout, stderr and the exit code, normalized as in slice 1 (`<BUNDLE>`, `<GITTMP>`, masked git and Docker diagnostics);
2. the tree digest of every path the command wrote (bundle, witness store, minimization result, session binding), with the bytes of each file that differs reported;
3. the recorded `git` and `docker` argv sequence.

### 2.4 Cases

- **Scenarios** built by `difftest/gen/fixtures.ts` from small git repositories: two states, many states, a heterogeneous environment, a partially bound ledger, unbound, sha256 object format, CJK paths and overlays.
- **Mutation templates** on the inputs: repository history, witness proposals and approvals, flags, ledgers, fake-Docker outcomes, and filesystem faults.
- **Cross-check:** every bundle Go writes is verified by slice 1's Go verifier and by TS `verify`.

### 2.5 Not compared byte for byte

- `judge-proof` serve mode: a live test fetches every route over HTTP from both servers and compares status, headers and bodies.
- The browser-open call: stubbed in both.
- `descendant.bundle` bytes across machines: git's pack output depends on the git version. Within one run both sides call the same git, so they are compared there.

## 3. Behaviour

### 3.1 Compatibility

stdout, stderr and exit status match frozen TS byte for byte, including Node-format fs errors and `formatCliFailure` with `Next: pnpm fl help` unchanged (overview: slice 5 decisions). Every KNOWN_DIFFERENCES rule from slice 1 (signals, `RLIMIT_NOFILE`, V8 limits, collation, Windows) applies to the new commands.

### 3.2 Writes

- Witness store files are write-once, canonical JSON plus `\n`, mode 0600, and refuse to overwrite with Node's message (`EEXIST: file already exists, open '<path>'`).
- Bundles are staged in a temporary directory and renamed into place; `mkdtemp` directories are removed in the equivalent of `finally`.
- When git, Docker or a write fails part way, Go leaves the same files behind as TS: the same staging cleanup and the same session-binding writes. Fault injection (strace on Linux, as in slice 1) covers `open`, `write`, `rename`, `unlink` and `close` on these paths.

### 3.3 Docker execution

`docker` is spawned through slice 1's `nodeproc` (libuv `spawnSync` semantics): the same container names, `--rm`, `docker rm --force` cleanup after errors, output caps (`maxBuffer`) and timeouts. A SIGINT during a run stops the container and ends `fl` by the signal, with the cleanup Node performs.

### 3.4 `judge-proof` and `commit-proof-preview`

The same routes, headers and HTML; the same port-binding errors (`EADDRINUSE`); `--serve-ms` timing; no browser under `CI`, `GITHUB_ACTIONS` or `FAULTLINE_NO_BROWSER=1`; on stop, exit status 0 as with `process.exit(0)`. `--export-only` output is compared byte for byte.

### 3.5 Secret redaction

The same detector kinds (`OPENAI_API_KEY`, `GITHUB_TOKEN`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AUTHORIZATION_BEARER`, `AUTHORIZATION_BASIC`, `PRIVATE_KEY_BLOCK`, `GENERIC_SECRET_ASSIGNMENT`), the same regular expressions, the 256 KiB scan window, and the same allowlist matching (path, kind, occurrence digest, optional file digest).

The secret-scanner corpus (`benchmarks/secret-scanner-corpus`, 151 cases) is grown as PR #195's `secret-scanner-corpus-growth` spec describes: more true-secret encodings, false positives, Unicode and multiline cases, split secrets labelled `should_miss`, tiny binary fixtures. Go must give the same detections, offsets and redacted text as TS on every case. New detector kinds change behaviour and wait until after parity.

### 3.6 Codex thread IDs on Git bundles (PR #195, CDX-09)

The Git proof manifest already has optional `codex.witnessDraftThreadId` and `codex.repairThreadId` (`src/git-proof-bundle.ts:152`), which the TS CLI never sets. `investigate git` gains `--witness-draft-thread-id <id>` and `--repair-thread-id <id>`:

- without them, output is identical to TS;
- with them, each value must match the existing identifier schema (1 to 160 characters, `^[A-Za-z0-9][A-Za-z0-9._:-]*$`), and the manifest binds it under the bundle root;
- identifiers only: no transcript, prompt or preview text enters the bundle;
- the bundle verifies VALID under slice 1's Go and TS verifiers, and editing either field without re-signing fails both.

These cases are tested directly, not against goldens. Thread IDs on turn manifests belong to slice 4.

### 3.7 Expected KNOWN_DIFFERENCES entries

Added as they are confirmed: git's bundle byte layout across git versions; Docker daemon diagnostics, masked like git stderr.

## 4. CI and done criteria

### 4.1 CI

- **`go`** (ubuntu-24.04, ubuntu-24.04-arm, macos-latest, macos-15-intel, windows-latest, windows-11-arm): writer replay against committed goldens with fake Docker and fake git; Windows skips only the POSIX fake-Docker and fake-git cases. The skip guard and the statement-coverage gate cover every new package.
- **`difftest-live`** (ubuntu-24.04): regenerate the writer goldens and fail on drift; writer live tests; strace fault injection on write paths; one real-Docker comparison (a small repository, both sides, comparing verdicts, run catalogs and bundle structure).
- **`stack-live`**: adds the writer live tests that need no `/proc` on macOS.

### 4.2 Done criteria

1. `go test ./...` passes on all six runners, and every writer case matches its golden: stdout, stderr, exit status, written-tree digest, and git and Docker argv.
2. `difftest-live` and `stack-live` pass, including the real-Docker comparison.
3. Every slice 2 command runs end to end on the Go binary without Node: `investigate git` → bundle → `fl verify` → `judge-proof --export-only`.
4. Every bundle Go writes verifies VALID under both the Go and the TS verifier.
5. The grown secret-scanner corpus gives identical results on both sides.
6. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference.
7. The overview is updated: the sandbox and its dependencies move into slice 2; slice 4 lists PR #195's batch turn-snapshot staging (`--stdin-paths --no-filters` with the TAB/LF/CR fallback, gated on identical tree digests) and hostile-verifier corpus parity for turn bundles; every subcommand in `src/cli-app.ts` has a slice.
8. A benchmark of `investigate git` (Go vs TS, fake Docker) is recorded in `difftest/BENCHMARK.md`. Evidence, not a gate.

Adversarial review rounds continue, as in slice 1, until a round finds no P0 or P1.
