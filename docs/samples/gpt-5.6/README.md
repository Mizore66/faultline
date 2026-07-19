# Redacted GPT-5.6 sample artifacts (judge-inspectable)

These files illustrate the **live GPT-5.6 response shapes** FaultLine accepts for:

1. Blinded witness proposal (`fl witness propose --live`)
2. Evidence-cited repair brief (`fl repair brief --live`)

They are **not** proof facts, not PASS/FAIL verdicts, and not a substitute for running `--live` with `OPENAI_API_KEY`. Digests inside the repair packet are synthetic placeholders so judges can validate schema + citation binding offline.

| File | Role |
| --- | --- |
| `witness-proposal.sample.json` | Schema-shaped blinded proposal (filenames only — overlay bytes come from templates / Codex drafting) |
| `repair-evidence.sample.json` | Citation packet the brief must bind to |
| `repair-brief.sample.json` | Inferred guidance; every recommendation cites packet evidence IDs |

Validate locally:

```powershell
pnpm exec vitest run tests/gpt-samples.test.ts
```
