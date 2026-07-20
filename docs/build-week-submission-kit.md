# FaultLine Build Week submission kit

This is a recording and submission checklist, not evidence that a submission has already been made. Recheck the official Build Week page before submitting.

**Finish path (video + external N=1 + Devpost):** start at [submission-finish-runbook.md](submission-finish-runbook.md) — teleprompter, invite, external impact form, and paste-ready Devpost text.

## Idea claim (say this once, then follow it)

**Locked claim:** FaultLine produces a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer verifies “where does *this* reviewed witness first go bad?” **without re-running repository code**, without trusting model intent.

**Default sequence (external-01 is `completed` — lead with permissioned later-turn, then mature sample):**

1. **Cold-open on permissioned later-turn:** MUMBCS external protocol validation (`EXPERIMENTAL_TURN`; say scripted protocol validation) — [publishable-summary-mumbcs.md](publishable-summary-mumbcs.md).
2. **Show mature trust:** verified `COMMIT_PROOF` sample via `pnpm fl judge-proof` (sample root `sha256:f85c446d…` — disposable live-git sample, **not** the historical self-incident unless you open that package). Historical dogfood: [faultline-self-incident.md](faultline-self-incident.md).
3. **Show GPT-5.6 on screen** — live `--live` when a key is available; otherwise committed redacted samples in `docs/samples/gpt-5.6/`.
4. **Show Codex** — `/feedback` + sidecar.
5. **Optional** later-turn / minimize / repair if time remains.
6. **Optional** `judge-demo` fixture sandbox last — never the Idea headline.

**Fallback** if you skip the external beat: cold-open on the COMMIT_PROOF sample only.

Pinned recording checkout: `git checkout v0.1.7-buildweek`. Teleprompter: [video-teleprompter.md](video-teleprompter.md).

Do not insert `--` between `fl` and the subcommand (`pnpm fl judge-demo`, never `pnpm fl -- judge-demo`).

## Fastest judge check (sandbox only)

Deterministic, no-Docker, no-API-key path for reviewers who cannot run a live environment:

```powershell
pnpm install --frozen-lockfile
pnpm fl judge-demo --rerun-all --export-only
pnpm fl verify .faultline/bundles/judge-demo
```

Or open [`judge-preview.html`](judge-preview.html) with zero install. Label accurately: frozen witness / evidence labels / sample boundary — **not** a real incident or native Docker proof.

### Live guided incident path (after Docker is ready)

```powershell
pnpm fl doctor --repo .
pnpm fl investigate --ci-log .\ci.log --repo . --command "<failing predicate>" --runtime node
# Browser: Approve, then Freeze (separate clicks). The CLI continues automatically.
# Resume if interrupted:
pnpm fl investigate --resume <id> --repo . --expect-digest <frozen-digest> --runtime node
pnpm fl serve --bundle <proof> --expect-root <retained-root>
```

Say on camera that FaultLine did not auto-approve or auto-freeze; the one command only orchestrates modular primitives after explicit human freeze.

## Three-minute demo run of show

### Cold open script (0:00–0:20) — Idea beat (you record)

**Locked sequence (matches [video-teleprompter.md](video-teleprompter.md) + Devpost Final gate):**  
1. **Preferred:** external MUMBCS protocol beat (summary / digests story), then  
2. `pnpm fl judge-proof` sample root `sha256:f85c446d…` (or `docs/self-incident-proof-preview.html` only as a visual aid for the sample).  
**Do not** open with `judge-demo` or “we built a better bisect.”

**Speak (approx.) — external-01 landed:**  
“In a consented external protocol validation on MUMBCS, FaultLine froze a human-reviewed witness and attributed Turn 3 as the earliest stable PASS→FAIL — experimental turn evidence, not a naturally occurring production bug. Same witness, recorded turn trees. For portable trust: here is a verified COMMIT_PROOF **sample** package — root `sha256:f85c446d…` — distinct from our historical self-incident dogfood at `97c3290`.”

**Fallback cold open (skip external beat):** sample-only — say “verified COMMIT_PROOF **sample** package,” never “real incident.”

