# Moni-Alpha × FaultLine — partner runbook (later-turn hero + starter CI)

**Audience:** developer of [Kaiz404/Moni-Alpha](https://github.com/Kaiz404/Moni-Alpha) (Moni).  
**FaultLine repo:** [Mizore66/faultline](https://github.com/Mizore66/faultline)  
**Goal:** Run FaultLine on a **real Moni Codex session** so FaultLine can publish a later-turn **First Bad Turn** evidence package (`EXPERIMENTAL_TURN`) with retained digests — closing the “real ledger” gap for FaultLine issue **#42**. Separately: starter CI ideas for Moni once you’re ready.

This is a collaboration guide, not a claim that FaultLine is already integrated into Moni’s production pipeline.

---

## 0. What success looks like

### For FaultLine (#42)

You produce artifacts FaultLine can cite honestly:

| Artifact | Why it matters |
| --- | --- |
| Codex lifecycle **ledger** with `SESSION_BASELINE_SNAPSHOT` + turn-tree snapshots for turns 1–3+ | Proves observation came from a real session, not a unit-test fixture |
| Human-**frozen** witness digest | Predicate is reviewed; model did not assign PASS/FAIL |
| Turn proof bundle under `.faultline/turn-proof-bundles/…` | Portable experimental package |
| Offline `fl verify … --expect-root sha256:…` **MATCH** | Independent check |
| Investigation shows `introduction.turnOrdinal === 3` (or later) | Headline “First Bad Turn” beyond Turn 1 |
| Short redacted write-up you consent to publish | Impact / partner proof for Build Week |

Evidence grade will remain **`EXPERIMENTAL_TURN`** (not Git `COMMIT_PROOF`). That is correct and honest.

### For Moni

You get:

- Hands-on practice freezing a real predicate on **your** monorepo  
- A repeatable “agent broke something → find earliest bad turn” workflow  
- A concrete CI starter list tailored to Expo + Go + Supabase (below)  
- Optional: a small `tools/faultline/` (or similar) witness helper checked into Moni so the demo isn’t a one-off

### What we are *not* claiming

- FaultLine does **not** read private Codex reasoning or transcripts  
- FaultLine does **not** prove unique semantic root cause or “the agent meant to…”  
- A turn package is **not** interchangeable with mature Git `COMMIT_PROOF`  
- Success here does **not** require full mobile E2E in Docker on day one

---

## 1. Moni context (why the witness must be chosen carefully)

From Moni’s README / layout:

| Piece | Stack | FaultLine-friendly on day 1? |
| --- | --- | --- |
| `apps/mobile` | Expo / React Native, Legend-State, MMKV | **No** for Docker proof (native modules, device, secrets) |
| `apps/backend` | Go + Gin, Groq, Supabase JWT | **Partial** — `go test` / pure helpers yes; live Groq/JWKS need care |
| `apps/web` | Next.js | Possible later; deprioritized upstream |
| `packages/types` | Shared Zod schemas | **Yes** — best first surface |
| `supabase/` | Migrations / RLS | Schema SQL checks possible later; not first |

FaultLine’s proof runner:

- uses **digest-pinned** images  
- runs with **no network**  
- mounts source **read-only**  
- requires a **structured** witness outcome (`PREDICATE_PASS` / `PREDICATE_FAIL`), not bare exit codes  

So the first Moni × FaultLine demo should target a **pure, offline predicate** that can run inside `node:22-alpine` or `golang:…-alpine` **without** calling Supabase, Groq, ngrok, or Expo.

---

## 2. Prerequisites (checklist)

### Machine

- [ ] Git clone of Moni-Alpha with a clean starting commit you control  
- [ ] Node ≥ 18 (Moni) and ability to run FaultLine (Node ≥ 22 recommended for FaultLine CLI)  
- [ ] pnpm (Moni uses pnpm 9; FaultLine checkout uses its own packageManager — keep them separate)  
- [ ] Docker Desktop / daemon running  
- [ ] Codex App or CLI available in the Moni worktree  
- [ ] ~90–120 minutes for the first successful session  

### FaultLine CLI

**Option A — from FaultLine source (recommended while collaborating):**

```powershell
# In the FaultLine checkout
pnpm install --frozen-lockfile
pnpm build
$FaultLineCli = (Resolve-Path .\dist\cli.js).Path
```

**Option B — published package (when available):**

```powershell
npm install -g @mizore66/faultline
# then use `fl` instead of `node $FaultLineCli`
```

### Important CLI habit (Windows)

Do **not** insert `--` between `fl` and the subcommand:

```powershell
# good
node $FaultLineCli doctor --repo .
# bad
node $FaultLineCli -- doctor --repo .
```

If `pnpm` hits ExecutionPolicy issues, use `pnpm.cmd`.

### Consent / redaction (do this before recording)

Agree with the FaultLine team what may be published:

- [ ] Redacted ledger (no `.env`, no tokens, no real receipt images)  
- [ ] Witness command text (should be non-secret)  
- [ ] Bundle root digest + frozen witness digest  
- [ ] Optional quote for impact notes  
- [ ] Whether Moni repo name / your handle may appear  

Sidecar already avoids storing raw prompts and transcripts; still scrub `.env*` and keys from any package you zip.

---

## 3. Recommended demo design (Moni-shaped)

### 3.1 Prefer a tiny “demo surface” inside Moni

Add something like this **in Moni** (names are suggestions):

```text
tools/faultline/
  README.md
  witness.mjs                 # structured predicate (Node)
  # OR witness_test.go        # if you prefer Go image
  scenarios/
    later-turn.md             # script for the Codex session
```

**Suggested Node witness (best default):** assert a small invariant in `packages/types` (or a dedicated tiny module), e.g.:

- A Zod schema still parses a known fixture JSON  
- A money/amount helper still rejects negative amounts  
- A shared type still exports an expected discriminant  

Emit **exactly one JSON line** on stdout:

```js
// tools/faultline/witness.mjs (sketch — adapt to a real Moni module)
import { readFileSync } from "node:fs";

// Example: read a checked-in fixture and apply a real Moni validation helper.
 // Keep dependencies runnable offline inside node:22-alpine.

const protocol = "faultline.witness-result.v1";
let pass = false;
try {
  // TODO: call a pure function from packages/types (or inline a minimal check
  // that mirrors production validation you care about).
  const fixture = JSON.parse(readFileSync(new URL("./fixture-ok.json", import.meta.url), "utf8"));
  pass = fixture.amount > 0 && typeof fixture.currency === "string";
} catch {
  pass = false;
}

const outcome = pass ? "PREDICATE_PASS" : "PREDICATE_FAIL";
console.log(JSON.stringify({ protocol, outcome }));
process.exit(pass ? 0 : 1);
```

**Why this shape:**

- Runs in FaultLine’s Node catalog image with no network  
- Easy for Codex to “accidentally” break on Turn 3 (edit helper / fixture / schema)  
- Still **Moni-real** if the helper is shared production validation  

### 3.2 Alternative: Go backend unit predicate

If you prefer Go:

```powershell
# witness command concept (structured wrapper still required)
# Prefer a tiny `go test` on a pure package under apps/backend/...
```

You would:

1. `fl runtime prepare go --yes`  
2. Freeze a witness that runs a **single** offline test package and prints `faultline.witness-result.v1`  

Live Groq / JWKS tests are **out of scope** for the first proof (no network in the sandbox).

### 3.3 Target timeline for the Codex session

| Turn | Worktree intent | Witness should |
| --- | --- | --- |
| Session baseline | Predicate holds (`good`) | PASS |
| Turn 1 | Harmless refactor / comment / docs | PASS |
| Turn 2 | Another safe change | PASS |
| Turn 3 | Change that breaks the frozen predicate | FAIL ← earliest recorded stable failure |

Exact turn ordinals can be 3+. The story FaultLine wants:

```text
Session baseline  PASS
Turn 1            PASS
Turn 2            PASS
Turn 3            FAIL  ← ATTRIBUTED
```

---

## 4. One-time setup on the Moni repo

Run these from a shell where `$FaultLineCli` points at FaultLine’s built CLI, and `$MoniRepo` is your Moni-Alpha path.

```powershell
$MoniRepo = (Resolve-Path C:\path\to\Moni-Alpha).Path
Push-Location $MoniRepo

node $FaultLineCli doctor --repo .
```

Fix anything `doctor` flags (Git, Docker daemon, dirty worktree warnings for *clean* checkpoints — dirty is OK later for **turn-tree** snapshots).

### 4.1 Prepare Docker runtime

For the Node witness path:

```powershell
node $FaultLineCli runtime prepare node
# review the printed effect, then:
node $FaultLineCli runtime prepare node --yes
node $FaultLineCli runtime resolve node
```

Copy the **digest-pinned** image string (looks like `node@sha256:…`). You will pass it to `investigate turns`.

### 4.2 Install the Codex sidecar (opt-in hooks)

```powershell
# Preview only (no write):
node $FaultLineCli codex sidecar install --repo $MoniRepo --cli $FaultLineCli

# After you review the hooks document:
node $FaultLineCli codex sidecar install --repo $MoniRepo --cli $FaultLineCli --yes
```

Then:

1. Open / restart **Codex in the Moni repo**  
2. Run `/hooks`  
3. **Trust / enable** the FaultLine hook commands  

If `.codex/hooks.json` already exists, the installer will refuse to overwrite. Emit config with `codex sidecar config`, merge carefully, or ask the FaultLine team for help.

**What the sidecar records:** public lifecycle IDs/metadata + turn-tree snapshots (including dirty Stops).  
**What it does not record:** raw prompts, assistant text, transcripts.

### 4.3 Confirm recorder health

```powershell
node $FaultLineCli codex sidecar status --repo .
```

You want a valid ledger path and a healthy recorder. Keep that `ledgerPath` for later — but a **new** session after install is what you will use for the hero capture.

---

## 5. Scripted Codex session (the actual #42 capture)

Work in a **dedicated branch**, e.g. `faultline/later-turn-demo`.

### 5.1 Commit a known-good baseline

1. Land `tools/faultline/witness.mjs` + fixture so the predicate **passes**  
2. Commit on the branch  
3. Confirm locally:

```powershell
node .\tools\faultline\witness.mjs
# must print PREDICATE_PASS and exit 0
```

### 5.2 Start a fresh Codex session (hooks on)

In Codex (Moni worktree):

1. New session after hooks are trusted  
2. Do **not** disable FaultLine hooks for this session  
3. Follow the turn script below (you can paste turns as user prompts)

### 5.3 Suggested turn prompts (edit to match your witness)

**Turn 1 (must keep predicate green):**

> In `tools/faultline/`, add a short comment to `witness.mjs` explaining that this predicate is used by FaultLine. Do not change the pass/fail logic. Do not edit production packages except that comment file if needed.

Stop. Wait for sidecar Stop handling.

**Turn 2 (still green):**

> Update `tools/faultline/README.md` with how to run `node tools/faultline/witness.mjs`. Do not change validation logic.

Stop.

**Turn 3 (introduce the regression — keep it small and real):**

> Refactor the validation used by `tools/faultline/witness.mjs` so that amounts of `0` are treated as valid (or invert the amount check / break the Zod field name). It’s OK if this temporarily fails the FaultLine witness — we are capturing a regression.

Stop. Confirm `node .\tools\faultline\witness.mjs` now prints `PREDICATE_FAIL`.

> Tip: the failure should be **obvious and local**. Avoid “also rewrite half of Legend-State sync.”

### 5.4 Verify the ledger before investigating

```powershell
node $FaultLineCli codex sidecar status --repo .
# Note ledgerPath

# Optional: inspect that the ledger JSON/NDJSON contains:
# - SESSION_BASELINE_SNAPSHOT (or equivalent baseline event FaultLine extracts)
# - TURN_TREE_SNAPSHOT for ordinals 1, 2, 3
```

If baseline or turn trees are missing:

- Hooks may not be trusted  
- Session may have started before install  
- Stop may not have fired  

Start a **new** session and redo turns — do not invent snapshots by hand if you want the “real Codex” claim.

---

## 6. Freeze the witness (human in the loop)

FaultLine must not auto-freeze. Typical path:

### 6.1 Propose / intake

From Moni repo (adjust command to your real witness invocation):

```powershell
# Example shape — match FaultLine’s witness propose / incident commands
# your collaborator uses for structured overlays.
#
# Conceptually you freeze:
#   command: node tools/faultline/witness.mjs
#   overlays: none, or a reviewed overlay root
#   runtime: node (digest-pinned)

node $FaultLineCli doctor --repo .
```

Practical freeze path used in FaultLine docs:

1. `fl witness propose` (optionally `--live` with GPT-5.6 for a blinded draft — still human-reviewed)  
2. Browser or CLI review: **Approve**  
3. Separate action: **Freeze**  
4. Retain:
   - `proposal` / frozen id  
   - `sha256:…` frozen digest  

If using guided investigate later for Git CI logs, that’s a different path (`fl investigate --ci-log`). For **#42 turn hero**, you need **`fl investigate turns`** with the sidecar ledger.

Keep the frozen digest in a notepad — you will pass `--expect-digest`.

### 6.2 Sanity check before Docker spend

```powershell
# Host-side sanity only (not proof):
node .\tools\faultline\witness.mjs
```

At the broken tip this should be FAIL. Baseline trees in the ledger should still be the earlier PASS trees.

---

## 7. Run turn investigation + verify (Docker)

```powershell
$Image = "<paste node@sha256:… from runtime resolve>"
$Ledger = "<ledgerPath from sidecar status>"
$Proposal = "<frozen proposal id>"
$Digest = "sha256:<frozen digest>"

node $FaultLineCli investigate turns `
  --repo $MoniRepo `
  --ledger $Ledger `
  --proposal $Proposal `
  --expect-digest $Digest `
  --image $Image
```

Expect JSON/CLI output including:

- stable states for baseline + turns  
- one primary `PASS→FAIL`  
- `introduction.status = ATTRIBUTED`  
- `introduction.turnOrdinal = 3` (or the ordinal you actually broke)  
- package path under `.faultline/turn-proof-bundles/…`  
- printed **root** digest  

Verify:

```powershell
node $FaultLineCli verify .faultline\turn-proof-bundles\<dir> `
  --expect-root sha256:<printed-root>
```

Optional serve for screenshots:

```powershell
node $FaultLineCli serve `
  --bundle .faultline\turn-proof-bundles\<dir> `
  --expect-root sha256:<printed-root>
```

### 7.1 If investigation fails closed (common causes)

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Sandbox unavailable | Docker daemon down | Start Docker; re-run |
| Env / lockfile skew | Image can’t see deps; or fingerprints diverge | Keep witness dependency-free, or use `runtime project` mapping (ask FaultLine team) |
| UNATTRIBUTED | Missing baseline, gap in ordinals, or unstable runs | Ensure SessionStart baseline + adjacent snapshots; re-session |
| Legacy EXIT_* rejected | Witness didn’t print structured protocol line | Fix witness to emit `faultline.witness-result.v1` |
| INCOMPATIBLE | Predicate can’t run on older trees | Don’t depend on files only added after baseline |

---

## 8. Hand artifacts back to FaultLine (definition of done for #42)

Package a folder (zip or PR to FaultLine) with:

```text
moni-later-turn-handoff/
  README.md                 # your short narrative + consent notes
  ROOT.sha256               # expect-root
  frozen-digest.txt         # witness digest
  image.txt                 # node@sha256:… used
  ledger/                   # redacted copy of the lifecycle ledger
  turn-proof-bundle/        # full verify-able package directory
  screenshots/              # optional: serve UI, sidecar status
```

FaultLine will typically:

1. Copy into `docs/samples/later-turn-hero/` (or similar)  
2. Update `docs/later-turn-incident.md` with digests + Moni attribution (per your consent)  
3. Keep grade labeled **`EXPERIMENTAL_TURN`**

### Suggested README blurb for the handoff

```markdown
# Moni × FaultLine later-turn handoff

- Repo: https://github.com/Kaiz404/Moni-Alpha
- Branch / commit tip: <sha>
- Session: Codex + FaultLine sidecar (public hooks only)
- Story: baseline PASS, turns 1–2 PASS, turn 3 FAIL
- introduction.turnOrdinal: 3
- Frozen witness: sha256:…
- Turn package root: sha256:…
- Image: node@sha256:…
- Consent: <what may be published>
```

---

## 9. Optional second beat — Git `COMMIT_PROOF` on Moni (not required for #42)

After you have CI (next section), you can also:

1. Capture a real CI failure log  
2. `fl investigate --ci-log ci.log --repo . --command "<predicate>" --runtime node|go`  
3. Approve → Freeze → Docker replay → `COMMIT_PROOF` package  

That is the **mature** portable path. Use it for Moni’s own incident history; use the turn package for FaultLine’s Codex-native story.

---

## 10. Starter CI/CD for Moni-Alpha (phased)

You said CI/CD comes later — here’s a practical ladder that matches Moni’s monorepo. Implement **Phase 0 → 1** before anything fancy.

### Phase 0 — “don’t merge broken basics” (1 workflow)

**Triggers:** PR + push to `main`  
**Runner:** `ubuntu-latest`

Jobs (can be one job initially):

1. **Checkout + pnpm install** (`pnpm install --frozen-lockfile`)  
2. **Format check:** `pnpm format:check`  
3. **Typecheck:** `pnpm check-types`  
4. **Lint:** `pnpm lint` (you already exclude web in scripts — keep that until web is alive)  
5. **Go vet/test:**  
   ```bash
   cd apps/backend
   go vet ./...
   go test ./...
   ```

**Do not** put Expo E2E or Cloud Run deploys in Phase 0.

**Secrets:** none required if tests are offline. Never put `GROQ_API_KEY` / Supabase service keys in PR logs.

### Phase 1 — backend contract hardening

Add to the Go job:

- `go test ./... -race` (when stable)  
- A tiny **httptest** fixture test for `/healthz` with the Gin engine in-process (no ngrok)  
- Optional: `golangci-lint`  

Add a **witness-friendly** unit around extraction DTO validation (pure), so FaultLine and CI share the same invariant.

### Phase 2 — Supabase / SQL safety

On PRs that touch `supabase/`:

- `supabase db lint` or migration dry-run in CI (Supabase CLI)  
- Policy smoke: “RLS enabled on public tables” check script  
- **No** production migrate from PRs  

### Phase 3 — mobile quality gates (still not full E2E)

- `pnpm --filter moni` typecheck/lint only  
- Optional: Maestro/Detox later on a nightly schedule — not on every PR at first  
- Cache Expo / pnpm aggressively or CI will be slow  

### Phase 4 — deploy pipelines (separate from PR checks)

| Pipeline | When | Notes |
| --- | --- | --- |
| Backend → Cloud Run | Push to `main` after tests green | Use Workload Identity / secrets; keep `--allow-unauthenticated` only if JWT auth stays in-app |
| Mobile → EAS Preview | Labels / manual dispatch | Don’t auto-ship store builds from every commit |
| Web | Deferred | Match product priority |

### Phase 5 — FaultLine in Moni CI (optional, advanced)

Only after you like the workflow locally:

- Nightly or manual workflow: `fl doctor --json`  
- On selected incidents: retain proof bundles as artifacts  
- Do **not** fail the whole monorepo CI because Docker proof flakes — keep proof jobs `continue-on-error: false` only on a dedicated workflow  

Example future job sketch:

```yaml
# .github/workflows/faultline-doctor.yml (future)
name: faultline-doctor
on:
  workflow_dispatch:
jobs:
  doctor:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
      - run: npm install -g @mizore66/faultline
      - run: fl doctor --repo . --json
```

### Suggested first GitHub Actions file (copy/adapt)

```yaml
# .github/workflows/ci.yml — Phase 0 starter for Moni-Alpha
name: ci
on:
  pull_request:
  push:
    branches: [main]

jobs:
  js-quality:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
        with:
          version: 9
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: "pnpm"
      - run: pnpm install --frozen-lockfile
      - run: pnpm format:check
      - run: pnpm check-types
      - run: pnpm lint

  go-backend:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: apps/backend
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26.x"
      - run: go vet ./...
      - run: go test ./...
```

---

## 11. Facilitation checklist (FaultLine teammate + Moni dev)

Use this during the live session:

1. [ ] Consent / redaction rules agreed  
2. [ ] `doctor` green enough for Docker proof  
3. [ ] Sidecar installed + trusted in Codex `/hooks`  
4. [ ] Known-good witness committed  
5. [ ] Fresh Codex session: baseline + T1 PASS + T2 PASS + T3 FAIL  
6. [ ] `sidecar status` shows ledger with baseline + 3 turn trees  
7. [ ] Witness frozen (Approve ≠ Freeze)  
8. [ ] `investigate turns` completes; Turn 3 attributed  
9. [ ] `verify --expect-root` MATCH  
10. [ ] Handoff zip / PR prepared  
11. [ ] Optional: 2–3 screenshots for Build Week video  
12. [ ] Moni CI Phase 0 opened as a follow-up PR (can be after the demo)

---

## 12. Claim language you can use on camera / in Discord

**Good:**

> We ran FaultLine on Moni-Alpha with the opt-in Codex sidecar. Under a human-frozen witness, the earliest recorded stable PASS→FAIL was Turn 3. The turn package verifies offline as experimental evidence.

**Avoid:**

> FaultLine proved the agent’s intent / found the unique root cause / this is COMMIT_PROOF / Moni CI is fully FaultLine-powered.

---

## 13. Contacts / pointers inside FaultLine

| Doc | Use |
| --- | --- |
| [../later-turn-incident.md](../later-turn-incident.md) | What #42 is trying to show |
| [../first-incident.md](../first-incident.md) | General first-incident path |
| [../codex-sidecar.md](../codex-sidecar.md) | Hooks install / status / limits |
| [../witness-protocol.md](../witness-protocol.md) | Propose → approve → freeze |
| [../proof-bundles.md](../proof-bundles.md) | Verify / serve / grades |
| [../external-participant-invite.md](../external-participant-invite.md) | Consent template |
| [../impact-validation-external-01.md](../impact-validation-external-01.md) | Fill after the session |

---

## 14. Quick FAQ

**Q: Can we demo on the Expo app directly?**  
A: Not for the first Docker proof. Use a pure `tools/faultline` / `packages/types` predicate. Mobile E2E belongs in later CI phases, not the FaultLine sandbox.

**Q: Do I need Groq or Supabase keys for FaultLine?**  
A: No for #42. Keep the witness offline.

**Q: Dirty worktree during Codex turns?**  
A: Expected. Turn-tree snapshots are for that. Clean Git checkpoints are a separate sidecar feature.

**Q: What if Turn 3 isn’t attributed?**  
A: Send the ledger + investigation JSON to the FaultLine team — attribution beyond Turn 1 is fixed on FaultLine `main`, but missing baseline / gaps still yield `UNATTRIBUTED`.

**Q: Should Moni depend on FaultLine in `package.json`?**  
A: Not required. Treat FaultLine as an external CLI for the collab; optional later.

---

*Last updated for FaultLine collaborators targeting Build Week issue #42 with Moni-Alpha as the external real-repo partner.*
