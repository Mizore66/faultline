# Secret-scanner efficacy (SEC-09)

Generated: 2026-07-16T12:00:00.000Z
Corpus cases: 241
Corpus digest: `sha256:928f14c7117c8034b464e52139e2ada20873894b93963ba339d2eac36d4d3f02`
Coverage label: **LIMITED** (kept until a qualified external review expands trust)

## Observed rates

| Metric | Point | 95% Wilson CI | n |
| --- | --- | --- | --- |
| Recall on labeled secrets | 1.0000 | [0.9745, 1] | 147 |
| FP rate on benign | 0.3556 | [0.2322, 0.5016] | 45 |

Machine twin: `benchmarks/secret-scanner-efficacy.json`.
Regenerate: `pnpm build && node scripts/expand-secret-scanner-corpus.mjs && pnpm measure:secret-scanner`.