| Time | Screen | Narration point |
| --- | --- | --- |
| 0:00–0:15 | External later-turn story / package | Turn 3 earliest stable failure; `EXPERIMENTAL_TURN`; protocol validation unless organic. [mumbcs guide](partners/mumbcs-faultline-guide.md). |
| 0:15–0:30 | Same frozen overlay + turn trees | Identical witness bytes across states; production-only Turn 3 edit. |
| 0:30–0:55 | `--minimize` / `fl prove transition` | Tree-to-tree counterfactual from the turn boundary. |
| 0:55–1:20 | `fl judge-proof` sample | Verified COMMIT_PROOF **sample** (`f85c446d…`), not historical self-incident unless that package is opened. |
| 1:20–1:40 | `fl witness propose --live` → Approve → Freeze | GPT-5.6 proposes; human freezes; model never assigns PASS/FAIL. |
| 1:40–2:10 | Docker / verify | Offline verify against retained root. |
| 2:10–2:40 | Codex `/feedback` + repair brief | Session `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`. |
| 2:40–3:00 | Optional `judge-demo` | Fixture sandbox only. |

Use a public video with spoken narration. The voiceover should explicitly cover both Codex and GPT-5.6, and the recording should show the product actually running rather than slides alone. **You still must record and upload the YouTube video** — this kit only supplies the Idea cold-open script.

### Signed-evidence insert (replace, do not extend, the 2:20–2:45 segment)

If CI provenance is available for the recording, spend that segment on the retained CI evidence instead of a second test view:

1. Show the frozen witness and, if used, `fl witness sign` proving that the reviewer approval is bound to a trusted Ed25519 key.
2. Show the GitHub Actions run creating `.faultline/provenance/ci-receipt.json`, then `actions/attest@v4` signing those exact bytes.
3. Show `fl provenance verify` with the saved Sigstore bundle and explicit trust file. Say plainly that this verifies the receipt bytes and configured GitHub Actions identity, not host or Docker-daemon enforcement.

Never imply that a locally generated `fl provenance create` file is signed: the command is CI-only and its output is unsigned until the separate GitHub Artifact Attestation is retained.

## Submission checklist

- [x] Select the **Developer Tools** track.
- [ ] Add a concise project description and a public under-three-minute narrated video.
- [ ] Start from the human-reviewed [Devpost description draft](devpost-description-draft.md); replace every remaining bracketed placeholder and remove unsupported claims.
- [x] Repository license: MIT on canonical branch `main` (set GitHub default branch to `main` if needed).
- [x] Public npm package name: `@mizore66/faultline` (publish with `pnpm publish` when releasing; `npx @mizore66/faultline` after publish).
- [x] Qualifying Codex `/feedback` session ID: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`.
- [ ] Keep the README's install, platform, test, demo, Docker, and scope-boundary instructions current.
- [ ] Run the native Docker investigation in a real Docker environment and retain the printed external proof root for the video (self-incident root already recorded: see [faultline-self-incident.md](faultline-self-incident.md)).
- [ ] If demonstrating reviewer authentication, retain the public-key keyring separately and use `fl witness verify --keyring ... --require-signature` on camera; never record or publish the private key.
- [ ] In GitHub Actions, create the CI-only provenance subject, pass its exact bytes through `actions/attest@v4`, and retain the produced Sigstore attestation bundle.
- [ ] Confirm the public CI test matrix is green, including the hermetic local-provenance refusal check: `fl provenance create` must reject a process with `GITHUB_ACTIONS=false` even when the parent test run is in GitHub Actions.
- [ ] Preserve the exact `faultline.github-artifact-attestation-trust.v1` file and its trusted root file used for `fl provenance verify`; review its repository, workflow, ref, event, and runner constraints before recording.
- [ ] Verify the live proof bundle, unsigned receipt binding, supplied Sigstore bundle, and trust configuration together before publishing the video. Do not claim this attests to the Docker host or daemon.
- [x] Impact framing: self-incident dogfood only (no invented adoption metrics); see README and [impact-validation template](impact-validation-template.md) before expanding claims.
- [ ] Use the [differentiation comparison](differentiation.md) to avoid claiming that FaultLine replaces Git bisect, CI artifacts, repro cases, or provenance tooling.
- [ ] Recheck current rules, deadline, track requirements, and all required fields immediately before submission.

## Judge-ready commands

```powershell
pnpm install --frozen-lockfile
pnpm test
pnpm fl judge-demo --rerun-all --export-only
pnpm fl verify .faultline/bundles/judge-demo
pnpm fl demo live-git --export-only
pnpm fl provenance verify `
  --bundle <git-proof-bundle-directory> `
  --receipt .faultline\provenance\ci-receipt.json `
  --attestation-bundle .\sigstore-bundle.json `
  --trust .\faultline-attestation-trust.json
