# Go Slice 5a CLI Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port help, advanced help, quickstart and version to Go with exact current-tree TypeScript-oracle output and permanent, Node-independent regression evidence.

**Architecture:** Keep `cmd/fl` and the existing `internal/cli` dispatcher. Embed the frozen text in `internal/cli/help.go`, and read one linker-stampable `internal/buildinfo.Version` variable. A separate deterministic CLI-help generator captures TS output; direct API, actual-binary replay, standalone release-build and live tests consume that evidence without changing existing bundle/writer goldens.

**Tech Stack:** Go 1.27, standard library only; existing TypeScript/tsx generator tooling; Node 22.22.2; existing six-native-target GitHub Actions matrix.

**Spec:** [Approved Slice 5a spec](../specs/2026-10-30-go-slice5a-cli-surfaces-design.md), approved by the user on 2026-10-30. The spec is authoritative over this plan.

**Status:** Approved by the user on 2026-10-30 with “1 and parallelize the executions”. Parallel Sonnet implementation, runtime tests and builds are authorized within this scope. The user separately authorized commit, push, draft PR and CI on 2026-10-30 under Mizore66; merge, ready transition and release publication remain unauthorized. The commands and code below are execution instructions, not passing evidence; actual outcomes are tracked in this plan's SDD ledger. Code, bounded local verification and independent whole-branch review are complete, with no Critical or Important review findings. Exact-head hosted acceptance remains pending the authorized hosted CI run. Checked steps record their implemented/verified deliverables, with preserved deviations: Task 1's files preceded its scratch control-red (not test-first TDD), and pinned Podman named-volume/copy isolation replaced unavailable historical-image/bind-mount setup. The corpus has 67 cases; the live comparison and new-fixture regeneration pass without drift. No full six-native/full positional gate completion is claimed.

## Global Constraints

- Go 1.27; standard library only. Keep Git CLI behavior, canonical JSON and digest paths unchanged.
- `src/`, `package.json` and `pnpm-lock.yaml` are never modified under this authorization. The final retirement goal does not lift that freeze.
- Node 22.22.2; source pin `ce99a793af0b020bf1b9cf21114f8d7897b37207`; current-tree manifest baseline `6f2450549acb002f04a73dd3c1e3091299d7d59f`, version `0.1.4`. The source pin has manifest `0.1.3`, not `0.1.4`. The locally cached `v0.1.10-buildweek` tag names that pin, but the live remote tag observed 2026-10-30 peels to `89b8e2348037ece69faf47b6e230a78c3bab6f44` (manifest `0.1.4`, identical `src/`). Use the explicit source/manifest SHAs, not the tag name, as the corpus baseline.
- Preserve punctuation, Unicode, spaces, line breaks and final newlines. Compare complete stdout, stderr and exit code; no length-only comparisons, masks, several accepted outputs, version normalization or skip-allowlist expansion.
- `internal/buildinfo.Version` defaults to `0.1.4`. The exact linker target is `github.com/Mizore66/faultline/internal/buildinfo.Version`. No runtime package-manifest or environment override.
- Permanent default replay uses an unstamped binary and historical TS-captured output. Release stamping must not rewrite that default or its goldens.
- Existing `Next: pnpm fl help` hints, doctor Node checks, hook commands, other commands and process bootstrap/signal handling remain unchanged.
- No server/UI/demo/tutorial/witness/guided-investigation implementation, optional new report mode, dependency update, release packaging or TS deletion belongs to 5a.
- linux, darwin, windows × amd64, arm64: actual native execution is required for final acceptance. Cross-compilation is not native evidence; positional coverage remains Ubuntu amd64 evidence.
- No real Codex/OpenAI operation; local scripted fakes only. If a fake key is needed elsewhere, use the literal `fl-test-key`, never an `sk-` value.
- The TruffleHog pre-commit hook must never be bypassed. No real-looking secret is committed.
- Never change global Git configuration. Any separately authorized commit/push must use personal Mizore66 identity; never plain `gh`, never a work-account/remote fallback. A failed push stops publication.
- No commit, push, PR-ready transition, merge, auto-merge, publication, publisher setup, protected-path change or worktree/artifact deletion is authorized by this plan alone. Authorized commits end with `Co-Authored-By: Claude Code <noreply@anthropic.com>`; authorized PR descriptions end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Every local shell invocation stays below five minutes in aggregate, including setup and cleanup. No detached/watchdog-reset workaround, broad process cleanup, silent retries, CPU-load loops, sleep/yield fixes or pump deferral.
- Preserve failures, skips, incomplete attempts, exact source provenance and logs. A passing test is not a passing containing job. Historical Slice 4 results are not Slice 5 evidence.
- Workers stay in their dispatched workspace; no other worktree edits, pushes, `gh` calls or nested agents. The controller owns independent reviews.

---

## File responsibilities and task boundaries

| Task | Files | Responsibility |
| --- | --- | --- |
| 1 | Create `difftest/gen/cli-help-cases.ts`, `difftest/gen/cli-help-cases.test.ts`, `difftest/gen/gen-cli-help.ts`; create `difftest/testdata/cli-help/baseline.json` and `golden.jsonl` by capture | Oracle declaration, deterministic input matrix, preflight and new captured evidence only |
| 2 | Create `internal/buildinfo/version.go`, `internal/cli/help.go`, `internal/cli/help_test.go`, `difftest/help_data_test.go`, `difftest/help_test.go`; modify `internal/cli/cli.go`, `internal/cli/cli_test.go`, `difftest/coverage_test.go`, `difftest/KNOWN_DIFFERENCES.md` | Working default CLI surface, exact direct/replay tests and removal of only the obsolete help-stub expectations |
| 3 | Create `difftest/release/help_test.go` | Actual untagged default/stamped binary tests, empty PATH and unrelated-manifest tests on every native target |
| 4 | Create `difftest/help_live_test.go`; modify `.github/workflows/go.yml` and the overview/spec status when evidence supports it | Live preflight/comparison, pinned generator CI wiring and final evidence closure |

No production test-helper package or generic harness refactor is needed. Small decoding code in `internal/cli` tests is intentionally local; the stricter `difftest` loader is shared by replay and live tests. `difftest/release` already has a portable `repoRoot(t *testing.T) string` in `clock_test.go`; reuse it without its POSIX-fake/clock test or `tagged` skip behavior.

### Shared evidence contracts

`baseline.json` is deterministic UTF-8 JSON plus one final LF, with exactly these five fields in this order:

```json
{
  "schema": "faultline.cli-help-oracle.v1",
  "sourceCommit": "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  "manifestCommit": "6f2450549acb002f04a73dd3c1e3091299d7d59f",
  "manifestVersion": "0.1.4",
  "nodeVersion": "22.22.2"
}
```

Each `golden.jsonl` line is `{case,args,stdout,stderr,exit}` in that property order, with actual UTF-8 text represented by JSON string escaping. `args` is an array, never a shell command; zero arguments are `[]`, not `null`. These are CLI output fixtures, not digest-path canonical JSON.

The matrix contains 67 rows: one no-argument row; 17 variants for each of the three help aliases; three variants each for `advanced`, `quickstart`, `--version` and `-V`; and three top-level negative/dispatch rows. Task 1 tests the count, unique identities and specific argv boundaries; the Go loader independently checks count and unique identities. CI regeneration fixes the complete ordered input/output matrix, which every replay executes. Existing fixture files outside `testdata/cli-help/` remain byte-identical.

### Execution setup and bounded commands

Do this only after plan approval plus production authorization. Use the execution skill's workspace/ledger setup; preserve the existing documentation edits and old Slice 4 worktrees. Do not create a second implementation that loses the uncommitted approved spec. If an isolated worktree is required, transfer the three reviewed documentation files explicitly after inspecting both source and destination; never discard or clean them. Record the chosen workspace and the base SHA in the ledger.

