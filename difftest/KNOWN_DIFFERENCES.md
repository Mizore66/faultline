# Known differences between Go `fl` and frozen TS (`faa98c0`)

Every intentional divergence is listed here. Anything not listed is a bug.

## Locale-dependent key order (TS bug, not reproduced)

TS `canonicalJson` sorts object keys with `localeCompare`, which uses the machine's ICU default locale. Under `sv-SE`, Node sorts `å ä ö` after `z`; under `tr-TR`, it orders `I` before `i`. Go always uses the en-US (CLDR root) order that CI and `C.UTF-8` machines produce. Bundles written under an affected locale with affected keys already fail TS verification on en-US machines.

Go's collation (`internal/collation`) is generated from the ICU 78 root collation data (Unicode 17, CLDR 48) that official Node 22 builds ship from 22.22.1 on, and follows ICU's comparison, not a plain UCA key compare: FCD-checked normalization (only failing segments are decomposed; contractions match the undecomposed text), ICU's contraction and prefix data (U+FDD0 rows excluded as genuca does), and `RuleBasedCollator::doCompare`'s identical-prefix skip, which makes `localeCompare` non-transitive on some inputs; `SortLocale` therefore runs a port of V8's TimSort so a non-transitive comparator produces V8's order. Parity is checked against Node by `TestLiveCollationAllCodePoints` (every code point), `TestLiveCollationRules` (every contraction and prefix key with inserted marks, reorderings, NFC/NFD variants and U+FDD0/U+FDD1 sequences) and `TestLiveSortLocale`.

**Minimum Node version.** Earlier Node 22 releases bundled ICU 74 (22.0), 75.1 (22.1) and 76/77 before ICU 78. Bundles written by those releases whose overlay paths or JSON keys contain characters assigned after their Unicode version (for example U+1FAE9, Unicode 16) may use an order Go rejects (`overlay records are not in canonical path order`, or a different canonical-JSON digest). Current Node 22 TS agrees with Go. A Node built against a system ICU (`--with-intl=system-icu`) can differ the same way.

## Directory listing order on Windows

Node's `readdirSync` returns byte-sorted names on Linux and macOS but filesystem order on Windows (NTFS: case-insensitive upcase order). Go returns byte-sorted names everywhere, so Go on Windows matches TS on Linux. This affects the order of error lines that enumerate files, and, because the file walkers stop at the first symlink, special file or oversized file they meet, which file such an error names when a bundle has two offending entries (for example `analysis.json` before `ROOT.sha256` on NTFS, the reverse in byte order).

## `Next: pnpm fl help` hint

Kept verbatim in slice 1. The packaging slice changes it deliberately and updates the goldens.

## zod issue kinds compared by code and path only

None. (Add entries here, with the issue kind and a reason, if any are ever excluded under spec §3.3.)

## Git's default object format

Not an output difference, but an environment dependency of both implementations. The verifier creates its temporary repository with a plain `git init --bare` (`src/git-proof-bundle.ts:931`), which follows `GIT_DEFAULT_HASH` and `init.defaultObjectFormat`. A SHA-1 bundle therefore verifies only where git defaults to SHA-1, and a SHA-256 bundle only where it defaults to SHA-256; elsewhere both report `Git bundle extraction failed`. Go runs the same git commands and matches TS in both settings. The golden generator and replay pin `GIT_DEFAULT_HASH=sha1`, and the `git-sha256` base runs with `GIT_DEFAULT_HASH=sha256` (`difftest/testdata/base-env.json`).

## V8 limits

When a zod schema rejects a deeply nested value, TS builds the error text with `JSON.stringify(issues, replacer, 2)` (the `ZodError.message` getter). V8 does this recursively, and past its native stack limit it throws `RangeError: Maximum call stack size exceeded`, which the verifier's catch prints as a `failed safely` line. Go models V8's stack use (`internal/jsjson/stack.go`): each non-empty array or object pushes a frame, empty containers and primitives push none, and objects on V8's slow path (array-index keys, or 128 or more members, which JSON.parse builds in dictionary mode) cost more. The model is fitted per platform and per verifier call site. On Node 22.22 linux/amd64 it reproduces every measured threshold exactly (for example, the demo verifier still prints the zod message for 2,233 nested arrays, 4,166 single-key objects or 2,232 index-keyed objects, and one more level overflows). darwin/arm64 and linux/arm64 use thresholds measured by the reviewers on Node 22.23 and 22.13 (arrays 2,747 and 1,781, objects 6,867 and 3,560). Windows and other targets use the Linux model for their architecture and have not been measured. V8's real limit also depends on the Node build and `--stack-size`, so TS on an unmeasured platform or Node version can overflow hundreds of levels earlier or later. `JSON.parse` is iterative on both sides and has no depth limit.

