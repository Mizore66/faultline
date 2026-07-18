# Impact validation record — FaultLine self-incident (dogfood)

> Completed dogfood record only. Not third-party adoption. Do not invent time-saved or customer metrics from this file.

## Record metadata

- Record ID: `faultline-self-incident-2026-07-17`
- Status: completed
- Date and timezone: 2026-07-17 (UTC dates as recorded in [faultline-self-incident.md](faultline-self-incident.md))
- Owner: FaultLine maintainers (dogfood on own repository)
- Evidence retention location: documented root + runbook in-repo; portable package retained outside git as required by proof protocol
- Consent / sharing scope: public dogfood on https://github.com/Mizore66/faultline
- Redaction review completed by: N/A (own public repository)

## Audience and job to be done

- Participant role and organization type: maintainers of an agent-assisted developer tool (self)
- Scenario: CI failure triage / agent-assisted regression
- Triggering question: When the provenance workflow’s `tee` step fails with “No such file or directory,” which recorded Git state first fails the exact directory-before-tee predicate?
- Current workflow and tools used: Git history, GitHub Actions logs, manual reading of `.github/workflows/verify.yml`
- What decision needs confidence, and by when: whether a frozen workflow predicate first becomes bad at the provenance-job introduction, under review pressure
- Why existing evidence was insufficient: CI logs and commits alone did not produce a portable, offline-verifiable package of repeated isolated executions of one human-frozen witness

## Incident replay record

- Repository / environment reference: https://github.com/Mizore66/faultline
- Known-good and known-bad state references: good `e8e30649` → bad `97c3290e` (and later `5546831c`); recovery `07ee7f11`
- Reviewed executable witness: frozen digest `sha256:985c0e48258d9a219dd4dc5bb4d377fcc63a283ecb1c4447a13640cd8898d8fd` (see self-incident runbook)
- How the witness was approved and frozen: human Approve then Freeze via local witness review (not auto-approved)
- Replay environment and exact command: digest-pinned native Docker; image `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2`; three executions per state
- Result classification: PASS -> FAIL boundary at `97c3290e`; FAIL -> PASS boundary at `07ee7f11`
- Externally retained proof root: `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`
- What FaultLine showed that the prior workflow did not: a portable package another engineer can verify offline without re-running repository code, with explicit stability and proof boundary
- What FaultLine did not answer: model intent, unique semantic root cause, first overall CI failure attribution, or third-party adoption
- Independent verification attempted by: FaultLine offline verify against retained root (21 declared files checked per runbook)
- Verification result and source record: VALID when given the recorded root — [faultline-self-incident.md](faultline-self-incident.md)

## Interview record

- Not applicable (dogfood incident, not an external interview)

## Measured outcomes

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Time to produce a reviewed regression boundary | not measured | not measured | — | — | No stopwatch study |
| Time for a second engineer to verify the package | not measured | offline verify succeeded against retained root | `fl verify` with `--expect-root` | self-incident runbook | Same-team dogfood |
| Number of reruns or handoffs avoided | not measured | not measured | — | — | Not claimed |
| Portable offline-verifiable predicate answer | unavailable from CI logs alone | produced and verified | Docker-isolated 3× replay + proof package | root `sha256:f6a391b3…` | Own-repo only |

## Decision and follow-up

- Evidence that supports a useful problem/solution fit: for this exact workflow predicate, FaultLine produced a handoff-ready proof package CI logs did not
- Evidence that weakens or contradicts the hypothesis: single-repo dogfood; no external participant; no time-saved metric
- Product change this record suggests: keep `COMMIT_PROOF` as the shippable path; keep honesty labels
- Product change explicitly not justified by this record: market-wide adoption claims, “faster incidents,” or replacing bisect/CI
- Follow-up owner and date: submitter — obtain one consented external record via [impact-validation-template.md](impact-validation-template.md) before expanding Impact claims
- Publishable, anonymized summary approved? yes (public dogfood)

## Integrity checklist

- [x] The participant or incident is real, and the relationship is described accurately (self).
- [x] Every metric has a method and source (or is marked not measured).
- [x] Quotes are attributed only with consent (none used).
- [x] Sensitive source, incident, and identity data are redacted or retained privately.
- [x] The record distinguishes observed replay facts from inferred explanations.
- [x] Public Devpost/README dogfood claims may link to this record; third-party impact remains NOT COLLECTED.