Before Task 1, record these read-only checks and stop on unexpected changes:

```sh
git status --short
git rev-parse HEAD
git diff -- src/ package.json pnpm-lock.yaml
git diff --name-only ce99a793af0b020bf1b9cf21114f8d7897b37207 HEAD -- src/
git remote get-url origin
```

Expected base for this plan is `6f2450549acb002f04a73dd3c1e3091299d7d59f`. A different base requires reconciling the actual changes, not resetting the branch. No source-pin history lookup is required by permanent replay or the shallow hosted CI checkout. Generation/live provenance relies on the existing frozen-source guard plus this controller-verified invariant; the Node/manifest/CLI version preflight is not a source-hash check. If the frozen-source invariant changes, stop attribution to the old pin pending a reviewed baseline transition.

Use this external scratch utility, not a new committed project dependency, at `<this plan's SDD workspace>/bounded.py`. It gives each future local command one owned process group, a unique combined log and a receipt. The controller writes it during execution setup; it is not created while writing this plan.

```python
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

limit = int(sys.argv[1])
label = sys.argv[2]
argv = sys.argv[3:]
assert 0 < limit <= 240 and argv
workspace = Path(__file__).resolve().parent
stem = f"{time.time_ns()}-{label}"
log = workspace / f"{stem}.log"
receipt = workspace / f"{stem}.json"
p = None
status = 125

def cleanup():
    if p is None:
        return
    try:
        os.killpg(p.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        p.wait(timeout=3)
    except subprocess.TimeoutExpired:
        os.killpg(p.pid, signal.SIGKILL)
        p.wait(timeout=3)
    # The leader may have exited while a compiler/Node descendant remains.
    try:
        os.killpg(p.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass

try:
    with log.open("xb") as output:
        p = subprocess.Popen(argv, stdout=output, stderr=subprocess.STDOUT,
                             start_new_session=True)
        try:
            status = p.wait(timeout=limit)
        except subprocess.TimeoutExpired:
            status = 124
        finally:
            cleanup()
finally:
    fact = {"argv": argv, "exit": status, "log": str(log)}
    receipt.write_text(json.dumps(fact, indent=2) + "\n")
    print(json.dumps(fact))
sys.exit(status if 0 <= status <= 255 else 128 - status)
```

Every `Run:` command below is one logical invocation, executed using this utility with limit 240 and a distinct label, e.g. `python3 -I <workspace>/bounded.py 240 task2-unit go test ...`. The Go timeout is 210 s, leaving time for tool startup/cleanup. Do not chain individually bounded commands in one shell to exceed the aggregate limit. Inspect each log and retain its actual exit code. Do not rerun a failure without recording its cause and what changed.

The host's default Node 22.23.4 is **not** the oracle. A previously used local image is `localhost/r73img`; verify its actual `process.versions.node` before use. Keep generator/CLI commands on actual 22.22.2. If the image/runtime is absent, record the prerequisite instead of silently using another Node. No download or new toolchain installation is implied by this plan.

For a Go-less pinned-Node container, compile the tagged difftest executable and tagged unstamped `fl` on the host for the inspected container architecture, each in its own bounded invocation. Then run only `TestLiveCLIHelp` with `FAULTLINE_FL_BINARY` pointing at that binary. This uses the existing `TestMain` prebuilt-binary contract, not a new skip. The separate `difftest/release` package requires Go to build its candidates and runs on native Go hosts/CI, not this Go-less container. A cgo-free local cross-build is fallback/fast-path evidence, never musl release-signal evidence.

---

### Task 1: Capture the immutable CLI-help oracle corpus

**Files:**
- Create: `difftest/gen/cli-help-cases.ts`
- Create: `difftest/gen/cli-help-cases.test.ts`
- Create: `difftest/gen/gen-cli-help.ts`
- Generate: `difftest/testdata/cli-help/baseline.json`
- Generate: `difftest/testdata/cli-help/golden.jsonl`

**Interfaces:**
- Consumes: actual Node 22.22.2, current-tree `package.json` version `0.1.4`, freshly built `dist/cli.js`.
- Produces: `cliHelpBaseline`, `cliHelpCases(): {case: string; args: string[]}[]`, and `assertCLIHelpOracle(nodeVersion: string, manifestVersion: unknown, result: CLIHelpResult): void` in `cli-help-cases.ts`; `CLIHelpResult = {stdout: string; stderr: string; exit: number}`.
- Produces: the five-field metadata and ordered 67-row JSONL contract above for Tasks 2–4. No Go production output is consulted during capture.

- [x] **Step 1: Write the contract/preflight tests first.** Add `cli-help-cases.test.ts`:

```ts
import assert from "node:assert/strict";
import test from "node:test";
import { assertCLIHelpOracle, cliHelpBaseline, cliHelpCases } from "./cli-help-cases.js";

const good = { stdout: "FaultLine 0.1.4\n", stderr: "", exit: 0 };

test("CLI-help inputs cover exact aliases, positions and argv boundaries", () => {
  const rows = cliHelpCases();
  assert.equal(rows.length, 67);
  assert.equal(new Set(rows.map((r) => r.case)).size, 67);
  const args = rows.map((r) => JSON.stringify(r.args));
  for (const argv of [[], ["help", "ignored", "advanced"],
    ["help", "ignored", "--advanced"], ["-h", "Advanced"],
    ["--help", "--advanced=false"], ["help", ""],
    ["help", "two words"], ["help", "é😀"],
    ["advanced", "ignored", "--advanced"], ["--version", "é😀"],
    ["--advanced"], ["Help"], ["verify"]]) {
    assert.ok(args.includes(JSON.stringify(argv)), JSON.stringify(argv));
  }
});

test("oracle preflight refuses drift rather than changing its baseline", () => {
  assert.doesNotThrow(() => assertCLIHelpOracle("22.22.2", "0.1.4", good));
  assert.throws(() => assertCLIHelpOracle("22.23.4", "0.1.4", good));
  for (const bad of ["0.1.3", "9.9.9", null, 14]) {
    assert.throws(() => assertCLIHelpOracle("22.22.2", bad, good));
  }
  for (const bad of [
    { ...good, stdout: "FaultLine 0.1.3\n" },
    { ...good, stdout: "FaultLine 0.1.4" },
    { ...good, stderr: "unexpected\n" },
    { ...good, exit: 1 },
  ]) assert.throws(() => assertCLIHelpOracle("22.22.2", "0.1.4", bad));
  assert.equal(cliHelpBaseline.sourceCommit, "ce99a793af0b020bf1b9cf21114f8d7897b37207");
});
```

Run on the verified pinned runtime: `pnpm exec tsx --test difftest/gen/cli-help-cases.test.ts`.
Expected red: missing `cli-help-cases` module. Preserve the receipt; do not treat a wrong Node/dependency failure as the intended red.

- [x] **Step 2: Implement the deterministic contract.** Add `cli-help-cases.ts`:

