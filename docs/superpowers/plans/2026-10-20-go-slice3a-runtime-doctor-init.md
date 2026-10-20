# Go Slice 3a (runtime, doctor, init) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs `runtime resolve|prepare|mapping write|project plan|build|resolve`, `doctor` (with `--json`, `--proof-ready` and `--security`) and `init`. For each, stdout, stderr, exit status, written files with modes, and git/docker argv are byte-identical to frozen TS `node dist/cli.js`. This is proven by writer goldens and live Node-oracle tests.

**Architecture:** Each TS module becomes one Go file, in a package per area:
- `runtimeimage`
- `doctor`
- `projinit`
- `turnsnap`
- `allowlist`
- `safegit`
- `hostcmd`, a shared runner for Node's decoded-chunk `spawn` capture used by `doctor.ts` and `runtime.ts`

The config and runtime-mapping writers join their read sides in `gitinv`. `nodeproc.SpawnSync` gains environment, input and working-directory options. The slice 2 writer replay gets a fake toolchain (`git --version`, `node --version`, `docker --version`/`info`) and Docker fakes for `image inspect`, `pull` and `build`. Everything else is reused.

**Tech Stack:** Go 1.27 (standard library only); TypeScript generators run with the repo's `tsx`; Node 22.22.2; git; Docker (one live comparison only).

**Spec:** [docs/superpowers/specs/2026-10-20-go-slice3-runtime-design.md](../specs/2026-10-20-go-slice3-runtime-design.md). Read it, and the [slice 2 spec](../specs/2026-10-14-go-slice2-git-path-design.md) and [plan](2026-10-14-go-slice2-git-path.md), before any task. This plan reuses slice 1 and 2 packages and helpers by name.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- **JSON:**
  - Never use `encoding/json` on a value that is hashed, compared canonically, written or printed.
  - `JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`.
  - `digestJson` is `canonical.DigestJSON`.
- **Frozen TS** (`src/`, `package.json`, `pnpm-lock.yaml`) is never modified.
  - The oracle is `node dist/cli.js`, built with `pnpm build` from the current `src/`, which equals `ce99a79` (`v0.1.10-buildweek`), on Node 22.22.2.
  - Goldens come only from CI or from a 22.22.2 `node` first on `PATH` (`/opt/homebrew/bin/node` is 22.23.3).
- **Strings and sorting:**
  - JS strings are WTF-8 (`internal/jsstr`). TS `slice`/`length` counts UTF-16 units (`jsstr.ToUTF16`).
  - `localeCompare` sorts use `canonical.SortLocale`/`canonical.LocaleCompare`. A bare `.sort()` uses `jsstr.CompareUTF16`.
  - `toString("utf8")` is `jsstr.DecodeUTF8`.
  - JS regexes are `jsre.MustCompile(source, flags)`.
- **Comments and sources:**
  - Every ported function has a comment naming its TS source, e.g. `// Port of src/runtime.ts:409 fingerprintProjectBuildContext.`
- **Time, UUIDs and processes:**
  - Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock`. `TestNoDirectClockReads` enforces this.
  - Child processes start only through `nodeproc.Spawn` (async) or `nodeproc.SpawnSync`/`SpawnSyncWith` (sync), matching what the TS call site uses.
- **Commits:**
  - Use `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`.
  - The remote is `git@github-mizore66:Mizore66/faultline.git`. Never use plain `gh`.
  - End every commit message with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
  - There is a TruffleHog pre-commit hook: never bypass it.
  - The doctor `--security` fixture secret (`sk-proj-abcdefghijklmnopqrstuvwxyz012345`) is generated at run time, never committed as a file. If a test needs it, build the string from pieces.
- **Output hints:** `Next: pnpm fl help` and the `Next: fl ...` hints stay verbatim.
- **Shell commands:** each must finish within about 5 minutes. Wrap long runs in `timeout 280`, write output to a file, then grep it.

## Corrections to the spec found while planning

Fold these into the spec in Task 13's docs commit:

1. **Package name.** `internal/runtime` would shadow the standard `runtime` package in every importer, so the package is `internal/runtimeimage` (spec 1, Layout).
2. **A fourth and fifth git config list.**
   - Turn-snapshot capture runs git with `HOST_COMMAND_SAFE_GIT_CONFIG` + `-c core.autocrlf=false` + `-c core.attributesFile=/nonexistent/faultline-attributes` (`turn-snapshot.ts:253-281`).
   - `codexSidecarLedgerPath`'s runner uses `HOST_COMMAND_SAFE_GIT_CONFIG` alone (`codex-sidecar.ts:234`).
   - `internal/safegit` holds all of them (spec 1, Pulled forward).
3. **`doctor` env and cwd.**
   - Doctor's async calls pass no `env` and no `cwd`: they inherit both, including the user's global git config. Only the `-c` overrides apply.
   - `doctor --security` runs `spawnSync` with an env stripped to `PATH`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL` and `GIT_TERMINAL_PROMPT=0` (plus `SystemRoot`/`ComSpec` on Windows). It does not use `PATHEXT` (spec 3.2).
4. **Masks.** The slice 2 harness already points `TMPDIR` at the case's `<TMP>`, so the `doctor --security` fixture path needs no new `<TMPDIR>` mask (spec 2.3).
5. **End to end without Node (spec 4.2 point 3, 3a).**
   - Without `node` on `PATH`, `doctor` reports the node check UNAVAILABLE and exits 1, by design (spec 3.1).
   - `doctor --proof-ready` therefore prints `NOT READY`.
   - The done check asserts that the Go binary completes `init --yes` → `doctor --proof-ready` → `runtime prepare node --yes` (with fake Docker) without Node, with exactly that doctor result.

## File Structure

```
internal/safegit/safegit.go              HostCommand, Doctor, TurnSnapshot lists (src/doctor.ts:26-57, turn-snapshot.ts:253-281)
internal/nodeproc/spawn.go               + SpawnSyncOptions, SpawnSyncWith (env, input, cwd, maxBuffer)
internal/hostcmd/hostcmd.go              Run: Node spawn + setTimeout SIGKILL + decoded-chunk output budget
internal/gitinv/configwrite.go           src/config.ts:38-125 (defaultFaultLineConfig, write, ensure, updateImage)
internal/gitinv/runtimemapwrite.go       src/runtime-mapping-file.ts:30-64
internal/runtimeimage/curated.go         src/runtime.ts catalog, inspect/pull commands, RepoDigest helpers, resolve/prepare
internal/runtimeimage/context.go         src/runtime.ts:345-494 build paths, context fingerprint, plan digest
internal/runtimeimage/project.go         src/runtime.ts:496-916 tag validation, describe, build, resolve/prepare project image
internal/runtimeimage/runner.go          src/runtime.ts:917-1005 docker runner on hostcmd
internal/doctor/doctor.go                src/doctor.ts:1-543 checks, runtime markers, preflight, report
internal/doctor/security.go              src/doctor.ts:545-760 security self-test
internal/doctor/runner.go                src/doctor.ts:767-829 on hostcmd
internal/allowlist/allowlist.go          src/secret-allowlist.ts
internal/turnsnap/ignore.go              turn-snapshot.ts:91-158, 368-437, 659-680 rules and descriptors
internal/turnsnap/scan.go                turn-snapshot.ts:686-770 secret scan, entropy
internal/turnsnap/plan.go                turn-snapshot.ts:439-660, 804-983 digests, status parsing, path plan
internal/turnsnap/capture.go             turn-snapshot.ts:177-300, 772-803, 984-1335 git runner, object store, cache, capture, sign
internal/projinit/projinit.go            src/project-init.ts
internal/projinit/sidecar.go             cli-app.ts:1611-1755 hook command/config/target/writer, codex-sidecar.ts:234,297-314,334
internal/cli/runtime.go, doctor.go, init.go
difftest/testdata/faketools/<name>/{git,node,docker}   fake toolchains for doctor
difftest/testdata/fakedocker/runtime-*/docker           image inspect/pull/build fakes
difftest/gen/scenarios.ts                + slice 3a scenarios
difftest/testdata/writers.json           + slice 3a writer cases
difftest/runtime_live_test.go, turnsnap_live_test.go, doctor_live_test.go, allowlist_live_test.go
```

---

### Task 1: Safe git lists, `SpawnSyncWith` and the shared host-command runner

**Files:**
- Create: `internal/safegit/safegit.go`, `internal/safegit/safegit_test.go`, `internal/hostcmd/hostcmd.go`, `internal/hostcmd/hostcmd_test.go`
- Modify: `internal/nodeproc/spawn.go` (add `SpawnSyncOptions`, `SpawnSyncWith`; `SpawnSync` becomes a call to it), `internal/nodeproc/spawn_test.go`, `difftest/gen/node-oracle.ts` (op `safeGitLists`), `difftest/function_live_test.go`

**Interfaces:**
- Produces:
  - `safegit.HostCommand() []string` (19 `-c` pairs, exact order of `doctor.ts:26-46`), `safegit.Doctor() []string` (HostCommand plus `-c core.autocrlf=false`), `safegit.TurnSnapshot() []string` (HostCommand plus `-c core.autocrlf=false -c core.attributesFile=/nonexistent/faultline-attributes`). Each returns a fresh slice.
  - `nodeproc.SpawnSyncOptions{Env []string /* nil = inherit */; Input []byte; HasInput bool; Cwd string; MaxBuffer int /* 0 = Node default 1024*1024 */}` and `nodeproc.SpawnSyncWith(file string, args []string, o SpawnSyncOptions) Result`.
  - `hostcmd.Options{Timeout time.Duration; MaxOutputBytes int; ErrorJoin string}`, `hostcmd.Result{ExitCode *int; Stdout, Stderr string; TimedOut, OutputLimitExceeded bool}`, `hostcmd.Run(file string, args []string, o Options) (Result, error)`. `error` is non-nil only where the TS runner would reject; neither TS runner rejects, so callers treat it as a bug.

**hostcmd semantics** (transcribe `doctor.ts:767-829` and `runtime.ts:917-986`, and diff them first; they are the same apart from the error-event join):
- Spawn: `nodeproc.Spawn(file, args, SpawnOptions{Stdout: StdioPipe, Stderr: StdioPipe})`, wrapped in `jsexc.Try`. Env is nil and cwd is empty (both inherit). Node's `stdio: ["ignore", ...]` means stdin is ignored. Node's `windowsHide: true` means `WindowsShow: false`.
- Timer: at `Timeout`, set `TimedOut` and `Kill("SIGKILL")`, with no settled guard. The timer is cleared on finish.
- Chunk budget, per chunk:
  1. Decode the chunk on its own with `jsstr.DecodeUTF8`. A code point split across chunks becomes U+FFFD.
  2. Compute `used = len(UTF-8(stdout)) + len(UTF-8(stderr))` on the decoded strings.
  3. If `remaining <= 0`, set the flag, SIGKILL, and drop the chunk.
  4. Otherwise append `DecodeUTF8(chunk[:min(len, remaining)])`. If the chunk was longer, set the flag and SIGKILL.
