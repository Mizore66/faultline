# FaultLine Build Week submission kit

This is a recording and submission checklist, not evidence that a submission has already been made. Recheck the official Build Week page before submitting.

**Finish path (video + external N=1 + Devpost):** start at [submission-finish-runbook.md](submission-finish-runbook.md) — teleprompter, invite, external impact form, and paste-ready Devpost text.

## Idea claim (say this once, then follow it)

**Locked claim:** FaultLine produces a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer verifies “where does *this* reviewed witness first go bad?” **without re-running repository code**, without trusting model intent.

1. **Lead with that claim + runnable `COMMIT_PROOF` sample:**

```powershell
pnpm fl judge-proof
```

Sample root: `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` ([COMMIT_PROOF_SAMPLE.md](samples/COMMIT_PROOF_SAMPLE.md)). Historical dogfood (`f6a391…` / `97c3290`) is separate — [faultline-self-incident.md](faultline-self-incident.md). One complement line only: complements Git bisect / CI logs / repro — does not replace them.

2. **Show GPT-5.6 live** — `fl witness propose --live` and/or `fl repair brief --live` (needs `OPENAI_API_KEY`).
3. **Show Codex** — build acceleration (`/feedback` session) and/or sidecar hook install / ledger path.
4. **Optional UI close-up** — `pnpm fl judge-demo` or `docs/judge-preview.html` as a **fixture sandbox**, not the Idea headline.
5. **Never headline** — `fl investigate turns` (`EXPERIMENTAL_TURN`).

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

**On screen:** `pnpm fl judge-proof` proof page (sample root `sha256:f85c446d…`) or `docs/self-incident-proof-preview.html`. **Do not** open with `judge-demo` or “we built a better bisect.”

**Speak (approx.):**  
“When an agent-assisted change turns CI red, FaultLine answers a narrower question: where does *this* human-frozen witness first go bad? It packages that answer so another engineer can verify the evidence offline — without re-running the repo, and without trusting model intent. Here’s a real COMMIT_PROOF package open in the product UI — root `sha256:f85c446d…`. Separately, we dogfooded the same protocol on our own provenance regression at `97c3290`. Complements bisect and CI logs — doesn’t replace them.”

| Time | Screen | Narration point |
| --- | --- | --- |
| 0:00–0:20 | `fl judge-proof` / sample proof view | Cold open script above. Sample: [COMMIT_PROOF_SAMPLE.md](samples/COMMIT_PROOF_SAMPLE.md). Dogfood cite: [self-incident](faultline-self-incident.md). |
| 0:20–0:40 | `fl witness propose --live` → Approve → Freeze | "GPT-5.6 may propose a blinded witness; a human freezes the exact predicate. The model never decides the verdict." |
| 0:40–1:10 | `fl demo live-git --export-only` + verified Docker policy | "FaultLine replays that immutable witness over real Git states, three times per state, in constrained Docker. Boundaries only when executions support them." |
| 1:10–1:35 | `fl minimize git` results | "Sufficiency/necessity of the selected diff — conflicts and unknowns preserved, not guessed." |
| 1:35–2:05 | `fl repair brief --bundle … --live` | "GPT-5.6 returns cited, explicitly inferred repair guidance from verified facts only." |
| 2:05–2:25 | Offline `fl verify` / proof page | "A teammate verifies the package against the retained root without re-executing repository code." |
| 2:25–2:45 | Codex `/feedback` ID + tests/CI | "Codex accelerated implementation and hardening. Session `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`." |
| 2:45–3:00 | Optional `judge-demo` UI close-up | "Fixture sandbox for judges without Docker — not the Idea claim." |

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

## CI evidence packet to retain

Before recording, prepare these four separate artifacts from the same successful GitHub Actions run:

1. The verified Git proof bundle and the proof root retained outside it.
2. `.faultline/provenance/ci-receipt.json`, created only by `fl provenance create` in GitHub Actions.
3. The Sigstore bundle emitted after `actions/attest@v4` signs the exact receipt subject.
4. The reviewed `faultline.github-artifact-attestation-trust.v1` file and its `trustedRootFile` (plus `sourceDigest` when the policy uses one).

That packet is what makes the reviewer and CI claims reproducible. It remains a bounded assertion: signature verification authenticates the configured GitHub Actions identity and receipt bytes; it does not attest to the physical or virtual host, Docker engine, or an unobserved build step.