```ts
import assert from "node:assert/strict";

export const cliHelpBaseline = {
  schema: "faultline.cli-help-oracle.v1",
  sourceCommit: "ce99a793af0b020bf1b9cf21114f8d7897b37207",
  manifestCommit: "6f2450549acb002f04a73dd3c1e3091299d7d59f",
  manifestVersion: "0.1.4",
  nodeVersion: "22.22.2",
} as const;

export type CLIHelpResult = { stdout: string; stderr: string; exit: number };

export function assertCLIHelpOracle(nodeVersion: string, manifestVersion: unknown,
  result: CLIHelpResult): void {
  assert.equal(nodeVersion, cliHelpBaseline.nodeVersion, "CLI-help oracle Node drift");
  assert.equal(manifestVersion, cliHelpBaseline.manifestVersion, "CLI-help manifest drift");
  assert.deepEqual(result, { stdout: "FaultLine 0.1.4\n", stderr: "", exit: 0 },
    "CLI-help oracle version-output drift");
}

export function cliHelpCases(): { case: string; args: string[] }[] {
  const rows = [{ case: "no-args", args: [] as string[] }];
  const aliases = [["word", "help"], ["long", "--help"], ["short", "-h"]] as const;
  const variants: [string, string[]][] = [
    ["normal", []], ["ignored", ["ignored"]],
    ["advanced-first", ["advanced"]], ["advanced-extra", ["advanced", "ignored"]],
    ["advanced-flag", ["--advanced"]], ["advanced-flag-late", ["ignored", "--advanced"]],
    ["advanced-not-first", ["ignored", "advanced"]],
    ["case-name", ["Advanced"]], ["case-flag", ["--Advanced"]],
    ["equals-flag", ["--advanced=false"]], ["bare-dash", ["--"]],
    ["empty", [""]], ["space", ["two words"]], ["unicode", ["é😀"]],
    ["advanced-empty", ["", "--advanced"]],
    ["advanced-space", ["two words", "--advanced"]],
    ["advanced-unicode", ["é😀", "--advanced"]],
  ];
  for (const [label, alias] of aliases) {
    for (const [variant, args] of variants) {
      rows.push({ case: `help-${label}-${variant}`, args: [alias, ...args] });
    }
  }
  for (const [label, command] of [["advanced", "advanced"], ["quickstart", "quickstart"],
    ["version-long", "--version"], ["version-short", "-V"]]) {
    for (const [suffix, args] of [["plain", []], ["extra", ["ignored", "--advanced"]],
      ["unicode", ["é😀"]]] as [string, string[]][]) {
      rows.push({ case: `${label}-${suffix}`, args: [command, ...args] });
    }
  }
  rows.push({ case: "unknown-advanced-flag", args: ["--advanced"] },
    { case: "unknown-case", args: ["Help"] },
    { case: "existing-verify-usage", args: ["verify"] });
  return rows;
}
```

Run the same test command. Expected green: both Node tests pass, with no generated fixtures or protected-file edits.

- [x] **Step 3: Implement capture as a separate script.** Add `gen-cli-help.ts`:

```ts
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { assertCLIHelpOracle, cliHelpBaseline, cliHelpCases, type CLIHelpResult } from "./cli-help-cases.js";

const repo = resolve(".");
const cli = join(repo, "dist", "cli.js");
assert.equal(process.versions.node, cliHelpBaseline.nodeVersion, "CLI-help oracle Node drift");
function newestMtime(dir: string): number {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    newest = Math.max(newest, entry.isDirectory() ? newestMtime(path) : statSync(path).mtimeMs);
  }
  return newest;
}
assert.ok(existsSync(cli) && statSync(cli).mtimeMs >= newestMtime(join(repo, "src")),
  "dist/cli.js is missing or older than src/: run pnpm build first");
const manifest: { version?: unknown } = JSON.parse(readFileSync(join(repo, "package.json"), "utf8"));
function run(args: string[]): CLIHelpResult {
  const child = spawnSync(process.execPath, [cli, ...args], {
    cwd: repo, encoding: "utf8", timeout: 5000, maxBuffer: 1024 * 1024,
  });
  if (child.error) throw child.error;
  assert.equal(child.signal, null, `CLI-help child signalled: ${JSON.stringify(args)}`);
  assert.notEqual(child.status, null, `CLI-help child has no exit: ${JSON.stringify(args)}`);
  return { stdout: child.stdout, stderr: child.stderr, exit: child.status! };
}
assertCLIHelpOracle(process.versions.node, manifest.version, run(["--version"]));
const cases = cliHelpCases();
assert.equal(cases.length, 67);
const rows = cases.map((c) => ({ case: c.case, args: c.args, ...run(c.args) }));
for (const row of rows) {
  const failure = row.case.startsWith("unknown-") || row.case === "existing-verify-usage";
  assert.equal(row.exit, failure ? 1 : 0, row.case);
  assert.equal(failure ? row.stdout : row.stderr, "", row.case);
}
// All preflight and capture succeed before either committed output is touched.
const directory = join(repo, "difftest", "testdata", "cli-help");
mkdirSync(directory, { recursive: true });
writeFileSync(join(directory, "baseline.json"), `${JSON.stringify(cliHelpBaseline, null, 2)}\n`, "utf8");
writeFileSync(join(directory, "golden.jsonl"), `${rows.map((r) => JSON.stringify(r)).join("\n")}\n`, "utf8");
console.log(`wrote ${rows.length} CLI-help goldens`);
```

Do not import `src/cli-help.ts`, use Go text as expected output, modify a protected manifest to test failure, or funnel these commands through `gen-goldens.ts`'s hard-coded `verify` prefix.

- [x] **Step 4: Capture and demonstrate deterministic regeneration.** On pinned Node 22.22.2, run `pnpm build` in one bounded invocation. Then run `pnpm exec tsx difftest/gen/gen-cli-help.ts` in another. The build writes only ignored `dist/`; the generator may create only the two new CLI-help fixture files. Snapshot both new files into this plan's evidence workspace; run the generator once more as a documented determinism check and compare both byte-for-byte with the snapshot. Do not overwrite a historical failure receipt.

For local `localhost/r73img`, use a new temporary `/tmp` work directory inside the container, copy only `package.json`, `tsconfig.json`, `src/` and these three generator files from the read-only repository mount, and link `/src/node_modules`. Run `pnpm build` and capture there; copy **only** `baseline.json` and `golden.jsonl` back through a mount dedicated to the inspected new `difftest/testdata/cli-help/` directory. Run as uid 1000. Do not mount the whole repository writable or copy old Slice 4 evidence/worktrees. Each container invocation is owned by the aggregate deadline; use an invocation-specific `--name`/cidfile and bounded `podman stop`/`podman rm` for that container only if the client is terminated. Never clean up other containers or assume killing the client stops the container.

Before copying generated evidence back, validate the five metadata values, count 67, unique case IDs, and exact version rows. Reject the entire capture on preflight/case failure; do not copy a partial result.

- [x] **Step 5: Review this task and record its actual outcome.** Verify the protected paths and every pre-existing `difftest/testdata` file are unchanged; all new fixture output comes from TS. Record Node version, source/manifest provenance, commands, exits, hashes of both new files, and determinism comparison. Independent review checks contract/schema/generator behavior. No commit unless separately authorized; proposed commit subject is `test(go): capture CLI-help oracle baseline`.

### Task 2: Implement exact default help/version behavior and replay

**Files:**
- Create: `internal/buildinfo/version.go`, `internal/cli/help.go`, `internal/cli/help_test.go`
- Create: `difftest/help_data_test.go`, `difftest/help_test.go`
- Modify: `internal/cli/cli.go:69-75` (dispatch), `internal/cli/cli_test.go:29-31` (obsolete no-args failure)
- Modify: `difftest/coverage_test.go:125` (obsolete help literal only)
- Modify: `difftest/KNOWN_DIFFERENCES.md` (append standalone version entry)

**Interfaces:**
- Consumes: Task 1's five-field metadata and ordered 67-row `{case,args,stdout,stderr,exit}` corpus.
- Produces: `internal/buildinfo.Version string`, default `"0.1.4"`; `helpCommand(e *env, args []string) error`; `usage`, `advancedUsage`, `quickstart` constants in `internal/cli/help.go`.
- Keeps: `cli.Run(args []string, stdout, stderr io.Writer) int`, `RunWithStdin` and `env.out(s string)` unchanged.
- Produces for Task 4: `cliHelpCase`, `validateCLIHelpBaseline(raw []byte) error`, `loadCLIHelpCases(t *testing.T) []cliHelpCase`, `runCLIHelpProcess(t *testing.T, binary, dir string, env []string, args ...string) (string, string, int)` in tagged `difftest` tests. `flBinary` remains the existing `TestMain` unstamped tagged/coverage binary.