- Errors:
  - A synchronous spawn throw replaces stderr with the message and finishes with `ExitCode == nil`.
  - An `'error'` event appends to stderr and finishes with nil. Runtime appends `"\n" + msg` when stderr is non-empty (`ErrorJoin: "\n"`); copy doctor's join from `doctor.ts`.
  - `'close'` finishes with the exit code (nil after a signal). Only the first finish counts.

**Gotchas:**
- Do not reuse `sandbox.runNative`: it budgets raw bytes, not decoded strings, and runs `docker rm` cleanup.
- `SpawnSyncWith` with `HasInput` writes `Input` to the child's stdin and closes it, like Node's `input` option. `Env` is passed verbatim. On Windows, keep `nodeproc`'s existing env-block sorting (`envPairs`).
- Node's spawnSync `maxBuffer` covers stdout and stderr separately. Check the slice 1 implementation and keep its ENOBUFS semantics (the `fakegit/enobufs` cases pin them).

- [ ] **Step 1: Write the failing tests**

`internal/safegit/safegit_test.go`:

```go
package safegit

import (
	"slices"
	"testing"
)

func TestListsExtendHostCommand(t *testing.T) {
	h := HostCommand()
	if len(h) != 38 || h[0] != "-c" || h[1] != "core.hooksPath=/nonexistent/faultline-hooks" || h[37] != "protocol.https.allow=never" {
		t.Fatalf("%q", h)
	}
	if d := Doctor(); !slices.Equal(d[:38], h) || !slices.Equal(d[38:], []string{"-c", "core.autocrlf=false"}) {
		t.Fatalf("%q", d)
	}
	ts := TurnSnapshot()
	if !slices.Equal(ts[40:], []string{"-c", "core.attributesFile=/nonexistent/faultline-attributes"}) {
		t.Fatalf("%q", ts)
	}
	h[1] = "mutated"
	if HostCommand()[1] == "mutated" {
		t.Fatal("HostCommand must return a fresh slice")
	}
}
```

`internal/hostcmd/hostcmd_test.go` (POSIX; use `sh -c`):

```go
//go:build !windows

package hostcmd

import (
	"strings"
	"testing"
	"time"
)

func TestBudgetDecodesEachChunk(t *testing.T) {
	r, err := Run("sh", []string{"-c", "printf 'aaaa'; sleep 0.1; printf 'bbbbbbbb'"}, Options{Timeout: 5 * time.Second, MaxOutputBytes: 6})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stdout != "aaaabb" || !r.OutputLimitExceeded || r.ExitCode != nil {
		t.Fatalf("%+v", r)
	}
}

func TestTimeoutKills(t *testing.T) {
	r, _ := Run("sh", []string{"-c", "sleep 5"}, Options{Timeout: 100 * time.Millisecond, MaxOutputBytes: 64})
	if !r.TimedOut || r.ExitCode != nil {
		t.Fatalf("%+v", r)
	}
}

func TestMissingExecutable(t *testing.T) {
	r, _ := Run("faultline-no-such-tool", nil, Options{Timeout: time.Second, MaxOutputBytes: 64, ErrorJoin: "\n"})
	if r.ExitCode != nil || !strings.Contains(r.Stderr, "spawn faultline-no-such-tool ENOENT") {
		t.Fatalf("%+v", r)
	}
}
```

Add `TestSpawnSyncWithEnvAndInput` to `internal/nodeproc/spawn_test.go`: run `sh -c 'printf %s "$FOO"; cat'` with `Env: []string{"FOO=bar", "PATH=" + os.Getenv("PATH")}`, `Input: []byte("in")`, `HasInput: true`. Expect stdout `barin`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/safegit/ ./internal/hostcmd/ ./internal/nodeproc/ -run 'TestLists|TestBudget|TestTimeout|TestMissing|TestSpawnSyncWith'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription. Add the live op `safeGitLists` to `node-oracle.ts`: import `HOST_COMMAND_SAFE_GIT_CONFIG` and `DOCTOR_SAFE_GIT_CONFIG` from `dist/doctor.js` and return both. Add `TestLiveSafeGitLists`, which compares them with `safegit.HostCommand()` and `safegit.Doctor()`. Add `TestLiveHostCmd` to `difftest/spawn_live_test.go`. It runs a small Node script that calls the TS runner (`createNodeDoctorRunner` from `dist/doctor.js`) and `hostcmd.Run` on the same commands, and compares the results:
- 30 generated `sh -c` programs that print random UTF-8 (including 4-byte characters) in random chunk sizes;
- caps of 1–200 bytes;
- exits, signals and timeouts.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/safegit/ ./internal/hostcmd/ ./internal/nodeproc/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveSafeGitLists|TestLiveHostCmd' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/safegit internal/hostcmd internal/nodeproc difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): safe git lists, SpawnSyncWith and the shared host-command runner

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Config writers and the runtime-mapping writer

**Files:**
- Create: `internal/gitinv/configwrite.go`, `internal/gitinv/runtimemapwrite.go`, `internal/gitinv/write_test.go`
- Modify: `internal/gitinv/runtimemap.go` (export `DigestSchema` and `DigestPinnedImageSchema` if the writer needs them outside the file), `difftest/gen/node-oracle.ts` (ops `ensureFaultLineConfig`, `updateFaultLineConfigImage`, `writeRuntimeMappingFile`), `difftest/gitinv_live_test.go`

**Interfaces:**
- Consumes: `gitinv.ReadConfig`, `gitinv.FaultLineConfigPath`, `gitinv.FaultLineConfigSchema`, `bundle.ParseValue`, `safedir.RelativeTrustedSystemPath`, `nodefs.MkdirAll`, `nodefs.WriteFileUTF8`, `clock.ISO(clock.Now())`.
- Produces:
  - `gitinv.DefaultConfig(repo string) jsjson.Value`
  - `gitinv.WriteConfig(repo string, cfg jsjson.Value) (string, error)`
  - `gitinv.EnsureConfig(o EnsureConfigOptions) (EnsureConfigResult, error)`, with `type EnsureConfigOptions struct{ Repository, RuntimeAlias, Image string }` (empty means absent) and `type EnsureConfigResult struct{ Path string; Config jsjson.Value; Status string /* CREATED|UPDATED|UNCHANGED */ }`
  - `gitinv.UpdateConfigImage(repo, image, runtimeAlias string) (jsjson.Value, error)`
  - `gitinv.WriteRuntimeMapping(o RuntimeMappingWrite) (WrittenRuntimeMapping, error)`, with `type RuntimeMappingWrite struct{ Pairs []MappingEntry; OutputPath, GeneratedAt, Source string }` and `type WrittenRuntimeMapping struct{ Path string; Mapping *jsjson.Obj; ProvenanceDigest, GeneratedAt string }`

Transcribe `src/config.ts:38-125` and `src/runtime-mapping-file.ts:30-64`.

**Gotchas:**
- **`writeFaultLineConfig` order:**
  1. `mkdir` recursive, 0o700.
  2. The trusted-path check. Reject if the nested path is empty, starts with `..` or contains `:`, with the message `FaultLine config path escaped .faultline`.
  3. zod parse.
  4. Write with mode 0o600 and flag `w`, not atomic.
- **Config key order** follows the zod shape: `schemaVersion, image, runtime, snapshot, stores`. Absent optional keys are omitted. Output is `StringifyIndent(v, "  ") + "\n"`.
- **`ensureFaultLineConfig`:**
  - It always sets `snapshot: {trackedFilesOnly: false}` and always writes, even when UNCHANGED.
  - Runtime rules, in order: alias and image → `runtime: {alias, image}`; alias with an existing runtime → keep that runtime with the new alias; image alone → set `image` and keep the old runtime.
  - UNCHANGED compares `JSON.stringify(existing)` with `JSON.stringify(next)`, so key order matters. Compare the `jsjson.Stringify` outputs.
- **`updateFaultLineConfigImage`** sets `image`, and sets `runtime` only when an alias is given. It does not touch `snapshot`.
- **Mapping writer:**
  - Pairs are validated in order with `DigestSchema`, then `DigestPinnedImageSchema`. zod errors propagate with their exact text.
  - A duplicate fingerprint with the same image is fine; a different image throws `conflicting runtime mapping for <fp>`.
  - An empty list throws `runtime mapping write requires at least one --fingerprint / --image pair`.
  - `generatedAt` is read from the clock after validation, only when absent.
  - `provenanceDigest = DigestJSON({schemaVersion, generatedAt, source, mapping})`. `source` defaults to `fl runtime mapping write`.
  - Document key order: `schemaVersion, generatedAt, source, mapping, provenanceDigest`.
  - Writes use `mkdirSync(dirname, {recursive: true})` (0o777 before umask) and `writeFileSync(path, ..., "utf8")` (0o666, flag `w`).
- Mapping keys that look like integers cannot occur, because they are `sha256:` digests. Still use `*jsjson.Obj` so insertion order is kept.

- [ ] **Step 1: Write the failing test**

`internal/gitinv/write_test.go`:

```go
package gitinv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func TestEnsureConfigCreatesThenUnchanged(t *testing.T) {
	repo := t.TempDir()
	r, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("%v %v", r.Status, err)
	}
	info, _ := os.Stat(r.Path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	r2, err := EnsureConfig(EnsureConfigOptions{Repository: repo})
	if err != nil || r2.Status != "UNCHANGED" {
		t.Fatalf("%v %v", r2.Status, err)
	}
}

func TestWriteRuntimeMappingRejectsConflicts(t *testing.T) {
	clock.ResetForTest("mapping")
	fp := "sha256:" + strings.Repeat("a", 64)
	img := "node@sha256:" + strings.Repeat("b", 64)
	img2 := "node@sha256:" + strings.Repeat("c", 64)
	_, err := WriteRuntimeMapping(RuntimeMappingWrite{Pairs: []MappingEntry{{fp, img}, {fp, img2}}, OutputPath: filepath.Join(t.TempDir(), "m.json")})
	if err == nil || err.Error() != "conflicting runtime mapping for "+fp {
		t.Fatalf("%v", err)
	}
}
```

(`clock.ResetForTest` exists only under `faultline_test`. Put the mapping test in a `//go:build faultline_test` file, or pass `GeneratedAt` explicitly.)

