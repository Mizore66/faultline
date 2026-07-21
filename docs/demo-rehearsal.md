# Demo rehearsal checklist (DEMO-01)

Run this before camera / judge sessions. Artifact-backed — see linked docs.

## Paths

- [ ] [Zero-install / Codespaces](demo-zero-install.md) — A, B, or C works on the machine you’ll use  
- [ ] Pin checkout: `git checkout v0.1.10-buildweek` (or Codespaces `ref=v0.1.10-buildweek`)  
- [ ] `pnpm fl doctor --proof-ready` understood (READY vs stay on `judge-proof`)  
- [ ] `FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only` prints verified root  

## Script surfaces

- [ ] [Q&A](demo-qa.md) — answers point at artifacts only  
- [ ] [Fallback](demo-fallback.md) — one-sentence switch rehearsed  
- [ ] [Teleprompter](video-teleprompter.md) — hard rules; no fake `YOUTUBE_URL`  
- [ ] [Comprehension protocol](demo-comprehension-test.md) — at least one real cold PASS retained (DEMO-03)

## On-camera claims → on-screen artifact

| Claim | Show |
| --- | --- |
| COMMIT_PROOF sample | `judge-proof` or HTML preview |
| GPT-5.6 propose | `docs/samples/gpt-5.6/` or live `--live` |
| External later-turn | MUMBCS summary docs (permissioned honesty) |
| npm install | `npm i -g @mizore66/faultline@0.1.2` |
| Signed pin | `git verify-tag v0.1.10-buildweek` |

## Honesty gates

- [ ] No invented soak / `TURN_PROOF` / YouTube URL  
- [ ] Fallback package identified before going live  
- [ ] Partner cold SEC-08 transcript separate from this rehearsal ([#171](https://github.com/Mizore66/faultline/issues/171))