- [x] **Step 1: Add direct API regression tests before production code.** Add this complete `internal/cli/help_test.go`:

```go
package cli

import (
    "bufio"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
)

func TestCLIHelpFastPaths(t *testing.T) {
    file, err := os.Open(filepath.Join("..", "..", "difftest", "testdata", "cli-help", "golden.jsonl"))
    if err != nil { t.Fatal(err) }
    defer file.Close()
    scanner := bufio.NewScanner(file)
    scanner.Buffer(make([]byte, 4096), 1<<20)
    seen := map[string]bool{}
    for scanner.Scan() {
        var row struct {
            Case string `json:"case"`
            Args []string `json:"args"`
            Stdout string `json:"stdout"`
            Stderr string `json:"stderr"`
            Exit int `json:"exit"`
        }
        if err := json.Unmarshal(scanner.Bytes(), &row); err != nil { t.Fatal(err) }
        if row.Case == "" || seen[row.Case] { t.Fatalf("duplicate/empty case %q", row.Case) }
        seen[row.Case] = true
        t.Run(row.Case, func(t *testing.T) {
            stdout, stderr, code := run(row.Args...)
            if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
                t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
                    row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
            }
        })
    }
    if err := scanner.Err(); err != nil { t.Fatal(err) }
    if len(seen) != 67 { t.Fatalf("cases=%d, want 67", len(seen)) }
}
```

Run: `go test -count=1 -timeout 210s ./internal/cli -run '^TestCLIHelpFastPaths$' -v`.
Expected red: successful TS help/version paths still return `not yet ported`. The three existing failure/dispatch rows already match and are not counted as new functionality.

- [x] **Step 2: Add strict metadata/corpus loading and actual-process helpers.** Create tagged `difftest/help_data_test.go` with:

```go
//go:build faultline_test

package difftest

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"
)

type cliHelpCase struct {
    Case string `json:"case"`
    Args []string `json:"args"`
    Stdout string `json:"stdout"`
    Stderr string `json:"stderr"`
    Exit int `json:"exit"`
}

func validateCLIHelpBaseline(raw []byte) error {
    var got map[string]string
    if err := json.Unmarshal(raw, &got); err != nil { return err }
    want := map[string]string{
        "schema": "faultline.cli-help-oracle.v1",
        "sourceCommit": "ce99a793af0b020bf1b9cf21114f8d7897b37207",
        "manifestCommit": "6f2450549acb002f04a73dd3c1e3091299d7d59f",
        "manifestVersion": "0.1.4",
        "nodeVersion": "22.22.2",
    }
    if len(got) != len(want) { return fmt.Errorf("CLI-help metadata fields: got %d, want %d", len(got), len(want)) }
    for k, v := range want {
        if got[k] != v { return fmt.Errorf("CLI-help metadata %s: got %q, want %q", k, got[k], v) }
    }
    return nil
}

func loadCLIHelpCases(t *testing.T) []cliHelpCase {
    t.Helper()
    directory := filepath.Join("testdata", "cli-help")
    raw, err := os.ReadFile(filepath.Join(directory, "baseline.json"))
    if err != nil { t.Fatal(err) }
    if err := validateCLIHelpBaseline(raw); err != nil { t.Fatal(err) }
    file, err := os.Open(filepath.Join(directory, "golden.jsonl"))
    if err != nil { t.Fatal(err) }
    defer file.Close()
    scanner := bufio.NewScanner(file)
    scanner.Buffer(make([]byte, 4096), 1<<20)
    var rows []cliHelpCase
    seen := map[string]bool{}
    for scanner.Scan() {
        var row cliHelpCase
        if err := json.Unmarshal(scanner.Bytes(), &row); err != nil { t.Fatal(err) }
        if row.Case == "" || seen[row.Case] || row.Args == nil || row.Exit < 0 || row.Exit > 1 {
            t.Fatalf("invalid/duplicate CLI-help case %q", row.Case)
        }
        seen[row.Case] = true
        rows = append(rows, row)
    }
    if err := scanner.Err(); err != nil { t.Fatal(err) }
    if len(rows) != 67 { t.Fatalf("CLI-help cases=%d, want 67", len(rows)) }
    return rows
}

func runCLIHelpProcess(t *testing.T, binary, dir string, env []string, args ...string) (string, string, int) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, binary, args...)
    cmd.Dir, cmd.Env = dir, env
    cmd.WaitDelay = time.Second
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err := cmd.Run()
    if ctx.Err() != nil { t.Fatalf("CLI-help process timeout: %q %q", binary, args) }
    var exitErr *exec.ExitError
    if err != nil && !errors.As(err, &exitErr) { t.Fatalf("CLI-help process: %v", err) }
    return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
```

This helper is deliberately one-shot: do not use `runFl`'s coverage-output retry or normalize coverage diagnostics. Replay/live calls inherit `os.Environ()` so an instrumented `flBinary` retains `GOCOVERDIR`; release/Node-free tests use independent untagged binaries. Do not use these helpers to introduce an alternate golden input path.

- [x] **Step 3: Add replay plus metadata-negative tests.** Create tagged `difftest/help_test.go`:

```go
//go:build faultline_test

package difftest

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
)

func TestCLIHelpBaseline(t *testing.T) {
    raw, err := os.ReadFile(filepath.Join("testdata", "cli-help", "baseline.json"))
    if err != nil { t.Fatal(err) }
    if err := validateCLIHelpBaseline(raw); err != nil { t.Fatal(err) }
    var original map[string]string
    if err := json.Unmarshal(raw, &original); err != nil { t.Fatal(err) }
    for _, key := range []string{"schema", "sourceCommit", "manifestCommit", "manifestVersion", "nodeVersion"} {
        t.Run(key, func(t *testing.T) {
            changed := map[string]string{}
            for k, v := range original { changed[k] = v }
            changed[key] = "unexpected"
            bad, err := json.Marshal(changed)
            if err != nil { t.Fatal(err) }
            if validateCLIHelpBaseline(bad) == nil { t.Fatalf("accepted changed %s", key) }
            delete(changed, key)
            bad, err = json.Marshal(changed)
            if err != nil { t.Fatal(err) }
            if validateCLIHelpBaseline(bad) == nil { t.Fatalf("accepted missing %s", key) }
        })
    }
    for _, bad := range []string{"null", "{}", "{", string(raw) + "{}"} {
        if validateCLIHelpBaseline([]byte(bad)) == nil { t.Fatalf("accepted metadata %q", bad) }
    }
    extra := map[string]string{}
    for k, v := range original { extra[k] = v }
    extra["unreviewed"] = "value"
    bad, err := json.Marshal(extra)
    if err != nil { t.Fatal(err) }
    if validateCLIHelpBaseline(bad) == nil { t.Fatal("accepted extra metadata field") }
}

func TestCLIHelpReplay(t *testing.T) {
    rows := loadCLIHelpCases(t)
    outside := t.TempDir()
    for _, row := range rows {
        t.Run(row.Case, func(t *testing.T) {
            stdout, stderr, code := runCLIHelpProcess(t, flBinary, outside, os.Environ(), row.Args...)
            if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
                t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
                    row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
            }
        })
    }
}
```

Run: `go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^TestCLIHelp(Baseline|Replay)$' -v`.
Expected: metadata test passes; replay fails on the unported successful paths. It must not start Node or read the protected TS manifest. The actual `flBinary` is built by existing `TestMain`, unstamped; a supplied prebuilt must also be unstamped or the version cases fail.

- [x] **Step 4: Implement the embedded surface.** Create:

```go
// internal/buildinfo/version.go
package buildinfo

// Version is embedded in the binary; release candidates may set it with -X.
var Version = "0.1.4"
```

