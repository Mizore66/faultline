# Impact validation record - external N=1

Blank or "not measured" fields mean the value was not collected. Do not infer time savings or scale from this record.

## Record metadata

- Record ID: `external-01`
- Status: completed
- Date and timezone: 2026-07-19 Asia/Singapore
- Owner: FaultLine submitter / facilitator
- Evidence retention location: `mumbcs-later-turn-handoff/` and `.faultline/turn-proof-bundles/turns-1784447895435/`
- Consent / sharing scope: MUMBCS may be named; screenshots, terminal captures, digests, and summary may be published
- Redaction review completed by: MUMBCS participant consent in Codex thread, 2026-07-19

## Audience and job to be done

- Participant role and organization type, generalized if necessary: Independent developer / maintainer-adjacent participant for a university club website repository
- Scenario: agent-assisted production-code regression localized by FaultLine
- Triggering question in the participant's words: demonstrate and publish evidence that FaultLine caught the MUMBCS regression and identified the first bad turn
- Current workflow and tools used: Codex, Git, Node 22.18.0, pnpm, Docker Desktop, FaultLine CLI, MUMBCS TypeScript / Next.js repository
- What decision needs confidence, and by when: whether the MUMBCS evidence can support a public hackathon submission showing FaultLine caught a production-code regression
- Why existing evidence was insufficient: screenshots alone would not prove that the regression crossed a frozen witness boundary, replayed in Docker, retained a root digest, and localized to a specific turn

## Incident replay record

- Repository / environment reference, redacted as needed: `monashblockchain/MUMBCS`, private repository; branch `faultline/later-turn-demo`
- Known-good and known-bad state references: Turn 2 stable PASS state -> Turn 3 stable FAIL state; historic commits `d79adf9` -> `261b93d`
- Production code involved: `src/lib/slugify.ts`, `isValidSlug`
- Regression caught: after Turn 3, `isValidSlug("")` incorrectly returned true
- Reviewed executable witness: frozen witness digest `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133`
- How the witness was approved and frozen: human approval then freeze; `frozen.json` records `reviewerType: HUMAN` and `approvedAt: 2026-07-19T07:55:40.044Z`
- Replay environment and exact command: Docker-isolated `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2`; witness command `node --experimental-strip-types .faultline-witness/check-slug.mjs`
- Result classification: PASS -> FAIL boundary
- Externally retained proof root, if one exists: `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`
- What FaultLine showed that the prior workflow did not: the MUMBCS production-code behavior stayed PASS through Turn 2 and flipped to FAIL at Turn 3 under the same frozen witness
- What FaultLine did not answer: it did not prove agent intent, prevention, or that the regression reached the deployed live website
- Independent verification attempted by: MUMBCS local participant / facilitator
- Verification result and source record: `valid: true`, `externalRootStatus: MATCH` from `node tools/faultline/verify-turn-bundle.mjs .faultline/turn-proof-bundles/turns-1784447895435 sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`

## Interview record

- Interview format and duration: Codex thread collaboration; duration not measured
- Participant's current process: not collected
- Concrete recent example they described: a MUMBCS production helper regression in `src/lib/slugify.ts`, where empty slugs became accepted after a later Codex turn
- Exact friction, delay, or risk described: needed evidence strong enough to say FaultLine caught the regression, while avoiding unsupported claims about live-site outage, agent intent, or prevention
- Demonstrated FaultLine path: doctor, runtime resolve, sidecar status, frozen witness replay, Turn 3 attribution, bundle verification, native Codex UI replay
- Participant response or quote (only with permission): "MUMBCS can be named, the screenshots can be published, all consent is given."
- Requested capability or objection: wanted native live Codex UI capture rather than only scripted sidecar-hook capture
- Would they try it again? Why or why not?: not collected
- Counterevidence or disagreement: Native delegated Codex worktree replay did not create a fresh sidecar ledger; verified proof package remains the sidecar-hook capture

## Measured outcomes

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Time to produce a reviewed regression boundary | not measured | not measured | - | - | Do not invent |
| Time for a second engineer to verify the package | not measured | not measured | - | - | Do not invent |
| Number of reruns or handoffs avoided | not measured | not measured | - | - | Do not invent |
| Proof package verification | no external package retained | `valid: true`, `externalRootStatus: MATCH` | verifier command | `.faultline/turn-proof-bundles/turns-1784447895435/` | verifies package integrity and replay evidence; does not prove deployment impact |
| Located transition | no turn attribution | Turn 3 `PASS_TO_FAIL`, `ATTRIBUTED` | investigation JSON | `.faultline/turn-proof-bundles/turns-1784447895435/investigation.json` | `EXPERIMENTAL_TURN`, not `COMMIT_PROOF` |

## Decision and follow-up

- Evidence that supports a useful problem/solution fit: FaultLine caught a real MUMBCS production-code regression, retained a human-frozen witness, replayed the boundary in Docker, and attributed the first stable PASS->FAIL transition to Turn 3
- Evidence that weakens or contradicts the hypothesis: native Codex delegated worktree replay did not produce a fresh sidecar ledger; the proof ledger is from sidecar-hook capture
- Product change this record suggests: improve or document sidecar hook behavior for Codex delegated worktrees and make native-ledger capture status more obvious
- Product change explicitly not justified by this record: claiming prevention, agent intent, or deployed live-site impact
- Follow-up owner and date: FaultLine submitter, 2026-07-19
- Publishable, named MUMBCS summary approved? yes

## Publishable summary

In one consented external run on `monashblockchain/MUMBCS`, FaultLine caught a regression in production slug validation: the frozen witness stayed green through Turn 2 and failed after Turn 3 changed `src/lib/slugify.ts`. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This shows FaultLine can freeze expected production behavior, replay the agent-assisted change sequence, and name the first bad turn; it does not claim agent intent, prevention, or deployed live-site impact.

## Integrity checklist

- [x] The participant is external to FaultLine dogfood and the relationship is described accurately.
- [x] Every metric has a method and source, or is marked not measured.
- [x] Quotes are attributed only with consent.
- [x] Sensitive source, incident, and identity data are redacted or retained privately.
- [x] The record distinguishes observed replay facts from inferred explanations.
- [x] Devpost summary matches this record and does not invent scale.
