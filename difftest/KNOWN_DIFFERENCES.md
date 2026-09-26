# Known differences between Go `fl` and frozen TS (`faa98c0`)

Every intentional divergence is listed here. Anything not listed is a bug.

## Locale-dependent key order (TS bug, not reproduced)

TS `canonicalJson` sorts object keys with `localeCompare`, which uses the machine's ICU default locale. Under `sv-SE`, Node sorts `å ä ö` after `z`; under `tr-TR`, it orders `I` before `i`. Go always uses the en-US (CLDR root) order that CI and `C.UTF-8` machines produce. Bundles written under an affected locale with affected keys already fail TS verification on en-US machines.

Go's collation (`internal/collation`) is generated from the ICU 78 root collation data that Node 22 bundles (Unicode 17, CLDR 48) and matches `localeCompare` on every code point and on the tie set (checked against Node by `TestLiveCollationAllCodePoints`). A Node built against a different ICU version (for example `--with-intl=system-icu` on an older system ICU) can order newly assigned characters differently; Go follows the ICU that official Node 22 builds ship.

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

## Closed stdout pipe

Both runtimes ignore SIGPIPE, so writing to a stdout pipe whose reader has gone is an `EPIPE` write error and the process exits 1. Node reports it as an unhandled `'error'` event with a stack trace on stderr; Go prints the single line `Error: write EPIPE`. Stdout and the exit code match; the stderr text does not.

## `ENOTDIR` on Windows

When a path component that should be a directory is a file (`file.json/x`), Linux reports `ENOTDIR` while Windows (libuv maps `ERROR_PATH_NOT_FOUND` to `ENOENT`) reports `ENOENT`. Go on Windows reports `ENOTDIR` in that case, so it matches TS on Linux and the Linux-generated goldens. A missing parent directory is `ENOENT` everywhere. All other Win32 errors follow libuv's `uv_translate_sys_error`.
