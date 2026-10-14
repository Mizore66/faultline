# Secret-scanner efficacy (SEC-09)

Generated: 2026-07-16T12:00:00.000Z
Corpus cases: 241
Corpus digest: `sha256:bb509f64837395528049a54847e5a88a8abe05900e4de6d9917ee1c0a592ed67`
Coverage label: **LIMITED** (kept until a qualified external review expands trust)

## Observed rates

| Metric | Point | 95% Wilson CI | n |
| --- | --- | --- | --- |
| Recall on labeled secrets | 1.0000 | [0.9745, 1] | 147 |
| FP rate on benign | 0.3556 | [0.2322, 0.5016] | 45 |

Machine twin: `benchmarks/secret-scanner-efficacy.json`.
Regenerate: `node scripts/expand-secret-scanner-corpus.mjs && pnpm measure:secret-scanner`.
