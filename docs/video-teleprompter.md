# Video teleprompter (&lt;3 min)

Record with product on screen. Prefer PowerShell: `pnpm.cmd fl …` if ExecutionPolicy blocks `pnpm`.

**Hard rules:** no cold-open on `judge-demo`; do not say `judge-proof` is root `f6a391…`; do not invent time-saved metrics.

---

## 0:00–0:20 — Cold open (prefer external later-turn when ready)

**Preferred screen:** external / partner later-turn package or ledger story (Turn 3 earliest stable failure; say **`EXPERIMENTAL_TURN`**).

**Fallback screen:** `pnpm fl judge-proof` sample root `sha256:f85c446d…`.

**Say (with external case study):**

> An independent developer used FaultLine on their repository to record a real Codex session. Under one human-frozen overlay witness, Turn 3 was the earliest stable PASS→FAIL. That’s experimental turn evidence — then we show mature portable COMMIT_PROOF for trust.

**Say (fallback without external package):**

> When an agent-assisted change turns CI red, FaultLine answers: where does *this* human-frozen witness first go bad? Here’s a verified COMMIT_PROOF **sample** package — root sha256:f85c446d — not our historical self-incident unless we open that package. Complements bisect and CI logs — doesn’t replace them.

---

## 0:20–0:40 — GPT-5.6 propose → human freeze

**Screen:** `pnpm fl witness propose --live …` (or prepared flow) → browser Approve → Freeze. Needs `OPENAI_API_KEY`.

**Say:**

> GPT-5.6 may propose a blinded witness. A human freezes the exact predicate. The model never decides the PASS or FAIL verdict.

---

## 0:40–1:10 — Docker replay

**Screen:** `pnpm fl demo live-git --export-only` **or** stay on proof page and highlight Docker / 3× runs. Docker Desktop must be running for live-git.

**Say:**

> FaultLine replays that immutable witness over real Git states, three times per state, in constrained Docker. It only calls a boundary when the executions support it.

---

## 1:10–1:35 — Minimize (optional; skip if short on time)

**Screen:** minimize results if available; else jump to repair.

**Say:**

> It asks whether the selected diff is sufficient and necessary — conflicts and unknowns are preserved, not guessed.

---

## 1:35–2:05 — GPT-5.6 repair brief

**Screen:** `pnpm fl repair brief --bundle <sample-or-live-bundle> --expect-root <root> --live`

**Say:**

> GPT-5.6 returns cited, explicitly inferred repair guidance from verified facts only. It cannot manufacture a proof or blame a model.

---

## 2:05–2:25 — Offline verify

**Screen:**

```powershell
pnpm fl verify .\docs\samples\self-incident-commit-proof --expect-root sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9
```

**Say:**

> A teammate verifies the package against the retained root without re-executing repository code.

---

## 2:25–2:45 — Codex

**Screen:** terminal / README showing `/feedback`, or `pnpm test` briefly.

**Say:**

> Codex accelerated FaultLine’s implementation, tests, and hardening. Our qualifying feedback session ID is zero-one-nine-f-six-six-b-d, dash zero-a-c-one, dash seven-eight-f-three, dash eight-d-c-one, dash five-nine-six-eight-e-four-f-two-f-a-zero-nine.

Exact ID to show on screen: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

---

## 2:45–3:00 — Optional fixture close-up

**Screen:** `pnpm fl judge-demo` **only if needed**.

**Say:**

> This judge-demo is a fixture sandbox for judges without Docker — not the product proof.

---

## After recording

1. Upload to YouTube as **public**.
2. Paste URL into [devpost-paste-ready.md](devpost-paste-ready.md) → `YOUTUBE_URL`.
3. Grab one still of the `judge-proof` page for the Devpost gallery.
