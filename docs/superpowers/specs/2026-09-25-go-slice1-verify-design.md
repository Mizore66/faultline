# Go migration slice 1 — foundation, differential harness, `fl verify`

- **Date:** 2026-09-25
- **Status:** Approved design (brainstorming session, 2026-09-25)
- **Parent:** [Go migration overview](2026-09-25-go-migration-overview.md)
- **TS oracle:** frozen TS at `faa98c0` (`v0.1.9-buildweek`), run as `node dist/cli.js`

## 1. Scope and layout

### Delivers

- A Go module with the foundation packages: canonical JSON, digests, JS-compatible JSON parsing, a zod-subset schema library, and Node-compatible file reading.
- `fl verify` in Go for three bundle types:
  - `faultline.proof-bundle.v2` (demo), TS `verifyProofBundle` (`src/proof-bundle.ts:439`)
  - `faultline.git-proof-bundle.v1`, TS `verifyGitInvestigationProofBundle` (`src/git-proof-bundle.ts:1142`)
  - `faultline.prevention-proof.v1`, TS `verifyPreventionProof` (`src/prevention-proof.ts:296`)
- The differential harness and its committed golden corpus, running in CI.

### TS code ported in this slice

Only schemas and pure verification logic. No execution, writing, or process-spawning code.

| TS module | What is ported |
| --- | --- |
| `canonical.ts` | `canonicalJson`, `sha256`, `digestJson` |
| `domain.ts`, `engine.ts` | Schemas used by the demo verifier; `analysisDigest` |
| `proof-bundle.ts` | `verifyProofBundle` and its schemas |
| `git-proof-bundle.ts` | `verifyGitInvestigationProofBundle`, `bindLifecycleLedger` (`:581`), manifest/source schemas |
| `git-investigation.ts` | `GitInvestigationResultSchema`, `GitInvestigationRunFactSchema`, `StableGitTransitionSchema`, `GitCommitStateSchema`, constants |
| `witness-lock.ts` | `FrozenWitnessSchema`, `verifyFrozenWitnessRecord` (`:467`) |
| `ledger.ts` | `CodexLifecycleLedgerSchema`, `verifyCodexLifecycleLedger` (`:505`) |
| `sandbox.ts` | `validateSandboxPlanAudit` (`:615`) and the audit schema it reads |
| `prevention-proof.ts` | `verifyPreventionProof` and its schemas |
| `cli-app.ts`, `cli.ts` | The `verify` case (`cli-app.ts:2903`), `option`/`hasFlag` (`:140`–`:147`), `formatCliFailure` (`cli.ts`) |

Estimated size: 3–4k lines of TS logic.

### Deferred

- `safe-directory.ts`: none of the three verifiers call it (only bundle writers do), so it moves to the Git-path slice.
- `faultline.turn-proof-bundle.v1` verification (turn slice). Its verifier depends on `git-materialization` and `sandbox` execution code.
- Every other command, all bundle writing, releases, the npm wrapper, and the Action.

### Layout

```
go.mod                       module github.com/Mizore66/faultline, go 1.27
cmd/fl/main.go               entry point; calls internal/cli
internal/jsjson/             JSON.parse / JSON.stringify clone (Section 2)
internal/canonical/          canonicalJson, sha256, digestJson
internal/nodefs/             file reading with Node semantics; Node-format fs errors
internal/schema/             zod-subset schema library (Section 3)
internal/bundle/demo/        faultline.proof-bundle.v2 verifier
internal/bundle/gitproof/    faultline.git-proof-bundle.v1 verifier + ported dependencies
internal/bundle/prevention/  faultline.prevention-proof.v1 verifier
internal/cli/                argument helpers, verify command, error formatting
difftest/                    harness, generators, corpus, goldens (Section 4)
```

Dependencies: stdlib and `golang.org/x/text`. No CLI framework; `internal/cli` reproduces the TS helpers:

- `hasFlag(args, flag)` → `args` contains `flag`
- `option(args, flag)` → the element after the first occurrence of `flag`, or absent

## 2. Canonical JSON and text semantics

Behavior below was confirmed against Node v22.23.2. Go's `encoding/json` differs on each point, so it is not used on any digest path.

