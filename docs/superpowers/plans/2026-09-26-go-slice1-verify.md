# Go Slice 1 (foundation, differential harness, `fl verify`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go `fl verify` that produces byte-identical stdout, stderr, and exit codes to frozen TS `node dist/cli.js verify` for demo, Git, and prevention proof bundles, proven by a committed golden corpus.

**Architecture:** Leaf packages reproduce JavaScript semantics (`jsstr` strings, `jsjson` JSON.parse/stringify with V8 error text, `canonical` digests with `localeCompare` ordering, `nodefs` Node file reading and error text). A zod-subset `schema` package reproduces zod v3 parsing, normalization, and issue messages. Three verifier packages are line-by-line ports of the TS verifiers. `difftest` generates goldens from frozen TS and replays every case against Go.

**Tech Stack:** Go 1.27, `golang.org/x/text` v0.42.0 (collation), TypeScript generators run with the repo's existing `tsx`, Node 22, git.

**Spec:** [docs/superpowers/specs/2026-09-25-go-slice1-verify-design.md](../specs/2026-09-25-go-slice1-verify-design.md) (parent: [overview](../specs/2026-09-25-go-migration-overview.md)). Read both before starting any task.

## Global Constraints

- Go 1.27; module `github.com/Mizore66/faultline`; only dependency `golang.org/x/text` v0.42.0.
- Never use `encoding/json` on any value that is hashed, compared canonically, or printed. Use `internal/jsjson`.
- Frozen TS (`src/`, `package.json`, `pnpm-lock.yaml`) is never modified. The TS oracle is `node dist/cli.js`, built with `pnpm build`.
- All Go strings that hold JS values are WTF-8 (see `internal/jsstr`). Convert with `jsstr.ToUTF8` before writing to stdout/stderr or hashing a JS string.
- Output must match TS byte for byte except the four normalizations in spec §4.5.
- Every ported Go function carries a comment naming its TS source, e.g. `// Port of src/proof-bundle.ts:439 verifyProofBundle.`
- Commits are authored as Mizore66 (repo-local git config is already set). End every commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Pin GitHub Actions by commit SHA like the existing workflows: `actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5`, `actions/setup-node@a0853c24544627f65ddf259abe73b1d18a591444 # v5`, `pnpm/action-setup@b0f76dfb45f55f8421693e4803ac7bb65143bd34 # v6`, `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7`.

## File Structure

```
go.mod, go.sum
cmd/fl/main.go                          entry point → cli.Run
internal/jsstr/jsstr.go                 WTF-8 ⇄ UTF-16, JS length/trim/whitespace, ToUTF8
internal/jsjson/value.go                Value, Object (JS property order)
internal/jsjson/parse.go                JSON.parse clone (success paths)
internal/jsjson/errors.go               V8 12.4 SyntaxError messages
internal/jsjson/number.go               ECMAScript Number::toString
internal/jsjson/stringify.go            JSON.stringify (compact + indent)
internal/canonical/collate.go           localeCompare (en-US) comparator
internal/canonical/canonical.go         CanonicalJSON, SHA256Hex, DigestJSON
internal/nodefs/decode.go               WHATWG UTF-8 decoding (readFileSync "utf8")
internal/nodefs/fs.go                   ReadText/ReadBytes/Lstat/ReadDir/Exists + Node error text
internal/schema/*.go                    zod v3 subset: combinators, issues, messages
internal/bundle/demo/*.go               faultline.proof-bundle.v2 verifier
internal/bundle/prevention/*.go         faultline.prevention-proof.v1 verifier
internal/bundle/gitproof/*.go           faultline.git-proof-bundle.v1 verifier + dependency ports
internal/cli/*.go                       Run, args, failure formatting, verify command
difftest/gen/*.ts                       TS generators + oracle (run with tsx)
difftest/oracle/client.go               Go client for node-oracle.ts
difftest/*_test.go                      golden replay + live property tests
difftest/testdata/                      bases/, mutations.json, golden/
difftest/KNOWN_DIFFERENCES.md, difftest/BENCHMARK.md
.github/workflows/go.yml                go + difftest-live jobs
```

---

### Task 1: Module scaffold, JS string semantics, CLI shell, Go CI

**Files:**
- Create: `go.mod`, `cmd/fl/main.go`, `internal/jsstr/jsstr.go`, `internal/jsstr/jsstr_test.go`, `internal/cli/cli.go`, `internal/cli/args.go`, `internal/cli/failure.go`, `internal/cli/verify.go`, `internal/cli/cli_test.go`, `.github/workflows/go.yml`
- Modify: `.gitattributes` (append one line)

**Interfaces:**
- Produces: `jsstr.FromUTF16([]uint16) string`, `jsstr.ToUTF16(string) []uint16`, `jsstr.Length(string) int`, `jsstr.ToUTF8(string) string`, `jsstr.IsWhitespace(uint16) bool`, `jsstr.Trim(string) string`; `cli.Run(args []string, stdout, stderr io.Writer) int`; in package `cli`: `type env struct{ stdout, stderr io.Writer; exitCode int }`, `func (e *env) out(s string)`, `func fail(format string, a ...any) error`, `func hasFlag([]string, string) bool`, `func option([]string, string) (string, bool)`, `func verifyCommand(e *env, args []string) error`.

- [x] **Step 1: Create the module and entry point**

```bash
go mod init github.com/Mizore66/faultline
go mod edit -go=1.27
```

`cmd/fl/main.go`:

```go
package main

import (
	"os"

	"github.com/Mizore66/faultline/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

Append to `.gitattributes` (golden bundles must keep exact bytes on Windows):

```
difftest/testdata/** -text
```

- [x] **Step 2: Write failing jsstr tests**

`internal/jsstr/jsstr_test.go`:

```go
package jsstr

import (
	"slices"
	"testing"
)

func TestRoundTripKeepsLoneSurrogates(t *testing.T) {
	units := []uint16{'a', 0xD800, 'b', 0xDC00, 0xD83D, 0xDE00}
	s := FromUTF16(units)
	if got := ToUTF16(s); !slices.Equal(got, units) {
		t.Fatalf("round trip = %x, want %x", got, units)
	}
	if Length(s) != 6 {
		t.Fatalf("Length = %d, want 6", Length(s))
	}
}

func TestToUTF8ReplacesLoneSurrogates(t *testing.T) {
	s := FromUTF16([]uint16{'a', 0xD800, 0xD83D, 0xDE00})
	if got, want := ToUTF8(s), "a\ufffd\U0001F600"; got != want {
		t.Fatalf("ToUTF8 = %q, want %q", got, want)
	}
}

func TestToUTF8JoinsSeparatelyEncodedPair(t *testing.T) {
	// JS concatenation of a lone high and a lone low surrogate forms a pair.
	s := FromUTF16([]uint16{0xD83D}) + FromUTF16([]uint16{0xDE00})
	if got := ToUTF8(s); got != "\U0001F600" {
		t.Fatalf("ToUTF8 = %q", got)
	}
}

func TestTrimMatchesJavaScript(t *testing.T) {
	in := "\ufeff\u00a0\u2028 \t x y \r\n\u3000\u200a"
	if got := Trim(in); got != "x y" {
		t.Fatalf("Trim = %q", got)
	}
	if Trim("\u0085x") != "\u0085x" { // NEL is not JS whitespace
		t.Fatal("NEL must not be trimmed")
	}
}
```

- [x] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/jsstr/`
Expected: FAIL (build error: undefined: FromUTF16).

- [x] **Step 4: Implement jsstr**

`internal/jsstr/jsstr.go`:

```go
// Package jsstr implements JavaScript string semantics on Go strings.
//
// Strings that hold JS values are WTF-8: UTF-8 that may additionally contain
// lone UTF-16 surrogates (U+D800–U+DFFF) encoded as three-byte sequences, so
// any JS string round-trips exactly.
package jsstr

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// FromUTF16 encodes JS code units as WTF-8, joining valid surrogate pairs.
func FromUTF16(units []uint16) string {
	var b strings.Builder
	b.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) {
			if lo := rune(units[i+1]); lo >= 0xDC00 && lo <= 0xDFFF {
				b.WriteRune(utf16.DecodeRune(u, lo))
				i++
				continue
			}
		}
		if u >= 0xD800 && u <= 0xDFFF {
			b.WriteByte(byte(0xE0 | u>>12))
			b.WriteByte(byte(0x80 | (u>>6)&0x3F))
			b.WriteByte(byte(0x80 | u&0x3F))
			continue
		}
		b.WriteRune(u)
	}
	return b.String()
}

// ToUTF16 decodes WTF-8 into JS code units.
func ToUTF16(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		if len(s)-i >= 3 && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			units = append(units, uint16(0xD000|rune(s[i+1]&0x3F)<<6|rune(s[i+2]&0x3F)))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			units = append(units, uint16(hi), uint16(lo))
			continue
		}
		units = append(units, uint16(r))
	}
	return units
}

// Length is JS `string.length`.
func Length(s string) int { return len(ToUTF16(s)) }

// ToUTF8 is `Buffer.from(s, "utf8")` / `process.stdout.write(s)`:
// lone surrogates become U+FFFD.
func ToUTF8(s string) string {
	if !strings.Contains(s, "\xED") {
		return s
	}
	return string(utf16.Decode(ToUTF16(s)))
}

// IsWhitespace reports ECMAScript WhiteSpace or LineTerminator (the set
// String.prototype.trim and the regex class \s use).
func IsWhitespace(u uint16) bool {
	switch u {
	case 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return u >= 0x2000 && u <= 0x200A
}

// Trim is JS `String.prototype.trim`.
func Trim(s string) string {
	units := ToUTF16(s)
	start, end := 0, len(units)
	for start < end && IsWhitespace(units[start]) {
		start++
	}
	for end > start && IsWhitespace(units[end-1]) {
		end--
	}
	return FromUTF16(units[start:end])
}
```

- [x] **Step 5: Run jsstr tests**

Run: `go test ./internal/jsstr/`
Expected: PASS.

- [x] **Step 6: Write failing CLI tests**

The expected strings below were captured from `node dist/cli.js` at `faa98c0`.

`internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"testing"
)

func run(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestFailures(t *testing.T) {
	cases := []struct {
		args   []string
		stderr string
	}{
		{[]string{"verify"}, "FaultLine error: Usage: fl verify <proof-bundle-directory>\nNext: pnpm fl help\n"},
		{[]string{"verify", ""}, "FaultLine error: Usage: fl verify <proof-bundle-directory>\nNext: pnpm fl help\n"},
		{[]string{"nope"}, "FaultLine error: Unknown command: nope\nNext: pnpm fl help\n"},
		{[]string{"--x"}, "FaultLine error: Unknown command: --x\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help\n"},
		{[]string{"--"}, "FaultLine error: Unknown command: --\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help\n"},
		{[]string{"doctor"}, "FaultLine error: not yet ported: doctor\nNext: pnpm fl help\n"},
	}
	for _, c := range cases {
		stdout, stderr, code := run(c.args...)
		if code != 1 || stdout != "" || stderr != c.stderr {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", c.args, code, stdout, stderr)
		}
	}
}

func TestExecutionPolicyHint(t *testing.T) {
	got := formatFailure("running scripts is DISABLED on this system")
	want := "FaultLine error: running scripts is DISABLED on this system\n\n" +
		"Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:\n" +
		"  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process\n" +
		"Or invoke the command proxy directly:\n" +
		"  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)\n" +
		"Next: pnpm fl help"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestOption(t *testing.T) {
	args := []string{"dir", "--expect-root", "sha256:x", "--expect-root", "second"}
	if v, ok := option(args, "--expect-root"); !ok || v != "sha256:x" {
		t.Fatalf("option = %q, %v", v, ok)
	}
	if _, ok := option([]string{"dir", "--expect-root"}, "--expect-root"); ok {
		t.Fatal("a trailing flag has no value")
	}
	if !hasFlag(args, "--expect-root") || hasFlag(args, "--nope") {
		t.Fatal("hasFlag")
	}
}
```

- [x] **Step 7: Run to verify failure**

Run: `go test ./internal/cli/`
Expected: FAIL (undefined: Run).

- [x] **Step 8: Implement the CLI shell**

`internal/cli/args.go`:

```go
package cli

import "slices"

// hasFlag ports src/cli-app.ts:140.
func hasFlag(args []string, flag string) bool { return slices.Contains(args, flag) }

// option ports src/cli-app.ts:144: the element after the first occurrence of
// flag. ok is false when the flag is absent or last (TS `undefined`).
func option(args []string, flag string) (value string, ok bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}
```

`internal/cli/failure.go`:

```go
package cli

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// formatFailure ports formatCliFailure in src/cli.ts.
func formatFailure(message string) string {
	windowsHint := strings.Join([]string{
		"",
		"Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:",
		"  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process",
		"Or invoke the command proxy directly:",
		"  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)",
	}, "\n")
	if unknownDashDash(message) || strings.HasPrefix(message, "Unknown command: --") {
		return "FaultLine error: " + message + "\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help"
	}
	if strings.HasPrefix(message, "Unknown command:") {
		return "FaultLine error: " + message + "\nNext: pnpm fl help"
	}
	lower := asciiLower(message)
	for _, needle := range []string{"executionpolicy", "running scripts is disabled", "pssecurityexception", "unauthorizedaccess"} {
		if strings.Contains(lower, needle) {
			return strings.Join([]string{"FaultLine error: " + message, windowsHint, "Next: pnpm fl help"}, "\n")
		}
	}
	return "FaultLine error: " + message + "\nNext: pnpm fl help"
}

// unknownDashDash is /^Unknown command:\s*--\b/: after optional JS
// whitespace, "--" must be followed by an ASCII word character.
func unknownDashDash(message string) bool {
	rest, ok := strings.CutPrefix(message, "Unknown command:")
	if !ok {
		return false
	}
	units := jsstr.ToUTF16(rest)
	i := 0
	for i < len(units) && jsstr.IsWhitespace(units[i]) {
		i++
	}
	if i+2 >= len(units) || units[i] != '-' || units[i+1] != '-' {
		return false
	}
	c := units[i+2]
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// asciiLower folds only ASCII letters, matching JS /i on these ASCII needles.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
```

`internal/cli/cli.go`:

```go
// Package cli is the Go port of src/cli.ts and src/cli-app.ts.
package cli

import (
	"fmt"
	"io"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// topLevelCommands is the runCli switch in src/cli-app.ts:2805-3066.
var topLevelCommands = map[string]bool{
	"help": true, "--help": true, "-h": true, "advanced": true, "quickstart": true,
	"--version": true, "-V": true, "judge-demo": true, "judge-proof": true,
	"commit-proof-preview": true, "judge-preview": true, "doctor": true, "ui": true,
	"init": true, "incident": true, "runtime": true, "tutorial": true, "demo": true,
	"verify": true, "serve": true, "codex": true, "witness": true, "record": true,
	"investigate": true, "prove": true, "minimize": true, "ledger": true,
	"attest": true, "provenance": true, "repair": true, "prevention": true,
}

type env struct {
	stdout, stderr io.Writer
	exitCode       int
}

// out is process.stdout.write: WTF-8 is encoded like Node encodes JS strings.
func (e *env) out(s string) { io.WriteString(e.stdout, jsstr.ToUTF8(s)) }

type cliError struct{ message string }

func (c *cliError) Error() string { return c.message }

func fail(format string, a ...any) error { return &cliError{fmt.Sprintf(format, a...)} }

// Run executes the fl command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	e := &env{stdout: stdout, stderr: stderr}
	if err := dispatch(e, args); err != nil {
		io.WriteString(stderr, jsstr.ToUTF8(formatFailure(err.Error())+"\n"))
		return 1
	}
	return e.exitCode
}

func dispatch(e *env, args []string) error {
	if len(args) == 0 {
		return fail("not yet ported: help")
	}
	command, rest := args[0], args[1:]
	switch {
	case command == "verify":
		return verifyCommand(e, rest)
	case topLevelCommands[command]:
		return fail("not yet ported: %s", command)
	default:
		return fail("Unknown command: %s", command)
	}
}
```

`internal/cli/verify.go`:

```go
package cli

// verifyCommand ports the "verify" case in src/cli-app.ts:2903.
func verifyCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fail("Usage: fl verify <proof-bundle-directory>")
	}
	return fail("not yet ported: verify")
}
```

- [x] **Step 9: Run all tests**

Run: `go vet ./... && go test ./...`
Expected: PASS.

- [x] **Step 10: Add the Go CI workflow**

`.github/workflows/go.yml`:

```yaml
name: Go

on:
  push:
  pull_request:

jobs:
  go:
    name: go test (${{ matrix.os }})
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7
        with:
          go-version: "1.27"
      - run: go vet ./...
      - run: go test ./...
```

- [x] **Step 11: Commit**

```bash
git add go.mod cmd internal .github/workflows/go.yml .gitattributes
git commit -m "feat(go): scaffold module, JS string semantics, and CLI shell

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Node oracle and Go client

The oracle lets Go tests ask frozen TS for ground truth. Later tasks add operations; this task builds the transport and `echo`.

**Files:**
- Create: `difftest/gen/node-oracle.ts`, `difftest/oracle/client.go`, `difftest/oracle/client_test.go`

**Interfaces:**
- Consumes: nothing from earlier Go tasks (the client transports JSON source text; decoding into `jsjson.Value` is added in Task 3).
- Produces: `oracle.Start(t testing.TB) *Client` (skips the test unless `FAULTLINE_NODE_ORACLE=1`), `(*Client).Call(op string, args string) (string, error)` where `args` and the returned string are JSON source text; `(*Client).Close()`. Node side: request line `{"id":N,"op":"...","args":<json>}`, response line `{"id":N,"ok":true,"result":<json>}` or `{"id":N,"ok":false,"error":"<message>"}`.

- [x] **Step 1: Write the oracle**

`difftest/gen/node-oracle.ts`:

```ts
// Long-running oracle over frozen TS. One JSON request per stdin line,
// one JSON response per stdout line. Run: pnpm exec tsx difftest/gen/node-oracle.ts
import { createInterface } from "node:readline";

type Handler = (args: any) => unknown;
export const handlers: Record<string, Handler> = {
  echo: (args) => args,
};

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", (line) => {
  let id: unknown = null;
  try {
    const request = JSON.parse(line) as { id: unknown; op: string; args: unknown };
    id = request.id;
    const handler = handlers[request.op];
    if (!handler) throw new Error(`unknown op: ${request.op}`);
    process.stdout.write(`${JSON.stringify({ id, ok: true, result: handler(request.args) })}\n`);
  } catch (error) {
    process.stdout.write(`${JSON.stringify({ id, ok: false, error: error instanceof Error ? error.message : String(error) })}\n`);
  }
});
```

- [x] **Step 2: Write the failing client test**

`difftest/oracle/client_test.go`:

```go
package oracle

import "testing"

func TestEchoRoundTripsLoneSurrogate(t *testing.T) {
	c := Start(t)
	defer c.Close()
	got, err := c.Call("echo", `{"s":"\ud800x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"s":"\ud800x"}` {
		t.Fatalf("echo = %s", got)
	}
}
```

- [x] **Step 3: Run to verify failure**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/oracle/`
Expected: FAIL (undefined: Start).

- [x] **Step 4: Implement the client**

The client finds the repo root by walking up to `go.mod`, starts `pnpm exec tsx difftest/gen/node-oracle.ts` there, and extracts `result` by scanning the response text so no Go JSON decoding is involved.

`difftest/oracle/client.go`:

```go
// Package oracle talks to difftest/gen/node-oracle.ts (frozen TS).
package oracle

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type Client struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

// RepoRoot returns the directory containing go.mod.
func RepoRoot(t testing.TB) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// Start launches the oracle; the test is skipped unless FAULTLINE_NODE_ORACLE=1.
func Start(t testing.TB) *Client {
	t.Helper()
	if os.Getenv("FAULTLINE_NODE_ORACLE") != "1" {
		t.Skip("set FAULTLINE_NODE_ORACLE=1 to run live oracle tests")
	}
	cmd := exec.Command("pnpm", "exec", "tsx", "difftest/gen/node-oracle.ts")
	cmd.Dir = RepoRoot(t)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &Client{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20)}
}

// Call sends op with args (JSON source text) and returns result as JSON source text.
func (c *Client) Call(op, args string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	prefix := fmt.Sprintf(`{"id":%d,`, c.nextID)
	if _, err := fmt.Fprintf(c.stdin, `%s"op":%q,"args":%s}`+"\n", prefix, op, args); err != nil {
		return "", err
	}
	line, err := c.stdout.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	if rest, ok := strings.CutPrefix(line, prefix+`"ok":true,"result":`); ok {
		return strings.TrimSuffix(rest, "}"), nil
	}
	if rest, ok := strings.CutPrefix(line, prefix+`"ok":false,"error":`); ok {
		return "", errors.New(strings.TrimSuffix(rest, "}"))
	}
	return "", fmt.Errorf("unexpected oracle response: %s", line)
}

func (c *Client) Close() {
	c.stdin.Close()
	c.cmd.Wait()
}
```

- [x] **Step 5: Run the test**

Run: `pnpm install --frozen-lockfile && FAULTLINE_NODE_ORACLE=1 go test ./difftest/oracle/`
Expected: PASS. Also run `go test ./difftest/oracle/` without the variable. Expected: `SKIP`.

- [x] **Step 6: Commit**

```bash
git add difftest/gen/node-oracle.ts difftest/oracle
git commit -m "test(difftest): add frozen-TS oracle process and Go client

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: `jsjson` — JSON.parse and JSON.stringify clone with V8 error text

**Files:**
- Create: `internal/jsjson/value.go`, `internal/jsjson/parse.go`, `internal/jsjson/errors.go`, `internal/jsjson/number.go`, `internal/jsjson/stringify.go`, `internal/jsjson/jsjson_test.go`, `difftest/gen/random.go`, `difftest/jsjson_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (add `parse`, `formatNumber` ops)

**Interfaces:**
- Consumes: `jsstr.FromUTF16`, `jsstr.ToUTF16`.
- Produces (package `jsjson`): `type Kind` with constants `Undefined` (zero value), `Null`, `Bool`, `Number`, `String`, `Array`, `Object`; `type Value` with `MakeNull()`, `MakeBool(bool)`, `MakeNumber(float64)`, `MakeString(string)`, `MakeArray([]Value)`, `MakeObject(*Obj)`, methods `Kind()`, `Bool()`, `Num()`, `Str()`, `Items()`, `Obj()`, `Get(keys ...string) Value` (JS property access; Undefined when any step is missing or not an object); `type Obj` with `NewObj()`, `Len()`, `Keys() []string` (JS property order), `Get(k) (Value, bool)`, `Field(k) Value` (Undefined when absent), `Set(k, v)`, `Delete(k)`; `type SyntaxError struct{ Message string }`; `Parse(text string) (Value, error)`; `FormatNumber(float64) string`; `Quote(string) string`; `Stringify(Value) string`; `StringifyIndent(Value, indent string) string`. Package `gen` (under `difftest/gen`, Go files beside the TS generators): `RandomJSONText(r *rand.Rand) string`, `RandomFloat(r *rand.Rand) float64`.

- [x] **Step 1: Write failing unit tests**

Every expected error string below was captured from Node v22.23.2.

`internal/jsjson/jsjson_test.go`:

```go
package jsjson

import (
	"math"
	"slices"
	"testing"
)

func TestParseErrorsMatchV8(t *testing.T) {
	cases := map[string]string{
		"":             "Unexpected end of JSON input",
		" ":            "Unexpected end of JSON input",
		"{":            "Expected property name or '}' in JSON at position 1 (line 1 column 2)",
		`{"a"`:         "Expected ':' after property name in JSON at position 4 (line 1 column 5)",
		`{"a":`:        "Unexpected end of JSON input",
		`{"a":1`:       "Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)",
		`{"a":1,`:      "Expected double-quoted property name in JSON at position 7 (line 1 column 8)",
		"[1,":          "Unexpected end of JSON input",
		"[1 2]":        "Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)",
		"{a:1}":        "Expected property name or '}' in JSON at position 1 (line 1 column 2)",
		"tru":          "Unexpected end of JSON input",
		"01":           "Unexpected number in JSON at position 1 (line 1 column 2)",
		"-":            "No number after minus sign in JSON at position 1 (line 1 column 2)",
		"1.":           "Unterminated fractional number in JSON at position 2 (line 1 column 3)",
		"1e":           "Exponent part is missing a number in JSON at position 2 (line 1 column 3)",
		`"abc`:         "Unterminated string in JSON at position 4 (line 1 column 5)",
		"\"a\x01\"":    "Bad control character in string literal in JSON at position 2 (line 1 column 3)",
		`"\x"`:         "Bad escaped character in JSON at position 2 (line 1 column 3)",
		`"\u12G4"`:     "Bad Unicode escape in JSON at position 5 (line 1 column 6)",
		"{} x":         "Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)",
		"\ufeff{}":     "Unexpected token '\ufeff', \"\ufeff{}\" is not valid JSON",
		`{"a":1}}`:     "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)",
		"x":            "Unexpected token 'x', \"x\" is not valid JSON",
		"xyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyz": "Unexpected token 'x', \"xyzxyzxyzx\"... is not valid JSON",
		"{\n  \"a\": 1\n  \"b\": 2\n}": "Expected ',' or '}' after property value in JSON at position 13 (line 3 column 3)",
		"é":            "Unexpected token 'é', \"é\" is not valid JSON",
		"NaN":          "\"NaN\" is not valid JSON",
		"[object Object]": "\"[object Object]\" is not valid JSON",
	}
	for text, want := range cases {
		_, err := Parse(text)
		if err == nil || err.Error() != want {
			t.Errorf("Parse(%q) error = %v, want %q", text, err, want)
		}
	}
}

func TestObjectPropertyOrder(t *testing.T) {
	v, err := Parse(`{"b":1,"1":2,"a":3,"0":4,"01":5,"4294967295":6,"4294967294":7,"b":8}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0", "1", "4294967294", "b", "a", "01", "4294967295"}
	if got := v.Obj().Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	if b, _ := v.Obj().Get("b"); b.Num() != 8 {
		t.Fatal("duplicate key: last value wins")
	}
}

func TestNumbers(t *testing.T) {
	cases := map[float64]string{
		0: "0", math.Copysign(0, -1): "0", 1e21: "1e+21", 1e20: "100000000000000000000",
		123456789012345680000: "123456789012345680000", 1e-7: "1e-7", 1.5e-7: "1.5e-7",
		0.000001: "0.000001", 0.1 + 0.2: "0.30000000000000004", 5e-324: "5e-324", -1.5: "-1.5",
	}
	for f, want := range cases {
		if got := FormatNumber(f); got != want {
			t.Errorf("FormatNumber(%v) = %q, want %q", f, got, want)
		}
	}
	v, _ := Parse("1e400")
	if !math.IsInf(v.Num(), 1) {
		t.Fatal("1e400 must parse to +Infinity like V8")
	}
}

func TestStringify(t *testing.T) {
	v, err := Parse(`{"s":" <>&\u0001\ud800\u2028","a":[],"o":{},"n":[1,{"x":null}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Stringify(v), "{\"s\":\" <>&\\u0001\\ud800\u2028\",\"a\":[],\"o\":{},\"n\":[1,{\"x\":null}]}"; got != want {
		t.Fatalf("Stringify = %q", got)
	}
	want := "{\n  \"s\": \" <>&\\u0001\\ud800\u2028\",\n  \"a\": [],\n  \"o\": {},\n  \"n\": [\n    1,\n    {\n      \"x\": null\n    }\n  ]\n}"
	if got := StringifyIndent(v, "  "); got != want {
		t.Fatalf("StringifyIndent = %q", got)
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/jsjson/`
Expected: FAIL (undefined: Parse).

- [x] **Step 3: Implement the value model**

`internal/jsjson/value.go`:

```go
// Package jsjson reproduces JSON.parse and JSON.stringify as V8 12.4 (Node 22)
// implements them. Strings are WTF-8 (see internal/jsstr).
package jsjson

import (
	"cmp"
	"slices"
	"strconv"
)

type Kind uint8

const (
	Undefined Kind = iota // never produced by Parse; models absent JS values
	Null
	Bool
	Number
	String
	Array
	Object
)

type Value struct {
	kind Kind
	b    bool
	n    float64
	s    string
	arr  []Value
	obj  *Obj
}

func MakeNull() Value               { return Value{kind: Null} }
func MakeBool(b bool) Value         { return Value{kind: Bool, b: b} }
func MakeNumber(n float64) Value    { return Value{kind: Number, n: n} }
func MakeString(s string) Value     { return Value{kind: String, s: s} }
func MakeArray(items []Value) Value { return Value{kind: Array, arr: items} }
func MakeObject(o *Obj) Value       { return Value{kind: Object, obj: o} }

func (v Value) Kind() Kind     { return v.kind }
func (v Value) Bool() bool     { return v.b }
func (v Value) Num() float64   { return v.n }
func (v Value) Str() string    { return v.s }
func (v Value) Items() []Value { return v.arr }
func (v Value) Obj() *Obj      { return v.obj }

// Get follows object keys like JS property access; a missing step or a
// non-object yields Undefined.
func (v Value) Get(keys ...string) Value {
	for _, k := range keys {
		if v.kind != Object {
			return Value{}
		}
		v = v.obj.values[k]
	}
	return v
}

// Obj keeps ordinary JS property order: array-index keys in ascending numeric
// order, then the remaining keys in first-insertion order.
type Obj struct {
	keys   []string
	values map[string]Value
	nIndex int // leading keys that are array indices
}

func NewObj() *Obj { return &Obj{values: map[string]Value{}} }

func (o *Obj) Len() int                   { return len(o.keys) }
func (o *Obj) Keys() []string             { return slices.Clone(o.keys) }
func (o *Obj) Get(k string) (Value, bool) { v, ok := o.values[k]; return v, ok }
func (o *Obj) Field(k string) Value       { return o.values[k] }

func (o *Obj) Set(k string, v Value) {
	if _, ok := o.values[k]; ok {
		o.values[k] = v
		return
	}
	o.values[k] = v
	idx, ok := arrayIndex(k)
	if !ok {
		o.keys = append(o.keys, k)
		return
	}
	pos, _ := slices.BinarySearchFunc(o.keys[:o.nIndex], idx, func(key string, target uint32) int {
		n, _ := arrayIndex(key)
		return cmp.Compare(n, target)
	})
	o.keys = slices.Insert(o.keys, pos, k)
	o.nIndex++
}

func (o *Obj) Delete(k string) {
	if _, ok := o.values[k]; !ok {
		return
	}
	delete(o.values, k)
	i := slices.Index(o.keys, k)
	o.keys = slices.Delete(o.keys, i, i+1)
	if i < o.nIndex {
		o.nIndex--
	}
}

// arrayIndex reports whether k is a canonical array index, 0 … 2^32−2.
func arrayIndex(k string) (uint32, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	if err != nil || n > 4294967294 {
		return 0, false
	}
	return uint32(n), true
}
```

- [x] **Step 4: Implement the parser**

This is a transcription of V8 12.4.254 `src/json/json-parser.cc` (`ParseJsonValue` 1480–1760, `ParseJsonNumber` 1771–1864, `ScanJsonString` 1996–2090) and `json-parser.h` (`Expect`, `ExpectNext`, `Check`, `ScanLiteral`, lines 238–283). Positions are indexes into the UTF-16 source.

`internal/jsjson/parse.go`:

```go
package jsjson

import (
	"strconv"

	"github.com/Mizore66/faultline/internal/jsstr"
)

type SyntaxError struct{ Message string }

func (e *SyntaxError) Error() string { return e.Message }

const eos = -1

type token uint8

const (
	tokEOS token = iota
	tokString
	tokNumber
	tokLBrace
	tokRBrace
	tokLBrack
	tokRBrack
	tokTrue
	tokFalse
	tokNull
	tokColon
	tokComma
	tokWhitespace
	tokIllegal
)

// tokenOf is V8's one_char_json_tokens; code units above 0xFF are ILLEGAL.
func tokenOf(c int) token {
	switch {
	case c == eos:
		return tokEOS
	case c == '"':
		return tokString
	case c == '-' || (c >= '0' && c <= '9'):
		return tokNumber
	case c == '{':
		return tokLBrace
	case c == '}':
		return tokRBrace
	case c == '[':
		return tokLBrack
	case c == ']':
		return tokRBrack
	case c == 't':
		return tokTrue
	case c == 'f':
		return tokFalse
	case c == 'n':
		return tokNull
	case c == ':':
		return tokColon
	case c == ',':
		return tokComma
	case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		return tokWhitespace
	}
	return tokIllegal
}

type parser struct {
	src []uint16
	pos int
	err *SyntaxError
}

// Parse is JSON.parse(text) without a reviver.
func Parse(text string) (Value, error) {
	p := &parser{src: jsstr.ToUTF16(text)}
	v := p.parseValue()
	if p.err == nil && !p.check(tokEOS) {
		p.failAt(msgNonWhitespace)
	}
	if p.err != nil {
		return Value{}, p.err
	}
	return v, nil
}

func (p *parser) peekChar() int {
	if p.pos < len(p.src) {
		return int(p.src[p.pos])
	}
	return eos
}

func (p *parser) skipWS() {
	for p.pos < len(p.src) && tokenOf(int(p.src[p.pos])) == tokWhitespace {
		p.pos++
	}
}

// check is V8 Check: skip whitespace, consume token if it is next.
func (p *parser) check(t token) bool {
	p.skipWS()
	if tokenOf(p.peekChar()) != t {
		return false
	}
	if t != tokEOS {
		p.pos++
	}
	return true
}

// expect is V8 Expect with an explicit message.
func (p *parser) expect(t token, tmpl string) bool {
	if tokenOf(p.peekChar()) == t {
		p.pos++
		return true
	}
	p.failAt(tmpl)
	return false
}

func (p *parser) expectNext(t token, tmpl string) bool {
	p.skipWS()
	return p.expect(t, tmpl)
}

func (p *parser) parseValue() Value {
	p.skipWS()
	c := p.peekChar()
	switch tokenOf(c) {
	case tokString:
		p.pos++
		if s, ok := p.scanString(); ok {
			return MakeString(s)
		}
	case tokNumber:
		return p.parseNumber()
	case tokLBrace:
		p.pos++
		return p.parseObject()
	case tokLBrack:
		p.pos++
		return p.parseArray()
	case tokTrue:
		if p.scanLiteral("true") {
			return MakeBool(true)
		}
	case tokFalse:
		if p.scanLiteral("false") {
			return MakeBool(false)
		}
	case tokNull:
		if p.scanLiteral("null") {
			return MakeNull()
		}
	default:
		p.unexpectedChar(c)
	}
	return Value{}
}

func (p *parser) parseObject() Value {
	obj := NewObj()
	if p.check(tokRBrace) {
		return MakeObject(obj)
	}
	if !p.expectNext(tokString, msgExpectedPropNameOrRBrace) {
		return Value{}
	}
	for {
		key, ok := p.scanString()
		if !ok || !p.expectNext(tokColon, msgExpectedColonAfterPropertyName) {
			return Value{}
		}
		v := p.parseValue()
		if p.err != nil {
			return Value{}
		}
		obj.Set(key, v)
		if p.check(tokComma) {
			if !p.expectNext(tokString, msgExpectedDoubleQuotedPropertyName) {
				return Value{}
			}
			continue
		}
		if !p.expect(tokRBrace, msgExpectedCommaOrRBrace) {
			return Value{}
		}
		return MakeObject(obj)
	}
}

func (p *parser) parseArray() Value {
	if p.check(tokRBrack) {
		return MakeArray(nil)
	}
	var items []Value
	for {
		v := p.parseValue()
		if p.err != nil {
			return Value{}
		}
		items = append(items, v)
		if p.check(tokComma) {
			continue
		}
		if !p.expect(tokRBrack, msgExpectedCommaOrRBrack) {
			return Value{}
		}
		return MakeArray(items)
	}
}

// scanLiteral is V8 ScanLiteral; the first character already matched.
func (p *parser) scanLiteral(lit string) bool {
	remaining := len(p.src) - p.pos
	if remaining >= len(lit) {
		match := true
		for i := 1; i < len(lit); i++ {
			if p.src[p.pos+i] != uint16(lit[i]) {
				match = false
				break
			}
		}
		if match {
			p.pos += len(lit)
			return true
		}
	}
	p.pos++
	for i := 0; i < min(len(lit)-1, remaining-1); i++ {
		if uint16(lit[1+i]) != p.src[p.pos] {
			p.unexpectedChar(int(p.src[p.pos]))
			return false
		}
		p.pos++
	}
	p.unexpectedToken(tokEOS)
	return false
}

func isDigit(c int) bool { return c >= '0' && c <= '9' }

// isNumberPart is V8 NumberPartField: digits . e E + -.
func isNumberPart(c int) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func (p *parser) parseNumber() Value {
	start := p.pos
	sign := 1
	c := p.peekChar()
	if c == '-' {
		sign = -1
		p.pos++
		c = p.peekChar()
	}
	if c == '0' {
		p.pos++
		c = p.peekChar()
		if isNumberPart(c) {
			if isDigit(c) {
				p.unexpectedToken(tokNumber)
				return Value{}
			}
		} else if sign > 0 {
			return MakeNumber(0)
		}
	} else {
		digits := p.pos
		for isDigit(p.peekChar()) {
			p.pos++
		}
		if p.pos == digits {
			p.failAt(msgNoNumberAfterMinusSign)
			return Value{}
		}
	}
	if p.peekChar() == '.' {
		p.pos++
		if !isDigit(p.peekChar()) {
			p.failAt(msgUnterminatedFractionalNumber)
			return Value{}
		}
		for isDigit(p.peekChar()) {
			p.pos++
		}
	}
	if c := p.peekChar(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.peekChar(); c == '-' || c == '+' {
			p.pos++
		}
		if !isDigit(p.peekChar()) {
			p.failAt(msgExponentPartMissingNumber)
			return Value{}
		}
		for isDigit(p.peekChar()) {
			p.pos++
		}
	}
	text := make([]byte, 0, p.pos-start)
	for _, u := range p.src[start:p.pos] {
		text = append(text, byte(u))
	}
	f, _ := strconv.ParseFloat(string(text), 64) // ±Inf on overflow, like V8
	return MakeNumber(f)
}

func hexValue(c int) int {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// scanString is V8 ScanJsonString; the opening quote is consumed.
func (p *parser) scanString() (string, bool) {
	var units []uint16
	for {
		if p.pos >= len(p.src) {
			p.failAt(msgUnterminatedString)
			return "", false
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return jsstr.FromUTF16(units), true
		case c == '\\':
			p.pos++
			e := p.peekChar()
			if e == eos || e > 0xFF {
				p.unexpectedChar(e)
				return "", false
			}
			switch e {
			case '"', '\\', '/':
				units = append(units, uint16(e))
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			case 'u':
				v := 0
				for i := 0; i < 4; i++ {
					p.pos++
					d := hexValue(p.peekChar())
					if d < 0 {
						p.failAt(msgBadUnicodeEscape)
						return "", false
					}
					v = v*16 + d
				}
				units = append(units, uint16(v))
			default:
				p.failAt(msgBadEscapedCharacter)
				return "", false
			}
			p.pos++
		case c < 0x20:
			p.failAt(msgBadControlCharacter)
			return "", false
		default:
			units = append(units, c)
			p.pos++
		}
	}
}
```

- [x] **Step 5: Implement V8 error reporting**

Templates are verbatim from V8 12.4.254 `src/common/message-template.h:518–564`; selection logic from `json-parser.cc:375–491`.

`internal/jsjson/errors.go`:

```go
package jsjson

import (
	"fmt"

	"github.com/Mizore66/faultline/internal/jsstr"
)

const (
	msgUnexpectedEOS                    = "Unexpected end of JSON input"
	msgUnexpectedNumber                 = "Unexpected number in JSON at position %d (line %d column %d)"
	msgUnexpectedString                 = "Unexpected string in JSON at position %d (line %d column %d)"
	msgUnterminatedString               = "Unterminated string in JSON at position %d (line %d column %d)"
	msgExpectedPropNameOrRBrace         = "Expected property name or '}' in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBrack            = "Expected ',' or ']' after array element in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBrace            = "Expected ',' or '}' after property value in JSON at position %d (line %d column %d)"
	msgExpectedDoubleQuotedPropertyName = "Expected double-quoted property name in JSON at position %d (line %d column %d)"
	msgExponentPartMissingNumber        = "Exponent part is missing a number in JSON at position %d (line %d column %d)"
	msgExpectedColonAfterPropertyName   = "Expected ':' after property name in JSON at position %d (line %d column %d)"
	msgUnterminatedFractionalNumber     = "Unterminated fractional number in JSON at position %d (line %d column %d)"
	msgNonWhitespace                    = "Unexpected non-whitespace character after JSON at position %d (line %d column %d)"
	msgBadEscapedCharacter              = "Bad escaped character in JSON at position %d (line %d column %d)"
	msgBadControlCharacter              = "Bad control character in string literal in JSON at position %d (line %d column %d)"
	msgBadUnicodeEscape                 = "Bad Unicode escape in JSON at position %d (line %d column %d)"
	msgNoNumberAfterMinusSign           = "No number after minus sign in JSON at position %d (line %d column %d)"
)

const (
	maxContextCharacters              = 10
	minOriginalSourceLengthForContext = maxContextCharacters*2 + 1
)

func (p *parser) setError(message string) {
	if p.err == nil {
		p.err = &SyntaxError{message}
	}
	p.pos = len(p.src) // V8 moves the cursor to the end
}

// location is V8 CalculateFileLocation: \r\n counts once.
func (p *parser) location(pos int) (line, column int) {
	line, lastBreak := 1, 0
	for i := 0; i < pos; i++ {
		if p.src[i] == '\r' && i < pos-1 && p.src[i+1] == '\n' {
			i++
		}
		if p.src[i] == '\r' || p.src[i] == '\n' {
			line++
			lastBreak = i + 1
		}
	}
	return line, 1 + pos - lastBreak
}

func (p *parser) failAt(tmpl string) {
	if p.err != nil {
		return
	}
	line, column := p.location(p.pos)
	p.setError(fmt.Sprintf(tmpl, p.pos, line, column))
}

// unexpectedChar is V8 ReportUnexpectedCharacter.
func (p *parser) unexpectedChar(c int) {
	t := tokIllegal
	if c == eos {
		t = tokEOS
	} else if c <= 0xFF {
		t = tokenOf(c)
	}
	p.unexpectedToken(t)
}

// unexpectedToken is V8 ReportUnexpectedToken without an explicit message.
func (p *parser) unexpectedToken(t token) {
	if p.err != nil {
		return
	}
	switch t {
	case tokEOS:
		p.setError(msgUnexpectedEOS)
	case tokNumber:
		p.failAt(msgUnexpectedNumber)
	case tokString:
		p.failAt(msgUnexpectedString)
	default:
		if p.isSpecialString() {
			p.setError(`"` + jsstr.FromUTF16(p.src) + `" is not valid JSON`)
			return
		}
		p.setError(p.withEllipses())
	}
}

func (p *parser) isSpecialString() bool {
	switch jsstr.FromUTF16(p.src) {
	case "[object Object]", "undefined", "Infinity", "NaN":
		return true
	}
	return false
}

// withEllipses is V8 GetErrorMessageWithEllipses.
func (p *parser) withEllipses() string {
	tok := jsstr.FromUTF16(p.src[p.pos : p.pos+1])
	n := len(p.src)
	if n < minOriginalSourceLengthForContext {
		return "Unexpected token '" + tok + "', \"" + jsstr.FromUTF16(p.src) + "\" is not valid JSON"
	}
	switch {
	case p.pos < maxContextCharacters:
		return "Unexpected token '" + tok + "', \"" + jsstr.FromUTF16(p.src[:p.pos+maxContextCharacters]) + "\"... is not valid JSON"
	case p.pos < n-maxContextCharacters:
		return "Unexpected token '" + tok + "', ...\"" + jsstr.FromUTF16(p.src[p.pos-maxContextCharacters:p.pos+maxContextCharacters]) + "\"... is not valid JSON"
	default:
		return "Unexpected token '" + tok + "', ...\"" + jsstr.FromUTF16(p.src[p.pos-maxContextCharacters:]) + "\" is not valid JSON"
	}
}
```

- [x] **Step 6: Implement number formatting and stringify**

`internal/jsjson/number.go`:

```go
package jsjson

import (
	"math"
	"strconv"
	"strings"
)

// FormatNumber is ECMAScript Number::toString(10).
func FormatNumber(f float64) string {
	switch {
	case f == 0:
		return "0"
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exponent)
	k, n := len(digits), e+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	out := digits[:1]
	if k > 1 {
		out += "." + digits[1:]
	}
	if n-1 < 0 {
		return sign + out + "e-" + strconv.Itoa(1-n)
	}
	return sign + out + "e+" + strconv.Itoa(n-1)
}
```

`internal/jsjson/stringify.go`:

```go
package jsjson

import (
	"fmt"
	"math"
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// Quote is JSON.stringify(string) with ES2019 well-formed output.
func Quote(s string) string {
	units := jsstr.ToUTF16(s)
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u == '"':
			b.WriteString(`\"`)
		case u == '\\':
			b.WriteString(`\\`)
		case u == '\b':
			b.WriteString(`\b`)
		case u == '\f':
			b.WriteString(`\f`)
		case u == '\n':
			b.WriteString(`\n`)
		case u == '\r':
			b.WriteString(`\r`)
		case u == '\t':
			b.WriteString(`\t`)
		case u < 0x20:
			fmt.Fprintf(&b, `\u%04x`, u)
		case u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF:
			b.WriteString(jsstr.FromUTF16(units[i : i+2]))
			i++
		case u >= 0xD800 && u <= 0xDFFF:
			fmt.Fprintf(&b, `\u%04x`, u)
		default:
			b.WriteString(jsstr.FromUTF16(units[i : i+1]))
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Stringify is JSON.stringify(v).
func Stringify(v Value) string { return StringifyIndent(v, "") }

// StringifyIndent is JSON.stringify(v, null, indent). Undefined object members
// are omitted; Undefined array elements and non-finite numbers print null.
func StringifyIndent(v Value, indent string) string {
	var b strings.Builder
	write(&b, v, indent, "")
	return b.String()
}

func write(b *strings.Builder, v Value, indent, current string) {
	switch v.kind {
	case Undefined, Null:
		b.WriteString("null")
	case Bool:
		if v.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Number:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(FormatNumber(v.n))
		}
	case String:
		b.WriteString(Quote(v.s))
	case Array:
		if len(v.arr) == 0 {
			b.WriteString("[]")
			return
		}
		inner := current + indent
		b.WriteByte('[')
		for i, item := range v.arr {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			write(b, item, indent, inner)
		}
		if indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte(']')
	case Object:
		inner := current + indent
		wrote := false
		b.WriteByte('{')
		for _, k := range v.obj.keys {
			child := v.obj.values[k]
			if child.kind == Undefined {
				continue
			}
			if wrote {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			b.WriteString(Quote(k))
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			write(b, child, indent, inner)
			wrote = true
		}
		if wrote && indent != "" {
			b.WriteString("\n" + current)
		}
		b.WriteByte('}')
	}
}
```

- [x] **Step 7: Run unit tests**

Run: `go test ./internal/jsjson/`
Expected: PASS. If a V8 case fails, re-read the cited V8 lines. Do not change the expected strings; they came from Node.

- [x] **Step 8: Add oracle ops and random generators**

Add to `handlers` in `difftest/gen/node-oracle.ts`:

```ts
  parse: ({ text }: { text: string }) => {
    try {
      const value = JSON.parse(text);
      return { ok: true, compact: JSON.stringify(value), pretty: JSON.stringify(value, null, 2) };
    } catch (error) {
      return { ok: false, message: (error as Error).message };
    }
  },
  formatNumber: ({ hex }: { hex: string }) => {
    const view = new DataView(new ArrayBuffer(8));
    view.setBigUint64(0, BigInt(`0x${hex}`));
    return String(view.getFloat64(0));
  },
```

`difftest/gen/random.go` (package `gen`: Go helpers that live beside the TS generators):

```go
// Package gen holds Go-side random input generators for live oracle tests.
package gen

import (
	"math"
	"math/rand/v2"
	"strings"
)

var stringPieces = []string{
	"a", "Z", "_", "-", ".", "/", "0", "9", " ", "é", "ß", "中", "😀",
	`\n`, `\t`, `\"`, `\\`, `\/`, `\u00e9`, `\ud800`, `\udc00`, `\ud83d\ude00`, `\u2028`,
}

var corruptions = []string{"{", "}", "[", "]", ",", ":", `"`, `\`, "-", "+", ".", "e", "0", "t", "n", " ", "\t", "\r", "\n", "\x00", "\x1f", "é", "\ufeff", "\u2028", "😀"}

func randomString(r *rand.Rand) string {
	var b strings.Builder
	b.WriteByte('"')
	for n := r.IntN(8); n > 0; n-- {
		b.WriteString(stringPieces[r.IntN(len(stringPieces))])
	}
	b.WriteByte('"')
	return b.String()
}

func randomNumber(r *rand.Rand) string {
	forms := []string{"0", "-0", "7", "-12", "3.25", "1e21", "1E-7", "123456789012345678901", "1e400", "-1e400", "1e-400", "0.1", "2.5e+3"}
	return forms[r.IntN(len(forms))]
}

func randomValue(r *rand.Rand, depth int) string {
	switch k := r.IntN(10); {
	case depth > 3 || k < 2:
		return randomNumber(r)
	case k < 4:
		return randomString(r)
	case k == 4:
		return []string{"true", "false", "null"}[r.IntN(3)]
	case k < 7:
		items := make([]string, r.IntN(4))
		for i := range items {
			items[i] = randomValue(r, depth+1)
		}
		return "[" + strings.Join(items, ",") + "]"
	default:
		keys := []string{`"b"`, `"a"`, `"A"`, `"_"`, `"-"`, `"0"`, `"10"`, `"01"`, `"4294967295"`, `"__proto__"`, `"é"`}
		members := make([]string, r.IntN(5))
		for i := range members {
			members[i] = keys[r.IntN(len(keys))] + ":" + randomValue(r, depth+1)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
}

// RandomJSONText returns JSON text; about a third of results are corrupted.
func RandomJSONText(r *rand.Rand) string {
	text := randomValue(r, 0)
	if r.IntN(3) != 0 || text == "" {
		return text
	}
	runes := []rune(text)
	i := r.IntN(len(runes) + 1)
	switch r.IntN(3) {
	case 0:
		return string(runes[:i])
	case 1:
		return string(runes[:i]) + corruptions[r.IntN(len(corruptions))] + string(runes[i:])
	default:
		if i == len(runes) {
			i--
		}
		return string(runes[:i]) + string(runes[i+1:])
	}
}

// RandomFloat returns a finite float64 drawn from its bit patterns.
func RandomFloat(r *rand.Rand) float64 {
	for {
		f := math.Float64frombits(r.Uint64())
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
}
```

- [x] **Step 9: Write the live property tests**

`difftest/jsjson_live_test.go`:

```go
package difftest

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const liveCases = 10000

// call sends args to op and parses the result with jsjson.
func call(t *testing.T, c *oracle.Client, op, args string) *jsjson.Obj {
	t.Helper()
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	v, err := jsjson.Parse(raw)
	if err != nil {
		t.Fatalf("%s: undecodable result %q: %v", op, raw, err)
	}
	return v.Obj()
}

func TestLiveParse(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < liveCases; i++ {
		text := gen.RandomJSONText(r)
		want := call(t, c, "parse", `{"text":`+jsjson.Quote(text)+`}`)
		v, err := jsjson.Parse(text)
		if want.Field("ok").Bool() {
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v; Node accepted it", text, err)
			}
			if got := jsjson.Stringify(v); got != want.Field("compact").Str() {
				t.Fatalf("Stringify(%q) = %q, want %q", text, got, want.Field("compact").Str())
			}
			if got := jsjson.StringifyIndent(v, "  "); got != want.Field("pretty").Str() {
				t.Fatalf("StringifyIndent(%q) = %q, want %q", text, got, want.Field("pretty").Str())
			}
			continue
		}
		if err == nil || err.Error() != want.Field("message").Str() {
			t.Fatalf("Parse(%q) error = %v, want %q", text, err, want.Field("message").Str())
		}
	}
}

func TestLiveFormatNumber(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < liveCases; i++ {
		f := gen.RandomFloat(r)
		raw, err := c.Call("formatNumber", fmt.Sprintf(`{"hex":"%016x"}`, math.Float64bits(f)))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if got := jsjson.FormatNumber(f); got != want.Str() {
			t.Fatalf("FormatNumber(%x) = %q, want %q", math.Float64bits(f), got, want.Str())
		}
	}
}
```

- [x] **Step 10: Run live tests**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run 'TestLive(Parse|FormatNumber)' -v`
Expected: PASS with 10,000 cases each. Any failure prints the input; fix the Go code to match Node, re-reading the V8 source for parser failures.

- [x] **Step 11: Commit**

```bash
git add internal/jsjson difftest
git commit -m "feat(go): JSON.parse/stringify clone with V8 12.4 error messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `canonical` — localeCompare ordering, canonicalJson, digests

**Files:**
- Create: `internal/canonical/collate.go`, `internal/canonical/canonical.go`, `internal/canonical/canonical_test.go`, `difftest/canonical_live_test.go`
- Modify: `go.mod`/`go.sum` (`go get golang.org/x/text@v0.42.0`), `difftest/gen/node-oracle.ts` (add `sort`, `canonical` ops), `difftest/gen/random.go` (add `RandomKeys`)

**Interfaces:**
- Consumes: `jsjson.*`, `jsstr.ToUTF8`.
- Produces: `canonical.LocaleCompare(a, b string) int`, `canonical.SortLocale([]string) []string` (stable, returns a new slice), `canonical.CanonicalJSON(jsjson.Value) (string, error)`, `canonical.SHA256Hex(s string) string` (hashes `jsstr.ToUTF8(s)`), `canonical.SHA256HexBytes([]byte) string`, `canonical.DigestJSON(jsjson.Value) (string, error)` (`"sha256:" + hex`). `gen.RandomKeys(r *rand.Rand) []string`.

- [x] **Step 1: Write failing tests**

Expected values were produced by frozen TS `canonicalJson`/`digestJson`. Note `"9"` before `"10"`: TS rebuilds each object, and JS objects put array-index keys first in numeric order, so only non-index keys follow `localeCompare`.

`internal/canonical/canonical_test.go`:

```go
package canonical

import (
	"slices"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestCanonicalJSONMatchesTS(t *testing.T) {
	v, err := jsjson.Parse(`{"b":1,"a":[{"d":null,"c":"é\ud800"}],"A":2,"_":3,"-":4,"10":5,"9":6,"n":1e21,"m":-0,"x":1.5e-7}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"9":6,"10":5,"_":3,"-":4,"a":[{"c":"é\ud800","d":null}],"A":2,"b":1,"m":0,"n":1e+21,"x":1.5e-7}`
	if got != want {
		t.Fatalf("CanonicalJSON = %s\nwant           %s", got, want)
	}
	if d, _ := DigestJSON(v); d != "sha256:39378331d042e1c105552bc3b6d08b0ceed9df0af4c63c941d8d2161dad68e3f" {
		t.Fatalf("DigestJSON = %s", d)
	}
}

func TestCanonicalRejectsNonFinite(t *testing.T) {
	v, _ := jsjson.Parse(`{"a":1e400}`)
	if _, err := CanonicalJSON(v); err == nil || err.Error() != "Value is not finite JSON: number" {
		t.Fatalf("err = %v", err)
	}
}

func TestSortLocale(t *testing.T) {
	got := SortLocale([]string{"rootDigest", "root-digest", "root_digest", "b", "B", "a", "A", "x10", "x2", "x1"})
	want := []string{"a", "A", "b", "B", "root_digest", "root-digest", "rootDigest", "x1", "x10", "x2"}
	if !slices.Equal(got, want) {
		t.Fatalf("SortLocale = %q", got)
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/canonical/`
Expected: FAIL (undefined: CanonicalJSON).

- [x] **Step 3: Implement**

```bash
go get golang.org/x/text@v0.42.0
```

`internal/canonical/collate.go`:

```go
package canonical

import (
	"slices"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Collators keep internal buffers, so each goroutine takes its own.
var collators = sync.Pool{New: func() any { return collate.New(language.AmericanEnglish) }}

// LocaleCompare is `a.localeCompare(b)` under Node's en-US ICU defaults.
func LocaleCompare(a, b string) int {
	c := collators.Get().(*collate.Collator)
	defer collators.Put(c)
	return c.CompareString(a, b)
}

// SortLocale is `[...keys].sort((l, r) => l.localeCompare(r))`; Array.prototype.sort is stable.
func SortLocale(keys []string) []string {
	out := slices.Clone(keys)
	slices.SortStableFunc(out, LocaleCompare)
	return out
}
```

`internal/canonical/canonical.go`:

```go
// Package canonical ports src/canonical.ts.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// normalize ports `normalize` in src/canonical.ts. The rebuilt object is a JS
// object, so array-index keys still come first (jsjson.Obj keeps that order).
func normalize(v jsjson.Value) (jsjson.Value, error) {
	switch v.Kind() {
	case jsjson.Array:
		items := make([]jsjson.Value, len(v.Items()))
		for i, item := range v.Items() {
			n, err := normalize(item)
			if err != nil {
				return jsjson.Value{}, err
			}
			items[i] = n
		}
		return jsjson.MakeArray(items), nil
	case jsjson.Object:
		var keys []string
		for _, k := range v.Obj().Keys() {
			if v.Obj().Field(k).Kind() != jsjson.Undefined {
				keys = append(keys, k)
			}
		}
		out := jsjson.NewObj()
		for _, k := range SortLocale(keys) {
			n, err := normalize(v.Obj().Field(k))
			if err != nil {
				return jsjson.Value{}, err
			}
			out.Set(k, n)
		}
		return jsjson.MakeObject(out), nil
	case jsjson.Number:
		if f := v.Num(); f-f != 0 { // NaN or ±Inf
			return jsjson.Value{}, errors.New("Value is not finite JSON: number")
		}
		return v, nil
	case jsjson.Undefined:
		return jsjson.Value{}, errors.New("Value is not JSON-serializable: undefined")
	}
	return v, nil
}

// CanonicalJSON ports canonicalJson.
func CanonicalJSON(v jsjson.Value) (string, error) {
	n, err := normalize(v)
	if err != nil {
		return "", err
	}
	return jsjson.Stringify(n), nil
}

// SHA256Hex ports sha256(string): Node hashes the UTF-8 encoding of the JS string.
func SHA256Hex(s string) string { return SHA256HexBytes([]byte(jsstr.ToUTF8(s))) }

// SHA256HexBytes ports sha256(Buffer).
func SHA256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DigestJSON ports digestJson.
func DigestJSON(v jsjson.Value) (string, error) {
	c, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return "sha256:" + SHA256Hex(c), nil
}
```

- [x] **Step 4: Run unit tests**

Run: `go test ./internal/canonical/`
Expected: PASS.

- [x] **Step 5: Add oracle ops, key generator, and live tests**

Add to the imports and `handlers` of `difftest/gen/node-oracle.ts`:

```ts
import { canonicalJson, digestJson } from "../../src/canonical.js";
```

```ts
  sort: ({ keys }: { keys: string[] }) => [...keys].sort((left, right) => left.localeCompare(right)),
  canonical: ({ text }: { text: string }) => {
    try {
      const value = JSON.parse(text);
      return { ok: true, canonical: canonicalJson(value), digest: digestJson(value) };
    } catch (error) {
      return { ok: false, message: (error as Error).message };
    }
  },
```

Append to `difftest/gen/random.go`:

```go
const asciiKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@ +#$%&*()[]{}!?,;=~^'\"<>|\\`"

var latin1Extras = []rune("éÉèàçñöÖüßøÆ¡¿ª·")

// RandomKeys returns 2–7 distinct keys; about a quarter include Latin-1 letters.
func RandomKeys(r *rand.Rand) []string {
	seen := map[string]bool{}
	var keys []string
	for count := 2 + r.IntN(6); len(keys) < count; {
		var b strings.Builder
		for n := 1 + r.IntN(12); n > 0; n-- {
			if r.IntN(4) == 0 {
				b.WriteRune(latin1Extras[r.IntN(len(latin1Extras))])
			} else {
				b.WriteByte(asciiKeyAlphabet[r.IntN(len(asciiKeyAlphabet))])
			}
		}
		if k := b.String(); !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}
```

`difftest/canonical_live_test.go`:

```go
package difftest

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = jsjson.Quote(s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func TestLiveSortLocale(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < liveCases; i++ {
		keys := gen.RandomKeys(r)
		raw, err := c.Call("sort", `{"keys":`+quoteList(keys)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsjson.Parse(raw)
		var want []string
		for _, item := range v.Items() {
			want = append(want, item.Str())
		}
		if got := canonical.SortLocale(keys); !slices.Equal(got, want) {
			t.Fatalf("SortLocale(%q) = %q, want %q", keys, got, want)
		}
	}
}

func TestLiveCanonical(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(7, 8))
	for i := 0; i < liveCases; i++ {
		text := gen.RandomJSONText(r)
		v, err := jsjson.Parse(text)
		if err != nil {
			continue // parse parity is TestLiveParse's job
		}
		want := call(t, c, "canonical", `{"text":`+jsjson.Quote(text)+`}`)
		got, cerr := canonical.CanonicalJSON(v)
		if !want.Field("ok").Bool() {
			if cerr == nil || cerr.Error() != want.Field("message").Str() {
				t.Fatalf("CanonicalJSON(%q) error = %v, want %q", text, cerr, want.Field("message").Str())
			}
			continue
		}
		if cerr != nil || got != want.Field("canonical").Str() {
			t.Fatalf("CanonicalJSON(%q) = %q (%v), want %q", text, got, cerr, want.Field("canonical").Str())
		}
		if d, _ := canonical.DigestJSON(v); d != want.Field("digest").Str() {
			t.Fatalf("DigestJSON(%q) = %s, want %s", text, d, want.Field("digest").Str())
		}
	}
}
```

- [x] **Step 6: Run live tests**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run 'TestLive(SortLocale|Canonical)' -v`
Expected: PASS. ASCII and Latin-1 keys must match with zero mismatches (a pre-plan spike found 0 mismatches in 20,000 ASCII key sets with x/text v0.42.0). If a Latin-1 mismatch appears, try `collate.New(language.AmericanEnglish, collate.Force)`. If it still differs, stop and report the failing keys; do not paper over it.

- [x] **Step 7: Commit**

```bash
git add go.mod go.sum internal/canonical difftest
git commit -m "feat(go): canonical JSON with en-US localeCompare ordering

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `nodefs` — Node file reading, paths, and error text

**Files:**
- Create: `internal/nodefs/decode.go`, `internal/nodefs/fs.go`, `internal/nodefs/path.go`, `internal/nodefs/nodefs_test.go`, `difftest/nodefs_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (add `decodeUtf8`)

**Interfaces:**
- Consumes: `jsstr`.
- Produces: `nodefs.DecodeUTF8([]byte) string` (Buffer.toString("utf8")); `nodefs.ReadBytes(path) ([]byte, error)` (readFileSync); `nodefs.ReadText(path) (string, error)` (readFileSync utf8); `nodefs.Lstat(path) (fs.FileInfo, error)` (lstatSync); `nodefs.Exists(path) bool` (existsSync); `nodefs.ReadDirNames(path) ([]string, error)` (readdirSync, byte-sorted); `type nodefs.Error struct{ Code, Syscall, Path string; NoPath bool }` whose `Error()` is Node's message; `nodefs.Resolve(p ...string) string`, `nodefs.Join(p ...string) string`, `nodefs.Relative(from, to string) string` (`""` when equal), `nodefs.IsAbsolute(string) bool`, `nodefs.MkdirTemp(prefix string) (string, error)` (mkdtempSync(join(tmpdir(), prefix))), `nodefs.RemoveAll(string)`.

- [x] **Step 1: Write failing tests**

Decoding expectations were captured from `Buffer.from(bytes).toString("utf8")` on Node 22.

`internal/nodefs/nodefs_test.go`:

```go
package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDecodeUTF8MatchesNode(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xE2, 0x82}, "\ufffd"},
		{[]byte{0xF0, 0x9F, 0x98}, "\ufffd"},
		{[]byte{0xC0, 0xAF}, "\ufffd\ufffd"},
		{[]byte{0xED, 0xA0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xE2, 0x82, 0x41}, "\ufffdA"},
		{[]byte{0xFF}, "\ufffd"},
		{[]byte{0xEF, 0xBB, 0xBF, 0x41}, "\ufeffA"},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, "\U0001F600"},
		{[]byte{0xE0, 0x80, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0x61, 0xF1, 0x80, 0x80, 0xE1, 0x80, 0xC2, 0x62}, "a\ufffd\ufffd\ufffdb"},
	}
	for _, c := range cases {
		if got := DecodeUTF8(c.in); got != c.want {
			t.Errorf("DecodeUTF8(% x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNodeErrorText(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := ReadText(missing); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+missing+"'" {
		t.Fatalf("ReadText error = %v", err)
	}
	if _, err := ReadBytes(dir); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("ReadBytes(dir) error = %v", err)
	}
	if _, err := Lstat(missing); err == nil || err.Error() != "ENOENT: no such file or directory, lstat '"+missing+"'" {
		t.Fatalf("Lstat error = %v", err)
	}
	if _, err := ReadDirNames(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("ReadDirNames error = %v", err)
	}
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o600)
	if _, err := ReadText(filepath.Join(file, "x")); err == nil || err.Error() != "ENOTDIR: not a directory, open '"+filepath.Join(file, "x")+"'" {
		if runtime.GOOS != "windows" {
			t.Fatalf("ENOTDIR error = %v", err)
		}
	}
}

func TestReadDirNamesIsByteSorted(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b", "Z", "_", "1", "é"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	got, _ := ReadDirNames(dir)
	want := []string{"1", "Z", "_", "b", "é"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadDirNames = %q", got)
		}
	}
}

func TestRelative(t *testing.T) {
	if Relative("/a/b", "/a/b") != "" {
		t.Fatal(`Relative of equal paths must be ""`)
	}
	if runtime.GOOS != "windows" && Relative("/a/b", "/a/c/d") != "../c/d" {
		t.Fatal("Relative")
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/nodefs/`
Expected: FAIL (undefined: DecodeUTF8).

- [x] **Step 3: Implement decoding**

WHATWG Encoding Standard §9.1.1 "UTF-8 decoder", which V8 follows.

`internal/nodefs/decode.go`:

```go
// Package nodefs reproduces the Node fs/path behavior the verifiers observe.
package nodefs

import "strings"

// DecodeUTF8 is Buffer.toString("utf8") (WHATWG UTF-8 decode with replacement).
func DecodeUTF8(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	cp, needed, seen := rune(0), 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				} else if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				} else if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(0xFFFD)
			}
			continue
		}
		if c < lower || c > upper {
			cp, needed, seen = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(0xFFFD)
			i-- // reprocess this byte
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(0xFFFD)
	}
	return out.String()
}
```

- [x] **Step 4: Implement fs and path helpers**

Errors are derived from file types first, then from errno, so every OS yields the Linux/Node message (goldens come from Linux). Descriptions are libuv's `uv_strerror` strings.

`internal/nodefs/fs.go`:

```go
package nodefs

import (
	"errors"
	"io/fs"
	"os"
	"sort"
	"syscall"
)

// Error renders like a Node fs error message.
type Error struct {
	Code, Syscall, Path string
	NoPath              bool
}

var descriptions = map[string]string{
	"ENOENT":       "no such file or directory",
	"ENOTDIR":      "not a directory",
	"EISDIR":       "illegal operation on a directory",
	"EACCES":       "permission denied",
	"EPERM":        "operation not permitted",
	"ELOOP":        "too many symbolic links encountered",
	"ENAMETOOLONG": "name too long",
	"EMFILE":       "too many open files",
	"EBUSY":        "resource busy or locked",
	"EINVAL":       "invalid argument",
	"EIO":          "i/o error",
	"UNKNOWN":      "unknown error",
}

func (e *Error) Error() string {
	if e.NoPath {
		return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall
	}
	return e.Code + ": " + descriptions[e.Code] + ", " + e.Syscall + " '" + e.Path + "'"
}

func codeOf(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ENOENT:
			return "ENOENT"
		case syscall.ENOTDIR:
			return "ENOTDIR"
		case syscall.EISDIR:
			return "EISDIR"
		case syscall.EACCES:
			return "EACCES"
		case syscall.EPERM:
			return "EPERM"
		case syscall.ELOOP:
			return "ELOOP"
		case syscall.ENAMETOOLONG:
			return "ENAMETOOLONG"
		case syscall.EMFILE:
			return "EMFILE"
		case syscall.EBUSY:
			return "EBUSY"
		case syscall.EINVAL:
			return "EINVAL"
		case syscall.EIO:
			return "EIO"
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	}
	return "UNKNOWN"
}

// ancestorNotDir reports whether a parent component of path is a non-directory
// (Linux returns ENOTDIR there; Windows reports "not found").
func ancestorNotDir(path string) bool {
	for dir := Dirname(path); dir != path; path, dir = dir, Dirname(dir) {
		if info, err := os.Stat(dir); err == nil {
			return !info.IsDir()
		}
	}
	return false
}

func wrap(err error, syscallName, path string) error {
	code := codeOf(err)
	if code == "ENOENT" && ancestorNotDir(path) {
		code = "ENOTDIR"
	}
	return &Error{Code: code, Syscall: syscallName, Path: path}
}

// ReadBytes is readFileSync(path).
func ReadBytes(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, wrap(err, "open", path)
	}
	if info.IsDir() {
		return nil, &Error{Code: "EISDIR", Syscall: "read", NoPath: true}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, wrap(err, "open", path)
	}
	return b, nil
}

// ReadText is readFileSync(path, "utf8").
func ReadText(path string) (string, error) {
	b, err := ReadBytes(path)
	if err != nil {
		return "", err
	}
	return DecodeUTF8(b), nil
}

// Lstat is lstatSync(path).
func Lstat(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, wrap(err, "lstat", path)
	}
	return info, nil
}

// Exists is existsSync(path): true when stat (following links) succeeds.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadDirNames is readdirSync(path): names sorted by byte order (libuv scandir).
func ReadDirNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	sort.Strings(names)
	return names, nil
}

// MkdirTemp is mkdtempSync(join(tmpdir(), prefix)).
func MkdirTemp(prefix string) (string, error) { return os.MkdirTemp("", prefix) }

// RemoveAll is rmSync(path, { recursive: true, force: true }).
func RemoveAll(path string) { os.RemoveAll(path) }
```

`internal/nodefs/path.go`:

```go
package nodefs

import (
	"os"
	"path/filepath"
)

// Resolve is path.resolve.
func Resolve(p ...string) string {
	joined := filepath.Join(p...)
	if filepath.IsAbs(joined) {
		return filepath.Clean(joined)
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, joined)
}

// Join is path.join.
func Join(p ...string) string { return filepath.Join(p...) }

// Dirname is path.dirname.
func Dirname(p string) string { return filepath.Dir(p) }

// IsAbsolute is path.isAbsolute.
func IsAbsolute(p string) bool { return filepath.IsAbs(p) }

// Relative is path.relative: "" when both resolve to the same path.
func Relative(from, to string) string {
	r, err := filepath.Rel(Resolve(from), Resolve(to))
	if err != nil {
		return Resolve(to)
	}
	if r == "." {
		return ""
	}
	return r
}
```

- [x] **Step 5: Run unit tests**

Run: `go test ./internal/nodefs/`
Expected: PASS.

- [x] **Step 6: Add the decode oracle op and live test**

Add to `handlers`:

```ts
  decodeUtf8: ({ hex }: { hex: string }) => Buffer.from(hex, "hex").toString("utf8"),
```

`difftest/nodefs_live_test.go`:

```go
package difftest

import (
	"encoding/hex"
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func TestLiveDecodeUTF8(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(9, 10))
	interesting := []byte{0x00, 0x41, 0x7F, 0x80, 0xBF, 0xC0, 0xC2, 0xDF, 0xE0, 0xE1, 0xED, 0xEF, 0xF0, 0xF1, 0xF4, 0xF5, 0xFF, 0xA0, 0x9F, 0x90, 0x8F}
	for i := 0; i < liveCases; i++ {
		b := make([]byte, r.IntN(12))
		for j := range b {
			b[j] = interesting[r.IntN(len(interesting))]
		}
		raw, err := c.Call("decodeUtf8", `{"hex":"`+hex.EncodeToString(b)+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if got := nodefs.DecodeUTF8(b); got != want.Str() {
			t.Fatalf("DecodeUTF8(% x) = %q, want %q", b, got, want.Str())
		}
	}
}
```

- [x] **Step 7: Run live test and commit**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveDecodeUTF8 -v`
Expected: PASS.

```bash
git add internal/nodefs difftest
git commit -m "feat(go): Node fs reading, path helpers, and error text

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: `schema` — zod v3 subset with exact issues and messages

**Files:**
- Create: `internal/schema/schema.go` (core, Parse, status), `internal/schema/issue.go` (issue construction, default messages, ErrorMessage), `internal/schema/scalars.go` (string, number, boolean, literal, enum), `internal/schema/composite.go` (object, array, record, optional, nullable, discriminated union, refine, default), `internal/schema/patterns.go` (zod regexes, UTF-16 helpers), `internal/schema/schema_test.go`, `difftest/schemadsl/dsl.go`, `difftest/gen/schema-dsl.ts`, `difftest/schema_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (add `zodDsl`), `difftest/gen/random.go` (add `RandomDSL`, `RandomDSLValue`)

**Interfaces:**
- Consumes: `jsjson`, `jsstr`.
- Produces (package `schema`):
  - `type Schema interface` (unexported parse method).
  - `Parse(s Schema, v jsjson.Value) (out jsjson.Value, issues []Issue, ok bool)`: zod `safeParse`. On failure, `out` is Undefined.
  - `type Issue struct{ Fields *jsjson.Obj }` holding the zod issue fields in zod's key order; `(Issue).Code() string`.
  - `ErrorMessage(issues []Issue) string`: zod v3 `ZodError.message`.
  - Constructors: `String(checks ...StringCheck)`, `Number(checks ...NumberCheck)`, `Boolean()`, `Literal(jsjson.Value)`, `LiteralString(string)`, `Enum(values ...string)`, `Array(item Schema, checks ...ArrayCheck)`, `Record(value Schema)`, `Optional(Schema)`, `Nullable(Schema)`, `Object(fields ...Field) *ObjectSchema` (strip mode), `F(key string, s Schema) Field` (use this outside the package; `go vet` rejects unkeyed literals of imported structs), with `(*ObjectSchema).Strict() *ObjectSchema` and `(*ObjectSchema).Extend(fields ...Field) *ObjectSchema`, `type Field struct{ Key string; Schema Schema }`, `DiscriminatedUnion(key string, options ...*ObjectSchema)`, `Refine(s Schema, pred func(jsjson.Value) bool, message string)`, `Default(s Schema, v jsjson.Value)`.
  - String checks: `Trim()` (zod `.trim()`: rewrites the value for later checks and for the output), `MinLength(n int)`, `MaxLength(n int)`, `Regex(m Matcher, message string)` (`message == ""` means zod's default), `Datetime()` (zod `datetime({ offset: true })`), `UUID()`. `type StringCheck func(c *ctx, s *string, path []any) bool` (a check may rewrite `*s`). `type Matcher func(string) bool`. `Pattern(re string) Matcher` compiles a Go regexp (ASCII patterns only).
  - Number checks: `Int()`, `Positive()`, `Nonnegative()`, `Gte(n float64)`, `Lte(n float64)`.
  - Array checks: `Length(n int)`, `MinItems(n int)`, `MaxItems(n int)`.
- Produces (package `schemadsl`, test support): `Build(dsl jsjson.Value) schema.Schema`, mirrored in TS by `buildZod(dsl)` in `difftest/gen/schema-dsl.ts`.

**zod v3 behavior to reproduce (zod 3.25.76, `node_modules/zod/v3/types.js`):**

- Status: each parse returns `valid`, `dirty` (issues recorded, value still produced), or `aborted`. A type mismatch aborts that node. Failed checks mark it dirty and every check still runs, in declaration order.
- Issues are appended in traversal order. Object: fields in schema shape order, then (strict mode) one `unrecognized_keys` issue listing extra input keys in JS property order. Strip mode drops extra keys silently. Array: length checks first, then elements. Every child is parsed and every child issue is recorded; then the object, array, or record is `aborted` if any child aborted (zod `ParseStatus.mergeObjectSync`/`mergeArray` return INVALID), otherwise `dirty` if any child or check was dirty.
- A result is valid only when no issue was recorded anywhere.
- `Refine` runs its predicate when the inner result is valid or dirty, not when aborted. A false predicate adds a `custom` issue.
- `Optional` accepts Undefined (a missing key). `Nullable` accepts null. `Default` substitutes its value for Undefined, then parses it.
- Output objects are built by assignment, so a `__proto__` key never becomes an own property (relevant for `Record` output). Drop it.
- `received` type names: `string`, `number` (`nan` for NaN), `boolean`, `undefined`, `null`, `array`, `object`. `Int()` failure is `invalid_type` with `expected:"integer"`, `received:"float"`.
- String length is UTF-16 length (`jsstr.Length`).

**Issue field order** (captured from zod 3.25.76; `path` is the full path; `message` is the default text unless a custom message is given):

| Issue | Fields in order |
| --- | --- |
| type mismatch | `code:"invalid_type"`, `expected`, `received`, `path`, `message` |
| enum type mismatch | `expected` (options joined `'A' \| 'B'`), `received`, `code:"invalid_type"`, `path`, `message` |
| `Int()` failure | `code:"invalid_type"`, `expected:"integer"`, `received:"float"`, `message`, `path` |
| literal | `received`, `code:"invalid_literal"`, `expected`, `path`, `message` |
| strict extra keys | `code:"unrecognized_keys"`, `keys`, `path`, `message` |
| regex | `validation:"regex"`, `code:"invalid_string"`, `message`, `path` |
| datetime | `code:"invalid_string"`, `validation:"datetime"`, `message`, `path` |
| uuid | `validation:"uuid"`, `code:"invalid_string"`, `message`, `path` |
| string/array/number too_small | `code:"too_small"`, `minimum`, `type`, `inclusive`, `exact`, `message`, `path` |
| string/array/number too_big | `code:"too_big"`, `maximum`, `type`, `inclusive`, `exact`, `message`, `path` |
| enum | `received`, `code:"invalid_enum_value"`, `options`, `path`, `message` |
| discriminator | `code:"invalid_union_discriminator"`, `options`, `path` (object path + discriminator key), `message` |
| refine | `code:"custom"`, `message`, `path` |

**Default messages (zod v3 `errorMap`):**

- `invalid_type`: `Required` when received is `undefined`, else `Expected <expected>, received <received>`. For enums, `expected` is the options joined as `'A' | 'B'`.
- `invalid_literal`: `Invalid literal value, expected <JSON.stringify(expected)>`.
- `unrecognized_keys`: `Unrecognized key(s) in object: 'k1', 'k2'`.
- `invalid_union_discriminator`: `Invalid discriminator value. Expected 'A' | 'B'`.
- `invalid_enum_value`: `Invalid enum value. Expected 'A' | 'B', received '<value>'`.
- `invalid_string`: `Invalid` for regex, `Invalid datetime`, `Invalid uuid`.
- `too_small`: array `Array must contain <exactly|at least|more than> N element(s)`; string `String must contain <exactly|at least|over> N character(s)`; number `Number must be <exactly equal to |greater than or equal to |greater than >N`.
- `too_big`: array `Array must contain <exactly|at most|less than> N element(s)`; string `String must contain <exactly|at most|under> N character(s)`; number `Number must be <exactly|less than or equal to|less than> N`.
- `custom`: the refine message (zod default `Invalid input`).

`positive()` is `too_small`, minimum 0, inclusive false. `nonnegative()` and `Gte(n)` are inclusive true. `Length(n)` sets `exact:true`, and failures report `too_small` or `too_big` with the same exact wording.

- [x] **Step 1: Write failing tests from captured zod output**

`internal/schema/schema_test.go`:

```go
package schema

import (
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func mustParse(t *testing.T, text string) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func expectMessage(t *testing.T, s Schema, input, want string) {
	t.Helper()
	_, issues, ok := Parse(s, mustParse(t, input))
	if ok {
		t.Fatalf("Parse(%s) succeeded", input)
	}
	if got := ErrorMessage(issues); got != want {
		t.Fatalf("Parse(%s) message =\n%s\nwant\n%s", input, got, want)
	}
}

func TestCapturedZodMessages(t *testing.T) {
	expectMessage(t, Object(Field{"a", String()}), `{"a":1}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"string\",\n    \"received\": \"number\",\n    \"path\": [\n      \"a\"\n    ],\n    \"message\": \"Expected string, received number\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}), `{}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"string\",\n    \"received\": \"undefined\",\n    \"path\": [\n      \"a\"\n    ],\n    \"message\": \"Required\"\n  }\n]")
	expectMessage(t, LiteralString("x"), `"y"`, "[\n  {\n    \"received\": \"y\",\n    \"code\": \"invalid_literal\",\n    \"expected\": \"x\",\n    \"path\": [],\n    \"message\": \"Invalid literal value, expected \\\"x\\\"\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}).Strict(), `{"a":"","z":1,"b":2,"1":3}`, "[\n  {\n    \"code\": \"unrecognized_keys\",\n    \"keys\": [\n      \"1\",\n      \"z\",\n      \"b\"\n    ],\n    \"path\": [],\n    \"message\": \"Unrecognized key(s) in object: '1', 'z', 'b'\"\n  }\n]")
	expectMessage(t, String(Regex(Pattern(`^a$`), "")), `"b"`, "[\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"Invalid\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(Regex(Pattern(`^a$`), "custom msg")), `"b"`, "[\n  {\n    \"validation\": \"regex\",\n    \"code\": \"invalid_string\",\n    \"message\": \"custom msg\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(Datetime()), `"nope"`, "[\n  {\n    \"code\": \"invalid_string\",\n    \"validation\": \"datetime\",\n    \"message\": \"Invalid datetime\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(UUID()), `"nope"`, "[\n  {\n    \"validation\": \"uuid\",\n    \"code\": \"invalid_string\",\n    \"message\": \"Invalid uuid\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(MinLength(1)), `""`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 1,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at least 1 character(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, String(MaxLength(2)), `"abc"`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 2,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at most 2 character(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Array(String(), Length(3)), `["a"]`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 3,\n    \"type\": \"array\",\n    \"inclusive\": true,\n    \"exact\": true,\n    \"message\": \"Array must contain exactly 3 element(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Array(String(), Length(3)), `["a","b","c","d"]`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 3,\n    \"type\": \"array\",\n    \"inclusive\": true,\n    \"exact\": true,\n    \"message\": \"Array must contain exactly 3 element(s)\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int()), `1.5`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"integer\",\n    \"received\": \"float\",\n    \"message\": \"Expected integer, received float\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int(), Positive()), `0`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 0,\n    \"type\": \"number\",\n    \"inclusive\": false,\n    \"exact\": false,\n    \"message\": \"Number must be greater than 0\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Nonnegative()), `-1`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 0,\n    \"type\": \"number\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"Number must be greater than or equal to 0\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Number(Int(), Lte(3)), `4`, "[\n  {\n    \"code\": \"too_big\",\n    \"maximum\": 3,\n    \"type\": \"number\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"Number must be less than or equal to 3\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Enum("A", "B"), `"C"`, "[\n  {\n    \"received\": \"C\",\n    \"code\": \"invalid_enum_value\",\n    \"options\": [\n      \"A\",\n      \"B\"\n    ],\n    \"path\": [],\n    \"message\": \"Invalid enum value. Expected 'A' | 'B', received 'C'\"\n  }\n]")
	expectMessage(t, DiscriminatedUnion("s", Object(Field{"s", LiteralString("A")}), Object(Field{"s", LiteralString("B")})), `{"s":"C"}`, "[\n  {\n    \"code\": \"invalid_union_discriminator\",\n    \"options\": [\n      \"A\",\n      \"B\"\n    ],\n    \"path\": [\n      \"s\"\n    ],\n    \"message\": \"Invalid discriminator value. Expected 'A' | 'B'\"\n  }\n]")
	expectMessage(t, Refine(String(), func(jsjson.Value) bool { return false }, "refine msg"), `"x"`, "[\n  {\n    \"code\": \"custom\",\n    \"message\": \"refine msg\",\n    \"path\": []\n  }\n]")
	expectMessage(t, Object(Field{"a", Array(Object(Field{"b", Number()}))}), `{"a":[{"b":"x"}]}`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"number\",\n    \"received\": \"string\",\n    \"path\": [\n      \"a\",\n      0,\n      \"b\"\n    ],\n    \"message\": \"Expected number, received string\"\n  }\n]")
	expectMessage(t, Object(Field{"a", String()}), `"str"`, "[\n  {\n    \"code\": \"invalid_type\",\n    \"expected\": \"object\",\n    \"received\": \"string\",\n    \"path\": [],\n    \"message\": \"Expected object, received string\"\n  }\n]")
}

func TestStripDropsUnknownKeysAndOptionalAbsence(t *testing.T) {
	s := Object(Field{"a", String()}, Field{"o", Optional(Number())})
	out, _, ok := Parse(s, mustParse(t, `{"z":1,"a":"x"}`))
	if !ok || jsjson.Stringify(out) != `{"a":"x"}` {
		t.Fatalf("out = %s ok=%v", jsjson.Stringify(out), ok)
	}
}

func TestTrimRewritesOutput(t *testing.T) {
	out, _, ok := Parse(String(Trim(), MinLength(1)), mustParse(t, `" \u00a0x\t"`))
	if !ok || out.Str() != "x" {
		t.Fatalf("out = %q ok=%v", out.Str(), ok)
	}
	expectMessage(t, String(Trim(), MinLength(1)), `"   "`, "[\n  {\n    \"code\": \"too_small\",\n    \"minimum\": 1,\n    \"type\": \"string\",\n    \"inclusive\": true,\n    \"exact\": false,\n    \"message\": \"String must contain at least 1 character(s)\",\n    \"path\": []\n  }\n]")
}

func TestRecordDropsProtoKey(t *testing.T) {
	out, _, ok := Parse(Record(String()), mustParse(t, `{"__proto__":"x","a":"y"}`))
	if !ok || jsjson.Stringify(out) != `{"a":"y"}` {
		t.Fatalf("out = %s ok=%v", jsjson.Stringify(out), ok)
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/schema/`
Expected: FAIL (undefined: Object).

- [x] **Step 3: Implement the core and issue construction**

`internal/schema/schema.go`:

```go
// Package schema reproduces the subset of zod v3 (3.25.76) that FaultLine's
// verifiers use: parsing, normalized output, issues, and ZodError.message.
package schema

import "github.com/Mizore66/faultline/internal/jsjson"

type status uint8

const (
	valid status = iota
	dirty
	aborted
)

func worst(a, b status) status { return max(a, b) }

type ctx struct{ issues []Issue }

// Schema is a zod schema. path elements are string keys or int indexes.
type Schema interface {
	parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status)
}

// Parse is zod safeParse.
func Parse(s Schema, v jsjson.Value) (jsjson.Value, []Issue, bool) {
	c := &ctx{}
	out, st := s.parse(c, v, nil)
	if st != valid || len(c.issues) > 0 {
		return jsjson.Value{}, c.issues, false
	}
	return out, nil, true
}

// receivedType is zod getParsedType for JSON values.
func receivedType(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.Undefined:
		return "undefined"
	case jsjson.Null:
		return "null"
	case jsjson.Bool:
		return "boolean"
	case jsjson.Number:
		if v.Num() != v.Num() {
			return "nan"
		}
		return "number"
	case jsjson.String:
		return "string"
	case jsjson.Array:
		return "array"
	}
	return "object"
}

func extend(path []any, elem any) []any {
	out := make([]any, len(path)+1)
	copy(out, path)
	out[len(path)] = elem
	return out
}
```

`internal/schema/issue.go`:

```go
package schema

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
)

type Issue struct{ Fields *jsjson.Obj }

func (i Issue) Code() string { return i.Fields.Field("code").Str() }

// kv is one issueData entry. A "message" entry with an Undefined value is
// zod's `message: check.message` when no custom message was given.
type kv struct {
	key   string
	value jsjson.Value
}

func str(s string) jsjson.Value  { return jsjson.MakeString(s) }
func num(n float64) jsjson.Value { return jsjson.MakeNumber(n) }
func boolean(b bool) jsjson.Value { return jsjson.MakeBool(b) }
func strList(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, s := range items {
		values[i] = str(s)
	}
	return jsjson.MakeArray(values)
}

func pathValue(path []any) jsjson.Value {
	values := make([]jsjson.Value, len(path))
	for i, p := range path {
		switch p := p.(type) {
		case string:
			values[i] = str(p)
		case int:
			values[i] = num(float64(p))
		}
	}
	return jsjson.MakeArray(values)
}

// addIssue ports zod makeIssue: {...issueData, path} then message, where keys
// already present in issueData keep their position.
func (c *ctx) addIssue(path []any, data []kv, defaultMessage string) {
	fields := jsjson.NewObj()
	for _, e := range data {
		fields.Set(e.key, e.value)
	}
	fields.Set("path", pathValue(path))
	if m := fields.Field("message"); m.Kind() == jsjson.Undefined {
		fields.Set("message", str(defaultMessage))
	}
	c.issues = append(c.issues, Issue{fields})
}

// joinValues is zod util.joinValues: strings become 'value'.
func joinValues(values []string, sep string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, sep)
}

func (c *ctx) invalidType(path []any, expected string, v jsjson.Value) {
	received := receivedType(v)
	message := "Expected " + expected + ", received " + received
	if received == "undefined" {
		message = "Required"
	}
	c.addIssue(path, []kv{{"code", str("invalid_type")}, {"expected", str(expected)}, {"received", str(received)}}, message)
}

// ErrorMessage is ZodError.message: JSON.stringify(issues, replacer, 2).
func ErrorMessage(issues []Issue) string {
	values := make([]jsjson.Value, len(issues))
	for i, issue := range issues {
		values[i] = jsjson.MakeObject(issue.Fields)
	}
	return jsjson.StringifyIndent(jsjson.MakeArray(values), "  ")
}

func tooSmallMessage(kind string, minimum float64, inclusive, exact bool) string {
	n := jsjson.FormatNumber(minimum)
	switch kind {
	case "array":
		return "Array must contain " + pick(exact, "exactly", pick(inclusive, "at least", "more than")) + " " + n + " element(s)"
	case "string":
		return "String must contain " + pick(exact, "exactly", pick(inclusive, "at least", "over")) + " " + n + " character(s)"
	}
	return "Number must be " + pick(exact, "exactly equal to ", pick(inclusive, "greater than or equal to ", "greater than ")) + n
}

func tooBigMessage(kind string, maximum float64, inclusive, exact bool) string {
	n := jsjson.FormatNumber(maximum)
	switch kind {
	case "array":
		return "Array must contain " + pick(exact, "exactly", pick(inclusive, "at most", "less than")) + " " + n + " element(s)"
	case "string":
		return "String must contain " + pick(exact, "exactly", pick(inclusive, "at most", "under")) + " " + n + " character(s)"
	}
	return "Number must be " + pick(exact, "exactly", pick(inclusive, "less than or equal to", "less than")) + " " + n
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (c *ctx) tooSmall(path []any, kind string, minimum float64, inclusive, exact bool) {
	c.addIssue(path, []kv{{"code", str("too_small")}, {"minimum", num(minimum)}, {"type", str(kind)}, {"inclusive", boolean(inclusive)}, {"exact", boolean(exact)}, {"message", jsjson.Value{}}}, tooSmallMessage(kind, minimum, inclusive, exact))
}

func (c *ctx) tooBig(path []any, kind string, maximum float64, inclusive, exact bool) {
	c.addIssue(path, []kv{{"code", str("too_big")}, {"maximum", num(maximum)}, {"type", str(kind)}, {"inclusive", boolean(inclusive)}, {"exact", boolean(exact)}, {"message", jsjson.Value{}}}, tooBigMessage(kind, maximum, inclusive, exact))
}
```

- [x] **Step 4: Implement scalars and patterns**

`internal/schema/patterns.go`:

```go
package schema

import "regexp"

// Matcher tests a JS string (WTF-8).
type Matcher func(string) bool

// Pattern compiles an ASCII-only JS regex source that has an identical RE2
// meaning. Anything RE2 cannot express (lookahead, UTF-16 counted classes) must
// be written as a Go Matcher instead.
func Pattern(source string) Matcher {
	re := regexp.MustCompile(source)
	return re.MatchString
}

// datetimePattern is zod's datetimeRegex({ precision: null, offset: true, local: false }).
var datetimePattern = regexp.MustCompile(`^((\d\d[2468][048]|\d\d[13579][26]|\d\d0[48]|[02468][048]00|[13579][26]00)-02-29|\d{4}-((0[13578]|1[02])-(0[1-9]|[12]\d|3[01])|(0[469]|11)-(0[1-9]|[12]\d|30)|(02)-(0[1-9]|1\d|2[0-8])))T([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d+)?)?(Z|([+-]\d{2}:?\d{2}))$`)

// uuidPattern is zod's uuidRegex (flag i).
var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}\b-[0-9a-f]{4}\b-[0-9a-f]{4}\b-[0-9a-f]{4}\b-[0-9a-f]{12}$`)
```

`internal/schema/scalars.go`:

```go
package schema

import (
	"math"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// StringCheck runs one zod string check; it may rewrite *s (zod .trim()).
type StringCheck func(c *ctx, s *string, path []any) bool

type stringSchema struct{ checks []StringCheck }

func String(checks ...StringCheck) Schema { return &stringSchema{checks} }

func (s *stringSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.String {
		c.invalidType(path, "string", v)
		return jsjson.Value{}, aborted
	}
	value := v.Str()
	st := valid
	for _, check := range s.checks {
		if !check(c, &value, path) {
			st = dirty
		}
	}
	return jsjson.MakeString(value), st
}

// Trim is zod .trim(): later checks and the output see the trimmed string.
func Trim() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		*s = jsstr.Trim(*s)
		return true
	}
}

func MinLength(n int) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if jsstr.Length(*s) >= n {
			return true
		}
		c.tooSmall(path, "string", float64(n), true, false)
		return false
	}
}

func MaxLength(n int) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if jsstr.Length(*s) <= n {
			return true
		}
		c.tooBig(path, "string", float64(n), true, false)
		return false
	}
}

func Regex(m Matcher, message string) StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if m(*s) {
			return true
		}
		msg := jsjson.Value{}
		if message != "" {
			msg = str(message)
		}
		c.addIssue(path, []kv{{"validation", str("regex")}, {"code", str("invalid_string")}, {"message", msg}}, "Invalid")
		return false
	}
}

func Datetime() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if datetimePattern.MatchString(*s) {
			return true
		}
		c.addIssue(path, []kv{{"code", str("invalid_string")}, {"validation", str("datetime")}, {"message", jsjson.Value{}}}, "Invalid datetime")
		return false
	}
}

func UUID() StringCheck {
	return func(c *ctx, s *string, path []any) bool {
		if uuidPattern.MatchString(*s) {
			return true
		}
		c.addIssue(path, []kv{{"validation", str("uuid")}, {"code", str("invalid_string")}, {"message", jsjson.Value{}}}, "Invalid uuid")
		return false
	}
}

type NumberCheck func(c *ctx, n float64, path []any) bool

type numberSchema struct{ checks []NumberCheck }

func Number(checks ...NumberCheck) Schema { return &numberSchema{checks} }

func (s *numberSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Number || math.IsNaN(v.Num()) {
		c.invalidType(path, "number", v)
		return jsjson.Value{}, aborted
	}
	st := valid
	for _, check := range s.checks {
		if !check(c, v.Num(), path) {
			st = dirty
		}
	}
	return v, st
}

func Int() NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if !math.IsInf(n, 0) && n == math.Trunc(n) {
			return true
		}
		c.addIssue(path, []kv{{"code", str("invalid_type")}, {"expected", str("integer")}, {"received", str("float")}, {"message", jsjson.Value{}}}, "Expected integer, received float")
		return false
	}
}

func Gte(minimum float64) NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n >= minimum {
			return true
		}
		c.tooSmall(path, "number", minimum, true, false)
		return false
	}
}

func Nonnegative() NumberCheck { return Gte(0) }

func Positive() NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n > 0 {
			return true
		}
		c.tooSmall(path, "number", 0, false, false)
		return false
	}
}

func Lte(maximum float64) NumberCheck {
	return func(c *ctx, n float64, path []any) bool {
		if n <= maximum {
			return true
		}
		c.tooBig(path, "number", maximum, true, false)
		return false
	}
}

type booleanSchema struct{}

func Boolean() Schema { return booleanSchema{} }

func (booleanSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Bool {
		c.invalidType(path, "boolean", v)
		return jsjson.Value{}, aborted
	}
	return v, valid
}

type literalSchema struct{ value jsjson.Value }

func Literal(v jsjson.Value) Schema   { return &literalSchema{v} }
func LiteralString(s string) Schema   { return &literalSchema{str(s)} }

// strictEqual is JS === for the primitive literals zod schemas use.
func strictEqual(a, b jsjson.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsjson.String:
		return a.Str() == b.Str()
	case jsjson.Number:
		return a.Num() == b.Num()
	case jsjson.Bool:
		return a.Bool() == b.Bool()
	case jsjson.Null, jsjson.Undefined:
		return true
	}
	return false
}

// parse ports ZodLiteral: `input.data !== this._def.value`. A missing key has
// received undefined, which JSON.stringify drops from the issue.
func (s *literalSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if !strictEqual(v, s.value) {
		c.addIssue(path, []kv{{"received", v}, {"code", str("invalid_literal")}, {"expected", s.value}}, "Invalid literal value, expected "+jsjson.Stringify(s.value))
		return jsjson.Value{}, aborted
	}
	return v, valid
}

type enumSchema struct{ values []string }

func Enum(values ...string) Schema { return &enumSchema{values} }

func (s *enumSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.String {
		expected, received := joinValues(s.values, " | "), receivedType(v)
		message := "Expected " + expected + ", received " + received
		if received == "undefined" {
			message = "Required"
		}
		c.addIssue(path, []kv{{"expected", str(expected)}, {"received", str(received)}, {"code", str("invalid_type")}}, message)
		return jsjson.Value{}, aborted
	}
	for _, option := range s.values {
		if option == v.Str() {
			return v, valid
		}
	}
	c.addIssue(path, []kv{{"received", v}, {"code", str("invalid_enum_value")}, {"options", strList(s.values)}},
		"Invalid enum value. Expected "+joinValues(s.values, " | ")+", received '"+v.Str()+"'")
	return jsjson.Value{}, aborted
}

```

- [x] **Step 5: Implement composites**

`internal/schema/composite.go`:

```go
package schema

import (
	"slices"

	"github.com/Mizore66/faultline/internal/jsjson"
)

type Field struct {
	Key    string
	Schema Schema
}

// F builds a Field; ported schemas use it to stay go-vet clean.
func F(key string, s Schema) Field { return Field{Key: key, Schema: s} }

type ObjectSchema struct {
	fields []Field
	strict bool
}

func Object(fields ...Field) *ObjectSchema { return &ObjectSchema{fields: fields} }

func (o *ObjectSchema) Strict() *ObjectSchema {
	return &ObjectSchema{fields: slices.Clone(o.fields), strict: true}
}

// Extend is zod .extend: {...shape, ...augmentation}. Existing keys keep their
// position with the new schema; new keys are appended. Strictness is kept.
func (o *ObjectSchema) Extend(fields ...Field) *ObjectSchema {
	out := &ObjectSchema{fields: slices.Clone(o.fields), strict: o.strict}
	for _, f := range fields {
		if i := slices.IndexFunc(out.fields, func(e Field) bool { return e.Key == f.Key }); i >= 0 {
			out.fields[i] = f
		} else {
			out.fields = append(out.fields, f)
		}
	}
	return out
}

func (o *ObjectSchema) field(key string) Schema {
	for _, f := range o.fields {
		if f.Key == key {
			return f.Schema
		}
	}
	return nil
}

func (o *ObjectSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	in := v.Obj()
	out := jsjson.NewObj()
	st := valid
	for _, f := range o.fields {
		child, childStatus := f.Schema.parse(c, in.Field(f.Key), extend(path, f.Key))
		st = worst(st, childStatus)
		if child.Kind() != jsjson.Undefined {
			out.Set(f.Key, child)
		}
	}
	if o.strict {
		var extra []string // for-in order: JS property order
		for _, k := range in.Keys() {
			if o.field(k) == nil {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			c.addIssue(path, []kv{{"code", str("unrecognized_keys")}, {"keys", strList(extra)}}, "Unrecognized key(s) in object: "+joinValues(extra, ", "))
			st = worst(st, dirty)
		}
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeObject(out), st
}

type ArrayCheck struct {
	min, max int
	exact    bool
}

func Length(n int) ArrayCheck   { return ArrayCheck{min: n, max: n, exact: true} }
func MinItems(n int) ArrayCheck { return ArrayCheck{min: n, max: -1} }
func MaxItems(n int) ArrayCheck { return ArrayCheck{min: -1, max: n} }

type arraySchema struct {
	item   Schema
	checks []ArrayCheck
}

func Array(item Schema, checks ...ArrayCheck) Schema { return &arraySchema{item, checks} }

func (a *arraySchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Array {
		c.invalidType(path, "array", v)
		return jsjson.Value{}, aborted
	}
	n := len(v.Items())
	st := valid
	for _, check := range a.checks {
		if check.exact {
			if n < check.min {
				c.tooSmall(path, "array", float64(check.min), true, true)
				st = dirty
			} else if n > check.max {
				c.tooBig(path, "array", float64(check.max), true, true)
				st = dirty
			}
			continue
		}
		if check.min >= 0 && n < check.min {
			c.tooSmall(path, "array", float64(check.min), true, false)
			st = dirty
		}
		if check.max >= 0 && n > check.max {
			c.tooBig(path, "array", float64(check.max), true, false)
			st = dirty
		}
	}
	items := make([]jsjson.Value, n)
	for i, item := range v.Items() {
		out, itemStatus := a.item.parse(c, item, extend(path, i))
		st = worst(st, itemStatus)
		items[i] = out
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeArray(items), st
}

type recordSchema struct{ value Schema }

func Record(value Schema) Schema { return &recordSchema{value} }

func (r *recordSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	out := jsjson.NewObj()
	st := valid
	for _, k := range v.Obj().Keys() {
		child, childStatus := r.value.parse(c, v.Obj().Field(k), extend(path, k))
		st = worst(st, childStatus)
		if k != "__proto__" {
			out.Set(k, child)
		}
	}
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	return jsjson.MakeObject(out), st
}

type optionalSchema struct{ inner Schema }

func Optional(inner Schema) Schema { return &optionalSchema{inner} }

func (o *optionalSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Undefined {
		return v, valid
	}
	return o.inner.parse(c, v, path)
}

type nullableSchema struct{ inner Schema }

func Nullable(inner Schema) Schema { return &nullableSchema{inner} }

func (n *nullableSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Null {
		return v, valid
	}
	return n.inner.parse(c, v, path)
}

type discriminatedSchema struct {
	key     string
	options []*ObjectSchema
	values  []string
}

// DiscriminatedUnion requires each option's discriminator to be a string literal.
func DiscriminatedUnion(key string, options ...*ObjectSchema) Schema {
	d := &discriminatedSchema{key: key, options: options}
	for _, o := range options {
		d.values = append(d.values, o.field(key).(*literalSchema).value.Str())
	}
	return d
}

func (d *discriminatedSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() != jsjson.Object {
		c.invalidType(path, "object", v)
		return jsjson.Value{}, aborted
	}
	disc := v.Obj().Field(d.key)
	for i, value := range d.values {
		if disc.Kind() == jsjson.String && disc.Str() == value {
			return d.options[i].parse(c, v, path)
		}
	}
	c.addIssue(extend(path, d.key), []kv{{"code", str("invalid_union_discriminator")}, {"options", strList(d.values)}},
		"Invalid discriminator value. Expected "+joinValues(d.values, " | "))
	return jsjson.Value{}, aborted
}

type refineSchema struct {
	inner   Schema
	pred    func(jsjson.Value) bool
	message string
}

func Refine(inner Schema, pred func(jsjson.Value) bool, message string) Schema {
	return &refineSchema{inner, pred, message}
}

func (r *refineSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	out, st := r.inner.parse(c, v, path)
	if st == aborted {
		return jsjson.Value{}, aborted
	}
	if !r.pred(out) {
		c.addIssue(path, []kv{{"code", str("custom")}, {"message", str(r.message)}}, "Invalid input")
		st = dirty
	}
	return out, st
}

type defaultSchema struct {
	inner Schema
	value jsjson.Value
}

func Default(inner Schema, v jsjson.Value) Schema { return &defaultSchema{inner, v} }

func (d *defaultSchema) parse(c *ctx, v jsjson.Value, path []any) (jsjson.Value, status) {
	if v.Kind() == jsjson.Undefined {
		v = d.value
	}
	return d.inner.parse(c, v, path)
}
```

- [x] **Step 6: Run unit tests**

Run: `go test ./internal/schema/`
Expected: PASS. If a captured message differs, the captured text wins. Fix the field order or status logic.

- [x] **Step 7: Build the schema DSL on both sides**

The DSL describes a schema as JSON so the oracle and Go build equivalent schemas. Regexes are named, not free-form, so both sides use known-equivalent patterns.

DSL grammar (a JSON value `S`):

```
{"t":"string","checks":[C...]}   C: {"k":"trim"} | {"k":"min","n":N} | {"k":"max","n":N} | {"k":"regex","name":"sha256"|"gitObjectId"|"identifier","msg":"..."|null} | {"k":"datetime"} | {"k":"uuid"}
{"t":"number","checks":[C...]}   C: {"k":"int"} | {"k":"positive"} | {"k":"nonnegative"} | {"k":"min","n":N} | {"k":"max","n":N}
{"t":"boolean"} | {"t":"literal","v":JSON} | {"t":"enum","values":["A",...]}
{"t":"array","item":S,"checks":[{"k":"length"|"min"|"max","n":N}...]}
{"t":"record","value":S} | {"t":"optional","inner":S} | {"t":"nullable","inner":S}
{"t":"object","strict":BOOL,"shape":[["key",S],...]}
{"t":"disc","key":"s","options":[S_object...]}
{"t":"refine","inner":S,"pred":"nonEmpty"|"alwaysFalse","msg":"..."}
{"t":"default","inner":S,"v":JSON}
```

Named regexes: `sha256` = `^sha256:[a-f0-9]{64}$`, `gitObjectId` = `^(?:[a-f0-9]{40}|[a-f0-9]{64})$`, `identifier` = `^[A-Za-z0-9][A-Za-z0-9._:-]*$`.

`difftest/gen/schema-dsl.ts`:

```ts
import { z, type ZodTypeAny } from "zod";

const regexes: Record<string, RegExp> = {
  sha256: /^sha256:[a-f0-9]{64}$/,
  gitObjectId: /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/,
  identifier: /^[A-Za-z0-9][A-Za-z0-9._:-]*$/,
};

export function buildZod(dsl: any): ZodTypeAny {
  switch (dsl.t) {
    case "string": {
      let s = z.string();
      for (const c of dsl.checks) {
        if (c.k === "trim") s = s.trim();
        else if (c.k === "min") s = s.min(c.n);
        else if (c.k === "max") s = s.max(c.n);
        else if (c.k === "regex") s = c.msg === null ? s.regex(regexes[c.name]!) : s.regex(regexes[c.name]!, c.msg);
        else if (c.k === "datetime") s = s.datetime({ offset: true });
        else if (c.k === "uuid") s = s.uuid();
      }
      return s;
    }
    case "number": {
      let n = z.number();
      for (const c of dsl.checks) {
        if (c.k === "int") n = n.int();
        else if (c.k === "positive") n = n.positive();
        else if (c.k === "nonnegative") n = n.nonnegative();
        else if (c.k === "min") n = n.min(c.n);
        else if (c.k === "max") n = n.max(c.n);
      }
      return n;
    }
    case "boolean": return z.boolean();
    case "literal": return z.literal(dsl.v);
    case "enum": return z.enum(dsl.values);
    case "array": {
      let a = z.array(buildZod(dsl.item));
      for (const c of dsl.checks) {
        if (c.k === "length") a = a.length(c.n);
        else if (c.k === "min") a = a.min(c.n);
        else if (c.k === "max") a = a.max(c.n);
      }
      return a;
    }
    case "record": return z.record(buildZod(dsl.value));
    case "optional": return buildZod(dsl.inner).optional();
    case "nullable": return buildZod(dsl.inner).nullable();
    case "object": {
      const shape = Object.fromEntries(dsl.shape.map(([k, s]: [string, any]) => [k, buildZod(s)]));
      const o = z.object(shape);
      return dsl.strict ? o.strict() : o;
    }
    case "disc": return z.discriminatedUnion(dsl.key, dsl.options.map(buildZod));
    case "refine": {
      const pred = dsl.pred === "nonEmpty" ? (v: unknown) => typeof v === "string" && v.length > 0 : () => false;
      return buildZod(dsl.inner).refine(pred, dsl.msg);
    }
    case "default": return buildZod(dsl.inner).default(dsl.v);
  }
  throw new Error(`unknown DSL node: ${dsl.t}`);
}
```

Add to the oracle:

```ts
import { buildZod } from "./schema-dsl.js";
```

```ts
  zodDsl: ({ schema, text }: { schema: unknown; text: string }) => {
    const result = buildZod(schema).safeParse(JSON.parse(text));
    if (!result.success) return { success: false, message: result.error.message };
    try {
      return { success: true, canonical: canonicalJson(result.data) };
    } catch (error) {
      return { success: true, canonical: `ERROR:${(error as Error).message}` };
    }
  },
```

`difftest/schemadsl/dsl.go` builds the same schemas in Go. The `shape` keys are unique object keys in DSL outputs, but `Object` needs order, so the DSL encodes shape as a list of pairs:

```go
// Package schemadsl builds internal/schema values from the test DSL that
// difftest/gen/schema-dsl.ts also understands.
package schemadsl

import (
	"fmt"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

var regexes = map[string]schema.Matcher{
	"sha256":      schema.Pattern(`^sha256:[a-f0-9]{64}$`),
	"gitObjectId": schema.Pattern(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`),
	"identifier":  schema.Pattern(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`),
}

func Build(dsl jsjson.Value) schema.Schema {
	o := dsl.Obj()
	checks := o.Field("checks").Items()
	switch t := o.Field("t").Str(); t {
	case "string":
		var cs []schema.StringCheck
		for _, c := range checks {
			switch c.Obj().Field("k").Str() {
			case "trim":
				cs = append(cs, schema.Trim())
			case "min":
				cs = append(cs, schema.MinLength(int(c.Obj().Field("n").Num())))
			case "max":
				cs = append(cs, schema.MaxLength(int(c.Obj().Field("n").Num())))
			case "regex":
				cs = append(cs, schema.Regex(regexes[c.Obj().Field("name").Str()], c.Obj().Field("msg").Str()))
			case "datetime":
				cs = append(cs, schema.Datetime())
			case "uuid":
				cs = append(cs, schema.UUID())
			}
		}
		return schema.String(cs...)
	case "number":
		var cs []schema.NumberCheck
		for _, c := range checks {
			switch c.Obj().Field("k").Str() {
			case "int":
				cs = append(cs, schema.Int())
			case "positive":
				cs = append(cs, schema.Positive())
			case "nonnegative":
				cs = append(cs, schema.Nonnegative())
			case "min":
				cs = append(cs, schema.Gte(c.Obj().Field("n").Num()))
			case "max":
				cs = append(cs, schema.Lte(c.Obj().Field("n").Num()))
			}
		}
		return schema.Number(cs...)
	case "boolean":
		return schema.Boolean()
	case "literal":
		return schema.Literal(o.Field("v"))
	case "enum":
		var values []string
		for _, v := range o.Field("values").Items() {
			values = append(values, v.Str())
		}
		return schema.Enum(values...)
	case "array":
		var cs []schema.ArrayCheck
		for _, c := range checks {
			n := int(c.Obj().Field("n").Num())
			switch c.Obj().Field("k").Str() {
			case "length":
				cs = append(cs, schema.Length(n))
			case "min":
				cs = append(cs, schema.MinItems(n))
			case "max":
				cs = append(cs, schema.MaxItems(n))
			}
		}
		return schema.Array(Build(o.Field("item")), cs...)
	case "record":
		return schema.Record(Build(o.Field("value")))
	case "optional":
		return schema.Optional(Build(o.Field("inner")))
	case "nullable":
		return schema.Nullable(Build(o.Field("inner")))
	case "object":
		return buildObject(dsl)
	case "disc":
		var options []*schema.ObjectSchema
		for _, opt := range o.Field("options").Items() {
			options = append(options, buildObject(opt))
		}
		return schema.DiscriminatedUnion(o.Field("key").Str(), options...)
	case "refine":
		pred := func(jsjson.Value) bool { return false }
		if o.Field("pred").Str() == "nonEmpty" {
			pred = func(v jsjson.Value) bool { return v.Kind() == jsjson.String && v.Str() != "" }
		}
		return schema.Refine(Build(o.Field("inner")), pred, o.Field("msg").Str())
	case "default":
		return schema.Default(Build(o.Field("inner")), o.Field("v"))
	default:
		panic(fmt.Sprintf("unknown DSL node %q", t))
	}
}

func buildObject(dsl jsjson.Value) *schema.ObjectSchema {
	var fields []schema.Field
	for _, pair := range dsl.Obj().Field("shape").Items() {
		fields = append(fields, schema.Field{Key: pair.Items()[0].Str(), Schema: Build(pair.Items()[1])})
	}
	obj := schema.Object(fields...)
	if dsl.Obj().Field("strict").Bool() {
		return obj.Strict()
	}
	return obj
}
```

In the TS builder, `dsl.shape` is also a list of pairs; `Object.fromEntries` keeps that order.

- [x] **Step 8: Write the random DSL generator and live test**

Append to `difftest/gen/random.go` (add `"github.com/Mizore66/faultline/internal/jsjson"` to its imports):

```go
func pick[T any](r *rand.Rand, items ...T) T { return items[r.IntN(len(items))] }

// randomNode returns a DSL schema and a value text that usually satisfies it.
func randomNode(r *rand.Rand, depth int) (string, string) {
	kinds := []string{"string", "number", "boolean", "literal", "enum", "array", "record", "optional", "nullable", "object", "disc", "refine", "default"}
	if depth >= 3 {
		kinds = kinds[:5]
	}
	switch pick(r, kinds...) {
	case "string":
		switch r.IntN(7) {
		case 6:
			return `{"t":"string","checks":[{"k":"trim"},{"k":"min","n":1},{"k":"max","n":3}]}`, pick(r, `"  ab  "`, `"   "`, `"\u2028abcd\ufeff"`, `" x "`)
		case 0:
			return `{"t":"string","checks":[{"k":"min","n":1},{"k":"max","n":4}]}`, pick(r, `"ab"`, `""`, `"abcdef"`, `"😀😀😀"`)
		case 1:
			return `{"t":"string","checks":[{"k":"regex","name":"sha256","msg":null}]}`, pick(r, `"sha256:`+strings.Repeat("a", 64)+`"`, `"sha256:zz"`)
		case 2:
			return `{"t":"string","checks":[{"k":"regex","name":"identifier","msg":"Identifier contains unsupported characters"},{"k":"max","n":8}]}`, pick(r, `"abc"`, `"-bad"`, `"waytoolong-identifier"`)
		case 3:
			return `{"t":"string","checks":[{"k":"datetime"}]}`, pick(r, `"2026-07-16T11:00:00.000Z"`, `"2026-02-30T00:00:00Z"`, `"2026-07-16T11:00:00+05:30"`, `"2026-07-16"`)
		case 4:
			return `{"t":"string","checks":[{"k":"uuid"}]}`, pick(r, `"123e4567-e89b-12d3-a456-426614174000"`, `"123E4567-E89B-12D3-A456-426614174000"`, `"nope"`)
		default:
			return `{"t":"string","checks":[]}`, `"x"`
		}
	case "number":
		return pick(r, `{"t":"number","checks":[{"k":"int"},{"k":"positive"}]}`, `{"t":"number","checks":[{"k":"nonnegative"}]}`, `{"t":"number","checks":[{"k":"int"},{"k":"min","n":1},{"k":"max","n":3}]}`),
			pick(r, "1", "0", "2.5", "-1", "3", "4", "1e400")
	case "boolean":
		return `{"t":"boolean"}`, pick(r, "true", "false")
	case "literal":
		return pick(r, `{"t":"literal","v":"x"}`, `{"t":"literal","v":false}`, `{"t":"literal","v":3}`), pick(r, `"x"`, "false", "3", `"y"`)
	case "enum":
		return `{"t":"enum","values":["A","B"]}`, pick(r, `"A"`, `"B"`, `"C"`, "1")
	case "array":
		d, v := randomNode(r, depth+1)
		checks := pick(r, `[]`, `[{"k":"length","n":2}]`, `[{"k":"min","n":1}]`, `[{"k":"max","n":1}]`)
		return `{"t":"array","item":` + d + `,"checks":` + checks + `}`, "[" + strings.Repeat(v+",", r.IntN(3)) + v + "]"
	case "record":
		d, v := randomNode(r, depth+1)
		return `{"t":"record","value":` + d + `}`, `{"a":` + v + `,"__proto__":` + v + `}`
	case "optional":
		d, v := randomNode(r, depth+1)
		return `{"t":"optional","inner":` + d + `}`, v
	case "nullable":
		d, v := randomNode(r, depth+1)
		return `{"t":"nullable","inner":` + d + `}`, pick(r, v, "null")
	case "object":
		d1, v1 := randomNode(r, depth+1)
		d2, v2 := randomNode(r, depth+1)
		return `{"t":"object","strict":` + pick(r, "true", "false") + `,"shape":[["a",` + d1 + `],["b",{"t":"optional","inner":` + d2 + `}],["c",{"t":"default","inner":{"t":"string","checks":[]},"v":"dflt"}]]}`,
			`{"a":` + v1 + `,"b":` + v2 + `}`
	case "disc":
		d, v := randomNode(r, depth+1)
		return `{"t":"disc","key":"s","options":[{"t":"object","strict":true,"shape":[["s",{"t":"literal","v":"A"}],["a",` + d + `]]},{"t":"object","strict":false,"shape":[["s",{"t":"literal","v":"B"}]]}]}`,
			pick(r, `{"s":"A","a":`+v+`}`, `{"s":"B","z":1}`, `{"s":"C"}`, `{"a":`+v+`}`, `"s"`)
	case "refine":
		return pick(r, `{"t":"refine","inner":{"t":"string","checks":[{"k":"max","n":3}]},"pred":"nonEmpty","msg":"must be non-empty"}`, `{"t":"refine","inner":{"t":"string","checks":[]},"pred":"alwaysFalse","msg":"never"}`),
			pick(r, `""`, `"ab"`, `"abcd"`, "1")
	default:
		d, v := randomNode(r, depth+1)
		return `{"t":"default","inner":` + d + `,"v":` + v + `}`, v
	}
}

var replacements = []string{"1", `"x"`, "null", "[]", "{}", "true", "1.5", "-1", `""`, `"sha256:zz"`}

// corruptValue replaces the whole value, or deletes, adds, or retypes one member.
func corruptValue(r *rand.Rand, text string) string {
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object || v.Obj().Len() == 0 || r.IntN(3) == 0 {
		return pick(r, replacements...)
	}
	o, keys := v.Obj(), v.Obj().Keys()
	switch r.IntN(3) {
	case 0:
		o.Delete(keys[r.IntN(len(keys))])
	case 1:
		o.Set("zzUnknown", jsjson.MakeNumber(1))
	default:
		repl, _ := jsjson.Parse(pick(r, replacements...))
		o.Set(keys[r.IntN(len(keys))], repl)
	}
	return jsjson.Stringify(v)
}

// RandomDSL returns a DSL schema and a JSON value text for it (about half invalid).
func RandomDSL(r *rand.Rand) (dsl string, value string) {
	d, v := randomNode(r, 0)
	if r.IntN(2) == 0 {
		v = corruptValue(r, v)
	}
	return d, v
}
```

`difftest/schema_live_test.go`:

```go
package difftest

import (
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/schemadsl"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func TestLiveSchemaDSL(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(11, 12))
	for i := 0; i < liveCases; i++ {
		dslText, valueText := gen.RandomDSL(r)
		want := call(t, c, "zodDsl", `{"schema":`+dslText+`,"text":`+jsjson.Quote(valueText)+`}`)
		dsl, _ := jsjson.Parse(dslText)
		value, _ := jsjson.Parse(valueText)
		out, issues, ok := schema.Parse(schemadsl.Build(dsl), value)
		if ok != want.Field("success").Bool() {
			t.Fatalf("schema %s value %s: ok=%v, zod success=%v (%s)", dslText, valueText, ok, want.Field("success").Bool(), want.Field("message").Str())
		}
		if ok {
			got, err := canonical.CanonicalJSON(out)
			if err != nil {
				got = "ERROR:" + err.Error()
			}
			if got != want.Field("canonical").Str() {
				t.Fatalf("schema %s value %s: data %s, zod %s", dslText, valueText, got, want.Field("canonical").Str())
			}
			continue
		}
		if got := schema.ErrorMessage(issues); got != want.Field("message").Str() {
			t.Fatalf("schema %s value %s:\ngot  %s\nwant %s", dslText, valueText, got, want.Field("message").Str())
		}
	}
}
```

- [x] **Step 9: Run live test**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveSchemaDSL -v`
Expected: PASS for 10,000 cases. Every mismatch is a zod behavior the Go code misses (issue order, abort vs dirty, message text). Fix `internal/schema`, add the failing case to `schema_test.go` as a regression test, and rerun.

- [x] **Step 10: Commit**

```bash
git add internal/schema difftest
git commit -m "feat(go): zod v3 subset schema library with exact issue messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Differential harness — bases, mutations, goldens, Go replay

**Files:**
- Create: `difftest/gen/fixtures.ts`, `difftest/gen/cases.ts`, `difftest/gen/gen-bases.ts`, `difftest/gen/gen-goldens.ts`, `difftest/testdata/mutations.json`, `difftest/cases.go`, `difftest/replay_test.go`, `difftest/testdata/bases/**` (generated), `difftest/testdata/golden/*.jsonl` (generated)

**Interfaces:**
- Consumes: `cli.Run`, `jsjson`, `canonical`, `nodefs`, `oracle.RepoRoot`.
- Produces: base ids with a type prefix (`git-`, `demo-`, `prevention-`); `difftest/testdata/golden/<base>.jsonl`, one JSON object per line `{case, inv, treeDigest, stdout, stderr, exit}` where `inv` is `plain`, `root-ok`, or `root-bad`; Go `var portedTypes map[string]bool` in `replay_test.go`, which verifier tasks extend (`"demo"`, `"prevention"`, `"git"`).

Case semantics are defined once below and implemented identically in `cases.ts` and `cases.go`. `treeDigest` proves both implementations built the same tree.

- **Files of a base:** every regular file, relative path with `/`, sorted by code unit (byte order for these ASCII names).
- **Cases:** `{id: base}` first, then for each template in `mutations.json` order, for each target file: `{id: base + "__" + template.id + "__" + file.replaceAll("/", "~")}`. `add-file` targets its own `path`. `file` patterns: `*` (all files), `*.json` (files ending `.json`), or an exact path.
- **Applicability** (a case is emitted only if true): `flip-byte` needs a non-empty file; `replace-text` needs `find` in the file bytes; `strip-trailing-newline` needs a final `\n` byte; `json-*` ops need the file to `JSON.parse` to a non-array object (`json-retype`/`json-drop-first` also need at least one key).
- **Ops:** `flip-byte` XORs byte `0` or the last byte (`offset: -1`) with `0x01`. `delete-file`. `make-dir` replaces the file with an empty directory. `symlink` replaces the file with a link to `target`. `add-file` writes `content` (creating parents). `replace-text` replaces the first occurrence of `find` with `replace`, bytewise. `strip-trailing-newline`. `prepend-bytes` (hex). `json-add-key` defines own property `key` = `value` at the root. `json-retype` sets the first key (JS property order) to `12345`, or to `"retyped"` if it was a number. `json-drop-first` deletes the first key. All `json-*` ops write `JSON.stringify(v, null, 2) + "\n"`.
- **`rehash: true`:** after the op, git/demo bases rewrite `hashes.txt` as `sha256hex(bytes) + "  " + path` lines for every regular file except `hashes.txt`/`ROOT.sha256`, sorted by `localeCompare`, joined with `\n` plus a trailing `\n`, and `ROOT.sha256` as `sha256:<hex of hashes text>\n`. Prevention bases set `manifest.prevention.digest = digestJson(prevention.json)` (only if `manifest.prevention` is an object) and `manifest.rootDigest = digestJson(manifest without rootDigest)`, then write the manifest as `JSON.stringify(v, null, 2) + "\n"`. If either JSON file fails to parse, or digesting throws, the rehash is skipped.
- **Invocations:** base cases (no mutation) run `plain`, `root-ok` (`--expect-root <base root>`), and `root-bad` (`--expect-root sha256:` followed by 64 `0`s). Mutated cases run `plain` only. The base root is `ROOT.sha256` trimmed (git/demo) or `manifest.rootDigest` (prevention).
- **treeDigest:** walk the tree in byte-sorted name order, emitting `F <rel> <sha256hex(bytes)>`, `D <rel>`, or `L <rel> <link target>`; the digest is `sha256hex` of the lines joined with `\n`.
- **Normalization** (spec §4.5), applied to stdout and stderr: (1) replace the bundle directory path and its realpath with `<BUNDLE>`; on Windows, turn `\` into `/` inside each `<BUNDLE>` path (until a quote, whitespace, or end); (2) replace every maximal run of non-whitespace, non-quote characters ending in `faultline-git-proof-verify-` followed by `[A-Za-z0-9]+` with `<GITTMP>`; (3) for each git label `L` in the spec list, replace the text after `L failed: ` up to (not including) the next `\n- ` or the final newline with `<GIT-DETAIL>`.
- **Git isolation:** `HOME` and `USERPROFILE` are an empty temp directory, `GIT_CONFIG_NOSYSTEM=1`, and `GIT_CONFIG_GLOBAL` is an empty file there. Both the golden generator and Go `TestMain` set this.

- [x] **Step 1: Write the fixtures module**

Create `difftest/gen/fixtures.ts` by copying, unchanged, the helpers `git`, `commit`, `createRepository`, `createFrozenWitness`, `deterministicDockerRunner`, `nativeDockerFixture`, `lifecycleBoundToDescendant`, and `lifecycleBoundToEveryInvestigatedState` from `tests/git-proof-bundle.test.ts:34-204`, and `digest`, `commit` (rename it `hexCommit`), `runIds`, and `sampleInput` from `tests/prevention-proof.test.ts:15-62`. Rewrite their imports from `../src/…` to `../../src/…`, drop the `vitest` import, and `export` every helper plus `pinnedImage` (`tests/git-proof-bundle.test.ts:32`). Make exactly these changes:

```ts
export function createRepository(objectFormat: "sha1" | "sha256" = "sha1"): { root: string; ancestor: string; descendant: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-git-proof-repository-"));
  git(root, ["init", `--object-format=${objectFormat}`]);
  git(root, ["config", "user.email", "faultline@example.test"]);
  git(root, ["config", "user.name", "FaultLine Test"]);
  const ancestor = commit(root, "good", "known good");
  commit(root, "bad", "regression");
  const descendant = commit(root, "repaired", "repair");
  return { root, ancestor, descendant };
}

/** The grounded input from tests/prevention-proof.test.ts:104-130. */
export function groundedPreventionInput(): PreventionProofWriteInput {
  const ids = runIds("rp");
  const witness = digest("witness");
  const environment = digest("environment");
  return sampleInput({
    grounding: "VERIFIED",
    repairBaseTree: hexCommit(22),
    repairPatchDigest: digest("repair-patch"),
    repaired: {
      commit: hexCommit(3), tree: hexCommit(33), witnessDigest: witness, environmentDigest: environment,
      executionTrust: "NATIVE_DOCKER", executionKind: "EXECUTED", distinctExecutionCount: 3, runIds: ids
    },
    repairedRunBindings: ids.map((runId, index) => ({
      runId, executionId: digest(`exec-${index}`), commit: hexCommit(3), tree: hexCommit(33), verdict: "PASS" as const,
      witnessDigest: witness, environmentDigest: environment, executionTrust: "NATIVE_DOCKER" as const, executionKind: "EXECUTED" as const
    }))
  });
}
```

- [x] **Step 2: Write the base generator and generate bases**

`difftest/gen/gen-bases.ts`:

```ts
// Writes difftest/testdata/bases/*. Run from the repo root:
//   pnpm exec tsx difftest/gen/gen-bases.ts
import { cpSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { DemoAnalysis } from "../../src/domain.js";
import { createDemoAnalysis } from "../../src/engine.js";
import { investigateGitRange } from "../../src/git-investigation.js";
import { writeGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { defaultPreventionProofRoot, writePreventionProof } from "../../src/prevention-proof.js";
import { writeProofBundle } from "../../src/proof-bundle.js";
import {
  createFrozenWitness, createRepository, deterministicDockerRunner, groundedPreventionInput,
  lifecycleBoundToDescendant, lifecycleBoundToEveryInvestigatedState, nativeDockerFixture, pinnedImage, sampleInput
} from "./fixtures.js";

const basesDir = resolve("difftest/testdata/bases");
const work = mkdtempSync(join(tmpdir(), "faultline-difftest-bases-"));

function place(id: string, directory: string): void {
  const target = join(basesDir, id);
  rmSync(target, { recursive: true, force: true });
  cpSync(directory, target, { recursive: true });
  console.log(`wrote ${id}`);
}

async function gitBase(id: string, ancestor: string, objectFormat: "sha1" | "sha256", lifecycle?: "descendant" | "every"): Promise<void> {
  const root = mkdtempSync(join(work, `${id}-`));
  const repository = createRepository(objectFormat);
  const frozen = createFrozenWitness(join(root, "witness-lock"));
  const observed = await investigateGitRange({
    repository: repository.root,
    range: { ancestor, descendant: "HEAD" },
    frozenWitness: frozen,
    expectedFrozenDigest: frozen.frozenDigest,
    sandbox: { mode: "DOCKER_ISOLATED", image: pinnedImage },
    runner: deterministicDockerRunner()
  });
  const result = nativeDockerFixture(observed);
  const lifecycleLedger = lifecycle === "descendant"
    ? lifecycleBoundToDescendant(repository.root)
    : lifecycle === "every"
      ? lifecycleBoundToEveryInvestigatedState(repository.root, result.states.map((state) => state.commit))
      : undefined;
  const written = writeGitInvestigationProofBundle(join(root, "proofs", id), result, frozen, {
    proofRoot: join(root, "proofs"),
    generatedAt: "2026-07-16T11:03:00.000Z",
    ...(lifecycleLedger === undefined ? {} : { lifecycleLedger })
  });
  place(id, written.directory);
}

function demoBase(id: string, mode: "REPLAY" | "RERUN", edit?: (analysis: DemoAnalysis) => void): void {
  const root = mkdtempSync(join(work, `${id}-`));
  const analysis = createDemoAnalysis(mode);
  edit?.(analysis);
  const bundle = writeProofBundle(join(root, "bundles", id), analysis, { proofRoot: join(root, "bundles") });
  place(id, bundle.directory);
}

function preventionBase(id: string, input: Parameters<typeof writePreventionProof>[1]): void {
  const root = mkdtempSync(join(work, `${id}-`));
  const previous = process.cwd();
  process.chdir(root);
  try {
    const written = writePreventionProof(join(defaultPreventionProofRoot(), id), input);
    place(id, written.directory);
  } finally {
    process.chdir(previous);
  }
}

cpSync(resolve("docs/samples/self-incident-commit-proof"), join(basesDir, "git-sample-self-incident"), { recursive: true });
await gitBase("git-unbound", "HEAD~2", "sha1");
await gitBase("git-partially-bound", "HEAD~2", "sha1", "descendant");
await gitBase("git-fully-bound", "HEAD~2", "sha1", "every");
await gitBase("git-two-states", "HEAD~1", "sha1");
demoBase("demo-replay", "REPLAY");
demoBase("demo-rerun", "RERUN");
demoBase("demo-rerun-unicode-stdout", "RERUN", (analysis) => {
  analysis.runCatalog[0]!.stdout = "héllo 😀\nsecond line\n";
});
demoBase("demo-rerun-lone-surrogate-stdout", "RERUN", (analysis) => {
  analysis.runCatalog[0]!.stdout = "bad \ud800 unit\n";
});
preventionBase("prevention-summary", sampleInput());
preventionBase("prevention-verified", groundedPreventionInput());
rmSync(work, { recursive: true, force: true });
```

Run: `pnpm install --frozen-lockfile && pnpm exec tsx difftest/gen/gen-bases.ts`

> **Amendment (2026-09-25, during execution):** the `git-sha256` base was dropped. Frozen TS cannot write a SHA-256 Git proof bundle: its pre-publish self-verification runs `git init --bare` (always SHA-1, `src/git-proof-bundle.ts:931`) and git rejects fetching the SHA-256 bundle (`pack is corrupted (SHA1 mismatch)`, reproduced on git 2.43 and 2.51). Recorded in `difftest/KNOWN_DIFFERENCES.md`.
Expected: `wrote …` for 10 generated bases; with the copied sample that makes 11 directories, and `node dist/cli.js verify difftest/testdata/bases/git-unbound` (after `pnpm build`) prints `Git proof self-consistency: VALID`. The lone-surrogate demo base is expected to be INVALID (`stdout does not match catalog`).

- [x] **Step 3: Write the mutation templates**

`difftest/testdata/mutations.json`:

```json
[
  {"id": "flip-first", "op": "flip-byte", "file": "*", "offset": 0},
  {"id": "flip-last", "op": "flip-byte", "file": "*", "offset": -1},
  {"id": "delete", "op": "delete-file", "file": "*"},
  {"id": "make-dir", "op": "make-dir", "file": "*"},
  {"id": "symlink-to-hashes", "op": "symlink", "file": "*", "target": "hashes.txt"},
  {"id": "symlink-outside", "op": "symlink", "file": "manifest.json", "target": "../outside.json"},
  {"id": "add-root-stray", "op": "add-file", "path": "stray.txt", "content": "stray\n"},
  {"id": "add-nested-stray", "op": "add-file", "path": "runs/stray.json", "content": "{}\n"},
  {"id": "add-unknown-key", "op": "json-add-key", "file": "*.json", "key": "zzUnknown", "value": 1, "rehash": true},
  {"id": "add-proto-key", "op": "json-add-key", "file": "*.json", "key": "__proto__", "value": "x", "rehash": true},
  {"id": "retype-first", "op": "json-retype", "file": "*.json", "rehash": true},
  {"id": "drop-first", "op": "json-drop-first", "file": "*.json", "rehash": true},
  {"id": "truncate-json", "op": "replace-text", "file": "*.json", "find": "}\n", "replace": "", "rehash": true},
  {"id": "bom-json", "op": "prepend-bytes", "file": "*.json", "hex": "efbbbf", "rehash": true},
  {"id": "invalid-utf8-json", "op": "prepend-bytes", "file": "*.json", "hex": "ff", "rehash": true},
  {"id": "hashes-traversal", "op": "replace-text", "file": "hashes.txt", "find": "  manifest.json", "replace": "  ../manifest.json"},
  {"id": "hashes-absolute", "op": "replace-text", "file": "hashes.txt", "find": "  manifest.json", "replace": "  /manifest.json"},
  {"id": "hashes-backslash", "op": "replace-text", "file": "hashes.txt", "find": "  manifest.json", "replace": "  .\\manifest.json"},
  {"id": "hashes-duplicate", "op": "replace-text", "file": "hashes.txt", "find": "  manifest.json\n", "replace": "  manifest.json\n0000000000000000000000000000000000000000000000000000000000000000  manifest.json\n"},
  {"id": "hashes-malformed", "op": "replace-text", "file": "hashes.txt", "find": "  ", "replace": " "},
  {"id": "hashes-no-final-newline", "op": "strip-trailing-newline", "file": "hashes.txt"},
  {"id": "root-mismatch", "op": "replace-text", "file": "ROOT.sha256", "find": "sha256:", "replace": "sha256:0"},
  {"id": "lifecycle-status-legacy", "op": "replace-text", "file": "manifest.json", "find": "\"status\":\"PARTIALLY_BOUND\"", "replace": "\"status\":\"BOUND\"", "rehash": true},
  {"id": "prevention-unverified", "op": "replace-text", "file": "manifest.json", "find": "PREVENTION_VERIFIED", "replace": "PREVENTION_EVIDENCE_SUMMARY", "rehash": true}
]
```

- [x] **Step 4: Write the TS case engine**

`difftest/gen/cases.ts`:

```ts
import { lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";

export type Template = {
  id: string; op: string; file?: string; path?: string; offset?: number; target?: string; content?: string;
  find?: string; replace?: string; hex?: string; key?: string; value?: unknown; rehash?: boolean;
};
export type Case = { id: string; base: string; template?: Template; file?: string };

const byCodeUnit = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

export function listFiles(root: string, rel = ""): string[] {
  const out: string[] = [];
  for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
    const r = rel ? `${rel}/${name}` : name;
    const stat = lstatSync(join(root, r));
    if (stat.isDirectory()) out.push(...listFiles(root, r));
    else if (stat.isFile()) out.push(r);
  }
  return out.sort(byCodeUnit);
}

function isObjectJson(path: string, needKey: boolean): boolean {
  try {
    const v = JSON.parse(readFileSync(path, "utf8"));
    return v !== null && typeof v === "object" && !Array.isArray(v) && (!needKey || Object.keys(v).length > 0);
  } catch {
    return false;
  }
}

function applicable(t: Template, path: string): boolean {
  switch (t.op) {
    case "flip-byte": return readFileSync(path).length > 0;
    case "replace-text": return readFileSync(path).toString("latin1").includes(t.find!);
    case "strip-trailing-newline": return readFileSync(path).at(-1) === 0x0a;
    case "json-add-key": return isObjectJson(path, false);
    case "json-retype": case "json-drop-first": return isObjectJson(path, true);
    default: return true;
  }
}

export function expandCases(base: string, root: string, templates: Template[]): Case[] {
  const files = listFiles(root);
  const cases: Case[] = [{ id: base, base }];
  for (const t of templates) {
    const targets = t.op === "add-file"
      ? [t.path!]
      : files.filter((f) => t.file === "*" || (t.file === "*.json" ? f.endsWith(".json") : t.file === f));
    for (const file of targets) {
      if (t.op !== "add-file" && !applicable(t, join(root, file))) continue;
      cases.push({ id: `${base}__${t.id}__${file.replaceAll("/", "~")}`, base, template: t, file });
    }
  }
  return cases;
}

function defineOwn(o: Record<string, unknown>, key: string, value: unknown): void {
  Object.defineProperty(o, key, { value, enumerable: true, writable: true, configurable: true });
}

function rehash(root: string, base: string): void {
  if (base.startsWith("prevention-")) {
    let manifest: Record<string, unknown>;
    let body: unknown;
    try {
      manifest = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8"));
      body = JSON.parse(readFileSync(join(root, "prevention.json"), "utf8"));
      if (manifest === null || typeof manifest !== "object" || Array.isArray(manifest)) return;
      const prevention = manifest.prevention;
      if (prevention !== null && typeof prevention === "object" && !Array.isArray(prevention)) {
        (prevention as Record<string, unknown>).digest = digestJson(body);
      }
      const { rootDigest: _dropped, ...unsigned } = manifest;
      manifest.rootDigest = digestJson(unsigned);
    } catch {
      return;
    }
    writeFileSync(join(root, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
    return;
  }
  const files = listFiles(root).filter((f) => f !== "hashes.txt" && f !== "ROOT.sha256");
  const hashes = `${[...files].sort((l, r) => l.localeCompare(r)).map((f) => `${sha256(readFileSync(join(root, f)))}  ${f}`).join("\n")}\n`;
  writeFileSync(join(root, "hashes.txt"), hashes);
  writeFileSync(join(root, "ROOT.sha256"), `sha256:${sha256(hashes)}\n`);
}

export function applyMutation(root: string, c: Case): void {
  const t = c.template;
  if (!t) return;
  const path = join(root, c.file!);
  switch (t.op) {
    case "flip-byte": {
      const b = readFileSync(path);
      b[t.offset === -1 ? b.length - 1 : 0]! ^= 0x01;
      writeFileSync(path, b);
      break;
    }
    case "delete-file": rmSync(path); break;
    case "make-dir": rmSync(path); mkdirSync(path); break;
    case "symlink": rmSync(path); symlinkSync(t.target!, path); break;
    case "add-file": mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, t.content!); break;
    case "replace-text": {
      const s = readFileSync(path).toString("latin1");
      writeFileSync(path, Buffer.from(s.replace(t.find!, () => t.replace!), "latin1"));
      break;
    }
    case "strip-trailing-newline": { const b = readFileSync(path); writeFileSync(path, b.subarray(0, b.length - 1)); break; }
    case "prepend-bytes": writeFileSync(path, Buffer.concat([Buffer.from(t.hex!, "hex"), readFileSync(path)])); break;
    case "json-add-key": case "json-retype": case "json-drop-first": {
      const v = JSON.parse(readFileSync(path, "utf8")) as Record<string, unknown>;
      const first = Object.keys(v)[0]!;
      if (t.op === "json-add-key") defineOwn(v, t.key!, t.value);
      else if (t.op === "json-retype") defineOwn(v, first, typeof v[first] === "number" ? "retyped" : 12345);
      else delete v[first];
      writeFileSync(path, `${JSON.stringify(v, null, 2)}\n`);
      break;
    }
    default: throw new Error(`unknown op ${t.op}`);
  }
  if (t.rehash) rehash(root, c.base);
}

export function treeDigest(root: string): string {
  const lines: string[] = [];
  const walk = (rel: string) => {
    for (const name of readdirSync(join(root, rel)).sort(byCodeUnit)) {
      const r = rel ? `${rel}/${name}` : name;
      const path = join(root, r);
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) lines.push(`L ${r} ${readlinkSync(path)}`);
      else if (stat.isDirectory()) { lines.push(`D ${r}`); walk(r); }
      else lines.push(`F ${r} ${sha256(readFileSync(path))}`);
    }
  };
  walk("");
  return sha256(lines.join("\n"));
}

export const GIT_LABELS = [
  "Git bundle head listing", "Temporary Git verifier initialization", "Git bundle verification",
  "Git bundle extraction", "Git commit resolution", "Git tree resolution", "Git bundle range enumeration",
  "Git binary range patch creation", "Git bundle object-format resolution"
];

function isBoundary(c: string | undefined): boolean {
  return c === undefined || c === "'" || c === "\"" || /\s/.test(c);
}

export function normalize(text: string, bundle: string): string {
  let out = text;
  for (const p of new Set([bundle, realpathSync(bundle)])) out = out.split(p).join("<BUNDLE>");
  const marker = "faultline-git-proof-verify-";
  for (let i = out.indexOf(marker); i >= 0; i = out.indexOf(marker, i)) {
    let start = i;
    while (start > 0 && !isBoundary(out[start - 1])) start--;
    let end = i + marker.length;
    while (end < out.length && /[A-Za-z0-9]/.test(out[end]!)) end++;
    out = `${out.slice(0, start)}<GITTMP>${out.slice(end)}`;
    i = start + "<GITTMP>".length;
  }
  for (const label of GIT_LABELS) {
    const needle = `${label} failed: `;
    for (let i = out.indexOf(needle); i >= 0; i = out.indexOf(needle, i + needle.length)) {
      const from = i + needle.length;
      let to = out.indexOf("\n- ", from);
      if (to < 0) to = out.endsWith("\n") ? out.length - 1 : out.length;
      out = `${out.slice(0, from)}<GIT-DETAIL>${out.slice(to)}`;
    }
  }
  return out;
}

export function baseRoot(root: string, base: string): string {
  try {
    if (base.startsWith("prevention-")) return JSON.parse(readFileSync(join(root, "manifest.json"), "utf8")).rootDigest;
    return readFileSync(join(root, "ROOT.sha256"), "utf8").trim();
  } catch {
    return "missing";
  }
}
```

- [x] **Step 5: Write the golden generator and generate goldens**

`difftest/gen/gen-goldens.ts`:

```ts
// Regenerates difftest/testdata/golden/*.jsonl from frozen TS.
//   pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts
import { spawn } from "node:child_process";
import { cpSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applyMutation, baseRoot, expandCases, normalize, treeDigest, type Case, type Template } from "./cases.js";

const repo = resolve(".");
const cli = join(repo, "dist", "cli.js");
const home = mkdtempSync(join(tmpdir(), "faultline-difftest-home-"));
writeFileSync(join(home, "empty.gitconfig"), "");
const env = { ...process.env, HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(home, "empty.gitconfig"), LC_ALL: "C.UTF-8" };
const templates = JSON.parse(readFileSync(join(repo, "difftest/testdata/mutations.json"), "utf8")) as Template[];
const ROOT_BAD = `sha256:${"0".repeat(64)}`;

function run(args: string[]): Promise<{ stdout: string; stderr: string; exit: number }> {
  return new Promise((done) => {
    const child = spawn(process.execPath, [cli, "verify", ...args], { env, cwd: repo });
    const out: Buffer[] = [];
    const err: Buffer[] = [];
    child.stdout.on("data", (b) => out.push(b));
    child.stderr.on("data", (b) => err.push(b));
    child.on("close", (code) => done({ stdout: Buffer.concat(out).toString("utf8"), stderr: Buffer.concat(err).toString("utf8"), exit: code ?? -1 }));
  });
}

async function golden(c: Case, root: string, rootDigest: string): Promise<string[]> {
  const work = mkdtempSync(join(tmpdir(), "faultline-difftest-"));
  const bundle = join(work, "bundle");
  try {
    cpSync(root, bundle, { recursive: true });
    applyMutation(bundle, c);
    const digest = treeDigest(bundle);
    const invocations: Array<[string, string[]]> = c.template
      ? [["plain", [bundle]]]
      : [["plain", [bundle]], ["root-ok", [bundle, "--expect-root", rootDigest]], ["root-bad", [bundle, "--expect-root", ROOT_BAD]]];
    const lines: string[] = [];
    for (const [inv, args] of invocations) {
      const r = await run(args);
      lines.push(JSON.stringify({ case: c.id, inv, treeDigest: digest, stdout: normalize(r.stdout, bundle), stderr: normalize(r.stderr, bundle), exit: r.exit }));
    }
    return lines;
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

const basesDir = join(repo, "difftest/testdata/bases");
for (const base of readdirSync(basesDir).sort()) {
  const root = join(basesDir, base);
  const cases = expandCases(base, root, templates);
  const rootDigest = baseRoot(root, base);
  const results: string[][] = new Array(cases.length);
  let next = 0;
  await Promise.all(Array.from({ length: 8 }, async () => {
    while (next < cases.length) {
      const index = next++;
      results[index] = await golden(cases[index]!, root, rootDigest);
    }
  }));
  writeFileSync(join(repo, "difftest/testdata/golden", `${base}.jsonl`), `${results.flat().join("\n")}\n`);
  console.log(`${base}: ${cases.length} cases`);
}
rmSync(home, { recursive: true, force: true });
```

Run: `mkdir -p difftest/testdata/golden && pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts`
Expected: one line per base with its case count. Run it a second time and check `git diff --exit-code difftest/testdata/golden` shows no changes (goldens must be deterministic). If they differ, find the nondeterminism (for example an unnormalized temp path) and extend normalization in both engines before continuing.

- [x] **Step 6: Write the Go case engine**

`difftest/cases.go` mirrors `cases.ts` function for function:

```go
package difftest

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

type template struct{ fields *jsjson.Obj }

func (t template) str(k string) string { return t.fields.Field(k).Str() }

type testCase struct {
	id, base, file string
	tpl            *template
}

func listFiles(root, rel string) []string {
	names, _ := nodefs.ReadDirNames(filepath.Join(root, rel))
	var out []string
	for _, name := range names {
		r := name
		if rel != "" {
			r = rel + "/" + name
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(r)))
		if err != nil {
			continue
		}
		if info.IsDir() {
			out = append(out, listFiles(root, r)...)
		} else if info.Mode().IsRegular() {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

func parseObjectFile(path string) (*jsjson.Obj, bool) {
	text, err := nodefs.ReadText(path)
	if err != nil {
		return nil, false
	}
	v, err := jsjson.Parse(text)
	if err != nil || v.Kind() != jsjson.Object {
		return nil, false
	}
	return v.Obj(), true
}

func applicable(t template, path string) bool {
	b, _ := os.ReadFile(path)
	switch t.str("op") {
	case "flip-byte":
		return len(b) > 0
	case "replace-text":
		return bytes.Contains(b, latin1(t.str("find")))
	case "strip-trailing-newline":
		return len(b) > 0 && b[len(b)-1] == '\n'
	case "json-add-key":
		_, ok := parseObjectFile(path)
		return ok
	case "json-retype", "json-drop-first":
		o, ok := parseObjectFile(path)
		return ok && o.Len() > 0
	}
	return true
}

// latin1 is Buffer.from(s, "latin1") for the ASCII find/replace strings.
func latin1(s string) []byte {
	units := jsstr.ToUTF16(s)
	out := make([]byte, len(units))
	for i, u := range units {
		out[i] = byte(u)
	}
	return out
}

func expandCases(base, root string, templates []template) []testCase {
	files := listFiles(root, "")
	cases := []testCase{{id: base, base: base}}
	for i := range templates {
		t := templates[i]
		var targets []string
		if t.str("op") == "add-file" {
			targets = []string{t.str("path")}
		} else {
			for _, f := range files {
				pattern := t.str("file")
				if pattern == "*" || (pattern == "*.json" && strings.HasSuffix(f, ".json")) || pattern == f {
					targets = append(targets, f)
				}
			}
		}
		for _, file := range targets {
			if t.str("op") != "add-file" && !applicable(t, filepath.Join(root, filepath.FromSlash(file))) {
				continue
			}
			cases = append(cases, testCase{id: base + "__" + t.str("id") + "__" + strings.ReplaceAll(file, "/", "~"), base: base, file: file, tpl: &templates[i]})
		}
	}
	return cases
}

func writeJSON(path string, v jsjson.Value) {
	os.WriteFile(path, []byte(jsstr.ToUTF8(jsjson.StringifyIndent(v, "  ")+"\n")), 0o644)
}

func rehash(root, base string) {
	if strings.HasPrefix(base, "prevention-") {
		manifest, ok := parseObjectFile(filepath.Join(root, "manifest.json"))
		if !ok {
			return
		}
		bodyText, err := nodefs.ReadText(filepath.Join(root, "prevention.json"))
		if err != nil {
			return
		}
		body, err := jsjson.Parse(bodyText)
		if err != nil {
			return
		}
		if p := manifest.Field("prevention"); p.Kind() == jsjson.Object {
			d, err := canonical.DigestJSON(body)
			if err != nil {
				return
			}
			p.Obj().Set("digest", jsjson.MakeString(d))
		}
		unsigned := jsjson.NewObj()
		for _, k := range manifest.Keys() {
			if k != "rootDigest" {
				unsigned.Set(k, manifest.Field(k))
			}
		}
		d, err := canonical.DigestJSON(jsjson.MakeObject(unsigned))
		if err != nil {
			return
		}
		manifest.Set("rootDigest", jsjson.MakeString(d))
		writeJSON(filepath.Join(root, "manifest.json"), jsjson.MakeObject(manifest))
		return
	}
	var files []string
	for _, f := range listFiles(root, "") {
		if f != "hashes.txt" && f != "ROOT.sha256" {
			files = append(files, f)
		}
	}
	var b strings.Builder
	for i, f := range canonical.SortLocale(files) {
		if i > 0 {
			b.WriteByte('\n')
		}
		data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		b.WriteString(canonical.SHA256HexBytes(data) + "  " + f)
	}
	hashes := b.String() + "\n"
	os.WriteFile(filepath.Join(root, "hashes.txt"), []byte(hashes), 0o644)
	os.WriteFile(filepath.Join(root, "ROOT.sha256"), []byte("sha256:"+canonical.SHA256Hex(hashes)+"\n"), 0o644)
}

// applyMutation returns false when the platform cannot create the case (symlinks on Windows).
func applyMutation(root string, c testCase) bool {
	if c.tpl == nil {
		return true
	}
	t := *c.tpl
	path := filepath.Join(root, filepath.FromSlash(c.file))
	switch t.str("op") {
	case "flip-byte":
		b, _ := os.ReadFile(path)
		i := 0
		if t.fields.Field("offset").Num() == -1 {
			i = len(b) - 1
		}
		b[i] ^= 0x01
		os.WriteFile(path, b, 0o644)
	case "delete-file":
		os.Remove(path)
	case "make-dir":
		os.Remove(path)
		os.Mkdir(path, 0o755)
	case "symlink":
		os.Remove(path)
		if err := os.Symlink(t.str("target"), path); err != nil {
			return false
		}
	case "add-file":
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(t.str("content")), 0o644)
	case "replace-text":
		b, _ := os.ReadFile(path)
		os.WriteFile(path, bytes.Replace(b, latin1(t.str("find")), latin1(t.str("replace")), 1), 0o644)
	case "strip-trailing-newline":
		b, _ := os.ReadFile(path)
		os.WriteFile(path, b[:len(b)-1], 0o644)
	case "prepend-bytes":
		prefix, _ := hex.DecodeString(t.str("hex"))
		b, _ := os.ReadFile(path)
		os.WriteFile(path, append(prefix, b...), 0o644)
	case "json-add-key", "json-retype", "json-drop-first":
		o, _ := parseObjectFile(path)
		switch first := firstKey(o); t.str("op") {
		case "json-add-key":
			o.Set(t.str("key"), t.fields.Field("value"))
		case "json-retype":
			if o.Field(first).Kind() == jsjson.Number {
				o.Set(first, jsjson.MakeString("retyped"))
			} else {
				o.Set(first, jsjson.MakeNumber(12345))
			}
		default:
			o.Delete(first)
		}
		writeJSON(path, jsjson.MakeObject(o))
	}
	if t.fields.Field("rehash").Bool() {
		rehash(root, c.base)
	}
	return true
}

func firstKey(o *jsjson.Obj) string {
	if keys := o.Keys(); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func treeDigest(root string) string {
	var lines []string
	var walk func(rel string)
	walk = func(rel string) {
		names, _ := nodefs.ReadDirNames(filepath.Join(root, filepath.FromSlash(rel)))
		for _, name := range names {
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			path := filepath.Join(root, filepath.FromSlash(r))
			info, _ := os.Lstat(path)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, _ := os.Readlink(path)
				lines = append(lines, "L "+r+" "+target)
			case info.IsDir():
				lines = append(lines, "D "+r)
				walk(r)
			default:
				b, _ := os.ReadFile(path)
				lines = append(lines, "F "+r+" "+canonical.SHA256HexBytes(b))
			}
		}
	}
	walk("")
	return canonical.SHA256Hex(strings.Join(lines, "\n"))
}

var gitLabels = []string{
	"Git bundle head listing", "Temporary Git verifier initialization", "Git bundle verification",
	"Git bundle extraction", "Git commit resolution", "Git tree resolution", "Git bundle range enumeration",
	"Git binary range patch creation", "Git bundle object-format resolution",
}

func isBoundary(c byte) bool {
	return c == '\'' || c == '"' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func normalize(text, bundle string) string {
	out := text
	paths := []string{bundle}
	if real, err := filepath.EvalSymlinks(bundle); err == nil && real != bundle {
		paths = append(paths, real)
	}
	for _, p := range paths {
		out = strings.ReplaceAll(out, p, "<BUNDLE>")
	}
	if runtime.GOOS == "windows" {
		var b strings.Builder
		for {
			i := strings.Index(out, "<BUNDLE>")
			if i < 0 {
				b.WriteString(out)
				break
			}
			b.WriteString(out[:i+len("<BUNDLE>")])
			rest := out[i+len("<BUNDLE>"):]
			end := 0
			for end < len(rest) && !isBoundary(rest[end]) {
				end++
			}
			b.WriteString(strings.ReplaceAll(rest[:end], `\`, "/"))
			out = rest[end:]
		}
		out = b.String()
	}
	const marker = "faultline-git-proof-verify-"
	for i := strings.Index(out, marker); i >= 0; {
		start := i
		for start > 0 && !isBoundary(out[start-1]) {
			start--
		}
		end := i + len(marker)
		for end < len(out) && isAlnum(out[end]) {
			end++
		}
		out = out[:start] + "<GITTMP>" + out[end:]
		next := strings.Index(out[start+len("<GITTMP>"):], marker)
		if next < 0 {
			break
		}
		i = start + len("<GITTMP>") + next
	}
	for _, label := range gitLabels {
		needle := label + " failed: "
		for i := strings.Index(out, needle); i >= 0; {
			from := i + len(needle)
			to := strings.Index(out[from:], "\n- ")
			if to >= 0 {
				to += from
			} else if strings.HasSuffix(out, "\n") {
				to = len(out) - 1
			} else {
				to = len(out)
			}
			out = out[:from] + "<GIT-DETAIL>" + out[to:]
			next := strings.Index(out[from+len("<GIT-DETAIL>"):], needle)
			if next < 0 {
				break
			}
			i = from + len("<GIT-DETAIL>") + next
		}
	}
	return out
}

func baseRoot(root, base string) string {
	if strings.HasPrefix(base, "prevention-") {
		if o, ok := parseObjectFile(filepath.Join(root, "manifest.json")); ok {
			return o.Field("rootDigest").Str()
		}
		return "missing"
	}
	text, err := nodefs.ReadText(filepath.Join(root, "ROOT.sha256"))
	if err != nil {
		return "missing"
	}
	return jsstr.Trim(text)
}
```

The TS normalizer uses JS `/\s/` as its boundary test, and Go uses ASCII whitespace. The texts involved are ASCII paths, so these agree. Keep both as written.

- [x] **Step 7: Write the Go replay test**

`difftest/replay_test.go`:

```go
package difftest

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/cli"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// portedTypes gates golden replay per bundle type; verifier tasks add entries.
var portedTypes = map[string]bool{}

func TestMain(m *testing.M) {
	home, _ := os.MkdirTemp("", "faultline-difftest-home-")
	empty := filepath.Join(home, "empty.gitconfig")
	os.WriteFile(empty, nil, 0o600)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", empty)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func loadTemplates(t *testing.T, path string) []template {
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	var out []template
	for _, item := range v.Items() {
		out = append(out, template{item.Obj()})
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoldenReplay(t *testing.T) {
	repo := oracle.RepoRoot(t)
	templates := loadTemplates(t, filepath.Join(repo, "difftest", "testdata", "mutations.json"))
	goldenDir := filepath.Join(repo, "difftest", "testdata", "golden")
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		base := strings.TrimSuffix(entry.Name(), ".jsonl")
		kind, _, _ := strings.Cut(base, "-")
		if !portedTypes[kind] {
			continue
		}
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(repo, "difftest", "testdata", "bases", base)
			golden := map[string]*jsjson.Obj{}
			var order []string
			f, _ := os.Open(filepath.Join(goldenDir, entry.Name()))
			defer f.Close()
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 1<<20), 1<<26)
			for scanner.Scan() {
				v, err := jsjson.Parse(scanner.Text())
				if err != nil {
					t.Fatal(err)
				}
				key := v.Obj().Field("case").Str() + "|" + v.Obj().Field("inv").Str()
				golden[key] = v.Obj()
				if v.Obj().Field("inv").Str() == "plain" {
					order = append(order, v.Obj().Field("case").Str())
				}
			}
			cases := expandCases(base, root, templates)
			if len(cases) != len(order) {
				t.Fatalf("Go expands %d cases, goldens have %d", len(cases), len(order))
			}
			rootDigest := baseRoot(root, base)
			for i, c := range cases {
				if c.id != order[i] {
					t.Fatalf("case %d: Go %q, golden %q", i, c.id, order[i])
				}
				t.Run(c.id, func(t *testing.T) {
					t.Parallel()
					work := t.TempDir()
					bundle := filepath.Join(work, "bundle")
					copyTree(t, root, bundle)
					if !applyMutation(bundle, c) {
						t.Skip("platform cannot create this case (symlink)")
					}
					digest := treeDigest(bundle)
					invocations := [][2]string{{"plain", ""}}
					if c.tpl == nil {
						invocations = append(invocations, [2]string{"root-ok", rootDigest}, [2]string{"root-bad", "sha256:" + strings.Repeat("0", 64)})
					}
					for _, inv := range invocations {
						want := golden[c.id+"|"+inv[0]]
						if want == nil {
							t.Fatalf("no golden for %s %s", c.id, inv[0])
						}
						if digest != want.Field("treeDigest").Str() {
							t.Fatalf("tree digest mismatch: the Go and TS mutation engines built different trees")
						}
						args := []string{"verify", bundle}
						if inv[1] != "" {
							args = append(args, "--expect-root", inv[1])
						}
						var stdout, stderr bytes.Buffer
						exit := cli.Run(args, &stdout, &stderr)
						gotOut, gotErr := normalize(stdout.String(), bundle), normalize(stderr.String(), bundle)
						if gotOut != want.Field("stdout").Str() || gotErr != want.Field("stderr").Str() || float64(exit) != want.Field("exit").Num() {
							t.Fatalf("%s:\n--- Go (exit %d)\n%s%s\n--- TS (exit %v)\n%s%s", inv[0], exit, gotOut, gotErr, want.Field("exit").Num(), want.Field("stdout").Str(), want.Field("stderr").Str())
						}
					}
				})
			}
		})
	}
}
```

- [x] **Step 8: Verify the harness wiring**

Temporarily set `portedTypes = map[string]bool{"demo": true}` and run `go test ./difftest/ -run TestGoldenReplay 2>&1 | head -40`.
Expected: the case-count and tree-digest checks pass (no `tree digest mismatch` or `Go expands` failures), and every case fails on output, because `verify` is not ported yet (`not yet ported: verify`). Then restore `portedTypes` to the empty map.

Run: `go test ./difftest/`
Expected: PASS (no types ported; live tests skipped).

- [x] **Step 9: Commit**

```bash
git add difftest
git commit -m "test(difftest): golden corpus from frozen TS and Go replay harness

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Demo bundle verifier (`faultline.proof-bundle.v2`) and verify dispatch

**Files:**
- Create: `internal/bundle/bundle.go` (shared result type, JS-exception helpers, JSON/schema helpers), `internal/bundle/demo/schemas.go`, `internal/bundle/demo/verify.go`, `internal/bundle/demo/verify_test.go`
- Modify: `internal/cli/verify.go` (dispatch + output), `difftest/replay_test.go` (`portedTypes["demo"] = true`)

**Interfaces:**
- Consumes: `schema.*`, `jsjson.*`, `canonical.*`, `nodefs.*`, `jsstr.Trim`.
- Produces (package `bundle`): `type Result struct{ Valid bool; CheckedFiles int; Errors []string; RootDigest *string; ExternalRootStatus string; Classification *string }`; `Throw(err error)`, `Must[T](v T, err error) T`, `Try(fn func()) error`, `Catch(handler func(error))` (must be deferred directly); `ParseValue(v jsjson.Value, s schema.Schema) (jsjson.Value, error)` (zod `.parse`: error text is `ZodError.message`); `ParseJSONFile(path string) (jsjson.Value, error)` (`JSON.parse(readFileSync(path, "utf8"))`); `ParseFile(path string, s schema.Schema) (jsjson.Value, error)` (`S.parse(JSON.parse(readFileSync(path, "utf8")))`); `SameCanonical(a, b jsjson.Value) bool` (throws on canonical errors); `Without(v jsjson.Value, key string) jsjson.Value` (object rest `{ key, ...rest }`); `Strings(v jsjson.Value) []string`; `DistinctCount([]string) int`. Package `demo`: `Verify(directory, expectedRoot string, rootProvided bool) bundle.Result`. Package `cli`: `(e *env) printBundle(label string, r bundle.Result)`.

**Porting rules (apply to every verifier task):**
- A TS `throw` (or a Node call that throws) becomes `bundle.Throw`/`bundle.Must`. Each TS `try { … } catch` becomes `bundle.Try(func() { … })`. The function's outermost `try/catch` becomes `defer bundle.Catch(...)`. This keeps error text and control flow identical, including exceptions from `canonicalJson` inside inner `try` blocks.
- `Map`/`Set` iteration is insertion order: keep a slice for order plus a map for lookup.
- `new Map(entries)` keeps the last duplicate; `new Set(array)` keeps the first.
- JS truthiness where TS uses it (`expectedRoot ? …`) versus `=== undefined` where TS uses that. Check each site.

- [x] **Step 1: Write the failing test**

`internal/bundle/demo/verify_test.go`:

```go
package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiesCommittedRerunBase(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "demo-rerun")
	root, err := os.ReadFile(filepath.Join(dir, "ROOT.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(dir, "", false)
	if !r.Valid || r.ExternalRootStatus != "NOT_PROVIDED" || r.RootDigest == nil || *r.RootDigest != strings.TrimSpace(string(root)) {
		t.Fatalf("result = %+v", r)
	}
	if r := Verify(dir, "", true); r.ExternalRootStatus != "NOT_PROVIDED" {
		t.Fatal(`TS treats --expect-root "" as not provided (truthiness)`)
	}
	if r := Verify(filepath.Join(dir, "nope"), "", false); r.Valid || r.Errors[0] != "bundle directory does not exist" {
		t.Fatalf("missing dir result = %+v", r)
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/demo/`
Expected: FAIL (undefined: Verify).

- [x] **Step 3: Implement shared bundle helpers**

`internal/bundle/bundle.go`:

```go
// Package bundle holds helpers shared by the proof bundle verifier ports.
package bundle

import (
	"errors"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

// Result mirrors the TS verification results. Nil pointers are TS null.
type Result struct {
	Valid              bool
	CheckedFiles       int
	Errors             []string
	RootDigest         *string
	ExternalRootStatus string
	Classification     *string
}

// Thrown carries a JS exception through Go panics.
type Thrown struct{ Err error }

func Throw(err error) { panic(Thrown{err}) }

func Must[T any](v T, err error) T {
	if err != nil {
		Throw(err)
	}
	return v
}

// Try runs fn like a JS try block and returns the caught exception.
func Try(fn func()) (err error) {
	defer Catch(func(e error) { err = e })
	fn()
	return nil
}

// Catch recovers a Thrown panic; use as `defer bundle.Catch(handler)`.
func Catch(handler func(error)) {
	if r := recover(); r != nil {
		t, ok := r.(Thrown)
		if !ok {
			panic(r)
		}
		handler(t.Err)
	}
}

// ParseValue is zod `Schema.parse(value)`.
func ParseValue(v jsjson.Value, s schema.Schema) (jsjson.Value, error) {
	out, issues, ok := schema.Parse(s, v)
	if !ok {
		return jsjson.Value{}, errors.New(schema.ErrorMessage(issues))
	}
	return out, nil
}

// ParseJSONFile is JSON.parse(readFileSync(path, "utf8")).
func ParseJSONFile(path string) (jsjson.Value, error) {
	text, err := nodefs.ReadText(path)
	if err != nil {
		return jsjson.Value{}, err
	}
	return jsjson.Parse(text)
}

// ParseFile is Schema.parse(JSON.parse(readFileSync(path, "utf8"))).
func ParseFile(path string, s schema.Schema) (jsjson.Value, error) {
	v, err := ParseJSONFile(path)
	if err != nil {
		return jsjson.Value{}, err
	}
	return ParseValue(v, s)
}

// SameCanonical is canonicalJson(a) === canonicalJson(b); it throws like TS.
func SameCanonical(a, b jsjson.Value) bool {
	return Must(canonical.CanonicalJSON(a)) == Must(canonical.CanonicalJSON(b))
}

// Without is `const { key: _, ...rest } = v`.
func Without(v jsjson.Value, key string) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != key {
			out.Set(k, v.Obj().Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

func Strings(v jsjson.Value) []string {
	out := make([]string, 0, len(v.Items()))
	for _, item := range v.Items() {
		out = append(out, item.Str())
	}
	return out
}

// DistinctCount is new Set(values).size.
func DistinctCount(values []string) int {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	return len(seen)
}
```

- [x] **Step 4: Port the schemas**

Port of `src/domain.ts` (whole file) and `src/proof-bundle.ts:10-37`.

`internal/bundle/demo/schemas.go`:

```go
package demo

import s "github.com/Mizore66/faultline/internal/schema"

var (
	verdict       = s.Enum("PASS", "FAIL", "UNSTABLE", "ERROR", "INAPPLICABLE")
	evidenceKind  = s.Enum("EXECUTED", "DERIVED", "INFERRED", "UNKNOWN")
	stateFidelity = s.Enum("NATIVE", "RECONSTRUCTED", "DEMO_SAMPLE")
	runMode       = s.Enum("REPLAY", "RERUN")
	positiveInt   = s.Number(s.Int(), s.Positive())

	executionPolicy = s.Object(
		s.F("network", s.LiteralString("disabled")),
		s.F("credentials", s.LiteralString("redacted")),
		s.F("runner", s.Enum("built-in-sample", "sandboxed")),
		s.F("timeoutSeconds", positiveInt),
		s.F("note", s.String()),
	)

	witnessSchema = s.Object(
		s.F("id", s.String()),
		s.F("version", positiveInt),
		s.F("behavior", s.String()),
		s.F("command", s.String()),
		s.F("overlay", s.Array(s.String())),
		s.F("policy", executionPolicy),
		s.F("proposedBeforeLocalization", s.Boolean()),
		s.F("approvedAt", s.String()),
		s.F("incidentPacketDigest", s.String()),
		s.F("digest", s.String()),
	)

	runRecordSchema = s.Object(
		s.F("id", s.String()),
		s.F("stateId", s.String()),
		s.F("witnessDigest", s.String()),
		s.F("environmentDigest", s.String()),
		s.F("verdict", verdict),
		s.F("reasonCode", s.String()),
		s.F("stdout", s.String()),
		s.F("stderr", s.String()),
		s.F("durationMs", s.Number(s.Nonnegative())),
		s.F("executionKind", s.Enum("EXECUTED", "CACHED")),
		s.F("executedAt", s.String()),
	)

	transitionSchema = s.Object(
		s.F("beforeStateId", s.String()),
		s.F("afterStateId", s.String()),
		s.F("beforeVerdict", verdict),
		s.F("afterVerdict", verdict),
		s.F("stable", s.Boolean()),
		s.F("kind", s.Enum("PASS_TO_FAIL", "FAIL_TO_PASS")),
		s.F("boundaryRunIds", s.Array(s.String())),
	)

	minimizationAttemptSchema = s.Object(
		s.F("id", s.String()),
		s.F("subset", s.Array(s.String())),
		s.F("outcome", s.Enum("PASS", "FAIL", "UNRESOLVED")),
		s.F("runId", s.Optional(s.String())),
		s.F("note", s.String()),
	)

	evidenceClaimSchema = s.Object(
		s.F("kind", evidenceKind),
		s.F("statement", s.String()),
		s.F("evidenceIds", s.Array(s.String())),
	)

	// StoredMinimizationSchema (proof-bundle.ts:22) equals DemoAnalysis.minimization.
	minimizationSchema = s.Object(
		s.F("budget", s.Object(s.F("used", s.Number(s.Int(), s.Nonnegative())), s.F("max", positiveInt))),
		s.F("attempts", s.Array(minimizationAttemptSchema)),
		s.F("candidate", s.Array(s.String())),
		s.F("sufficiency", runRecordSchema),
		s.F("necessity", runRecordSchema),
		s.F("termination", s.Enum("BIDIRECTIONALLY_VALIDATED", "NOT_EXECUTED")),
	)

	// StoredPreventionSchema (proof-bundle.ts:31) equals DemoAnalysis.prevention.
	preventionSchema = s.Object(
		s.F("lastGood", runRecordSchema),
		s.F("firstBad", runRecordSchema),
		s.F("repaired", runRecordSchema),
		s.F("verified", s.Boolean()),
	)

	demoAnalysisSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.demo.v1")),
		s.F("mode", runMode),
		s.F("generatedAt", s.String()),
		s.F("fixture", s.Object(s.F("id", s.String()), s.F("title", s.String()), s.F("summary", s.String()), s.F("stateFidelity", stateFidelity))),
		s.F("witness", witnessSchema),
		s.F("metrics", s.Object(s.F("sessions", positiveInt), s.F("turns", positiveInt), s.F("files", positiveInt), s.F("changedLines", positiveInt), s.F("implicatedHunks", positiveInt))),
		s.F("contributionRuns", s.Array(runRecordSchema)),
		s.F("timelineRuns", s.Array(runRecordSchema)),
		s.F("transitions", s.Array(transitionSchema)),
		s.F("runCatalog", s.Array(runRecordSchema)),
		s.F("minimization", minimizationSchema),
		s.F("prevention", preventionSchema),
		s.F("grade", s.Object(s.F("value", s.Enum("A", "B", "C", "D")), s.F("reasons", s.Array(s.String())))),
		s.F("claims", s.Array(evidenceClaimSchema)),
		s.F("warning", s.String()),
	)

	manifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.proof-bundle.v2")),
		s.F("investigationId", s.String()),
		s.F("fixtureId", s.String()),
		s.F("generatedAt", s.String()),
		s.F("analysisDigest", s.String()),
		s.F("witnessDigest", s.String()),
		s.F("environmentDigest", s.String()),
		s.F("mode", s.Enum("REPLAY", "RERUN")),
		s.F("integrityScope", s.LiteralString("complete-declared-file-set")),
	)
)
```

- [x] **Step 5: Port the verifier**

Port of `src/proof-bundle.ts`: `verifyProofBundle` (439–532), `collectFiles` (425–437), `assertNoLinksOrSpecialFiles` (57–70), `validateAnalysisCoverage` (202–211), `readJsonArtifact` (213–220), `stableExecutedRuns` (226–234), `validateSemanticEvidence` (236–338), `requiredDeclaredFiles` (183–186), `referencedRunIds` (188–200), `safeRunFileName`/`runPaths` (134–141).

`internal/bundle/demo/verify.go`:

```go
// Package demo ports the faultline.proof-bundle.v2 verifier in src/proof-bundle.ts.
package demo

import (
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New("Proof bundle destination must be a real directory: " + directory))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New("Proof bundle destination contains a symbolic link or special file: " + child))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

func collectFiles(directory, current string) []string {
	var files []string
	for _, name := range bundle.Must(nodefs.ReadDirNames(current)) {
		path := nodefs.Join(current, name)
		stat := bundle.Must(nodefs.Lstat(path))
		if isLink(stat) || (!stat.IsDir() && !stat.Mode().IsRegular()) {
			bundle.Throw(errors.New("bundle contains a symbolic link or special file: " + path))
		}
		if stat.IsDir() {
			files = append(files, collectFiles(directory, path)...)
		} else {
			files = append(files, strings.ReplaceAll(nodefs.Relative(directory, path), `\`, "/"))
		}
	}
	return files
}

// safeRunFileName is run.id.replace(/[^a-zA-Z0-9_-]/g, "_") per UTF-16 unit.
func safeRunFileName(id string) string {
	units := jsstr.ToUTF16(id)
	out := make([]byte, len(units))
	for i, u := range units {
		switch {
		case u == '_' || u == '-' || (u >= '0' && u <= '9') || (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z'):
			out[i] = byte(u)
		default:
			out[i] = '_'
		}
	}
	return string(out)
}

func requiredDeclaredFiles(analysis jsjson.Value) []string {
	files := []string{"manifest.json", "report.md", "analysis.json", "witness/witness.json", "minimization/attempts.json", "prevention/three-state.json", "VERIFY.md"}
	for _, run := range analysis.Get("runCatalog").Items() {
		base := "runs/" + safeRunFileName(run.Get("id").Str())
		files = append(files, base+"/result.json", base+"/stdout.log", base+"/stderr.log")
	}
	return canonical.SortLocale(files)
}

// orderedSet is a JS Set of strings.
type orderedSet struct {
	order []string
	has   map[string]bool
}

func newSet(values ...string) *orderedSet {
	s := &orderedSet{has: map[string]bool{}}
	for _, v := range values {
		s.add(v)
	}
	return s
}

func (s *orderedSet) add(v string) {
	if !s.has[v] {
		s.has[v] = true
		s.order = append(s.order, v)
	}
}

func referencedRunIDs(a jsjson.Value) *orderedSet {
	ids := newSet()
	var runs []jsjson.Value
	runs = append(runs, a.Get("contributionRuns").Items()...)
	runs = append(runs, a.Get("timelineRuns").Items()...)
	runs = append(runs, a.Get("prevention", "lastGood"), a.Get("prevention", "firstBad"), a.Get("prevention", "repaired"), a.Get("minimization", "sufficiency"), a.Get("minimization", "necessity"))
	for _, run := range runs {
		ids.add(run.Get("id").Str())
	}
	for _, t := range a.Get("transitions").Items() {
		for _, id := range bundle.Strings(t.Get("boundaryRunIds")) {
			ids.add(id)
		}
	}
	for _, attempt := range a.Get("minimization", "attempts").Items() {
		if id := attempt.Get("runId"); id.Kind() == jsjson.String && id.Str() != "" {
			ids.add(id.Str())
		}
	}
	return ids
}

func validateAnalysisCoverage(a jsjson.Value, errs *[]string) {
	catalog := map[string]bool{}
	for _, record := range a.Get("runCatalog").Items() {
		id := record.Get("id").Str()
		if catalog[id] {
			*errs = append(*errs, "duplicate run id in catalog: "+id)
		}
		catalog[id] = true
	}
	for _, id := range referencedRunIDs(a).order {
		if !catalog[id] {
			*errs = append(*errs, "analysis references a run not present in the catalog: "+id)
		}
	}
}

func readJSONArtifact(path, label string, errs *[]string) (jsjson.Value, bool) {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		*errs = append(*errs, label+" JSON validation failed: "+err.Error())
		return jsjson.Value{}, false
	}
	return v, true
}

func stableExecutedRuns(records []jsjson.Value, expectedStateID, expectedVerdict string) bool {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.Get("id").Str()
	}
	if len(records) != 3 || bundle.DistinctCount(ids) != len(records) {
		return false
	}
	first := records[0]
	for _, r := range records {
		if r.Get("executionKind").Str() != "EXECUTED" || r.Get("verdict").Str() != expectedVerdict || r.Get("stateId").Str() != expectedStateID ||
			r.Get("witnessDigest").Str() != first.Get("witnessDigest").Str() || r.Get("environmentDigest").Str() != first.Get("environmentDigest").Str() {
			return false
		}
	}
	return true
}

func validateSemanticEvidence(a jsjson.Value, output string, errs *[]string) {
	add := func(s string) { *errs = append(*errs, s) }

	if payload, ok := readJSONArtifact(nodefs.Join(output, "witness", "witness.json"), "witness artifact", errs); ok {
		if err := bundle.Try(func() {
			witness := bundle.Must(bundle.ParseValue(payload, witnessSchema))
			if !bundle.SameCanonical(witness, a.Get("witness")) {
				add("witness artifact does not match analysis.witness")
			}
			if bundle.Must(canonical.DigestJSON(bundle.Without(witness, "digest"))) != witness.Get("digest").Str() {
				add("witness artifact digest is invalid")
			}
		}); err != nil {
			add("witness artifact schema validation failed: " + err.Error())
		}
	}

	if payload, ok := readJSONArtifact(nodefs.Join(output, "minimization", "attempts.json"), "minimization artifact", errs); ok {
		if err := bundle.Try(func() {
			if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(payload, minimizationSchema)), a.Get("minimization")) {
				add("minimization artifact does not match analysis.minimization")
			}
		}); err != nil {
			add("minimization artifact schema validation failed: " + err.Error())
		}
	}

	if payload, ok := readJSONArtifact(nodefs.Join(output, "prevention", "three-state.json"), "prevention artifact", errs); ok {
		if err := bundle.Try(func() {
			if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(payload, preventionSchema)), a.Get("prevention")) {
				add("prevention artifact does not match analysis.prevention")
			}
		}); err != nil {
			add("prevention artifact schema validation failed: " + err.Error())
		}
	}

	catalog := map[string]jsjson.Value{}
	for _, run := range a.Get("runCatalog").Items() {
		catalog[run.Get("id").Str()] = run
	}
	for _, run := range a.Get("runCatalog").Items() {
		id := run.Get("id").Str()
		base := nodefs.Join(output, "runs", safeRunFileName(id))
		if persisted, ok := readJSONArtifact(nodefs.Join(base, "result.json"), "run "+id, errs); ok {
			if err := bundle.Try(func() {
				if !bundle.SameCanonical(bundle.Must(bundle.ParseValue(persisted, runRecordSchema)), run) {
					add("persisted run does not match catalog: " + id)
				}
			}); err != nil {
				add("persisted run schema validation failed for " + id + ": " + err.Error())
			}
		}
		if err := bundle.Try(func() {
			if bundle.Must(nodefs.ReadText(nodefs.Join(base, "stdout.log"))) != run.Get("stdout").Str() {
				add("stdout does not match catalog for " + id)
			}
			if bundle.Must(nodefs.ReadText(nodefs.Join(base, "stderr.log"))) != run.Get("stderr").Str() {
				add("stderr does not match catalog for " + id)
			}
		}); err != nil {
			add("run log read failed for " + id + ": " + err.Error())
		}
	}

	mode := a.Get("mode").Str()
	for _, t := range a.Get("transitions").Items() {
		ids := bundle.Strings(t.Get("boundaryRunIds"))
		var boundary []jsjson.Value
		for _, id := range ids {
			if run, ok := catalog[id]; ok {
				boundary = append(boundary, run)
			}
		}
		if len(boundary) != len(ids) {
			continue
		}
		before, after := t.Get("beforeStateId").Str(), t.Get("afterStateId").Str()
		if bundle.DistinctCount(ids) != len(ids) {
			add("transition contains duplicate boundary run IDs: " + before + " -> " + after)
		}
		var beforeRuns, afterRuns []jsjson.Value
		for _, run := range boundary {
			if run.Get("stateId").Str() == before {
				beforeRuns = append(beforeRuns, run)
			}
			if run.Get("stateId").Str() == after {
				afterRuns = append(afterRuns, run)
			}
		}
		beforeVerdict, afterVerdict := t.Get("beforeVerdict").Str(), t.Get("afterVerdict").Str()
		shouldBeStable := stableExecutedRuns(beforeRuns, before, beforeVerdict) && stableExecutedRuns(afterRuns, after, afterVerdict)
		if t.Get("stable").Bool() != shouldBeStable {
			add("transition stability does not match executed boundary evidence: " + before + " -> " + after)
		}
		expectedKind := ""
		if beforeVerdict == "PASS" && afterVerdict == "FAIL" {
			expectedKind = "PASS_TO_FAIL"
		} else if beforeVerdict == "FAIL" && afterVerdict == "PASS" {
			expectedKind = "FAIL_TO_PASS"
		}
		if expectedKind == "" || t.Get("kind").Str() != expectedKind {
			add("transition kind does not match verdicts: " + before + " -> " + after)
		}
		if mode == "REPLAY" && t.Get("stable").Bool() {
			add("cached replay cannot certify a stable transition")
		}
	}

	m := a.Get("minimization")
	sufficiency, hasSufficiency := catalog[m.Get("sufficiency", "id").Str()]
	necessity, hasNecessity := catalog[m.Get("necessity", "id").Str()]
	minimizationProof := m.Get("termination").Str() == "BIDIRECTIONALLY_VALIDATED"
	if minimizationProof {
		if !hasSufficiency || !hasNecessity || mode != "RERUN" ||
			sufficiency.Get("executionKind").Str() != "EXECUTED" || necessity.Get("executionKind").Str() != "EXECUTED" ||
			sufficiency.Get("verdict").Str() != "FAIL" || necessity.Get("verdict").Str() != "PASS" ||
			sufficiency.Get("witnessDigest").Str() != necessity.Get("witnessDigest").Str() ||
			sufficiency.Get("environmentDigest").Str() != necessity.Get("environmentDigest").Str() ||
			len(m.Get("candidate").Items()) == 0 {
			add("bidirectional minimization claim is not supported by executed evidence")
		}
	} else if mode == "REPLAY" && m.Get("termination").Str() != "NOT_EXECUTED" {
		add("cached replay minimization must be marked NOT_EXECUTED")
	}

	p := a.Get("prevention")
	preventionRuns := []jsjson.Value{p.Get("lastGood"), p.Get("firstBad"), p.Get("repaired")}
	if p.Get("verified").Bool() {
		var witnesses, environments []string
		executed := true
		for _, run := range preventionRuns {
			executed = executed && run.Get("executionKind").Str() == "EXECUTED"
			witnesses = append(witnesses, run.Get("witnessDigest").Str())
			environments = append(environments, run.Get("environmentDigest").Str())
		}
		if mode != "RERUN" || !executed ||
			preventionRuns[0].Get("verdict").Str() != "PASS" || preventionRuns[1].Get("verdict").Str() != "FAIL" || preventionRuns[2].Get("verdict").Str() != "PASS" ||
			bundle.DistinctCount(witnesses) != 1 || bundle.DistinctCount(environments) != 1 {
			add("three-state prevention claim is not supported by executed evidence")
		}
	}
	stablePassToFail := slices.ContainsFunc(a.Get("transitions").Items(), func(t jsjson.Value) bool {
		return t.Get("stable").Bool() && t.Get("kind").Str() == "PASS_TO_FAIL"
	})
	if a.Get("grade", "value").Str() == "A" && (!stablePassToFail || !minimizationProof || !p.Get("verified").Bool()) {
		add("A-grade claim is not supported by stable, minimized, and prevention evidence")
	}
}

// parseHashLine is /^(?<digest>[a-f0-9]{64})  (?<file>.+)$/ where JS `.`
// excludes \r, U+2028, and U+2029 (\n cannot occur after splitting).
func parseHashLine(line string) (digest, file string, ok bool) {
	if len(line) < 67 || line[64:66] != "  " {
		return "", "", false
	}
	for i := 0; i < 64; i++ {
		if c := line[i]; !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	file = line[66:]
	if strings.ContainsAny(file, "\r\u2028\u2029") {
		return "", "", false
	}
	return line[:64], file, true
}

func invalidDeclaredPath(file string) bool {
	return strings.Contains(file, `\`) || strings.HasPrefix(file, "/") ||
		slices.ContainsFunc(strings.Split(file, "/"), func(part string) bool { return part == "" || part == "." || part == ".." })
}

// Verify ports verifyProofBundle (src/proof-bundle.ts:439).
func Verify(directory, expectedRoot string, rootProvided bool) (result bundle.Result) {
	output := nodefs.Resolve(directory)
	var errs []string
	var rootDigest *string
	expected := rootProvided && expectedRoot != "" // TS: `expectedRoot ? …`
	status := "NOT_PROVIDED"
	if expected {
		status = "MISMATCH"
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, "bundle verification failed safely: "+err.Error())
		result = bundle.Result{Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
	})

	if !nodefs.Exists(output) {
		return bundle.Result{Errors: []string{"bundle directory does not exist"}, ExternalRootStatus: status}
	}
	assertNoLinksOrSpecialFiles(output)
	hashesPath := nodefs.Join(output, "hashes.txt")
	rootPath := nodefs.Join(output, "ROOT.sha256")
	manifestPath := nodefs.Join(output, "manifest.json")
	analysisPath := nodefs.Join(output, "analysis.json")
	if !nodefs.Exists(hashesPath) || !nodefs.Exists(rootPath) || !nodefs.Exists(manifestPath) || !nodefs.Exists(analysisPath) {
		return bundle.Result{Errors: []string{"bundle is missing hashes.txt, ROOT.sha256, manifest.json, or analysis.json"}, ExternalRootStatus: status}
	}
	hashes := bundle.Must(nodefs.ReadText(hashesPath))
	calculated := "sha256:" + canonical.SHA256Hex(hashes)
	rootDigest = &calculated
	if jsstr.Trim(bundle.Must(nodefs.ReadText(rootPath))) != calculated {
		errs = append(errs, "ROOT.sha256 does not match hashes.txt")
	}
	status = "NOT_PROVIDED"
	if expected {
		status = "MISMATCH"
		if expectedRoot == calculated {
			status = "MATCH"
		}
	}
	if status == "MISMATCH" {
		errs = append(errs, "externally supplied bundle root does not match")
	}
	manifest, manifestErr := bundle.ParseFile(manifestPath, manifestSchema)
	if manifestErr != nil {
		errs = append(errs, "manifest validation failed: "+manifestErr.Error())
	}
	analysis, analysisErr := bundle.ParseFile(analysisPath, demoAnalysisSchema)
	if analysisErr != nil {
		errs = append(errs, "analysis validation failed: "+analysisErr.Error())
	}
	if manifestErr == nil && analysisErr == nil {
		if manifest.Get("analysisDigest").Str() != bundle.Must(canonical.DigestJSON(analysis)) {
			errs = append(errs, "manifest analysisDigest does not match analysis.json")
		}
		if manifest.Get("fixtureId").Str() != analysis.Get("fixture", "id").Str() {
			errs = append(errs, "manifest fixtureId does not match analysis.json")
		}
		if manifest.Get("witnessDigest").Str() != analysis.Get("witness", "digest").Str() {
			errs = append(errs, "manifest witnessDigest does not match analysis.json")
		}
		validateAnalysisCoverage(analysis, &errs)
	}
	declared := map[string]string{}
	var declaredOrder []string
	for _, line := range strings.Split(jsstr.Trim(hashes), "\n") {
		if line == "" {
			continue
		}
		digest, file, ok := parseHashLine(line)
		if !ok {
			errs = append(errs, "invalid hash entry: "+line)
			continue
		}
		if _, dup := declared[file]; dup {
			errs = append(errs, "duplicate declared file: "+file)
			continue
		}
		if invalidDeclaredPath(file) {
			errs = append(errs, "invalid declared path: "+file)
			continue
		}
		declared[file] = digest
		declaredOrder = append(declaredOrder, file)
	}
	if analysisErr == nil {
		required := newSet(requiredDeclaredFiles(analysis)...)
		for _, file := range required.order {
			if _, ok := declared[file]; !ok {
				errs = append(errs, "required evidence file is not declared: "+file)
			}
		}
		for _, file := range declaredOrder {
			if !required.has[file] {
				errs = append(errs, "undeclared-schema file is present in hashes.txt: "+file)
			}
		}
	}
	for _, file := range declaredOrder {
		path := nodefs.Resolve(output, file)
		local := nodefs.Relative(output, path)
		if strings.HasPrefix(local, "..") || nodefs.IsAbsolute(local) {
			errs = append(errs, "declared path escapes bundle: "+file)
			continue
		}
		if !nodefs.Exists(path) {
			errs = append(errs, "declared file is missing: "+file)
			continue
		}
		if canonical.SHA256HexBytes(bundle.Must(nodefs.ReadBytes(path))) != declared[file] {
			errs = append(errs, "digest mismatch: "+file)
		}
	}
	expectedPhysical := newSet(append(slices.Clone(declaredOrder), "hashes.txt", "ROOT.sha256")...)
	for _, file := range collectFiles(output, output) {
		if !expectedPhysical.has[file] {
			errs = append(errs, "undeclared file exists in bundle: "+file)
		}
	}
	if analysisErr == nil {
		validateSemanticEvidence(analysis, output, &errs)
	}
	return bundle.Result{Valid: len(errs) == 0, CheckedFiles: len(declaredOrder), Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
}
```

- [x] **Step 6: Wire verify dispatch and output**

Replace `internal/cli/verify.go` (port of `src/cli-app.ts:2903-2945`):

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func verifyCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fail("Usage: fl verify <proof-bundle-directory>")
	}
	root := nodefs.Resolve(args[0])
	schemaVersion := ""
	if text, err := nodefs.ReadText(nodefs.Join(root, "manifest.json")); err == nil {
		if v, err := jsjson.Parse(text); err == nil {
			if sv := v.Get("schemaVersion"); sv.Kind() == jsjson.String {
				schemaVersion = sv.Str()
			}
		}
	}
	expectedRoot, rootProvided := option(args, "--expect-root")
	switch schemaVersion {
	case "faultline.prevention-proof.v1":
		return fail("not yet ported: prevention proof verification")
	case "faultline.turn-proof-bundle.v1":
		return fail("not yet ported: turn proof bundle verification")
	case "faultline.git-proof-bundle.v1":
		return fail("not yet ported: git proof bundle verification")
	}
	e.printBundle("Bundle", demo.Verify(root, expectedRoot, rootProvided))
	return nil
}

func verdictWord(valid bool) string {
	if valid {
		return "VALID"
	}
	return "INVALID"
}

func orUnavailable(s *string) string {
	if s == nil {
		return "unavailable"
	}
	return *s
}

func (e *env) printErrorsAndExit(r bundle.Result) {
	if !r.Valid {
		lines := make([]string, len(r.Errors))
		for i, err := range r.Errors {
			lines[i] = "- " + err
		}
		e.out(strings.Join(lines, "\n") + "\n")
		e.exitCode = 1
		return
	}
	e.exitCode = 0
}

// printBundle is the demo/git branch of the TS verify case.
func (e *env) printBundle(label string, r bundle.Result) {
	first := "Integrity"
	if r.ExternalRootStatus == "NOT_PROVIDED" {
		first = label + " self-consistency"
	}
	e.out(first + ": " + verdictWord(r.Valid) + "\n")
	e.out(fmt.Sprintf("Declared files checked: %d\nBundle root: %s\nExternal root: %s\n", r.CheckedFiles, orUnavailable(r.RootDigest), r.ExternalRootStatus))
	e.printErrorsAndExit(r)
}
```

In `difftest/replay_test.go` set `var portedTypes = map[string]bool{"demo": true}`.

- [x] **Step 7: Run tests**

Run: `go test ./internal/bundle/... ./internal/cli/ && go test ./difftest/ -run TestGoldenReplay`
Expected: PASS, with every `demo-*` case matching its golden. For each failing case the test prints Go and TS output side by side. Find the TS line that produced the differing text and correct the port; never edit goldens.

- [x] **Step 8: Commit**

```bash
git add internal/bundle internal/cli difftest/replay_test.go
git commit -m "feat(go): port faultline.proof-bundle.v2 verifier and verify dispatch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Prevention proof verifier (`faultline.prevention-proof.v1`)

**Files:**
- Create: `internal/bundle/prevention/schemas.go`, `internal/bundle/prevention/verify.go`, `internal/bundle/prevention/verify_test.go`, `difftest/named_live_test.go`, `difftest/gen/corrupt.go`
- Modify: `internal/cli/verify.go`, `difftest/replay_test.go` (`"prevention": true`), `difftest/gen/node-oracle.ts` (add `zodNamed`)

**Interfaces:**
- Consumes: `bundle.*`, `schema.*`, `canonical.*`, `nodefs.*`.
- Produces: `prevention.Verify(directory, expectedRoot string, rootProvided bool) bundle.Result` (sets `Classification` when the manifest parsed); exported schemas `prevention.BodySchema`, `prevention.ManifestSchema`, `prevention.RepairedRunsSchema`; `(e *env) printPrevention(bundle.Result)`; oracle op `zodNamed {name, text}` → `{success, canonical}` or `{success:false, message}`; Go `gen.CorruptDeep(r *rand.Rand, text string) string`; `difftest.namedSchemas map[string]schema.Schema` (registry each later task extends).

- [x] **Step 1: Write the failing test**

`internal/bundle/prevention/verify_test.go`:

```go
package prevention

import (
	"path/filepath"
	"testing"
)

func TestVerifiesCommittedBases(t *testing.T) {
	for base, classification := range map[string]string{
		"prevention-summary":  "PREVENTION_EVIDENCE_SUMMARY",
		"prevention-verified": "PREVENTION_VERIFIED",
	} {
		r := Verify(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base), "", false)
		if !r.Valid || r.Classification == nil || *r.Classification != classification || r.ExternalRootStatus != "NOT_PROVIDED" {
			t.Fatalf("%s: %+v", base, r)
		}
		if r := Verify(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base), "", true); r.Valid || r.ExternalRootStatus != "MISMATCH" {
			t.Fatalf(`%s: TS treats --expect-root "" as provided (=== undefined check)`, base)
		}
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/prevention/`
Expected: FAIL (undefined: Verify).

- [x] **Step 3: Port the schemas**

Port of `src/prevention-proof.ts:26-107`. zod's `.extend` keeps strictness.

`internal/bundle/prevention/schemas.go`:

```go
package prevention

import (
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

const SchemaVersion = "faultline.prevention-proof.v1"

var jsonTrue = jsjson.MakeBool(true)

var (
	digest    = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), ""))
	commit    = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`), ""))
	runIDList = s.Array(digest, s.Length(3))

	executedState = s.Object(
		s.F("role", s.Enum("LAST_GOOD", "FIRST_BAD", "REPAIRED")),
		s.F("commit", commit),
		s.F("tree", s.Optional(commit)),
		s.F("verdict", s.Enum("PASS", "FAIL")),
		s.F("witnessDigest", digest),
		s.F("environmentDigest", digest),
		s.F("executionTrust", s.LiteralString("NATIVE_DOCKER")),
		s.F("executionKind", s.LiteralString("EXECUTED")),
		s.F("distinctExecutionCount", s.Number(s.Int(), s.Gte(3))),
		s.F("runIds", runIDList),
	).Strict()

	repairedRunBinding = s.Object(
		s.F("runId", digest),
		s.F("executionId", digest),
		s.F("commit", commit),
		s.F("tree", commit),
		s.F("verdict", s.LiteralString("PASS")),
		s.F("witnessDigest", digest),
		s.F("environmentDigest", digest),
		s.F("executionTrust", s.LiteralString("NATIVE_DOCKER")),
		s.F("executionKind", s.LiteralString("EXECUTED")),
	).Strict()

	limitations = s.Array(s.String(s.MinLength(1)), s.MinItems(1), s.MaxItems(16))

	BodySchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("originalProofRoot", digest),
		s.F("frozenWitnessDigest", digest),
		s.F("investigationDigest", s.Optional(digest)),
		s.F("lastGood", executedState.Extend(s.F("role", s.LiteralString("LAST_GOOD")), s.F("verdict", s.LiteralString("PASS")))),
		s.F("firstBad", executedState.Extend(s.F("role", s.LiteralString("FIRST_BAD")), s.F("verdict", s.LiteralString("FAIL")))),
		s.F("repaired", executedState.Extend(s.F("role", s.LiteralString("REPAIRED")), s.F("verdict", s.LiteralString("PASS")))),
		s.F("repairBaseTree", s.Optional(commit)),
		s.F("repairPatchDigest", s.Optional(digest)),
		s.F("codexThreadId", s.Optional(s.String(s.MinLength(1), s.MaxLength(256)))),
		s.F("hardGuardArtifactDigests", s.Optional(s.Array(digest, s.MaxItems(64)))),
		s.F("verified", s.Literal(jsonTrue)),
		s.F("limitations", limitations),
	).Strict()

	ManifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("classification", s.Enum("PREVENTION_EVIDENCE_SUMMARY", "PREVENTION_VERIFIED")),
		s.F("prevention", s.Object(s.F("path", s.LiteralString("prevention.json")), s.F("digest", digest)).Strict()),
		s.F("limitations", limitations),
		s.F("rootDigest", digest),
	).Strict()

	RepairedRunsSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.prevention-repaired-runs.v1")),
		s.F("runs", s.Array(repairedRunBinding, s.Length(3))),
	).Strict()
)
```

- [x] **Step 4: Port the verifier**

Port of `src/prevention-proof.ts`: `assertNoLinksOrSpecialFiles` (140–153), `readJson` (225–232), `validatePreventionProofSemantics` (235–270), `validateRepairedRunsArtifact` (272–293), `verifyPreventionProof` (296–378).

`internal/bundle/prevention/verify.go`:

```go
// Package prevention ports src/prevention-proof.ts verification.
package prevention

import (
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/schema"
)

var baseArtifacts = []string{"README.md", "prevention.json", "manifest.json"}

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New("Prevention proof directory must be a real directory: " + directory))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New("Prevention proof directory contains a symbolic link or special file: " + child))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

// readJSON returns Undefined on failure, like the TS helper returning undefined.
func readJSON(path, label string, errs *[]string) jsjson.Value {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		*errs = append(*errs, "Unable to read "+label+": "+err.Error())
		return jsjson.Value{}
	}
	return v
}

func validateSemantics(b jsjson.Value) []string {
	var errs []string
	states := []jsjson.Value{b.Get("lastGood"), b.Get("firstBad"), b.Get("repaired")}
	field := func(name string) []string {
		out := make([]string, len(states))
		for i, st := range states {
			out[i] = st.Get(name).Str()
		}
		return out
	}
	if !b.Get("verified").Bool() {
		errs = append(errs, "prevention package must set verified=true")
	}
	if states[0].Get("verdict").Str() != "PASS" || states[1].Get("verdict").Str() != "FAIL" || states[2].Get("verdict").Str() != "PASS" {
		errs = append(errs, "three-state prevention requires PASS → FAIL → PASS")
	}
	if bundle.DistinctCount(field("witnessDigest")) != 1 {
		errs = append(errs, "all three states must share the same frozen witness digest")
	}
	if b.Get("frozenWitnessDigest").Str() != states[0].Get("witnessDigest").Str() {
		errs = append(errs, "package frozenWitnessDigest must match executed witness digests")
	}
	if bundle.DistinctCount(field("environmentDigest")) != 1 {
		errs = append(errs, "all three states must share the same environment digest")
	}
	if slices.ContainsFunc(states, func(st jsjson.Value) bool {
		return st.Get("executionTrust").Str() != "NATIVE_DOCKER" || st.Get("executionKind").Str() != "EXECUTED"
	}) {
		errs = append(errs, "prevention requires NATIVE_DOCKER EXECUTED evidence on every state")
	}
	if slices.ContainsFunc(states, func(st jsjson.Value) bool { return st.Get("distinctExecutionCount").Num() < 3 }) {
		errs = append(errs, "prevention requires at least three distinct executions per state")
	}
	if bundle.DistinctCount(field("commit")) != 3 {
		errs = append(errs, "last-good, first-bad, and repaired commits must be distinct")
	}
	for _, st := range states {
		ids := bundle.Strings(st.Get("runIds"))
		role := st.Get("role").Str()
		if len(ids) != 3 || bundle.DistinctCount(ids) != 3 {
			errs = append(errs, role+" requires three distinct runIds")
		}
		if st.Get("distinctExecutionCount").Num() != float64(len(ids)) {
			errs = append(errs, role+" distinctExecutionCount must equal runIds length")
		}
	}
	base, firstBadTree := b.Get("repairBaseTree"), b.Get("firstBad", "tree")
	if base.Kind() != jsjson.Undefined && firstBadTree.Kind() != jsjson.Undefined && base.Str() != firstBadTree.Str() {
		errs = append(errs, "repairBaseTree must match first-bad tree when both are present")
	}
	return errs
}

func validateRepairedRuns(body, artifact jsjson.Value) []string {
	var errs []string
	var ids []string
	for _, run := range artifact.Get("runs").Items() {
		ids = append(ids, run.Get("runId").Str())
	}
	if strings.Join(ids, "\x00") != strings.Join(bundle.Strings(body.Get("repaired", "runIds")), "\x00") {
		errs = append(errs, "repaired-runs.json runIds must match prevention.repaired.runIds in order")
	}
	repaired := body.Get("repaired")
	for _, run := range artifact.Get("runs").Items() {
		runID := run.Get("runId").Str()
		tree := repaired.Get("tree")
		if run.Get("commit").Str() != repaired.Get("commit").Str() || tree.Kind() != jsjson.String || run.Get("tree").Str() != tree.Str() {
			errs = append(errs, "repaired run "+runID+" commit/tree does not match repaired state")
		}
		if run.Get("witnessDigest").Str() != body.Get("frozenWitnessDigest").Str() {
			errs = append(errs, "repaired run "+runID+" witness digest mismatch")
		}
	}
	return errs
}

// Verify ports verifyPreventionProof (src/prevention-proof.ts:296).
func Verify(directory, expectedRoot string, rootProvided bool) (result bundle.Result) {
	var errs []string
	var manifest jsjson.Value
	haveManifest, havePrevention := false, false
	var prevention jsjson.Value
	var rootDigest *string
	status := "NOT_PROVIDED"
	if rootProvided {
		status = "MISMATCH"
	}
	finish := func() bundle.Result {
		r := bundle.Result{Valid: len(errs) == 0, Errors: errs, RootDigest: rootDigest, ExternalRootStatus: status}
		if haveManifest {
			c := manifest.Get("classification").Str()
			r.Classification = &c
		}
		return r
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, "Prevention proof verification failed safely: "+err.Error())
		result = finish()
	})

	root := nodefs.Resolve(directory)
	if !nodefs.Exists(root) {
		return bundle.Result{Errors: []string{"Prevention proof directory does not exist"}, ExternalRootStatus: status}
	}
	assertNoLinksOrSpecialFiles(root)
	physical := bundle.Must(nodefs.ReadDirNames(root))
	for _, expected := range baseArtifacts {
		if !slices.Contains(physical, expected) {
			errs = append(errs, "Prevention proof package is missing "+expected)
		}
	}
	for _, actual := range physical {
		if actual != "repaired-runs.json" && !slices.Contains(baseArtifacts, actual) {
			errs = append(errs, "Prevention proof package contains an unexpected artifact: "+actual)
		}
		stat := bundle.Must(nodefs.Lstat(nodefs.Join(root, actual)))
		if !stat.Mode().IsRegular() || isLink(stat) {
			errs = append(errs, "Prevention proof artifact must be a regular non-symlink file: "+actual)
		}
	}

	if out, issues, ok := schema.Parse(ManifestSchema, readJSON(nodefs.Join(root, "manifest.json"), "prevention proof manifest", &errs)); !ok {
		errs = append(errs, "Prevention proof manifest schema validation failed: "+schema.ErrorMessage(issues))
	} else {
		manifest, haveManifest = out, true
		rd := manifest.Get("rootDigest").Str()
		rootDigest = &rd
		if rd != bundle.Must(canonical.DigestJSON(bundle.Without(manifest, "rootDigest"))) {
			errs = append(errs, "Prevention proof root digest does not match its canonical contents.")
		}
		if rootProvided {
			if rd == expectedRoot {
				status = "MATCH"
			} else {
				status = "MISMATCH"
				errs = append(errs, "Prevention proof root digest does not match the externally supplied digest.")
			}
		}
	}

	if out, issues, ok := schema.Parse(BodySchema, readJSON(nodefs.Join(root, "prevention.json"), "prevention body", &errs)); !ok {
		errs = append(errs, "Prevention proof body schema validation failed: "+schema.ErrorMessage(issues))
	} else {
		prevention, havePrevention = out, true
		errs = append(errs, validateSemantics(prevention)...)
		if haveManifest && manifest.Get("prevention", "digest").Str() != bundle.Must(canonical.DigestJSON(prevention)) {
			errs = append(errs, "Prevention proof manifest digest does not match prevention.json.")
		}
		if haveManifest && manifest.Get("classification").Str() == "PREVENTION_VERIFIED" {
			if prevention.Get("repairPatchDigest").Str() == "" {
				errs = append(errs, "PREVENTION_VERIFIED requires repairPatchDigest")
			}
			if prevention.Get("repairBaseTree").Str() == "" {
				errs = append(errs, "PREVENTION_VERIFIED requires repairBaseTree")
			}
			if !slices.Contains(physical, "repaired-runs.json") {
				errs = append(errs, "PREVENTION_VERIFIED package must include repaired-runs.json")
			}
		}
	}

	if slices.Contains(physical, "repaired-runs.json") && havePrevention {
		if out, issues, ok := schema.Parse(RepairedRunsSchema, readJSON(nodefs.Join(root, "repaired-runs.json"), "repaired runs artifact", &errs)); !ok {
			errs = append(errs, "repaired-runs.json schema validation failed: "+schema.ErrorMessage(issues))
		} else {
			errs = append(errs, validateRepairedRuns(prevention, out)...)
		}
	}
	return finish()
}
```

In `validateRepairedRuns`, TS compares `run.tree !== body.repaired.tree`. When `repaired.tree` is absent (undefined), a string never equals it, which is what the `tree.Kind() != jsjson.String` term reproduces.

- [x] **Step 5: Wire dispatch and output**

In `internal/cli/verify.go` replace the prevention `case` with:

```go
	case "faultline.prevention-proof.v1":
		e.printPrevention(prevention.Verify(root, expectedRoot, rootProvided))
		return nil
```

and add (import `github.com/Mizore66/faultline/internal/bundle/prevention`):

```go
// printPrevention ports the prevention branch of the TS verify case.
func (e *env) printPrevention(r bundle.Result) {
	first := "Integrity"
	if r.ExternalRootStatus == "NOT_PROVIDED" {
		first = "Prevention proof self-consistency"
	}
	e.out(first + ": " + verdictWord(r.Valid) + "\n")
	e.out("Classification: " + orUnavailable(r.Classification) + "\nBundle root: " + orUnavailable(r.RootDigest) + "\nExternal root: " + r.ExternalRootStatus + "\n")
	if r.Valid && r.Classification != nil && *r.Classification == "PREVENTION_VERIFIED" {
		e.out("Prevention verified\n")
	} else if r.Valid {
		e.out("Prevention evidence summary\n")
	}
	e.printErrorsAndExit(r)
}
```

Set `portedTypes` to `{"demo": true, "prevention": true}`.

- [x] **Step 6: Add named-schema oracle checks**

Add to the oracle's imports and handlers:

```ts
import { PreventionProofBodySchema, PreventionProofManifestSchema, PreventionRepairedRunsArtifactSchema } from "../../src/prevention-proof.js";

export const namedSchemas: Record<string, { safeParse(v: unknown): any }> = {
  PreventionProofBodySchema,
  PreventionProofManifestSchema,
  PreventionRepairedRunsArtifactSchema,
};
```

```ts
  zodNamed: ({ name, text }: { name: string; text: string }) => {
    const result = namedSchemas[name]!.safeParse(JSON.parse(text));
    if (!result.success) return { success: false, message: result.error.message };
    try {
      return { success: true, canonical: canonicalJson(result.data) };
    } catch (error) {
      return { success: true, canonical: `ERROR:${(error as Error).message}` };
    }
  },
```

`difftest/gen/corrupt.go`:

```go
package gen

import (
	"math/rand/v2"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
)

var deepReplacements = []string{"1", "0", "-1", "1.5", "3", `"x"`, `""`, "null", "[]", "{}", "true", "false",
	`"sha256:` + strings.Repeat("a", 64) + `"`, `"sha256:zz"`, `"` + strings.Repeat("b", 40) + `"`, `"2026-07-16T11:00:00.000Z"`, `"2026-02-30T00:00:00.000Z"`}

// CorruptDeep walks to a random node of a JSON document and deletes a key,
// adds an unknown key, drops an array element, or replaces the node.
func CorruptDeep(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	node := root
	for depth := 0; depth < 8 && r.IntN(4) != 0; depth++ {
		switch node.Kind() {
		case jsjson.Object:
			keys := node.Obj().Keys()
			if len(keys) == 0 {
				break
			}
			child := node.Obj().Field(keys[r.IntN(len(keys))])
			if child.Kind() != jsjson.Object && child.Kind() != jsjson.Array {
				break
			}
			node = child
		case jsjson.Array:
			if len(node.Items()) == 0 {
				break
			}
			child := node.Items()[r.IntN(len(node.Items()))]
			if child.Kind() != jsjson.Object && child.Kind() != jsjson.Array {
				break
			}
			node = child
		}
	}
	replacement, _ := jsjson.Parse(pick(r, deepReplacements...))
	switch node.Kind() {
	case jsjson.Object:
		keys := node.Obj().Keys()
		switch {
		case len(keys) == 0 || r.IntN(4) == 0:
			node.Obj().Set("zzUnknown", replacement)
		case r.IntN(2) == 0:
			node.Obj().Delete(keys[r.IntN(len(keys))])
		default:
			node.Obj().Set(keys[r.IntN(len(keys))], replacement)
		}
	case jsjson.Array:
		items := node.Items()
		if len(items) > 0 {
			items[r.IntN(len(items))] = replacement // arrays share backing storage with the tree
		}
	}
	return jsjson.Stringify(root)
}
```

`jsjson.Value.Items()` returns the backing slice, so replacing an element mutates the tree in place (intended here).

`difftest/named_live_test.go`:

```go
package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

// namedSchemas maps oracle names to Go schemas; later tasks add entries.
var namedSchemas = map[string]schema.Schema{
	"PreventionProofBodySchema":            prevention.BodySchema,
	"PreventionProofManifestSchema":        prevention.ManifestSchema,
	"PreventionRepairedRunsArtifactSchema": prevention.RepairedRunsSchema,
}

// namedSeeds lists base files (relative to difftest/testdata/bases) that satisfy each schema.
var namedSeeds = map[string][]string{
	"PreventionProofBodySchema":            {"prevention-summary/prevention.json", "prevention-verified/prevention.json"},
	"PreventionProofManifestSchema":        {"prevention-summary/manifest.json", "prevention-verified/manifest.json"},
	"PreventionRepairedRunsArtifactSchema": {"prevention-verified/repaired-runs.json"},
}

func TestLiveNamedSchemas(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	r := rand.New(rand.NewPCG(13, 14))
	for name, seeds := range namedSeeds {
		for _, seed := range seeds {
			raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(seed)))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < liveCases/len(namedSeeds)/len(seeds); i++ {
				text := string(raw)
				if i > 0 {
					text = gen.CorruptDeep(r, text)
				}
				want := call(t, c, "zodNamed", `{"name":"`+name+`","text":`+jsjson.Quote(text)+`}`)
				value, _ := jsjson.Parse(text)
				out, issues, ok := schema.Parse(namedSchemas[name], value)
				if ok != want.Field("success").Bool() {
					t.Fatalf("%s on %s: ok=%v, zod=%v\n%s", name, text, ok, want.Field("success").Bool(), want.Field("message").Str())
				}
				if !ok {
					if got := schema.ErrorMessage(issues); got != want.Field("message").Str() {
						t.Fatalf("%s on %s:\ngot  %s\nwant %s", name, text, got, want.Field("message").Str())
					}
					continue
				}
				got, err := canonical.CanonicalJSON(out)
				if err != nil {
					got = "ERROR:" + err.Error()
				}
				if got != want.Field("canonical").Str() {
					t.Fatalf("%s on %s: data %s, zod %s", name, text, got, want.Field("canonical").Str())
				}
			}
		}
	}
}
```

- [x] **Step 7: Run tests**

Run: `go test ./internal/bundle/prevention/ && go test ./difftest/ -run TestGoldenReplay && FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveNamedSchemas`
Expected: PASS; all `prevention-*` goldens match.

- [x] **Step 8: Commit**

```bash
git add internal/bundle/prevention internal/cli difftest
git commit -m "feat(go): port faultline.prevention-proof.v1 verifier

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Git proof schemas and their dependency schemas

**Files:**
- Create: `internal/bundle/gitproof/schemas.go`, `internal/bundle/gitproof/witness_schemas.go`, `internal/bundle/gitproof/matchers.go`, `internal/bundle/gitproof/schemas_test.go`
- Modify: `difftest/named_live_test.go` (registry + seeds), `difftest/gen/node-oracle.ts` (`namedSchemas` entries)

**Interfaces:**
- Consumes: `schema.*`.
- Produces (package `gitproof`, exported for the oracle registry): `GitCommitStateSchema`, `GitInvestigationRunFactSchema`, `StableGitStateSchema`, `StableGitTransitionSchema`, `GitInvestigationResultSchema`, `EnvironmentFingerprintSchema`, `MaterializedOverlaySchema`, `FrozenWitnessSchema`, `GitProofSourceMetadataSchema`, `GitProofBundleManifestSchema`; unexported `lifecycleLegacyBoundSchema`, `lifecyclePartiallyBoundSchema`, `lifecycleFullyBoundSchema`, `lifecycleUnboundSchema` (used by Task 14); matchers `isSafeRelativeArtifactPath(string) bool` (also the witness `isSafeOverlayPath`), `safeGitRevision(string) bool`, `isCanonicalBase64(string) bool`; constants `GitInvestigationSchemaVersion = "faultline.git-investigation.v1"`, `StableExecutionCount = 3`, `BundleHeadRef = "refs/faultline/portable-descendant"`.

Transcribe these TS ranges 1:1, keeping field order, strictness, and every custom message:

| TS source | Go |
| --- | --- |
| `src/git-investigation.ts:62-78` (constants, `DigestSchema` with message `expected sha256:<64 lowercase hex characters>`, `GitObjectIdSchema` with message `expected a 40- or 64-character lowercase Git object id`, `GitRevisionSchema`, `TimestampSchema`, `GitRangeSchema`) | `schemas.go` |
| `src/git-investigation.ts:124-298` (`GitCommitStateSchema` … `GitInvestigationResultSchema`) | `schemas.go` |
| `src/safe-overlay.ts:12-16` (`MaterializedOverlaySchema`) | `schemas.go` |
| `src/environment-fingerprint.ts:8,47,53-57` (`EnvironmentFingerprintSchema`, version `faultline.environment-fingerprint.v2`; its digest regex has **no** custom message) | `schemas.go` |
| `src/evidence-grade.ts:31` (`COMMIT_PROOF`) | `schemas.go` |
| `src/witness-lock.ts:7-117` (all schemas feeding `FrozenWitnessSchema`) | `witness_schemas.go` |
| `src/git-proof-bundle.ts:44-176` (constants, artifact/lifecycle/source/manifest schemas) | `schemas.go` |

**Translation rules for this task:**
- `z.string().regex(RE, "msg")` → `s.String(s.Regex(s.Pattern(src), "msg"))`. Copy each `src` from the TS literal; all are ASCII RE2-compatible except the next item.
- `SAFE_GIT_REVISION = /^(?!-)[^\0\r\n]{1,512}$/` → `safeGitRevision`: UTF-16 length 1–512, first unit not `-`, no NUL/CR/LF. Use it as `s.Regex(safeGitRevision, "revision must be non-empty, cannot start with '-', and cannot contain NUL or line breaks")` (zod still reports it as a `regex` issue).
- `.refine(isSafeRelativeArtifactPath, …)`, `.refine(isSafeOverlayPath, …)`, `.refine(isCanonicalBase64, …)` → `s.Refine`. `isCanonicalBase64` checks `^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`, then that base64-decoding and re-encoding reproduces the input (`base64.StdEncoding`).
- `z.string().trim().min(1).max(512)` → `s.String(s.Trim(), s.MinLength(1), s.MaxLength(512))`.
- `z.string().datetime({ offset: true })` → `s.String(s.Datetime())`; `z.string().uuid()` → `s.String(s.UUID())`.
- `z.record(DigestSchema)` → `s.Record(...)`; `z.discriminatedUnion("status", [...])` → `s.DiscriminatedUnion("status", ...)`.
- `.extend({...}).strict()` → `.Extend(...).Strict()`.
- `EVIDENCE_LOG_PREVIEW_BYTES * 2` = 32768.
- The request/sandbox schemas with `.default` (`git-investigation.ts:80-122`) are not used by any verifier and are not ported.

- [x] **Step 1: Write failing tests**

`internal/bundle/gitproof/schemas_test.go`:

```go
package gitproof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func baseFile(t *testing.T, base, rel string) jsjson.Value {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCommittedBasesSatisfySchemas(t *testing.T) {
	for _, base := range []string{"git-unbound", "git-fully-bound", "git-sha256", "git-sample-self-incident"} {
		for rel, s := range map[string]schema.Schema{
			"manifest.json":        GitProofBundleManifestSchema,
			"investigation.json":   GitInvestigationResultSchema,
			"witness/frozen.json":  FrozenWitnessSchema,
			"source/metadata.json": GitProofSourceMetadataSchema,
		} {
			if _, issues, ok := schema.Parse(s, baseFile(t, base, rel)); !ok {
				t.Errorf("%s/%s: %s", base, rel, schema.ErrorMessage(issues))
			}
		}
	}
}

func TestSafeGitRevision(t *testing.T) {
	for in, want := range map[string]bool{"HEAD~2": true, "-x": false, "": false, "a\nb": false, "a\x00": false} {
		if safeGitRevision(in) != want {
			t.Errorf("safeGitRevision(%q) != %v", in, want)
		}
	}
}

func TestCanonicalBase64(t *testing.T) {
	for in, want := range map[string]bool{"QQ==": true, "QR==": false, "QUI=": true, "QUJD": true, "QQ": false, "": true} {
		if isCanonicalBase64(in) != want {
			t.Errorf("isCanonicalBase64(%q) != %v", in, want)
		}
	}
}
```

- [x] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/gitproof/`
Expected: FAIL (undefined schemas).

- [x] **Step 3: Implement matchers**

`internal/bundle/gitproof/matchers.go`:

```go
package gitproof

import (
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// isSafeRelativeArtifactPath ports git-proof-bundle.ts:67 (and witness-lock.ts:14 isSafeOverlayPath).
func isSafeRelativeArtifactPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.Contains(value, "\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func safePathValue(v jsjson.Value) bool { return isSafeRelativeArtifactPath(v.Str()) }

// safeGitRevision is /^(?!-)[^\0\r\n]{1,512}$/ counted in UTF-16 units.
func safeGitRevision(value string) bool {
	units := jsstr.ToUTF16(value)
	if len(units) < 1 || len(units) > 512 || units[0] == '-' {
		return false
	}
	for _, u := range units {
		if u == 0 || u == '\r' || u == '\n' {
			return false
		}
	}
	return true
}

var base64Shape = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)

// isCanonicalBase64 ports witness-lock.ts:20.
func isCanonicalBase64(value string) bool {
	if !base64Shape.MatchString(value) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && base64.StdEncoding.EncodeToString(decoded) == value
}

func canonicalBase64Value(v jsjson.Value) bool { return isCanonicalBase64(v.Str()) }
```

- [x] **Step 4: Transcribe the schemas**

Write `schemas.go` and `witness_schemas.go` per the table. Example of the expected style (this is `src/git-proof-bundle.ts:74-113` in full; transcribe every other schema the same way):

```go
var (
	gitDigest = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "expected sha256:<64 lowercase hex characters>"))

	artifactPathSchema = s.Refine(s.String(s.MinLength(1), s.MaxLength(1024)), safePathValue, "artifact path must be a safe POSIX-relative path")

	runArtifactSchema        = s.Object(s.F("runId", gitDigest), s.F("path", artifactPathSchema)).Strict()
	transitionArtifactSchema = s.Object(s.F("index", s.Number(s.Int(), s.Nonnegative())), s.F("path", artifactPathSchema)).Strict()

	lifecycleUnboundSchema = s.Object(
		s.F("status", s.LiteralString("UNBOUND")),
		s.F("limitation", s.LiteralString("No caller-supplied Codex lifecycle ledger is bound to this Git investigation package.")),
	).Strict()

	lifecycleBindingFields = s.Object(
		s.F("path", s.LiteralString("lifecycle/ledger.json")),
		s.F("ledgerDigest", gitDigest),
		s.F("headHash", gitDigest),
		s.F("transport", s.Enum("CODEX_CLI", "CODEX_APP", "SIDE_CAR", "OBSERVED_EXTERNAL_TRANSPORT")),
		s.F("checkpointBindings", s.Array(s.Object(
			s.F("sequence", s.Number(s.Int(), s.Positive())),
			s.F("stateIndex", s.Number(s.Int(), s.Nonnegative())),
			s.F("checkpointDigest", gitDigest),
		).Strict(), s.MinItems(1))),
	).Strict()

	lifecycleLegacyBoundSchema    = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("BOUND"))).Strict()
	lifecyclePartiallyBoundSchema = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("PARTIALLY_BOUND"))).Strict()
	lifecycleFullyBoundSchema     = lifecycleBindingFields.Extend(s.F("status", s.LiteralString("FULLY_BOUND"))).Strict()

	lifecycleBindingSchema = s.DiscriminatedUnion("status", lifecycleUnboundSchema, lifecycleLegacyBoundSchema, lifecyclePartiallyBoundSchema, lifecycleFullyBoundSchema)
)
```

`DiscriminatedUnion` finds the discriminator by looking up the `status` field of each option. It needs `(*ObjectSchema).field` to see extended fields, which `Extend` already provides.

- [x] **Step 5: Run unit tests**

Run: `go test ./internal/bundle/gitproof/`
Expected: PASS.

- [x] **Step 6: Extend the named-schema oracle check**

Add to the oracle's `namedSchemas` (with imports from `../../src/git-investigation.js`, `../../src/git-proof-bundle.js`, `../../src/witness-lock.js`, `../../src/environment-fingerprint.js`, `../../src/safe-overlay.js`): `GitCommitStateSchema`, `GitInvestigationRunFactSchema`, `StableGitStateSchema`, `StableGitTransitionSchema`, `GitInvestigationResultSchema`, `EnvironmentFingerprintSchema`, `MaterializedOverlaySchema`, `FrozenWitnessSchema`, `GitProofSourceMetadataSchema`, `GitProofBundleManifestSchema`.

Add the same names to `namedSchemas` in `difftest/named_live_test.go` (mapping to `gitproof.*`), plus seeds:

```go
	"GitProofBundleManifestSchema":  {"git-unbound/manifest.json", "git-partially-bound/manifest.json", "git-fully-bound/manifest.json", "git-sample-self-incident/manifest.json"},
	"GitInvestigationResultSchema":  {"git-unbound/investigation.json", "git-sha256/investigation.json"},
	"FrozenWitnessSchema":           {"git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json"},
	"GitProofSourceMetadataSchema":  {"git-unbound/source/metadata.json", "git-sha256/source/metadata.json"},
	"StableGitTransitionSchema":     {"git-unbound/transitions/0000.json"},
	"GitInvestigationRunFactSchema": {firstRunArtifact("git-unbound")},