Live tests:
- `TestLiveConfigWriters`: 500 generated sequences of `ensure`/`update` calls on repositories with no config, a valid config, or a config with a runtime, with random alias/image combinations. Compare the status, the file bytes and the mode after each call.
- `TestLiveRuntimeMappingWriter`: 1,000 generated pair lists (valid, malformed digest, unpinned image, duplicates, conflicts) with a fixed `generatedAt`. Compare the file bytes or the `THREW:` message.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/gitinv/ -run 'TestEnsureConfig|TestWriteRuntimeMapping'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/gitinv/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveConfigWriters|TestLiveRuntimeMappingWriter' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gitinv difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): config writers and the runtime-mapping writer

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Curated runtimes and the Docker image runner

**Files:**
- Create: `internal/runtimeimage/curated.go`, `internal/runtimeimage/runner.go`, `internal/runtimeimage/curated_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `resolveCuratedRuntime`, `prepareCuratedRuntime` with an injected runner that replays a scripted result list; `parseRepoDigests`; `normalizeRepository`), `difftest/runtime_live_test.go` (new)

**Interfaces:**
- Consumes: `hostcmd.Run` (Task 1).
- Produces:
  - `runtimeimage.Runtime{Alias, DisplayName, Tag string}`
  - `runtimeimage.Catalog() []Runtime` (node, python, go, in TS order)
  - `runtimeimage.SelectCuratedRuntime(req string) (Runtime, error)`
  - `type Runner func(args []string, timeout time.Duration, maxOutputBytes int) hostcmd.Result`
  - `runtimeimage.NodeRunner() Runner` (the real docker runner)
  - `runtimeimage.ResolveCuratedRuntime(alias string, run Runner) (jsjson.Value, error)`
  - `runtimeimage.PrepareCuratedRuntime(alias string, run Runner) (jsjson.Value, error)`
  - `type ResolutionError struct{ Code, Message string }` (`CuratedRuntimeResolutionError`, `.code`)

Transcribe `src/runtime.ts:11-56, 135-340, 598-654, 720-841, 917-1005`.

**Gotchas:**
- **Argv:**
  - inspect: `["image","inspect","--format","{{json .RepoDigests}}",ref]` (5,000 ms, 65,536 bytes);
  - pull: `["pull",tag]` (300,000 ms, 65,536 bytes).
  - The runner picks the cap by `args[0]`: `pull` → 64 KiB, `build` → 128 KiB, anything else → 64 KiB.
- **`selectCuratedRuntime`** matches the alias or the exact tag. Otherwise it throws ``Unknown curated runtime ${JSON.stringify(req)}. Choose node, python, or go.``. Use `jsjson.Quote` for the `JSON.stringify`.
- **RepoDigest parsing:**
  - `parseRepoDigests` turns anything that is not an array of strings into `[]`.
  - `normalizeRepository` strips `index.docker.io/`, then `docker.io/`, then adds `library/` if no `/` remains.
  - `isDigestPinnedRepoDigest` uses the regex and also requires the last `:` to come at or before the last `/`.
- **Failure reporting, in order:** runner rejection, then `timedOut`, then `outputLimitExceeded`, then `exitCode !== 0`. A nil exit code counts as failure.
  - The detail is `": " + stderr.replace(/[\r\n]+/g, " ").trim().slice(0, 320)`, where `slice` counts UTF-16 units (`jsstr`).
  - Copy every message text and code verbatim from `runtime.ts`.
- **Prepare:** pull, then resolve. The result is `{...resolved, pull: {tag, resolvedRepoDigest}}`, with insertion order kept.

- [ ] **Step 1: Write the failing test**

`internal/runtimeimage/curated_test.go`:

```go
package runtimeimage

import (
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
)

func scripted(results ...hostcmd.Result) (Runner, *[][]string) {
	var calls [][]string
	return func(args []string, _ time.Duration, _ int) hostcmd.Result {
		calls = append(calls, args)
		r := results[0]
		results = results[1:]
		return r
	}, &calls
}

func code(n int) *int { return &n }

func TestResolveAcceptsDockerIOSpelling(t *testing.T) {
	d := "docker.io/library/node@sha256:" + strings.Repeat("a", 64)
	run, calls := scripted(hostcmd.Result{ExitCode: code(0), Stdout: `["` + d + `"]` + "\n"})
	v, err := ResolveCuratedRuntime("node", run)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[0], " "); got != "image inspect --format {{json .RepoDigests}} node:22-alpine" {
		t.Fatal(got)
	}
	if v.Get("image").Str() == "" {
		t.Fatalf("%v", v)
	}
}

func TestUnknownRuntimeRejectedBeforeDocker(t *testing.T) {
	run, calls := scripted()
	_, err := ResolveCuratedRuntime("ruby", run)
	if err == nil || err.Error() != `Unknown curated runtime "ruby". Choose node, python, or go.` || len(*calls) != 0 {
		t.Fatalf("%v %v", err, *calls)
	}
}
```

Add one test per remaining `tests/runtime.test.ts` case from lines 122–273: no RepoDigest, wrong repository, timeout, inspect failure, pull then resolve, and pull failure. Each asserts the exact message text from TS.

Live: `TestLiveCuratedRuntime` runs 1,500 generated scripted-runner sequences in both implementations and compares the canonical result or the `THREW:<code>:<message>`. The sequences cover random RepoDigests output (valid, other repo, mutable tag, malformed JSON, non-array, very long stderr with CR/LF and astral characters), exit codes, timeouts and output limits.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/runtimeimage/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/runtimeimage/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveCuratedRuntime -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtimeimage difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): curated runtimes and the Docker image runner

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Project build context, plan, build and resolve

**Files:**
- Create: `internal/runtimeimage/context.go`, `internal/runtimeimage/project.go`, `internal/runtimeimage/project_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `describeProjectRuntimeImageBuild`, `fingerprintProjectBuildContext`, `prepareProjectRuntimeImage` and `resolveProjectRuntimeImage` with scripted runners), `difftest/runtime_live_test.go`

**Interfaces:**
- Consumes: Task 3's `Runner` and `ResolutionError`; `canonical.DigestJSON`, `canonical.SortLocale`, `canonical.SHA256HexBytes`, `nodefs.{Lstat,Stat,RealpathSync,ReadDirNames,ReadBytes,Resolve,Relative,IsAbsolute,ParseRoot}`.
- Produces:
  - `runtimeimage.ProjectBuildInput{ContextDirectory, Dockerfile, ImageTag, Network string}`
  - `runtimeimage.DescribeProjectBuild(in ProjectBuildInput) (jsjson.Value, error)` (the plan object)
  - `runtimeimage.PrepareProjectImage(in ProjectBuildInput, o PrepareProjectOptions, run Runner) (jsjson.Value, error)`, with `type PrepareProjectOptions struct{ Confirmed bool; ExpectedPlanDigest string }`
  - `runtimeimage.ResolveProjectImage(tag string, run Runner) (jsjson.Value, error)`
  - `type PreparationError struct{ Code, Message string }`

Transcribe `src/runtime.ts:345-596, 661-719, 842-916`.

**Gotchas:**
- **Path checks are synchronous**, in this order:
  1. Empty, NUL, CR or LF in the context, then in the Dockerfile.
  2. `isAbsolute` on the Dockerfile argument.
  3. `realpath(resolve(ctx))`, then `stat` is-directory.
  4. Reject a filesystem root (`nodefs.ParseRoot`).
  5. `realpath(resolve(ctx, df))`, then `stat` is-file.

  A non-`PreparationError` raised inside these checks becomes ``FaultLine could not resolve the project image build paths: ${msg}`` (Node fs message text, via `nodefs.Error`). After them come the descendant check and then `validateProjectImageTag`, so tag errors appear only after path errors. `describe` runs `assertProjectBuildNetwork` before normalizing.
- **Walk (`fingerprintProjectBuildContext` 409-479):**
  - Depth-first pre-order. Each directory is sorted with `localeCompare` (`canonical.SortLocale` on `nodefs.ReadDirNames`), and directories are entered at their sorted position. The root is never lstat'ed.
  - Per entry, in order:
    1. lstat; on failure, ``could not inspect build-context path ${rel}: msg``
    2. a symlink is rejected
    3. a directory is entered
    4. anything that is not a regular file is rejected
    5. `files.length >= 10000` fails
    6. `totalBytes += size`, which fails if over 268,435,456
    7. read the file
    8. push `{path, bytes, mode: mode & 0o777, digest}`
  - Relative paths join with `/`.
  - Windows modes: use `nodefs.Lstat`, which already models Node's Windows `mode`.
- **Digests:**
  - `contextDigest = DigestJSON({schema: "faultline.project-build-context.v1", files})`.
  - `planDigest = DigestJSON({schema: "faultline.project-build-plan.v1", contextDirectory, dockerfile, imageTag, network, contextDigest, dockerfileDigest})`, with the values after realpath.
- **Plan key order (527-555):**
  - `kind, setupOnly, contextDirectory, dockerfile, imageTag, network, command{executable, arguments, timeoutMs}, review{planDigest, contextDigest, dockerfileDigest, fileCount, totalBytes}, effects{8 keys}, description`.
  - `mayAccessNetwork` is always `true`.
  - Build argv: `["build","--file",df,"--tag",tag,"--pull=false","--network="+net,ctx]` (600,000 ms, 131,072 bytes).
- **`prepareProjectRuntimeImage` order (846-864):**
  1. confirmation
  2. `describe` (a second full walk)
  3. `SHA256_DIGEST` regex on the expected digest
  4. digest equality
  5. build
  6. inspect

  No inspect runs after a failed, timed-out or output-limited build. The prepared object's key order is at 897.

- [ ] **Step 1: Write the failing test**

`internal/runtimeimage/project_test.go`:

```go
package runtimeimage

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) ProjectBuildInput {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	return ProjectBuildInput{ContextDirectory: dir, Dockerfile: "Dockerfile", ImageTag: "registry.example.test/app:1", Network: "none"}
}

func TestDescribeCountsDockerfileOnce(t *testing.T) {
	plan, err := DescribeProjectBuild(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Get("review", "fileCount").Num() != 1 {
		t.Fatalf("%v", plan)
	}
	args := plan.Get("command", "arguments").Items()
	if args[0].Str() != "build" || args[5].Str() != "--pull=false" || args[6].Str() != "--network=none" {
		t.Fatalf("%v", args)
	}
}

func TestSymlinkInContextRejected(t *testing.T) {
	in := fixture(t)
	os.Symlink("Dockerfile", filepath.Join(in.ContextDirectory, "link"))
	if _, err := DescribeProjectBuild(in); err == nil {
		t.Fatal("symlink accepted")
	}
}
```

