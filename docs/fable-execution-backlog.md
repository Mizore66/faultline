# Fable execution backlog

This is an execution backlog, not evidence that a task has happened. Every
external, organic-incident, and soak claim remains blocked until the named
artifact exists.

## Engineering-ready

| ID | Task | Current gap | Done when |
| --- | --- | --- | --- |
| RIG-01 | Enumerate bounded distinct minimal sets and surface `NON_MONOTONIC_INTERACTION`. | Minimization certifies one 1-minimal set. | Result reports the number of distinct minimal sets within a stated budget; contradictory subset outcomes receive an explicit verdict and tests cover the A-fails/B-fails/A∪B-passes case. |
| RIG-02 | Execute all eight coverage-matrix rows as Docker E2E on two OS families. | Four rows are promoted by one Linux Docker job. | CI artifacts show eight executed IDs and a second OS-family execution, with provenance labels. |
| RIG-03 | Complete multi-image environment mapping. | Runtime mapping input exists; end-to-end certified heterogeneous-boundary coverage is missing. | A mid-range lockfile fixture maps fingerprints to images and produces a certified boundary in E2E. |
| RIG-04 | Publish reproducible scale benchmarks. | No retained 10k-file public-history benchmark. | Versioned benchmark fixture/protocol, raw outputs, and localization/snapshot numbers are published without invented conclusions. |
| RIG-05 | Add property tests for ledger ordering, cache-key separation, and hunk split/reapply byte identity. | Targeted examples exist; these three properties lack generative coverage. | Each invariant has a bounded property test and shrinking regression fixture. |
| SEC-01 | Fuzz offline verifiers against hostile packages. | Verifier validation is example-test heavy. | Seeded malformed manifests, traversal, hash-confusion, and archive-limit cases are exercised without invoking repository code. |
| SEC-02 | Remove `UNSAFE_LOCAL` from the shipped surface behind a development-only build gate. | Portable bundle export already refuses it, but runtime schemas still expose it. | Production build/API cannot construct or select the mode; development tests retain an explicitly gated fixture path. |
| SEC-03 | Join tag signature, green CI, attested judge artifacts, and stranger verification. | All pieces exist independently. | One documented, executable verification chain binds all four. |
| SEC-04 | Measure secret-scanner efficacy on a labeled corpus. | Fail-closed scanner has no published FP/FN study. | Corpus provenance, methodology, and rates are published with limitations. |
| PROD-01 | Build an interactive generated-repo first-incident tutorial. | Narrative first-incident guide only. | A fresh clone completes an interactive toy-repo path under tests. |
| PROD-02 | Ship real distribution. | Source-only install is intentional today. | Either an npm package or Node SEA binary is independently installed and smoke-tested. |
| PROD-03 | Generate runtime mapping files and split docs by persona. | Mapping remains hand-authored; docs are broad. | Generated mapping provenance and judge/new-user vs. forensics navigation are tested. |
| CODEX-01 | Plugin-native live-fire install and proof-package thread bindings. | Sidecar works; plugin-native adoption and bound drafting/repair-thread provenance are incomplete. | Native install demonstrably fires and package bindings verify without private transcript claims. |
| DEMO-01 | Zero-install path, comprehension test, Q&A, and fallback package. | Teleprompter and committed samples exist; rehearsal artifacts do not. | Hosted/Codespaces path plus retained test/rehearsal artifacts exist. |

## People-gated

| ID | Task | Required evidence |
| --- | --- | --- |
| EXT-01 | Independent security review. | Reviewer identity/role or permitted redaction, findings, and disposition. |
| EXT-02 | Three to five unassisted new-user runs and a friction → fix → re-test loop. | Consent, verbatim friction records, timing method, fixes, and retest outcomes. |
| EXT-03 | Second external turn-path validation. | Permissioned retained package and published boundary/scope statement. |

## Calendar- and event-gated

| ID | Task | Gate |
| --- | --- | --- |
| TIME-01 | Multi-platform sidecar soak. | Days of real sessions across macOS/Linux/Windows and another developer; publish stability statistics only after collection. |
| TIME-02 | Organic Break → Find → Prove → Fix → Prevent incident. | A naturally occurring regression with retained artifacts; do not seed or simulate one. |
| TIME-03 | Promote to `TURN_PROOF`. | Existing stated policy: soak, second external run, organic case, and offline reconstruction guarantees. |
| DEMO-02 | Record the final video. | Human-recorded live screens from the pinned tag; leave `YOUTUBE_URL` absent until public. |

## Execution order

1. RIG-05 and SEC-01 establish adversarial regression coverage before expanding claims.
2. RIG-01 and RIG-03 improve the core proof result; RIG-02 verifies them in CI.
3. SEC-02 through SEC-04 and RIG-04 produce security/scale evidence with explicit methodology.
4. PROD-01 through PROD-03 reduce adoption friction while EXT-01 through EXT-03 recruit real participants.
5. TIME-01 through TIME-03 and DEMO-02 remain blocked until their real-world evidence exists.

## Current execution rule

No task may be marked complete from a fixture, a prose assertion, a placeholder
URL, or a self-recorded synthetic incident when it requires external or elapsed
time evidence.

## Execution record

- 2026-07-20: RIG-05 is implemented in a bounded generative regression suite:
  raw lifecycle-event reorderings are rejected, distinct dirty content cannot
  reuse a turn-snapshot cache entry, and independently split Git hunks reapply
  byte-identically. This is local regression coverage, not scale evidence.
- 2026-07-20: SEC-01 has an initial generated hostile-package corpus: malformed
  manifests, malformed analysis, traversal-like declared paths, and an external
  root mismatch must fail closed without throwing. Archive-limit and broader
  mutation corpus coverage remain open.
- 2026-07-20: RIG-01 is implemented with a 10-patch-unit / 8-minimal-set
  bounded subset enumeration. Results retain the number of proper subsets
  searched, distinct 1-minimal sets found, and a `NON_MONOTONIC_INTERACTION`
  verdict when two recorded failures have a recorded passing union. The offline
  verifier checks those claims against retained forward-run attempts.
- 2026-07-20: **Codex organic self-dogfood evidence populated** (Windows slice;
  multi-OS remains future work for TIME-01): trusted `SIDE_CAR` sessions during
  genuine development produced a 96-event valid Windows ledger (~74 min, 18
  turn-tree snapshots, `CHECKPOINT_SKIPPED_DIRTY` on Stops) plus a shorter
  5-event session; public redacted excerpt + impact record at
  `docs/samples/faultline-self-sidecar-soak/` and
  `docs/impact-validation-codex-self-dogfood.md`. Guided `fl investigate --ci-log`
  on organic CI failure `29694948356` retained freeze
  `ci-29694948356-pnpm-test` (digest `sha256:89313b84…`, `fl witness verify`
  valid). **Future work for TIME-01 (#152):** macOS + Linux + second developer +
  published per-OS stability table. Optional TIME-02 deepening (#153): full
  PREVENTION_VERIFIED arc beyond organic use + CI freeze.
