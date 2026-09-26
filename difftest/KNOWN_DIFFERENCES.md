# Known differences between Go `fl` and frozen TS (`faa98c0`)

Every intentional divergence is listed here. Anything not listed is a bug.

## Locale-dependent key order (TS bug, not reproduced)

TS `canonicalJson` sorts object keys with `localeCompare`, which uses the machine's ICU default locale. Under `sv-SE`, Node sorts `å ä ö` after `z`; under `tr-TR`, it orders `I` before `i`. Go always uses the en-US (CLDR root) order that CI and `C.UTF-8` machines produce. Bundles written under an affected locale with affected keys already fail TS verification on en-US machines.

Go's collation (`internal/collation`) is generated from the ICU 78 root collation data (Unicode 17, CLDR 48) that official Node 22 builds ship from 22.22.1 on, and follows ICU's comparison, not a plain UCA key compare: FCD-checked normalization (only failing segments are decomposed; contractions match the undecomposed text), ICU's contraction and prefix data (U+FDD0 rows excluded as genuca does), and `RuleBasedCollator::doCompare`'s identical-prefix skip, which makes `localeCompare` non-transitive on some inputs; `SortLocale` therefore runs a port of V8's TimSort so a non-transitive comparator produces V8's order. Parity is checked against Node by `TestLiveCollationAllCodePoints` (every code point), `TestLiveCollationRules` (every contraction and prefix key with inserted marks, reorderings, NFC/NFD variants and U+FDD0/U+FDD1 sequences) and `TestLiveSortLocale`.

**Minimum Node version.** Earlier Node 22 releases bundled ICU 74 (22.0), 75.1 (22.1) and 76/77 before ICU 78. Bundles written by those releases whose overlay paths or JSON keys contain characters assigned after their Unicode version (for example U+1FAE9, Unicode 16) may use an order Go rejects (`overlay records are not in canonical path order`, or a different canonical-JSON digest). Current Node 22 TS agrees with Go. A Node built against a system ICU (`--with-intl=system-icu`) can differ the same way.

## Directory listing order on Windows

Node's `readdirSync` returns byte-sorted names on Linux and macOS but filesystem order on Windows. Go returns byte-sorted names everywhere, so Go on Windows matches TS on Linux. This only affects the order of error lines that enumerate files.

## `Next: pnpm fl help` hint

Kept verbatim in slice 1. The packaging slice changes it deliberately and updates the goldens.

## zod issue kinds compared by code and path only

None. (Add entries here, with the issue kind and a reason, if any are ever excluded under spec §3.3.)

## Git's default object format

Not an output difference, but an environment dependency of both implementations. The verifier creates its temporary repository with a plain `git init --bare` (`src/git-proof-bundle.ts:931`), which follows `GIT_DEFAULT_HASH` and `init.defaultObjectFormat`. A SHA-1 bundle therefore verifies only where git defaults to SHA-1, and a SHA-256 bundle only where it defaults to SHA-256; elsewhere both report `Git bundle extraction failed`. Go runs the same git commands and matches TS in both settings. The golden generator and replay pin `GIT_DEFAULT_HASH=sha1`, and the `git-sha256` base runs with `GIT_DEFAULT_HASH=sha256` (`difftest/testdata/base-env.json`).

## `Maximum call stack size exceeded` threshold

When a zod schema rejects a deeply nested value, TS builds the error text with `JSON.stringify(issues, replacer, 2)` (the `ZodError.message` getter). V8 does this recursively, and past its native stack limit it throws `RangeError: Maximum call stack size exceeded`, which the verifier's catch prints as a `failed safely` line. Go reproduces the RangeError with a fixed budget (`internal/jsjson/stringify.go`), calibrated on Node 22.22 Linux x64: a value nested 2,233 arrays (or 4,166 single-key objects) deep still prints the zod message, and one more level overflows. V8's real limit depends on frame sizes, the platform and `--stack-size`, so TS on another machine can overflow a few levels earlier or later. Only inputs within a few levels of the threshold are affected. `JSON.parse` is iterative on both sides and has no depth limit.

## Stdout write errors

Both runtimes ignore SIGPIPE, so writing to a stdout pipe whose reader has gone is an `EPIPE` write error, and any stdout write error exits 1. Node reports it as an unhandled `'error'` event: a stack trace plus the error line. Go prints only the error line, in Node's form: `Error: <CODE>: <description>, write` when stdout is a file or a non-terminal device (`/dev/full` gives `Error: ENOSPC: no space left on device, write`), and `Error: write <CODE>` for a pipe, socket or terminal. On Windows, `ERROR_BROKEN_PIPE` and `ERROR_NO_DATA` are `EPIPE`, as in libuv. Stdout and the exit code match; the stack trace lines do not.

## Signals and resource limits

Node resets every signal to its default action at startup, ignoring only SIGPIPE and SIGXFSZ. Go emulates this: HUP, INT, QUIT, ABRT, ALRM, TERM, USR2, VTALRM and XCPU (including a CPU-time `ulimit`) end `fl` with no output and exit status 128+signal, also when the signal was inherited as ignored (`nohup`). For HUP, INT and TERM `fl` dies by the signal itself, as Node does; for the others the parent sees an exit status of 128+signal rather than a signal death (a shell's `$?` is the same). Git children get the default dispositions in both. Remaining differences: SIGPROF is consumed by the Go runtime, so it doesn't stop `fl`; SIGUSR1 starts Node's inspector (a "Debugger listening" line on stderr) and is ignored by Go; on Windows, console control events follow the Go runtime.

Node keeps about 16 file descriptors open for its event loop and worker threads. Under a very low `RLIMIT_NOFILE` (about 16 or fewer on Linux), TS fails `spawnSync git` with `EMFILE` or does not start, while Go still verifies. The threshold depends on the Node version and platform, so it is not modelled.

## `ENOTDIR` on Windows

When a path component that should be a directory is a file (`file.json/x`), Linux reports `ENOTDIR` while Windows (libuv maps `ERROR_PATH_NOT_FOUND` to `ENOENT`) reports `ENOENT`. Go on Windows reports `ENOTDIR` in that case, so it matches TS on Linux and the Linux-generated goldens. A missing parent directory is `ENOENT` everywhere. All other Win32 errors follow libuv's `uv_translate_sys_error`.

## WSL symlinks on Windows

Go's `Lstat` follows libuv 1.52 (Node 22.23 and later): a WSL symlink (`IO_REPARSE_TAG_LX_SYMLINK`) is a symbolic link. libuv 1.51 (Node 22.22) has no case for that tag and retries with a following stat, which Win32 cannot resolve, so `lstatSync` throws. Either way the verifiers reject the file; only the error line differs.
