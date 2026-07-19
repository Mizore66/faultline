# Security policy

FaultLine executes reviewer-frozen commands against historical Git trees and turn snapshots, materializes repository state, writes proof packages, and may invoke Docker and Codex. Treat it as an incident-evidence tool with a real attack surface.

## Supported versions

Security fixes are accepted for the current `main` branch of this repository (pre-1.0 Build Week software). There is no long-term support promise for older tags until a stable release is cut.

## Reporting a vulnerability

**Do not** open a public GitHub issue for security reports that could expose secrets, private proof bundles, or exploitable command-injection paths.

**Report privately through GitHub Security Advisories for this repository:**

https://github.com/Mizore66/faultline/security/advisories/new

That private advisory form is the sole security contact for FaultLine. Do not email maintainers or open a public issue for vulnerability reports.

Include:

- FaultLine version or commit SHA
- Host OS and whether Docker was involved
- Minimal reproduction (redact secrets)
- Impact assessment (what an attacker gains)

## What we consider a vulnerability

In scope examples:

- Escaping the intended sandbox / Docker policy during proof-grade execution
- Path traversal or symlink tricks during materialization or overlay write
- Forged or rewritten proof packages accepted as valid without detection when an external root digest is supplied
- Secret leakage from sidecar ledgers, proof packages, or CLI output beyond documented retention
- Command injection through untrusted witness overlays or Git object handling

Out of scope examples:

- “The witness command can do anything the reviewer froze” (by design — humans freeze predicates)
- Lack of cryptographic host/Docker-daemon attestation (documented limitation)
- Issues that require ignoring `--expect-root` / offline verify warnings
- Social-engineering a reviewer into freezing a malicious predicate

## Expected process

1. Acknowledge receipt when possible
2. Investigate and, if confirmed, prepare a fix on a private branch
3. Disclose after a fix is available, or sooner if active exploitation is likely
4. Credit reporters who want attribution

## Public issues and proof packages

**Never** paste private proof bundles, `.env` files, API keys, Codex transcripts, or customer source snapshots into public issues or PRs.

Proof packages retain frozen witnesses and bounded repository material — treat them as sensitive incident artifacts and share only with authorized reviewers.
