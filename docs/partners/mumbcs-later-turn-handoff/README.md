# MUMBCS x FaultLine production-code regression handoff

FaultLine caught and localized a production-code regression in MUMBCS slug validation during a consented agent-assisted change sequence. The retained package verifies offline and attributes the first stable PASS->FAIL transition to Turn 3.

## Session summary

- **Repo:** monashblockchain/MUMBCS (private repo, name approved for publication)
- **Branch:** `faultline/later-turn-demo`
- **Branch tip:** `261b93d` (after Turn 3 regression)
- **Session:** Codex sidecar hooks captured the change sequence
- **Story:** baseline PASS -> turns 1-2 PASS -> Turn 3 changes production code -> FAIL
- **Regression:** `src/lib/slugify.ts` accepted an empty slug after Turn 3
- **introduction.turnOrdinal:** 3
- **Evidence grade:** `EXPERIMENTAL_TURN`

## Digests

| Field | Value |
| --- | --- |
| Frozen witness | `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133` |
| Witness overlay (stable) | `sha256:6a22d755f3cba45149eb4caf7a41cc7260112887c2b67b813a4d7a4760f0ece0` |
| Turn package root | `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` |
| Docker image | `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2` |
| Proposal id | `mumbcs-check-slug` |

## Bundle location

Full verify-able turn package:

```text
.faultline/turn-proof-bundles/turns-1784447895435/
```

## Re-verify

```bash
cd /path/to/MUMBCS
node tools/faultline/verify-turn-bundle.mjs \
  .faultline/turn-proof-bundles/turns-1784447895435 \
  sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1
```

Expected result: `valid: true`, `externalRootStatus: MATCH`.

## Claim boundary

FaultLine froze a human-reviewed witness importing real production code from `src/lib/slugify.ts` (`isValidSlug`) and replayed the captured turn states in Docker. The verified claim is that FaultLine caught the MUMBCS production-code regression and localized the first stable PASS->FAIL boundary to Turn 3. Do not extend this to agent intent, prevention, or deployed live-site impact without separate evidence.

## Consent

MUMBCS can be named. Screenshots, terminal captures, retained digests, and the summary may be published. Avoid publishing secrets, `.env` values, private service credentials, or unrelated private repository material.