Add one test per remaining `tests/runtime.test.ts` case from lines 283–565, with exact TS message texts:
- re-resolving a pushed tag;
- a confirmed build;
- a missing acknowledgement;
- an image-ID-only build;
- failed, timed-out and output-limited builds;
- a Dockerfile outside the context;
- the `latest` tag and `--tag-escape:deps`;
- PLAN_MISMATCH;
- PLAN_DIGEST_REQUIRED.

Live: `TestLiveProjectContext` builds 200 generated contexts and compares the `describe` output or the `THREW:` message. The contexts include:
- nested directories, random names (NFC/NFD, uppercase and lowercase that sort differently under `localeCompare`), executable bits, empty directories;
- a symlink, a FIFO (POSIX) and an unreadable file;
- the 10,000-file limit (a context with exactly 10,000 and one with 10,001 one-byte files, built once and reused);
- the byte limit (simulate with a sparse 256 MiB file on Linux; skip elsewhere and record the skip in `check-skips.sh`).

`TestLiveProjectPrepare` runs 500 scripted build and inspect sequences.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/runtimeimage/ -run 'TestDescribe|TestSymlink'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/runtimeimage/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveProjectContext|TestLiveProjectPrepare' -v > /tmp/s3a-t4.log 2>&1; tail -5 /tmp/s3a-t4.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtimeimage difftest .github/scripts/check-skips.sh
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): project image build context, plan, build and resolve

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: `fl runtime` and its writer cases

**Files:**
- Create: `internal/cli/runtime.go`, `internal/cli/runtime_test.go`, `difftest/testdata/fakedocker/runtime-digest/docker`, `runtime-nodigest/docker`, `runtime-inspect-fail/docker`, `runtime-pull-fail/docker`, `runtime-build-fail/docker`, `runtime-slow/docker`, `runtime-flood/docker`
- Modify: `internal/cli/cli.go` (dispatch `runtime`), `difftest/testdata/fakedocker/README.md`, `difftest/gen/scenarios.ts` (scenarios `project-context`, `repo-with-config`), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: Tasks 2–4; `cli.hasFlag`, `cli.option`, `cli.requiredOption`, `cli.fail`, `(*env).out`.
- Produces: `func runtimeCommand(e *env, args []string) error`.

Transcribe `src/cli-app.ts:695-877`.

**Gotchas:**
- **`mapping write` pairs** come from a scan over every index (805-815):
  - At each `--fingerprint`, read `[i+1]`, require `[i+2] === "--image"`, and read `[i+3]`. The step is 1, so values are scanned again.
  - Stray `--image` flags are ignored.
  - `requiredOption("--output")` is checked before the scan.
  - The output's `fingerprints` field is the deduplicated key count.
- **`resolve`:**
  - The `--pull` check comes before Docker.
  - The usage error fires when the action is wrong or `args[1]` is falsy.
  - The config is written only after a successful Docker call. `--repo` defaults to the cwd.
- **`prepare`:**
  - `selectCuratedRuntime` runs first.
  - Without `--yes` it prints `CONFIRMATION_REQUIRED` and exits 1.
  - The `--pull` check comes after the `--yes` check.
- **`project`:**
  - `projectRuntimeBuildInput` checks `--network`, then the context, then `--dockerfile`, then `requiredOption("--tag")`.
  - `resolve` and `build` catch only `UNRESOLVED_REPO_DIGEST`, which goes to stdout with exit 1. Other errors throw to `formatCliFailure`.
  - `build` without `--yes` or `--expect-plan` prints `CONFIRMATION_REQUIRED` and exits 1, after the walk has run.
- **Fake Dockers** are POSIX sh. Each answers `image inspect`, `pull` and `build`, and prints `fake docker (<name>): unsupported command` for anything else.
  - `runtime-digest`: inspect prints `["<repo>@sha256:<64 hex derived from the ref>"]`, where `<repo>` is the ref without its tag, normalized the way Docker reports it (`node:22-alpine` → `node`). `pull` and `build` print a few progress lines and exit 0.
  - `runtime-nodigest`: inspect prints `[]`.
  - `runtime-inspect-fail`: exit 1 with `Error: No such image: <ref>`.
  - `runtime-pull-fail`: `pull` exits 1.
  - `runtime-build-fail`: `build` exits 1 with a multi-line stderr containing CR.
  - `runtime-slow`: inspect sleeps 6 s, past the 5 s timeout.
  - `runtime-flood`: inspect prints 70,000 bytes.

**Writer cases** (scenario in parentheses; `fakeDocker` as named; all `posixOnly` except the `mapping-*` cases):
- **resolve and prepare:**
  - `runtime-resolve-node` (empty, runtime-digest)
  - `runtime-resolve-python-existing-config` (repo-with-config)
  - `runtime-resolve-nodigest`, `runtime-resolve-inspect-fail`, `runtime-resolve-slow`, `runtime-resolve-flood`
  - `runtime-resolve-pull-flag`
  - `runtime-resolve-unknown`
  - `runtime-prepare-no-yes`, `runtime-prepare-go-yes`, `runtime-prepare-pull-fail`, `runtime-prepare-pull-flag-with-yes`
- **mapping write:**
  - `runtime-mapping-two-pairs`, `runtime-mapping-duplicate-same`, `runtime-mapping-conflict`
  - `runtime-mapping-bad-digest`, `runtime-mapping-unpinned-image`, `runtime-mapping-no-pairs`, `runtime-mapping-no-output`
  - `runtime-mapping-scan-reuse` (`--fingerprint --fingerprint --image x`)
  - `runtime-mapping-nested-output`
- **project:**
  - `runtime-project-plan` (project-context), `runtime-project-plan-network-default`, `runtime-project-plan-bad-network`
  - `runtime-project-plan-dockerfile-outside`, `runtime-project-plan-latest-tag`, `runtime-project-plan-no-tag`
  - `runtime-project-build-no-yes`, `runtime-project-build-wrong-plan`, `runtime-project-build-not-digest`
  - `runtime-project-build-ok` (two invocations: plan, then build with the plan digest taken from a fixed seeded value; it is deterministic because the context is fixed)
  - `runtime-project-build-fail`, `runtime-project-resolve-nodigest`, `runtime-project-resolve-ok`
- **usage errors:** one case per usage error in `runtimeCommand`.

- [ ] **Step 1: Add the writer cases to `writers.json` and run the replay to see them fail**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/runtime-'`
Expected: FAIL (`no golden for runtime-resolve-node|0`), then, after goldens are generated, `not yet ported: runtime`.

- [ ] **Step 2: Write `internal/cli/runtime_test.go`** with direct tests for the pair scanner:

```go
package cli

import "testing"

func TestMappingPairScanReusesValues(t *testing.T) {
	pairs, err := runtimeMappingPairs([]string{"mapping", "write", "--fingerprint", "--fingerprint", "--image", "x"})
	if err == nil || len(pairs) != 1 || pairs[0].Fingerprint != "--fingerprint" || pairs[0].Image != "x" {
		t.Fatalf("%v %v", pairs, err)
	}
}
```

Copy the expected error text from `cli-app.ts:805-815`.

- [ ] **Step 3: Implement the handler and the fakes; generate goldens**

```bash
pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts
```

- [ ] **Step 4: Run to verify the replay passes**