```

For `GitInvestigationRunFactSchema`, add a helper `firstRunArtifact(base string) string` that returns `base + "/runs/" + <first file name in that directory>`. For `GitCommitStateSchema`, `StableGitStateSchema`, `EnvironmentFingerprintSchema`, and `MaterializedOverlaySchema`, seed with a JSON sub-document instead: extend the test so a seed of the form `"git-unbound/investigation.json#states.0"` means "parse the file and follow the dotted path (numeric segments index arrays)". Use `#states.0`, `#stableStates.0`, `#environment.fingerprints.0.fingerprint`, and `#runs.0.overlays.0`. If `git-unbound` has an empty `environment.fingerprints`, pick the first base that has one.

> **Amendment (during execution):** with the `git-sha256` base dropped (Task 7), the unit test and seeds use `git-two-states` in its place.

- [x] **Step 7: Run live test and commit**

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveNamedSchemas -v`
Expected: PASS for every schema.

```bash
git add internal/bundle/gitproof difftest
git commit -m "feat(go): port git proof, investigation, and witness schemas

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Frozen witness verification (`witness-lock.ts`)

**Files:**
- Create: `internal/bundle/gitproof/witness.go`, `internal/bundle/gitproof/witness_test.go`, `difftest/function_live_test.go`
- Modify: `difftest/gen/node-oracle.ts` (add `verifyFrozenWitnessRecord`), `go.mod` (already has `golang.org/x/text`)

