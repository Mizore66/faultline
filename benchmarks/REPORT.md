# FaultLine incident benchmark

- Incidents: **8**
- Expected localization outcomes: **8**
- Unsupported exact-cause claims: **0**

| Fixture | Expected | Status | Detail |
| --- | --- | --- | --- |
| `dirty-codex-turns` | PROVENANCE_GAP | SPEC_AND_UNIT_COVERED | Turn tree snapshots exist; clean checkpoints may be skipped. |
| `flaky-witness` | REFUSE_UNSTABLE | SPEC_AND_UNIT_COVERED | No stable state and no A-grade proof. |
| `historical-api-incompatibility` | INAPPLICABLE | SPEC_AND_UNIT_COVERED | INCOMPATIBLE_STATE must not become FAIL. |
| `lockfile-change` | REQUIRE_ENV_MAPPING | SPEC_AND_UNIT_COVERED | Heterogeneous environment fingerprints refuse single-image proof. |
| `minimization-budget-exhausted` | ASSOCIATED_NONMINIMAL | SPEC_AND_UNIT_COVERED | Return associated set without unsupported exact-cause claims. |
| `multi-hunk-interaction` | ONE_MINIMAL_SET | SPEC_AND_UNIT_COVERED | Bidirectional counterfactual validation. |
| `repaired-and-reintroduced` | MULTIPLE_TRANSITIONS | SPEC_AND_UNIT_COVERED | Non-monotonic PASS/FAIL transitions must remain visible. |
| `simple-source-regression` | LOCALIZE_BOUNDARY | SPEC_AND_UNIT_COVERED | Expect one stable PASS->FAIL boundary. |

This matrix documents adversarial expectations. It does not claim a unique semantic root cause for any fixture.