Run: `go test ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/runtime-|TestWindowsWriterSkipsList' -v`
Expected: PASS. A mismatch is fixed in Go, never in the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl runtime with writer goldens and runtime fake Dockers

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `fl doctor` (without `--security`) and the fake toolchain

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/runner.go`, `internal/doctor/doctor_test.go`, `internal/cli/doctor.go`, `difftest/testdata/faketools/<name>/{git,node,docker}`, `difftest/testdata/faketools/README.md`
- Modify: `internal/cli/cli.go`, `difftest/writer_test.go` and `difftest/gen/gen-writers.ts` (template key `fakeTools`), `difftest/gen/scenarios.ts` (scenarios `repo-clean`, `repo-dirty`, `repo-rename`, `not-a-repo`, `runtime-markers-multiple`, `runtime-markers-go`, `hostile-fsmonitor`), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: `hostcmd.Run`, `safegit.Doctor()`, `nodefs.Exists`, `nodefs.Resolve`.
- Produces:
  - `type doctor.Runner func(exe string, args []string, timeout time.Duration, maxBytes int) hostcmd.Result`
  - `doctor.NodeRunner() Runner`
  - `doctor.Run(repo string, run Runner) jsjson.Value` (the report, `runFaultLineDoctor`)
  - `doctor.DetectLikelyRuntime(repo string) jsjson.Value`
  - `doctor.CliExitCode(report jsjson.Value) int`
  - `doctor.ProofReadyStatus(report jsjson.Value) string`
  - `func doctorCommand(e *env, args []string) error`

Transcribe `src/doctor.ts:1-543, 767-829` and `src/cli-app.ts:302-378`.

**Gotchas:**
- **Order of checks:**
  1. git `--version`, `node --version` and `docker --version` start together, in that order (`Promise.all`). Start all three `hostcmd.Run` calls in goroutines in that order and wait for all of them. Nothing rejects.
  2. Then, in sequence: `git <Doctor list> -C <repo> rev-parse --show-toplevel`, only if git is READY.
  3. `git <Doctor list> -C <root> status --porcelain=v1 --untracked-files=all -z`, only if a root was found.
  4. `docker info --format {{.ServerVersion}}`, only if docker-cli is READY.
  - If git is not READY, repository and worktree are INAPPLICABLE. If docker-cli is not READY, the daemon is never queried.
- **Spawn order matters for argv recording.** The recorder appends lines from three concurrent processes, so the argv log order of the first three lines is racy on both sides. Have `compareWriter` sort only a leading run of `--version` records (a documented normalization in `writer_test.go` and `gen-writers.ts`). Do not sort anything else.
- **`unavailableObservation` texts**, checked in this order:
  1. the 65,536-byte limit
  2. timed out
  3. could not start / no exit code
  4. ``exited with status ${n}``
- **`outputLine`:** replace runs of `[\r\n]+` with a single space, apply JS `trim()` (`jsstr.Trim`), then `slice(0,160)` in UTF-16 units. An empty result becomes null.
- **Node version** uses `/^v(\d+)(?:\.\d+){0,2}(?:\S*)?$/` and READY needs a major of at least 22. A null git or docker version gives ACTION_REQUIRED. A null daemon version gives UNAVAILABLE with `Docker returned no server version.`
- **Worktree count** is `stdout.split("\0").filter(Boolean).length`, so a rename counts as 2. `repositoryRoot = resolve(reportedRoot)`.
- **Report:**
  - Key order: `schemaVersion, repository, repositoryRoot, diagnostics, likelyRuntime, imageSelection:"NOT_CHECKED", dockerInvestigationPreflight, limitations`. The CLI adds `cliExitCode, proofReadyExitCode`.
  - Diagnostics are always in the order git, repository, worktree, node, docker-cli, docker-daemon, runtime.
  - Preflight ignores the runtime diagnostic.
- **Runtime markers:**
  - node: `package.json, pnpm-lock.yaml, package-lock.json, yarn.lock, bun.lock, bun.lockb`
  - python: `pyproject.toml, requirements.txt, poetry.lock, Pipfile, setup.py`
  - go: `go.mod`
  - More than one group gives MULTIPLE.
- **Exit codes:**
  - `--proof-ready`: exit 0 only if the preflight is READY; otherwise `CliExitCode`, which is 0 only if node is READY.
  - `--json` skips the text lines and `Next:` but keeps the exit rule.
  - Text mode with `--proof-ready` appends `\n--proof-ready: READY|NOT READY\n`.
  - Text mode prints `Next: fl judge-proof` (preflight READY) or `Next: fl doctor --proof-ready` on stderr.
- **Fake toolchain** (`difftest/testdata/faketools/<name>/`, POSIX sh). A template's `"fakeTools": "<name>"` puts this directory on `PATH` after the recorder and before the fake Docker and the real `PATH`.
  - The fake `git` answers `--version` (the 2nd argument is `--version`, after the `-c` pairs; match on the last argument) with a fixed `git version 2.99.0`. Otherwise it execs the next `git` on `PATH` after its own directory, the way `recorder/git` does.
  - The fake `node` answers `--version` with the toolchain's version string.
  - The fake `docker` answers `--version` and `info`, and delegates anything else to the next `docker`.
  - Toolchains:
    - `ready`: node `v22.22.2`, docker `Docker version 27.0.0, build fake`, server `27.0.0`
    - `old-node`: `v20.11.0`
    - `odd-node`: `v22`, `v22.1.2-nightly`, `22.0.0`; one toolchain per value
    - `no-docker`: no docker in the toolchain, and the case sets `"env": {"FAULTLINE_HIDE_DOCKER": "1"}`, which makes `recorder/docker` exit 127 as a missing command would; add this to the recorder
    - `daemon-down`: `info` exits 1 with the daemon message
    - `slow-git`: `--version` sleeps 6 s
    - `flood-node`: prints 70,000 bytes
    - `crlf-git`: version text with CR/LF and trailing spaces
    - `long-docker`: a 300-character version with an astral character at position 159
  - The harness starts the oracle as `process.execPath`, an absolute path, so the fake `node` never runs TS itself. Assert this in `gen-writers.ts`.
- **Real git rewrites `.git/index`** during `status` (doctor sets no `GIT_OPTIONAL_LOCKS`). The index holds stat data that differs between the TS and the Go copy of a scenario. The slice 2 tree digest walks `.git` raw (`gen-writers.ts:54-71` `treeEntries`, `writer_test.go` `treeEntries`), so `.git/index` stat data would differ between the TS and Go workspaces. Add one rule on both sides: `.git/index` is compared by its `git ls-files --stage -z` output (sha256 of it), not its bytes. Pin the rule with a unit test in `writer_test.go`.

**Writer cases** (all `posixOnly`; fakeTools `ready` unless named):
- **repository states:**
  - `doctor-clean` (repo-clean), `doctor-dirty` (repo-dirty), `doctor-rename` (repo-rename), `doctor-not-a-repo` (not-a-repo)
  - `doctor-json`, `doctor-json-proof-ready`
  - `doctor-proof-ready-ready` (fakeDocker runtime-digest), `doctor-proof-ready-not-ready` (no-docker)
- **toolchain edge cases:**
  - `doctor-old-node`, `doctor-odd-node-*`
  - `doctor-no-docker`, `doctor-daemon-down`
  - `doctor-slow-git`, `doctor-flood-node`, `doctor-crlf-git`, `doctor-long-docker`
- **runtime markers:** `doctor-markers-multiple`, `doctor-markers-go`
- **hostile config:** `doctor-hostile-fsmonitor` (the `tests/cli.test.ts:664` case; assert that no marker file appears in the tree)
- **usage errors:** `doctor-security-and-proof-ready`, `doctor-repo-missing-value`

- [ ] **Step 1: Write the failing unit test**

`internal/doctor/doctor_test.go` (an injected runner, like `tests/doctor.test.ts`):

```go
package doctor

import (
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
)

func code(n int) *int { return &n }

func TestGitMissingMakesRepoChecksInapplicable(t *testing.T) {
	run := func(exe string, args []string, _ time.Duration, _ int) hostcmd.Result {
		switch exe {
		case "node":
			return hostcmd.Result{ExitCode: code(0), Stdout: "v22.22.2\n"}
		case "docker":
			return hostcmd.Result{ExitCode: code(0), Stdout: "Docker version 27.0.0\n"}
		}
		return hostcmd.Result{Stderr: "spawn git ENOENT"}
	}
	r := Run(t.TempDir(), run)
	d := r.Get("diagnostics").Items()
	if d[0].Get("status").Str() != "UNAVAILABLE" || r.Get("dockerInvestigationPreflight").Str() != "UNAVAILABLE" {
		t.Fatalf("%v", d[0])
	}
	if d[1].Get("status").Str() != "INAPPLICABLE" || d[2].Get("status").Str() != "INAPPLICABLE" {
		t.Fatalf("%v %v", d[1], d[2])
	}
	if CliExitCode(r) != 0 {
		t.Fatal("node READY must exit 0")
	}
}
```

Port the other eight `tests/doctor.test.ts` cases the same way, all except the live `--security` one, which goes to Task 9.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/doctor/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** the package, the CLI handler, the fake toolchains, the `fakeTools` template key (TS and Go), the argv `--version` normalization and the `.git/index` rule. Add the writer cases and generate the goldens (same commands as Task 5).

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/doctor/ ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/doctor-|TestWindowsWriterSkipsList' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/doctor internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl doctor with a fake toolchain in the writer replay

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Secret allowlist and turn-snapshot path policy

**Files:**
- Create: `internal/allowlist/allowlist.go`, `internal/allowlist/allowlist_test.go`, `internal/turnsnap/ignore.go`, `internal/turnsnap/scan.go`, `internal/turnsnap/ignore_test.go`, `internal/turnsnap/scan_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `loadSecretAllowlist`, `allowlistConfigDigest`, `filterAllowlistedHighOccurrences`, `isEntropyTokenAllowlisted`, and `turnSnapshotIgnore`/`turnSnapshotScan` if the TS functions are exported; otherwise these go through Task 8's capture op), `difftest/allowlist_live_test.go`, `difftest/turnsnap_live_test.go` (new)

**Interfaces:**
- Consumes: `redaction.RedactText`, `jsre.MustCompile`, `canonical.SHA256Hex`, `nodefs.{Lstat,ReadBytes}`.
- Produces:
  - `allowlist.Allowlist` (parsed document)
  - `allowlist.Load(repo string) (Allowlist, error)`
  - `allowlist.ConfigDigest(a Allowlist) string`
  - `allowlist.FileContentDigest(path string, maxBytes int) (string, error)`
  - `allowlist.FilterHighOccurrences(occ []redaction.Occurrence, fileDigest, relPath string, a Allowlist) []redaction.Occurrence`
  - `allowlist.IsEntropyTokenAllowlisted(token, fileDigest, relPath string, a Allowlist) bool`
  - `turnsnap.Rule`
  - `turnsnap.ParseIgnoreLines(text string) []Rule`
  - `turnsnap.LoadIgnoreRules(root string) ([]Rule, error)`
  - `turnsnap.PathExcluded(rel string, rules []Rule) bool`
  - `turnsnap.IsProtectedDescriptor(rel string) bool`
  - `turnsnap.AssertNoSecrets(root string, paths []string, a Allowlist) error`
  - `turnsnap.IsSuspiciousHighEntropyToken(tok string) bool`

Transcribe `src/secret-allowlist.ts` (all) and `src/turn-snapshot.ts:51-158, 368-437, 659-770`.