**Interfaces:**
- Consumes: `FrozenWitnessSchema`, `canonical.*`, `bundle.*`.
- Produces: `type WitnessVerification struct{ Valid bool; Errors []string; ProposalDigest, FrozenDigest *string; Approval *WitnessApproval; ExternalDigestStatus string }`, `type WitnessApproval struct{ Actor, ApprovedAt string }`, `VerifyFrozenWitnessRecord(input jsjson.Value, expected string, expectedProvided bool) WitnessVerification`, `(WitnessVerification) ErrorsValue() jsjson.Value`, `(WitnessVerification) ApprovalValue() jsjson.Value` (`{actor, approvedAt}` or null, for `sameCanonical` checks in Task 14), `(WitnessVerification) JSON() jsjson.Value` (the whole TS result object, keys in TS order: `valid, errors, proposalDigest, frozenDigest, approval, externalDigestStatus`).

Transcribe `src/witness-lock.ts`: `byteDigest`/`commandDigest`/`overlayDigest` (143–153), the payload helpers (155–183), `assertSortedOverlayPaths`/`assertUniqueOverlayPathsForVerification` (196–215), `verifyProposal` (317–337), `verifyApproval` (339–348), `verifyFrozenRecord` (350–381), `verifyFrozenWitnessRecord` (467–485).