Add the full constants from Appendix A to `internal/cli/help.go`, together with this selector:

```go
func helpCommand(e *env, args []string) error {
    advanced := len(args) > 0 && args[0] == "advanced"
    for _, arg := range args {
        if arg == "--advanced" { advanced = true }
    }
    text := usage
    if advanced { text = advancedUsage }
    e.out(text + "\n")
    return nil
}
```

Import `internal/buildinfo` in `internal/cli/cli.go`. Replace only the empty-argument stub and add these cases **before** existing handlers and the `topLevelCommands` fallback:

```go
if len(args) == 0 {
    return helpCommand(e, nil)
}
command, rest := args[0], args[1:]
switch {
case command == "help" || command == "--help" || command == "-h":
    return helpCommand(e, rest)
case command == "advanced":
    e.out(advancedUsage + "\n")
    return nil
case command == "quickstart":
    e.out(quickstart)
    return nil
case command == "--version" || command == "-V":
    e.out("FaultLine " + buildinfo.Version + "\n")
    return nil
// Existing verify/witness/... cases continue unchanged.
}
```

The final comment above describes the insertion boundary, not a replacement for the existing cases. Do not add standalone `--advanced`, validate ignored arguments, or start tool/network/browser/repository work.

Remove exactly the no-argument failure row and its preceding two-line comment from `TestFailures`. Delete exactly the `"not yet ported: help"` entry from `unreachableLiterals`; leave the generic fallback and all other unported-command waivers intact. Do not add `internal/buildinfo` to positional-coverage gated packages: it contains a variable and no error site.

Append this known difference:

```markdown
## Standalone CLI version metadata (slice 5a)

The normal unstamped Go binary prints `FaultLine 0.1.4\n`, matching the declared current-tree TypeScript oracle manifest. The source/submission pin `ce99a79` has manifest `0.1.3`; those versions are not normalized into equality. TypeScript reads its own package manifest at runtime and returns `unknown` when that file is absent, malformed or has a non-string version. Go retains its embedded `internal/buildinfo.Version` in those deployment circumstances and does not read a runtime manifest or environment override. A separately linker-stamped candidate reports its exact stamp. Permanent default replay compares an unstamped binary with the original TS-captured version bytes; stamping or TS retirement does not replace that baseline.
```

- [x] **Step 5: Run green and regression checks.** Format the new Go files and the modified dispatcher/tests with `gofmt -w` on those explicit paths only. Run each command as a separate bounded invocation:

```sh
go test -count=1 -timeout 210s ./internal/cli -run '^(TestCLIHelpFastPaths|TestFailures|TestExecutionPolicyHint)$' -v
go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^TestCLIHelp(Baseline|Replay)$' -v
go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^TestGoldensReachEveryErrorLiteral$' -v
go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^TestUnreachableSitesTable$' -v
go vet ./internal/buildinfo ./internal/cli
go vet -tags faultline_test ./internal/buildinfo ./internal/cli ./difftest
```

`TestGoldensReachEveryErrorLiteral` checks the literal inventory, including stale `unreachableLiterals` rows; `TestUnreachableSitesTable` checks the separate positional-waiver table and is not a substitute for it. The former reads the existing corpus but does not execute the entire replay. If its bounded attempt cannot finish, retain that result and leave its acceptance pending the hosted gate, not a reset watchdog. Full positional coverage and the full package run remain existing hosted gates. These two source/table checks alone are not runtime positional-coverage evidence.

Record red/green receipts and review the changed production paths against the captured bytes. No commit unless authorized; proposed subject: `feat(go): port help quickstart and embedded version`.

### Task 3: Prove default/stamped release binaries work without Node

**Files:**
- Create: `difftest/release/help_test.go`
- Reuse without modification: `difftest/release/clock_test.go`'s `repoRoot(t *testing.T) string`

**Interfaces:**
- Consumes: Task 1's CLI-help rows, Task 2's production CLI and linker target.
- Produces: `TestCLIHelpStandalone`, `TestCLIHelpVersionStamp`, `buildCLIHelpRelease(t *testing.T, stamp string) string`, `runCLIHelpRelease(t *testing.T, bin, cwd string, env []string, args ...string) (string, string, int)`.
- All are in package `release`, with no build constraint, no `tagged` branch, no OS skip and no dependence on `FAULTLINE_FL_BINARY`. They run in both existing native matrix passes; their child binaries are always untagged.

- [x] **Step 1: Add the portable helpers and standalone test.** Create this file (add the stamp test in Step 3):

```go
package release

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "testing"
    "time"
)

func buildCLIHelpRelease(t *testing.T, stamp string) string {
    t.Helper()
    name := "fl"
    if runtime.GOOS == "windows" { name += ".exe" }
    bin := filepath.Join(t.TempDir(), name)
    args := []string{"build", "-o", bin}
    if stamp != "" {
        args = append(args, "-ldflags=-X=github.com/Mizore66/faultline/internal/buildinfo.Version="+stamp)
    }
    args = append(args, "./cmd/fl")
    ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, "go", args...)
    cmd.Dir = repoRoot(t)
    cmd.WaitDelay = time.Second
    if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("build CLI-help release: %v\n%s", err, out) }
    return bin
}

func runCLIHelpRelease(t *testing.T, bin, cwd string, env []string, args ...string) (string, string, int) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, bin, args...)
    cmd.Dir, cmd.Env = cwd, env
    cmd.WaitDelay = time.Second
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err := cmd.Run()
    if ctx.Err() != nil { t.Fatalf("CLI-help release timeout: %q", args) }
    var exitErr *exec.ExitError
    if err != nil && !errors.As(err, &exitErr) { t.Fatalf("CLI-help release: %v", err) }
    return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func cliHelpEmptyPathEnv(t *testing.T) []string {
    t.Helper()
    empty, home, tmp := t.TempDir(), t.TempDir(), t.TempDir()
    entries, err := os.ReadDir(empty)
    if err != nil || len(entries) != 0 { t.Fatalf("controlled PATH is not empty: %v", err) }
    env := []string{"PATH="+empty, "HOME="+home, "USERPROFILE="+home,
        "TMPDIR="+tmp, "TEMP="+tmp, "TMP="+tmp,
        "FAULTLINE_VERSION=not-a-runtime-version", "npm_package_version=not-a-runtime-version"}
    for _, key := range []string{"SystemRoot", "WINDIR"} {
        if value := os.Getenv(key); value != "" { env = append(env, key+"="+value) }
    }
    return env
}

func TestCLIHelpStandalone(t *testing.T) {
    bin := buildCLIHelpRelease(t, "")
    file, err := os.Open(filepath.Join(repoRoot(t), "difftest", "testdata", "cli-help", "golden.jsonl"))
    if err != nil { t.Fatal(err) }
    defer file.Close()
    cwd, env := t.TempDir(), cliHelpEmptyPathEnv(t)
    scanner := bufio.NewScanner(file)
    scanner.Buffer(make([]byte, 4096), 1<<20)
    count := 0
    for scanner.Scan() {
        var row struct {
            Case string `json:"case"`
            Args []string `json:"args"`
            Stdout string `json:"stdout"`
            Stderr string `json:"stderr"`
            Exit int `json:"exit"`
        }
        if err := json.Unmarshal(scanner.Bytes(), &row); err != nil { t.Fatal(err) }
        count++
        t.Run(row.Case, func(t *testing.T) {
            stdout, stderr, code := runCLIHelpRelease(t, bin, cwd, env, row.Args...)
            if stdout != row.Stdout || stderr != row.Stderr || code != row.Exit {
                t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
                    row.Args, stdout, row.Stdout, stderr, row.Stderr, code, row.Exit)
            }
        })
    }
    if err := scanner.Err(); err != nil { t.Fatal(err) }
    if count != 67 { t.Fatalf("CLI-help cases=%d, want 67", count) }
}
```

