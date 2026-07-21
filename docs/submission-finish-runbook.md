# Submission finish runbook (Video + External N=1 + Devpost)

Human-owned finish path. Deadline: **Jul 21, 2026 @ 5:00pm PDT** — https://openai.devpost.com/

Related artifacts:

| Artifact | Purpose |
| --- | --- |
| [demo-partner-handoff.md](demo-partner-handoff.md) | **Narration source of truth** for the recording partner |
| [video-teleprompter.md](video-teleprompter.md) | Shot list / teleprompter (handoff wins on claim wording) |
| [external-participant-invite.md](external-participant-invite.md) | Copy-paste invite for N=1 |
| [impact-validation-external-01.md](impact-validation-external-01.md) | Fill during external session |
| [devpost-paste-ready.md](devpost-paste-ready.md) | Paste into Devpost after YouTube URL |
| [build-week-submission-kit.md](build-week-submission-kit.md) | Full kit / optional signed CI insert |

## Phase 0 — Prep status (agent-checked 2026-07-18)

| Check | Result | Your action |
| --- | --- | --- |
| `pnpm fl judge-proof --export-only` | **PASS** — root `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` MATCH | None |
| Verify FaultLine CI on `main_k5_offMain` @ `81a9f5c` | **PASS** (green) | Keep branch green before recording |
| Docker daemon | **DOWN** locally when checked | Start Docker Desktop before mid-video `demo live-git` |
| `OPENAI_API_KEY` | **unset** in this environment | Set in your shell before `--live` shots |
| `/feedback` ID | `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` | Keep on sticky / teleprompter |
| External participant | **Not booked by agent** | Send [external-participant-invite.md](external-participant-invite.md) |

### Phase 0 checklist (you)

- [ ] Docker Desktop running (`pnpm fl doctor --proof-ready` shows daemon READY if you will record live-git)
- [ ] `$env:OPENAI_API_KEY = "..."` (or equivalent) for GPT-5.6 `--live`
- [ ] External person confirmed + consent to publish redacted summary
- [ ] Rehearse once: `pnpm fl judge-proof` opens the proof page

---

## Phase 1 — Video (you record)

1. Narrate from [demo-partner-handoff.md](demo-partner-handoff.md); use [video-teleprompter.md](video-teleprompter.md) as the shot list. Record **under 3 minutes** (target 2:30–2:40).
2. Cold-open on the external MUMBCS protocol beat when available, then `pnpm fl judge-proof` (never `judge-demo`).
3. Speak **Codex** and **GPT-5.6** by name; show Codex via build session + lifecycle/turn evidence (do not invent repair footage).
4. Upload YouTube as **public**.
5. Paste URL into [devpost-paste-ready.md](devpost-paste-ready.md) field `YOUTUBE_URL`.

- [ ] YouTube URL saved: `_______________________________`

---

## Phase 2 — External N=1 (you + participant)

1. Send invite from [external-participant-invite.md](external-participant-invite.md).
2. Run session on **their** repo (not FaultLine dogfood).
3. Fill [impact-validation-external-01.md](impact-validation-external-01.md) with facts only.
4. Copy the “Publishable summary” paragraph into Devpost “Why it matters”.

Do **not** use [impact-validation-self-incident.md](impact-validation-self-incident.md) as the external N=1.

- [ ] External record status = `completed`
- [ ] Redacted summary approved by participant

---

## Phase 3 — Devpost submit (you)

1. Open https://openai.devpost.com/ and join/submit under **Developer Tools**.
2. Paste body from [devpost-paste-ready.md](devpost-paste-ready.md) (after filling YouTube + external summary).
3. Fields:
   - Repo: `https://github.com/Mizore66/faultline`
   - `/feedback`: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`
   - Video: public YouTube URL
   - Install: clone + `pnpm fl judge-proof`
4. Gallery: proof page still, Approve/Freeze still, verify still.
5. Final gate:
   - [ ] Video &lt;3 min, names Codex **and** GPT-5.6, opens on `judge-proof`
   - [ ] External N=1 summary present (no fake scale)
   - [ ] No claim `judge-proof` = historical `f6a391` package
   - [ ] No `npm install -g @mizore66/faultline`
   - [ ] Recheck deadline Jul 21, 2026 @ 5:00pm PDT
   - [ ] **Submit**

---

## Score effect reminder

| Deliverable | Design | Idea | Impact | Tech |
| --- | ---: | ---: | ---: | ---: |
| Video done well | → ≥19.5 | → ≥19.5 | small | stays ≥19.5 |
| External N=1 | small | small | → ≥19.5 | — |
| Devpost submit | required | required | required | required |