**Gotchas:**
- `commandDigest` hashes `Buffer.from(command, "utf8")`: use `canonical.SHA256Hex(command)` (UTF-8 conversion replaces lone surrogates).
- Overlay bytes: `Buffer.from(overlay.bytesBase64, "base64")`. Input is schema-validated canonical base64, so `base64.StdEncoding.DecodeString` matches.
- `toLocaleLowerCase("en-US")` is full Unicode lowercasing (`"İ"` → `"i̇"`). Use `cases.Lower(language.AmericanEnglish).String(path)` from `golang.org/x/text/cases`, not `strings.ToLower`.
- Overlay order check uses `canonical.LocaleCompare(prev, cur) > 0`.
- `DigestSchema.safeParse(expected).success` for the external digest is the regex `^sha256:[a-f0-9]{64}$`.
- `witnessPayload` builds a new object with exactly the keys listed at 171–182 (order is irrelevant after canonicalization).
- On schema failure, the single error is `frozen witness schema validation failed: <ZodError.message>` and status is `NOT_PROVIDED` or `MISMATCH` depending on whether an expected digest was supplied.

- [x] **Step 1: Write the failing unit test**

`internal/bundle/gitproof/witness_test.go`:

```go
package gitproof

import "testing"

func TestCommittedFrozenWitnessVerifies(t *testing.T) {
	frozen := baseFile(t, "git-unbound", "witness/frozen.json")
	digest := frozen.Get("frozenDigest").Str()
	v := VerifyFrozenWitnessRecord(frozen, digest, true)
	if !v.Valid || v.ExternalDigestStatus != "MATCH" || v.Approval == nil || v.Approval.Actor != "reviewer@example.test" {
		t.Fatalf("%+v", v)
	}
	if v := VerifyFrozenWitnessRecord(frozen, "nope", true); v.Valid || v.ExternalDigestStatus != "MISMATCH" || v.Errors[0] != "expected frozen digest is not a valid sha256 digest" {
		t.Fatalf("%+v", v)
	}
}
```