### 2.1 Parsing (`internal/jsjson`)

A strict clone of `JSON.parse`:

- Grammar exactly as ECMA-404: no BOM, no comments, no trailing commas; whitespace is space, tab, LF, CR only.
- Numbers parse to float64 with round-to-nearest. A value that overflows (e.g. `1e400`) becomes ±Infinity, which canonicalization later rejects with `Value is not finite JSON: number`, matching TS.
- Strings are stored UTF-16-faithfully (WTF-8 internally) so lone surrogates such as `\ud800` survive a round trip.
- Duplicate keys: last value wins.
- Objects keep JavaScript property order: integer-like keys (canonical array indices, `0`…`2^32−2`) first in ascending numeric order, then other keys in first-insertion order.
- **Syntax errors reproduce V8 12.4 (Node 22) messages exactly**, because verifiers print `error.message` (e.g. `manifest validation failed: Expected ',' or '}' after property value in JSON at position 13 (line 3 column 3)`). Positions, line, and column count UTF-16 code units; `\r\n` counts as one line break. Message selection follows `src/json/json-parser.cc` at V8 tag `12.4.254` (`ReportUnexpectedToken`, `LookUpErrorMessageForJsonToken`, `GetErrorMessageWithEllipses`: 10 characters of context, context only when the source is at least 21 code units long, whole-source special cases `[object Object]`, `undefined`, `Infinity`, `NaN`). Templates come from `src/common/message-template.h` at the same tag.

### 2.1a Output encoding

TS writes strings with `process.stdout.write`, which encodes UTF-8 and replaces lone surrogates with `U+FFFD`. Go converts its internal WTF-8 strings the same way before writing. Wherever TS hashes a JS string (`sha256(string)`, `Buffer.from(string, "utf8")`), Go hashes that same UTF-8 conversion, not the original file bytes.

### 2.2 Canonicalization (`internal/canonical`)

Port of `normalize` + `JSON.stringify`:

