# External case study template (permissioned)

Use this after an external developer runs FaultLine on **their** repository (e.g. MUMBCS). Do **not** publish until the owner explicitly approves each field marked for release.

Partner runbook: [partners/mumbcs-faultline-guide.md](partners/mumbcs-faultline-guide.md).

## Consent

| Question | Answer |
| --- | --- |
| Owner / org | |
| Can the repository be **named** publicly? | yes / no / anonymized only |
| Approved quote (verbatim) | |
| May we publish digests + redacted summary? | yes / no |
| May we publish terminal captures? | yes / no |
| Date of consent | |

**Private repos:** publish only a redacted incident summary, terminal capture (scrubbed), proof-root digest, and named/anonymized quote. Do **not** publish source snapshots or witness overlay bytes without approval.

## Structured evidence fields

| Field | Value |
| --- | --- |
| Repository language(s) | |
| Approximate size (files / LOC / packages) | |
| Named publicly? | |
| Original symptom | |
| Setup time | |
| Assistance required (none / light / facilitated) | |
| Recorded turns or states | |
| Frozen-witness digest | `sha256:…` |
| Located transition | e.g. Turn 3 PASS→FAIL / commit `…` |
| Proof-package root digest | `sha256:…` |
| Evidence grade | `EXPERIMENTAL_TURN` / `COMMIT_PROOF` |
| Changed developer’s diagnosis? | yes / no — how |
| Time to locate the boundary | |
| Codex repair outcome | none / drafted / verified candidate / N/A |
| Rough edges / failures | |

## Onboarding metrics (product experience)

Ask the developer to attempt from the README with **minimal intervention**. Record:

| Metric | Value |
| --- | --- |
| Time to install | |
| Time to proof-ready (`doctor` + runtime + freeze) | |
| Commands before first useful result | |
| Where they became confused | |
| Witness creation understandable? | |
| Docker / runtime preparation blocked them? | |
| Did the final result change their next action? | |

### Strong vs weak testimony

**Weak:** “It worked well on my project.”

**Strong:** “I installed it without help, went from CI log / sidecar ledger to a verified boundary in N minutes, and it identified an edit I had blamed incorrectly.”

## Publish checklist

- [ ] Consent table complete  
- [ ] Digests match retained packages (`fl verify … --expect-root`)  
- [ ] Secrets / `.env` / private source removed  
- [ ] README “External validation” section updated  
- [ ] Impact note filled ([impact-validation-external-01.md](impact-validation-external-01.md))  
