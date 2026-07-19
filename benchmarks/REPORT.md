# FaultLine adversarial scenario coverage matrix

This artifact documents expected outcomes covered by **specification + unit/integration tests**.
It is **not** an end-to-end Docker benchmark. Every row below is `SPEC_AND_UNIT_COVERED` today
(0 of 8 are independent CI E2E executions). A future hybrid may split 4 executable integration
scenarios from 4 spec-only adversarial scenarios — until then, do not call this a benchmark.

- Scenarios: **8**
- Distinct expected outcomes: **8**
- Unsupported exact-cause claims: **0**
- Execution mode: **UNIT_AND_INTEGRATION_COVERAGE**
- Executable E2E rows in CI: **0** (all rows are spec/unit coverage)

| Scenario | Expected | Observed | Result | Coverage |
| --- | --- | --- | --- | --- |
| `dirty-codex-turns` | PROVENANCE_GAP | PROVENANCE_GAP | PASS | SPEC_AND_UNIT_COVERED |
| `flaky-witness` | REFUSE_UNSTABLE | REFUSE_UNSTABLE | PASS | SPEC_AND_UNIT_COVERED |
| `historical-api-incompatibility` | INAPPLICABLE | INAPPLICABLE | PASS | SPEC_AND_UNIT_COVERED |
| `lockfile-change` | REQUIRE_ENV_MAPPING | REQUIRE_ENV_MAPPING | PASS | SPEC_AND_UNIT_COVERED |
| `minimization-budget-exhausted` | ASSOCIATED_NONMINIMAL | ASSOCIATED_NONMINIMAL | PASS | SPEC_AND_UNIT_COVERED |
| `multi-file-interaction` | ONE_MINIMAL_SET | ONE_MINIMAL_SET | PASS | SPEC_AND_UNIT_COVERED |
| `repaired-and-reintroduced` | MULTIPLE_TRANSITIONS | MULTIPLE_TRANSITIONS | PASS | SPEC_AND_UNIT_COVERED |
| `simple-source-regression` | LOCALIZE_BOUNDARY | LOCALIZE_BOUNDARY | PASS | SPEC_AND_UNIT_COVERED |

## Coverage legend

| Label | Meaning |
| --- | --- |
| `SPEC_AND_UNIT_COVERED` | Expected outcome asserted by unit/integration tests — **not** a full Docker E2E run in CI |
| `EXECUTABLE_E2E` (reserved) | Scenario actually executed end-to-end in CI with Docker |

## Detail

- **dirty-codex-turns** (PROVENANCE_GAP): Turn tree snapshots exist; clean checkpoints may be skipped.
- **flaky-witness** (REFUSE_UNSTABLE): No stable state and no A-grade proof.
- **historical-api-incompatibility** (INAPPLICABLE): INCOMPATIBLE_STATE must not become FAIL.
- **lockfile-change** (REQUIRE_ENV_MAPPING): Heterogeneous environment fingerprints refuse single-image proof.
- **minimization-budget-exhausted** (ASSOCIATED_NONMINIMAL): Return associated set without unsupported exact-cause claims.
- **multi-file-interaction** (ONE_MINIMAL_SET): Bidirectional counterfactual validation at file-level Git patch units.
- **repaired-and-reintroduced** (MULTIPLE_TRANSITIONS): Non-monotonic PASS/FAIL transitions must remain visible.
- **simple-source-regression** (LOCALIZE_BOUNDARY): Expect one stable PASS->FAIL boundary.

Regenerate with `pnpm coverage-matrix` (alias: `pnpm benchmark`).