- **Key order** is `a.localeCompare(b)` under en-US, not byte order. Examples from Node: `root_digest < root-digest < rootDigest`; `a < A < b`; `_x < -x < 1 < 10 < 2 < a`. Implemented in `internal/collation` from ICU's own root collation source (`FractionalUCA.txt`, ICU 78, Unicode 17, including the radical-stroke Han order) with ICU's comparison rules as V8 configures them (tertiary strength, non-ignorable, normalization on, case bits ignored), sorted with a stable sort over JS property order so ties keep TS's order. _(Amended during review: `golang.org/x/text/collate` ships Unicode 6.2 / CLDR 23 tables and diverged from Node on CJK, emoji, newer code points and lone surrogates.)_ Parity is enforced by live tests: the full order and ties of all 1,114,112 code points, plus random mixed-script keys (Section 4.4).
- **Numbers** follow ECMAScript `Number::toString`: `123456789012345680000`, `1e+21`, `1e-7`, `0.30000000000000004`, `5e-324`; `-0` prints `0`. Implemented from `strconv` shortest digits laid out per the spec algorithm.
- **Strings** follow well-formed `JSON.stringify` (ES2019): escape `"` and `\`; `\b \f \n \r \t`; other code units below `0x20` as `\u00xx` (lowercase hex); lone surrogates as `\udxxx` (lowercase hex). `<`, `>`, `&`, `U+2028`, and `U+2029` are emitted raw.
- `sha256` hashes the UTF-8 encoding of the canonical string; `digestJson` returns `sha256:<hex>`.

### 2.3 Reading files (`internal/nodefs`)

- Raw bytes are hashed as-is (TS `sha256(readFileSync(path))`).
- Text reads reproduce `readFileSync(path, "utf8")`: WHATWG UTF-8 decoding, invalid sequences replaced with `U+FFFD` using the maximal-subpart rule, BOM kept (so `JSON.parse` then fails, as in TS).
- `trim()` follows JavaScript: strips ECMAScript WhiteSpace and LineTerminator code points, including `U+FEFF`.
- String comparisons that TS performs on decoded text (e.g. `stdout.log` vs catalog in `proof-bundle.ts:282`) compare UTF-16 code-unit sequences.
- Directory listings are sorted by byte order, matching Node's `readdirSync` on Linux and macOS (libuv sorts `scandir` results with `strcmp`). Node on Windows returns filesystem order instead; Go keeps byte order on every OS and this is recorded as a known difference.
- Filesystem errors that reach output use Node's format `<CODE>: <libuv description>, <syscall> '<path>'` with Node's syscall names (`open`, `lstat`, `scandir`, `realpath`), and `EISDIR: illegal operation on a directory, read` (no path) when a directory is read as a file.

### 2.4 Known TS bug, not reproduced

TS `localeCompare` uses the machine's ICU default locale. Under `sv-SE`, Node sorts `å ä ö` after `z`; under `tr-TR`, it orders `I` before `i`. Go always uses en-US ordering, which is what CI and `C.UTF-8` machines produce. Bundles written under such locales with affected keys already fail TS verification on en-US machines. Recorded in `difftest/KNOWN_DIFFERENCES.md`.

## 3. Schema validation (`internal/schema`)

### 3.1 Why a schema library

TS computes digests over zod's parsed output, not raw file contents (e.g. `digestJson(parsedResult.data)` in `git-proof-bundle.ts:1205`). Zod changes the data before hashing:

- non-strict objects drop unknown keys
- absent optional fields are omitted
- `git-investigation.ts` applies defaults and transforms

Go must produce the same normalized value, so plain `encoding/json` struct decoding is not sufficient.

### 3.2 Design

- Combinators covering exactly what the ported schemas use: `Object` (strict or strip), `String` (with regex, min length), `Number` (with int, nonnegative, bounds), `Literal`, `Enum`, `Array`, `Record`, `Optional`, `Nullable`, `DiscriminatedUnion`, `Refine`, `Default`, `Transform`.
- `Parse(value) → (normalized jsjson.Value, []Issue)`. Verifier logic then decodes the normalized value into typed Go structs.
- Each Go schema is a 1:1 transcription with a comment naming the TS file and line it came from.
- Object output key order follows zod (schema shape order); canonicalization sorts it anyway.
- String length checks (`min`, `max`) and regex quantifiers count UTF-16 code units, as JS does without the `u` flag. JS regexes are translated to Go by hand; constructs RE2 lacks (the lookahead in `SAFE_GIT_REVISION`, `/^(?!-)[^\0\r\n]{1,512}$/`) and length-counted classes become Go functions over UTF-16. zod's own regexes (`datetime` with `offset: true`, `uuid`) are copied from `node_modules/zod/v3/types.js` (zod 3.25.76).
- `CanonicalTimestampSchema` (`ledger.ts:40`) reproduces `new Date(value).toISOString() === value`: V8 accepts out-of-range days and hours and rolls them over (`2021-02-30T…` → `2021-03-02T…`), so Go computes the date arithmetic the same way and compares the formatted result.

### 3.3 Issues and error text

- `Issue` carries zod v3 fields: `code`, `path`, `message`, plus the code-specific fields zod v3 attaches for that code (for example `expected`, `received`, `keys`, `validation`, `minimum`, `inclusive`, `exact`, `options`).
- An error's message is rendered as zod v3 renders `ZodError.message`: `JSON.stringify(issues, null, 2)` with JS key insertion order.
- Target: exact match for every issue kind these schemas can produce (`invalid_type`, `invalid_literal`, `unrecognized_keys`, `invalid_string`, `too_small`, `too_big`, `invalid_enum_value`, `invalid_union_discriminator`, `custom`).
- If an issue kind proves impractical to match exactly, the harness compares only its `code` and `path` for that kind, and the exclusion is recorded in `difftest/KNOWN_DIFFERENCES.md`.

## 4. Differential harness (`difftest/`)

### 4.1 Corpus

- `difftest/testdata/bases/<base-id>/` holds valid bundles for all three types:
  - `docs/samples/self-incident-commit-proof` (git) and `.faultline/bundles/judge-demo` (demo), copied in.
  - Synthetic bundles from `difftest/gen/gen-bases.ts` (run with `tsx`), which imports the frozen `src/` modules with synthetic inputs, as the existing vitest suites do. Variations: one run and many runs; lifecycle ledger present and absent; non-ASCII, lone-surrogate, and invalid-UTF-8 stdout; keys that exercise collation (punctuation, case, digits).
- `difftest/testdata/mutations.json` lists tampering steps as data. Operations:
  - `flip-byte` (file, offset)
  - `delete-file`, `add-file` (path, content)
  - `replace-text` (file, find, replace), for text files such as the demo bundle's hash list
  - `json-add-key` (file, key, value), `json-retype` (file: first key becomes `12345`, or `"retyped"` if it was a number), `json-drop-first` (file)
  - `strip-trailing-newline` (file), `prepend-bytes` (file, hex): BOMs and invalid UTF-8
  - `rename` (from, to), within the bundle
  - `symlink` (path, target), including targets outside the bundle
  - `make-dir` (path): replaces a file with an empty directory
  - `rehash`: after any of the above, recompute `hashes.txt` and `ROOT.sha256` (git and demo) or `rootDigest` (prevention), simulating an editor who updates the mutable checksums

  Traversal cases (`../x`, `a/../../x`, absolute paths, backslashes, NUL) are expressed as `replace-text` on declared file paths, since that is where verifiers read them. The initial list covers the two hostile cases in `tests/hostile-git-verifier-corpus.test.ts`. The generator then applies every applicable operation to every file of every base.
- Each case runs three invocations: no option (`plain`), `--expect-root <base root digest>` (`root-ok`), and `--expect-root sha256:` followed by 64 zeros (`root-bad`).
- `difftest/testdata/golden/<base-id>.jsonl` holds one line per invocation: `{case, inv, treeDigest, stdout, stderr, exit}`. Case ids are `<base-id>` or `<base-id>__<mutation-id>__<target>`.

### 4.2 Generators (Node, frozen TS)

- `difftest/gen/gen-bases.ts`: writes synthetic bases. Generators are TypeScript run with the existing `tsx` dev dependency, so they import the same frozen `src/` code that `dist/` is built from.
- `difftest/gen/gen-goldens.ts`: for each case, copies the base to a temp directory, applies the mutation with the Node applier, records `treeDigest` (sha256 over the byte-sorted list of `/`-separated relative paths, each with its entry type and its contents, or its link target for symlinks), runs `node dist/cli.js verify <dir>` for each of the three invocations, normalizes (4.5), and writes the golden lines.

### 4.3 Go test

`go test ./difftest` does the same steps with a Go mutation applier and the Go `verify` command, then asserts:

1. `treeDigest` equals the golden `treeDigest` (proves both appliers built the same tree).
2. stdout, stderr, and exit code equal the golden values byte for byte after normalization.

This suite needs no Node and remains the regression oracle after TS is deleted.

### 4.4 Live property tests (need Node)

- `difftest/gen/node-oracle.ts` is a long-running process that reads JSON-line requests and exposes `canonicalJson`, `localeCompare` sort, `JSON.parse` errors, UTF-8 decoding, number formatting, the exported zod schemas, a small schema DSL that builds equivalent zod and Go schemas for combinator tests (non-exported schemas are covered by the golden corpus), and the exported pure verification functions (`verifyFrozenWitnessRecord`, `verifyCodexLifecycleLedger`, `validateSandboxPlanAudit`).
- Go property tests (run when `FAULTLINE_NODE_ORACLE=1`) send random inputs and compare:
  - key sets → sort order
  - arbitrary JSON values → `canonicalJson` output and `digestJson`
  - schema inputs (valid values and random corruptions of them) → success, normalized output, and issue `code`/`path` lists (full messages where exact matching applies)
- 10,000 cases per property per CI run, seeded from the edge cases in Section 2.

### 4.5 Normalizations (the complete list)

1. The temp bundle root path is replaced with `<BUNDLE>`. On Windows, backslashes in the replaced root and in the path that follows it (up to the next quote, whitespace, or end of line) are converted to `/`.
2. The git verifier's temporary repository path (`<tmpdir>/faultline-git-proof-verify-XXXXXX`) is replaced with `<GITTMP>`.
3. Text that `git` itself wrote after one of the verifier's git labels (`Git bundle head listing failed: `, `Git bundle verification failed: `, `Git bundle extraction failed: `, `Git commit resolution failed: `, `Git tree resolution failed: `, `Git bundle range enumeration failed: `, `Git binary range patch creation failed: `, `Git bundle object-format resolution failed: `, `Temporary Git verifier initialization failed: `) is replaced with `<GIT-DETAIL>` up to the end of that error entry. Git's wording varies by git version; FaultLine's own text around it is still compared exactly.
4. Messages of zod issue kinds excluded under Section 3.3.

Any other difference fails the test.

Golden generation and Go tests both run git with an isolated configuration (`HOME` set to an empty temp directory, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL` pointing at an empty file), so user settings such as `diff.noprefix` cannot change the reproduced range patch.

