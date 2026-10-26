# Go migration slice 3 — runtime, doctor, init, incident, prevention

- **Date:** 2026-10-20
- **Status:** Approved design (brainstorming session, 2026-10-20); corrected 2026-10-25 with the PR 3a plan's five corrections and what PR 3a's tasks found (package name, git config lists, doctor env, masks and harness rules, the end-to-end result, the ignore matcher, the Docker image store); corrected 2026-10-26 with the PR 3b plan's five corrections and what PR 3b's tasks found (the incident git environments and runner, the intake list alias, `continue`'s binding write and output, `prevention write --input`, the TS-built incident scenarios). Slice 3 is complete: PR 3a and PR 3b port every command in Section 1
- **Parent:** [Go migration overview](2026-09-25-go-migration-overview.md); builds on [slice 1](2026-09-25-go-slice1-verify-design.md) and [slice 2](2026-10-14-go-slice2-git-path-design.md)
- **TS oracle:** frozen TS at `ce99a79` (`v0.1.10-buildweek`), run as `node dist/cli.js`. This is the `src/` that CI has built since slice 1. Earlier specs name `faa98c0` (`v0.1.9-buildweek`), but `14ad89d` and `ce99a79` changed `src/` after it (Section 3.1). This slice corrects the overview and `difftest/KNOWN_DIFFERENCES.md`.
- **Tracking:** issue #211

## 1. Scope and layout

Slice 3 is one spec delivered as two PRs. Each PR has its own plan, its own adversarial review rounds and its own done criteria (Section 4.2).

### Commands

**PR 3a: runtime, doctor, init**

| Command | TS entry | Notes |
| --- | --- | --- |
| `runtime resolve <alias>`, `runtime prepare <alias>` | `runtimeCommand` (`src/cli-app.ts:793-877`), `src/runtime.ts` | `--repo`; `prepare` needs `--yes`; both reject `--pull`. Both write the image to `.faultline/config.json` through `updateFaultLineConfigImage`. |
| `runtime mapping write` | `runtimeCommand`, `src/runtime-mapping-file.ts` | Repeated `--fingerprint X --image Y` pairs, read by position; `--output` is required. |
| `runtime project plan`, `build`, `resolve` | `projectRuntimeCommand` (`src/cli-app.ts:713-791`), `src/runtime.ts` | `--context` (default: cwd), `--dockerfile` (default: `Dockerfile`), `--tag` (required), `--network none\|default` (default: `none`), `--expect-plan`, `--yes`. |
| `doctor` | `doctorCommand` (`src/cli-app.ts:331-378`), `src/doctor.ts` | `--repo`, `--json`, `--proof-ready`, `--security`. Combining `--proof-ready` with `--security` is a usage error. |
| `init` | `initCommand` (`src/cli-app.ts:1839-1940`), `src/project-init.ts` | `--repo`, `--yes`, `--cli <file.js>`, `--runtime node\|python\|go`, `--standing-policy`, `--standing-command`, `--standing-frozen-by`, `--standing-policy-id` (default: `init-default`), `--standing-max-timeout`. |

PR 3a also ports the write side of `src/config.ts` (`writeFaultLineConfig`, `ensureFaultLineConfig`, `updateFaultLineConfigImage`) and `writeRuntimeMappingFile`.

**PR 3b: incident, prevention**

| Command | TS entry | Notes |
| --- | --- | --- |
| `incident suggest` | `incidentCommand` (`src/cli-app.ts:573-693`), `src/incident-intake.ts` | `--repo` is required. |
| `incident start` | `incidentCommand`, `src/incident.ts`, `src/incident-store.ts` | `--repo`, plus exactly one of `--command` and `--command-file`. Optional: `--from`/`--to` (both or neither), `--id`, `--runtime` or `--image` (not both), `--timeout-seconds` (1–3600, default 300), `--store`, `--draft-store`, `--symptom`, `--language`, `--behavior`. It reads HEAD even when `--from`/`--to` are given (Section 3.2). |
| `incident status <id>` | `incidentStatusCommand` (`src/cli-app.ts:464-520`) | `--repo`, `--store`, `--draft-store`, `--expect-digest`. |
| `incident continue <id>` | `continueIncidentCommand` (`src/cli-app.ts:522-571`), `src/incident-continue.ts` | `--repo`, `--store`, `--draft-store`, `--image`, `--expect-digest`, `--ledger`, `--max-states`, `--output`. Reuses slice 2's investigation and bundle writer, with its own proof root and output (Section 3.6). |
| `prevention verify <dir>` | `preventionCommand` (`src/cli-app.ts:2560-2665`) | `--expect-root`. JSON output followed by the summary line; slice 1 already has the verifier and `fl verify`'s text form. |
| `prevention write` | `preventionCommand`, `src/prevention-proof.ts`, `src/prevention-from-artifacts.ts`, `src/agents-md.ts` | `--input` or `--from-bundle` (not both), `--output`, `--repo`. `--from-bundle` also needs `--expect-root`, `--repaired-runs`, `--repaired-commit`, `--repaired-tree` and `--patch`; `--codex-thread-id` is optional. |

Every other subcommand keeps throwing `not yet ported: <name>`.

### Pulled forward from slice 4

`init --yes` pre-warms the first turn snapshot, `doctor --security` captures one, and `init --cli` writes Codex sidecar hooks. So that every slice 3 command ships complete, slice 3 also ports:

- the capture side of `src/turn-snapshot.ts` (`captureTurnTreeSnapshot`, `turnSnapshotRepoCachePath`, the repository cache, quiescence and the object alternates). The turn-bundle verifier stays where slice 2 put it, in `internal/bundle/gitproof`;
- `src/secret-allowlist.ts` (`captureAllowlistedEvent` in `src/redaction.ts` has no caller in `src/` and stays in slice 4);
- from `src/codex-sidecar.ts`: `codexSidecarLedgerPath` and the hook-config writer that `init --cli` uses (`sidecarCliCommand`, `codexSidecarHookConfig`, `sidecarProjectHookTarget`, `writeSidecarProjectHookConfig` in `src/cli-app.ts:1656-1760`);
- the git config override lists `HOST_COMMAND_SAFE_GIT_CONFIG` and `DOCTOR_SAFE_GIT_CONFIG` (`src/doctor.ts`), and `INTAKE_SAFE_GIT_CONFIG` (`src/cli-app.ts:380-399`), which is the same list as `HOST_COMMAND_SAFE_GIT_CONFIG` (`safegit.Intake()` is an alias with a live equality test, not a new list; PR 3b plan correction 2). These differ from slice 2's `materializationSafeGitConfig`. `HOST_COMMAND_SAFE_GIT_CONFIG` has 18 `-c` pairs (36 entries; `TestLiveSafeGitLists` compares it with TS), and `DOCTOR_SAFE_GIT_CONFIG` adds `-c core.autocrlf=false`. Two more lists derive from it: turn-snapshot capture runs git with `HOST_COMMAND_SAFE_GIT_CONFIG` + `-c core.autocrlf=false` + `-c core.attributesFile=/nonexistent/faultline-attributes` (`turn-snapshot.ts:253-281`; 40 entries), and `codexSidecarLedgerPath`'s runner uses `HOST_COMMAND_SAFE_GIT_CONFIG` alone (`codex-sidecar.ts:234`). `internal/safegit` holds all of them.

### Out of scope

- The rest of the sidecar (`codex sidecar install|status|config`, the hook itself), `codex snapshot gc`, turn bundles, `investigate turns`, the repaired-run helpers (`commitRepairedWorktreeState`, `collectRepairedPreventionRuns`, reached only from `src/codex-loop.ts`) and `src/prevention-from-turn.ts`: slice 4.
- PR #195's batch turn-snapshot staging (`git update-index --stdin-paths --no-filters`): frozen TS does not have it, and it changes the git argv, so it stays in slice 4.
- `serve --prevention` attachments (`src/incident-attachments.ts`): slice 5.

### Layout

New packages, one Go file per TS module:

```
internal/runtimeimage curated runtimes, Docker inspect/pull/build, context fingerprint, project image
internal/doctor       checks, preflight, security self-test, summary
internal/projinit     planProjectInit and apply, .faultlineignore, sidecar hook config
internal/turnsnap     capture, repository cache, quiescence, alternates
internal/allowlist    secret-allowlist
internal/incident     intake (suggest), draft, store, continue
internal/safegit      the HOST_COMMAND, DOCTOR and TURN_SNAPSHOT override lists, and INTAKE (an alias of HOST_COMMAND)
internal/hostcmd      the shared runner for Node's decoded-chunk spawn capture (doctor.ts, runtime.ts)
```

The runtime package is `internal/runtimeimage`, not `internal/runtime`: that name would shadow the standard `runtime` package in every importer (plan correction 1).

- `internal/bundle/prevention` gains the writer, `--from-bundle` builder and `AGENTS.md` upsert next to slice 1's verifier.
- The config and runtime-mapping writers join their read sides in `internal/gitinv`.
- The CLI handlers live in `internal/cli/{runtime,doctor,init,incident,prevention}.go`.

All writers use the existing `canonical`, `jsjson`, `schema`, `nodefs`, `nodeproc`, `clock` and `safedir` packages.

## 2. Differential harness for writers

Slice 3 extends slice 2's writer replay (`difftest/writers.json`, TestWriterReplay); it builds no new harness.

### 2.1 Seeded clock and IDs

`difftest/gen/seed.mjs` already replaces `Date`, `Date.now`, `crypto.randomUUID` and every form of `mkdtemp`, and `internal/clock` produces the same sequence under the `faultline_test` build tag. Slice 3 adds new consumers of these values:

- the timestamps `frozenAt` (standing policy), `generatedAt` (runtime mapping), `createdAt` (draft), `proposedAt` (witness proposal) and `updatedAt` (session binding);
- incident IDs `incident-<17 ISO digits>-<12 UUID characters>` (`src/cli-app.ts:435-437`);
- the `Date.now()` directory names `incident-<id>-<desc12>-<ms>` and `prevention-<ms>`;
- the turn-snapshot temporary index `faultline-turn-tree-<uuid>.index`;
- the prevention stage directory `.<name>.<uuid>.tmp`;
- the `doctor --security` directory from `mkdtempSync(tmpdir()/faultline-doctor-security-)`.

Go reads them in TS's order. The quiescence wait (`Atomics.wait`, 50 ms per attempt) is not seeded: only the number of attempts reaches the output, and it depends on repository state, not timing.

### 2.2 Fakes

- **Fake Docker** (`difftest/testdata/fakedocker/`) gains scripted answers for `image inspect --format {{json .RepoDigests}}` (digest present, absent, malformed), `pull`, `build`, `--version` and `info --format {{.ServerVersion}}`, with failures, slow exits past the timeout and output past the cap.
- **Fake toolchain for `doctor`:** a `git` wrapper on `PATH` answers `--version` and passes every other call to the real git; a fake `node` answers `--version`. The harness starts the oracle by absolute path, so the fake `node` does not affect it. `doctor` output then does not depend on the machine.
- **Real git** for `init --yes`, `doctor --security`, turn-snapshot capture, `incident suggest` and `incident start`, as slice 2's investigate cases do.
- **Harness rules added in 3a** (each a no-op on every slice 2 golden, which a full regeneration showed):
  - the `fakeTools` template key selects a toolchain under `difftest/testdata/faketools/<name>`; PATH is the recorder, then `faketools/<name>`, then `fakedocker/<name>`, then the inherited PATH;
  - `doctor` starts git, node and docker `--version` together (`Promise.all`), so their order in the recorded argv is racy on both sides: every run of consecutive `--version` records is sorted by UTF-16 code unit, and nothing else is reordered (`fl init --yes --cli` runs doctor twice);
  - a Git index (`.git/index` at any depth, and a linked worktree's `.git/worktrees/<name>/index`) holds stat data that differs between the two case directories and that git rewrites on a plain `git status`, so it is compared by the SHA-256 of `git ls-files --stage -z` read from it;
  - a loose object in the turn-snapshot store (`.git/faultline/objects/xx/…`) is zlib-compressed by git, so its bytes depend on the zlib git links: it is compared by its name, the id of the object it holds;
  - a `chmod` mutation op, applied on both sides after the mode reset, makes a directory read-only; such cases need a non-root user, and both harness sides fail loudly as root;
  - each recorder appends a record in one write, so concurrent doctor processes cannot interleave records.

### 2.3 What a case compares

1. stdout, stderr and the exit code, with slice 2's normalizations. The `doctor --security` fixture path needs no new mask: the slice 2 harness already points `TMPDIR` at the case's `<TMP>`, so the path prints as `<TMP>/faultline-doctor-security-<seeded>`. Project build plans print a digest over the absolute context directory and Dockerfile, which differ between the TS and Go case directories: a `<PLANDIGEST>` mask replaces a printed plan digest only when it equals the `digestJson` of the plan fields printed beside it (so a wrong Go digest stays unmasked and fails), and an argv string `<STDOUT:n:/json/pointer>` takes the string at that pointer of invocation n's stdout (`runtime project build --expect-plan <STDOUT:0:/plan/review/planDigest>`);
2. the tree digest and file modes of every path the command wrote, which now includes `.faultline/`, `.faultlineignore`, `.codex/hooks.json`, `.git/faultline/` (objects, cache), `.git/objects/info/alternates`, witness and draft stores, bindings, prevention packages and `AGENTS.md`;
3. the recorded `git` and `docker` argv sequence.

### 2.4 Cases

- **Scenarios** built by `difftest/gen/fixtures.ts`:
  - empty, dirty and non-repository directories, with and without an existing config;
  - each curated runtime;
  - a runtime mapping with several pairs;
  - a project build context with a symlink, an executable bit and nested directories;
  - an incident flow end to end (`start`, witness approve and freeze, `status`, `continue`). The `start` and `suggest` cases run the CLI on generated repositories. The `status` and `continue` cases start from scenarios that TS builds with the functions `incident start` calls (`createIncidentDraft`, `proposeWitness`, `writeIncidentDraft`, as `cli-app.ts:589-693` calls them): the draft records the relative repository `repo` (an absolute one would name the build directory), with fixed `createdAt` and `proposedAt` and an explicit `HEAD~n..HEAD` range. The chain from a Go-written draft with an absolute repository through `continue` is covered by `TestEndToEndSlice3bWithoutNode`;
  - `prevention write` from `--input` and from `--from-bundle`.
- **Mutation templates:**
  - flag combinations and invalid values;
  - `wx` collisions (`EEXIST`);
  - `--command-file` that is a symlink, over 32,000 bytes or invalid UTF-8;
  - a mismatched `--expect-digest` or `--expect-plan`;
  - tampered drafts, bindings and prevention inputs;
  - fake-Docker outcomes;
  - filesystem faults.
- **Cross-check:** every Git proof bundle and prevention proof Go writes verifies under the Go and the TS verifier, and Go verifies the ones TS writes.

### 2.5 Live Node-oracle tests

For inputs a golden list cannot enumerate, both sides run on generated inputs and their outputs are compared:

- the build-context fingerprint: the 10,000-file and 256 MiB limits, symlinks, unusual modes, non-UTF-8 names;
- turn-snapshot capture: paths with TAB, LF or CR, `.faultlineignore`, the allowlist, cache reuse;
- `incident suggest` on real repositories: upstream, merge base, detached HEAD, root commit;
- the allowlist parser: the 256 KiB cap and malformed JSON.

### 2.6 Not compared byte for byte

- Real Docker daemon output (`build`, `pull`): masked as in slice 2. The real-Docker `runtime project build` comparison also depends on the daemon's image store: the classic store records no RepoDigest for a local build (`PROJECT_IMAGE_BUILT_NEEDS_REGISTRY_DIGEST`, exit 1), the containerd store (Docker 29's default) records one (`PROJECT_IMAGE_PREPARED_LOCAL_DIGEST`), and podman records `localhost/<repository>@sha256:…` (`UNEXPECTED_REPOSITORY`). The test compares TS and Go on whichever store runs; CI forces the classic store.
- Real host tool versions, when a test runs without the fake toolchain.
- Quiescence wall time.

## 3. Behaviour

### 3.1 Compatibility

stdout, stderr and the exit code match frozen TS byte for byte, including `Next: pnpm fl help`. Every KNOWN_DIFFERENCES rule from slices 1 and 2 applies to the new commands.

`doctor` keeps the Node check (`node --version`; exit 1 when it is not READY), and `init --cli` writes the TS hook command `node '<cli>' codex sidecar hook --quiet`. Dropping both belongs to slice 5 packaging and is recorded in the overview's slice 5 decisions.

The oracle is `ce99a79`. Against `faa98c0` it changes `doctor.ts` (`HOST_COMMAND_SAFE_GIT_CONFIG` split out of `DOCTOR_SAFE_GIT_CONFIG`, which adds `core.autocrlf=false`), `codex-sidecar.ts`, `ledger.ts`, `sandbox.ts` and `witness-result.ts`. Slice 3 ports the `ce99a79` versions.

### 3.2 Process model

Go keeps TS's choice of synchronous or asynchronous spawn at each call site, and the environment each site builds (inherited, stripped to `PATH` plus `GIT_*`, or hardened):

- **Asynchronous, through `nodeproc.Spawn`:**
  - `doctor`'s three version checks (git, node, docker), started together as `Promise.all` starts them;
  - then `rev-parse --show-toplevel`, `status --porcelain=v1 --untracked-files=all -z`, and `docker info` only when the Docker CLI is READY;
  - `incident suggest`'s git calls. The runner is a port of `createNodeIncidentIntakeRunner` on `nodeproc.Spawn`, not `hostcmd`: it joins the output bytes and decodes them once, counts its 64 KiB cap on raw byte lengths, and on a spawn error keeps only the bytes already read. Its env is the hardened set (`GIT_CONFIG_GLOBAL` set) plus `GIT_PROTOCOL_FROM_USER=0` right after `GIT_ALLOW_PROTOCOL`, and its list is `safegit.Doctor()` (5 s, 64 KiB, SIGKILL);
  - every `runtime` Docker call.

  Each keeps its own timer, SIGKILL on timeout and output cap: 5 s and 64 KiB for checks and `image inspect`; 5 min for `pull`; 10 min and 128 KiB for `build`. Doctor's asynchronous calls pass no `env` and no `cwd`: they inherit both, including the user's global git config, and only the `-c` overrides apply.
- **Synchronous, through slice 1's `spawnSync` port:**
  - `doctor --security`'s git calls, with an env stripped to `PATH`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL` and `GIT_TERMINAL_PROMPT=0` (plus `SystemRoot`/`ComSpec` on Windows; not `PATHEXT`);
  - `init`'s hook-target `rev-parse`;
  - turn-snapshot capture;
  - `incident start`'s `git show -s --format=%H%n%P HEAD`, with `safegit.Intake()` (that is `HOST_COMMAND_SAFE_GIT_CONFIG`) and an env with no `GIT_CONFIG_GLOBAL`, no `GIT_ATTR_NOSYSTEM` and no `GIT_PROTOCOL_FROM_USER` (`cli-app.ts:402-433`). `start` reads HEAD even when `--from` and `--to` are given.

So incident runs git in three environments: `suggest`'s hardened async runner, `start`'s synchronous HEAD read, and `continue`'s slice 2 materialization (PR 3b plan correction 1).

### 3.3 Writes

| Writer | Mode, flags and atomicity |
| --- | --- |
| `.faultline/config.json` | directory 0700, file 0600, not atomic, rewritten on every call |
| `.faultlineignore` | `wx`, default mode |
| Standing policy, `.codex/hooks.json`, incident drafts | 0600 `wx` under 0700 directories |
| Session bindings | 0600, overwritten |
| Runtime mapping | no explicit mode; recursive `mkdir` |
| Prevention package | stage directory 0700, files 0600 `wx` with fsync, rename into place, then fsync of the parent directory |
| `AGENTS.md` | 0644, only on `PREVENTION_VERIFIED`; errors are swallowed |
| Turn-snapshot store | `.git/faultline/objects/{info,pack}` 0700, appended `alternates`, cache at `<git-common-dir>/faultline/turn-snapshot-cache.json` |

When a write or a child process fails part way, Go leaves the same files behind as TS. Strace fault injection (as in slices 1 and 2) covers `open`, `write`, `fsync`, `rename`, `unlink` and `close` on these paths.

### 3.4 Turn-snapshot capture

- The same git argv sequence (`ls-files`, `status`, `rev-parse HEAD`, `read-tree`, `update-index`, `hash-object -w --stdin`, `write-tree`, `cat-file -e`) and the same environment.
- The same temporary index, quiescence loop (up to 4 attempts) and cache format.
- `init`'s pre-warm reports `snapshotPrewarm.status: "SKIPPED"` on the same failures, and writes the same stderr lines.
- `.faultlineignore` rules do not run through `jsre`: TS builds each line into a `RegExp` with flag `i`, whose `[^/]` and non-ASCII literals `jsre` cannot compile with JS semantics. `internal/turnsnap/ignore.go` keeps TS's regex source and matches it with a set-of-states scan over UTF-16 units (ECMAScript case folding without `u` from a V8-verified table, `.` excluding line terminators, last match wins). V8's "Regular expression too large" and "Stack overflow" on lines of tens of kilobytes are not reproduced (KNOWN_DIFFERENCES).

### 3.5 Runtime

- The curated images are exactly `node:22-alpine`, `python:3.13-alpine` and `golang:1.24-alpine`.
- `--pull` is rejected; `--yes` gates `prepare` and `project build` as in TS.
- The build-context fingerprint keeps TS's walk order, limits and error texts. `normalizeProjectBuildPaths` requires an absolute `contextDirectory` (`runtime.ts:363`); a relative Dockerfile is resolved against the context, not the process cwd.
- `--expect-plan` matching and every status are unchanged, including the exit-1 statuses `CONFIRMATION_REQUIRED`, `PROJECT_IMAGE_DIGEST_UNAVAILABLE` and `PROJECT_IMAGE_BUILT_NEEDS_REGISTRY_DIGEST`.

### 3.6 Incident and prevention

- The draft (`faultline.incident-draft.v1`) and intake (`faultline.incident-intake.v1`) schemas, incident IDs and `--command-file` validation (a regular file, at most 32,000 bytes, valid UTF-8) match TS.
- `incident status` exits 1 only for `INVALID_FROZEN_WITNESS`.
- `incident continue` exits 1 with the full result for `INVESTIGATION_NOT_PROOF` and 0 for `PROOF_BUNDLE_READY`.
- `incident continue` writes the session binding first, before any verification, so the binding persists when the continuation fails later (`cli-app.ts:545-556`; PR 3b plan correction 3).
- `incident continue`'s output differs from `investigate git`'s (PR 3b plan correction 4):
  - its proof root is `<draft repository>/.faultline/git-proof-bundles`, not the cwd default;
  - its `proofBundle` keys are `directory, rootDigest, externalRootStatus, lifecycle`, in that order;
  - it leaves `investigation` out of the proof output;
  - a bundle that fails its own verification prints `Generated incident proof bundle failed verification: …` (race only: the coverage gate lists it with slice 2's Git bundle text).
- `internal/incident.ContinueOptions` keeps TS's difference between an absent option and a supplied empty string: `Image`, `LedgerFile` and `Output` are `*string` (nil is absent), because the CLI can pass `--image ""`, `--ledger ""` or `--output ""` and TS treats each as present.
- `prevention write --input` is ordinary: with `grounding: "VERIFIED"` input it also yields `PREVENTION_VERIFIED` and runs the `AGENTS.md` upsert, so the upsert is not specific to `--from-bundle` (PR 3b plan correction 5).
- `prevention verify` and `prevention write` print the JSON and then `Prevention verified` or `Prevention evidence summary`; `PREVENTION_EXPORT_FAILED` exits 1.
- `--from-bundle` verifies the bundle through slice 1's Git proof verifier before building the proof.

### 3.7 Expected KNOWN_DIFFERENCES entries

Added as they are confirmed: real `docker build` and `pull` diagnostics and the image-store dependency of the project build status; host tool version strings outside the fake toolchain; quiescence timing; oversized `.faultlineignore` rules (V8's regex size limit); the non-deterministic V8 stack depth at `fl init`'s config and secret-allowlist zod sites. `difftest/KNOWN_DIFFERENCES.md` holds the final list, and its title moves to `ce99a79`.

## 4. CI and done criteria

### 4.1 CI

- **`go`** (ubuntu-24.04, ubuntu-24.04-arm, macos-latest, macos-15-intel, windows-latest, windows-11-arm):
  - writer replay against the slice 3 goldens;
  - Windows skips only the POSIX fake-tool cases, listed in `.github/scripts/windows-writer-skips.txt`;
  - the skip guard, the statement-coverage gate and the error-literal gate cover every new package;
  - the `not yet ported` waiver in `difftest/coverage_test.go` shrinks to slice 4 and 5 commands.
- **`difftest-live`** (ubuntu-24.04):
  - regenerate the slice 3 writer goldens and fail on drift;
  - the live oracles of Section 2.5;
  - strace fault injection on the new write paths;
  - one real-Docker comparison of `runtime project build` (digest status and argv).
- **`stack-live`**: adds the slice 3 live tests that need no `/proc` on macOS.

### 4.2 Done criteria

Each holds for PR 3a and again for PR 3b, for the commands that PR ports.

1. `go test ./...` passes on all six runners, and every writer case matches its golden: stdout, stderr, exit code, written-tree digest with modes, and git and Docker argv.
2. `difftest-live` and `stack-live` pass, including the real-Docker comparison.
3. The commands run end to end on the Go binary without Node:
   - **3a:** `fl init --yes` → `fl doctor --proof-ready` → `fl runtime prepare node --yes` (with fake Docker). Without `node` on `PATH`, `doctor` reports the node check UNAVAILABLE and exits 1 by design (Section 3.1), so `--proof-ready` prints `NOT READY`; the check (`TestEndToEndSlice3aWithoutNode`) asserts that result, and that `init` and `runtime prepare` succeed;
   - **3b:** `incident start` → `witness approve` and `freeze` → `incident continue` → `prevention write --from-bundle` → `fl verify`, which must report `Prevention verified` (`TestEndToEndSlice3bWithoutNode`, with the `pass-fail` fake Docker and an absolute repository path in the draft).
4. Every Git proof bundle and prevention proof Go writes verifies under both the Go and the TS verifier.
5. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference.
6. The overview is updated:
   - turn-snapshot capture, `secret-allowlist` and the sidecar hook writer move from slice 4 to slice 3;
   - the slice 5 decisions gain "drop the Node check from `doctor`" and "sidecar hooks call the `fl` binary";
   - the frozen TS reference becomes `ce99a79` (`v0.1.10-buildweek`).
7. A benchmark of `doctor` and `init --yes` (Go vs TS) is recorded in `difftest/BENCHMARK.md`. Evidence, not a gate.

Adversarial review rounds run per PR, time-boxed to about 90 minutes each, until a round finds no P0 or P1.
