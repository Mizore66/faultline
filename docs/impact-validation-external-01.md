# Impact validation record — external N=1

> Fill during/after the consented external session. Blank fields mean “not collected.” Do not invent metrics. This is **not** the dogfood self-incident ([impact-validation-self-incident.md](impact-validation-self-incident.md)).

## Related recorded fixture (not a live external case)

Until a consented external session lands, CI and docs use the carefully redacted
**recorded** multi-turn ledger at [samples/later-turn-ledger/](samples/later-turn-ledger/).
That sample is produced by `scripts/generate-later-turn-ledger-fixture.mjs`, is
honestly labeled `RECORDED_REDACTED_FIXTURE`, and must not be described as organic
dogfood or as this external-01 impact case.

## Record metadata

- Record ID: `external-01`
- Status: planned
- Date and timezone: [INSERT]
- Owner: [INSERT — FaultLine submitter]
- Evidence retention location: [INSERT RESTRICTED LOCATION OR OMIT]
- Consent / sharing scope: [INSERT — e.g. “redacted Devpost summary OK; no employer name”]
- Redaction review completed by: [INSERT]

## Audience and job to be done

- Participant role and organization type, generalized if necessary: [INSERT]
- Scenario: [agent-assisted regression | CI failure triage | code-review handoff | other]
- Triggering question in the participant's words: [INSERT]
- Current workflow and tools used: [INSERT]
- What decision needs confidence, and by when: [INSERT]
- Why existing evidence was insufficient: [INSERT]

## Incident replay record

- Repository / environment reference, redacted as needed: [INSERT]
- Known-good and known-bad state references: [INSERT OR NOT ESTABLISHED]
- Reviewed executable witness: [INSERT DIGEST OR LINK]
- How the witness was approved and frozen: [INSERT — human Approve then Freeze]
- Replay environment and exact command: [INSERT]
- Result classification: [PASS -> FAIL boundary | FAIL -> PASS boundary | unstable | inconclusive | freeze-only / not run]
- Externally retained proof root, if one exists: [INSERT OR NOT PRODUCED]
- What FaultLine showed that the prior workflow did not: [INSERT FACT ONLY]
- What FaultLine did not answer: [INSERT]
- Independent verification attempted by: [INSERT OR NOT ATTEMPTED]
- Verification result and source record: [INSERT]

## Interview record

- Interview format and duration: [INSERT]
- Participant's current process: [INSERT]
- Concrete recent example they described: [INSERT]
- Exact friction, delay, or risk described: [INSERT]
- Demonstrated FaultLine path: [INSERT]
- Participant response or quote (only with permission): [INSERT OR OMIT]
- Requested capability or objection: [INSERT]
- Would they try it again? Why or why not?: [INSERT]
- Counterevidence or disagreement: [INSERT]

## Measured outcomes

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Time to produce a reviewed regression boundary | not measured | not measured | — | — | Do not invent |
| Time for a second engineer to verify the package | not measured | not measured | — | — | Do not invent |
| Number of reruns or handoffs avoided | not measured | not measured | — | — | Do not invent |
| Other outcome selected by the participant | not measured | not measured | — | — | Do not invent |

## Decision and follow-up

- Evidence that supports a useful problem/solution fit: [INSERT]
- Evidence that weakens or contradicts the hypothesis: [INSERT]
- Product change this record suggests: [INSERT]
- Product change explicitly not justified by this record: [INSERT]
- Follow-up owner and date: [INSERT]
- Publishable, anonymized summary approved? [yes | no | pending]

## Publishable summary (paste into Devpost after approval)

> [INSERT 3–5 sentences. Example shape — replace with real facts:]  
> In one consented external triage (role: &lt;generalized&gt;), FaultLine was used to &lt;freeze predicate / localize / verify&gt;. Prior workflow (&lt;tools&gt;) did not provide &lt;portable offline package / frozen witness / …&gt;. FaultLine showed &lt;fact&gt; and did not claim &lt;intent / unique cause&gt;. N=1; no time-saved metric claimed.

## Integrity checklist

- [ ] The participant is external (not FaultLine dogfood) and the relationship is described accurately.
- [ ] Every metric has a method and source, or is marked not measured.
- [ ] Quotes are attributed only with consent.
- [ ] Sensitive source, incident, and identity data are redacted or retained privately.
- [ ] The record distinguishes observed replay facts from inferred explanations.
- [ ] Devpost summary matches this record and does not invent scale.