### 4.6 CI

- **`go`** (ubuntu-latest, macos-latest, windows-latest): `go vet ./...` and `go test ./...` against the committed corpus. Symlink cases are skipped on Windows when symlink creation is not permitted.
- **`difftest-live`** (ubuntu-latest, Node 22, `LC_ALL=C.UTF-8`): `pnpm install --frozen-lockfile`, `pnpm build`, regenerate goldens, `git diff --exit-code difftest/testdata`, then run the property tests with `FAULTLINE_NODE_ORACLE=1`.

## 5. `fl verify` behavior

Reproduced from `src/cli-app.ts:2903` and `src/cli.ts`, quirks included.

- **Arguments.** `fl verify <dir> [--expect-root <digest>]`. `<dir>` is always `args[0]`, so `fl verify --expect-root X dir` uses `--expect-root` as the directory. Missing `<dir>` throws `Usage: fl verify <proof-bundle-directory>`.
- **Dispatch.** Read `<dir>/manifest.json` as text and parse it; if parsing fails or `schemaVersion` is not a string, fall through. Then:
  - `faultline.prevention-proof.v1` → prevention verifier and its output block
  - `faultline.turn-proof-bundle.v1` → not ported (see below)
  - `faultline.git-proof-bundle.v1` → git verifier, label `Git proof`
  - anything else → demo verifier, label `Bundle`
