# Go Slice 2 (Git path, witness store, sandbox) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Go `fl` runs `investigate git`, `minimize git|verify`, `judge-proof`, `commit-proof-preview`, the offline `witness` subcommands and `ledger bind|verify` with stdout, stderr, exit status, written files and child-process argv byte-identical to frozen TS `node dist/cli.js`, proven by a committed writer golden corpus.

**Architecture:** Writers take time, UUIDs and temp-directory names from `internal/clock`, which a `faultline_test` build seeds with the same sequence a `node --import` preload gives frozen TS. Child processes start through a new `nodeproc.Spawn` that models Node's async `child_process.spawn`. Each TS module becomes one Go file in a package per area (`witness`, `redaction`, `gitinv`, `sandbox`, `minimize`, `proofview`, and the writer half of `bundle/gitproof`). `difftest` gains a writer case kind that runs a command in a temporary workspace with fake `git`/`docker` recorders on `PATH` and compares output, the written tree and the recorded argv.

**Tech Stack:** Go 1.27 (standard library only), TypeScript generators run with the repo's `tsx`, Node 22.22.2, git, Docker (one live comparison only).

**Spec:** [docs/superpowers/specs/2026-10-14-go-slice2-git-path-design.md](../specs/2026-10-14-go-slice2-git-path-design.md) (parents: [overview](../specs/2026-09-25-go-migration-overview.md), [slice 1 spec](../specs/2026-09-25-go-slice1-verify-design.md), [slice 1 plan](2026-09-26-go-slice1-verify.md)). Read all four before starting any task; this plan reuses slice 1's packages and helpers by name.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; no dependencies.
- Never use `encoding/json` on any value that is hashed, compared canonically, written to disk or printed. Use `internal/jsjson` (`JSON.stringify(x, null, 2)` is `jsjson.StringifyIndent(v, "  ")`) and `internal/canonical`.
- Frozen TS (`src/`, `package.json`, `pnpm-lock.yaml`) is never modified. The oracle is `node dist/cli.js` built with `pnpm build` at `faa98c0`, run on Node 22.22.2 (`/opt/homebrew/bin/node` is 22.23.3; goldens are generated only by CI or with a 22.22.2 `node` first on `PATH`).
- All Go strings that hold JS values are WTF-8 (`internal/jsstr`); lengths, slices and offsets that TS takes on strings are UTF-16 (`jsstr.Length`, `jsstr.ToUTF16`).
- Every `localeCompare` sort is `canonical.SortLocale` / `canonical.LocaleCompare`; every bare `.sort()` on strings is UTF-16 code-unit order (`jsstr.CompareUTF16`, added in Task 3).
- Every byte buffer TS decodes with `toString("utf8")` is decoded with `jsstr.DecodeUTF8` (lossy, U+FFFD).
- Every ported Go function carries a comment naming its TS source, e.g. `// Port of src/witness-lock.ts:285 writeOnceJson.`
- Time, UUIDs and `mkdtemp` suffixes come only from `internal/clock` (Task 1). A direct `time.Now`, `crypto/rand` or `math/rand` call in a writer is a bug; `TestNoDirectClockReads` (Task 1) enforces it.
- Child processes start only through `nodeproc.Spawn` (async, Task 2) or `nodeproc.SpawnSync` (slice 1), matching what the TS call site uses.
- Commits follow the repo memory rule: `git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit ...`, remote `git@github-mizore66:Mizore66/faultline.git`, never the `gh` CLI. End every commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Pin GitHub Actions by commit SHA exactly as `.github/workflows/go.yml` already does.
- `formatCliFailure`'s `Next: pnpm fl help` hint stays verbatim (overview: slice 5 decisions).

## Corrections to the spec found while planning

Fold these into the spec in Task 20's docs commit:

1. **`mkdtemp` suffixes.** Node's `mkdtemp` suffix comes from libuv, not `randomUUID`, and `git-minimization.ts:994-995` hashes the worktree path into `worktreeDigest`. The preload therefore also replaces `fs.mkdtemp`, `fs.mkdtempSync` and `fs.promises.mkdtemp` (spec 2.1).
2. **Async spawn.** `runHardenedGit` and `createNodeSandboxRunner` use async `spawn` with their own timers and kill signals (SIGTERM for git, SIGKILL for Docker), not `spawnSync` (spec 3.3). Neither installs a signal handler: SIGINT reaches the `docker` CLI through the terminal's process group, and `fl` dies by the default action.
3. **Secret allowlist.** `secret-allowlist.ts` is reachable only from `turn-snapshot.ts` (slice 4), and the "256 KiB scan window" is the allowlist file's size cap (`secret-allowlist.ts:41`). It moves to slice 4 (spec 1 and 3.5).
4. **`witness propose` without `--input`.** It prints `defaultWitnessProposal()` from `src/ai.ts:44`, a constant, so it is ported; only `--live` is deferred.
5. **`judge-proof --port` default** is 4174 (`cli-app.ts:997-1039`), not the demo server's 4173.
6. **Layout.** Besides the packages in spec 1, the port adds small leaf packages (`jsre`, `safedir`, `evidence`, `witnessresult`, `ledgerbind`) and keeps the config/runtime-mapping read side in `gitinv`; the CLI owns the browser opener.

## File Structure

```
internal/clock/clock.go                 Now, NowMillis, NewUUID, TempSuffix (shared counters)
internal/clock/release.go               //go:build !faultline_test  — wall clock, crypto/rand
internal/clock/seeded.go                //go:build faultline_test   — FAULTLINE_TEST_SEED sequence
internal/nodeproc/async.go              Spawn, Child, Kill (child_process.spawn model)
internal/nodeproc/async_unix.go, async_windows.go
internal/jsstr/compare.go               CompareUTF16 (Array.prototype.sort default order)
internal/jsre/jsre.go                   JS regex source → RE2 (non-u `i`, \s, ., UTF-16 offsets)
internal/safedir/safedir.go             src/safe-directory.ts
internal/evidence/evidence.go           src/evidence-grade.ts, src/turn-proof-promotion.ts
internal/witnessresult/witnessresult.go src/witness-result.ts
internal/redaction/redaction.go         src/redaction.ts (redactText, redactValue)
internal/witness/lock.go                src/witness-lock.ts store half (propose/approve/freeze/read/verify)
internal/witness/authenticated.go       src/authenticated-witness-approval.ts
internal/witness/policy.go              src/standing-approval-policy.ts
internal/witness/autonomous.go          src/autonomous-session.ts
internal/witness/binding.go             src/incident-binding.ts (+ incident.ts id schema, incident-store default)
internal/ledgerbind/ledgerbind.go       src/ledger-binding.ts
internal/gitinv/config.go               src/config.ts (read side + resolveConfiguredImage)
internal/gitinv/runtimemap.go           src/runtime-mapping-file.ts (load side)
internal/gitinv/fingerprint.go          src/environment-fingerprint.ts
internal/gitinv/hardened.go             src/git-materialization.ts
internal/gitinv/overlay.go              src/safe-overlay.ts
internal/gitinv/investigate.go          src/git-investigation.ts
internal/sandbox/plan.go                src/sandbox.ts plan, environment, audit
internal/sandbox/run.go                 src/sandbox.ts runner, truncation, classification
internal/bundle/gitproof/write.go       src/git-proof-bundle.ts writer half
internal/minimize/minimize.go           src/git-minimization.ts minimizeGitDiff
internal/minimize/verify.go             verifyGitMinimizationResult(+File), writer
internal/proofview/page.go              src/incident-page.ts, src/git-proof-view.ts
internal/proofview/judge.go             src/judge-proof.ts, src/proof-roots.ts
internal/proofview/server.go            src/server.ts startGitProofServer
internal/cli/witness.go, ledger.go, investigate.go, minimize.go, judge.go, browser.go
difftest/gen/seed.mjs                   TS preload: seeded Date, randomUUID, mkdtemp
difftest/gen/gen-writers.ts             writer goldens (scenarios × writer templates)
difftest/gen/scenarios.ts               small git repositories and witness stores
difftest/testdata/writers.json          writer case templates
difftest/testdata/golden/writers.jsonl  writer goldens
difftest/testdata/fakedocker/<name>/docker
difftest/testdata/recorder/{git,docker} argv recorders that exec the real tool
difftest/writer_test.go                 TestWriterReplay
difftest/*_live_test.go                 new live tests per task
```

---

### Task 1: Seeded clock, UUIDs and temp names (`internal/clock`, `seed.mjs`)

**Files:**
- Create: `internal/clock/clock.go`, `internal/clock/release.go`, `internal/clock/seeded.go`, `internal/clock/clock_test.go`, `internal/clock/seeded_test.go`, `difftest/gen/seed.mjs`, `difftest/clock_live_test.go`, `difftest/directclock_test.go`
- Modify: `internal/nodefs/fs.go:485-510` (`Mkdtemp` takes its suffix from `clock.TempSuffix`), `difftest/replay_test.go` `TestMain` (build `fl` with `-tags faultline_test`)

