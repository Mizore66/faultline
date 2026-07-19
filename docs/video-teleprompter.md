# Video teleprompter (&lt;3 min)

Record with product on screen. Prefer PowerShell: `pnpm.cmd fl …` if ExecutionPolicy blocks `pnpm`.

**Hard rules:** no cold-open on `judge-demo`; do not say `judge-proof` is root `f6a391…`; do not invent time-saved metrics; external-01 is `completed` — say **protocol validation** / **`EXPERIMENTAL_TURN`**, not a naturally occurring production bug.

**Pinned checkout for the video:** `git checkout v0.1.0-buildweek`

---

## 0:00–0:20 — Cold open (prefer external later-turn, then sample)

<!-- faultline-cold-open: prefer-external -->

**Preferred screen:** external MUMBCS protocol summary / partner evidence story, then `pnpm fl judge-proof` sample root `sha256:f85c446d…`. See [publishable-summary-mumbcs.md](publishable-summary-mumbcs.md).

**Say (external-01 landed — preferred):**

> In a consented external protocol validation on MUMBCS, FaultLine froze a human-reviewed witness and attributed Turn 3 as the earliest stable PASS→FAIL. That’s experimental turn evidence — scripted protocol validation, not a naturally occurring production bug — then we show mature portable COMMIT_PROOF for trust.

**Fallback screen / say (skip external beat):** `pnpm fl judge-proof` sample root `sha256:f85c446d…`.

> When an agent-assisted change turns CI red, FaultLine answers: where does *this* human-frozen witness first go bad? Here’s a verified COMMIT_PROOF **sample** package — root sha256:f85c446d — not our historical self-incident unless we open that package. Complements bisect and CI logs — doesn’t replace them.

---

## 0:20–0:45 — GPT-5.6 propose → human freeze

**Screen:** `pnpm fl witness propose --live …` → browser Approve → Freeze. Needs `OPENAI_API_KEY`.

**Fallback footage (no key / flaky API):** open committed samples in `docs/samples/gpt-5.6/` (redacted response shapes) and say they match the live schema.

**Say:**

> GPT-5.6 drafts the executable question from a blinded, redacted incident packet — it never sees candidates and never gets a vote on PASS or FAIL. A human freezes the exact bytes.

---

## 0:45–1:20 — Docker replay

**Screen:** `pnpm fl demo live-git --export-only` **or** stay on proof page and highlight Docker / 3× runs. Docker Desktop must be running for live-git. Safe fallback: verified sample proof page.

**Say:**

> FaultLine replays that immutable witness over real Git states, three times per state, in constrained Docker. A compile error can never masquerade as a failure.

---

## 1:20–1:50 — Fix + GPT-5.6 repair brief

**Screen:** `pnpm fl repair --with-codex` (if time) then `pnpm fl repair brief --bundle … --live`.

**Fallback:** `docs/samples/gpt-5.6/repair-brief.sample.json` — stronger GPT moment than proposal alone.

**Say:**

> Codex implements the fix in a throwaway worktree; GPT-5.6 writes a repair brief that cites only executed facts and is labeled inference.

---

## 1:50–2:15 — Offline verify

**Screen:**

```powershell
pnpm fl verify .\docs\samples\self-incident-commit-proof --expect-root sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9
```

**Say:**

> A teammate — or a judge — verifies this package offline without re-running our code. Judges: README three-command path, no Docker, no API key.

---

## 2:15–2:45 — Codex build story + close

**Screen:** `/feedback` ID `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` and `pnpm test`.

**Say (novelty):**

> FaultLine freezes one human-reviewed executable check, replays those exact bytes across immutable Git and Codex-turn states in a locked-down sandbox, and emits a portable evidence package that a stranger can re-derive offline — it refuses every claim the executions don't support.

**Claims to avoid:** “real production incident,” “root cause,” “prevention verified” for the turn arc, “invisible overhead,” or upgrading the MUMBCS beat beyond protocol validation / `EXPERIMENTAL_TURN`.