```

For a live incident, use a digest-pinned image and the Git investigation flow documented in the README. A Docker daemon is required for proof-grade output; injected and unsafe-local runs are intentionally non-proof. The provenance verification command does not run repository code; it validates the saved proof bundle, the supplied GitHub Artifact Attestation material (through `gh attestation verify`), and FaultLine's own byte bindings.

## Required factual evidence ledger

These are separate facts to gather; none is created merely by copying this kit into a repository.

| Submission or presentation statement | Evidence to retain or fill in | Status |
| --- | --- | --- |
| FaultLine runs as shown | Commit/release, platform/runtime, exact commands, and recorded output | [RECORD AT DEMO TIME] |
| The live Git/Docker path produced evidence | Verified FaultLine self-incident bundle with recorded root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` | [RECORDED] |
| Codex and GPT-5.6 were used as described | Qualifying `/feedback` `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`; README documents build vs runtime boundaries | [RECORDED SESSION ID] |
| The project addressed a real audience problem | Dogfood record: [impact-validation-self-incident.md](impact-validation-self-incident.md); no third-party adoption metrics claimed | [SCOPED / SELF-VALIDATED] |
| The Devpost entry is complete | Selected track, public narrated-video URL, feedback session ID, repository URL/license, and required fields | [VIDEO + FORM STILL OPEN] |
| Signed CI provenance is shown | CI receipt, matching Sigstore bundle, trust file, trusted root, and exact verification result | [OPTIONAL / NOT YET RECORDED] |

Do not transform a blank status into a claim. The [Devpost description draft](devpost-description-draft.md), [impact-validation template](impact-validation-template.md), and [differentiation comparison](differentiation.md) are deliberately structured so a human can replace placeholders with auditable facts.

## Claims inventory (public claim → artifact)

Grep-able map of statements we may say publicly to the retained artifact that backs them. No row may be claimed without its artifact path. Re-verified 2026-07-19 against post-backlog roots.

### Public disclosure — soak integrity incident (SUB-03)

