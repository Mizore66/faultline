# FaultLine Devpost description draft

> Paste into Devpost after a final human pass. Do not invent a video URL — leave that field empty on Devpost until the YouTube link exists.

## Project name

FaultLine

## Category

**Developer Tools**

## One-line summary

FaultLine freezes a reviewed executable regression witness, replays it across recorded Git states, and packages only the evidence another engineer can verify offline.

## The problem

When an agent-assisted change turns CI red, a team can often see logs and a failing test but still lacks a portable answer to a narrower operational question: which recorded source state first fails the exact reviewed predicate, and what evidence supports that conclusion?

Git history, CI logs, and a reproduction case are useful inputs, but they do not by themselves preserve a human-reviewed witness, repeated isolated executions across a range, a stated stability rule, and an offline-verifiable package of the resulting facts. FaultLine is designed for that gap. It does not claim model intent, a unique semantic root cause, or universal arbitrary-code execution.

## What FaultLine demonstrates today

FaultLine is a runnable TypeScript CLI and read-only proof view. Its implemented workflow is:

1. A human reviews, approves, and freezes an executable witness.
2. FaultLine replays that unchanged witness over selected Git states. Proof-grade Git investigation uses a digest-pinned Docker image, constrained execution, and repeated runs.
3. It records only supported verdicts and exposes stable PASS-to-FAIL or FAIL-to-PASS transitions when the stored executions meet the configured rule.
4. It writes a portable proof package that another engineer can inspect and verify without rerunning repository code.
5. It can retain bounded, explicitly inferred GPT-5.6 repair guidance that cites verified evidence rather than changing the verdict.

**Demo hierarchy for judges:**

- **Product path (`COMMIT_PROOF`):** recorded self-incident — human-frozen witness measured `PASS -> FAIL` at `97c3290` and `FAIL -> PASS` at `07ee7f1`; package root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`. Details: [faultline-self-incident.md](faultline-self-incident.md). Optional live smoke: `pnpm fl demo live-git --export-only` (Docker).
- **Sandbox (no Docker / no API key):** `pnpm fl judge-demo` opens the incident UI on a fixture bundle; or open [`judge-preview.html`](judge-preview.html) with zero install. Label these as protocol samples, not real incidents.
- **Codex-native preview:** turn-boundary localization is `EXPERIMENTAL_TURN` and is not interchangeable with commit-path proof yet.

## How Codex and GPT-5.6 are used

**Qualifying Codex Project session (`/feedback`):** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening (witness lock, Git investigation/minimization, sandbox policy, proof bundles). Product decisions stayed human-owned: evidence labels, fail-closed sandbox rules, and refusing model text as verdicts. At runtime, FaultLine can observe public Codex hook metadata via an opt-in sidecar or accept a hash-chained Codex-compatible lifecycle ledger; transports are labeled rather than claimed as private Codex interception.

GPT-5.6 is used through the Responses API only at two bounded points: proposing a blinded witness and producing an evidence-cited repair brief from an already verified package. GPT-5.6 cannot decide the PASS/FAIL verdict, assign model intent, replace the frozen witness, or create proof evidence.

## Fast judge path

**Supported install today:** clone `main` (MIT). Global `npm install -g @mizore66/faultline` is **not** supported yet (name reserved; no published release).

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout main
pnpm install --frozen-lockfile
pnpm fl judge-demo
```

Offline verify of the fixture (no browser):

```powershell
pnpm fl judge-demo --rerun-all --export-only
pnpm fl verify .faultline/bundles/judge-demo
```

Live Git/Docker smoke:

```powershell
pnpm fl demo live-git --export-only
```

Guided real CI log (human Approve then Freeze in the local review UI; never auto-approves):

```powershell
pnpm fl investigate --ci-log .\ci.log --repo . --command "<failing predicate>" --runtime node
```

**Recorded demo evidence (fill at recording time):**

- Repository: https://github.com/Mizore66/faultline (MIT, branch `main`)
- Self-incident root (already recorded): `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`
- Commit shown in video: _(record at demo time)_
- Platform / Node / Docker: _(record at demo time)_
- CI run URL: _(optional; omit if not shown)_

## Why it matters

FaultLine is intended for engineers responsible for diagnosing an agent-assisted regression under review, incident, or audit pressure. Its value proposition is not that it diagnoses every bug: it narrows one high-friction handoff by preserving an executable question, the replay facts, the proof boundary, and the limits of those facts.

**Validated so far (dogfood only):** FaultLine's first completed proof package is this repository's own provenance-workflow regression (`PASS -> FAIL` at `97c3290`, recovery at `07ee7f1`). That supports the frozen workflow predicate only—not agent intent, unique semantic cause, or third-party adoption. Do not add time-saved, customer, or accuracy claims unless supported by a consented record in [the impact-validation template](impact-validation-template.md).

## How FaultLine differs

FaultLine composes a reviewed executable witness, repeated isolated Git-state replays, a portable offline-verifiable proof package, and explicit evidence boundaries. It is not a replacement for Git bisect, CI artifacts, a normal repro case, or provenance tooling; it uses or complements them. See [the concise scope comparison](differentiation.md).

## Submission fields

| Field | Value |
| --- | --- |
| Track | Developer Tools |
| Public narrated demo video (<3 min, shows product + spoken Codex **and** GPT-5.6) | **REQUIRED — insert YouTube URL when recorded** |
| `/feedback` Codex session ID | `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` |
| Repository | https://github.com/Mizore66/faultline (MIT, `main`) |
| Install for judges | Clone + `pnpm install --frozen-lockfile` + `pnpm fl judge-demo` (or open `docs/judge-preview.html`) |
| npm global install | Not claimed (unpublished) |

Recheck https://openai.devpost.com/ rules, deadline (Jul 21, 2026 @ 5:00pm PDT), and required form fields immediately before submit.