Go also reproduces V8's other size limits: strings longer than 0x1fffffe8 UTF-16 units (`RangeError: Invalid string length`, from `JSON.stringify`, message concatenation and the CLI's error join), Sets and Maps with more than 2^24 entries (`RangeError: Set maximum size exceeded` / `Map maximum size exceeded`, at the Go ports of the TS Sets and Maps), and JSON arrays of 2^27 or more elements, which make V8 abort the process. For the abort, Go prints the V8 fatal-error header (`# Fatal JavaScript invalid size error 134217728`) and dies by SIGTRAP (exit 133) like Node, but not Node's native stack trace.

## Stdout write errors

Both runtimes ignore SIGPIPE, so writing to a stdout pipe whose reader has gone is an `EPIPE` write error, and any stdout write error exits 1. Node reports it as an unhandled `'error'` event: a stack trace plus the error line. Go prints only the error line, in Node's form: `Error: <CODE>: <description>, write` when stdout is a file or a non-terminal device (`/dev/full` gives `Error: ENOSPC: no space left on device, write`), and `Error: write <CODE>` for a pipe, socket or terminal. On Windows, `ERROR_BROKEN_PIPE` and `ERROR_NO_DATA` are `EPIPE`, as in libuv. Stdout and the exit code match; the stack trace lines do not.

## Signals and resource limits

Node resets every signal to its default action at startup, ignoring only SIGPIPE and SIGXFSZ. Go emulates this: HUP, INT, QUIT, ABRT, ALRM, TERM, USR2, VTALRM and XCPU (including a CPU-time `ulimit`) end `fl` by the signal's default action with no output, also when the signal was inherited as ignored (`nohup`). Go re-executes itself as `/bin/sh -c 'kill -N $$'` to die by the signal, since the Go runtime's own handlers for QUIT, ABRT and TRAP dump goroutines instead; without `/bin/sh` it exits 128+signal. Git children get the default dispositions in both. Remaining differences: SIGPROF is consumed by the Go runtime, so it doesn't stop `fl`; SIGUSR1 starts Node's inspector (a "Debugger listening" line on stderr) and is ignored by Go; on Windows, console control events follow the Go runtime.

Node keeps about 16 file descriptors open for its event loop and worker threads. Under a very low `RLIMIT_NOFILE` (about 16 or fewer on Linux), TS fails `spawnSync git` with `EMFILE` or does not start, while Go still verifies. The threshold depends on the Node version and platform, so it is not modelled.

## `ENOTDIR` on Windows

When a path component that should be a directory is a file (`file.json/x`), Linux reports `ENOTDIR` while Windows (libuv maps `ERROR_PATH_NOT_FOUND` to `ENOENT`) reports `ENOENT`. Go on Windows reports `ENOTDIR` in that case, deliberately: the Linux-generated goldens contain this error (`git-unbound__hashes-enotdir`, `manifest.json/x` declared in `hashes.txt`), and one expected output on every OS keeps the corpus portable. This is a deliberate divergence from TS on Windows, listed in the spec's done criteria. A missing parent directory is `ENOENT` everywhere. All other Win32 errors follow libuv's `uv_translate_sys_error`.

## WSL symlinks on Windows

Go's `Lstat` follows libuv 1.52 (Node 22.23 and later): a WSL symlink (`IO_REPARSE_TAG_LX_SYMLINK`) is a symbolic link. libuv 1.51 (Node 22.22) has no case for that tag and retries with a following stat, which Win32 cannot resolve, so `lstatSync` throws. Either way the verifiers reject the file; only the error line differs.
