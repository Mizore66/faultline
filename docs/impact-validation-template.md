# FaultLine impact-validation template

> Create one completed record for each real incident or interview. A blank template is not validation. Do not publish names, repository data, logs, quotes, or metrics without permission and an appropriate redaction review.

## Record metadata

- Record ID: [INSERT]
- Status: [planned | in progress | completed | declined | unusable]
- Date and timezone: [INSERT]
- Owner: [INSERT]
- Evidence retention location: [INSERT RESTRICTED LOCATION OR URL]
- Consent / sharing scope: [INSERT]
- Redaction review completed by: [INSERT OR NOT APPLICABLE]

## Audience and job to be done

- Participant role and organization type, generalized if necessary: [INSERT]
- Scenario: [agent-assisted regression | CI failure triage | code-review handoff | other]
- Triggering question in the participant's words: [INSERT]
- Current workflow and tools used: [INSERT]
- What decision needs confidence, and by when: [INSERT]
- Why existing evidence was insufficient: [INSERT]

## Incident replay record

Use this section only when a real incident may be reproduced with authorization.

- Repository / environment reference, redacted as needed: [INSERT]
- Known-good and known-bad state references: [INSERT]
- Reviewed executable witness: [INSERT OR LINK]
- How the witness was approved and frozen: [INSERT]
- Replay environment and exact command: [INSERT]
- Result classification: [PASS -> FAIL boundary | FAIL -> PASS boundary | unstable | inconclusive | not run]
- Externally retained proof root, if one exists: [INSERT OR NOT PRODUCED]
- What FaultLine showed that the prior workflow did not: [INSERT FACT ONLY]
- What FaultLine did not answer: [INSERT]
- Independent verification attempted by: [INSERT OR NOT ATTEMPTED]
- Verification result and source record: [INSERT]

## Interview record

Use this section for a structured user interview. Capture facts, not a sales script.

- Interview format and duration: [INSERT]
- Participant's current process: [INSERT]
- Concrete recent example they described: [INSERT]
- Exact friction, delay, or risk described: [INSERT]
- Demonstrated FaultLine path: [INSERT]
- Participant response or quote (only with permission): [INSERT]
- Requested capability or objection: [INSERT]
- Would they try it again? Why or why not?: [INSERT]
- Counterevidence or disagreement: [INSERT]

## Measured outcomes

Record only measurements with a source and method. Leave a field empty or write NOT MEASURED when evidence does not exist.

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Time to produce a reviewed regression boundary | [INSERT / not measured] | [INSERT / not measured] | [INSERT] | [INSERT] | [INSERT] |
| Time for a second engineer to verify the package | [INSERT / not measured] | [INSERT / not measured] | [INSERT] | [INSERT] | [INSERT] |
| Number of reruns or handoffs avoided | [INSERT / not measured] | [INSERT / not measured] | [INSERT] | [INSERT] | [INSERT] |
| Other outcome selected by the participant | [INSERT / not measured] | [INSERT / not measured] | [INSERT] | [INSERT] | [INSERT] |

## Decision and follow-up

- Evidence that supports a useful problem/solution fit: [INSERT]
- Evidence that weakens or contradicts the hypothesis: [INSERT]
- Product change this record suggests: [INSERT]
- Product change explicitly not justified by this record: [INSERT]
- Follow-up owner and date: [INSERT]
- Publishable, anonymized summary approved? [yes | no | pending]

## Integrity checklist

- [ ] The participant or incident is real, and the relationship is described accurately.
- [ ] Every metric has a method and source.
- [ ] Quotes are attributed only with consent.
- [ ] Sensitive source, incident, and identity data are redacted or retained privately.
- [ ] The record distinguishes observed replay facts from inferred explanations.
- [ ] Any public Devpost or README claim links to a reviewed, publishable summary of this record.