**Interfaces:**
- Produces: `clock.Now() time.Time` (`new Date()`), `clock.NowMillis() int64` (`Date.now()`), `clock.NewUUID() string` (`crypto.randomUUID()`), `clock.TempSuffix() string` (six characters of libuv's `mkdtemp` alphabet), `clock.ISO(t time.Time) string` (`Date.prototype.toISOString`, UTC with milliseconds).

**The sequence (both sides, seed `S` = `FAULTLINE_TEST_SEED`):**
- Clock: one counter `k` shared by `Date.now()` and `new Date()` (no arguments); each read returns `1784203200000 + k` ms (2026-07-16T12:00:00.000Z) and then increments `k`. `new Date(x)` with arguments is untouched.
- UUID `n` (own counter): `b = sha256(utf8(S + "\0uuid\0" + n))[0:16]`, `b[6] = b[6]&0x0f|0x40`, `b[8] = b[8]&0x3f|0x80`, lowercase hex as `8-4-4-4-12`.
- Temp suffix `n` (own counter): `h = sha256(utf8(S + "\0tmp\0" + n))`, character `i` (0–5) is `"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"[h[i] % 62]`. On `EEXIST` the next suffix is tried; any other `mkdir` failure is delegated to the real `mkdtemp` so the error text is Node's own.
- With `FAULTLINE_TEST_SEED` unset both sides behave as release builds.

**Gotchas:**
- `dist` imports `randomUUID` by name from `node:crypto`. Assigning `crypto.randomUUID` alone does not change that binding; call `syncBuiltinESMExports()` from `node:module` after patching (checked on Node 22: assignment alone leaves the named binding unchanged; after the sync call it is patched). The same call publishes the patched `fs.mkdtemp`/`fs.mkdtempSync`; `node:fs/promises` exports are the `fs.promises` object, patch `fs.promises.mkdtemp` before syncing.
- Replace `Date` with a subclass; keep `Date.parse`, `Date.UTC` and the prototype. `zod` datetime checks are regexes and do not read the clock.
- `globalThis.crypto.randomUUID` (WebCrypto) is a different function and `dist` never calls it; leave it alone.
- Go: the release file must not read `FAULTLINE_TEST_SEED` at all (spec 2.1). `TestReleaseIgnoresSeed` runs without the tag.
- `TestNoDirectClockReads` walks `internal/` (excluding `internal/clock`, `internal/nodeproc`, `internal/boot`, `internal/sigexit`, `internal/collation` and `_test.go` files) with `go/parser` and fails on any call to `time.Now`, `time.Since`, `rand.*` from `crypto/rand` or `math/rand`, or `os.MkdirTemp`/`os.CreateTemp`.

- [x] **Step 1: Write the failing tests**

`internal/clock/seeded_test.go`:

```go
//go:build faultline_test

package clock

import "testing"

func TestSeededSequence(t *testing.T) {
	reset("slice2")
	if got := ISO(Now()); got != "2026-07-16T12:00:00.000Z" {
		t.Fatalf("first Now = %s", got)
	}
	if got := NowMillis(); got != 1784203200001 {
		t.Fatalf("second read = %d (Now and NowMillis share one counter)", got)
	}
	for i, want := range []string{"6639a543-cb34-48c9-a29a-1073897b64a8", "96c7242c-e44d-4ac1-9d9e-828863fec810"} {
		if got := NewUUID(); got != want {
			t.Fatalf("uuid %d = %s, want %s", i, got, want)
		}
	}
	for i, want := range []string{"YefOax", "I8Q8zW"} {
		if got := TempSuffix(); got != want {
			t.Fatalf("suffix %d = %s, want %s", i, got, want)
		}
	}
}
```

`internal/clock/clock_test.go`:

```go
//go:build !faultline_test

package clock

import (
	"regexp"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestReleaseIgnoresSeed(t *testing.T) {
	t.Setenv("FAULTLINE_TEST_SEED", "slice2")
	a, b := NewUUID(), NewUUID()
	if !uuidV4.MatchString(a) || a == b || a == "6639a543-cb34-48c9-a29a-1073897b64a8" {
		t.Fatalf("release UUIDs %s %s", a, b)
	}
	if ms := NowMillis(); ms < 1784203200000+86400000 {
		t.Fatalf("release clock looks seeded: %d", ms)
	}
}
```

`difftest/clock_live_test.go` runs the preload under Node and the seeded Go sequence and compares them:

```go
package difftest

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/clock"
)

// The probe reads the clock through the same bindings dist uses.
const seedProbe = `
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
const a = mkdtempSync(join(tmpdir(), "seed-probe-"));
const b = await mkdtemp(join(tmpdir(), "seed-probe-"));
rmSync(a, { recursive: true }); rmSync(b, { recursive: true });
console.log([new Date().toISOString(), Date.now(), new Date(5).toISOString(), randomUUID(), randomUUID(),
  a.slice(-6), b.slice(-6)].join("\n"));
`

func TestLiveSeedPreload(t *testing.T) {
	oracle.Skip(t)
	repo := oracle.RepoRoot(t)
	cmd := exec.Command("node", "--import", repo+"/difftest/gen/seed.mjs", "--input-type=module", "-e", seedProbe)
	cmd.Env = append(cmd.Environ(), "FAULTLINE_TEST_SEED=slice2")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	clock.ResetForTest("slice2")
	want := strings.Join([]string{clock.ISO(clock.Now()), itoa(clock.NowMillis()), "1970-01-01T00:00:00.005Z",
		clock.NewUUID(), clock.NewUUID(), clock.TempSuffix(), clock.TempSuffix()}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("preload:\n%s\ngo:\n%s", out, want)
	}
}
```

(`oracle.Skip(t)` is the existing `FAULTLINE_NODE_ORACLE` guard factored out of `oracle.Start`; `itoa` is `strconv.FormatInt(v, 10)`. `clock.ResetForTest` is the exported wrapper of `reset`, defined only in `seeded.go`, so the difftest package is always built with `-tags faultline_test`; add the tag to every `go test ./difftest/...` command in `go.yml`.)

`difftest/directclock_test.go`: the `TestNoDirectClockReads` walker described in Gotchas.

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/clock/ && go test ./internal/clock/`
Expected: FAIL (`undefined: reset`, `undefined: NewUUID`).

- [x] **Step 3: Implement**

`internal/clock/clock.go` holds `ISO` (`t.UTC().Format("2006-01-02T15:04:05.000Z")`, years outside 0–9999 are unreachable here) and the alphabet constant. `release.go`: `Now` is `time.Now()`, `NowMillis` is `time.Now().UnixMilli()`, `NewUUID` reads 16 bytes from `crypto/rand` and sets the version/variant bits, `TempSuffix` draws from `math/rand/v2`. `seeded.go`: a `sync.Mutex`-guarded state `{seed string; seeded bool; tick, uuid, tmp uint64}` initialised from `os.Getenv("FAULTLINE_TEST_SEED")` on first use; when the seed is empty, delegate to the release functions (copy them into an unexported `wall*` set in `clock.go` so both files share them).

`difftest/gen/seed.mjs`:

```js
// Seeded clock, UUIDs and mkdtemp suffixes for writer goldens (slice 2 spec 2.1).
// Loaded with `node --import`; src/ is not modified.
import crypto, { createHash } from "node:crypto";
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";

const seed = process.env.FAULTLINE_TEST_SEED;
if (seed) {
  const EPOCH = 1784203200000;
  const ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let tick = 0, uuid = 0, tmp = 0;
  const hash = (kind, n) => createHash("sha256").update(`${seed}\0${kind}\0${n}`, "utf8").digest();
  const RealDate = Date;
  class SeededDate extends RealDate {
    constructor(...args) { if (args.length === 0) super(EPOCH + tick++); else super(...args); }
    static now() { return EPOCH + tick++; }
  }
  globalThis.Date = SeededDate;
  crypto.randomUUID = () => {
    const b = hash("uuid", uuid++).subarray(0, 16);
    b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
    const h = b.toString("hex");
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
  };
  const suffix = () => { const h = hash("tmp", tmp++); let s = ""; for (let i = 0; i < 6; i++) s += ALPHABET[h[i] % 62]; return s; };
  const realSync = fs.mkdtempSync, realAsync = fs.promises.mkdtemp;
  fs.mkdtempSync = (prefix, options) => {
    for (;;) {
      const path = `${prefix}${suffix()}`;
      try { fs.mkdirSync(path, 0o700); return path; }
      catch (error) { if (error?.code !== "EEXIST") return realSync(prefix, options); }
    }
  };
  fs.promises.mkdtemp = async (prefix, options) => {
    for (;;) {
      const path = `${prefix}${suffix()}`;
      try { await fs.promises.mkdir(path, 0o700); return path; }
      catch (error) { if (error?.code !== "EEXIST") return realAsync(prefix, options); }
    }
  };
  fs.mkdtemp = (prefix, options, callback) => {
    const cb = typeof options === "function" ? options : callback;
    fs.promises.mkdtemp(prefix).then((p) => cb(null, p), (e) => cb(e));
  };
  syncBuiltinESMExports();
}
```

In `nodefs.Mkdtemp`, replace `tempChars[rand.IntN(len(tempChars))]` × 6 with `clock.TempSuffix()`. In `difftest/replay_test.go` `TestMain`, add `-tags faultline_test` to the `go build` of `cmd/fl`.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/clock/ ./internal/nodefs/ && go test ./internal/clock/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveSeedPreload|TestNoDirectClockReads|TestGoldenReplay' -v`
Expected: PASS (slice 1 goldens are unaffected: the verifier reads no clock, and its `mkdtemp` names are masked as `<GITTMP>`).

- [x] **Step 5: Commit**

```bash
git add internal/clock internal/nodefs/fs.go difftest/gen/seed.mjs difftest/clock_live_test.go difftest/directclock_test.go difftest/replay_test.go difftest/oracle .github/workflows/go.yml
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): seeded clock, UUIDs and mkdtemp names for writer goldens

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Async child processes (`nodeproc.Spawn`)

**Files:**
- Create: `internal/nodeproc/async.go`, `internal/nodeproc/async_unix.go`, `internal/nodeproc/async_windows.go`, `internal/nodeproc/async_test.go`, `difftest/spawn_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (op `spawnProbe`)

**Interfaces:**
- Consumes: `boot.BeforeSpawn()`, slice 1's `nodeEnv()`, `searchPath`, `cloexec` helpers and Windows argv quoting in `internal/nodeproc`.
- Produces:

```go
type Stdio int // StdioIgnore, StdioPipe

type SpawnOptions struct {
	Cwd      string   // "" inherits fl's cwd
	Env      []string // nil means nodeEnv() (process.env)
	Stdin    []byte   // with StdinPipe: written, then end(); EPIPE is swallowed
	StdinPipe bool    // false: stdin "ignore"
	Stdout, Stderr Stdio
	OnStdout, OnStderr func(chunk []byte) // called on one goroutine, in arrival order
}

type Exit struct {
	Code   *int   // nil when killed by a signal or never started
	Signal string // "SIGKILL", "SIGTERM", ... or ""
	Err    error  // the 'error' event (spawn failure), as Node's message: "spawn git ENOENT"
}

func Spawn(file string, args []string, o SpawnOptions) *Child
func (c *Child) Kill(sig string) bool // child.kill(sig); false when already exited or never started
func (c *Child) Wait() Exit           // returns after Node's 'close' (or 'error' when spawn failed)
```

**Gotchas:**
- Node emits `error` (not `close`) when the executable cannot be started (`ENOENT`, `EACCES`); `Code` is nil. The TS callers in Tasks 12 and 13 treat this as "exit null with the message appended to stderr"; `Exit.Err.Error()` must be exactly Node's `error.message`, e.g. `spawn docker ENOENT`. Probe it with the oracle rather than guessing; if Node also emits `close` in that case on 22.22.2, model it.
- `Kill` after the child has exited is a no-op returning false, as `child.kill()` is once `exitCode` is set.
- Arguments containing NUL make `spawn` throw synchronously (`ERR_INVALID_ARG_VALUE`). The callers validate their inputs first, so `Spawn` panics with a `jsexc` TypeError carrying Node's message; Task 13's witness validation rejects NUL before this point.
- `windowsHide: true` is the default for these call sites; on Windows reuse slice 1's `CREATE_NO_WINDOW` flag.
- Call `boot.BeforeSpawn()` first, as `SpawnSync` does.

- [x] **Step 1: Write the failing test**

`internal/nodeproc/async_test.go`:

```go
//go:build unix

package nodeproc

import (
	"bytes"
	"testing"
)

func TestSpawnStreamsAndKills(t *testing.T) {
	var out bytes.Buffer
	c := Spawn("/bin/sh", []string{"-c", "cat; printf done; sleep 30"}, SpawnOptions{
		StdinPipe: true, Stdin: []byte("in-"), Stdout: StdioPipe, Stderr: StdioPipe,
		OnStdout: func(b []byte) { out.Write(b) },
	})
	for !bytes.Contains(out.Bytes(), []byte("done")) {
		Yield()
	}
	if !c.Kill("SIGKILL") {
		t.Fatal("Kill returned false on a live child")
	}
	exit := c.Wait()
	if exit.Code != nil || exit.Signal != "SIGKILL" || out.String() != "in-done" {
		t.Fatalf("%+v %q", exit, out.String())
	}
	if c.Kill("SIGKILL") {
		t.Fatal("Kill after exit must return false")
	}
}

func TestSpawnMissingExecutable(t *testing.T) {
	exit := Spawn("faultline-no-such-binary", nil, SpawnOptions{}).Wait()
	if exit.Code != nil || exit.Err == nil || exit.Err.Error() != "spawn faultline-no-such-binary ENOENT" {
		t.Fatalf("%+v", exit)
	}
}
```

(`Yield` is a test helper in `async_test.go`: `time.Sleep(time.Millisecond)`.)

`difftest/spawn_live_test.go` drives `spawnProbe` (oracle: `spawn(file, args, {cwd, env, stdio})`, collect chunks, optional `kill(signal)` after the first stdout chunk, resolve `{events: [...names in order], code, signal, error, stdout, stderr}`) and the same through `nodeproc.Spawn` for: exit 0, exit 3, missing executable, non-executable file (`EACCES`), `cwd` that does not exist (Node reports `spawn <file> ENOENT`), stdin to a child that closed stdin early (EPIPE swallowed), kill with SIGTERM and SIGKILL. Compare `code`, `signal`, `error` and both outputs exactly.

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/nodeproc/ -run Spawn`
Expected: FAIL (`undefined: Spawn`).

- [x] **Step 3: Implement**

Unix: `syscall.ForkExec` with pipes (as `spawn_unix.go` does for `SpawnSync`), one reader goroutine per piped stream sending chunks on a single channel, a dispatcher goroutine that calls `OnStdout`/`OnStderr` in arrival order, `Wait4` in its own goroutine; `Wait` returns after the process exited **and** both pipes reached EOF (Node's `close`). Windows: `syscall.CreateProcess` path from `spawn_windows.go`.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/nodeproc/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveSpawn -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/nodeproc difftest/spawn_live_test.go difftest/gen/node-oracle.ts
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): async child processes modelled on child_process.spawn

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Leaf modules (`safedir`, `evidence`, `witnessresult`, `jsstr.CompareUTF16`)

**Files:**
- Create: `internal/safedir/safedir.go`, `internal/safedir/safedir_test.go`, `internal/evidence/evidence.go`, `internal/evidence/evidence_test.go`, `internal/witnessresult/witnessresult.go`, `internal/witnessresult/witnessresult_test.go`, `internal/jsstr/compare.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `resolveWitnessResultFromStdout`, `classifyFromWitnessResult`, `witnessResultConsistentWithExitCode`), `difftest/function_live_test.go` (`TestLiveWitnessResult`)

**Interfaces:**
- Produces:
  - `safedir.ResolveSafeDirectorySegment(path string) (string, bool)` — `null` is `("", false)`;
  - `safedir.ResolveTrustedSystemPath(path string) string`;
  - `safedir.RelativeTrustedSystemPath(from, to string) string`;
  - `evidence.CommitPathEvidence(isProof bool) (grade, label string)`;
  - `evidence.TurnPathEvidence(hasTransitions bool) (grade, label string)`;
  - the label/grade constants with their TS names in Go case (`evidence.CommitPathEvidenceGrade`, `AgentDraftEvidenceGrade`, `AgentDraftEvidenceLabel`, ...);
  - `witnessresult.Format(outcome, detail string, hasDetail bool) string`;
  - `witnessresult.Resolve(stdout string) Parsed` with `type Parsed struct{ Kind string /* "NONE"|"RESULT"|"INVALID" */; Result *Result; Error string }` mirroring `ParsedWitnessStdout`;
  - `witnessresult.ConsistentWithExitCode(r Result, exitCode int) bool`;
  - `witnessresult.Classify(outcome string) Classification`;
  - `jsstr.CompareUTF16(a, b string) int`.

Transcribe:
- `src/safe-directory.ts`: all of it (4–55).
- `src/evidence-grade.ts`: constants 31–58, `commitPathEvidence` 74–82, `turnPathEvidence` 84–104.
- `src/turn-proof-promotion.ts`: checklist 11–32, `TURN_PATH_EVIDENCE_LABEL_PROOF` 34, `soakRowsSatisfyPromotion` 48–56, `promotionCriteriaSatisfied` 58–68 (only the default-checklist call is reachable; port it as a constant `false` with the checklist kept for documentation, and say so in the comment).
- `src/witness-result.ts`: all of it (158 lines).

**Gotchas:**
- `safe-directory` only differs on darwin (the `/var` → `/private/var` and `/tmp` → `/private/tmp` aliases, checked with `lstat` and `realpath`). Write `safedir_darwin.go` and `safedir_other.go`; `process.platform` maps to `runtime.GOOS`.
- Every label contains U+2014; copy the strings with the em dash, not `--`.
- `witness-result.ts` line parsing: read it fully before porting; it splits on `/\r?\n/`, accepts the optional `FAULTLINE_WITNESS_RESULT=` marker, and validates with a strict zod object whose `detail` max is 4096 UTF-16 units (`schema.MaxLength(4096)`). Parse each candidate line with `jsjson.Parse` and catch its error the way TS's `try/catch` does.
- `CompareUTF16` compares `jsstr.ToUTF16` slices; it is what `[...].sort()` uses.

- [x] **Step 1: Write the failing tests**

`internal/evidence/evidence_test.go`:

```go
package evidence

import "testing"

func TestEvidenceGrades(t *testing.T) {
	if g, l := CommitPathEvidence(true); g != "COMMIT_PROOF" || l != "Proven at commit granularity — portable and offline-verifiable" {
		t.Fatalf("%s %s", g, l)
	}
	if g, l := CommitPathEvidence(false); g != "NONE" || l != "Commit-path localization — not certified as portable proof" {
		t.Fatalf("%s %s", g, l)
	}
	if g, _ := TurnPathEvidence(true); g != "EXPERIMENTAL_TURN" {
		t.Fatalf("promotion criteria are not met today, got %s", g)
	}
}
```

`internal/witnessresult/witnessresult_test.go`:

```go
package witnessresult

import "testing"

func TestResolve(t *testing.T) {
	line := Format("PREDICATE_FAIL", "", false)
	if line != `{"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}` {
		t.Fatalf("Format = %s", line)
	}
	p := Resolve("state=bad\r\n" + line + "\n")
	if p.Kind != "RESULT" || p.Result.Outcome != "PREDICATE_FAIL" || !ConsistentWithExitCode(*p.Result, 1) || ConsistentWithExitCode(*p.Result, 0) {
		t.Fatalf("%+v", p)
	}
}
```

`internal/safedir/safedir_test.go` (darwin-only assertions behind `runtime.GOOS == "darwin"`): `/tmp` resolves to `/private/tmp`, a symlink you create under `t.TempDir()` resolves to `("", false)`, a regular file resolves to `("", false)`.

Live: `TestLiveWitnessResult` feeds 2,000 generated stdouts (random mixes of marker and plain lines, valid and invalid JSON, extra keys, long `detail`, CRLF, duplicate and conflicting results) through the three oracle ops and the Go functions; outputs are compared as canonical JSON.

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/evidence/ ./internal/witnessresult/ ./internal/safedir/`
Expected: FAIL (undefined functions).

- [x] **Step 3: Implement** by transcribing the listed ranges.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/evidence/ ./internal/witnessresult/ ./internal/safedir/ ./internal/jsstr/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveWitnessResult -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/safedir internal/evidence internal/witnessresult internal/jsstr difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): port safe-directory, evidence grades and witness results

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: JS regular expressions and secret redaction (`jsre`, `redaction`)

**Files:**
- Create: `internal/jsre/jsre.go`, `internal/jsre/jsre_test.go`, `internal/redaction/redaction.go`, `internal/redaction/redaction_test.go`, `difftest/redaction_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `jsreExec`, `redactText`, `redactValue`)

**Interfaces:**
- Produces:
  - `jsre.MustCompile(source, flags string) *jsre.Regexp`; `(*Regexp).FindAll(s string) []jsre.Match` where `type Match struct{ Start, End int /* UTF-16 */; Groups []*string /* nil = undefined */ }`; `(*Regexp).Test(s string) bool`.
  - `type Report struct{ Redacted bool; Count int; Kinds []string; Locations []Location; Warnings []string }` and `func (Report) JSON() jsjson.Value` in TS key order (read `reportFor`, 273–286, and copy the order).
  - `redaction.RedactText(text, location string) (string, Report)`.
  - `redaction.RedactValue(value jsjson.Value, location string) (jsjson.Value, Report)`.

Transcribe `src/redaction.ts` 9–397 except `captureAllowlistedEvent` and its helpers (`validPath`, `readOwnPath`, `setOwnPath`, 405–475), which only `turn-snapshot.ts` reaches (slice 4).

**`jsre` translation rules** (only constructs used by `redaction.ts`, `sandbox.ts` and `autonomous-session.ts`; anything else panics at compile time so a later use cannot slip through):
- `\s` → `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`; `\S` and `[^\s...]` use the same set negated / merged into the class.
- `.` (no `s` flag) → `[^\n\r\x{2028}\x{2029}]`.
- Flag `i` (no `u`): do **not** use Go's `(?i)`, which folds `ſ`→`s` and `K` (U+212A)→`k`. Expand every ASCII letter in a literal to `[xX]` and every class range intersecting `A-Z`/`a-z` to include the other case. Non-ASCII literals in these patterns do not exist; panic if one appears.
- `\b`, `\d`, `\w` are ASCII in JS without `u`; Go's are ASCII too — keep them.
- Offsets: Go returns byte offsets into the WTF-8 string; convert with `jsstr.Length(s[:i])` (cache a byte→UTF-16 table per input).
- Lone surrogates are three WTF-8 bytes; RE2 sees three invalid bytes. They are never word characters and never whitespace on either side, so classes like `[^\s,;}&]+` consume them whole. `TestLoneSurrogateExtent` pins this.

**Redaction gotchas:**
- `pushCandidate` and `locationForText` work in UTF-16 indices; slice with `jsstr.ToUTF16`/`FromUTF16`.
- Candidate sort (217–237): start ascending, priority descending, end descending, then `kind` with `localeCompare`; overlap resolution is greedy.
- `reportFor` sorts warnings with bare `.sort()`: `jsstr.CompareUTF16`.
- `redactValueInContext` sorts object keys with `localeCompare` (351) and builds the redacted object in that order; array holes do not exist in parsed JSON. The cycle/accessor/opaque branches (303–311) cannot be reached from `jsjson` values; leave them out and note it.
- Digests hash the UTF-8 of the UTF-16 string: `canonical.SHA256Hex`.

- [x] **Step 1: Write the failing tests**

`internal/jsre/jsre_test.go`:

```go
package jsre

