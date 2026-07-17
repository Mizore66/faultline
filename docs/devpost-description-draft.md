# FaultLine Devpost description draft

> This is a human-editable submission draft, not a record that FaultLine has been submitted, tested by users, or accepted into an event. Replace every bracketed field, delete any unsupported statement, and have the submitter review the final wording before publishing.

## Project name

FaultLine

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

The included `fl judge-demo` is deterministic and intentionally limited: it lets a reviewer inspect the evidence model without Docker, a network call, an API key, or a pre-existing incident. The separate `fl demo live-git` path is the actual Docker-backed Git replay demonstration. FaultLine has also completed a bounded self-incident: a human-frozen witness measured `PASS -> FAIL` at `97c3290` and `FAIL -> PASS` at `07ee7f1` in its historical provenance workflow; the recorded package root is `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`. This is evidence for that exact workflow predicate, not an attribution of model intent or a universal diagnosis claim. The README explains the boundary between those paths.

## How Codex and GPT-5.6 are used

Codex was used to accelerate FaultLine's implementation, adversarial testing, and product hardening. At runtime, FaultLine accepts a hash-chained Codex-compatible lifecycle ledger and labels its observed transport rather than claiming private Codex interception.

GPT-5.6 is used through the Responses API only at two bounded points: proposing a blinded witness and producing an evidence-cited repair brief from an already verified package. GPT-5.6 cannot decide the PASS/FAIL verdict, assign model intent, replace the frozen witness, or create proof evidence.

## Fast judge path

Run this no-Docker, no-API-key path from a checkout with Node.js 22+ and pnpm 10.32.1:

    pnpm install --frozen-lockfile
    pnpm fl -- judge-demo --rerun-all --export-only
    pnpm fl -- verify .faultline/bundles/judge-demo

This produces and verifies the deterministic judge bundle. For the live Git/Docker path, run:

    pnpm fl -- demo live-git --export-only

The submitter should record the exact commit, platform, command output, and any external proof root shown in the demo:

- Commit or release: [INSERT COMMIT SHA OR RELEASE]
- Platform and runtime: [INSERT OS, NODE VERSION, AND DOCKER VERSION IF USED]
- Recorded live-demo root: [INSERT EXTERNALLY RETAINED PROOF ROOT, OR OMIT]
- CI run URL and status: [INSERT URL, OR OMIT]

## Why it matters

FaultLine is intended for engineers responsible for diagnosing an agent-assisted regression under review, incident, or audit pressure. Its value proposition is not that it diagnoses every bug: it narrows one high-friction handoff by preserving an executable question, the replay facts, the proof boundary, and the limits of those facts.

Do not add adoption, time-saved, incident-rate, customer, or accuracy claims unless they are supported by a consented record. Use [the impact-validation template](impact-validation-template.md) to collect that evidence. Current validated evidence:

- [INSERT LINK OR SUMMARY OF A CONSENTED INTERVIEW OR INCIDENT; OTHERWISE WRITE NOT YET COLLECTED]
- [INSERT OBSERVED BEFORE/AFTER FACT WITH MEASUREMENT METHOD; OTHERWISE WRITE NOT YET MEASURED]

## How FaultLine differs

FaultLine composes a reviewed executable witness, repeated isolated Git-state replays, a portable offline-verifiable proof package, and explicit evidence boundaries. It is not a replacement for Git bisect, CI artifacts, a normal repro case, or provenance tooling; it uses or complements them. See [the concise scope comparison](differentiation.md) for the exact distinction and the claims that still need validation.

## Submission evidence to complete before publishing

- Track: Developer Tools
- Public narrated demo video URL: [INSERT URL]
- Qualifying Codex feedback session ID: [INSERT SESSION ID]
- Repository URL and license: [INSERT URL AND LICENSE]
- Human-reviewed final project description: [CONFIRM COMPLETED]
- Optional signed-CI evidence, if shown: [INSERT ATTESTATION BUNDLE AND TRUST-CONFIGURATION REFERENCE, OR OMIT]

The submitter must verify the current event rules, required fields, access requirements, and deadlines immediately before submission. This draft deliberately does not invent missing links, results, metrics, user quotes, or submission status.
