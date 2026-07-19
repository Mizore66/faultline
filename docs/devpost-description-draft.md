# FaultLine Devpost description draft

> Paste into Devpost after a final human pass. Do not invent a video URL — leave that field empty on Devpost until the YouTube link exists.

## Project name

FaultLine

## Category

**Developer Tools**

## One-line summary

FaultLine produces a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer can verify where that reviewed witness first goes bad without re-running repository code.

## The problem

When an agent-assisted change turns CI red, a team can often see logs and a failing test but still lacks a portable answer to a narrower operational question: which recorded source state first fails the exact reviewed predicate, and what evidence supports that conclusion?

Git history, CI logs, and a reproduction case are useful inputs, but they do not by themselves preserve a human-reviewed witness, repeated isolated executions across a range, a stated stability rule, and an offline-verifiable package of the resulting facts. FaultLine is designed for that gap. It does not claim model intent, a unique semantic root cause, or universal arbitrary-code execution.

## What FaultLine demonstrates today

**Runnable Idea path (`COMMIT_PROOF` sample for judges):**

```powershell
pnpm install --frozen-lockfile
pnpm fl judge-proof
pnpm fl commit-proof-preview
```

Installed sample root: `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` (from `fl demo live-git`; details: [COMMIT_PROOF_SAMPLE.md](samples/COMMIT_PROOF_SAMPLE.md)). Zero-install snapshot: [self-incident-proof-preview.html](self-incident-proof-preview.html).

**Historical dogfood (separate package):** this repository’s provenance-workflow regression measured `PASS -> FAIL` at `97c3290` and `FAIL -> PASS` at `07ee7f1`; recorded root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`. Runbook: [faultline-self-incident.md](faultline-self-incident.md). That root is **not** what `judge-proof` opens unless the sample is replaced with that exact package.

Implemented workflow:

1. A human reviews, approves, and freezes an executable witness.
2. FaultLine replays that unchanged witness over selected Git states. Proof-grade Git investigation uses a digest-pinned Docker image, constrained execution, and repeated runs.
3. It records only supported verdicts and exposes stable PASS-to-FAIL or FAIL-to-PASS transitions when the stored executions meet the configured rule.
4. It writes a portable proof package that another engineer can inspect and verify without rerunning repository code.
5. It can retain bounded, explicitly inferred GPT-5.6 repair guidance that cites verified evidence rather than changing the verdict.

**Supporting paths (not the Idea headline):**

- **Sandbox (no Docker / no API key):** `pnpm fl judge-demo` or [`judge-preview.html`](judge-preview.html) — protocol fixture only.
- **Codex-native preview:** turn-boundary localization is `EXPERIMENTAL_TURN` and is not interchangeable with commit-path proof yet.
- **Green supporting artifacts only:** completed [external-01](impact-validation-external-01.md); [observed-external-transport](samples/observed-external-transport/); coverage matrix honesty in [benchmarks/REPORT.md](../benchmarks/REPORT.md); optional Docker flagship `fl demo full` (COMMIT_PROOF → minimize → PREVENTION_VERIFIED + AGENTS.md). Do not claim signed release, soak, or stranger test unless those artifacts exist.

## How Codex and GPT-5.6 are used

**Qualifying Codex Project session (`/feedback`):** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening (witness lock, Git investigation/minimization, sandbox policy, proof bundles). Product decisions stayed human-owned: evidence labels, fail-closed sandbox rules, and refusing model text as verdicts. At runtime, FaultLine can observe public Codex hook metadata via an opt-in sidecar or accept a hash-chained Codex-compatible lifecycle ledger; transports are labeled rather than claimed as private Codex interception.

GPT-5.6 is used through the Responses API only at two bounded points: proposing a blinded witness and producing an evidence-cited repair brief from an already verified package. GPT-5.6 cannot decide the PASS/FAIL verdict, assign model intent, replace the frozen witness, or create proof evidence.

## Fast judge path

**Supported install today:** clone `main` (MIT). Global `npm install -g @mizore66/faultline` is **not** supported yet.

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.3-buildweek
pnpm install --frozen-lockfile
pnpm fl judge-proof
```

Fixture sandbox (not the product claim): `pnpm fl judge-demo` or open `docs/judge-preview.html`.

**Recorded demo evidence (fill at recording time):**

- Repository: https://github.com/Mizore66/faultline (MIT, branch `main`)
- Judge sample root: `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9`
- Historical dogfood root: `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`
- Commit / platform shown in video: _(record at demo time)_

## Why it matters

FaultLine is intended for engineers responsible for diagnosing an agent-assisted regression under review, incident, or audit pressure. Its value proposition is not that it diagnoses every bug: it narrows one high-friction handoff by preserving an executable question, the replay facts, the proof boundary, and the limits of those facts.

**Validated so far (dogfood only):** see [impact-validation-self-incident.md](impact-validation-self-incident.md) — FaultLine’s own provenance-workflow regression (`PASS -> FAIL` at `97c3290`, recovery at `07ee7f1`). No third-party adoption or time-saved metrics. Do not add customer claims without a consented new record in [impact-validation-template.md](impact-validation-template.md).

## How FaultLine differs

FaultLine combines four narrow pieces: (1) a human-reviewed immutable executable witness, (2) replay of that exact witness over recorded Git states with a stated stability rule, (3) a portable proof package plus an offline semantic verifier that does not execute repository code, and (4) evidence labels that separate executed facts, derivations, inferences, and unknowns. Complements Git bisect, CI artifacts, and repro cases — does not replace them. See [differentiation.md](differentiation.md).

## Submission fields

| Field | Value |
| --- | --- |
| Track | Developer Tools |
| Public narrated demo video (<3 min, shows product + spoken Codex **and** GPT-5.6) | **REQUIRED — insert YouTube URL when recorded** |
| `/feedback` Codex session ID | `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` |
| Repository | https://github.com/Mizore66/faultline (MIT, `main`) |
| Install for judges | Clone + `pnpm fl judge-proof` (sample root `f85c446d…`) or open `docs/self-incident-proof-preview.html` |
| npm global install | Not claimed (unpublished) |

Recheck https://openai.devpost.com/ rules, deadline (Jul 21, 2026 @ 5:00pm PDT), and required form fields immediately before submit.