- [x] **Step 2: Run to verify failure, then implement**

Run: `go test ./internal/bundle/gitproof/ -run Witness`
Expected: FAIL (undefined: VerifyFrozenWitnessRecord). Transcribe the listed functions into `witness.go`, then rerun.
Expected: PASS.

- [x] **Step 3: Add the oracle op and live test**

Oracle (import `verifyFrozenWitnessRecord` from `../../src/witness-lock.js`):

```ts
  verifyFrozenWitnessRecord: ({ text, expected }: { text: string; expected?: string }) =>
    canonicalJson(verifyFrozenWitnessRecord(JSON.parse(text), expected)),
```

`difftest/function_live_test.go` holds a shared comparison helper that Tasks 12 and 13 reuse:

```go
package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// seedTexts reads files relative to difftest/testdata/bases.
func seedTexts(t *testing.T, rels ...string) []string {
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	var out []string
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

func canonicalOf(v jsjson.Value) string {
	s, err := canonical.CanonicalJSON(v)
	if err != nil {
		return "ERROR:" + err.Error()
	}
	return s
}

// liveCompare calls an oracle op that returns a string (canonical JSON, or
// "THREW:<message>" when TS threw) and compares it with the Go result.
func liveCompare(t *testing.T, op string, next func(r *rand.Rand, i int) (args, got string)) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(15, uint64(len(op))))
	for i := 0; i < liveCases; i++ {
		args, got := next(r, i)
		raw, err := c.Call(op, args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if strings.HasPrefix(want.Str(), "THREW:") {
			continue
		}
		if got != want.Str() {
			t.Fatalf("%s(%s):\ngot  %s\nwant %s", op, args, got, want.Str())
		}
	}
}

func TestLiveWitness(t *testing.T) {
	seeds := seedTexts(t, "git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json")
	liveCompare(t, "verifyFrozenWitnessRecord", func(r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptDeep(r, text)
		}
		value, _ := jsjson.Parse(text)
		expected, provided := "", false
		switch r.IntN(3) {
		case 1:
			expected, provided = value.Get("frozenDigest").Str(), true
		case 2:
			expected, provided = "nope", true
		}
		args := `{"text":` + jsjson.Quote(text)
		if provided {
			args += `,"expected":` + jsjson.Quote(expected)
		}
		return args + "}", canonicalOf(gitproof.VerifyFrozenWitnessRecord(value, expected, provided).JSON())
	})
}
```

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveWitness -v`
Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add internal/bundle/gitproof difftest
git commit -m "feat(go): port frozen witness verification

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Codex lifecycle ledger schema and verification (`ledger.ts`)

**Files:**
- Create: `internal/bundle/gitproof/ledger.go`, `internal/bundle/gitproof/ledger_test.go`
- Modify: `difftest/gen/node-oracle.ts` (`verifyCodexLifecycleLedger` op; `CodexLifecycleLedgerSchema` in `namedSchemas`), `difftest/named_live_test.go` (registry + seeds from `git-partially-bound/lifecycle/ledger.json` and `git-fully-bound/lifecycle/ledger.json`), `difftest/function_live_test.go` (add `TestLiveLedger`)

**Interfaces:**
- Consumes: `schema.*`, `canonical.*`.
- Produces: `CodexLifecycleLedgerSchema schema.Schema`; `type LedgerVerification struct{ Valid bool; Errors []string; HeadHash *string; … }` holding every field of the TS `LedgerVerification` result; `VerifyCodexLifecycleLedger(v jsjson.Value) LedgerVerification`; `(LedgerVerification) JSON() jsjson.Value` (keys in TS order).

Transcribe `src/ledger.ts`: constants and schemas (27–220, including `StrictObject`, `IdentifierSchema`, `HashSchema`/`GitObjectIdSchema` with their custom messages, `CanonicalTimestampSchema`, all payload schemas, and both discriminated unions), `ledgerGenesisHash` (222–231), `hashLifecycleEvent` (233–235), `verifyGitCheckpoint` and its signing digest (237–249), `validateLifecycleState` (351–503), and `verifyCodexLifecycleLedger` (505–564). Read the `LedgerVerification` type definition in `ledger.ts` to get the exact result fields.

> **Amendment (during execution):** `validateLifecycleState` also depends on `src/turn-snapshot.ts` (`UnsignedTurnTreeSnapshotSchema`/`TurnTreeSnapshotSchema` 56–78 and `verifyTurnTreeSnapshot` 295–302). Both are pure (schema + digest check) and are ported into `ledger.go`. `canonicalTimestamp` validates the canonical shape and field ranges directly (equivalent to the round-trip, since any rollover changes the output); a one-off check against V8 on 200k edge-heavy inputs found 0 mismatches.

**Gotchas:**
- `CanonicalTimestampSchema` is `refine(value => new Date(value).toISOString() === value, "Expected a canonical ISO-8601 UTC timestamp")`. Implement `canonicalTimestamp(s string) bool` as: match `^([+-]\d{6}|\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{3})Z$`, reject `-000000`, compute epoch milliseconds with ECMAScript `MakeDay`/`MakeTime` arithmetic (month 1–12 and day 1–31 accepted by V8's ISO parser, hour 0–24, minute and second 0–59, with rollover), require `|ms| ≤ 8.64e15`, and require that formatting the milliseconds back as `toISOString` (`YYYY` for years 0–9999, else `±YYYYYY`) returns the input. Unit-test it with the V8 results captured during planning: `2021-02-30T00:00:00.000Z` → false (V8 rolls it to March 2), `2021-02-28T24:00:00.000Z` → false, `+275760-09-13T00:00:00.000Z` → true, `0000-01-01T00:00:00.000Z` → true, `2021-04-31T00:00:00.000Z` → false.
- Hash chains use `digestJson` exactly as TS composes the objects; keep key sets identical.
- Error strings and their order must match; the golden cases under `git-*-bound` exercise them.

- [x] **Step 1: Write failing tests**

`internal/bundle/gitproof/ledger_test.go`:

```go
package gitproof

import "testing"

func TestCanonicalTimestamp(t *testing.T) {
	for in, want := range map[string]bool{
		"2026-07-16T11:00:00.000Z":    true,
		"2021-02-30T00:00:00.000Z":    false,
		"2021-02-28T24:00:00.000Z":    false,
		"+275760-09-13T00:00:00.000Z": true,
		"0000-01-01T00:00:00.000Z":    true,
		"2021-04-31T00:00:00.000Z":    false,
		"2026-07-16T11:00:00Z":        false,
		"-000000-01-01T00:00:00.000Z": false,
	} {
		if canonicalTimestamp(in) != want {
			t.Errorf("canonicalTimestamp(%q) != %v", in, want)
		}
	}
}

func TestCommittedLedgersVerify(t *testing.T) {
	for _, base := range []string{"git-partially-bound", "git-fully-bound"} {
		v := VerifyCodexLifecycleLedger(baseFile(t, base, "lifecycle/ledger.json"))
		if !v.Valid || v.HeadHash == nil {
			t.Fatalf("%s: %+v", base, v)
		}
	}
}
```

- [x] **Step 2: Run to verify failure, implement, rerun**

Run: `go test ./internal/bundle/gitproof/ -run 'Timestamp|Ledger'`
Expected: FAIL first; after transcription, PASS.

- [x] **Step 3: Add the oracle op and live test**

Oracle (import `verifyCodexLifecycleLedger` from `../../src/ledger.js`):

```ts
  verifyCodexLifecycleLedger: ({ text }: { text: string }) => canonicalJson(verifyCodexLifecycleLedger(JSON.parse(text))),
```

Append to `difftest/function_live_test.go`:

```go
func TestLiveLedger(t *testing.T) {
	seeds := seedTexts(t, "git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json")
	liveCompare(t, "verifyCodexLifecycleLedger", func(r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptDeep(r, text)
		}
		value, _ := jsjson.Parse(text)
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(gitproof.VerifyCodexLifecycleLedger(value).JSON())
	})
}
```

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run 'TestLive(Ledger|NamedSchemas)' -v`
Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add internal/bundle/gitproof difftest
git commit -m "feat(go): port Codex lifecycle ledger schema and verification

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Sandbox audit validation (`sandbox.ts`)