Use absolute binary paths, `exec.CommandContext` and argument arrays; no shell script, `lookPathIn`, symlink privilege, `git --exec-path`, process-global PATH mutation or Node startup. The explicit child environment has one PATH entry with an empty directory, avoiding duplicate `Path`/`PATH` entries on Windows. Windows pipe output must retain LF and UTF-8, not console CRLF conversion.

Task 2 already provides the production feature. This task strengthens its regression tests; use the explicit test-helper mutation control in Step 3 rather than reverting production files or editing another worker's workspace.

- [x] **Step 2: Add default/stamped deployment assertions before the negative control.** Append to the same file:

```go
func TestCLIHelpVersionStamp(t *testing.T) {
    for _, build := range []struct{ name, stamp, want string }{
        {"default", "", "FaultLine 0.1.4\n"},
        {"stamped", "0.0.0-slice5a-test", "FaultLine 0.0.0-slice5a-test\n"},
    } {
        t.Run(build.name, func(t *testing.T) {
            bin := buildCLIHelpRelease(t, build.stamp)
            root := t.TempDir()
            cwd := filepath.Join(root, "work")
            if err := os.Mkdir(cwd, 0o755); err != nil { t.Fatal(err) }
            env := cliHelpEmptyPathEnv(t)
            for _, manifest := range []struct{ name, body string }{
                {"missing", ""}, {"unrelated", `{"version":"9.9.9-unrelated"}`},
                {"malformed", "{"}, {"non-string", `{"version":42}`},
            } {
                t.Run(manifest.name, func(t *testing.T) {
                    for _, dir := range []string{root, cwd, filepath.Dir(bin)} {
                        path := filepath.Join(dir, "package.json")
                        if manifest.name == "missing" {
                            if err := os.Remove(path); err != nil && !os.IsNotExist(err) { t.Fatal(err) }
                        } else if err := os.WriteFile(path, []byte(manifest.body), 0o600); err != nil { t.Fatal(err) }
                    }
                    for _, alias := range []string{"--version", "-V"} {
                        stdout, stderr, code := runCLIHelpRelease(t, bin, cwd, env, alias, "ignored")
                        if stdout != build.want || stderr != "" || code != 0 {
                            t.Fatalf("%s: stdout=%q want=%q stderr=%q exit=%d", alias, stdout, build.want, stderr, code)
                        }
                    }
                })
            }
        })
    }
}
```

Only temporary unrelated manifests are written. The test does not damage TS's own package to recreate its `unknown` fallback. The production variable remains the sole version source; no test-tag/runtime stamp override is added.

- [x] **Step 3: Verify the real stamp seam is observed.** Temporarily replace only the new build helper's linker argument with this wrong target:

```go
args = append(args, "-ldflags=-X=github.com/Mizore66/faultline/internal/buildinfo.NotVersion="+stamp)
```

Run: `go test -count=1 -timeout 210s ./difftest/release -run '^TestCLIHelpVersionStamp$' -v`.
Expected red: the stamped subtest gets `FaultLine 0.1.4\n` rather than `FaultLine 0.0.0-slice5a-test\n`, even if the linker silently accepts the missing target. Record this as an expected assertion failure. Restore precisely the correct argument below; do not retain the mutation or alter production/default fixtures:

```go
args = append(args, "-ldflags=-X=github.com/Mizore66/faultline/internal/buildinfo.Version="+stamp)
```

Run the same test again. Expected green: both aliases in every default/stamped deployment case match the exact intended bytes. This is test-strength evidence, not a production bug fix.

- [x] **Step 4: Run native local green checks and inspect exact output.** After gofmt, run each separately:

```sh
go test -count=1 -timeout 210s ./difftest/release -run '^TestCLIHelpStandalone$' -v
go test -count=1 -timeout 210s ./difftest/release -run '^TestCLIHelpVersionStamp$' -v
go test -tags faultline_test -count=1 -timeout 210s ./difftest/release -run '^TestCLIHelp(Standalone|VersionStamp)$' -v
go vet ./difftest/release
go vet -tags faultline_test ./difftest/release
```

If the combined tagged run exceeds its bounded budget, preserve that attempt and run the two named tests in separate invocations; do not reset its watchdog or call an incomplete parent a pass. This is an explicit budget partition after diagnosis, not a silent retry.

The existing six-runner `go test ./...` and tagged pass discover this untagged test file automatically. Do not add its tests to any skip allowlist or to the `TestLive*` namespace. Record actual GOOS/GOARCH for local results; the other five targets remain pending until their native hosted jobs execute.

- [x] **Step 5: Review this task.** Confirm default/stamped results, UTF-8/LF behavior, minimal environment, Windows portability, no protected writes and no new runtime version escape hatch. Proposed separately authorized commit subject: `test(go): verify standalone help and linker-stamped versions`.

### Task 4: Wire live comparison, CI regeneration and evidence closure

**Files:**
- Create: `difftest/help_live_test.go`
- Modify: `.github/workflows/go.yml:109-126` (new generator/test before no-drift)
- Modify: `.github/workflows/go.yml` main-live comment only if useful; discovery itself is unchanged
- Modify when justified: overview and 5a spec status lines; do not pre-mark hosted acceptance complete

**Interfaces:**
- Consumes: Task 2 `loadCLIHelpCases`, `runCLIHelpProcess`, `flBinary`; existing `oracle.Skip(t)` and `oracle.RepoRoot(t)`; actual Node 22.22.2 and current-tree TS CLI.
- Produces: `TestLiveCLIHelp`, automatically selected by existing main `TestLive*` discovery, and deterministic golden-generation wiring. No new job/skip/normalization mechanism.

- [x] **Step 1: Add the live preflight and direct comparison.** Create:

```go
//go:build faultline_test

package difftest

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"

    "github.com/Mizore66/faultline/difftest/oracle"
)

func TestLiveCLIHelp(t *testing.T) {
    oracle.Skip(t)
    rows := loadCLIHelpCases(t)
    repo := oracle.RepoRoot(t)
    node, err := exec.LookPath("node")
    if err != nil { t.Fatal(err) }
    outside, env := t.TempDir(), os.Environ()
    stdout, stderr, code := runCLIHelpProcess(t, node, outside, env, "--version")
    if stdout != "v22.22.2\n" || stderr != "" || code != 0 {
        t.Fatalf("oracle Node drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
    }
    raw, err := os.ReadFile(filepath.Join(repo, "package.json"))
    if err != nil { t.Fatal(err) }
    var manifest struct{ Version any `json:"version"` }
    if err := json.Unmarshal(raw, &manifest); err != nil { t.Fatal(err) }
    if manifest.Version != "0.1.4" { t.Fatalf("oracle manifest drift: %#v", manifest.Version) }
    cli := filepath.Join(repo, "dist", "cli.js")
    stdout, stderr, code = runCLIHelpProcess(t, node, outside, env, cli, "--version")
    if stdout != "FaultLine 0.1.4\n" || stderr != "" || code != 0 {
        t.Fatalf("oracle CLI-version drift: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
    }
    for _, row := range rows {
        t.Run(row.Case, func(t *testing.T) {
            tsOut, tsErr, tsCode := runCLIHelpProcess(t, node, outside, env, append([]string{cli}, row.Args...)...)
            if tsOut != row.Stdout || tsErr != row.Stderr || tsCode != row.Exit {
                t.Fatalf("TS oracle drift: stdout=%q want=%q stderr=%q want=%q exit=%d want=%d",
                    tsOut, row.Stdout, tsErr, row.Stderr, tsCode, row.Exit)
            }
            goOut, goErr, goCode := runCLIHelpProcess(t, flBinary, outside, env, row.Args...)
            if goOut != tsOut || goErr != tsErr || goCode != tsCode {
                t.Fatalf("Go/TS: stdout=%q want=%q stderr=%q want=%q exit=%d want=%d",
                    goOut, tsOut, goErr, tsErr, goCode, tsCode)
            }
        })
    }
}
```

