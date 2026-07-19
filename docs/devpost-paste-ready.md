# Devpost paste-ready (fill YOUTUBE + external summary, then submit)

> Replace `YOUTUBE_URL` and the external impact paragraph, then paste into https://openai.devpost.com/  
> Deadline: Jul 21, 2026 @ 5:00pm PDT

---

## Project name

FaultLine

## Category / track

Developer Tools

## Tagline / one-line summary

FaultLine produces a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer can verify where that reviewed witness first goes bad without re-running repository code.

## Demo video

YOUTUBE_URL

## Repository

https://github.com/Mizore66/faultline  
License: MIT  
Qualifying Codex `/feedback` session: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

## How to run (judges)

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.0-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

Pinned submission tag / Release: [`v0.1.0-buildweek`](https://github.com/Mizore66/faultline/releases/tag/v0.1.0-buildweek) · SHA `589bbfd2dae6907e94b4a993e98848c35d3e5a81`.  
Green CI (incl. native Docker E2E): https://github.com/Mizore66/faultline/actions/runs/29673784462  
Zero-install UI snapshot: open `docs/self-incident-proof-preview.html` after clone (or regenerate with `pnpm fl commit-proof-preview`).  
GPT-5.6 sample artifacts (no API key): `docs/samples/gpt-5.6/`.

Judge sample root: `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9`  
(Not the historical self-incident root `sha256:f6a391b3…` — that dogfood is documented separately.)

**Source-only install:** clone + `pnpm fl` (commands above). Global `npm install -g @mizore66/faultline` is **not** supported yet — do not claim npm availability on Devpost.

---

## Full description (paste)

### The problem

When an agent-assisted change turns CI red, a team can often see logs and a failing test but still lacks a portable answer to a narrower operational question: which recorded source state first fails the exact reviewed predicate, and what evidence supports that conclusion?

Git history, CI logs, and a reproduction case are useful inputs, but they do not by themselves preserve a human-reviewed witness, repeated isolated executions across a range, a stated stability rule, and an offline-verifiable package of the resulting facts. FaultLine is designed for that gap. It does not claim model intent, a unique semantic root cause, or universal arbitrary-code execution.

### What FaultLine demonstrates

1. A human reviews, approves, and freezes an executable witness.
2. FaultLine replays that unchanged witness over selected Git states (proof-grade path: digest-pinned Docker, constrained execution, repeated runs).
3. It records only supported verdicts and stable PASS→FAIL / FAIL→PASS transitions when executions meet the rule.
4. It writes a portable proof package another engineer can verify offline without re-running repository code.
5. GPT-5.6 may propose a blinded witness or produce an evidence-cited repair brief — it never assigns PASS/FAIL.

**Runnable Idea path for judges:** `pnpm fl judge-proof` (sample root `sha256:f85c446d…`).  
**Historical dogfood (separate):** provenance-workflow `PASS→FAIL` at `97c3290`, root `sha256:f6a391b3…` — see repo docs.  
**Fixture sandbox (not the Idea):** `pnpm fl judge-demo` / `docs/judge-preview.html`.

### How Codex and GPT-5.6 are used

**Qualifying Codex `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

Codex accelerated implementation, adversarial testing, and product hardening. Product decisions stayed human-owned: evidence labels, fail-closed sandbox rules, and refusing model text as verdicts. At runtime, FaultLine can observe public Codex hook metadata via an opt-in sidecar or accept a hash-chained Codex-compatible lifecycle ledger.

GPT-5.6 is used only for blinded witness proposal and evidence-cited repair briefs. It cannot decide verdicts, assign model intent, replace the frozen witness, or create proof evidence.

### Why it matters

FaultLine is for engineers diagnosing an agent-assisted regression under review, incident, or audit pressure. It narrows one high-friction handoff: executable question + replay facts + proof boundary + explicit limits.

**Dogfood:** FaultLine’s own provenance-workflow regression (`PASS→FAIL` at `97c3290`) — see `docs/impact-validation-self-incident.md`.

**External N=1 (from completed `docs/impact-validation-external-01.md`):**  
In one consented external protocol validation on `monashblockchain/MUMBCS`, FaultLine froze a human-reviewed witness over production slug validation and replayed recorded turn states in Docker. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This validates the external workflow of freeze, replay, locate, and verify; it does not claim FaultLine caught a naturally occurring production bug, agent intent, unique root cause, or prevention.

### How FaultLine differs

FaultLine combines (1) a human-reviewed immutable executable witness, (2) replay over recorded Git states with a stability rule, (3) a portable proof package plus offline verifier that does not execute repository code, and (4) evidence labels separating executed facts, derivations, inferences, and unknowns. Complements Git bisect, CI artifacts, and repro cases — does not replace them.

---

## Submission field checklist

| Field | Value |
| --- | --- |
| Track | Developer Tools |
| Video | YOUTUBE_URL |
| `/feedback` | `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` |
| Repo | https://github.com/Mizore66/faultline (MIT) |
| Install | clone + `pnpm fl judge-proof` |
| npm global | not claimed |

## Final gate

- [ ] YOUTUBE_URL filled and video public
- [x] EXTERNAL_IMPACT_SUMMARY filled from completed external-01 record ([publishable-summary-mumbcs.md](publishable-summary-mumbcs.md))
- [ ] Video names Codex and GPT-5.6
- [ ] Cold-open follows teleprompter: **preferred** external MUMBCS protocol beat, then `judge-proof` sample (fallback: sample-only if external beat skipped) — see [video-teleprompter.md](video-teleprompter.md)
- [ ] Submitted on Devpost before Jul 21, 2026 @ 5:00pm PDT