**Files:**
- Create: `internal/bundle/gitproof/sandbox.go`, `internal/bundle/gitproof/sandbox_test.go`
- Modify: `difftest/gen/node-oracle.ts` (add `validateSandboxPlanAudit`), `difftest/gen/corrupt.go` (add `CorruptLeaf`), `difftest/function_live_test.go` (add `TestLiveSandbox`)

**Interfaces:**
- Consumes: `canonical.*`.
- Produces: `ValidateSandboxPlanAudit(audit jsjson.Value) []string` (input is a schema-validated run `sandbox` object); `gen.CorruptLeaf(r *rand.Rand, text string) string` (changes one leaf value but keeps its JSON type, so the input stays schema-shaped).

Transcribe `src/sandbox.ts`: `SANDBOX_POLICY_VERSION`, `LEGACY_SANDBOX_POLICY_VERSION`, `SANDBOX_SOURCE_TARGET`, `LEGACY_SANDBOX_SOURCE_TARGET`, `ENVIRONMENT_POLICY_VERSION`, `MAX_SANDBOX_LIMITS`, `DETERMINISTIC_ENVIRONMENT`, `ENVIRONMENT_NAME`, `IMAGE_REFERENCE` (14–55); `dockerPolicyPayload`/`unsafeLocalPolicyPayload` (387–450); `validateSandboxPlanAudit` (615–709).

