# Security model

Limits that matter when sharing proofs, reviewing signatures, or enabling turn-tree capture. Product claim boundaries: [concepts.md](concepts.md).

## Provenance honesty (never fake Codex ledgers)

FaultLine refuses unearned claims. Do **not** fabricate Codex sidecar ledgers (including Cursor or other editor sessions rebadged as Codex `SessionStart`/`Stop` hook events). A forged ledger inside a provenance tool poisons every honest artifact and violates “must function as depicted.”

Allowed Codex-native depth paths only:

1. Real Codex sessions (credits / teammate / free tier) with genuine hook capture
2. Partner redacted MUMBCS (or other) ledgers from real Codex
3. Honestly labeled `fl record` observed-transport checkpoints with transport `OBSERVED_EXTERNAL_TRANSPORT` (sample: [samples/observed-external-transport/](samples/observed-external-transport/)) that explicitly say they are **not** Codex hook events

Package-smoke continues to block claiming an external case study until `docs/impact-validation-external-01.md` status is `completed`. See META #108.

## Turn-tree snapshot storage warning

Turn-tree capture supports experimental turn localization (`EXPERIMENTAL_TURN`). It is not commit-path portable proof (`COMMIT_PROOF`).

Codex sidecar SessionStart/Stop turn-tree snapshots use a **temporary Git index**, but they are **not storage-neutral**: staging still writes blob objects. FaultLine quarantines those writes into `.git/faultline/objects` via `GIT_OBJECT_DIRECTORY` (registered as a Git alternate) so the primary `.git/objects` database is not polluted. Purge with `fl codex snapshot gc`. Eligible **untracked** files may be included unless you opt out.

Mitigations:

- Add a repository-root `.faultlineignore` (gitignore syntax) for project-specific exclusions. Prefer a minimal reviewed list; `fl init` writes a commented starter that does **not** exclude lockfiles or tests by default.
- Environment descriptors (`package.json`, lockfiles, `go.mod`/`go.sum`, `Cargo.toml`/`Cargo.lock`, Dockerfiles, tool pins, …) are **protected**: `.faultlineignore` cannot suppress them from turn snapshots, because environment fingerprinting depends on them.
- Built-in defaults already skip common build/cache trees (`dist/`, `build/`, `.next/`, `coverage/`, …), dependency dirs, and secret-shaped paths.
- Session snapshot cache reuse requires a content fingerprint over every eligible dirty/untracked path plus the deleted-path set (porcelain status alone is never enough). The cache is a performance hint only; cached Git tree objects must still exist.
- Hard caps reject oversized snapshots (per-file, total bytes, and file count) before any blob write.
- Secret scanning (regex + entropy) rejects high-confidence credential material before acceptance.
- Set `FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY=1` to stage tracked files only.

The sidecar prints this warning when it primes the SessionStart baseline snapshot.

Measured overhead (when regenerated): [turn-snapshot-overhead.md](turn-snapshot-overhead.md). Sidecar operations: [codex-sidecar.md](codex-sidecar.md).

## Integrity receipts (`fl attest`) — not host attestation

`fl attest` is an **integrity-only** receipt. An external digest detects an editor who rewrites both local content and local checksums. It is not a cryptographic signature, an identity check, proof of authorship, or a provenance guarantee.

Commands: [proof-bundles.md](proof-bundles.md#verify-and-retain-integrity-evidence).

## Signed GitHub CI provenance — host / Docker limits

Signed provenance says that the configured GitHub Actions identity signed the receipt bytes. It does **not** cryptographically prove that a host, Docker client, or Docker daemon enforced FaultLine's recorded sandbox policy, nor does it expand the predicate-specific proof into a general build or authorship claim.

`NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. A signed GitHub CI receipt binds bytes and the configured GitHub Actions identity; it does not change this host/Docker-enforcement limitation.

Trust file and verify flow: [proof-bundles.md](proof-bundles.md#signed-github-ci-provenance). Start from [`faultline-github-attestation-trust.example.json`](faultline-github-attestation-trust.example.json), then retain a reviewed copy alongside the downloaded root and artifact bundle.

## Portable package sensitivity

Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.

## Signature caveats (reviewer keyring)

`fl witness approve` records a reviewed approval but is intentionally not an identity assertion. When a reviewer needs to authenticate the approval, sign the already-frozen witness with an Ed25519 private key and verify it against a separately retained reviewer keyring.

The keyring, not the approval record, decides which reviewer keys are trusted. A normal verification may accept a frozen witness without a signature for the local/offline MVP; `--require-signature` fails closed when the authenticated record is missing, untrusted, or altered. Keep private keys outside the repository, rotate keys by changing the retained keyring, and do not treat a displayed `approvedBy` string as authenticated unless this signature check passes.

Minimization verify detects a rewritten record only when an externally retained digest is supplied; it is not a cryptographic signature, identity assertion, or host-attestation claim.

Full witness sign/verify and keyring schema: [witness-protocol.md](witness-protocol.md).