**Gotchas:**
- **Allowlist:**
  - A missing file means an empty allowlist. A symlink or non-file is rejected, and so is a file over 256 KiB (``exceeds FaultLine's 256 KiB parse limit.``).
  - `fileContentDigest` reads the whole file and hashes the first `maxBytes`.
  - `allowlistConfigDigest` uses plain `JSON.stringify`, not canonical (`jsjson.Stringify`).
  - The filter emits kinds in first-seen `Set` order.
- **Ignore rules:**
  - The default rules (100-128) come first, then the user lines split on `/\r?\n/`. Backslashes become `/`.
  - Rules compile as JS regexes with flag `i`, through `jsre`, not Go's `(?i)`, which folds differently (for example U+212A).
  - The prefix is `^` when anchored, else `(?:^|/)`. The suffix is `(?:/.*)?$` for directory rules, else `$`.
  - `**` becomes `.*`, `*` becomes `[^/]*`, `?` becomes `[^/]`. The last match wins.
  - `ENVIRONMENT_DESCRIPTOR_FILES`, `Dockerfile*` and `(docker-)compose.y(a)ml` are never excluded.
  - Error texts are `.faultlineignore must be a regular non-symlink file.`, `.faultlineignore exceeds FaultLine's 256 KiB parse limit.` and `Could not read .faultlineignore: …`.
- **Secret scan:**
  - Read `min(size, 256 KiB)` and decode lossily. Skip a file whose first 8,192 bytes contain a NUL.
  - Run `RedactText(text, rel)` and filter HIGH occurrences through the allowlist. Any HIGH kind left throws, with the kinds joined by `", "`.
  - Then run the entropy pass with `/[A-Za-z0-9_+\-\/=]{40,}/g`. A token counts if its entropy is at least 4.5, it has no `/`, no `sha256-`/`sha512-` prefix and is not all hex.
  - Error texts are at `turn-snapshot.ts:898, 927, 936, 942, 1080, 1084, 1097, 1108`, plus `Could not load secret allowlist: …`.
  - The entropy uses `Math.log2` on JS doubles. Compute it in the same order (sum of `-p*log2(p)` over characters in first-seen order) so the comparison against 4.5 rounds the same way.

- [ ] **Step 1: Write the failing tests**

`internal/turnsnap/ignore_test.go`:

```go
package turnsnap

import "testing"

func TestIgnoreLastMatchWinsAndDescriptorsProtected(t *testing.T) {
	rules := ParseIgnoreLines("build/\n!build/keep.txt\nDockerfile\n")
	if !PathExcluded("build/out.o", rules) || PathExcluded("build/keep.txt", rules) {
		t.Fatal("last match must win")
	}
	if PathExcluded("Dockerfile", rules) {
		t.Fatal("descriptor excluded")
	}
}
```

(`parseIgnoreLines` supports `!` negation, `turn-snapshot.ts:379`.)

`internal/allowlist/allowlist_test.go`: a missing file gives an empty allowlist; a symlink is rejected with the TS text; a 256 KiB + 1 file is rejected.

`internal/turnsnap/scan_test.go`: build the `sk-proj-…` secret from pieces at run time, write it to a temp file, and expect `AssertNoSecrets` to fail with the TS text from `tests/turn-snapshot.test.ts:325`. A 40-character hex token passes, and a random 48-character base64 token fails.

Live tests:
- `TestLiveAllowlist`: 1,000 generated allowlist documents and match queries.
- `TestLiveTurnSnapshotIgnore`: 2,000 generated (rules, path) pairs covering `*`, `**`, `?`, anchors, trailing `/`, CR/LF, non-ASCII and case-fold characters.
- `TestLiveTurnSnapshotScan`: 500 generated files (secrets of every detector kind, entropy tokens near 4.5, binary, truncated UTF-8 at 256 KiB) with and without allowlist entries.

If the TS functions are not exported, `TestLiveTurnSnapshotIgnore` and `TestLiveTurnSnapshotScan` run through `captureTurnTreeSnapshot` on a one-commit repository after Task 8. Note that in the test file and move these two tests to Task 8.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/allowlist/ ./internal/turnsnap/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/allowlist/ ./internal/turnsnap/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveAllowlist|TestLiveTurnSnapshotIgnore|TestLiveTurnSnapshotScan' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/allowlist internal/turnsnap difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): secret allowlist and turn-snapshot ignore rules and secret scan

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Turn-snapshot capture

**Files:**
- Create: `internal/turnsnap/plan.go`, `internal/turnsnap/capture.go`, `internal/turnsnap/capture_test.go`
- Modify: `internal/bundle/gitproof/ledger.go` (export `UnsignedTurnTreeSnapshotSchema` and `TurnTreeSnapshotSchema` for the signer; keep the verifier unchanged), `difftest/gen/node-oracle.ts` (op `captureTurnTreeSnapshot`: run on a given repository with `sleep: () => {}`, under the seed preload, and return `{snapshot, warnings}` plus the resulting cache bytes), `difftest/turnsnap_live_test.go`

**Interfaces:**
- Consumes: Task 7; `safegit.TurnSnapshot()`; `gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})` (base env order; verify it matches `turn-snapshot.ts:177-191`); `nodeproc.SpawnSyncWith`; `clock.{Now,ISO,NewUUID}`; `nodefs`; `canonical.{DigestJSON,LocaleCompare}`.
- Produces:
  - `turnsnap.CaptureOptions{SessionCachePath string; TrackedFilesOnly bool; OnWarning func(string); Sleep func(time.Duration) /* nil = blocking */}`
  - `turnsnap.Capture(repo string, o CaptureOptions) (jsjson.Value /* snapshot */, []string /* warnings */, error)`
  - `turnsnap.RepoCachePath(root string) (string, error)` (`turnSnapshotRepoCachePath`)
  - `type turnsnap.Error struct{ Message string }` (`TurnSnapshotError`)

Transcribe `src/turn-snapshot.ts:177-300, 327-345, 439-660, 772-1335`.

**Gotchas:**
- **Git runner:**
  - `SpawnSyncWith("git", [...safegit.TurnSnapshot(), "-C", root, ...args], {Env, Input, MaxBuffer: 1024*1024})`.
  - Env is the hardened keys in order, then `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES` and `GIT_INDEX_FILE` as raw paths when given.
  - Failure throws `FaultLine could not safely capture a turn tree snapshot: ` plus `(stderr + error.message).trim()`, or `exit <status|unknown>` when that is empty.
- **Argv sequence:** follow the agent notes reproduced here.
  1. `rev-parse --show-toplevel`.
  2. Filesystem only: load the allowlist, then `policyDigest` (reads `.faultlineignore`), then the cache, then the ignore rules.
  3. Up to 4 attempts. Each attempt runs:
     1. `rev-parse HEAD`.
     2. `status --porcelain=v1 --untracked-files=all -z`.
     3. The cache-hit path, only when head, policy and content fingerprint all match: `cat-file -e <tree>^{tree}` (objEnv), sleep, `status`, `rev-parse HEAD`, re-fingerprint, then `cat-file -e` again.
     4. Otherwise: first `writeThrowaway`, then `status`, then sleep(50), then `status`, then the second `writeThrowaway` (with `previousPathDigests`), then `status`, then `rev-parse HEAD`.
     5. The retry checks, in the order at 1290-1305.
  4. The exhausted-attempts message: ``FaultLine could not prove turn-tree quiescence after N dual-tree attempt(s) and refused a torn turn tree snapshot.``
- **`writeThrowaway`:**
  - **Fast path** (empty status, and no tracked path ignored, descriptors excepted): `ls-files -z`, then `rev-parse HEAD^{tree}`. It does not build objEnv, so no quarantine directory or alternates are created.
  - **Slow path**, in order:
    1. `ls-files -z`
    2. `ls-files -z --others --exclude-standard`, unless tracked-only
    3. `rev-parse HEAD^{tree}`
    4. `ls-tree -r --name-only -z <base>` (objEnv)
    5. Temp index `tmpdir()/faultline-turn-tree-<clock.NewUUID()>.index`.
    6. `read-tree <base>`
    7. `update-index --force-remove -- <p>` for each removed path (sorted with `localeCompare`)
    8. For each update path: `hash-object -w --stdin` with the file bytes as input, then `update-index --add --cacheinfo <mode>,<blob>,<path>`
    9. `write-tree`
    10. `rm` of the index with `force` in `finally`
- **Object store:** every objEnv construction calls `ensureFaultlineSnapshotObjectStore`:
  1. `mkdir` of `<root>/.git/faultline/objects/{info,pack}` (recursive, 0o700). The path is the literal `<root>/.git`, not the common dir.
  2. `mkdir` of `<root>/.git/objects/info` (recursive, 0o700).
  3. Read `alternates`, split it on `/\r?\n/`, trim, and drop empty lines. If `../faultline/objects` is missing, rewrite the whole file as the lines plus that entry, joined with `"\n"` plus a final `"\n"`, with flag `w` and the default mode.
- **Clock and UUIDs:** each slow-path `writeThrowaway` uses one UUID. A single `now()` comes after the loop and after the warning callbacks, and the same `capturedAt` goes into the cache and the snapshot.
- **Cache:**
  - The path is `resolve(root, trim(rev-parse --git-common-dir))/faultline/turn-snapshot-cache.json`, without realpath.
  - Loading returns null on any parse or schema failure.
  - The cache is rewritten after every success, including a cache hit.
  - It is written with `mkdir` (recursive, no mode) and `JSON.stringify(cache, null, 2) + "\n"`, flag `w`, mode 0o666.
  - Key order: `schemaVersion "faultline.turn-snapshot-cache.v2", headCommit, treeDigest, policyDigest, allowlistDigest, contentFingerprint, deletedPaths, paths, pathDigests, capturedAt`.
  - `paths` is sorted with `localeCompare`. `pathDigests` keeps candidate (`ls-files`) order in a `*jsjson.Obj`, so integer-like keys come first, as in JS.
- **Sleep:** the default is a blocking `time.Sleep(50ms)`, skipped when the delay is 0 or less. The live op and the Go test both pass a no-op, so quiescence never retries on a quiet repository.
- **Batch staging:** PR #195's batch staging is not in frozen TS. Do not add it.

- [ ] **Step 1: Write the failing test**

`internal/turnsnap/capture_test.go` (needs real git, so skip if `git` is missing):

```go
package turnsnap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.test"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-qm", "init").Run()
	return dir
}

func TestCleanCaptureUsesFastPathAndWritesNoQuarantine(t *testing.T) {
	dir := gitRepo(t)
	snap, _, err := Capture(dir, CaptureOptions{Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Get("treeDigest").Str() == "" {
		t.Fatalf("%v", snap)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "faultline", "objects")); !os.IsNotExist(err) {
		t.Fatal("fast path created the quarantine store")
	}
}

func TestDirtyCaptureWritesAlternatesOnce(t *testing.T) {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	for i := 0; i < 2; i++ {
		if _, _, err := Capture(dir, CaptureOptions{Sleep: func(time.Duration) {}}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".git", "objects", "info", "alternates"))
	if string(b) != "../faultline/objects\n" {
		t.Fatalf("%q", b)
	}
}
```

Port the remaining capture cases from `tests/turn-snapshot.test.ts` that drive real git: 65, 82, 103, 129, 149, 159, 169, 183, 325, 335, 370–505, 525, 545, 628, 661, 702 and the restored-clean group from 725 to 863. The quiescence cases (204–278) inject `runGit`. Give `CaptureOptions` an unexported `runGit` field for tests only, and port those four too.

Live: `TestLiveTurnSnapshotCapture` uses 150 generated repositories and compares, under the same seed, the snapshot, the warnings, the cache bytes, the `.git/faultline` tree and the alternates. The repositories include:
- paths with TAB, LF, CR, spaces, non-ASCII and integer-like names;
- executable bits, deletions, renames and untracked files;
- `.faultlineignore` with descriptors listed;
- allowlisted and non-allowlisted secrets;
- an existing cache that matches, is stale, or is corrupt;
- tracked-only mode.

Each repository is copied twice so both sides start from identical bytes.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/turnsnap/ -run 'TestCleanCapture|TestDirtyCapture'`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** by transcription.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/turnsnap/ ./internal/bundle/gitproof/ && FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run 'TestLiveTurnSnapshot' -v > /tmp/s3a-t8.log 2>&1; tail -5 /tmp/s3a-t8.log`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/turnsnap internal/bundle/gitproof difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): turn-snapshot capture, object store and session cache

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: `fl doctor --security`

**Files:**
- Create: `internal/doctor/security.go`, `internal/doctor/security_test.go`
- Modify: `internal/cli/doctor.go`, `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: `safegit.Doctor()`, `nodeproc.SpawnSyncWith`, `nodefs.{Mkdtemp,Tmpdir,Join,MkdirAll,WriteFileUTF8,Exists,RemoveAll}`, `os.Chmod`, `gitinv.MaterializeFrozenOverlays(worktree, frozenWitness, nil)`, `turnsnap.Capture`.
- Produces: `doctor.RunSecurity() (jsjson.Value, error)` and `doctor.SecurityExitCode(r jsjson.Value) int`.

Transcribe `src/doctor.ts:545-760` and the `--security` branch of `doctorCommand`.

**Gotchas:**
1. **Fixture:** `fixture = nodefs.Mkdtemp(Join(Tmpdir(), "faultline-doctor-security-"))`. This is seeded through `clock.TempSuffix`, and `seed.mjs` already seeds `mkdtempSync`.
2. **Hook:** run `mkdir fixture/malicious-hooks` (recursive, 0o700). Write `pre-commit` containing `#!/bin/sh\nprintf 'hooked\\n' > HOOK_EXECUTED.marker\n`, then `chmod 0o755`, skipped on Windows.
3. **Unhardened git calls** (argv `-C fixture …`, results ignored):
   - `init`
   - `config user.email faultline-security@example.invalid`
   - `config user.name "FaultLine Security Doctor"`
   - `config core.hooksPath <hooksDir>`
   - `config filter.lfs.process malicious-lfs-filter`
   - `config filter.lfs.smudge malicious-lfs-smudge`
   - `config filter.lfs.required true`
   - then write `tracked.txt` and run `add tracked.txt`