We published a multi-OS SIDE_CAR soak sample that was fabricated by a generator script, then used it to self-grant `TURN_PROOF` promotion — that was our error, not an external discovery. Commit [`457fabeb9d5ff2261cbbf1ced4673ff28c9074d0`](https://github.com/Mizore66/faultline/commit/457fabeb9d5ff2261cbbf1ced4673ff28c9074d0) deletes those samples, restores honest promotion gates, and adds `tests/evidence-honesty.test.ts` so fabricated soak origins cannot quietly return. Release `v0.1.4-buildweek` is **retracted**; do not cite it.

| Public claim | Artifact path | Recorded value / note |
| --- | --- | --- |
| Autonomous design (COH-12) | `docs/devpost-paste-ready.md` + `src/autonomous-session.ts` | Unattended sessions keep moving, but nothing becomes proof until a human owned the question — before it ran, or verifiably unchanged after. |
| Soak integrity revert + honesty tests (owned disclosure) | commit `457fabe` + `tests/evidence-honesty.test.ts` + retracted `v0.1.4-buildweek` | Fabricated multi-OS SIDE_CAR soak removed; `TURN_PROOF` reserved until organic/external soak; see SUB-03 paragraph above |
| Judge Idea path opens a verified `COMMIT_PROOF` sample | `docs/samples/self-incident-commit-proof/ROOT.sha256` | `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` |
| Sample package docs / replace procedure | `docs/samples/COMMIT_PROOF_SAMPLE.md` | Install from `fl demo live-git --export-only` |
| Historical self-incident dogfood root | `docs/impact-validation-self-incident.md` | `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` at `97c3290` |
| External-01 MUMBCS protocol validation (not organic prod bug) | `docs/impact-validation-external-01.md` | Status `completed`; root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`; local `fl verify` VALID+MATCH |
| External-01 publishable summary | `docs/publishable-summary-mumbcs.md` | Same root; `EXPERIMENTAL_TURN` / Turn 3 |
| External-01 Codex sidecar ledger (W0-2 via partner, not self-dogfood) | `docs/samples/mumbcs-sidecar-ledger/` | Real `SIDE_CAR` 11-event ledger; trees.pack not shipped |
| Codex organic self-dogfood SIDE_CAR (Windows) | `docs/impact-validation-codex-self-dogfood.md` + `docs/samples/faultline-self-sidecar-soak/` | Genuine Codex development sessions; 96-event private ledger head `sha256:ef0c9a2f…`; public excerpt verifies; **multi-OS soak = future work (#152)** |
| Guided CI freeze on organic CI failure | `docs/impact-validation-codex-self-dogfood.md` §B | Run `29694948356`; frozen `ci-29694948356-pnpm-test` digest `sha256:89313b84…`; local `fl witness verify` valid; not a COMMIT_PROOF package |
| Honest non-Codex observed transport sample | `docs/samples/observed-external-transport/` | `OBSERVED_EXTERNAL_TRANSPORT`; not Codex hooks |
| Coverage matrix honesty (hybrid: spec/unit + Docker E2E rows in CI) | `benchmarks/REPORT.md` + `benchmarks/e2e-executed.json` | 8 scenarios; Docker CI promotes listed rows to `EXECUTABLE_E2E` |
| Coverage matrix machine-readable twin | `benchmarks/report.json` | `faultline.coverage-matrix.v1` |
| Qualifying Codex `/feedback` session | README + this kit | `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` |
| Zero-install proof page snapshot | `docs/self-incident-proof-preview.html` | Visual aid for sample; not a substitute for `fl verify` |
| GPT-5.6 redacted samples (no API key) | `docs/samples/gpt-5.6/` | Proposal / repair-brief shapes |
| Pinned submission checkout | README + `package-smoke` pin | `v0.1.7-buildweek` |
| Security control → test traceability | `docs/security-model.md` appendix | Smoke-grepped by `scripts/package-smoke.mjs` |
| Flagship `fl demo full` arc (Docker) | CLI: `fl demo full` | Ends with `PREVENTION_VERIFIED` + demo-repo `AGENTS.md` only when artifacts verify; requires Docker |

**Do not claim without a green artifact in this inventory:** signed release, soak results, or stranger test.

## Claims we do not make (read before record / paste)

- Model intent, private Codex interception, or unique semantic root cause
- Tamper-proof host / Docker daemon enforcement (signed CI provenance binds receipt bytes + Actions identity only — [security-model.md](security-model.md))
- That MUMBCS external-01 is a naturally occurring production bug (it is protocol / interoperability validation, `EXPERIMENTAL_TURN`)
- That native Codex UI replay without a fresh sidecar ledger is the proof package (demo-only — [codex-sidecar.md](codex-sidecar.md#delegated-worktrees--demo-only-when-no-ledger))
- Fabricated Codex ledgers (Cursor or other editors rebadged as Codex hook events) — [security-model.md](security-model.md#provenance-honesty-never-fake-codex-ledgers)
- Time-saved metrics, third-party adoption scale, or npm global install
- `TURN_PROOF` or “prevention verified” for the turn arc unless the retained artifacts actually say so
- Signed release, soak, or stranger test unless those artifacts land and are added to the inventory above

## CI evidence packet to retain

Before recording, prepare these four separate artifacts from the same successful GitHub Actions run:

1. The verified Git proof bundle and the proof root retained outside it.
2. `.faultline/provenance/ci-receipt.json`, created only by `fl provenance create` in GitHub Actions.
3. The Sigstore bundle emitted after `actions/attest@v4` signs the exact receipt subject.
4. The reviewed `faultline.github-artifact-attestation-trust.v1` file and its `trustedRootFile` (plus `sourceDigest` when the policy uses one).

That packet is what makes the reviewer and CI claims reproducible. It remains a bounded assertion: signature verification authenticates the configured GitHub Actions identity and receipt bytes; it does not attest to the physical or virtual host, Docker engine, or an unobserved build step.