import "testing"

func TestJSCaseFoldingAndWhitespace(t *testing.T) {
	re := MustCompile(`\bsecret\s*=\s*([^\s,;}&]+)`, "gi")
	if re.Test("ſecret=x") { // JS: ſ does not fold to s without the u flag
		t.Fatal("long s must not match")
	}
	m := re.FindAll("x SECRET =　v w")
	if len(m) != 1 || *m[0].Groups[0] != "v" || m[0].Start != 2 || m[0].End != 12 {
		t.Fatalf("%+v", m)
	}
}

func TestLoneSurrogateExtent(t *testing.T) {
	s := "token=a" + string([]byte{0xed, 0xa0, 0x80}) + "b c" // a, lone U+D800, b
	m := MustCompile(`token=([^\s,;}&]+)`, "g").FindAll(s)
	if len(m) != 1 || m[0].End != 9 { // the surrogate is one UTF-16 unit
		t.Fatalf("%+v", m)
	}
}
```

`internal/redaction/redaction_test.go`:

```go
package redaction

import (
	"strings"
	"testing"
)

func TestRedactTextOpenAIKey(t *testing.T) {
	out, r := RedactText("key sk-proj-abcdefghijklmnop1234 end", "$.run.stdout")
	if !r.Redacted || r.Count != 1 || r.Kinds[0] != "OPENAI_API_KEY" {
		t.Fatalf("%+v", r)
	}
	if !strings.HasPrefix(out, "key <FAULTLINE_REDACTED:OPENAI_API_KEY:sha256:") || !strings.HasSuffix(out, "> end") {
		t.Fatalf("%q", out)
	}
}
```

`difftest/redaction_live_test.go`:
- `TestLiveJSRegex`: every pattern in `redaction.ts:144-151`, `sandbox.ts:56,83,91,270,789` and the CI-log patterns in `autonomous-session.ts:131-141`, against 5,000 generated inputs per pattern (ASCII, Unicode spaces from the list above, ſ/K/İ, lone surrogates, CRLF, U+2028), comparing `FindAll` with `jsreExec` (which returns `[...s.matchAll(re)].map(m => ({start: m.index, end: m.index + m[0].length, groups: m.slice(1)}))`).
- `TestLiveRedaction`: 3,000 generated texts (secrets of every kind embedded in noise, overlapping candidates, keys in JSON, YAML, env and URL forms) through `redactText`, plus 1,000 generated JSON values through `redactValue`; compare redacted output and the canonical report.

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/jsre/ ./internal/redaction/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** `jsre` (a small parser over the pattern source that emits RE2 syntax) and transcribe `redaction.ts`.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/jsre/ ./internal/redaction/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveJSRegex|TestLiveRedaction' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/jsre internal/redaction difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): JS regex translation and secret redaction

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Witness store, incident binding, and `witness propose|approve|freeze|verify`

**Files:**
- Create: `internal/witness/lock.go`, `internal/witness/binding.go`, `internal/witness/lock_test.go`, `internal/witness/binding_test.go`, `internal/cli/witness.go`, `internal/cli/witness_test.go`
- Modify: `internal/cli/cli.go` (dispatch `witness`), `internal/cli/args.go` (add `requiredOption`, `witnessStore`, `readJSONInput`), `internal/bundle/gitproof/witness.go` and `witness_schemas.go` (export what `witness` reuses)

**Interfaces:**
- Consumes: `gitproof.FrozenWitnessSchema`, the unexported verify helpers in `gitproof/witness.go` (export them as `gitproof.VerifyProposal`, `gitproof.VerifyApproval`, `gitproof.VerifyFrozenRecord`, `gitproof.WitnessPayload`), `redaction.RedactValue`, `safedir.*`, `canonical.*`, `jscase.Lower`, `clock.Now`, `nodefs`.
- Produces:
  - `witness.Propose(store string, input jsjson.Value) (jsjson.Value, error)` (the parsed proposal);
  - `witness.ReadProposal(store, id string) (jsjson.Value, error)`;
  - `witness.Approve(store, id string, input jsjson.Value) (jsjson.Value, error)`;
  - `witness.ReadApproval`, `witness.Freeze(store, id string, opts FreezeOptions) (jsjson.Value, error)` with `type FreezeOptions struct{ FrozenAt string; HasFrozenAt bool }`;
  - `witness.ReadFrozen(store, id string) (jsjson.Value, error)`;
  - `witness.VerifyFrozen(store, id, expected string, expectedProvided bool) gitproof.WitnessVerification`;
  - `witness.WriteOnceJSON(path string, v jsjson.Value) error` and `witness.EnsureDirectoryIsSafe(dir string) error` (reused by Tasks 7 and 8);
  - `witness.WriteSessionBinding(store string, in BindingInput) (jsjson.Value, error)` with `type BindingInput struct{ IncidentID string; ExpectDigest, LedgerPath, UpdatedAt *string }`;
  - `witness.ReadSessionBinding(store, id string) (jsjson.Value, bool, error)`;
  - `witness.ResolveInheritedSessionFacts(in InheritInput) InheritedFacts` (fields as `InheritedSessionFacts`, incident-binding.ts:109);
  - `witness.DefaultIncidentDraftStore(repo string) string`;
  - in package `cli`: `requiredOption(args []string, flag string) (string, error)`, `witnessStore(args []string) string`, `readJSONInput(file string) (jsjson.Value, error)`.

Transcribe:
- `src/witness-lock.ts`: `WitnessProposalInputSchema` 40, `HumanApprovalInputSchema` 90, `FreezeOptionsSchema` 119, `assertUniqueOverlayPaths` 185–194, `buildProposal` 217–244, `assertDirectoryIsSafe`/`ensureDirectoryIsSafe` 246–256, `storePaths` 258–283, `writeOnceJson` 285–293, the readers 295–315, `proposeWitness` 383–388, `readWitnessProposal` 390–393, `approveWitnessProposal` 399–422, `readWitnessApproval` 424–427, `freezeApprovedWitness` 434–455, `readFrozenWitness` 457–460, `verifyFrozenWitness` 487–553.
- `src/incident-binding.ts`: all of it (134 lines). `IncidentDraftIdSchema` from `src/incident.ts:12,18`. `defaultIncidentDraftStore` from `src/incident-store.ts:83-85`.
- `src/ai.ts:44-52` `defaultWitnessProposal` (constant).
- `src/cli-app.ts`: `option` 144, `requiredOption` 149, `readJsonInput` 244, `witnessStore` 252, `witnessProposal` 969–985, the `witness` cases `propose` (without `--live`), `approve`, `freeze`, `verify` (1348–1456, verify without `--keyring` here; Task 7 adds it), the usage throw at 1458.

**Gotchas:**
- Clock reads: `proposedAt` (230), `approvedAt` (416), `frozenAt` (447), `updatedAt` in the binding (100). Each is `clock.ISO(clock.Now())`, read at the same point in the flow as TS. The binding read happens **after** reading the previous binding record.
- `writeOnceJson` opens with `wx`, mode 0600; on `EEXIST` it throws `Immutable witness lock record already exists: <path>`; other errors propagate with Node's message (`nodefs` error text). Directories: `mkdirSync({recursive: true, mode: 0o700})`.
- The incident binding is written with `writeFileSync` (no `wx`, overwrite, mode 0600), not atomically. Undefined keys disappear from canonical JSON.
- `witness freeze` swallows every binding error (`try {} catch {}`); Go must swallow `jsexc` throws too (`jsexc.Try`).
- Overlay duplicate check lower-cases with `toLocaleLowerCase("en-US")` → `jscase.Lower`; overlay sort is `localeCompare`.
- `Buffer.from(b64, "base64")` is lenient; the schema already requires canonical base64, so the strict decoder is equivalent **after** validation only. Keep the order: validate, then decode.
- `witness propose --input`: the `incidentPacket` key of an object input is replaced by its redacted value with spread semantics (`{...input, incidentPacket}` keeps the key's original position if present, appends it if absent). `jsjson.Obj` must preserve that.
- `witness propose --live` prints `not yet ported: witness propose --live` through `fail` (spec 1, out of scope).
- `option()` returns the next argument even if it starts with `--`; `requiredOption` rejects empty and `--`-prefixed values with `Missing required option: <flag>`.
- CLI stdout is `JSON.stringify(x, null, 2) + "\n"`: `jsjson.StringifyIndent(v, "  ")`; key order exactly as written in each `process.stdout.write`.

- [x] **Step 1: Write the failing tests**

`internal/witness/lock_test.go`:

```go
//go:build faultline_test