- **Output** (stdout), exactly as TS. The first line is `<Label> self-consistency: VALID|INVALID` when the verifier reports `externalRootStatus` `NOT_PROVIDED`, otherwise `Integrity: VALID|INVALID`.
  - Git and demo (labels `Git proof`, `Bundle`): first line, then `Declared files checked: N`, `Bundle root: <digest|unavailable>`, `External root: <status>`.
  - Prevention (label `Prevention proof`): first line, then `Classification: <classification|unavailable>`, `Bundle root: <digest|unavailable>`, `External root: <status>`; then `Prevention verified` if valid and classification is `PREVENTION_VERIFIED`, or `Prevention evidence summary` if valid otherwise.
  - On INVALID, one `- <error>` line per error, for all three.
- **Exit code.** 0 when valid, 1 when invalid.
- **Thrown errors.** Written to stderr in the `formatCliFailure` format (`FaultLine error: <message>\nNext: pnpm fl help`), exit 1. Node filesystem errors that can reach output (ENOENT, ENOTDIR, EISDIR, EACCES, ELOOP) are rendered in Node's format, e.g. `ENOENT: no such file or directory, open '<path>'`.
- **Turn bundles and other commands.** Throw `not yet ported: <name>`, rendered through the same error path. Nothing is released before parity, so users never see this.

## 6. Done criteria

1. `go test ./...` passes on ubuntu, macOS, and Windows, and every base and mutated case matches its golden stdout, stderr, and exit code.
2. `difftest-live` passes: no golden drift, and 10,000 cases per property with zero mismatches.
3. `difftest/KNOWN_DIFFERENCES.md` lists every intentional difference: the locale bug (2.4), any zod issue-kind exclusions (3.3), and the `Next: pnpm fl help` hint, which is kept verbatim in this slice and changed deliberately, with a golden update, in the packaging slice.
4. A benchmark of Go vs TS `verify` on the largest base is recorded in `difftest/BENCHMARK.md`. It is evidence for the performance goal, not a gate.
