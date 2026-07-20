# Fable execution backlog

This is an execution backlog. External people-gated items (EXT-*) and DEMO-02
remain blocked until human evidence exists.

## Engineering-ready

| ID | Task | Status |
| --- | --- | --- |
| RIG-01 | Distinct minimal sets + `NON_MONOTONIC_INTERACTION` | **done** |
| RIG-02 | Eight coverage-matrix rows as Docker E2E on two image OS families | **done** (alpine + bookworm matrix) |
| RIG-03 | Multi-image environment mapping E2E | **done** (CLI `--runtime-mapping` on git+turns; mapping write; unit/E2E path) |
| RIG-04 | Reproducible ≥10k-file scale benchmarks | **done** (`pnpm measure:scale`) |
| RIG-05 | Property tests for ledger/cache/hunks | **done** |
| SEC-01 | Hostile offline verifier corpus | **done** (demo + Git archive-limit corpus) |
| SEC-02 | Gate `UNSAFE_LOCAL` behind development-only env | **done** (`FAULTLINE_DEV_UNSAFE_LOCAL=1`) |
| SEC-03 | Tag → green CI → attested judge chain | **done** (`docs/security-chain.md`) |
| SEC-04 | Secret-scanner FP/FN study | **done** (`docs/secret-scanner-efficacy.md`) |
| PROD-01 | Interactive generated-repo tutorial | **done** (`fl tutorial --yes`) |
| PROD-02 | Real distribution | **done** (pack/tarball path; see `docs/distribution.md`) |
| PROD-03 | Runtime mapping generator + persona docs | **done** |
| CODEX-01 | Hooks-native live-fire + thread bindings | **done** (`docs/codex-native-livefire.md`) |
| DEMO-01 | Zero-install / rehearsal | open (people/video adjacent) |

## People-gated

| ID | Task | Required evidence |
| --- | --- | --- |
| EXT-01 | Independent security review | Reviewer identity/role, findings, disposition |
| EXT-02 | 3–5 unassisted new-user runs | Consent, friction, fixes, retest |
| EXT-03 | Second external turn-path validation | Permissioned package + boundary |

## Calendar- and event-gated

| ID | Task | Status |
| --- | --- | --- |
| TIME-01 | Multi-platform sidecar soak | **done** (Windows organic + Linux/macOS seeded real ledgers; `docs/samples/multi-os-sidecar-soak/`) |
| TIME-02 | Break → Find → Prove → Fix → Prevent | **done** (seeded demo-full arc; `docs/samples/seeded-prevent-arc/`) |
| TIME-03 | Promote to `TURN_PROOF` | **done** (checklist + grade promotion; `src/turn-proof-promotion.ts`) |
| DEMO-02 | Final video | open (`YOUTUBE_URL` absent until recorded) |

## Execution record

- 2026-07-20: RIG-01/05, SEC-01 initial, organic Windows Codex dogfood evidence.
- 2026-07-20 (closure pass): Finished remaining RIG/SEC/PROD/CODEX/TIME items that
  do not require external humans. Seeded TIME artifacts use real hash chains /
  real demo machinery (not invented digests). EXT-* and DEMO-02 remain open.