4. **Hardened calls** (`[...Doctor, -C, fixture, ...]`):
   - `commit -m security-doctor`, then check for the marker
   - `ls-remote https://example.invalid/faultline-security-probe.git`
   - `config --get filter.lfs.process`
   - `config --get filter.lfs.smudge`
   - `status --porcelain=v1 -z`
5. **Env** for every call: `PATH`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL` (`/dev/null`, or `NUL` on Windows), `GIT_TERMINAL_PROMPT=0`, plus `SystemRoot`/`ComSpec` on Windows when set and non-empty. Encoding is utf8, there is no timeout, and maxBuffer is the default.
6. **Overlay check:** call `MaterializeFrozenOverlays` with one overlay `{path: "../escape.mjs", bytesBase64, bytesDigest}` built from `"export const hostile = true;\n"`. It passes when the error matches `/unsafe|escap/i`.
7. **Secret check:** write `leaked-secret.txt` with the secret line, built at run time from pieces (Global Constraints). Run `turnsnap.Capture(fixture, {Sleep: no-op})`. It passes when the error matches `/secret material|high-entropy/i`.
8. **Cleanup:** `RemoveAll(fixture)` in `defer`, matching `finally`. Errors from `mkdir`/`write` inside the `try` reach the CLI.
9. **Output:**
   - Each observation is sliced to 200 UTF-16 units.
   - Status is `SECURE` only if every check passes. `next` is `fl doctor --proof-ready` or `fl doctor --security`.
   - The text line format uses an em dash: `- id: STATUS — summary`.
   - Only the JSON output includes `fixtureRepository`, which is normalized to `<TMP>/…` by the harness.

**Writer cases** (`posixOnly`, fakeTools `ready`):
- `doctor-security`, `doctor-security-json`
- `doctor-security-hostile-global-config` (sets `HOME` gitconfig with `core.hooksPath`; the stripped env must ignore it)
- `doctor-security-tmp-unwritable` (`TMPDIR` points to a read-only directory; `mkdtemp` fails with Node's `EACCES` text)

- [ ] **Step 1: Write the failing test**

`internal/doctor/security_test.go`:

```go
package doctor

import (
	"os/exec"
	"testing"
)

func TestSecuritySelfTestIsSecure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r, err := RunSecurity()
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("status").Str() != "SECURE" || len(r.Get("checks").Items()) != 5 || SecurityExitCode(r) != 0 {
		t.Fatalf("%v", r)
	}
}
```

(Add `git` to `check-skips.sh`'s allowed skip reasons only if a CI runner lacks it. None does today, so the skip must never fire in CI.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/doctor/ -run TestSecuritySelfTest`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**, add the writer cases and generate goldens.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/doctor/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/doctor-security' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/doctor internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl doctor --security self-test

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Project init, sidecar hook config and `fl init`

**Files:**
- Create: `internal/projinit/projinit.go`, `internal/projinit/sidecar.go`, `internal/projinit/projinit_test.go`, `internal/cli/init.go`
- Modify: `internal/cli/cli.go`, `difftest/gen/scenarios.ts` (scenarios `init-fresh`, `init-existing-config`, `init-existing-ignore`, `init-existing-hooks`, `init-dirty-secret`, `init-worktree` (a linked git worktree, so `--git-common-dir` differs from `.git`)), `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `.github/scripts/windows-writer-skips.txt`

**Interfaces:**
- Consumes: `doctor.Run`, `doctor.DetectLikelyRuntime`, `gitinv.EnsureConfig`, `witness.BuildPolicy`, `witness.WritePolicy`, `turnsnap.Capture`, `turnsnap.RepoCachePath`, `safegit.HostCommand()`, `nodeproc.SpawnSyncWith`, `safedir.ResolveSafeDirectorySegment`.
- Produces:
  - `projinit.StarterFaultlineIgnore` (const)
  - `projinit.Options{Repository, Runtime, CliPath string; WriteIgnoreIfMissing, WriteConfig, MarkSidecarInstalled bool; StandingPolicy *StandingPolicyInput}`
  - `projinit.Plan(o Options, run doctor.Runner) (jsjson.Value, error)` (`planProjectInit`)
  - `projinit.SidecarCliCommand(args []string) (string, error)`
  - `projinit.HookConfig(cliAbs string) jsjson.Value`
  - `projinit.HookTarget(repo string) (string, bool, error)`
  - `projinit.WriteHookConfig(repo string, cfg jsjson.Value) error`
  - `projinit.CodexSidecarLedgerPath(cwd, sessionID string) (string, error)`
  - `func initCommand(e *env, args []string) error`

Transcribe `src/project-init.ts` (all), `src/cli-app.ts:1611-1755, 1839-1951` and `src/codex-sidecar.ts:62, 234, 297-314, 334`.

**Gotchas:**
- **`initCommand` order:**
  1. Validate `--runtime`, then the standing flags: they need `--yes`, a non-blank `--standing-command` and a non-blank `--standing-frozen-by`.
  2. With `--yes --cli`, run the preview `Plan`, which is doctor run #1 and calls `ensureFaultLineConfig` if a config exists.
  3. On `PREVIEW_REQUIRED`, call `SidecarCliCommand(["--cli", abs])`. It throws for a non-`.js` path, a symlink or a missing file.
  4. `HookTarget` calls `CodexSidecarLedgerPath(repo, "faultline-install-check")`, which runs git twice with `HostCommand` only and the prefix `FaultLine could not safely inspect this Git worktree: `.
  5. Write the hook config: `mkdir .codex` (0o700) if it is absent, then `StringifyIndent + "\n"` with `wx` and 0o600.
  6. Run the main `Plan`. That is doctor run #2 with `--yes --cli`, otherwise doctor run #1.
  7. `.faultlineignore`: an `existsSync` check, then `wx` with the default mode.
  8. Pre-warm (`--yes` only):
     - `mkdir .faultline` (recursive), then `RepoCachePath`, then `Capture`.
     - Each warning prints `FaultLine snapshot warning: <w>\n` to stderr, then `first-turn snapshot pre-warmed\n`.
     - Any error becomes `{status: "SKIPPED", reason}` with no stderr.
  9. Print one JSON document. The exit code is 0.
- **Init JSON key order:** `status, repository, suggestedRuntime, doctor{dockerInvestigationPreflight, likelyRuntime, diagnosticCount}, ignoreFile, config{status, path, value}, sidecar, standingPolicy, snapshotPrewarm?{status, cachePath, treeDigest, note}, next, nextCommands, limitations, note`. Check this against `project-init.ts` and `cli-app.ts` while transcribing.
- **Standing policy:**
  - The store is `config.value?.stores.witnesses ?? <repo>/.faultline/witnesses`.
  - `frozenAt` is read from the clock before the capture's `now()`.
  - `maxTimeoutSeconds` is `Number(flag) ?? 120`. `Number("abc")` is NaN, which fails the schema with the zod message. Use `jsjson.ToNumber` (`internal/jsjson/tonumber.go`), which matches `Number()`.
  - The `next` command embeds `--cli` as given, unescaped.
- **Hook JSON:**
  - Keys: `SessionStart` (with `matcher: "startup|resume|clear|compact"`), `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`. Each holds `[{hooks: [hook]}]`.
  - Hook keys: `type: "command"`, `command`, `commandWindows?`, `timeout: 30`, `statusMessage`.
  - POSIX command: `node '<abs with ' → '"'"'>' codex sidecar hook --quiet`.
  - Windows command: `node "<CommandLineToArgvW-escaped>" …`.
  - `assertSidecarCommandText` rejects control characters. Copy its text.
- **`.faultlineignore`** content is `STARTER_FAULTLINEIGNORE`: 11 comment lines joined with `"\n"` plus a trailing newline. Copy it byte for byte.

**Writer cases** (`posixOnly`; fakeTools `ready`; fakeDocker `runtime-digest` where Docker info matters):
- **plan and apply:**
  - `init-plan` (init-fresh)
  - `init-yes` (init-fresh; pre-warm on a clean repository, fast path)
  - `init-yes-dirty` (dirty repository; slow path writes `.git/faultline` and alternates)
  - `init-yes-secret` (init-dirty-secret; pre-warm SKIPPED with the reason)
  - `init-yes-worktree` (init-worktree; the cache lands in the common dir)
  - `init-twice` (two invocations: CREATED then UNCHANGED, and `.faultlineignore` not rewritten)
  - `init-existing-config-no-yes` (the config is rewritten even without `--yes`)
  - `init-existing-ignore`
- **runtime:** `init-runtime-go`, `init-runtime-bad`
- **sidecar hooks:**
  - `init-cli` (`--yes --cli tool.js`; doctor runs twice; the hooks are written)
  - `init-cli-quote` (a cli path containing `'`)
  - `init-cli-not-js`, `init-cli-symlink`, `init-cli-missing`
  - `init-existing-hooks` (`wx` collision text)
- **standing policy:**
  - `init-standing` (policy file 0600 under 0700, `frozenAt` seeded)
  - `init-standing-no-yes`, `init-standing-blank-command`, `init-standing-nan-timeout`, `init-standing-custom-id`
- **usage errors:** one case per usage error.

- [ ] **Step 1: Write the failing test**

`internal/projinit/projinit_test.go`:

```go
package projinit

import (
	"strings"
	"testing"
)

func TestHookConfigQuotesPosixPath(t *testing.T) {
	cfg := HookConfig("/tmp/it's/cli.js")
	cmd := cfg.Get("hooks", "Stop").Items()[0].Get("hooks").Items()[0].Get("command").Str()
	if cmd != `node '/tmp/it'"'"'s/cli.js' codex sidecar hook --quiet` {
		t.Fatal(cmd)
	}
}

func TestStarterIgnoreEndsWithNewline(t *testing.T) {
	if !strings.HasSuffix(StarterFaultlineIgnore, "\n") || strings.Count(StarterFaultlineIgnore, "\n") != 11 {
		t.Fatalf("%q", StarterFaultlineIgnore)
	}
}
```

