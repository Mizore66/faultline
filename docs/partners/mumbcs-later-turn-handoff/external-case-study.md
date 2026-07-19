# External case study - FaultLine caught a MUMBCS production-code regression

Consent for this record was provided in the Codex thread on 2026-07-19: MUMBCS can be named, screenshots can be published, and the retained evidence/digests may be published.

Framing: FaultLine caught and localized a regression in real MUMBCS production code during an agent-assisted change sequence. The affected contract was `isValidSlug` in `src/lib/slugify.ts`; the frozen witness stayed unchanged while the production function was changed on Turn 3. This is publishable as a production-code regression caught by FaultLine, with the precise caveat that the retained evidence does not separately prove the regression reached the deployed live website.

Witness integrity: the frozen overlay bytes stayed identical across the replayed turn states. The witness imports production code from `src/lib/slugify.ts`; Turn 3 changed production code only.

## Consent

| Question | Answer |
| --- | --- |
| Owner / org | monashblockchain / MUMBCS |
| Can the repository be named publicly? | yes |
| Approved quote (verbatim) | "MUMBCS can be named, the screenshots can be published, all consent is given." |
| May we publish digests + summary? | yes |
| May we publish terminal captures? | yes |
| May we publish screenshots? | yes |
| Date of consent | 2026-07-19 Asia/Singapore |

Publication scope: repo name, screenshots, terminal captures, proof digests, proof-root digest, summary, and the direct consent quote above may be published. Avoid publishing secrets, `.env` values, private service credentials, or unrelated private repository material.

## Structured evidence fields

| Field | Value |
| --- | --- |
| Repository language(s) | TypeScript, TSX, JavaScript, Markdown; Next.js 15 + Payload CMS |
| Approximate size (files / LOC / packages) | 210 tracked files; LOC not measured; package name `mumbcs-web` |
| Named publicly? | yes: monashblockchain/MUMBCS |
| Original symptom | A Codex-assisted change introduced a regression in MUMBCS production slug validation: `isValidSlug("")` became accepted when the frozen production contract required it to be rejected. |
| Setup / installation time | not measured |
| Assistance required (none / light / facilitated) | facilitated |
| Ecosystem + approximate size | TypeScript / Next.js / Payload CMS; 210 tracked files; pnpm package with Next, Payload, Playwright |
| Snapshot overhead observed | not measured |
| Recorded turns or states | 4 stable states: session baseline, Turn 1, Turn 2, Turn 3 |
| Frozen-witness digest | `sha256:3c7ebd021525de79ed7a70d3893a4f4585a47c10b44096843b2c313f7ffef133` |
| Overlay path (immutable) | `.faultline-witness/check-slug.mjs` |
| Production module exercised | `src/lib/slugify.ts` (`isValidSlug`) |
| Overlay hash stable through Turn 3? | yes: `sha256:6a22d755f3cba45149eb4caf7a41cc7260112887c2b67b813a4d7a4760f0ece0` |
| Exact stable transition found | Turn 2 PASS -> Turn 3 FAIL; `introduction.status = ATTRIBUTED`; `introduction.turnOrdinal = 3`; transition kind `PASS_TO_FAIL` |
| Offline verification succeeded? | yes: `valid: true`, `externalRootStatus: MATCH` |
| Verification command | `node tools/faultline/verify-turn-bundle.mjs .faultline/turn-proof-bundles/turns-1784447895435 sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` |
| Minimization succeeded? | not attempted / not retained |
| Proof-package root digest | `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1` |
| Partner independently verified root? | yes, verified locally in the MUMBCS workspace |
| Evidence grade | `EXPERIMENTAL_TURN` |
| Incident type | intentional production-code regression during a consented capture session; deployment impact not claimed |
| Unexpected failure / rough edges | The native delegated Codex UI replay showed live Turn 1/2/3 commits, but the sidecar did not create a fresh native ledger for that delegated worktree. The verified proof ledger is from sidecar-hook capture. |
| Changed developer's diagnosis? | yes: the retained evidence localized the first stable PASS->FAIL boundary to Turn 3 rather than leaving the failure as a vague end-state regression |
| Time from freeze -> verified boundary | not measured |
| Would use on a naturally occurring failure? | not collected |
| Codex repair outcome | N/A; repair was not part of this record |
| Approved quote (critical or mixed preferred) | "MUMBCS can be named, the screenshots can be published, all consent is given." |
| One criticism (required) | Native Codex UI task replay worked visually, but sidecar recording did not produce a fresh native ledger in the delegated worktree; hook/worktree behavior needs clearer support or operator guidance. |

## Onboarding metrics (product experience)

| Metric | Value |
| --- | --- |
| Time to install | not measured |
| Time to proof-ready (`doctor` + runtime + freeze) | not measured |
| Commands before first useful result | not measured |
| Where they became confused | native Codex delegated worktree sidecar behavior; the UI replay occurred but no new sidecar ledger appeared |
| Witness creation understandable? | partially; the final witness imported real production code and remained immutable, but the session required facilitation |
| Docker / runtime preparation blocked them? | no; Docker CLI/daemon were READY and runtime resolved to the expected pinned Node image |
| Did the final result change their next action? | yes: use the retained proof package to show that FaultLine caught the MUMBCS production-code regression at Turn 3, and use the native Codex UI replay as supporting live demonstration evidence |

## Publish checklist

- [x] Consent table complete
- [x] Digests match retained package verification
- [x] Secrets / `.env` / private credentials excluded from this summary
- [x] Impact note filled in `impact-validation-external-01.md`
- [x] Case framed as a production-code regression caught during a consented capture session, without overstating deployed live-site impact
- [ ] Publish summary / Devpost update
