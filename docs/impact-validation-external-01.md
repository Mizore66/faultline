# Impact validation record - external N=1

Blank or "not measured" fields mean the value was not collected. Do not infer time savings or scale from this record.

Filled case study: [external-case-study-mumbcs.md](external-case-study-mumbcs.md). Publishable summary: [publishable-summary-mumbcs.md](publishable-summary-mumbcs.md). Partner handoff mirror: [partners/mumbcs-later-turn-handoff/](partners/mumbcs-later-turn-handoff/).

## Related recorded fixture (not this case)

CI still uses the carefully redacted recorded multi-turn ledger at [samples/later-turn-ledger/](samples/later-turn-ledger/) (`RECORDED_REDACTED_FIXTURE`). That sample is **not** the MUMBCS external-01 case study.

## Record metadata

- Record ID: `external-01`
- Status: completed
- Date and timezone: 2026-07-19 Asia/Singapore
- Owner: FaultLine submitter / facilitator
- Evidence retention location: in-repo partner handoff mirror `docs/partners/mumbcs-later-turn-handoff/` (summary + digests); sidecar ledger excerpt `docs/samples/mumbcs-sidecar-ledger/`; full turn package remains private in MUMBCS (Path B). Local corroboration 2026-07-19: `fl verify` on the retained partner package → Integrity **VALID**, External root **MATCH**, root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`.
- Consent / sharing scope: MUMBCS may be named; digests and redacted summary published in FaultLine; screenshots/terminal captures consent-approved for external use but not in-tree here
- Re-verification path: Path B — byte-level re-verify requires MUMBCS collaborator access or private redacted handoff; public readers use in-repo digests + consent + summary
- Redaction review completed by: MUMBCS participant consent in Codex thread, 2026-07-19

## Audience and job to be done

- Participant role and organization type, generalized if necessary: Independent developer / maintainer-adjacent participant for a university club website repository
- Scenario: agent-assisted regression / Codex later-turn protocol validation
- Triggering question in the participant's words: "Help me run and test out the live ui demo of faultline with this project" and later consent to publish MUMBCS evidence
- Current workflow and tools used: Codex, Git, Node 22.18.0, pnpm, Docker Desktop, FaultLine CLI, MUMBCS TypeScript / Next.js repository
- What decision needs confidence, and by when: whether the MUMBCS run can support FaultLine external validation / hackathon submission evidence
- Why existing evidence was insufficient: visual demo alone would not prove a frozen witness, Docker-isolated replay, retained root digest, or located turn transition

## Incident replay record

- Repository / environment reference, redacted as needed: `monashblockchain/MUMBCS`, private repository; branch `faultline/later-turn-demo`
- Known-good and known-bad state references: Turn 2 stable PASS state -> Turn 3 stable FAIL state; historic commits `d79adf9` -> `261b93d`
- Reviewed executable witness: frozen witness digest `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133`
- How the witness was approved and frozen: human approval then freeze; `frozen.json` records `reviewerType: HUMAN` and `approvedAt: 2026-07-19T07:55:40.044Z`
- Replay environment and exact command: Docker-isolated `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2`; witness command `node --experimental-strip-types .faultline-witness/check-slug.mjs`
- Result classification: PASS -> FAIL boundary
- Externally retained proof root, if one exists: `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`
- What FaultLine showed that the prior workflow did not: a retained, offline-verifiable package with frozen witness digest, Docker-isolated run evidence, and Turn 3 attribution for the first stable PASS->FAIL transition
- What FaultLine did not answer: it did not prove agent intent, unique semantic root cause, prevention, or that the failure was naturally occurring in production
- Independent verification attempted by: MUMBCS local participant / facilitator
- Verification result and source record: `valid: true`, `externalRootStatus: MATCH` from `node tools/faultline/verify-turn-bundle.mjs .faultline/turn-proof-bundles/turns-1784447895435 sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` (run in MUMBCS workspace; not reproducible from FaultLine alone)

## Interview record

- Interview format and duration: Codex thread collaboration; duration not measured
- Participant's current process: not collected
- Concrete recent example they described: hackathon submission evidence needs for FaultLine on MUMBCS
- Exact friction, delay, or risk described: needed native-looking UI evidence and clarity on what evidence is admissible without inventing testimony
- Demonstrated FaultLine path: doctor, runtime resolve, sidecar status, frozen witness verification, turn investigation excerpt, bundle verification, native Codex UI replay
- Participant response or quote (only with permission): "MUMBCS can be named, the screenshots can be published, all consent is given."
- Requested capability or objection: wanted native live Codex UI capture rather than only scripted sidecar-hook capture
- Would they try it again? Why or why not?: not collected
- Counterevidence or disagreement: Native delegated Codex worktree replay did not create a fresh sidecar ledger; verified proof package remains the scripted sidecar-hook capture

## Measured outcomes

| Outcome | Baseline | Observed result | Measurement method | Source / artifact | Limitations |
| --- | --- | --- | --- | --- | --- |
| Time to produce a reviewed regression boundary | not measured | not measured | - | - | Do not invent |
| Time for a second engineer to verify the package | not measured | not measured | - | - | Do not invent |
| Number of reruns or handoffs avoided | not measured | not measured | - | - | Do not invent |
| Proof package verification | no external package retained | `valid: true`, `externalRootStatus: MATCH` | verifier command | `.faultline/turn-proof-bundles/turns-1784447895435/` | verifies package integrity; does not prove organic bug |
| Located transition | no turn attribution | Turn 3 `PASS_TO_FAIL`, `ATTRIBUTED` | investigation JSON | `.faultline/turn-proof-bundles/turns-1784447895435/investigation.json` | `EXPERIMENTAL_TURN`, not `COMMIT_PROOF` |

## Decision and follow-up

- Evidence that supports a useful problem/solution fit: the MUMBCS package retains a human-frozen witness, Docker-isolated replay evidence, a root digest that verifies offline, and an attributed later-turn PASS->FAIL transition
- Evidence that weakens or contradicts the hypothesis: native Codex delegated worktree replay did not produce a fresh sidecar ledger; the proof ledger is from scripted sidecar-hook capture
- Product change this record suggests: document sidecar expected behavior for Codex delegated worktrees and mark UI replay demo-only when no ledger is written (docs-only this cycle; see [codex-sidecar.md](codex-sidecar.md#delegated-worktrees--demo-only-when-no-ledger))
- Product change explicitly not justified by this record: claiming prevention, agent intent, unique semantic root cause, or a naturally occurring production bug
- Follow-up owner and date: FaultLine submitter, 2026-07-19
- Publishable, anonymized summary approved? yes; named MUMBCS summary approved

## Publishable summary

In one consented external protocol validation on `monashblockchain/MUMBCS`, FaultLine froze a human-reviewed witness over production slug validation and replayed recorded turn states in Docker. The retained package verifies offline with root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` and attributes the first stable PASS->FAIL transition to Turn 3 under `EXPERIMENTAL_TURN` evidence. This validates the external workflow of freeze, replay, locate, and verify; it does not claim FaultLine caught a naturally occurring production bug, agent intent, unique root cause, or prevention.

## Integrity checklist

- [x] The participant is external to FaultLine dogfood and the relationship is described accurately.
- [x] Every metric has a method and source, or is marked not measured.
- [x] Quotes are attributed only with consent.
- [x] Sensitive source, incident, and identity data are redacted or retained privately.
- [x] The record distinguishes observed replay facts from inferred explanations.
- [x] Devpost summary matches this record and does not invent scale.
