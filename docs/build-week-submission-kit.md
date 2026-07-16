# FaultLine Build Week submission kit

This is a recording and submission checklist, not evidence that a submission has already been made. Recheck the official Build Week page before submitting.

## Fastest judge check

Show this deterministic, no-Docker, no-API-key path before asking a reviewer to evaluate a live environment:

```powershell
pnpm install --frozen-lockfile
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
```

It demonstrates the frozen witness, evidence labels, stable-boundary rules, counterfactual result, and offline verification of the included sample. It does **not** demonstrate a real incident, a production deployment, or native Docker proof. Label it accurately in the recording, then use the separate live-Git path when Docker evidence is available.

## Three-minute demo run of show

| Time | Screen | Narration point |
| --- | --- | --- |
| 0:00–0:18 | A recorded FaultLine self-incident and its incident screen | "When an agent-assisted change turns CI red, FaultLine answers a narrower, useful question: where does a reviewed executable witness first become bad?" Do not call it recorded until the [self-incident runbook](faultline-self-incident.md) has produced a verified bundle/root. |
| 0:18–0:40 | `fl witness propose`, approval, and freeze output | "A human freezes the exact predicate before localization. The model may suggest it, but never decides the verdict." |
| 0:40–1:10 | `fl demo live-git --export-only` plus the Docker policy in the verified proof view | "FaultLine replays that immutable witness over real Git states, three times per state, in a constrained Docker environment. It only calls a boundary when the executions support it." |
| 1:10–1:35 | `fl minimize git` results | "It then asks whether the selected diff is actually sufficient and necessary, preserving conflicts and unknowns instead of guessing." |
| 1:35–2:05 | `fl repair brief --bundle … --live` and `fl repair verify …` | "GPT-5.6 receives a privacy-minimized packet of verified facts and returns only cited, explicitly inferred repair guidance. It cannot manufacture a proof or blame a model." |
| 2:05–2:20 | `fl serve --bundle … --expect-root … --minimization … --expect-minimization … --repair … --expect-repair …` | "A teammate sees the verified boundary, certified minimization, and citation summary in one read-only incident page. The downstream records are independently checked against retained digests and explicitly not claimed as part of the original Git root." |
| 2:20–2:45 | `pnpm test`, proof verification, and GitHub Actions | "Codex accelerated the implementation, tests, adversarial review, and product hardening. The live Docker boundary is exercised in CI; local systems without Docker fail closed." |
| 2:45–3:00 | FaultLine proof view and repository README | "FaultLine makes agent-assisted debugging auditable: freeze, replay, prove, fix, and prevent — without pretending the evidence says more than it does." |

Use a public video with spoken narration. The voiceover should explicitly cover both Codex and GPT-5.6, and the recording should show the product actually running rather than slides alone.

### Signed-evidence insert (replace, do not extend, the 2:20–2:45 segment)

If CI provenance is available for the recording, spend that segment on the retained CI evidence instead of a second test view:

1. Show the frozen witness and, if used, `fl witness sign` proving that the reviewer approval is bound to a trusted Ed25519 key.
2. Show the GitHub Actions run creating `.faultline/provenance/ci-receipt.json`, then `actions/attest@v4` signing those exact bytes.
3. Show `fl provenance verify` with the saved Sigstore bundle and explicit trust file. Say plainly that this verifies the receipt bytes and configured GitHub Actions identity, not host or Docker-daemon enforcement.

Never imply that a locally generated `fl provenance create` file is signed: the command is CI-only and its output is unsigned until the separate GitHub Artifact Attestation is retained.

## Submission checklist

- [ ] Select the **Developer Tools** track.
- [ ] Add a concise project description and a public under-three-minute narrated video.
- [ ] Start from the human-reviewed [Devpost description draft](devpost-description-draft.md); replace every bracketed placeholder and remove unsupported claims.
- [ ] Provide the repository URL. Before publishing, choose an appropriate license; this repository deliberately does not choose one on the submitter's behalf.
- [ ] If keeping the repository private, grant the organizers the required testing access specified in the current rules.
- [ ] Include the qualifying Codex `/feedback` session ID.
- [ ] Keep the README's install, platform, test, demo, Docker, and scope-boundary instructions current.
- [ ] Run the native Docker investigation in a real Docker environment and retain the printed external proof root for the video.
- [ ] If demonstrating reviewer authentication, retain the public-key keyring separately and use `fl witness verify --keyring ... --require-signature` on camera; never record or publish the private key.
- [ ] In GitHub Actions, create the CI-only provenance subject, pass its exact bytes through `actions/attest@v4`, and retain the produced Sigstore attestation bundle.
- [ ] Confirm the public CI test matrix is green, including the hermetic local-provenance refusal check: `fl provenance create` must reject a process with `GITHUB_ACTIONS=false` even when the parent test run is in GitHub Actions.
- [ ] Preserve the exact `faultline.github-artifact-attestation-trust.v1` file and its trusted root file used for `fl provenance verify`; review its repository, workflow, ref, event, and runner constraints before recording.
- [ ] Verify the live proof bundle, unsigned receipt binding, supplied Sigstore bundle, and trust configuration together before publishing the video. Do not claim this attests to the Docker host or daemon.
- [ ] If making impact, adoption, or time-saved claims, retain a consented record using the [impact-validation template](impact-validation-template.md), including its method and limitations.
- [ ] Use the [differentiation comparison](differentiation.md) to avoid claiming that FaultLine replaces Git bisect, CI artifacts, repro cases, or provenance tooling.
- [ ] Recheck current rules, deadline, track requirements, and all required fields immediately before submission.

## Judge-ready commands

```powershell
pnpm install --frozen-lockfile
pnpm test
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
pnpm fl -- demo live-git --export-only
pnpm fl -- provenance verify `
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
| FaultLine runs as shown | Commit/release, platform/runtime, exact commands, and recorded output | [NOT YET RECORDED] |
| The live Git/Docker path produced evidence | Verified bundle plus externally retained proof root | [NOT YET RECORDED] |
| Codex and GPT-5.6 were used as described | Human-reviewed description that matches the implemented bounded workflow | [REVIEW REQUIRED] |
| The project addressed a real audience problem | Consented incident or interview record, including counterevidence and limitations | [NOT YET COLLECTED] |
| The Devpost entry is complete | Selected track, public narrated-video URL, qualifying feedback session ID, repository URL/license, and required fields | [NOT YET COMPLETED] |
| Signed CI provenance is shown | CI receipt, matching Sigstore bundle, trust file, trusted root, and exact verification result | [OPTIONAL / NOT YET RECORDED] |

Do not transform a blank status into a claim. The [Devpost description draft](devpost-description-draft.md), [impact-validation template](impact-validation-template.md), and [differentiation comparison](differentiation.md) are deliberately structured so a human can replace placeholders with auditable facts.

## CI evidence packet to retain

Before recording, prepare these four separate artifacts from the same successful GitHub Actions run:

1. The verified Git proof bundle and the proof root retained outside it.
2. `.faultline/provenance/ci-receipt.json`, created only by `fl provenance create` in GitHub Actions.
3. The Sigstore bundle emitted after `actions/attest@v4` signs the exact receipt subject.
4. The reviewed `faultline.github-artifact-attestation-trust.v1` file and its `trustedRootFile` (plus `sourceDigest` when the policy uses one).

That packet is what makes the reviewer and CI claims reproducible. It remains a bounded assertion: signature verification authenticates the configured GitHub Actions identity and receipt bytes; it does not attest to the physical or virtual host, Docker engine, or an unobserved build step.