A non-oracle run retains the existing expected `TestLive*` skip policy. With `FAULTLINE_NODE_ORACLE=1`, absent/wrong Node, bad/missing manifest and wrong version output fail, not skip. No oracle adapter/server or real provider is started. Baseline metadata is validated before preflight; exact declared values are not selected dynamically from a changed manifest.

- [x] **Step 2: Exercise preflight controls and the passing live path.** Use Task 1's pure preflight negative tests to prove refusal of 0.1.3/wrong Node/version bytes without touching protected files. Run the actual live test only with a verified pinned runtime and freshly built current-tree TS:

```sh
env FAULTLINE_NODE_ORACLE=1 go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^TestLiveCLIHelp$' -v
```

Expected green: preflight and every row compare TS, captured evidence and Go exactly. On the historical Go-less image, use separately cross-built unstamped `fl` and difftest test executable with the existing `FAULTLINE_FL_BINARY` contract; invoke the executable with `-test.run '^TestLiveCLIHelp$' -test.timeout 210s -test.v`, not the entire multi-hour suite. Mount only this plan's new evidence outputs writable; copy the current TS/gen inputs to a fresh container scratch directory and build there. Record that this is container-platform live evidence, not host-platform execution or six-platform completion.

- [x] **Step 3: Add the two CI commands at the existing no-drift anchor.** Preserve every existing generator and the final status/diff/no-drift checks. The end of the `Regenerate goldens and require no drift` block becomes:

```yaml
          pnpm exec tsx difftest/gen/gen-corpus.ts
          pnpm exec tsx --test difftest/gen/cli-help-cases.test.ts
          pnpm exec tsx difftest/gen/gen-cli-help.ts
          git status --porcelain difftest/testdata
          git diff --stat difftest/testdata
          test -z "$(git status --porcelain difftest/testdata)"
```

Update the adjacent comment to mention CLI-help metadata/output. The existing main-live dynamic `TestLive*` list discovers `TestLiveCLIHelp`; do not add it to `elsewhere` or change the skip regex. The native matrix already runs replay and release tests on all six targets. Leave the untagged/tagged `FAULTLINE_TEST_OPENAI_URL` release guard unchanged; no new version override is allowed to bypass it.

- [x] **Step 4: Run bounded final local checks, then review the whole branch.** Run each separately:

```sh
go mod tidy -diff
go vet ./internal/buildinfo ./internal/cli ./difftest/release
go vet -tags faultline_test ./internal/buildinfo ./internal/cli ./difftest ./difftest/release
go test -count=1 -timeout 210s ./internal/cli -run '^(TestCLIHelpFastPaths|TestFailures|TestExecutionPolicyHint)$' -v
go test -tags faultline_test -count=1 -timeout 210s ./internal/cli -run '^(TestCLIHelpFastPaths|TestFailures|TestExecutionPolicyHint)$' -v
go test -tags faultline_test -count=1 -timeout 210s ./difftest -run '^(TestCLIHelpBaseline|TestCLIHelpReplay|TestGoldensReachEveryErrorLiteral|TestUnreachableSitesTable)$' -v
git diff --check
git diff -- src/ package.json pnpm-lock.yaml
```

Run Task 3's release tests and the pinned live/regeneration checks using their separate bounded invocations, not appended to one shell. Compare every pre-existing golden with the execution base; only the new CLI-help directory is additive. Verify the generator and loader use the same five metadata fields and 67 case IDs; inspect all process stderr/exit assertions. Native full suites, full error/positional coverage, full existing regeneration and existing release guard remain the hosted workflow gates. Do not expand a local cap to run them.

The whole-branch review must inspect the actual uncommitted diff and new files if commits remain unauthorized. A BASE..HEAD-only review would miss this work. Record spec compliance and quality findings, verified fixes and residual decisions without treating review as human consent. Proposed separately authorized final commit subject: `test(go): enforce live CLI-help parity and CI regeneration`.

- [x] **Step 5: Close only the evidence that exists.** Update the overview/spec to distinguish implementation/local verification, pending publication authorization and exact-head hosted acceptance. Final 5a acceptance requires six native jobs, pinned generation/no-drift, `TestLiveCLIHelp`, permanent replay, existing coverage/error gates and the unchanged release no-test-override guard. Record workflow run, event, attempt, head SHA, containing-job outcome and allowed skips. Do not reuse Slice 4's 54-check receipt as 5a evidence.

If push/PR/CI publication remains unauthorized, leave the branch and evidence intact and report hosted acceptance pending. Do not mark 5a complete under that condition. A later explicit publication authorization must preserve Mizore66 SSH/API checks, normal TruffleHog behavior and attribution; no merge/ready/release authorization follows from it.

## Appendix A: Exact embedded text for `internal/cli/help.go`

This is source text copied from `src/cli-help.ts` at the verified frozen source baseline. Both usage constants exclude the appended LF; `quickstart` already includes its final LF. Keep these literals and the selector from Task 2 in one `package cli` file. The original Node/pnpm instructions are intentionally retained for later 5j review.

```go
package cli

const usage = `FaultLine — freeze one reviewed witness; prove only what executions support

Quickstart (judges / first look):
  fl doctor [--proof-ready] [--security]
  fl judge-proof [--export-only]          # verified COMMIT_PROOF sample (no Docker / no API key)
  fl quickstart                          # print the recommended next commands
  fl ui [--repo <directory>] [--port <number>]
  fl --version

Common product commands:
  fl init [--yes]
  fl investigate --ci-log <file> --repo <directory> [...]
  fl investigate git ...
  fl investigate turns ...
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl witness propose|review|approve|freeze ...
  fl repair [--with-codex] | fl repair brief --live ...
  fl prevention write|verify ...

More:
  fl advanced                            # full command reference
  fl help advanced

Notes:
  fl doctor exits 0 for local CLI readiness; fl doctor --proof-ready exits nonzero unless Docker proof-grade preflight is READY.
  fl doctor --security runs a live hardened-Git + fixture self-test (≥5 PASS: hooks / protocol.allow=never / filters / overlay traversal / secret blob).
  Headless judges: FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only
  GPT-5.6 samples (no key): docs/samples/gpt-5.6/
  The judge demo/fixture paths do not require an OpenAI API key.`

const advancedUsage = `FaultLine — full command reference

