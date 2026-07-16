# FaultLine Build Week submission kit

This is a recording and submission checklist, not evidence that a submission has already been made. Recheck the official Build Week page before submitting.

## Three-minute demo run of show

| Time | Screen | Narration point |
| --- | --- | --- |
| 0:00–0:18 | A real CI regression and FaultLine's incident screen | "When an agent-assisted change turns CI red, FaultLine answers a narrower, useful question: where does a reviewed executable witness first become bad?" |
| 0:18–0:40 | `fl witness propose`, approval, and freeze output | "A human freezes the exact predicate before localization. The model may suggest it, but never decides the verdict." |
| 0:40–1:10 | `fl demo live-git --export-only` plus the Docker policy in the verified proof view | "FaultLine replays that immutable witness over real Git states, three times per state, in a constrained Docker environment. It only calls a boundary when the executions support it." |
| 1:10–1:35 | `fl serve --bundle … --expect-root …` | "A teammate can inspect and verify the portable package without rerunning repository code. The package rejects rehashed contradictions, not just changed checksums." |
| 1:35–1:55 | `fl minimize git` results | "It then asks whether the selected diff is actually sufficient and necessary, preserving conflicts and unknowns instead of guessing." |
| 1:55–2:20 | `fl repair brief --bundle … --live` and `fl repair verify …` | "GPT-5.6 receives a privacy-minimized packet of verified facts and returns only cited, explicitly inferred repair guidance. It cannot manufacture a proof or blame a model." |
| 2:20–2:45 | `pnpm test`, proof verification, and GitHub Actions | "Codex accelerated the implementation, tests, adversarial review, and product hardening. The live Docker boundary is exercised in CI; local systems without Docker fail closed." |
| 2:45–3:00 | FaultLine proof view and repository README | "FaultLine makes agent-assisted debugging auditable: freeze, replay, prove, fix, and prevent — without pretending the evidence says more than it does." |

Use a public video with spoken narration. The voiceover should explicitly cover both Codex and GPT-5.6, and the recording should show the product actually running rather than slides alone.

## Submission checklist

- [ ] Select the **Developer Tools** track.
- [ ] Add a concise project description and a public under-three-minute narrated video.
- [ ] Provide the repository URL. Before publishing, choose an appropriate license; this repository deliberately does not choose one on the submitter's behalf.
- [ ] If keeping the repository private, grant the organizers the required testing access specified in the current rules.
- [ ] Include the qualifying Codex `/feedback` session ID.
- [ ] Keep the README's install, platform, test, demo, Docker, and scope-boundary instructions current.
- [ ] Run the native Docker investigation in a real Docker environment and retain the printed external proof root for the video.
- [ ] Recheck current rules, deadline, track requirements, and all required fields immediately before submission.

## Judge-ready commands

```powershell
pnpm install --frozen-lockfile
pnpm test
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
pnpm fl -- demo live-git --export-only
```

For a live incident, use a digest-pinned image and the Git investigation flow documented in the README. A Docker daemon is required for proof-grade output; injected and unsafe-local runs are intentionally non-proof.