**Gotchas:**
- `Object.entries(MAX_SANDBOX_LIMITS)` iterates in object-literal order; keep that order in a Go slice of pairs so error lines come out in the same order.
- `Number.isInteger(value)` is false for Undefined, non-numbers, and ±Infinity.
- `JSON.stringify([...fixedKeys].sort(localeCompare)) !== JSON.stringify(expectedFixed)`: compare `jsjson.Stringify` of both sorted arrays.
- `ENVIRONMENT_NAME` is `^[A-Z_][A-Z0-9_]*$`.
- `runtime.image ?? ""` passes `""` when image is null.

- [ ] **Step 1: Write the failing unit test**

`internal/bundle/gitproof/sandbox_test.go`:

```go
package gitproof

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommittedRunAuditsValidate(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", "git-unbound", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		run := baseFile(t, "git-unbound", "runs/"+e.Name())
		if errs := ValidateSandboxPlanAudit(run.Get("sandbox")); len(errs) != 0 {
			t.Fatalf("%s: %q", e.Name(), errs)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure, implement, rerun**

Run: `go test ./internal/bundle/gitproof/ -run Audit`
Expected: FAIL first; after transcription, PASS.

- [ ] **Step 3: Add the oracle op, leaf corruption, and live test**

Oracle (import `validateSandboxPlanAudit` from `../../src/sandbox.js`):

```ts
  validateSandboxPlanAudit: ({ text }: { text: string }) => {
    try {
      return canonicalJson(validateSandboxPlanAudit(JSON.parse(text)));
    } catch (error) {
      return `THREW:${(error as Error).message}`;
    }
  },
```

Append to `difftest/gen/corrupt.go`:

```go
// CorruptLeaf changes one scalar leaf or reverses one array, keeping JSON types.
func CorruptLeaf(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil {
		return text
	}
	var parents []jsjson.Value
	var keys []string
	var walk func(v jsjson.Value)
	walk = func(v jsjson.Value) {
		if v.Kind() != jsjson.Object {
			return
		}
		for _, k := range v.Obj().Keys() {
			parents = append(parents, v)
			keys = append(keys, k)
			walk(v.Obj().Field(k))
		}
	}
	walk(root)
	if len(keys) == 0 {
		return text
	}
	i := r.IntN(len(keys))
	parent, key := parents[i].Obj(), keys[i]
	switch leaf := parent.Field(key); leaf.Kind() {
	case jsjson.String:
		parent.Set(key, jsjson.MakeString(pick(r, leaf.Str()+"x", "", "sha256:"+strings.Repeat("c", 64), "bad name", "IMG@sha256:"+strings.Repeat("d", 64))))
	case jsjson.Number:
		parent.Set(key, jsjson.MakeNumber(pick(r, leaf.Num()+1, 0, -1, 1.5, 1e12)))
	case jsjson.Bool:
		parent.Set(key, jsjson.MakeBool(!leaf.Bool()))
	case jsjson.Array:
		items := leaf.Items()
		for a, b := 0, len(items)-1; a < b; a, b = a+1, b-1 {
			items[a], items[b] = items[b], items[a]
		}
	}
	return jsjson.Stringify(root)
}
```

Append to `difftest/function_live_test.go`. Seeds are the `sandbox` objects of the committed run facts; TS throws only on shapes the schema would already reject, so those iterations are skipped via `THREW:`.

```go
func TestLiveSandbox(t *testing.T) {
	dir := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "git-unbound", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seeds []string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		run, _ := jsjson.Parse(string(raw))
		seeds = append(seeds, jsjson.Stringify(run.Get("sandbox")))
	}
	liveCompare(t, "validateSandboxPlanAudit", func(r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptLeaf(r, text)
		}
		value, _ := jsjson.Parse(text)
		var items []jsjson.Value
		for _, e := range gitproof.ValidateSandboxPlanAudit(value) {
			items = append(items, jsjson.MakeString(e))
		}
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(jsjson.MakeArray(items))
	})
}
```

Run: `FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLiveSandbox -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/bundle/gitproof difftest
git commit -m "feat(go): port sandbox audit validation

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Git proof bundle verifier (`faultline.git-proof-bundle.v1`)

**Files:**
- Create: `internal/bundle/gitproof/git.go` (git process helpers), `internal/bundle/gitproof/semantics.go`, `internal/bundle/gitproof/verify.go`, `internal/bundle/gitproof/verify_test.go`
- Modify: `internal/cli/verify.go` (git case), `difftest/replay_test.go` (`"git": true`)

**Interfaces:**
- Consumes: everything from Tasks 10–13; `bundle.*`, `nodefs.*`, `canonical.*`.
- Produces: `gitproof.Verify(directory, expectedRoot string, rootProvided bool) bundle.Result`.

Transcribe `src/git-proof-bundle.ts`:

| TS | Go file |
| --- | --- |
| constants 47–56, `digestBytes` 214, `sameCanonical` 222, `safeArtifactPath` 230–239, `assertNoLinksOrSpecialFiles` 241–255, `assertRegularFile` 302–307, `readBoundedFile` 309–312, `collectFiles` 314–337, `runArtifactPath` 346, `transitionArtifactPath` 350, `requiredArtifactPaths` 354–367, `parseJsonFile` 852–860, `parseHashCatalog` 862–888, `expectedManifestArtifacts` 970–982, `verifyGitInvestigationProofBundle` 1142–1260 | `verify.go` |
| `toGitPath` 376, `runGit` 380–396, `gitBytes` 398–405, `gitText` 407–409, `resolveGitState` 411–418, `parseBundleHeads` 451–459, `binaryRangePatch` 486–500, `sourceMetadataFromArtifacts` 890–911, `verifyPortableGitSource` 913–968 | `git.go` |
| `expectedExecutionId` 502–512, `expectedRunId` 514–517, `reconstructStableStates` 519–546, `reconstructTransitions` 548–561, `hasLifecycleLedger` 565, `bindLifecycleLedger` 581–632, `validateGitInvestigationProofSemantics` 634–795, `verifyLifecycleBinding` 984–1027 | `semantics.go` |

**Gotchas:**
- `runGit` is `spawnSync("git", args, { encoding: "buffer", maxBuffer: 4 MiB, shell: false })`. Go: `exec.Command("git", args...)`, no shell, inheriting the environment (the harness sets git isolation). Emulate `maxBuffer`: if stdout or stderr exceeds 4 MiB, kill the process and report the error message `spawnSync git ENOBUFS` with a null status. If git cannot start, the error message is `spawnSync git ENOENT`. `gitBytes` detail is `[stderr.toString("utf8").trim(), error?.message].filter(Boolean).join("; ")`, or `exit <status>` (`exit null` when status is null).
- `gitText` = `jsstr.Trim(nodefs.DecodeUTF8(stdout))`.
- `parseBundleHeads`: split on `\r?\n`, drop empty lines, `line.trim()`, then match `^([a-f0-9]{40}|[a-f0-9]{64})\s+(\S+)$` using JS `\s` (`jsstr.IsWhitespace`) and `\S`. Write it as a small hand parser, not RE2 (RE2 `\s` is ASCII-only).
- `rev-list` output split on `/\r?\n/` with empty entries dropped.
- The verifier's temp repository comes from `nodefs.MkdirTemp("faultline-git-proof-verify-")` and is removed in a `defer` (the TS `finally`), even when a `bundle.Throw` unwinds through it.
- `validateGitInvestigationProofSemantics` calls `VerifyFrozenWitnessRecord(frozen, frozen.frozenDigest, true)` and compares `result.witness.errors` and `result.witness.approval` using `SameCanonical` against `ErrorsValue()`/`ApprovalValue()`.
- `String(result.proof.evidenceGrade) === "AGENT_DRAFT"` and `(note ?? "").includes(...)`: use `.Str()` (Undefined gives `""`).
- `run.sandbox.commandDigest !== digestBytes(Buffer.from(command, "utf8"))` → `"sha256:" + canonical.SHA256Hex(command)`.
- `result.runs.find(candidate => candidate.commit === entry.commit)`: first match.
- The heterogeneous-mapping `Set` of images counts `undefined` as a distinct member. Represent a missing image as a sentinel string that cannot collide with a real image (for example `"\x00undefined"`).
- `/@sha256:[a-f0-9]{64}$/` and `DIGEST_PINNED_IMAGE` are ASCII RE2 patterns; `runtime.image ?? ""`.
- `LifecycleLegacyBoundSchema.parse({ ...rebound, status: "BOUND" })`: this parse can throw. It sits inside a TS `try`, so its ZodError message lands in `lifecycle ledger cannot bind to the investigation: …`.
- Output assembly: when `manifest` failed to parse, TS returns `valid: false` with `checkedFiles: catalog.size`; in the outer catch, `checkedFiles: 0`.
- In `verifyGitInvestigationProofBundle`, `expectedRoot` uses `=== undefined` semantics (so `""` counts as provided and fails the sha256 test).

- [ ] **Step 1: Write the failing test**

The expected lines were captured from `node dist/cli.js verify docs/samples/self-incident-commit-proof`.

`internal/bundle/gitproof/verify_test.go`:

```go
package gitproof

import (
	"path/filepath"
	"testing"
)

func TestVerifiesCommittedSample(t *testing.T) {
	r := Verify(filepath.Join("..", "..", "..", "docs", "samples", "self-incident-commit-proof"), "", false)
	want := "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9"
	if !r.Valid || r.CheckedFiles != 18 || r.RootDigest == nil || *r.RootDigest != want || r.ExternalRootStatus != "NOT_PROVIDED" {
		t.Fatalf("%+v", r)
	}
	if r := Verify(filepath.Join("..", "..", "..", "docs", "samples", "self-incident-commit-proof"), "", true); r.Valid || r.Errors[0] != "externally supplied Git proof root is not a sha256 digest" {
		t.Fatalf("%+v", r)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bundle/gitproof/ -run TestVerifiesCommittedSample`
Expected: FAIL (undefined: Verify).

- [ ] **Step 3: Implement the git helpers**

`internal/bundle/gitproof/git.go` starts with the process layer below; transcribe the remaining `git.go` functions from the table on top of it.

```go
package gitproof

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

const maxGitOutputBytes = 4 * 1024 * 1024

type gitResult struct {
	status         *int
	stdout, stderr []byte
	err            error
}

// limitedBuffer fails the write once maxGitOutputBytes is exceeded (spawnSync maxBuffer).
type limitedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxGitOutputBytes {
		b.exceeded = true
		return 0, errors.New("maxBuffer exceeded")
	}
	return b.Buffer.Write(p)
}

func toGitPath(path string) string { return strings.ReplaceAll(nodefs.Resolve(path), `\`, "/") }

// runGit ports runGit (git-proof-bundle.ts:380).
func runGit(repository string, args ...string) gitResult {
	if repository != "" {
		args = append([]string{"-C", toGitPath(repository)}, args...)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	result := gitResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	switch {
	case stdout.exceeded || stderr.exceeded:
		result.err = errors.New("spawnSync git ENOBUFS")
	case errors.Is(runErr, exec.ErrNotFound):
		result.err = errors.New("spawnSync git ENOENT")
	default:
		code := cmd.ProcessState.ExitCode()
		result.status = &code
	}
	return result
}

func statusText(status *int) string {
	if status == nil {
		return "null"
	}
	return strconv.Itoa(*status)
}

// gitBytes ports gitBytes (git-proof-bundle.ts:398).
func gitBytes(repository, label string, args ...string) []byte {
	r := runGit(repository, args...)
	if r.err != nil || r.status == nil || *r.status != 0 {
		var parts []string
		if s := jsstr.Trim(nodefs.DecodeUTF8(r.stderr)); s != "" {
			parts = append(parts, s)
		}
		if r.err != nil {
			parts = append(parts, r.err.Error())
		}
		detail := strings.Join(parts, "; ")
		if detail == "" {
			detail = "exit " + statusText(r.status)
		}
		bundle.Throw(errors.New(label + " failed: " + detail))
	}
	return r.stdout
}

func gitText(repository, label string, args ...string) string {
	return jsstr.Trim(nodefs.DecodeUTF8(gitBytes(repository, label, args...)))
}
```

`runGit(undefined, …)` in TS maps to `repository == ""` here; the verifier only passes `undefined` or a temp directory, never an empty path.

- [ ] **Step 4: Transcribe semantics and the verifier; wire dispatch**

Write `semantics.go` and `verify.go` per the table and gotchas, using the porting rules from Task 8. In `internal/cli/verify.go` replace the git case with:

```go
	case "faultline.git-proof-bundle.v1":
		e.printBundle("Git proof", gitproof.Verify(root, expectedRoot, rootProvided))
		return nil
```

(import `github.com/Mizore66/faultline/internal/bundle/gitproof`). Set `portedTypes` to `{"demo": true, "prevention": true, "git": true}`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/... && go test ./difftest/ -run TestGoldenReplay`
Expected: PASS, including every `git-*` golden. Git-heavy cases take a few minutes. Work through failures one at a time, starting with the smallest case id.

- [ ] **Step 6: Commit**

```bash
git add internal difftest/replay_test.go
git commit -m "feat(go): port faultline.git-proof-bundle.v1 verifier

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Known differences, benchmark, live CI job, done check

**Files:**
- Create: `difftest/KNOWN_DIFFERENCES.md`, `difftest/BENCHMARK.md`, `difftest/benchmark_test.go`
- Modify: `.github/workflows/go.yml` (add `difftest-live`)

**Interfaces:**
- Consumes: everything.
- Produces: the spec §6 done criteria, all satisfied.

- [ ] **Step 1: Write KNOWN_DIFFERENCES.md**

`difftest/KNOWN_DIFFERENCES.md`:

```markdown
# Known differences between Go `fl` and frozen TS (`faa98c0`)

Every intentional divergence is listed here. Anything not listed is a bug.

## Locale-dependent key order (TS bug, not reproduced)

TS `canonicalJson` sorts object keys with `localeCompare`, which uses the machine's ICU default locale. Under `sv-SE`, Node sorts `å ä ö` after `z`; under `tr-TR`, it orders `I` before `i`. Go always uses en-US ordering (what CI and `C.UTF-8` machines produce). Bundles written under an affected locale with affected keys already fail TS verification on en-US machines.

## Directory listing order on Windows

Node's `readdirSync` returns byte-sorted names on Linux and macOS but filesystem order on Windows. Go returns byte-sorted names everywhere, so Go on Windows matches TS on Linux. This only affects the order of error lines that enumerate files.

## `Next: pnpm fl help` hint

Kept verbatim in slice 1. The packaging slice changes it deliberately and updates the goldens.

## zod issue kinds compared by code and path only

None. (Add entries here, with the issue kind and a reason, if any are ever excluded under spec §3.3.)
```

If any Latin-1 or non-ASCII collation mismatches were recorded in Task 4, add a section for them with the failing characters.

- [ ] **Step 2: Write the benchmark**

`difftest/benchmark_test.go`:

```go
package difftest

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/cli"
)

func BenchmarkVerifyGitFullyBound(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "git-fully-bound")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}

func BenchmarkVerifyDemoRerun(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "demo-rerun")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}
```

Run: `go test ./difftest/ -run '^$' -bench Verify -benchtime 20x`
Then time TS on the same bases:

```bash
hyperfine --warmup 2 'node dist/cli.js verify difftest/testdata/bases/git-fully-bound' 'node dist/cli.js verify difftest/testdata/bases/demo-rerun'
```

If `hyperfine` is not installed, run each TS command 20 times under `time` in a shell loop and divide by 20. Record both results in `difftest/BENCHMARK.md` with the machine (CPU, OS), Go and Node versions, and the date. This is evidence for the performance goal, not a gate.

- [ ] **Step 3: Add the live CI job**

Append to `jobs:` in `.github/workflows/go.yml`:

```yaml
  difftest-live:
    name: difftest live (frozen TS oracle)
    runs-on: ubuntu-latest
    env:
      LC_ALL: C.UTF-8
    steps:
      - uses: actions/checkout@93cb6efe18208431cddfb8368fd83d5badbf9bfd # v5
      - uses: pnpm/action-setup@b0f76dfb45f55f8421693e4803ac7bb65143bd34 # v6
      - uses: actions/setup-node@a0853c24544627f65ddf259abe73b1d18a591444 # v5
        with:
          node-version: 22
          cache: pnpm
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7
        with:
          go-version: "1.27"
      - run: pnpm install --frozen-lockfile
      - run: pnpm build
      - name: Regenerate goldens and require no drift
        run: |
          pnpm exec tsx difftest/gen/gen-goldens.ts
          git diff --exit-code difftest/testdata
      - name: Live property tests
        run: FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLive -v -timeout 30m
```

- [ ] **Step 4: Run the done check (spec §6)**

Run each and confirm:

```bash
go vet ./... && go test ./...
```

Expected: PASS (golden replay for all three types).

```bash
pnpm build && pnpm exec tsx difftest/gen/gen-goldens.ts && git diff --exit-code difftest/testdata
```

Expected: no diff.

```bash
FAULTLINE_NODE_ORACLE=1 go test ./difftest/ -run TestLive -v -timeout 30m
```

Expected: PASS, with 10,000 cases per property.

Push the branch and confirm both CI jobs (`go` on three OSes, `difftest-live`) pass before calling slice 1 done.

- [ ] **Step 5: Commit**

```bash
git add difftest .github/workflows/go.yml
git commit -m "docs(difftest): known differences, benchmark, and live oracle CI job

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
