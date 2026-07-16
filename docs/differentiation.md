# FaultLine differentiation and scope comparison

FaultLine is positioned as an evidence layer for a reviewed regression question. This is a scope comparison, not a claim that FaultLine replaces established tools or that no other product offers overlapping capabilities. Validate any market, novelty, adoption, or outcome claim with real user evidence before publishing it.

| Tool or practice | Useful primary job | What it normally supplies | FaultLine's bounded addition | What FaultLine does not replace |
| --- | --- | --- | --- | --- |
| Git bisect | Search a revision range for a change that satisfies a test condition | A search process and a selected revision boundary | A frozen, reviewable witness; recorded repeated execution facts; and a portable package another engineer can verify offline | Git history, a good test predicate, or general revision-search workflows |
| CI artifacts and logs | Preserve output from a particular build or job | Build logs, test output, files, and job metadata | Cross-state replay of the same frozen witness with explicit stability and proof-boundary rules | CI execution, retention policy, dashboards, or incident ownership |
| A reproduction case | Describe or encode a failure someone can run | A test, script, fixture, or instructions | Review/freeze records, isolated multi-state replay facts, and a verifier for the resulting evidence package | Designing a meaningful repro, debugging, or determining semantic root cause |
| Provenance and attestation tools | Bind a build artifact or subject to a signing identity and policy | Signed artifact or workflow provenance | A predicate-specific regression proof package that can be the subject of provenance | Identity, authorship, build provenance, or host/Docker enforcement guarantees |
| Model or agent observability | Observe model/agent interactions and lifecycle data | Traces, prompts, events, or audit records | Optional binding of observed lifecycle checkpoints to the Git replay evidence | Private model reasoning, causal claims about an agent, or native interception that was not observed |

## The concrete composition

FaultLine combines four deliberately narrow pieces:

1. A human-reviewed, immutable executable witness.
2. Replay of that exact witness over recorded Git states, with a stated stability rule.
3. A portable proof package plus an offline semantic verifier that does not execute repository code.
4. Evidence labels and scope limits that separate executed facts, deterministic derivations, inferences, and unknowns.

Optional reviewer signatures and GitHub CI attestations strengthen specific identity or CI-byte claims when their retained key material, trust policy, and signing artifacts are available. They do not turn a local run into host attestation or prove a unique root cause.

## Claims to keep precise

Safe, implementation-backed language:

- "FaultLine helps a reviewer inspect where a frozen witness first becomes bad across the supplied Git states."
- "FaultLine packages the stored replay facts for offline verification without rerunning repository code."
- "FaultLine complements Git bisect, CI artifacts, and repro cases; it does not replace them."

Claims that require external validation before use:

- "Teams resolve regressions faster."
- "FaultLine reduces incidents, costs, or handoffs."
- "FaultLine is the first, only, or best tool of its kind."
- "A proof package establishes semantic causality, model intent, or general build provenance."

Use [the impact-validation template](impact-validation-template.md) to turn a real incident or interview into a reviewable, consented basis for any outcome claim.
