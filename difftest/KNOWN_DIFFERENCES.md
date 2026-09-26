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

## Corpus gap: no SHA-256 Git proof base

Not an output difference. Frozen TS cannot write a SHA-256 Git proof bundle: before publishing, `writeGitInvestigationProofBundle` re-verifies the package, and the verifier creates its temporary repository with a plain `git init --bare` (always SHA-1, `src/git-proof-bundle.ts:931`) and then fetches the SHA-256 bundle into it, which git rejects (`fatal: pack is corrupted (SHA1 mismatch)`, reproduced on git 2.43 and 2.51). The golden corpus therefore has no SHA-256 base. Go runs the same git commands, so on a SHA-256 bundle it reports the same `Git bundle extraction failed` INVALID result as TS.

## `Maximum call stack size exceeded` threshold

When a zod schema rejects a deeply nested value, TS builds the error text with `JSON.stringify(issues, replacer, 2)` (the `ZodError.message` getter). V8 does this recursively, and past its native stack limit it throws `RangeError: Maximum call stack size exceeded`, which the verifier's catch prints as a `failed safely` line. Go reproduces the RangeError with a fixed budget (`internal/jsjson/stringify.go`), calibrated on Node 22.22 Linux x64: a value nested 2,233 arrays (or 4,166 single-key objects) deep still prints the zod message, and one more level overflows. V8's real limit depends on frame sizes, the platform and `--stack-size`, so TS on another machine can overflow a few levels earlier or later. Only inputs within a few levels of the threshold are affected. `JSON.parse` is iterative on both sides and has no depth limit.

## Closed stdout pipe

Both runtimes ignore SIGPIPE, so writing to a stdout pipe whose reader has gone is an `EPIPE` write error and the process exits 1. Node reports it as an unhandled `'error'` event with a stack trace on stderr; Go prints the single line `Error: write EPIPE`. Stdout and the exit code match; the stderr text does not.

## `ENOTDIR` on Windows

When a path component that should be a directory is a file (`file.json/x`), Linux reports `ENOTDIR` while Windows (libuv maps `ERROR_PATH_NOT_FOUND` to `ENOENT`) reports `ENOENT`. Go on Windows reports `ENOTDIR` in that case, so it matches TS on Linux and the Linux-generated goldens. A missing parent directory is `ENOENT` everywhere. All other Win32 errors follow libuv's `uv_translate_sys_error`.
