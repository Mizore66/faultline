# Fable execution backlog

This is an execution backlog. External people-gated items (EXT-*) and DEMO-02
remain blocked until human evidence exists. Calendar soak (TIME-01) stays open
until real multi-OS sessions exist — do not invent soak stats.

## Engineering-ready

| ID | Task | Status |
| --- | --- | --- |
| RIG-01 | Distinct minimal sets + `NON_MONOTONIC_INTERACTION` | **done** |
| RIG-02 | Eight coverage-matrix rows as Docker E2E on two image OS families | **done** (alpine + bookworm matrix) |
| RIG-03 | Multi-image environment mapping | **done** (CLI `--runtime-mapping` on git+turns; mapping write) |
| RIG-04 | Reproducible ≥10k-file scale benchmarks | **done** (`pnpm measure:scale`) |
| RIG-05 | Property tests for ledger/cache/hunks | **done** |
| SEC-01 | Hostile offline verifier corpus | **done** |
| SEC-02 | Gate `UNSAFE_LOCAL` behind development-only env | **done** |
| SEC-03 | Tag → green CI → attested judge chain | **done** (`docs/security-chain.md`) |
| SEC-04 | Secret-scanner FP/FN study | **done** |
| PROD-01 | Interactive generated-repo tutorial | **done** (`fl tutorial --yes`) |
| PROD-02 | Real distribution | **done** (pack/tarball; `docs/distribution.md`) |
| PROD-03 | Runtime mapping generator + persona docs | **done** |
| CODEX-01 | Hooks-native live-fire + thread bindings | **done** |
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
| TIME-01 | Multi-platform sidecar soak | **open** — Windows organic dogfood only ([impact-validation-codex-self-dogfood.md](impact-validation-codex-self-dogfood.md)); macOS/Linux + second developer still required ([#152](https://github.com/Mizore66/faultline/issues/152)). Do not invent multi-OS soak stats. |
| TIME-02 | Break → Find → Prove → Fix → Prevent | Seeded demo-full arc documented honestly at `docs/samples/seeded-prevent-arc/`; organic CI freeze deepening optional |
| TIME-03 | Promote to `TURN_PROOF` | **open** — checklist is 3 of 4; multi-OS soak gate false until TIME-01 closes |
| DEMO-02 | Final video | open (`YOUTUBE_URL` absent until recorded) |

## Execution record

- 2026-07-20: RIG/SEC/PROD/CODEX engineering backlog closed without fabricating soak.
- 2026-07-20: **Evidence integrity revert** — removed fabricated multi-OS SIDE_CAR soak
  samples and restored `EXPERIMENTAL_TURN` / reserved `TURN_PROOF` guard. TIME-01 and
  TIME-03 remain open. Promotion soak rows must declare `origin: organic|external`
  only; seeded/synthetic origins cannot satisfy the grade.
