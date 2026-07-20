# Secret-scanner efficacy (SEC-09)

Generated: 2026-07-20T18:25:35.081Z
Corpus cases: 151
Corpus digest: `sha256:2083c387235d0afc4a6972ef76e63cf628d6167eddfc60e548b215b2551928c1`
Coverage label: **LIMITED** (kept until a qualified external review expands trust)

## Observed rates

| Metric | Point | 95% Wilson CI | n |
| --- | --- | --- | --- |
| Recall on labeled secrets | 1.0000 | [0.963, 1] | 100 |
| FP rate on benign | 0.5926 | [0.4073, 0.7549] | 27 |

Machine twin: `benchmarks/secret-scanner-efficacy.json`.
Regenerate: `node scripts/expand-secret-scanner-corpus.mjs && pnpm measure:secret-scanner`.