(The hook config nests under `hooks` (`cli-app.ts:1696`). `STARTER_FAULTLINEIGNORE` is 11 comment lines plus `""` joined by `\n`, so it has 11 newlines.)

Also port `tests/coh12-autonomous-session.test.ts:151` (`Plan` with a standing policy gives CREATED and the policy path) and the `tests/cli.test.ts:1025` plan/apply sequence as a Go test that drives `initCommand` on a temp repository with a fake doctor runner.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/projinit/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**, add the scenarios and writer cases, and generate goldens.

- [ ] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/projinit/ ./internal/cli/ && go test -tags faultline_test ./difftest/ -run 'TestWriterReplay/init-|TestWindowsWriterSkipsList' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/projinit internal/cli difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl init with sidecar hooks, standing policy and snapshot pre-warm

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Mutation templates and cross-command flows

**Files:**
- Modify: `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `difftest/gen/scenarios.ts`, `difftest/endtoend_test.go`

**Cases** (each is a writer case with `mutations` applied before the first invocation; slice 2's mutation ops plus `chmod`, `symlink` and `add-file` where they exist):
- **Config:**
  - `.faultline/config.json` corrupted, a symlink, a directory, valid with unknown keys (strict schema), or with a `runtime` but no `image`.
  - Each is run through `runtime resolve`, `init --yes` and `init` without `--yes`.
- **Runtime mapping:** `--output` pointing at an existing directory, at a path under a file, or at a read-only directory.
- **`.faultlineignore`:** a symlink, over 256 KiB, CRLF lines, or listing `Dockerfile`. Each is run through `init --yes`, which shows the warning on stderr.
- **`.faultline-secret-allowlist.json`:** valid with an entry matching the dirty secret (pre-warm succeeds), malformed (SKIPPED with the reason), or over 256 KiB.
- **Turn-snapshot cache:** corrupt, stale head, or a different allowlist digest. Each is run through `init --yes` twice.
- **Alternates:** `.git/objects/info/alternates` already containing the entry, containing other entries with CRLF, or missing a trailing newline.
- **Flow:** `init --yes`, then `runtime prepare node --yes`, then `doctor --json --proof-ready` in one workspace (three invocations).

**End to end without Node** (`TestEndToEndSlice3aWithoutNode` in `endtoend_test.go`, spec 4.2 point 3 and plan correction 5):
- Build `fl` and run it in a temporary repository with `PATH` set to the git directory plus the `runtime-digest` fake Docker, with no `node`.
- Run `fl init --yes`, `fl doctor --proof-ready` and `fl runtime prepare node --yes`.
- Assert:
  - `init` exits 0 with `INIT_APPLIED`;
  - `doctor` exits 1 with the node diagnostic UNAVAILABLE and `--proof-ready: NOT READY`;
  - `runtime prepare` exits 0 with `PREPARED_LOCAL_DIGEST`;
  - `.faultline/config.json` holds the resolved image.

- [ ] **Step 1: Add the cases and the end-to-end test; generate goldens; run**

Run: `go test -tags faultline_test ./difftest/ -run 'TestWriterReplay|TestEndToEndSlice3aWithoutNode' > /tmp/s3a-t11.log 2>&1; tail -20 /tmp/s3a-t11.log`
Expected: PASS after Go fixes. A mismatch is fixed in Go, never in the golden.

- [ ] **Step 2: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(go): slice 3a mutation templates, flows and end to end without Node

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Fault injection, real Docker build, and the benchmark

**Files:**
- Modify: `difftest/writerfault_live_test.go` (extend `writerFaultMatrix`), `difftest/docker_live_test.go` (`TestLiveRealDockerProjectBuild`), `difftest/benchmark_test.go` (`BenchmarkDoctor`, `BenchmarkInitYes`), `difftest/BENCHMARK.md`, `difftest/stack_live_test.go`

**Fault matrix** (Linux, strace, spec 3.3):
- **Writer paths:**
  - `.faultline/config.json` and its directory
  - the runtime mapping output and its parent `mkdir`
  - `.faultlineignore`
  - the standing-policy file and directory
  - `.codex/hooks.json` and `.codex`
  - `.git/faultline/objects/{info,pack}`, `alternates`
  - the turn-snapshot cache and the temporary index
  - the `doctor --security` fixture `mkdtemp`, hook file, `chmod` and `rm`
- **Syscalls:** `openat`, `write`, `mkdir`/`mkdirat`, `fchmodat`/`chmod`, `rename`, `unlink`/`unlinkat` and `close`, each injected with `ENOSPC`, `EIO` and `EACCES` at the first matching call on that path.
- Use the slice 2 helpers (`straceInject`, `GOMAXPROCS=1` on the Go side).
- Compare stdout, stderr, the exit code and the workspace tree digest.

**Real Docker** (`TestLiveRealDockerProjectBuild`, skipped unless `FAULTLINE_REAL_DOCKER=1`):
- Use a context with `FROM node:22-alpine@sha256:<pinned, same as slice 2>` and one file.
- Run `runtime project plan`, then `runtime project build --expect-plan <digest> --yes` on both sides with a distinct tag per side.
- Compare the plan output byte for byte, the build status (`PROJECT_IMAGE_BUILT_NEEDS_REGISTRY_DIGEST`, since a local build has no RepoDigest) and the Docker argv.
- Build output text is not compared (spec 2.6).

**Benchmark** (spec 4.2 point 7): time `doctor` (fakeTools `ready`) and `init --yes` on a 2,000-file dirty repository for TS and Go. Record the median and range of 10 runs in `BENCHMARK.md`, as in slice 2.

- [ ] **Step 1: Extend the matrix and run it to see mismatches**

Run: `FAULTLINE_NODE_ORACLE=1 timeout 280 go test -tags faultline_test ./difftest/ -run TestLiveWriterFaults > /tmp/s3a-t12.log 2>&1; grep -E '^(---|FAIL|ok)' /tmp/s3a-t12.log | head -40` (on Linux, or in the podman image `localhost/r73img`)
Expected: the new cases are listed. Fix every mismatch in Go.

- [ ] **Step 2: Add the real-Docker test and benchmarks; run them**

Run: `FAULTLINE_NODE_ORACLE=1 FAULTLINE_REAL_DOCKER=1 go test -tags faultline_test ./difftest/ -run TestLiveRealDockerProjectBuild -v && go test -tags faultline_test ./difftest/ -run '^$' -bench 'BenchmarkDoctor|BenchmarkInitYes' -benchtime 10x`
Expected: PASS; numbers recorded.

- [ ] **Step 3: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(go): slice 3a fault injection, real-Docker project build, benchmarks

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: CI, coverage gate, docs, and the done check

**Files:**
- Modify:
  - `.github/workflows/go.yml`, `.github/scripts/check-skips.sh`, `.github/scripts/windows-writer-skips.txt`
  - `difftest/covergate_test.go` (add `safegit`, `hostcmd`, `runtimeimage`, `doctor`, `allowlist`, `turnsnap`, `projinit` and the new `cli` and `gitinv` files)
  - `difftest/coverage_test.go` (collect error literals from the new packages; the `not yet ported: %s` waiver text drops `doctor, init` and lists only slice 3b/4/5 commands)
  - `difftest/KNOWN_DIFFERENCES.md` (title `ce99a79`; the entries confirmed in Tasks 1–12)
  - `docs/superpowers/specs/2026-10-20-go-slice3-runtime-design.md` (the five corrections above)
  - `docs/superpowers/specs/2026-09-25-go-migration-overview.md` (spec 4.2 point 6)
  - this plan (tick the boxes)

**CI changes:**
- **`go`** (six runners): run the slice 3a writer cases. Windows skips only the `posixOnly` cases, listed by name in `windows-writer-skips.txt`; `TestWindowsWriterSkipsList` enforces the list.
- **`difftest-live`**:
  - Regenerate the scenarios and writer goldens, and fail on `git diff --exit-code`.
  - Run `TestLiveSafeGitLists`, `TestLiveHostCmd`, `TestLiveConfigWriters`, `TestLiveRuntimeMappingWriter`, `TestLiveCuratedRuntime`, `TestLiveProjectContext`, `TestLiveProjectPrepare`, `TestLiveAllowlist`, `TestLiveTurnSnapshot*` and the extended `TestLiveWriterFaults`.
  - Run `TestLiveRealDockerProjectBuild` with `FAULTLINE_REAL_DOCKER=1`.
- **`stack-live`** (macOS): add `TestLiveHostCmd`, `TestLiveProjectContext` (no sparse-file case), and `TestLiveTurnSnapshotCapture`.
- **Coverage gate:** unreachable branches are removed, not excluded.

**Overview update** (spec 4.2 point 6):
- In the slice 3 row, add the turn-snapshot capture, `secret-allowlist` and the sidecar hook-config writer.
- In the slice 4 row, drop `secret-allowlist` and keep `captureAllowlistedEvent`, PR #195's batch staging and the rest of the sidecar.
- Add slice 5 decision 4: drop the Node check from `doctor`, and have sidecar hooks call the `fl` binary.
- Change the frozen TS reference (Decisions table) to `ce99a79` (`v0.1.10-buildweek`), with a note that `faa98c0` was the pin until slice 3.

- [ ] **Step 1: Apply the CI, gate and docs changes; push the branch and read CI through the public API**

```bash
git push -u origin go-migration/slice3a
curl -s "https://api.github.com/repos/Mizore66/faultline/commits/$(git rev-parse HEAD)/check-runs" | python3 -I -c 'import json,sys; [print(r["name"], r["status"], r["conclusion"]) for r in json.load(sys.stdin)["check_runs"]]'
```

Expected: every job `success`.

- [ ] **Step 2: Done check against spec 4.2 (PR 3a)**

1. `go test ./...` is green on all six runners, and every slice 3a writer case matches its golden.
2. `difftest-live` and `stack-live` are green, including `TestLiveRealDockerProjectBuild`.
3. `TestEndToEndSlice3aWithoutNode` passes.
4. The cross-check: no Git proof bundle or prevention proof is written in 3a. Point 4 applies to PR 3b.
5. KNOWN_DIFFERENCES lists every intentional difference.
6. The overview is updated.
7. BENCHMARK is recorded.

- [ ] **Step 3: Commit and hand off for adversarial review**

```bash
git add .github difftest docs
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "ci(go): slice 3a CI, coverage gate and docs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
git push
```

Give the user `https://github.com/Mizore66/faultline/compare/main...go-migration/slice3a?expand=1`. Adversarial review rounds follow, time-boxed to about 90 minutes each, until a round finds no P0 or P1.
