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
