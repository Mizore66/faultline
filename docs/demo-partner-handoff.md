# Demo partner handoff (source of truth for narration)

Use this document — not the repository teleprompter verbatim — when recording. The teleprompter remains the shot list; **this file wins** on claim wording.

**Pin:** `v0.1.10-buildweek` only. Do not record from `main` or `v0.1.9-buildweek`.

**Order:** rehearse → record → edit to 2:30–2:40 → upload public YouTube → test logged out → fill `YOUTUBE_URL` → `node scripts/lint-devpost-claims.mjs` → submit.

## Locked claim

FaultLine’s mature `COMMIT_PROOF` path proves the earliest stable PASS→FAIL boundary under a frozen witness. Its Codex-turn path applies the same evidence model but remains experimental. It does **not** prove model intent, unique semantic root cause, private Codex interception, or one universal runtime.

| Path | What you may say |
| --- | --- |
| `COMMIT_PROOF` / `judge-proof` | Mature portable proof — lead trust here |
| MUMBCS Turn 3 | Consented **protocol / interoperability validation** under `EXPERIMENTAL_TURN` |
| GPT-5.6 | Blinded witness proposal + evidence-cited repair brief only — never PASS/FAIL |
| Codex | Build session + public hooks + turn trees — not repair authorship unless footage proves it |

## Shot sequence (2:30–2:40)

1. **Cold open (0:00–0:20)** — MUMBCS protocol validation → Turn 3 earliest stable FAIL → then `pnpm fl judge-proof` sample root `sha256:f85c446d…`.
2. **GPT-5.6 (0:20–0:45)** — `fl witness propose --live` → human Approve → Freeze (or committed samples in `docs/samples/gpt-5.6/`).
3. **Demo arc (0:45–1:35)** — Prefer `pnpm fl demo full` with Docker. Only say `PREVENTION_VERIFIED` if that run emitted it.
4. **Offline verify (1:35–1:55)** — `pnpm fl verify docs/samples/self-incident-commit-proof --expect-root sha256:f85c446d…`.
5. **Codex close (1:55–2:30)** — `/feedback` `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` → public lifecycle hook → recorded turn ID → immutable snapshot tree → Turn 3 FAIL story.

## Accurate lines (prefer these)

**Offline verify**

> Verifies the package offline—without rerunning repository code, without Docker, and without trusting the UI.

(The verifier is FaultLine code shipped in the package path — do not say “without re-running our code.”)

**Judge install path**

> README six-command clone/install path on the pin, then `pnpm fl judge-proof` — no Docker, no API key.

(Do not say “three-command path.”)

**AGENTS.md**

> Executable tests and guards enforce the invariant; AGENTS.md records it as guidance for future agents.

(Do not present AGENTS.md as enforcement.)

**Codex (no repair footage)**

> Codex was load-bearing in building FaultLine, and its public lifecycle hooks power the turn recorder. FaultLine binds an observed turn to an immutable source tree, then independently executes the frozen witness across those states.

**Security (if asked)**

> An independent security review found a Critical issue. We fixed it, added a regression test, and published the disposition.

Never: “FaultLine is security audited.”

## Forbidden unless artifact is on screen

- Do not say: “Codex generated this repair” / “Codex fixed the regression” / “Here is the Codex repair thread”
- Do not say: “real production incident” / “root cause” / organic bug (MUMBCS is scripted protocol validation)
- Do not say: “invisible overhead”
- Do not invent time-saved or soak metrics
- Do not equate `judge-proof` with historical root `f6a391…`

## Pre-camera checks

- [ ] Checkout is exactly `v0.1.10-buildweek`
- [ ] Confirm CI for commit `89b8e23…` / tag `v0.1.10-buildweek` (Actions run URL + screenshot) — do not rely only on a badge. Note: the pin was retargeted to current `main` on 2026-07-21 (includes narration cleanup + package `0.1.4`). Earlier tip `ce99a793` remains in history. Capture the successful required jobs you will cite on camera.
- [ ] `FAULTLINE_REHEARSE_TAG=v0.1.10-buildweek node scripts/rehearse-teleprompter.mjs`
- [ ] `node scripts/lint-devpost-claims.mjs`
- [ ] Docker up only if you will show `fl demo full` / live proof

## Related

- Shot list: [video-teleprompter.md](video-teleprompter.md)
- Finish path: [submission-finish-runbook.md](submission-finish-runbook.md)
- Full kit: [build-week-submission-kit.md](build-week-submission-kit.md)
- Devpost paste: [devpost-paste-ready.md](devpost-paste-ready.md)
