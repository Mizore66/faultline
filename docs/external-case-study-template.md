# External case study template (permissioned)

Use this after an external developer runs FaultLine on **their** repository (e.g. MUMBCS). Do **not** publish until the owner explicitly approves each field marked for release.

Partner runbook: [partners/mumbcs-faultline-guide.md](partners/mumbcs-faultline-guide.md).

**Framing:** a scripted Baseline→T1/T2 PASS→T3 FAIL session is **protocol / interoperability validation** (install, hooks, freeze, Docker replay, offline verify, onboarding). Do **not** title it “FaultLine caught a real production bug” unless the failure was naturally occurring.

**Witness integrity (mandatory):** frozen overlay bytes identical on every state; Codex mutated production only; no shadow duplicate of production logic in the witness.

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
| Setup / installation time | |
| Assistance required (none / light / facilitated) | |
| Ecosystem + approximate size | language(s), packages, files/LOC |
| Snapshot overhead observed | cold / warm / one-file edit (qualitative or ms) |
| Recorded turns or states | |
| Frozen-witness digest | `sha256:…` |
| Overlay path (immutable) | e.g. `.faultline-witness/check-slug.mjs` |
| Production module exercised | e.g. `src/utils/slug.ts` (not a shadow copy) |
| Overlay hash stable through Turn 3? | yes / no |
| Exact stable transition found | e.g. Turn 3 PASS→FAIL (adjacent compatible) |
| Offline verification succeeded? | yes / no — command + root digest |
| Minimization succeeded? | yes / no / not attempted |
| Proof-package root digest | `sha256:…` |
| Partner independently verified root? | yes / no |
| Evidence grade | `EXPERIMENTAL_TURN` / `COMMIT_PROOF` |
| Organic incident or scripted protocol run? | |
| Unexpected failure / rough edges | |
| Changed developer’s diagnosis? | yes / no — how |
| Time from freeze → verified boundary | |
| Would use on a naturally occurring failure? | yes / no |
| Codex repair outcome | none / drafted / verified candidate / N/A |
| Approved quote (critical or mixed preferred) | |
| One criticism (required) | |

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

## Publish readiness path

The MUMBCS N=1 permissioned case is already landed:
[external-case-study-mumbcs.md](external-case-study-mumbcs.md) /
[publishable-summary-mumbcs.md](publishable-summary-mumbcs.md) /
[impact-validation-external-01.md](impact-validation-external-01.md).

Use this blank template for any **future** partner run. Do not mark README or Devpost
as completed for a new case until the checklist below is green with retained digests
and explicit consent.

| Step | Artifact | Owner |
| --- | --- | --- |
| 1. Run partner session | [partners/mumbcs-faultline-guide.md](partners/mumbcs-faultline-guide.md) (or equivalent invite) | Facilitator + partner |
| 2. Capture structured fields | This template (tables above) | Facilitator |
| 3. Fill impact record | [impact-validation-external-01.md](impact-validation-external-01.md) — set `Status: completed` only when filled | Facilitator |
| 4. Offline verify | `fl verify <bundle> --expect-root <digest>` MATCH | Partner or facilitator |
| 5. Redaction review | Private sources, overlays, CI logs scrubbed | Partner + facilitator |
| 6. Public wording | README Validation section: “external underway” → permissioned summary link | Maintainer |

**Not a substitute:** [samples/later-turn-ledger/](samples/later-turn-ledger/) is a
`RECORDED_REDACTED_FIXTURE` for CI drills. It must never be published as the MUMBCS
or external-01 case study.

## Publish checklist

- [ ] Consent table complete  
- [ ] Digests match retained packages (`fl verify … --expect-root`)  
- [ ] Secrets / `.env` / private source removed  
- [ ] README Validation section updated only after consent + digests land  
- [ ] Impact note filled ([impact-validation-external-01.md](impact-validation-external-01.md)) and status flipped from `planned` → `completed`  
- [ ] One criticism + one approved quote captured (not generic praise)  