Usage:
  fl judge-demo [--replay | --rerun-all] [--output <managed-bundle-directory>] [--export-only]
  fl judge-proof [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--export-only] [--port <number>]
  fl commit-proof-preview [--bundle <git-proof-bundle-directory>] [--expect-root <sha256:...>] [--output <static-preview.html>]
  fl --version
  fl judge-preview [--output <static-preview.html>]
  fl doctor [--repo <directory>] [--json] [--proof-ready] [--security]
  fl quickstart
  fl ui [--repo <directory>] [--port <number>]
  fl init [--repo <directory>] [--cli <built-cli.js>] [--runtime <node|python|go>] [--yes]
  fl incident suggest --repo <directory>
  fl incident start --repo <directory> (--command <failing-command> | --command-file <utf8-file>) [--id <safe-id>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--store <directory>]
  fl incident status <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--expect-digest <sha256:...>]
  fl incident continue <id> [--repo <directory>] [--store <directory>] [--draft-store <directory>] [--image <digest-pinned-image>] [--expect-digest <sha256:...>] [--ledger <ledger.json>] [--max-states <count>] [--output <managed-bundle-directory>]
  fl investigate --ci-log <file> --repo <directory> [--command <failing-command>] [--from <commit> --to <commit>] [--runtime <node|python|go> | --image <digest-pinned-image>] [--id <safe-id>]
  fl investigate --resume <incident-id> --repo <directory> [--expect-digest <sha256:...>] [--runtime <node|python|go> | --image <digest-pinned-image>]
  fl tutorial [--repo <directory>] [--yes]
  fl investigate turns --repo <directory> (--ledger <ledger.json> | --latest) --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--output <managed-bundle-directory>] [--minimize] [--transition <index>] [--max-executions <count>]
  fl prove transition <turn-proof-bundle-directory> --repo <directory> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--transition <index>] [--max-executions <count>] [--output <managed-result.json>]
  fl investigate git --repo <directory> --from <commit> --to <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--runtime-mapping <mapping.json>] [--ledger <ledger.json>] [--output <managed-bundle-directory>]
  fl runtime mapping write --fingerprint <sha256:...> --image <digest-pinned-image> [--fingerprint <sha256:...> --image <digest-pinned-image> ...] --output <mapping.json>
  fl runtime resolve <node|python|go>
  fl runtime prepare <node|python|go> --yes
  fl runtime project <plan|build|resolve> [--context <directory>] [--dockerfile <file>] --tag <repository:tag> [--network <none|default>] [--yes]
  fl demo live-git|full [--image <digest-pinned-image>] [--export-only] [--port <number>]
  fl verify <proof-bundle-directory> [--expect-root <sha256:...>]
  fl serve [--port <number>]
  fl serve --bundle <git-proof-bundle-directory> [--expect-root <sha256:...>] [--minimization <result.json> --expect-minimization <sha256:...>] [--repair <repair-brief-directory> --expect-repair <sha256:...>] [--prevention <prevention-proof-directory> --expect-prevention <sha256:...>] [--port <number>]
  fl codex --dry-run | --snapshot [--repo <directory>]
  fl codex snapshot gc [--repo <directory>] [--force]
  fl codex record <init|stdin|checkpoint|verify> [...]
  fl codex sidecar config (--cli <built-cli.js> | --command <hook-command> [--command-windows <hook-command>])
  fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes
  fl codex sidecar hook [--input <hook.json>] [--quiet]
  fl codex sidecar status [--repo <directory>] [--session <session-id>]
  fl record <init|stdin|checkpoint|attach|verify> [...]
  fl minimize git --repo <directory> --before <commit> --after <commit> --proposal <id> --expect-digest <sha256:...> --image <digest-pinned-image> [--max-executions <count>] [--output <managed-result.json>]
  fl minimize verify <result.json> [--expect-digest <sha256:...>]
  fl ledger bind --ledger <ledger.json> --investigation <investigation.json> --output <binding.json>
  fl ledger verify <binding.json> [--expect-digest <sha256:...>]
  fl attest create --bundle <proof-bundle-directory> --receipt <id> --subject <label> --issuer <label> [--store <directory>]
  fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]
  fl provenance create --bundle <git-proof-bundle-directory> [--output <managed-receipt.json>]
  fl provenance verify --bundle <git-proof-bundle-directory> --receipt <ci-receipt.json> --attestation-bundle <sigstore-bundle.json> --trust <trust.json>
  fl repair --bundle <git-proof-bundle-directory> --expect-root <sha256:...> [--repo <directory>] [--output <directory>] [--with-codex] [--instructions-only] [--keep-worktree]
  fl repair brief (--bundle <git-proof-bundle-directory> | --investigation <verified-bundle>/investigation.json) (--live | --input <repair-brief.json>) [--expect-root <sha256:...>] [--model <model>] [--output <managed-directory>]
  fl repair verify <repair-brief-directory> [--expect-digest <sha256:...>]
  fl prevention write --input <prevention-input.json> [--output <managed-directory>]
  fl prevention write --from-bundle <git-proof-bundle> --expect-root <sha256:...> --patch <repair.patch> --repaired-commit <id> --repaired-tree <id> --repaired-runs <runs.json> [--output <managed-directory>] [--codex-thread-id <id>]
  fl prevention verify <prevention-proof-directory> [--expect-root <sha256:...>]
  fl witness propose --input <proposal.json> [--store <directory>]
  fl witness propose --live --incident <incident.json> --proposal-id <id> --overlay-root <directory> [--model <model>] [--store <directory>]
  fl witness implement --incident <incident.json> --proposal-id <id> --overlay-out <directory> [--repo <directory>] [--with-codex] [--behavior <text>]
  fl witness review <proposal-id> [--json | --port <number>] [--store <directory>] [--draft-store <directory>]
  fl witness approve <proposal-id> --approved-by <actor> [--store <directory>]
  fl witness freeze <proposal-id> [--repo <directory>] [--draft-store <directory>] [--store <directory>]
  fl witness sign <proposal-id> --private-key <ed25519-private.pem> --keyring <trusted-reviewers.json> [--store <directory>]
  fl witness verify <proposal-id> [--expect-digest <sha256:...>] [--keyring <trusted-reviewers.json> --require-signature] [--store <directory>]

The judge demo is a reviewed, deterministic Node fixture. It does not require an OpenAI API key.
The lifecycle adapter accepts observed Codex-compatible events; it does not claim to intercept private Codex internals.
Evidence outputs are intentionally confined to their managed .faultline roots; --output selects a child of that root rather than an arbitrary directory.
Use --live for a GPT-5.6 witness proposal or an inferred repair brief after setting OPENAI_API_KEY.
fl doctor exits 0 when Node can run the local CLI; fl doctor --proof-ready exits nonzero unless Docker proof-grade preflight is READY.`

const quickstart = `FaultLine quickstart

1) Local readiness (Docker-less judges stay on the sample path):
   fl doctor
   fl doctor --proof-ready

2) Verified COMMIT_PROOF sample (no Docker, no API key):
   fl judge-proof --export-only
   fl judge-proof

3) Inspect GPT-5.6 response shapes without a key:
   docs/samples/gpt-5.6/

4) Live paths (optional):
   OPENAI_API_KEY=… fl witness propose --live …
   OPENAI_API_KEY=… fl repair brief --live …
   Docker required: fl demo full   # or: fl demo live-git --export-only

Pinned submission checkout: git checkout v0.1.10-buildweek
Full command list: fl advanced
`
```

## Plan self-review and handoff

Before offering this plan, the controller must verify the following from the plan text itself, without running the proposed implementation:

- [x] Spec coverage: scope/argv/bytes/embedded metadata → Tasks 1–2; standalone/stamping → Task 3; permanent/live/no-drift/native coverage → Tasks 2–4; later Slice 5 boundaries → Global Constraints and final acceptance.
- [x] Placeholder scan: no incomplete code bodies, unresolved function names or speculative existing helpers. The exact `[...]`/`...` in frozen public help are legitimate text, not planning placeholders. Optional local-container setup remains subject to the explicit pinned-runtime/owned-cleanup prerequisites, not substituted with an unpinned oracle.
- [x] Type/interface consistency: TS `{case,args,stdout,stderr,exit}` agrees with all Go decoders; 67 rows agree everywhere; shared helper signatures are defined in Task 2, Task 3 or verified existing sources. Corrected the literal-waiver check to `TestGoldensReachEveryErrorLiteral`, rather than misattributing it to the separate positional-table test.
- [x] Compared Appendix A's three raw strings byte-for-byte with frozen source after correcting the self-check findings. Exact text, not line counts, is the criterion; the initial failed check remains in the recovery record.
- [x] Mechanical docs checks: `IMPLEMENTATION_PLAN_DOC_CHECKS_PASS` verified nine local links, one heading anchor, whitespace/final newline, exactly three documentation paths changed, protected paths unchanged and no staged files/new commits. These are documentation checks, not runtime test results.

After this self-review, present the saved plan and recommend **Sonnet subagent-driven execution**, one implementer at a time with task review and final whole-branch review. Inline execution is the alternative. Wait for the user's choice/plan approval and production authorization before implementation; no commit/push is included in that choice.
