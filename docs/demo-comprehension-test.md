# 20-second comprehension test (DEMO-03)

**Accept:** a cold viewer can state what the product does after a short open — iterate until true.

This file is the **protocol**. It does **not** invent subject transcripts. Fill the score sheet only after a real cold viewer.

Related: umbrella [#148](https://github.com/Mizore66/faultline/issues/148), issue [#176](https://github.com/Mizore66/faultline/issues/176).

## Cold open (≤20 seconds, on screen)

Show **one** of:

1. Codespaces / clone → `pnpm fl judge-proof` landing on the verified `COMMIT_PROOF` sample, **or**  
2. Open [`self-incident-proof-preview.html`](self-incident-proof-preview.html) full-bleed.

Say (or caption) exactly one sentence:

> FaultLine freezes one reviewed executable question, replays it over recorded Git states, and proves only what those executions support.

Stop. Do not explain bisect, Codex, or grades yet.

## Ask (immediately)

> In your own words: what does this product do?

## Pass / fail

| Result | Criterion |
| --- | --- |
| **PASS** | Viewer mentions (paraphrase OK): freeze/question **and** replay/proof over history or Git — without claiming “AI finds bugs by itself” as the product |
| **FAIL** | Viewer thinks it is only ChatGPT, only CI, only git bisect, or cannot answer |

On **FAIL**: shorten the open, change the visual (HTML vs CLI), retest with a **new** cold viewer. Do not coach the same person mid-test.

## Score sheet (copy per subject)

```text
DEMO-03 comprehension score sheet
Date (UTC):
Subject ID (initials / anon):
Cold? (never saw FaultLine before): yes / no
Open used: judge-proof / HTML preview / other:
Product sentence used (paste):
Viewer paraphrase (quote, do not edit):
PASS / FAIL:
If FAIL, change for next iteration:
Facilitator:
```

Retain filled sheets under `docs/samples/comprehension/` only when real (do not fabricate). Example filename: `2026-07-21-subject-a.md`.

## Facilitator tips

- Prefer subjects outside the Build Week team.  
- Phone screen-share of Codespaces is enough.  
- If they ask “is it AI debugging?”, answer after the paraphrase: GPT proposes; humans freeze; executions prove.
