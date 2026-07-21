# Devpost paste-ready (fill YOUTUBE + external summary, then submit)

> Replace `YOUTUBE_URL` after you record (see [video-teleprompter.md](video-teleprompter.md)). Do not invent a URL.  
> Deadline: Jul 21, 2026 @ 5:00pm PDT  
> Bound only to green artifacts: external-01, observed-external-transport, coverage-matrix honesty, judge sample / self-incident roots, and (with Docker) `fl demo full` → `PREVENTION_VERIFIED` + `AGENTS.md`. No signed-release / soak / stranger claims.

---

## Project name

FaultLine

## Category / track

Developer Tools

## Tagline / one-line summary

FaultLine produces a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer can verify where that reviewed witness first goes bad without re-running repository code.

**Autonomous sessions:** Unattended sessions keep moving, but nothing becomes proof until a human owned the question — before it ran, or verifiably unchanged after.

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
git checkout v0.1.8-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

Pinned submission tag / Release: [`v0.1.8-buildweek`](https://github.com/Mizore66/faultline/releases/tag/v0.1.8-buildweek) · validated on the `v0.1.8-buildweek` tag commit � **literal SHA: FILL_AFTER_TAG** (human fills after cutting the signed tag).
`pnpm install` builds `dist` via `prepare`; `pnpm fl` runs `node dist/cli.js` (compiled entry).  
Green CI: Actions Verify workflow on the `v0.1.8-buildweek` pin (incl. native Docker E2E).
Zero-install UI snapshot: open `docs/self-incident-proof-preview.html` after clone (or regenerate with `pnpm fl commit-proof-preview`).  
GPT-5.6 sample artifacts (no API key): `docs/samples/gpt-5.6/`.  
Honest non-Codex transport sample: `docs/samples/observed-external-transport/` (`OBSERVED_EXTERNAL_TRANSPORT`).  
Coverage matrix (hybrid: Docker CI promotes listed rows to `EXECUTABLE_E2E`): `benchmarks/REPORT.md`.

Judge sample root: `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9`  
(Not the historical self-incident root `sha256:f6a391b3…` — that dogfood is documented separately.)

**Source-only install:** clone + `pnpm fl` (commands above). Or install the published package: `npm i -g @mizore66/faultline@0.1.2`.

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
**Green supporting artifacts:** completed external-01 protocol record; `docs/samples/observed-external-transport/`; coverage matrix in `benchmarks/REPORT.md` (8 spec/unit rows — explicitly not independent CI Docker soak).

### How Codex and GPT-5.6 are used

**Qualifying Codex `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

Codex accelerated implementation, adversarial testing, and product hardening. Product decisions stayed human-owned: evidence labels, fail-closed sandbox rules, and refusing model text as verdicts. At runtime, FaultLine can observe public Codex hook metadata via an opt-in sidecar or accept a hash-chained Codex-compatible lifecycle ledger. Non-Codex editor checkpoints use the honestly labeled `OBSERVED_EXTERNAL_TRANSPORT` sample path — never a forged Codex hook ledger.

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

- [ ] YOUTUBE_URL filled and video public (**you** must record — teleprompter does not ship a URL)
- [x] EXTERNAL_IMPACT_SUMMARY filled from completed external-01 record ([publishable-summary-mumbcs.md](publishable-summary-mumbcs.md))
- [ ] Video names Codex and GPT-5.6
- [ ] Cold-open follows teleprompter: **preferred** external MUMBCS protocol beat, then `judge-proof` sample (fallback: sample-only if external beat skipped) — see [video-teleprompter.md](video-teleprompter.md)
- [ ] No claims of signed release, soak, or stranger test unless those artifacts exist at submit time
- [ ] If showing `fl demo full`, only claim `PREVENTION_VERIFIED` / `AGENTS.md` when that run printed those classifications
- [ ] Submitted on Devpost before Jul 21, 2026 @ 5:00pm PDT