package witness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func proposalInput(t *testing.T) jsjson.Value {
	v, err := jsjson.Parse(`{"proposalId":"demo-1","incidentPacket":{"summary":"refund currency"},
		"witness":{"behavior":"b","command":"node witness.mjs",
		"overlays":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"policy":{"network":"disabled","credentials":"redacted","timeoutSeconds":30}}}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestProposeApproveFreezeVerify(t *testing.T) {
	clock.ResetForTest("witness")
	store := t.TempDir()
	p, err := Propose(store, proposalInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Get("proposedAt").Str() != "2026-07-16T12:00:00.000Z" {
		t.Fatalf("proposedAt %s", p.Get("proposedAt").Str())
	}
	approval, _ := jsjson.Parse(`{"approvedBy":"reviewer@example.test"}`)
	if _, err := Approve(store, "demo-1", approval); err != nil {
		t.Fatal(err)
	}
	frozen, err := Freeze(store, "demo-1", FreezeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyFrozen(store, "demo-1", frozen.Get("frozenDigest").Str(), true)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
	info, _ := os.Stat(filepath.Join(store, "frozen", "demo-1.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, err := Propose(store, proposalInput(t)); err == nil || !strings.HasPrefix(err.Error(), "Immutable witness lock record already exists: ") {
		t.Fatalf("second propose: %v", err)
	}
}
```

`internal/cli/witness_test.go`:

```go
//go:build faultline_test

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWitnessUsageErrors(t *testing.T) {
	for _, c := range []struct{ args, stderr string }{
		{"witness approve", "FaultLine error: Usage: fl witness approve <proposal-id> --approved-by <actor>\nNext: pnpm fl help\n"},
		{"witness freeze", "FaultLine error: Usage: fl witness freeze <proposal-id> [--repo <directory>] [--draft-store <directory>]\nNext: pnpm fl help\n"},
		{"witness approve x --approved-by --note", "FaultLine error: Missing required option: --approved-by\nNext: pnpm fl help\n"},
	} {
		var out, errb bytes.Buffer
		if code := Run(strings.Fields(c.args), &out, &errb); code != 1 || errb.String() != c.stderr {
			t.Fatalf("%s: %d %q", c.args, code, errb.String())
		}
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/witness/ ./internal/cli/ -run 'Witness|Propose'`
Expected: FAIL (undefined: `Propose`; `witness` prints `not yet ported`).

- [x] **Step 3: Implement** by transcribing the listed ranges. Add `case command == "witness": return witnessCommand(e, rest)` to `dispatch`; subcommands not yet ported (`sign`, `policy`, `resolve-autonomous`, `ratify`, `review`, `implement`) return `fail("not yet ported: witness %s", sub)` until Tasks 7–8.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/witness/ ./internal/cli/`
Expected: PASS. (Golden comparison against TS arrives with the harness in Task 6.)

- [x] **Step 5: Commit**

```bash
git add internal/witness internal/cli internal/bundle/gitproof
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): witness store, incident binding, witness propose/approve/freeze/verify

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Writer replay harness (scenarios, recorders, fake Docker, `TestWriterReplay`)

**Files:**
- Create: `difftest/gen/scenarios.ts`, `difftest/gen/gen-writers.ts`, `difftest/scenarios_test.go`, `difftest/writer_test.go`, `difftest/testdata/writers.json`, `difftest/testdata/golden/writers.jsonl`, `difftest/testdata/recorder/git`, `difftest/testdata/recorder/docker`, `difftest/testdata/fakedocker/pass-fail/docker`, `difftest/testdata/fakedocker/README.md`
- Modify: `difftest/replay_test.go` (`runFlOnce` gains a `dir` parameter; existing callers pass `repo`), `difftest/cases.go` and `difftest/gen/cases.ts` (export `normalize` helpers for the workspace path), `.github/workflows/go.yml` (regenerate writer goldens in `difftest-live` and fail on drift), `.github/scripts/check-skips.sh` allowlist (Windows writer skips)

**Interfaces:**
- Consumes: `clock` (Task 1), `caseEnv`, `copyTree`, `treeDigest`, `loadBaseEnv`, `normalizeChecked`, the TestMain environment, the oracle.
- Produces:
  - writer template schema (`difftest/testdata/writers.json`, an array):

    ```json
    {"id": "approve-twice", "scenario": "witness-proposed",
     "argv": [["witness", "approve", "demo-1", "--approved-by", "r@example.test"],
              ["witness", "approve", "demo-1", "--approved-by", "r@example.test"]],
     "fakeDocker": "pass-fail", "env": {}, "posixOnly": false, "mutations": []}
    ```

    `argv` is a list of invocations run in order in one workspace (later ones see earlier writes). `scenario` names a builder in `scenarios.ts`/`scenarios_test.go`. `mutations` reuse slice 1's mutation ops on workspace files, applied before the first invocation. `fakeDocker` names `difftest/testdata/fakedocker/<name>`; absent means the recorder execs the real `docker` (only the live Docker test does that).
  - golden line (`golden/writers.jsonl`): `{"case": id, "inv": n, "stdout", "stderr", "exit", "tree": treeDigest(workspace) after invocation n, "files": {relPath: sha256hex} for files that changed in invocation n, "argv": ["git ...", "docker ..."]}` — `files` makes a failing tree comparison name the file (spec 2.3, point 2).
  - Go: `func runWriterCase(t *testing.T, tpl writerTemplate) []writerResult` and `TestWriterReplay`.

**Workspace and recording rules:**
- Each case runs in `<mkdtemp>/ws` (the command's cwd), with `HOME`/`USERPROFILE` in `<mkdtemp>/home` and `TMPDIR` (`TEMP`/`TMP` on Windows) in `<mkdtemp>/tmp`, so every `mkdtemp` lands in a directory the case owns.
- `FAULTLINE_TEST_SEED` is the case id; both sides start every invocation from counter 0 (separate processes), so a sequence mismatch shows up as a digest difference.
- `PATH` is `difftest/testdata/recorder:<fakedocker/name>:<real PATH>`. `recorder/git` appends `git <args joined by \x1f>` to `$FAULTLINE_ARGV_LOG` and execs the real git found after itself on `PATH`; `recorder/docker` does the same for `docker` and then execs the next `docker` on `PATH` (the fake). Argv are compared after normalization.
- Normalization: workspace path → `<WS>`, case tmp → `<TMP>`, home → `<HOME>`, `127.0.0.1:<port>` → `127.0.0.1:<PORT>` (judge-proof serve mode), plus slice 1's `<BUNDLE>`/`<GITTMP>`/git-detail masks. Never normalize digests: they must already match.
- `FAULTLINE_NO_BROWSER=1` is always set; the browser path is tested separately (Task 18).
- Fake Docker protocol (`difftest/testdata/fakedocker/<name>/docker`, POSIX sh): for `run`, read the bind-mount source from `--mount type=bind,src=<dir>,...`, read `<dir>/state.txt`, and print/exit from a table in the script (for `pass-fail`: `bad` → stdout `state=bad` + `PREDICATE_FAIL` result line, exit 1, stderr `witness failed`; anything else → `PREDICATE_PASS`, exit 0; this matches `difftest/gen/fixtures.ts:82-95`). For `rm`, exit 0 silently. Other fake Dockers (Task 13) add `timeout` (sleep past the limit), `flood` (print more than `maxOutputBytes`), `daemon-down` (exit 125 with the daemon message), `missing-image` and `crash` (killed by SIGSEGV).
- `posixOnly` cases (fake Docker and recorders are sh scripts) are skipped on Windows with the existing `windowsUncreatable`-style allowlist; add `TestWriterReplay/.*` POSIX names to `check-skips.sh`'s Windows pattern.

**Scenarios added in this task:** `empty` (no files), `witness-proposed` (a store with one proposal, built by running the TS module functions under the seed so the files are identical to what `fl` would write), `witness-approved`, `witness-frozen`. Later tasks add scenarios next to the commands that need them; each scenario is a pure function of its name.

**Writer cases added in this task (Task 5's commands):** `propose-sample` (no `--input`), `propose-input`, `propose-input-secrets` (an incident packet containing every detector kind), `propose-input-not-object`, `propose-twice`, `propose-symlink-store` (store is a symlink), `approve`, `approve-note`, `approve-empty-note`, `approve-unknown`, `approve-mutated` (proposal file edited after proposing), `approve-twice`, `freeze`, `freeze-unapproved`, `freeze-with-draft-store`, `freeze-binding-dir-is-file` (binding write fails and is swallowed), `verify`, `verify-expect-digest`, `verify-bad-digest`, `verify-missing-frozen`, `verify-tampered-approval`, and one case per usage error.

**Gotchas:**
- `gen-writers.ts` runs `process.execPath --import difftest/gen/seed.mjs dist/cli.js ...argv` with `cwd = ws`; it refuses to run if `process.version !== "v22.22.2"` unless `FAULTLINE_ALLOW_NODE_MISMATCH=1` (local experiments only; CI never sets it).
- The oracle and the Go replay must build the scenario identically. Scenarios are built by TS once into `difftest/testdata/scenarios/<name>/` (committed, like `bases/`), and both sides copy that directory. Do not rebuild scenarios in Go.
- `TestWriterReplay` must fail when a golden has a case or invocation the templates do not produce, and vice versa (mirror replay_test.go:211).

- [x] **Step 1: Write the failing test**

`difftest/writer_test.go`:

```go
package difftest

import (
	"testing"
)

func TestWriterReplay(t *testing.T) {
	repo := repoRoot(t)
	templates := loadWriterTemplates(t, repo)
	goldens := loadWriterGoldens(t, repo)
	seen := map[string]bool{}
	for _, tpl := range templates {
		t.Run(tpl.ID, func(t *testing.T) {
			if tpl.PosixOnly && windows {
				t.Skip("POSIX fake git/docker recorders")
			}
			for _, got := range runWriterCase(t, repo, tpl) {
				key := tpl.ID + "|" + itoa(int64(got.Inv))
				seen[key] = true
				want, ok := goldens[key]
				if !ok {
					t.Fatalf("no golden for %s", key)
				}
				compareWriter(t, key, got, want) // stdout, stderr, exit, tree (names changed files), argv
			}
		})
	}
	for key := range goldens {
		if !seen[key] && !windows {
			t.Errorf("golden %s has no template", key)
		}
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run TestWriterReplay`
Expected: FAIL (`undefined: loadWriterTemplates`).

- [x] **Step 3: Implement** the TS generator, the Go runner, scenarios, recorders and `pass-fail` fake Docker; add the writer cases listed above; generate goldens:

```bash
pnpm build && pnpm exec tsx difftest/gen/scenarios.ts && pnpm exec tsx difftest/gen/gen-writers.ts
```

- [x] **Step 4: Run to verify the replay passes**

Run: `go test -tags faultline_test ./difftest/ -run TestWriterReplay -v`
Expected: PASS for every Task 5 case. A mismatch is fixed in Go, never in the golden.

- [x] **Step 5: Commit**

```bash
git add difftest .github
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(go): writer replay harness with seeded oracle, recorders and fake Docker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Authenticated approvals (`witness sign`, `witness verify --keyring`)

**Files:**
- Create: `internal/witness/authenticated.go`, `internal/witness/authenticated_test.go`, `difftest/testdata/scenarios/keys/` (one Ed25519 key pair and keyrings, generated by `scenarios.ts`)
- Modify: `internal/cli/witness.go` (`sign`, `verify --keyring`, `--require-signature`), `difftest/testdata/writers.json`, `difftest/gen/scenarios.ts`

**Interfaces:**
- Consumes: `witness.ReadFrozen`, `witness.VerifyFrozen`, `safedir.*`, `clock.Now`.
- Produces: `witness.SignAuthenticatedApproval(store, id string, o SignOptions) (jsjson.Value, error)` with `type SignOptions struct{ PrivateKeyPEM string; Keyring jsjson.Value; SignedAt *string }`; `witness.VerifyAuthenticatedApproval(store, id string, o VerifyAuthOptions) jsjson.Value` with `type VerifyAuthOptions struct{ ExpectedFrozenDigest *string; Keyring *jsjson.Value; RequireSignature bool }`.

Transcribe `src/authenticated-witness-approval.ts` (412 lines, all of it) and `src/cli-app.ts:1413-1456`.

**Gotchas:**
- Ed25519 via `crypto/ed25519`; private keys from `x509.ParsePKCS8PrivateKey` on the PEM block, public keys via `x509.ParsePKIXPublicKey`; fingerprints are `sha256:` + hex of `x509.MarshalPKIXPublicKey`. `sign(null, ...)` is pure Ed25519 (no prehash), so signatures are deterministic and match byte for byte.
- `signingBytes` is the UTF-8 of `canonicalJson({domain, payload})`.
- Signatures are standard padded base64.
- `TimestampSchema` here requires `new Date(v).toISOString() === v`: parse with JS `Date` semantics for the ISO subset (`jsdate` helper inside the package; only `YYYY-MM-DDTHH:mm:ss.sssZ` round-trips, so the check is "matches that regex and is a real calendar instant").
- `ReviewerIdentitySchema`: trim (JS trim), length 1–512 UTF-16 units, no `\u0000-\u001f\u007f`.
- `MAX_APPROVAL_BYTES = 500_000` is checked on file size before `lstat` — keep the order of 195–204.
- The private-key read failure message is `Unable to read reviewer private key <file>: <message>`; `--require-signature` without `--keyring` throws `--require-signature needs --keyring <trusted-reviewers.json>.`.
- Directories are created one segment at a time with mode 0700 (158), refusing symlinked segments.
- The receipt write is `wx`, mode 0600; `EEXIST` becomes `Authenticated witness approval already exists and cannot be overwritten: <path>`.
- Copy every error string in 254–277 and 365–404 verbatim.

- [x] **Step 1: Write the failing test**

`internal/witness/authenticated_test.go`:

```go
//go:build faultline_test

package witness

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestSignThenVerify(t *testing.T) {
	clock.ResetForTest("sign")
	store := frozenStore(t) // helper from lock_test.go: propose, approve, freeze demo-1
	pub, priv, _ := ed25519.GenerateKey(zeroReader{})
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	keyring := keyringFor(t, "reviewer-1", pub) // builds {schemaVersion, reviewers:[...]} with the fingerprint
	receipt, err := SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: privPEM, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyAuthenticatedApproval(store, "demo-1", VerifyAuthOptions{Keyring: &keyring, RequireSignature: true})
	if v.Get("status").Str() != "VERIFIED" || receipt.Get("keyId").Str() != "reviewer-1" {
		t.Fatalf("%s", jsjson.Stringify(v))
	}
}
```

(`zeroReader` yields zero bytes so the key is fixed; `keyringFor` is a test helper in the same file.)

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/witness/ -run TestSign`
Expected: FAIL (undefined).

- [x] **Step 3: Implement**; add writer cases: `sign`, `sign-twice`, `sign-unfrozen`, `sign-wrong-key`, `sign-bad-pem`, `sign-missing-key-file`, `sign-keyring-duplicate-id`, `sign-keyring-not-ed25519`, `verify-keyring`, `verify-keyring-untrusted`, `verify-require-signature`, `verify-require-signature-missing`, `verify-require-signature-no-keyring`, `verify-receipt-tampered`, `verify-receipt-oversized`. Regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/witness/ && go test -tags faultline_test ./difftest/ -run TestWriterReplay`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/witness internal/cli difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): authenticated witness approvals (witness sign, verify --keyring)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Standing policies and autonomous sessions (`witness policy`, `resolve-autonomous`, `ratify`)

**Files:**
- Create: `internal/witness/policy.go`, `internal/witness/autonomous.go`, `internal/witness/policy_test.go`
- Modify: `internal/cli/witness.go`, `difftest/testdata/writers.json`, `difftest/gen/scenarios.ts`

**Interfaces:**
- Consumes: `witness.Approve`, `witness.Freeze`, `witness.ReadProposal`, `gitproof.WitnessPayload`, `evidence.AgentDraft*`, `jsre`.
- Produces: `witness.BuildPolicy(input jsjson.Value) (jsjson.Value, error)`, `witness.MatchPolicy(proposal, policy jsjson.Value) (bool, []string)`, `witness.ApplyPolicy(store, id string, policy jsjson.Value, o ApplyOptions) (jsjson.Value, error)`, `witness.WritePolicy(store string, policy jsjson.Value) (string, error)`, `witness.ReadPolicy(store, policyID string) (jsjson.Value, error)`, `witness.ResolveAutonomous(store, id string, policy *jsjson.Value) (jsjson.Value, error)`, `witness.Ratify(store, id string, o RatifyOptions) (jsjson.Value, error)`, `type AutonomousSessionError struct{ Code, Message string }`.

Transcribe `src/standing-approval-policy.ts` (199 lines), `src/autonomous-session.ts` (323 lines, except `commandsSuggestedInCiLog` 131–141, `assertCanExportProofFromWitnessStore` 288–302 and `assertEvidenceGradeAllowsProofExport` 304–311, which only guided investigate reaches — slice 5), and `src/cli-app.ts:1272-1346`.

**Gotchas:**
- Policies and parked/ratification records are `JSON.stringify(x, null, 2) + "\n"` (not canonical), `wx`, 0600. A policy `EEXIST` propagates as Node's raw error (`EEXIST: file already exists, open '<path>'`); a ratification `EEXIST` becomes `AutonomousSessionError` `ALREADY_RATIFIED`.
- `readStandingApprovalPolicy` creates the policy directory (154) — a read that writes. The writer goldens' tree digest catches a Go port that skips it.
- `policyId` is guarded only by the escape check (`relative` empty, starts with `..`, or contains `:`).
- Template sort: `localeCompare` on path, then on base64 (`||` chain).
- Clock reads: `policy freeze` reads `new Date()` once at `cli-app.ts:1284`; `resolveAutonomous` reads approve (416) then freeze (447) on a match, or `parkedAt` (167) on no match; `ratify` reads once (259) and reuses the value for `approvedAt` and `frozenAt`.
- `ratify`: the digest-mismatch check runs before the malformed-digest check (242–250).
- `--max-timeout` must be a positive integer (`Number(x)` then `Number.isInteger && > 0`), else `--max-timeout must be a positive integer.`; JS `Number("")` is 0, `Number("1e2")` is 100, `Number(" 5 ")` is 5 — use a new `jsjson.ToNumber(s string) float64` (ECMAScript `StringToNumber`: JS whitespace trimmed, empty → 0, `0x`/`0o`/`0b` prefixes, `Infinity`, decimal with exponent, anything else NaN), with a live test against `Number(s)` over 20,000 generated strings.
- Verbatim strings: note `${APPROVED_AFTER_EXECUTION}: ${note ?? "human owned the question after execution; digests identical"}`; parked notification `Parked AGENT_DRAFT ${id}: no standing-policy match. Session continues; human review required before proof.`; match notification `Standing policy ${policyId} auto-froze ${id}.`.
- `...agentDraftAnnotation()` keys are inserted after `status`.

- [x] **Step 1: Write the failing test**

`internal/witness/policy_test.go`:

```go
//go:build faultline_test

package witness

import (
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestPolicyMatchFreezesAndMismatchParks(t *testing.T) {
	clock.ResetForTest("policy")
	store := proposedStore(t) // helper from lock_test.go
	policyIn, _ := jsjson.Parse(`{"schemaVersion":"faultline.standing-approval-policy.v1","policyId":"p1",
		"frozenBy":"ops@example.test","frozenAt":"2026-07-16T11:00:00.000Z","allowedCommands":["node witness.mjs"],
		"allowedOverlayTemplates":[{"path":"witness.mjs","bytesBase64":"Y29uc29sZS5sb2coMSk="}],
		"maxTimeoutSeconds":60,"network":"disabled","credentials":"redacted"}`)
	policy, err := BuildPolicy(policyIn)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ResolveAutonomous(store, "demo-1", &policy)
	if err != nil || r.Get("outcome").Str() != "FROZEN_BY_POLICY" || r.Get("notification").Str() != "Standing policy p1 auto-froze demo-1." {
		t.Fatalf("%v %s", err, jsjson.Stringify(r))
	}
	other := proposedStoreWithCommand(t, "rm -rf /")
	r, _ = ResolveAutonomous(other, "demo-1", &policy)
	if r.Get("outcome").Str() != "PARKED_AGENT_DRAFT" {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}
```

(`proposedStore` and `proposedStoreWithCommand` are helpers in `lock_test.go` that propose `demo-1` with the overlay above and the given command.)

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/witness/ -run TestPolicy`
Expected: FAIL (undefined).

- [x] **Step 3: Implement**; add writer cases: `policy-freeze`, `policy-freeze-twice`, `policy-freeze-bad-timeout` (×4: `0`, `-1`, `1.5`, `abc`), `policy-freeze-escape-id`, `policy-apply-match`, `policy-apply-no-match` (each mismatch reason), `policy-apply-missing-policy`, `resolve-autonomous-match`, `resolve-autonomous-park`, `resolve-autonomous-no-policy`, `ratify`, `ratify-twice`, `ratify-digest-mismatch`, `ratify-malformed-digest`, usage errors. Regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/witness/ && go test -tags faultline_test ./difftest/ -run TestWriterReplay`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/witness internal/cli internal/jsjson difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): standing approval policies and autonomous session records

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Ledger binding (`ledger bind`, `ledger verify`)

**Files:**
- Create: `internal/ledgerbind/ledgerbind.go`, `internal/ledgerbind/ledgerbind_test.go`, `internal/cli/ledger.go`
- Modify: `internal/cli/cli.go`, `difftest/gen/node-oracle.ts` (op `verifyLedgerBoundInvestigation`), `difftest/function_live_test.go`, `difftest/testdata/writers.json`, `difftest/gen/scenarios.ts`

**Interfaces:**
- Consumes: `gitproof.CodexLifecycleLedgerSchema`, `gitproof.VerifyCodexLifecycleLedger`, `gitproof.GitInvestigationResultSchema`, `gitproof.GitCommitStateSchema`, the zod issue formatting helpers in `gitproof/ledger.go` (`issuePath`, `issueLines` — export them), `clock.NewUUID`.
- Produces: `ledgerbind.Bind(ledger, investigation jsjson.Value) (report jsjson.Value)`, `ledgerbind.Create(ledger, investigation jsjson.Value, ledgerPath string) (jsjson.Value, error)`, `ledgerbind.Verify(value jsjson.Value, expected string, provided bool) jsjson.Value`, `ledgerbind.WriteAtomic(path string, v jsjson.Value) (string, error)`, `ledgerbind.VerifyFile(path, expected string, provided bool) jsjson.Value`, `ledgerbind.ReadVerified(path string) (jsjson.Value, error)`, `type BindingError struct{ Message string }`.

Transcribe `src/ledger-binding.ts` (613 lines, all of it) and `src/cli-app.ts:2412-2428`.

**Gotchas:**
- `atomicWrite` (536–565): `mkdirSync(dir, {recursive: true})` with no mode; staged name `.<basename>.<pid>.<uuid>.tmp` opened `wx` 0600; write, fsync, close, `renameSync`, best-effort directory fsync, `finally` unlink if present. `process.pid` differs between runs but the staged file never survives a successful write; on a failed rename it does survive, so the fault cases (Task 20) mask `<pid>`.
- `pathsEqual` (219–225) on win32 lower-cases with `toLocaleLowerCase()` **without** a locale (`jscase.Lower`, the default-locale path); POSIX compares `resolve()` results exactly.
- zod messages: `Invalid lifecycle ledger at ${path.join(".") || "root"}: ${message}`, likewise `Invalid Git investigation` and `Invalid ledger-bound investigation`.
- `Map` iteration is insertion order; use slices of keys, not Go maps.
- Verification result key order: `valid, errors, bindingDigest, externalDigestStatus, ledgerBinding, investigation`.

- [x] **Step 1: Write the failing test**

`internal/ledgerbind/ledgerbind_test.go`:

```go
package ledgerbind

import (
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
)

func TestBindCommittedBase(t *testing.T) {
	ledger := gitproof.BaseFileForTest(t, "git-fully-bound", "lifecycle/ledger.json")
	inv := gitproof.BaseFileForTest(t, "git-fully-bound", "investigation.json")
	bound, err := Create(ledger, inv, "lifecycle/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(bound, bound.Get("bindingDigest").Str(), true)
	if !v.Get("valid").Bool() || v.Get("externalDigestStatus").Str() != "MATCH" {
		t.Fatalf("%v", v)
	}
}
```

(`gitproof.BaseFileForTest` is slice 1's `baseFile` helper, moved to `gitproof/testing.go` under `//go:build faultline_test` so other packages can use it.)

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/ledgerbind/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement**; add oracle op and `TestLiveLedgerBinding` (2,000 corrupted bound investigations via `gen.CorruptDeep`, compare canonical verification); add writer cases `ledger-bind`, `ledger-bind-unbound-ledger`, `ledger-bind-tree-mismatch`, `ledger-bind-invalid-investigation`, `ledger-bind-output-is-dir`, `ledger-verify`, `ledger-verify-expect-digest`, `ledger-verify-tampered`, usage errors. Regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/ledgerbind/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveLedgerBinding|TestWriterReplay'`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/ledgerbind internal/cli internal/bundle/gitproof difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): ledger-bound investigations (ledger bind, ledger verify)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Image configuration, runtime mappings and environment fingerprints

**Files:**
- Create: `internal/gitinv/config.go`, `internal/gitinv/runtimemap.go`, `internal/gitinv/fingerprint.go`, `internal/gitinv/support_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `loadRuntimeMappingDocument`, `computeEnvironmentFingerprint`, `resolveRuntimeImageForFingerprint`), `difftest/function_live_test.go`

**Interfaces:**
- Produces: `gitinv.DigestPinnedImage` (matcher), `gitinv.ReadConfig(repo string) (jsjson.Value, bool, error)`, `gitinv.ResolveConfiguredImage(repo string) (string, bool, error)`, `gitinv.LoadRuntimeMapping(doc jsjson.Value) ([]MappingEntry, error)` with `type MappingEntry struct{ Fingerprint, Image string }` (in `Object.entries` order), `gitinv.ComputeFingerprint(dir string) (jsjson.Value, error)`, `gitinv.Homogeneity(digests []string) string`, `gitinv.DistinctFingerprintDigests`, `gitinv.MissingRuntimeMappingDigests`, `gitinv.ResolveRuntimeImageForFingerprint`, `gitinv.BuildEffectiveRuntimeMapping`, `gitinv.EnvironmentChangedProofMessage`.

Transcribe `src/config.ts` 9–15 and 17–63 and 122–125 (read side; `writeFaultLineConfig`, `ensureFaultLineConfig` and `updateFaultLineConfigImage` belong to `fl init`, slice 3), `src/runtime-mapping-file.ts:67-88` (the writer, 30–64, belongs to `fl runtime`, slice 3), `src/environment-fingerprint.ts` (all), and `requireImageOrConfig` (`cli-app.ts:161`).

**Gotchas:**
- `readFaultLineConfig` refuses symlinks and non-regular files before `JSON.parse`; the config schema is zod `strict`.
- `computeEnvironmentFingerprint` keeps the 26 descriptor names in source order for lookup but sorts the found files with `localeCompare` (83); missing, non-regular and symlinked files are skipped (`existsSync` then `lstatSync`).
- `distinctFingerprintDigests` sorts with `localeCompare` (101–103); `git-investigation.ts`'s `distinctDigests` (Task 14) uses bare `.sort()` — different orders on purpose.
- `loadRuntimeMappingDocument` uses `.mapping` when it is an object, otherwise the root, and skips the four metadata keys.

- [x] **Step 1: Write the failing test**

`internal/gitinv/support_test.go`:

```go
package gitinv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintSortsAndSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0o644)
	os.Symlink("package.json", filepath.Join(dir, "go.mod"))
	fp, err := ComputeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := fp.Get("files").Items()
	if len(files) != 2 || files[0].Get("path").Str() != "Cargo.toml" || files[1].Get("path").Str() != "package.json" {
		t.Fatalf("%v", files)
	}
}
```

Live: `TestLiveFingerprint` builds 300 directories with random subsets of the 26 descriptor names (some symlinks, some directories, some FIFOs on POSIX) and compares canonical fingerprints; `TestLiveRuntimeMapping` feeds 2,000 generated mapping documents.

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/gitinv/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/gitinv/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveFingerprint|TestLiveRuntimeMapping' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/gitinv difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): image config, runtime mappings and environment fingerprints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 11: Hardened git, tree materialization and frozen overlays

**Files:**
- Create: `internal/gitinv/hardened.go`, `internal/gitinv/overlay.go`, `internal/gitinv/hardened_test.go`, `difftest/hardened_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (ops `runHardenedGit`, `materializeGitTree`, `materializeFrozenOverlays`), `difftest/testdata/fakegit/` (new fakes: `slow` sleeps 40 s, `flood` prints 65 MiB, `filter-config` reports a filter, `attrs-filter` makes `git grep` exit 0)

**Interfaces:**
- Consumes: `nodeproc.Spawn` (Task 2), `clock.NewUUID`, `safedir`, `canonical`.
- Produces:

```go
type GitRun struct {
	ExitCode       *int
	Stdout, Stderr []byte
	Error          string // "" when none; TS sets error on timeout, cap, or spawn failure
}
type HardenedOptions struct {
	Repository, GitDir, WorkTree, IndexFile string
	Stdin    []byte
	HasStdin bool
}
func RunHardenedGit(args []string, o HardenedOptions) GitRun
func HardenedGitText(args []string, o HardenedOptions) (string, error)
func HardenedGitEnvironment(o HardenedOptions) []string
func AssertSafeGitMaterialization(repo string, commits []string) error
func AssertOverlayTargetsNotGitSymlinks(repo, commit string, paths []string) error
type MaterializedTree struct{ Worktree string; Cleanup func() error }
func MaterializeGitTree(repo, commit, tempRoot, name string) (MaterializedTree, error)
func MaterializeFrozenOverlays(worktree string, overlays jsjson.Value) (jsjson.Value, error)
```

Transcribe `src/git-materialization.ts` (334 lines, all of it) and `src/safe-overlay.ts` (137 lines, all of it).

**Gotchas:**
- Argv is `[...MATERIALIZATION_SAFE_GIT_CONFIG (25 -c pairs, source order), --git-dir=X?, --work-tree=Y?, -C, repo, ...args]` (115–122).
- Environment (72–95): host `PATH` only, plus the fixed variables; on Windows also `SystemRoot`, `ComSpec`, `PATHEXT`; `GIT_CONFIG_GLOBAL` is `NUL` on Windows.
- `stdin` is `pipe` + `end(stdin)` when given, else `ignore`; EPIPE on stdin is swallowed.
- Timeout 30 s → `child.kill()` (SIGTERM), timer `unref`'d. Output cap 64 MiB combined → `child.kill()` (SIGTERM). On `close`, append `\nFaultLine stopped Git: <reason>.` to stderr and set `error`. A spawn failure resolves with `ExitCode == nil` and the message in `Error`.
- `hardenedGitText` trims with JS trim and decodes lossily; failure text is `Git <args joined by " "> failed: <stderr.trim()>` (or `<error>` / `exit N`, read 197–206 for the precedence).
- `materializeGitTree` call order and cleanup are exact (the two `assertSafeGitMaterialization` checks, `rev-parse --absolute-git-dir`, mkdir, then with `-C worktree`, `--git-dir`/`--work-tree` and `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE`: `read-tree --reset <commit>`, `checkout-index --all --force`, `update-index --refresh`, `diff-files --quiet --`); `randomUUID()` at 288 names the index file; `mkdir` of `worktrees/` and `indexes/` is recursive 0700, the worktree itself 0755. On any error the cleanup is awaited, then the error rethrown.
- `assertSafeGitMaterialization`: `git grep` exit 0 throws, exit 1 is fine, anything else throws.
- `safe-overlay` base64 decoding is Node's lenient decoder (ignores characters outside the alphabet, tolerates missing padding). Add `nodebuf.DecodeBase64Lenient` in `internal/jsstr` or a new tiny `internal/nodebuf`, with a live test against `Buffer.from(s, "base64")` over 20,000 random strings.
- Overlay writes: `writeFile(staged, {flag: "wx", mode: 0o644})`, `rename`, `rm(staged, {force: true})` in `finally`, then re-read and compare bytes. One `randomUUID()` per overlay (`.faultline-overlay-<uuid>`), in overlay order.

- [x] **Step 1: Write the failing test**

`internal/gitinv/hardened_test.go`:

```go
//go:build faultline_test && unix

package gitinv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
)

func gitRepo(t *testing.T) (dir, head string) {
	dir = t.TempDir()
	for _, a := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "a"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	os.WriteFile(filepath.Join(dir, "state.txt"), []byte("good\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "b").Run()
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return dir, string(out[:40])
}

func TestMaterializeAndCleanUp(t *testing.T) {
	clock.ResetForTest("materialize")
	repo, head := gitRepo(t)
	root := t.TempDir()
	tree, err := MaterializeGitTree(repo, head, root, "state-0000-"+head[:16])
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tree.Worktree, "state.txt")); string(b) != "good\n" {
		t.Fatalf("worktree content %q", b)
	}
	if err := tree.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tree.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree survived cleanup")
	}
}

func TestHardenedGitTimeoutMessage(t *testing.T) {
	t.Setenv("PATH", fakeGitDir(t, "slow")+string(os.PathListSeparator)+os.Getenv("PATH"))
	restore := SetGitTimeoutForTest(200) // ms
	defer restore()
	r := RunHardenedGit([]string{"status"}, HardenedOptions{Repository: t.TempDir()})
	if r.ExitCode != nil || r.Error == "" || !endsWith(string(r.Stderr), "\nFaultLine stopped Git: ") {
		t.Fatalf("%+v", r)
	}
}
```

(`fakeGitDir` resolves `difftest/testdata/fakegit/<name>`; `endsWith` checks the prefix of the appended line; `SetGitTimeoutForTest` swaps the 30 000 ms constant, following slice 1's `SetSplitLimitForTest` pattern.)

Live: `TestLiveHardenedGit` runs the same argv sets through the oracle's `runHardenedGit` and Go (success, exit 1, missing repo, stdin patch apply, each new fake git) and compares `{exitCode, stdout, stderr, error}`; `TestLiveMaterialize` compares worktree tree digests and thrown messages for good commits, filter configs, `.gitattributes` filters, symlinked overlay targets and existing-path overlays.

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/gitinv/ -run 'Materialize|Hardened'`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/gitinv/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveHardenedGit|TestLiveMaterialize|TestLiveBase64' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/gitinv internal/jsstr difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): hardened git, tree materialization and frozen overlays

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Docker sandbox (plan, runner, truncation, classification)

**Files:**
- Create: `internal/sandbox/plan.go`, `internal/sandbox/run.go`, `internal/sandbox/sandbox_test.go`, `difftest/sandbox_live_test.go`, `difftest/testdata/fakedocker/{timeout,flood,daemon-down,missing-image,crash,setup-127,forged-pass,compile-error}/docker`
- Modify: `internal/bundle/gitproof/sandbox.go` (export `DeterministicEnvironment`, `MaxSandboxLimits`, `DockerPolicyPayload`, `UnsafeLocalPolicyPayload`), `difftest/gen/node-oracle.ts` (ops `createSandboxPlan`, `classifySandboxResult`, `truncateOutput`, `executeSandboxPlan`)

**Interfaces:**
- Consumes: `gitproof.ValidateSandboxPlanAudit`, `witnessresult.*`, `jsre`, `nodeproc.Spawn`, `clock.NewUUID`, `canonical`.
- Produces:

```go
type Plan struct{ Value jsjson.Value } // the TS plan object, schema key order
func CreatePlan(request jsjson.Value) (Plan, error)              // createSandboxPlan
func AuditPlan(p Plan) jsjson.Value                                // auditSandboxPlan
type RunResult struct {
	ExitCode         *int
	Stdout, Stderr   string // already truncated and lossily decoded
	TimedOut, OutputLimitExceeded bool
}
type Runner func(p Plan) (RunResult, error)
func NativeRunner() Runner                                         // createNodeSandboxRunner
func Execute(p Plan, r Runner, injected bool) jsjson.Value          // executeSandboxPlan → result object
func Classify(p Plan, r RunResult) jsjson.Value                     // classifySandboxResult
func TruncateOutput(stdout, stderr []byte, max int) (string, string, bool)
```

Transcribe `src/sandbox.ts` 29–613 except `validateSandboxPlanAudit` (already `gitproof.ValidateSandboxPlanAudit`), and 715–980.

**Gotchas:**
- Container name: `faultline-` + `randomUUID()` with hyphens removed, one per plan (464), read **after** `realpathSync` of the source directory (262).
- Docker argv is exactly the template at 466–506; numbers are `String(n)` of integers; `--env K=V` entries (fixed deterministic plus allowlisted) are sorted with `localeCompare`:

  ```
  docker run --rm --name faultline-<uuid32> --init --pull=never --network none --ipc none --read-only
    --user 65534:65534 --cap-drop ALL --security-opt no-new-privileges:true
    --ulimit fsize=<maxOut>:<maxOut> --pids-limit <pids> --memory <mem> --memory-swap <mem> --cpus <cpu>
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=<tmpfs>
    --mount type=bind,src=<realpath>,dst=/workspace/src,readonly --workdir /workspace/src
    [--env K=V ...] --entrypoint /bin/sh <image> -lc <witness.command>
  ```
- `buildEnvironment` (318–359): secret names (`SECRET_ENVIRONMENT_NAME`, `/i`) are redacted; overriding a deterministic key throws unless identical; names not in the allowlist are redacted; `passed[]` carries `valueDigest`.
- `resolveSourceDirectory` refuses the filesystem root and paths containing CR, LF or comma.
- `validateWitness` uses JS trim and rejects NUL — this is what keeps NUL out of `nodeproc.Spawn`.
- `truncateOutput` takes byte prefixes from stdout first, then stderr, from one budget, then decodes with `toString("utf8")` (U+FFFD for a cut code point).
- Native runner: the main process gets `cwd = sourceDirectory`, `env = process.env` whole, `stdio [ignore, pipe, pipe]`; counts every observed byte, captures up to `maxOutputBytes` combined, sends SIGKILL once more than `maxOutputBytes` has been observed; timeout sets `timedOut` and sends SIGKILL (timer **not** unref'd). Cleanup `docker rm --force <name>` (stdio `[ignore, ignore, pipe]`, own 5 s SIGKILL timer) runs only after a timeout, an overflow or a null exit; its error or nonzero-exit detail is appended to stderr as `\n<detail>`.
- `classifySandboxResult` order (735–819): UNSAFE_LOCAL plan → INAPPLICABLE; timeout → TIMEOUT; overflow → OUTPUT_LIMIT_EXCEEDED; null exit → RUNNER_FAILURE; exit 125 or the Docker stderr regex → SANDBOX_UNAVAILABLE; exit 126/127 → WITNESS_SETUP_ERROR; unparseable witness result → HARNESS_ERROR; nonzero exit, no result and the 789 regex → WITNESS_SETUP_ERROR; a structured result → consistency check, then `witnessresult.Classify`; compile-incompatibility regex → INCOMPATIBLE_STATE; else EXIT_ZERO_UNSTRUCTURED / EXIT_NONZERO_UNSTRUCTURED. `DOCKER_INFRASTRUCTURE_ERROR` and `COMPILE_OR_SETUP_INCOMPATIBILITY` go through `jsre` (`\s`, `.` and `/i` semantics).
- `createUnsafeLocalSandboxPlan` and `FAULTLINE_DEV_UNSAFE_LOCAL` are reachable from `investigate git` only through a request the CLI never builds (the CLI always passes `DOCKER_ISOLATED`). Port `createSandboxPlan`'s dispatch but not the unsafe-local plan; the unsafe branch returns the TS error for an unknown mode. Note this in KNOWN_DIFFERENCES only if a CLI path reaches it (none does).

- [x] **Step 1: Write the failing test**

`internal/sandbox/sandbox_test.go`:

```go
//go:build faultline_test

package sandbox

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestDockerArgvAndClassification(t *testing.T) {
	clock.ResetForTest("sandbox")
	src := t.TempDir()
	req, _ := jsjson.Parse(`{"mode":"DOCKER_ISOLATED","sourceDirectory":` + jsjson.Quote(src) + `,
		"image":"node@sha256:` + strings.Repeat("a", 64) + `",
		"witness":{"digest":"sha256:` + strings.Repeat("b", 64) + `","command":"node witness.mjs"}}`)
	p, err := CreatePlan(req)
	if err != nil {
		t.Fatal(err)
	}
	args := p.Value.Get("args").Items()
	if args[0].Str() != "run" || args[1].Str() != "--rm" || args[2].Str() != "--name" || !strings.HasPrefix(args[3].Str(), "faultline-") || len(args[3].Str()) != 42 {
		t.Fatalf("%v", args[:4])
	}
	code := 1
	got := Classify(p, RunResult{ExitCode: &code, Stdout: "state=bad\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"})
	if got.Get("verdict").Str() != "FAIL" || got.Get("reason").Str() == "" {
		t.Fatalf("%s", jsjson.Stringify(got))
	}
}

func TestTruncateOutputReplacesCutCodePoint(t *testing.T) {
	out, errOut, cut := TruncateOutput([]byte("ab\xe2\x82\xac"), []byte("zz"), 3)
	if out != "ab�" || errOut != "" || !cut {
		t.Fatalf("%q %q %v", out, errOut, cut)
	}
}
```

The request is `SandboxPlanRequest` (`sandbox.ts:107-121`, witness is `{digest, command}`); results carry `verdict` and `reason` (735–819).

Live: `TestLiveSandboxPlan` (oracle `createSandboxPlan` with `randomUUID` seeded through the oracle's own preload: start the oracle with `node --import seed.mjs` and the same seed, then compare canonical plans over 2,000 generated requests including invalid images, secret-looking env names, overrides, limits at and over the maxima, CR/LF/comma paths), `TestLiveClassify` (5,000 generated `{exitCode, stdout, stderr, timedOut, overflow}` tuples), `TestLiveTruncate` (random byte strings and budgets), and `TestLiveNativeRunner` (each fake Docker through oracle `executeSandboxPlan` with the native runner vs `Execute(p, NativeRunner(), false)`, comparing results and the recorded `docker` argv including the `rm --force` cleanup).

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/sandbox/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription; add the fake Dockers listed under Files (each one is a few lines of sh following the `pass-fail` protocol from Task 6).

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/sandbox/ ./internal/bundle/gitproof/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveSandboxPlan|TestLiveClassify|TestLiveTruncate|TestLiveNativeRunner' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/sandbox internal/bundle/gitproof difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): Docker sandbox plan, runner and result classification

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: `investigateGitRange`

**Files:**
- Create: `internal/gitinv/investigate.go`, `internal/gitinv/investigate_test.go`, `difftest/investigate_live_test.go`
- Modify: `internal/bundle/gitproof/schemas.go` (add `GitInvestigationRequestSchema`, `GitSandboxSchema` with their `.default`s; export `ExpectedExecutionID`, `ExpectedRunID`), `difftest/gen/node-oracle.ts` (op `investigateGitRange`, run under the seed preload with the native runner)

**Interfaces:**
- Consumes: Tasks 3, 4, 10, 11, 12; `gitproof.VerifyFrozenWitnessRecord`, `gitproof.GitInvestigationResultSchema`.
- Produces: `gitinv.InvestigateGitRange(request jsjson.Value, runner sandbox.Runner, injected bool) (jsjson.Value, error)` — the schema-parsed result; `gitinv.GitInvestigationSchemaVersion`.

Transcribe `src/git-investigation.ts` 62–863 (schemas the slice 1 verifier lacks, then every function: `sha256Digest` 300, `redactedEvidencePreview` 304–313, `runGit`/`gitText` 319–336, the resolvers 338–405, `planRequestForState` 407–421, `stableStates` 423–458, `findTransitions` 460–473, `hasNonMonotonicTransitions` 475, `witnessSummary` 480–489, `executionTrustFor` 491, `emptyProof` 496–512, `baseResult` 514–541, `runFact` 543–602, `classifyStatus` 604–615, `investigateGitRange` 617–863).

**Gotchas:**
- Nondeterministic reads in order (the replay fails on any reordering): `mkdtemp(tmpdir()/faultline-git-investigation-)` once; phase 1 per state: index UUID (name `fp-${i}-${commit[0:16]}`); phase 2 per state: index UUID (`state-${pad4(i)}-${commit16}`), one UUID per overlay, container UUID, then for attempts 1–3: `Date.now()`, execute, `Date.now()`, nonce UUID. `startedAt`/`finishedAt` are `new Date(epoch).toISOString()` of those reads (not extra reads); `durationMs = max(0, finished - started)`.
- Git call order: `rev-parse --show-toplevel`; `rev-parse --verify --end-of-options <rev>^{commit}` for ancestor then descendant; `merge-base --is-ancestor A D` (exit 1 → `Range ancestor … is not an ancestor of descendant …`); `rev-list --reverse --ancestry-path --max-count=N --end-of-options A..D`; only when the count equals `maxStates`, `rev-list --count --ancestry-path --end-of-options A..D`; `rev-parse --verify --end-of-options <c>^{tree}` per commit; `assertSafeGitMaterialization(all commits)`; per state and phase the materialization calls; `ls-files --stage -- <path>` per overlay. The recorder argv in the goldens checks it.
- `distinctDigests` is `[...new Set(x)].sort()` (code-unit order); `split(/\r?\n/).filter(Boolean)`; `Number.parseInt(x, 10)` plus a safe-integer check; `String(i).padStart(4, "0")`.
- `redactedEvidencePreview`: first 16 384 bytes of the UTF-8, lossy decode, then `redactText(preview, "$.run.stdout")` / `"$.run.stderr"`; `truncated` is `bytes > 16384`.
- The result is `Schema.parse(result)`: key order comes from the schema and unknown keys are dropped — build the Go value through `schema.Parse` on the final object, as TS does.
- `proofReason` (812–826) is a nested ternary; copy each string.
- `rm(tempRoot, {recursive, force})` in `finally` even when a step throws (`nodefs.RemoveAll`).

- [x] **Step 1: Write the failing test**

`internal/gitinv/investigate_test.go`:

```go
//go:build faultline_test && unix

package gitinv

import (
	"os"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func TestInvestigateTwoStatesFindsTransition(t *testing.T) {
	clock.ResetForTest("investigate")
	t.Setenv("PATH", fakeDockerDir(t, "pass-fail")+":"+os.Getenv("PATH"))
	repo, ancestor, descendant := goodThenBadRepo(t) // state.txt: good at ancestor, bad at descendant
	frozen := frozenWitnessFor(t)                    // witness command reads state.txt
	req := jsjson.MustObject(
		"repository", jsjson.MakeString(repo),
		"range", jsjson.MustObject("ancestor", jsjson.MakeString(ancestor), "descendant", jsjson.MakeString(descendant)),
		"frozenWitness", frozen,
		"expectedFrozenDigest", frozen.Get("frozenDigest"),
		"sandbox", jsjson.MustObject("mode", jsjson.MakeString("DOCKER_ISOLATED"), "image", jsjson.MakeString(pinnedImage)),
	)
	res, err := InvestigateGitRange(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Get("transitions").Items()
	if res.Get("status").Str() != "COMPLETED" || len(tr) != 1 || tr[0].Get("kind").Str() != "PASS_TO_FAIL" || !res.Get("proof").Get("isProof").Bool() {
		t.Fatalf("%s", jsjson.Stringify(res))
	}
}
```

`fakeDockerDir`, `goodThenBadRepo`, `frozenWitnessFor` and `pinnedImage` (`node@sha256:` + 64 `a`) live in `internal/gitinv/testing.go` under `//go:build faultline_test`; `frozenWitnessFor` runs `witness.Propose/Approve/Freeze` in a temp store with a `witness.mjs` overlay that exits 1 when `state.txt` says `bad`. Fields follow `GitInvestigationRequestSchema` (`git-investigation.ts:97-106`) and the result schema (240–285). `jsjson.MustObject(kv ...any) Value` is new in this task: alternating key strings and values, built with `NewObj` in argument order.

Live: `TestLiveInvestigate` builds 40 repositories (2 to 20 states, PASS→FAIL at a random point, non-monotonic histories, a heterogeneous fingerprint with and without a runtime mapping, sha256 object format, CJK and spaced paths, overlays that collide with tracked files) and runs the oracle and Go with the same seed and fake Docker; it compares canonical results.

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/gitinv/ -run TestInvestigate`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/gitinv/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveInvestigate -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/gitinv internal/bundle/gitproof internal/jsjson difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): port investigateGitRange

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Git proof bundle writer

**Files:**
- Create: `internal/bundle/gitproof/write.go`, `internal/bundle/gitproof/write_test.go`, `difftest/write_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (op `writeGitInvestigationProofBundle`, under the seed)

**Interfaces:**
- Consumes: `gitproof.Verify` (slice 1), slice 1's helpers in `gitproof` (`digestBytes`, `safeArtifactPath`, `assertNoLinksOrSpecialFiles`, `assertRegularFile`, `runArtifactPath`, `transitionArtifactPath`, `requiredArtifactPaths`, `resolveGitState`, `parseBundleHeads`, `binaryRangePatch`, `bindLifecycleLedger`, `validateGitInvestigationProofSemantics`), `safedir`, `clock`.
- Produces:

```go
type WriteOptions struct {
	ProofRoot       string  // "" = resolve(".faultline", "git-proof-bundles")
	GeneratedAt     *string // nil = clock
	LifecycleLedger *jsjson.Value
	Codex           *CodexThreadIDs // Task 15
}
type CodexThreadIDs struct{ WitnessDraftThreadID, RepairThreadID *string }
type Written struct {
	Directory, ManifestDigest, RootDigest string
	Manifest jsjson.Value
}
func WriteGitInvestigationProofBundle(output string, result, frozen jsjson.Value, o WriteOptions) (Written, error)
func DefaultGitProofRoot() string
```

Transcribe `src/git-proof-bundle.ts`: `defaultGitProofRoot` 210, `jsonBytes` 218, `sameCanonical` 222, `ensureRealDirectoryTree` 257–277, `isNested` 279, `prepareFreshOutput` 288–300, `writeArtifact` 339–344, `sourceRepositoryFor` 420–449, `writePortableGitBundle` 461–484, `makeManifest` 797–834, `verificationReadme` 836–850, `writeGitInvestigationProofBundle` 1029–1135; and `assertSafeProofOutput` from `src/proof-bundle.ts` (read it; port only the checks the writer reaches).

**Gotchas:**
- Reads in order: `new Date()` (1052, only without `generatedAt`), stage UUID (1056, `dirname(out)/.<name>.staging-<uuid>`), `mkdtempSync(tmpdir()/faultline-git-proof-source-)` (462), then the verifier's own `mkdtempSync(...verify-)` (slice 1, already through `nodefs.Mkdtemp` and therefore seeded).
- Git call order: `rev-parse --show-toplevel`; `resolveGitState` for ancestor then descendant; `merge-base --is-ancestor`; `rev-list --reverse --ancestry-path --end-of-options A..D` (no max-count); `resolveGitState` per commit; `rev-parse --show-object-format`; `clone --bare --shared --no-tags --quiet <src> <bare>`; `update-ref refs/faultline/portable-descendant <D>` (bare); `bundle create <stage>/source/descendant.bundle <ref>` (bare); `bundle list-heads <path>`; `bundle verify <path>` (bare); `diff --binary --full-index --no-ext-diff --no-textconv --no-renames --end-of-options A D`; then `Verify`'s calls.
- Artifacts are written in map insertion order (manifest, investigation, witness, source metadata, descendant bundle, range patch, VERIFY.md, ledger if bound, runs in `result.runs` order, transitions `%04d`); the `localeCompare`-sorted key list must equal `requiredArtifactPaths`.
- `writeArtifact`: `mkdir -p` 0700, `writeFileSync` with `wx` and 0600, then `assertRegularFile`. The descendant bundle is written by git; skip it when it already exists with equal bytes, else fail with `...changed before package assembly`.
- `hashes.txt` lines are `<hex>  <path>` sorted with `localeCompare`, joined with `\n`, plus a trailing `\n`; `ROOT.sha256` is `sha256:<hex of hashes.txt>\n`.
- `VERIFY.md` has no trailing newline (`join("\n")`).
- `codex` appears in the manifest only when it has keys; `lifecycleLedger` path only when bound.
- Any error removes the stage (`rmSync -rf`) and rethrows; the temporary bare repository is always removed.
- Error texts: `Refusing to compile Git proof bundle: UNSAFE_LOCAL run facts are structurally unexportable (<ids joined by ", ">).`, `Cannot write semantically inconsistent Git proof bundle: <errors joined by "; ">`, `... already exists and will not be replaced: <output>`.

- [x] **Step 1: Write the failing test**

`internal/bundle/gitproof/write_test.go` (external test package: `gitinv` imports `gitproof`, so an internal test importing `gitinv` would be a cycle):

```go
//go:build faultline_test && unix

package gitproof_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func TestWrittenBundleVerifies(t *testing.T) {
	clock.ResetForTest("writer")
	req, frozen := gitinv.TwoStateRequestForTest(t, "pass-fail") // Task 13's fixture, exported under faultline_test
	result, err := gitinv.InvestigateGitRange(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "out"), result, frozen, gitproof.WriteOptions{ProofRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if v := gitproof.Verify(w.Directory, w.RootDigest, true); !v.Valid {
		t.Fatalf("%v", v.Errors)
	}
	info, _ := os.Stat(filepath.Join(w.Directory, "manifest.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode %v", info.Mode())
	}
	if _, err := gitproof.WriteGitInvestigationProofBundle(filepath.Join(root, "out"), result, frozen, gitproof.WriteOptions{ProofRoot: root}); err == nil {
		t.Fatal("second write into an existing output must fail")
	}
}
```

(`TwoStateRequestForTest` moves Task 13's `goodThenBadRepo`/`frozenWitnessFor` helpers into `internal/gitinv/testing.go` under `//go:build faultline_test`.)

Live: `TestLiveProofWriter` runs oracle `writeGitInvestigationProofBundle` and Go on the same seeded investigation (from Task 13's generator, 30 repositories, with and without a lifecycle ledger), into sibling directories, and compares `treeDigest` of the two bundles, the returned `{manifestDigest, rootDigest}`, and the recorded git argv. On a mismatch it prints the first differing file.

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/bundle/gitproof/ -run TestWriter`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/bundle/gitproof/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveProofWriter -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/bundle/gitproof difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): Git proof bundle writer

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: `fl investigate git` and Codex thread IDs on Git bundles

**Files:**
- Create: `internal/cli/investigate.go`, `internal/cli/investigate_test.go`, `difftest/threadid_test.go`
- Modify: `internal/cli/cli.go`, `difftest/gen/scenarios.ts` (scenarios below), `difftest/testdata/writers.json`, `difftest/gen/gen-writers.ts` (cross-verify step)

**Interfaces:**
- Consumes: everything above.
- Produces: `investigateCommand(e *env, args []string) error`; flags `--witness-draft-thread-id` and `--repair-thread-id` (Go only; spec 3.6).

Transcribe `src/cli-app.ts`: `requireImageOrConfig` 161, `sessionFlagOverrides` 171, `requireExpectDigestOrBinding` 183–207, `investigateCommand` 1953 (guided-mode guard and the git branch 2161–2237). `investigate turns` and guided mode keep `not yet ported` (slices 4 and 5); the guard's own error message is ported because it is reached before dispatch.

**Gotchas:**
- Order of operations: guided-mode guard; `args[0] !== "git"` usage throw (2162); store; `--proposal`; `--repo` resolved; draft store; `--max-states`; read the frozen witness; inherited facts; expect-digest (may write a binding); ledger; image; runtime mapping; `writeIncidentSessionBinding`; `investigateGitRange` with `{mode: DOCKER_ISOLATED, image}`. `--max-states` goes through `jsjson.ToNumber` (`""` → 0, `"1e2"` → 100).
- Clock reads at CLI level: `requireExpectDigestOrBinding` may write a binding (`new Date()` #1), the unconditional `writeIncidentSessionBinding` before `investigateGitRange` (#2), then everything inside `investigateGitRange`, then `Date.now()` (2210) for the default output `<proofRoot>/investigation-${desc.slice(0,12)}-${Date.now()}`, then the writer, then the verifier's `mkdtemp`, then optionally `ledgerbind.WriteAtomic` (pid + UUID).
- Not-proof stdout is `{investigation, proofBundle: null, note}` with exit 1; proof stdout is `{evidenceGrade, evidenceLabel, investigation, proofBundle: {directory, rootDigest, lifecycle, externalRootStatus}, ...ledgerBinding?}` with exit 0; both `JSON.stringify(x, null, 2) + "\n"`.
- A written bundle that fails verification throws `Generated Git proof bundle failed verification: <errors joined by "; ">`.
- **Thread IDs (spec 3.6):** each value must match `^[A-Za-z0-9][A-Za-z0-9._:-]*$` with length 1–160 (UTF-16), the same rule as the manifest schema (`src/git-proof-bundle.ts:152-155`); otherwise throw `--witness-draft-thread-id must be a Codex thread identifier (1-160 characters, [A-Za-z0-9._:-], starting with a letter or digit).` (and the same for `--repair-thread-id`). Validate both flags before reading the witness store, so a bad flag writes nothing. With neither flag the manifest has no `codex` key and output equals TS.

**Scenarios and writer cases:**
- Scenarios (each a repo, a frozen witness store and an optional ledger, built by `scenarios.ts`): `git-two-states`, `git-many-states` (12), `git-max-states-overflow` (`--max-states 4` on 8 states), `git-heterogeneous` (with and without `--runtime-mapping`), `git-ledger-partial`, `git-ledger-full`, `git-unbound`, `git-sha256`, `git-cjk-overlays`, `git-non-monotonic`, `git-all-pass`, `git-all-fail`.
- Cases per scenario: default output, `--output` inside the proof root, `--output` outside it, `--output` existing, missing `--proposal`/`--repo`/`--from`/`--to`, `--expect-digest` mismatch, binding-inherited digest (no flag), `--image` absent with and without `.faultline/config.json`, each fake Docker (`pass-fail`, `timeout`, `flood`, `daemon-down`, `missing-image`, `crash`, `setup-127`, `forged-pass`, `compile-error`), `--ledger` with `--ledger-binding-output`, guided-mode guard, unknown subcommand.
- Cross-check (spec 2.4): `gen-writers.ts` runs TS `verify` on every bundle TS wrote; `TestWriterReplay` runs Go `fl verify` on every bundle Go wrote; both must print `VALID` where the golden says a bundle was written.

- [x] **Step 1: Write the failing tests**

`difftest/threadid_test.go`:

```go
//go:build unix

package difftest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThreadIDsBindUnderRoot(t *testing.T) {
	ws := scenarioWorkspace(t, "git-two-states")
	stdout, stderr, code := runFlIn(t, ws, nil, "investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--witness-draft-thread-id", "thr_01.draft", "--repair-thread-id", "thr:02")
	if code != 0 {
		t.Fatalf("%d %s %s", code, stdout, stderr)
	}
	dir := proofDirectory(t, stdout)
	manifest, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if !strings.Contains(string(manifest), `"codex":{"repairThreadId":"thr:02","witnessDraftThreadId":"thr_01.draft"}`) {
		t.Fatalf("manifest %s", manifest)
	}
	for _, verifier := range []func(string) (string, int){goVerify(t), tsVerify(t)} {
		if out, code := verifier(dir); code != 0 || !strings.Contains(out, "VALID") {
			t.Fatalf("fresh bundle: %d %s", code, out)
		}
	}
	tamperManifestField(t, dir, "codex.repairThreadId", "thr:03")
	for _, verifier := range []func(string) (string, int){goVerify(t), tsVerify(t)} {
		if _, code := verifier(dir); code == 0 {
			t.Fatal("edited thread id still verifies")
		}
	}
	_, stderr, code = runFlIn(t, ws, nil, "investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD",
		"--proposal", "demo-1", "--image", pinnedImage, "--repair-thread-id", "-bad")
	if code != 1 || !strings.Contains(stderr, "--repair-thread-id must be") {
		t.Fatalf("%d %s", code, stderr)
	}
}
```

(`tsVerify` skips unless `FAULTLINE_NODE_ORACLE=1`; `scenarioWorkspace`, `runFlIn`, `proofDirectory`, `tamperManifestField`, `goVerify` are helpers in `writer_test.go`.)

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run 'TestThreadIDs|TestWriterReplay'`
Expected: FAIL (`investigate` is not yet ported).

- [x] **Step 3: Implement**; add the scenarios and cases above; regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestThreadIDs|TestWriterReplay' -v`
Expected: PASS, including the cross-verification of every written bundle.

- [x] **Step 5: Commit**

```bash
git add internal/cli difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl investigate git, with optional Codex thread IDs bound under the root

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 16: `minimize git` and `minimize verify`

**Files:**
- Create: `internal/minimize/minimize.go`, `internal/minimize/verify.go`, `internal/minimize/minimize_test.go`, `internal/cli/minimize.go`
- Modify: `internal/cli/cli.go`, `internal/cli/args.go` (`safeManagedFileOutput`), `difftest/gen/node-oracle.ts` (op `verifyGitMinimizationResult`), `difftest/function_live_test.go`, `difftest/testdata/writers.json`, `difftest/gen/scenarios.ts`

**Interfaces:**
- Consumes: Tasks 4, 11, 12; `gitproof.FrozenWitnessSchema`, `gitproof.VerifyFrozenWitnessRecord`, `safedir`, `clock`.
- Produces: `minimize.MinimizeGitDiff(request jsjson.Value, runner sandbox.Runner, injected bool) (jsjson.Value, error)`, `minimize.SplitModifyUnitIntoHunks(unit PatchUnit) []PatchUnit`, `minimize.Verify(value jsjson.Value, expected string, provided bool) Verification`, `minimize.VerifyFile(path, expected string, provided bool) Verification`, `minimize.WriteResult(path string, result jsjson.Value) (string, string, error)` (path, resultDigest), `type Verification struct{ Valid bool; ResultDigest *string; ExternalDigestStatus string; Result *jsjson.Value; Errors []string }`; in `cli`: `safeManagedFileOutput(output, managedRoot, label string) (string, error)`.

Transcribe `src/git-minimization.ts` (2065 lines, all of it: schemas 64–359, helpers 394–891, `minimizeGitDiff` 899–1502, verification 1504–1994, writer 1997–2065), `safeManagedFileOutput` (`cli-app.ts:265-285`) and `minimizeCommand` (`cli-app.ts:2358-2408`).

**Gotchas:**
- Reads per `executeCandidate`, in order: `Date.now()` (992) → `startedAt` ISO (993, derived, no extra read), `mkdtemp(tmpdir()/faultline-git-minimization-)` (994), `randomUUID()` for `worktree-<uuid>` (995) → `worktreeDigest = sha256(worktree path)`, materialization's index UUID, overlay UUIDs, the sandbox container UUID, then `Date.now()` (1069) and the nonce UUID (1071). `writeGitMinimizationResult` reads one UUID (2045) for its temp name.
- The CLI runs `minimizeGitDiff` **before** `safeManagedFileOutput`, so the default file name's `Date.now()` comes after every read above; an existing output is only detected after the full Docker run, as in TS.
- `--max-executions` goes through `Number()` and is passed as `budget.maxExecutions` only when the flag is present.
- `git apply` gets the patch on stdin (`nodeproc.Spawn` with `StdinPipe`).
- `writeGitMinimizationResult`: `ensureRealOutputDirectory` (mkdir 0700), temp `.<uuid>.faultline-git-minimization.tmp` opened `wx` 0600, `link()` to the final path (refuses `EEXIST` with `Git minimization result already exists and will not be replaced: <abs>`), then `rm` of the temp. Go: `os.Link`; Windows `CreateHardLink` through `os.Link` too.
- Contents are `canonicalJson(verifiedResult) + "\n"`.
- `minimize verify` output spreads `status, proof, usedExecutions` only when `verification.result` is defined; exit 1 when invalid.
- `MAX_ENUMERATION_PATCH_UNITS 10`, `MAX_ENUMERATED_MINIMAL_SETS 8`, `CERTIFICATION_EXECUTIONS 3`; keep the subset enumeration order exactly (`boundedProperSubsets` 422–429).

- [x] **Step 1: Write the failing test**

`internal/minimize/minimize_test.go`:

```go
//go:build faultline_test && unix

package minimize

import (
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/sandbox"
)

func TestMinimizeFindsTheBreakingFile(t *testing.T) {
	clock.ResetForTest("minimize")
	// before: state.txt=good, other.txt=a ; after: state.txt=bad, other.txt=b
	req := gitinv.TwoFileMinimizationRequestForTest(t, "pass-fail")
	res, err := MinimizeGitDiff(req, sandbox.NativeRunner(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Get("proof").Get("isProof").Bool() {
		t.Fatalf("not certified: %v", res.Get("proof"))
	}
	ids := res.Get("candidateUnitIds").Items()
	if len(ids) != 1 || unitPath(res, ids[0].Str()) != "state.txt" {
		t.Fatalf("minimal set %v", ids)
	}
	path, digest, err := WriteResult(t.TempDir()+"/r.json", res)
	if err != nil {
		t.Fatal(err)
	}
	if v := VerifyFile(path, digest, true); !v.Valid || v.ExternalDigestStatus != "MATCH" {
		t.Fatalf("%+v", v)
	}
}
```

The minimal set is `candidateUnitIds` (digests of `patchUnits[].id`, `git-minimization.ts:340-341`); `unitPath` is a test helper that looks the id up in `patchUnits` and returns its `path`. `TwoFileMinimizationRequestForTest` lives in `internal/gitinv/testing.go` with Task 13's helpers.

Live: `TestLiveMinimizationVerify` (oracle `verifyGitMinimizationResult` against 3,000 corrupted results from 10 seeded runs) and `TestLiveMinimize` (15 repositories with 1–12 changed files, including binary files, renames shown as add+delete, mode changes, hunk splitting in one modified file, and a non-monotonic interaction; compare canonical results with the seed and the `pass-fail` fake Docker).

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./internal/minimize/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement**; add writer cases: `minimize-git` (default output), `minimize-git-output`, `minimize-git-output-exists`, `minimize-git-output-outside-root`, `minimize-git-output-through-symlink`, `minimize-git-max-executions-low`, `minimize-git-no-patches`, `minimize-git-bad-range`, `minimize-git-invalid-witness`, `minimize-git-daemon-down`, `minimize-verify`, `minimize-verify-expect-digest`, `minimize-verify-tampered`, `minimize-verify-missing-file`, the usage errors. Regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test -tags faultline_test ./internal/minimize/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveMinimiz|TestWriterReplay'`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/minimize internal/cli internal/gitinv difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): fl minimize git and minimize verify

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Proof page, `judge-proof`, `commit-proof-preview` and the local server

**Files:**
- Create: `internal/proofview/page.go`, `internal/proofview/judge.go`, `internal/proofview/server.go`, `internal/proofview/proofview_test.go`, `internal/cli/judge.go`, `internal/cli/browser.go`, `difftest/judge_live_test.go`
- Modify: `internal/cli/cli.go`, `difftest/testdata/writers.json`, `difftest/testdata/fakebrowser/{open,xdg-open}`

**Interfaces:**
- Consumes: `gitproof.Verify`, `canonical`, `evidence`, `witness`.
- Produces: `proofview.LoadVerifiedGitProofView(dir string, expectedRoot *string) (View, error)`, `proofview.RenderGitProofIncidentPage(v View) string`, `proofview.WriteCommitProofPreview(dir string, expectedRoot *string, output string) (Preview, error)` with `type Preview struct{ Path string; Bytes int; RootDigest string }`, `proofview.StartGitProofServer(v View, port int) (*Server, error)` with `(*Server).URL() string` and `(*Server).Close() error`; in `cli`: `judgeProofCommand`, `commitProofPreviewCommand`, `shouldAutoOpenBrowser(getenv func(string) string) bool`, `openLocalDemoURL(e *env, url string)`, `awaitServeInterrupt(close func() error, serveMs *float64, onStop func())`.

Transcribe:
- `src/incident-page.ts` (78 lines, all; `incidentPageStyle` byte for byte).
- `src/git-proof-view.ts` (370 lines, all; the minimization, repair and prevention panels render their `null` branch only, because `judge-proof` and `commit-proof-preview` pass no attachment options — `loadVerifiedIncidentAttachments` with empty options returns all nulls without reading anything; port that as a constant and say so in a comment).
- `src/judge-proof.ts` (73 lines), `src/proof-roots.ts` (26 lines).
- `src/server.ts` `startGitProofServer` (65–99) only; the demo server (12–56) belongs to `fl serve` (slice 3).
- `src/cli-app.ts`: `describeBundlePath` (read it), `shouldAutoOpenBrowser` 879–884, `openLocalDemoUrl` 886–901, `awaitServeInterrupt` 904–937, `judgeProofCommand` 997–1039, `commitProofPreviewCommand` 1041–1059.

**Gotchas:**
- `escapeHtml` replaces `&`, `<`, `>`, `"`, `'` in that order with `replaceAll`; the footer is not escaped. All HTML comes from template literals: keep every space and newline.
- `loadVerifiedGitProofView` (89–138) verifies, reads, verifies, reads, verifies, then compares canonical manifests and artifacts; it throws `Refusing to render an invalid Git proof bundle: ...` or `...that changed during verification: ...`.
- `shortDigest`: ≤22 characters unchanged, else `slice(0,15) + "..." + slice(-8)` (UTF-16 slices).
- `writeCommitProofPreview`: two `String.replace` calls (first occurrence only), `mkdirSync(dirname, {recursive: true})`, `writeFileSync(out, page, {mode: 0o644})` (overwrite allowed); `bytes` is `Buffer.byteLength(page)`.
- Server: `listen(port, "127.0.0.1")`; `GET /` → 200 with `Content-Type: text/html; charset=utf-8`, `Cache-Control: no-store`, `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`; any non-`GET /` request is a 404 `Not found` with `text/plain; charset=utf-8`. `EADDRINUSE` rejects with Node's `listen EADDRINUSE: address already in use 127.0.0.1:<port>`. A server that reports no TCP address throws `FaultLine Git proof server did not report a TCP address` (unreachable with TCP; port the check anyway only if the coverage gate allows — otherwise leave it out with a comment).
- Node's `http` adds `Date`, `Connection: keep-alive`, `Keep-Alive: timeout=5` and `Content-Length`/`Transfer-Encoding` headers. Go's `net/http` differs. The live test compares status, the headers TS sets explicitly, and bodies; it records the remaining header differences in KNOWN_DIFFERENCES (spec 2.5).
- `--port` and `--serve-ms` go through `Number()`; `--serve-ms` must be finite and ≥ 0, else `--serve-ms must be a non-negative number of milliseconds`. Default port 4174.
- `awaitServeInterrupt`: resolves on SIGINT, SIGTERM or the `serveMs` timer, runs `close` then `onStop`, then `process.exit(0)`. Go: `signal.Notify` for SIGINT/SIGTERM only while serving, then `sigexit`-free normal exit 0. A second signal during close is ignored, as Node's `once` handlers leave the default restored — read 904–937 and match what it does.
- Browser: skipped (with the stdout line) when `FAULTLINE_NO_BROWSER=1`, `CI` is `true` or `1`, or `GITHUB_ACTIONS=true`. Otherwise `exec()` runs a **shell string** (`/bin/sh -c 'open "<url>"'`, `xdg-open "<url>"`, or `cmd /c start "" "<url>"`); on error it writes `FaultLine could not open a browser automatically. Open <url> manually.` to stderr. Use `nodeproc.Spawn("/bin/sh", ["-c", cmd])` (Windows: `cmd.exe /d /s /c "<cmd>"`, as Node's `exec` does).

- [x] **Step 1: Write the failing test**

`internal/proofview/proofview_test.go`:

```go
package proofview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewOfCommittedSample(t *testing.T) {
	repo := repoRootForTest(t)
	sample := filepath.Join(repo, "docs", "samples", "self-incident-commit-proof")
	out := filepath.Join(t.TempDir(), "preview.html")
	p, err := WriteCommitProofPreview(sample, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(out)
	if len(page) != p.Bytes || !strings.Contains(string(page), `<meta name="faultline-preview" content="static-commit-proof-snapshot" />`) {
		t.Fatalf("bytes %d vs %d", len(page), p.Bytes)
	}
	if p.RootDigest != JudgeCommitProofSampleRoot {
		t.Fatalf("root %s", p.RootDigest)
	}
}

func TestEscapeHTML(t *testing.T) {
	if got := EscapeHTML(`<a href="x">'&'</a>`); got != "&lt;a href=&quot;x&quot;&gt;&#039;&amp;&#039;&lt;/a&gt;" {
		t.Fatalf("%s", got)
	}
}
```

Writer cases: `commit-proof-preview` (default output in the workspace — the scenario copies `docs/samples/self-incident-commit-proof` into `ws/docs/samples/`), `commit-proof-preview-output`, `-expect-root` match and mismatch, `-bundle-missing`, `-bundle-tampered`, `judge-proof-export-only`, `judge-proof-export-only-expect-root`, `judge-proof-missing-sample`, `judge-proof-bad-serve-ms` (`-1`, `abc`, `Infinity`), `judge-proof-serve` (`--port 0 --serve-ms 200`; stdout normalized to `127.0.0.1:<PORT>`), `judge-proof-port-in-use` (the case harness holds a listener on a port it passes with `--port`).

Live: `TestLiveJudgeServer` starts TS and Go servers on the sample with `--port 0 --serve-ms 3000`, fetches `/`, `/x`, `/api/rerun` (POST), `HEAD /` from both, and compares status, the TS-set headers and bodies. `TestBrowserOpen` puts `difftest/testdata/fakebrowser` first on `PATH` (scripts that log their argv and exit 0, or exit 1 for the failure case), unsets `CI`/`GITHUB_ACTIONS`/`FAULTLINE_NO_BROWSER`, runs both sides with `--serve-ms 100`, and compares the logged argv and stdout/stderr.

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/proofview/`
Expected: FAIL (undefined).

- [x] **Step 3: Implement** by transcription; regenerate goldens.

- [x] **Step 4: Run to verify the tests pass**

Run: `go test ./internal/proofview/ && FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run 'TestLiveJudgeServer|TestBrowserOpen|TestWriterReplay' -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/proofview internal/cli difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "feat(go): judge-proof, commit-proof-preview and the Git proof page server

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Secret-scanner corpus growth and parity

**Files:**
- Modify: `scripts/expand-secret-scanner-corpus.mjs` (new families), `benchmarks/secret-scanner-corpus/**` (regenerated), `benchmarks/secret-scanner-efficacy.json`, `docs/secret-scanner-efficacy.md` (regenerated by `scripts/measure-secret-scanner.mjs`)
- Create: `difftest/corpus_test.go`, `difftest/testdata/golden/secret-corpus.jsonl`, `difftest/gen/gen-corpus.ts`

**Interfaces:**
- Consumes: `redaction.RedactText`.
- Produces: `golden/secret-corpus.jsonl` lines `{id, redactedText, report}` (report as canonical JSON), generated by TS `redactText(text, "corpus:<id>")` over every corpus file read as UTF-8 (`readFileSync(p, "utf8")`).

**New families** (PR #195 `secret-scanner-corpus-growth`, spec 3.5), each deterministic from `h(seed)` like the existing ones:
- true_secret: `openai-unicode-wrap` (key between U+00A0/U+3000/U+FEFF), `github-json-escaped` (inside a JSON string with `\"`), `aws-secret-crlf` (`aws_secret_access_key = "..."` with CRLF), `pem-crlf` (CRLF PEM block), `bearer-url` (in a URL query), `generic-yaml` (`password: '...'`), `basic-header-folded` (header value after tabs) — 6 each.
- false_positive: `placeholder-like` (`<FAULTLINE_REDACTED:...>`-shaped text), `long-s-key` (`ſecret=...`, `K` in keys), `docs-token-word` (`token` in prose without `=`/`:`) — 6 each.
- should_miss: `split-across-lines` (key split by `\n`), `split-concat` (`"sk-" + "..."`), `url-encoded` (`%73k-...`), `lone-surrogate-break` (a lone surrogate inside a key, written as WTF-8 bytes) — 6 each.
- binary: `tiny-binary` (64–256 random bytes containing an embedded key), labelled from TS's actual behaviour — 6 files.
- Labels follow the families; the script keeps `caseCount` accurate and still exits 1 below 100 cases.

**Gotchas:**
- Do not add detector kinds (spec 3.5); this task only grows inputs.
- Files are read as UTF-8 text, so invalid bytes become U+FFFD on both sides (`nodefs.ReadText`).
- The measure script's `generatedAt` is wall-clock; run it with the seed preload (`node --import difftest/gen/seed.mjs`) so the committed efficacy JSON is reproducible.

- [x] **Step 1: Write the failing test**

`difftest/corpus_test.go`:

```go
package difftest

import (
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
)

func TestSecretCorpusParity(t *testing.T) {
	repo := repoRoot(t)
	golden := loadCorpusGolden(t, repo) // id → {redactedText, reportCanonical}
	labels := loadCorpusLabels(t, repo)
	if len(labels) != len(golden) || len(labels) < 200 {
		t.Fatalf("labels %d goldens %d", len(labels), len(golden))
	}
	for _, c := range labels {
		text, err := nodefs.ReadText(filepath.Join(repo, "benchmarks", "secret-scanner-corpus", filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatal(err)
		}
		out, report := redaction.RedactText(text, "corpus:"+c.ID)
		want := golden[c.ID]
		if out != want.RedactedText || canonicalOf(report.JSON()) != want.Report {
			t.Errorf("%s: redaction differs", c.ID)
		}
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test -tags faultline_test ./difftest/ -run TestSecretCorpusParity`
Expected: FAIL (`undefined: loadCorpusGolden`; then fewer than 200 cases).

- [x] **Step 3: Implement**: extend the script, regenerate the corpus, run `gen-corpus.ts`, regenerate the efficacy report:

```bash
node scripts/expand-secret-scanner-corpus.mjs && pnpm build && pnpm exec tsx difftest/gen/gen-corpus.ts && FAULTLINE_TEST_SEED=corpus node --import ./difftest/gen/seed.mjs scripts/measure-secret-scanner.mjs
```

- [x] **Step 4: Run to verify the test passes**

Run: `go test -tags faultline_test ./difftest/ -run TestSecretCorpusParity -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add scripts benchmarks docs/secret-scanner-efficacy.md difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test: grow the secret-scanner corpus and check Go redaction parity on it

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 19: Fault injection, real Docker, and the benchmark

**Files:**
- Create: `difftest/writerfault_live_test.go`, `difftest/docker_live_test.go`
- Modify: `difftest/benchmark_test.go` (`BenchmarkInvestigateGit`), `difftest/BENCHMARK.md`, `difftest/stack_live_test.go` (writer cases that need no `/proc`)

**Interfaces:**
- Consumes: slice 1's strace fault helpers in `difftest/fault_live_test.go` (`straceInject`), the writer harness.

**Fault matrix (Linux, strace; spec 3.2):** for each writer path — witness store record, policy, ratification, binding, ledger atomic write, minimization result, bundle stage files, `ROOT.sha256`, the stage rename, overlay staged file and rename, preview HTML — inject `ENOSPC`, `EIO`, `EACCES` on `openat`, `write`, `rename`/`renameat2`, `link`/`linkat`, `unlink`/`unlinkat` and `close` at the first matching call on that path (use `--inject=<sc>:error=<E>:when=1` filtered by path with a `-P <path>` and `--trace-path`, and run the command single-threaded with `GOMAXPROCS=1` on the Go side to avoid strace's per-thread `when` counting, round 6 finding 15). Compare stdout, stderr, exit code and the tree digest of the workspace (which files TS leaves behind) between TS and Go. Mask `<pid>` in ledger staged names.

**Real Docker (spec 4.1):** `TestLiveRealDocker` (skips unless `FAULTLINE_REAL_DOCKER=1`) builds a two-state repository with a `node:22-alpine@sha256:<pinned>` image (pull it first in CI), runs `investigate git` on both sides without fake Docker (recorder still on `PATH`), and compares: verdict per state, the run catalog with timings and nonces masked (`startedAt`, `finishedAt`, `durationMs`, `executionNonce`, `executionId`, `runId`), bundle file list, and `fl verify` VALID on both bundles under both verifiers. Byte equality is not expected because container timing differs.

**Benchmark (spec 4.2 point 8):** `BenchmarkInvestigateGit` times `investigate git` on `git-many-states` with the `pass-fail` fake Docker for TS and Go; record median and range of 10 runs in `BENCHMARK.md` as ranges, like slice 1. Evidence, not a gate.

- [x] **Step 1: Write the fault test skeleton and run it to see it fail**

```go
//go:build linux

package difftest

import "testing"

func TestLiveWriterFaults(t *testing.T) {
	requireStrace(t)
	for _, f := range writerFaultMatrix() { // {case, path glob, syscall, errno}
		t.Run(f.name(), func(t *testing.T) {
			ts := runTSWithFault(t, f)
			goRes := runGoWithFault(t, f)
			compareWriter(t, f.name(), goRes, ts)
		})
	}
}
```

Run: `FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test ./difftest/ -run TestLiveWriterFaults`
Expected: FAIL first (`undefined: writerFaultMatrix`), then real mismatches to fix in Go.

- [x] **Step 2: Implement the matrix and fix every mismatch in Go**

- [x] **Step 3: Add the real-Docker test and the benchmark; run them**

Run: `FAULTLINE_NODE_ORACLE=1 FAULTLINE_REAL_DOCKER=1 go test -tags faultline_test ./difftest/ -run 'TestLiveRealDocker' -v && go test -tags faultline_test ./difftest/ -run '^$' -bench BenchmarkInvestigateGit -benchtime 10x`
Expected: PASS; benchmark numbers recorded.

- [x] **Step 4: Commit**

```bash
git add difftest
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "test(go): writer fault injection, real-Docker comparison, investigate benchmark

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 20: CI, coverage gate, docs, and the done check

**Files:**
- Modify: `.github/workflows/go.yml`, `.github/scripts/check-skips.sh` allowlists, `difftest/covergate_test.go` (`coverpkg` gains every new package), `difftest/coverage_test.go` (collect error literals from the new packages), `difftest/KNOWN_DIFFERENCES.md`, `docs/superpowers/specs/2026-10-14-go-slice2-git-path-design.md` (the six corrections at the top of this plan), `docs/superpowers/specs/2026-09-25-go-migration-overview.md` (spec 4.2 point 7), this plan (tick the boxes)

**CI changes:**
- `go` job (six runners): every `go test` call gets `-tags faultline_test`; `TestWriterReplay` and `TestSecretCorpusParity` run everywhere; Windows skips only the `posixOnly` writer cases (the skip guard's Windows pattern lists them by name).
- `difftest-live` (ubuntu-24.04): Node 22.22.2; `pnpm build`; regenerate scenarios, writer goldens and corpus goldens and fail on `git diff --exit-code`; run every `TestLive*` added in Tasks 1–19, `TestLiveWriterFaults`, and `TestLiveRealDocker` with `FAULTLINE_REAL_DOCKER=1` after `docker pull` of the pinned image.
- `stack-live` (macOS): add `TestLiveJudgeServer`, `TestBrowserOpen`, `TestLiveSeedPreload`, `TestLiveSpawn`, `TestLiveHardenedGit`.
- The coverage gate (`FAULTLINE_COVERAGE_GATE` on ubuntu-24.04) covers `internal/clock`, `jsre`, `safedir`, `evidence`, `witnessresult`, `redaction`, `witness`, `ledgerbind`, `gitinv`, `sandbox`, `minimize`, `proofview` and the new `cli` files; unreachable branches are removed, not excluded (`// coverage gate: race only (...)` remains the only marker).

**KNOWN_DIFFERENCES entries to add as they are confirmed:** git bundle byte layout across git versions; Docker daemon diagnostics (masked); `net/http` response headers Node adds and Go does not (or vice versa); anything else a task recorded.

**Overview update (spec 4.2 point 7):** move sandbox, git-materialization, safe-overlay, environment-fingerprint, redaction, config read side, runtime-mapping load side, incident-binding, evidence-grade and turn-proof-promotion into slice 2; move `secret-allowlist` and `captureAllowlistedEvent` into slice 4; slice 4 lists PR #195's batch turn-snapshot staging (`--stdin-paths --no-filters` with the TAB/LF/CR fallback, gated on identical tree digests) and hostile-verifier corpus parity for turn bundles; check that every subcommand in `src/cli-app.ts` has a slice.

- [ ] **Step 1: Apply the CI, gate and docs changes; push the branch and read CI through the public API**

```bash
git push -u origin go-migration/slice2
curl -s "https://api.github.com/repos/Mizore66/faultline/commits/$(git rev-parse HEAD)/check-runs" | python3 -I -c 'import json,sys; [print(r["name"], r["status"], r["conclusion"]) for r in json.load(sys.stdin)["check_runs"]]'
```

Expected: every job `success`. Failing tests appear as check-run annotations (`check-skips.sh` emits them).

- [ ] **Step 2: Done check against spec 4.2**

1. `go test ./...` green on all six runners; every writer case matches its golden.
2. `difftest-live` and `stack-live` green, including `TestLiveRealDocker`.
3. On a machine without Node on `PATH`: `fl witness propose --input ...`, `approve`, `freeze`, `fl investigate git ...` → bundle → `fl verify <bundle>` → `fl judge-proof --bundle <bundle> --export-only` all succeed (`TestEndToEndWithoutNode` in `writer_test.go` runs this with `PATH` stripped of `node`).
4. Every bundle Go writes verifies VALID under both verifiers (Task 15 cross-check).
5. `TestSecretCorpusParity` green on the grown corpus.
6. KNOWN_DIFFERENCES lists every intentional difference.
7. Overview updated.
8. BENCHMARK recorded.

- [ ] **Step 3: Commit and hand off for adversarial review**

```bash
git add .github difftest docs
git -c user.name="Anas Tarek Qumhiyeh" -c user.email=anasqumhiyeh@gmail.com commit -m "ci(go): slice 2 CI, coverage gate and docs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
```

Give the user `https://github.com/Mizore66/faultline/compare/main...go-migration/slice2?expand=1`. Adversarial review rounds follow, as in slice 1, until a round finds no P0 or P1.
