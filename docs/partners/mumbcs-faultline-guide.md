# MUMBCS × FaultLine — partner runbook (later-turn hero + starter CI)

**Audience:** developer / maintainer of [monashblockchain/MUMBCS](https://github.com/monashblockchain/MUMBCS) (MUMBCS website).  
**FaultLine repo:** [Mizore66/faultline](https://github.com/Mizore66/faultline)  
**Goal:** Run FaultLine on a **real MUMBCS Codex session** so FaultLine can publish a later-turn **First Bad Turn** evidence package (`EXPERIMENTAL_TURN`) with retained digests — closing the “real ledger” gap for FaultLine issue **#42**. Separately: practical CI/CD suggestions for MUMBCS.

This is a collaboration guide, not a claim that FaultLine is already part of MUMBCS production.

> **Access note:** MUMBCS is a **private** repo (`master` default branch). The FaultLine collaborator needs read access (or a redacted handoff zip) to help verify packages.

---

## 0. Why MUMBCS (vs a mobile app)

MUMBCS is a **Next.js 15 + Payload CMS + TypeScript** club website (`mumbcs-web`) with Playwright route checks already in-tree. That is much easier to bring into FaultLine’s Docker proof path than an Expo/React Native app:

| Concern | Mobile app | MUMBCS |
| --- | --- | --- |
| Proof sandbox | Native modules, device, secrets | Node image is enough for pure predicates |
| Install story | EAS / Android toolchains | `pnpm` + optional Mongo later |
| Existing tests | Often device-bound | Playwright HTTP checks + lintable TS |
| Day-1 witness | Hard | Pure `src/utils` / small fixture — easy |

You still should **not** try to freeze “boot full Payload + Mongo + Vercel Blob in the FaultLine sandbox” on the first attempt. Start with an offline predicate; grow CI around the real stack afterward.

---

## 1. What success looks like

### For FaultLine (#42)

| Artifact | Why it matters |
| --- | --- |
| Codex lifecycle **ledger** with `SESSION_BASELINE_SNAPSHOT` + turn-tree snapshots for turns 1–3+ | Real session, not a unit-test fixture |
| Human-**frozen** witness digest | Model never assigns PASS/FAIL |
| Turn proof bundle under `.faultline/turn-proof-bundles/…` | Portable experimental package |
| Offline `fl verify … --expect-root sha256:…` **MATCH** | Independent check |
| `introduction.turnOrdinal === 3` (or later) | First Bad Turn beyond Turn 1 |
| Consent for redacted publish | Build Week impact / partner proof |

Grade stays **`EXPERIMENTAL_TURN`** (not Git `COMMIT_PROOF`). That is honest.

### For MUMBCS

- Practice freezing a real predicate on the club site repo  
- A repeatable “Codex broke something → earliest bad turn” workflow  
- Clear CI ladder (lint/typecheck → Playwright → optional Docker Compose)  
- Optional `tools/faultline/` helper checked into MUMBCS so the demo is repeatable  

### What we are *not* claiming

- FaultLine does **not** read private Codex reasoning or transcripts  
- FaultLine does **not** prove unique semantic root cause or agent intent  
- A turn package is **not** interchangeable with mature Git `COMMIT_PROOF`  
- Success does **not** require live Mongo/Payload inside the first Docker proof  

---

## 2. MUMBCS context (pick the right witness)

Observed layout (private `master`):

| Piece | Role | FaultLine-friendly day 1? |
| --- | --- | --- |
| `src/app`, `src/components` | Next.js UI | Later (build-heavy) |
| `src/collections`, `payload.config.ts` | Payload CMS | **No** without Mongo + env |
| `src/utils/date-utils.ts`, `src/lib/utils.ts` | Pure helpers | **Yes — best first surface** |
| `tests/status-check.spec.ts` | Playwright route 200s | Great for **CI**; needs a running server (not first FaultLine sandbox witness) |
| `.github/workflows/playwright.yml` | Existing CI | Extend; don’t replace blindly |
| Mongo / Vercel Blob / Resend / better-auth | Runtime services | Keep **out** of the frozen witness |

FaultLine’s proof runner:

- digest-pinned images  
- **no network**  
- source mount **read-only**  
- structured outcomes only: `PREDICATE_PASS` / `PREDICATE_FAIL` (`faultline.witness-result.v1`)

So the first MUMBCS × FaultLine demo should target a **pure, offline Node predicate** that runs in `node:22-alpine` without Mongo, Auth, Blob, or `next start`.

---

## 3. Prerequisites

### Machine

- [ ] Clone of MUMBCS with a branch you control (e.g. `faultline/later-turn-demo`)  
- [ ] Node matching MUMBCS engines (`^18.20.2 \|\| >=20.9.0`; **22 recommended** for FaultLine CLI)  
- [ ] pnpm 9 or 10 (MUMBCS) — keep separate from FaultLine’s own packageManager  
- [ ] Docker Desktop / daemon running  
- [ ] Codex App or CLI opened on the **MUMBCS** worktree  
- [ ] ~90–120 minutes for the first successful capture  

### FaultLine CLI

**From FaultLine source (recommended while collaborating):**

```powershell
# In the FaultLine checkout
pnpm install --frozen-lockfile
pnpm build
$FaultLineCli = (Resolve-Path .\dist\cli.js).Path
```

**Published package (when available):**

```powershell
npm install -g @mizore66/faultline
# then use `fl` instead of `node $FaultLineCli`
```

### Windows CLI habit

Do **not** insert `--` between the CLI and the subcommand:

```powershell
# good
node $FaultLineCli doctor --repo .
# bad
node $FaultLineCli -- doctor --repo .
```

### Consent / redaction

Agree before recording:

- [ ] Redacted ledger (no `.env`, Mongo URIs, Blob tokens, auth secrets)  
- [ ] Witness command text (keep it non-secret)  
- [ ] Bundle root + frozen witness digests  
- [ ] Whether “MUMBCS” / org name / handle may appear publicly  
- [ ] Optional quote for FaultLine impact notes  

---

## 4. Recommended demo design (MUMBCS-shaped)

### 4.1 Add a tiny demo surface in MUMBCS

```text
tools/faultline/
  README.md
  witness.mjs
  fixture-ok.json          # optional
  scenarios/later-turn.md  # Codex turn script
```

**Suggested witness:** assert a real pure helper (prefer something under `src/utils/` such as date formatting / URL helpers), or a tiny checked-in fixture that mirrors a club invariant (e.g. committee slug rules, FAQ shape).

Emit **exactly one JSON line** on stdout:

```js
// tools/faultline/witness.mjs (sketch — wire to a real MUMBCS pure helper)
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

// Prefer importing a compiled/plain helper that needs no Mongo/Payload.
 // If TS import is awkward in Alpine without a build step, duplicate a
 // 10-line pure check that matches production logic for the demo.

const protocol = "faultline.witness-result.v1";
let pass = false;
try {
  const fixture = JSON.parse(
    readFileSync(new URL("./fixture-ok.json", import.meta.url), "utf8")
  );
  // Example invariant — replace with a real MUMBCS rule:
  pass =
    typeof fixture.slug === "string"
    && /^[a-z0-9-]+$/.test(fixture.slug)
    && fixture.slug.length > 0;
} catch {
  pass = false;
}

const outcome = pass ? "PREDICATE_PASS" : "PREDICATE_FAIL";
console.log(JSON.stringify({ protocol, outcome }));
process.exit(pass ? 0 : 1);
```

**Why this shape:**

- Runs in FaultLine’s Node catalog image with no network  
- Easy for Codex to break on Turn 3 (loosen the slug regex, invert a date check, rename a required field)  
- Still **MUMBCS-real** if it mirrors a production validation rule  

### 4.2 What not to freeze first

| Avoid as first witness | Why |
| --- | --- |
| `pnpm build` / full Next build | Slow, env-heavy, brittle in no-network sandbox |
| Playwright against `next start` | Needs server + often DB/content |
| Payload seed / Mongo scripts | Needs credentials + network/DB |
| Auth / passkey flows | Secrets + external services |

Those belong in **CI phases** below, not in the first FaultLine turn package.

### 4.3 Target Codex timeline

| Moment | Worktree intent | Witness |
| --- | --- | --- |
| Session baseline | Predicate holds | PASS |
| Turn 1 | Harmless comment / docs | PASS |
| Turn 2 | Another safe change | PASS |
| Turn 3 | Break the frozen predicate | FAIL ← earliest recorded stable failure |

Story FaultLine wants:

```text
Session baseline  PASS
Turn 1            PASS
Turn 2            PASS
Turn 3            FAIL  ← ATTRIBUTED
```

---

## 5. One-time setup on the MUMBCS repo

```powershell
$MumbcsRepo = (Resolve-Path C:\path\to\MUMBCS).Path
Push-Location $MumbcsRepo

node $FaultLineCli doctor --repo .
```

### 5.1 Prepare Docker runtime (Node)

```powershell
node $FaultLineCli runtime prepare node
node $FaultLineCli runtime prepare node --yes
node $FaultLineCli runtime resolve node
```

Copy the digest-pinned image (`node@sha256:…`).

### 5.2 Install the Codex sidecar

```powershell
node $FaultLineCli codex sidecar install --repo $MumbcsRepo --cli $FaultLineCli
node $FaultLineCli codex sidecar install --repo $MumbcsRepo --cli $FaultLineCli --yes
```

Then in Codex on MUMBCS:

1. Restart / reopen the project  
2. Run `/hooks`  
3. **Trust / enable** FaultLine hook commands  

If `.codex/hooks.json` already exists, the installer refuses to overwrite — merge via `codex sidecar config` output.

### 5.3 Confirm recorder health

```powershell
node $FaultLineCli codex sidecar status --repo .
```

Note `ledgerPath`. Use a **fresh session** after install for the hero capture.

---

## 6. Scripted Codex session (#42 capture)

Work on a dedicated branch, e.g. `faultline/later-turn-demo`.

### 6.1 Commit known-good baseline

1. Land `tools/faultline/witness.mjs` (+ fixture) so the predicate **passes**  
2. Commit  
3. Confirm:

```powershell
node .\tools\faultline\witness.mjs
# PREDICATE_PASS, exit 0
```

### 6.2 Fresh Codex session (hooks on)

**Turn 1 (keep green):**

> In `tools/faultline/`, add a short comment to `witness.mjs` explaining this predicate is used by FaultLine. Do not change pass/fail logic.

**Turn 2 (still green):**

> Update `tools/faultline/README.md` with how to run `node tools/faultline/witness.mjs`. Do not change validation logic.

**Turn 3 (introduce regression):**

> Change the validation used by `tools/faultline/witness.mjs` so the fixture fails (e.g. allow empty slugs, invert the date/slug check, or rename a required field). It’s OK if the FaultLine witness fails — we are capturing a regression.

Stop after each turn. Confirm host-side:

```powershell
node .\tools\faultline\witness.mjs   # should be PREDICATE_FAIL after Turn 3
```

### 6.3 Verify the ledger

```powershell
node $FaultLineCli codex sidecar status --repo .
```

Confirm the ledger contains:

- session baseline snapshot  
- `TURN_TREE_SNAPSHOT` (or equivalent) for ordinals 1, 2, 3  

If missing: hooks not trusted / session started too early / Stop didn’t fire → **new session**, don’t hand-edit a “fake” Codex ledger if you want the real-session claim.

---

## 7. Freeze the witness (human in the loop)

FaultLine must not auto-freeze.

1. Propose a witness whose command is effectively `node tools/faultline/witness.mjs`  
2. Human **Approve**  
3. Separate action: **Freeze**  
4. Retain proposal id + `sha256:…` frozen digest  

Optional: `fl witness propose --live` (GPT-5.6 blinded draft) — still human-reviewed before freeze.

Host sanity (not proof):

```powershell
node .\tools\faultline\witness.mjs
```

---

## 8. Turn investigation + verify (Docker)

```powershell
$Image = "<node@sha256:… from runtime resolve>"
$Ledger = "<ledgerPath from sidecar status>"
$Proposal = "<frozen proposal id>"
$Digest = "sha256:<frozen digest>"

node $FaultLineCli investigate turns `
  --repo $MumbcsRepo `
  --ledger $Ledger `
  --proposal $Proposal `
  --expect-digest $Digest `
  --image $Image
```

Expect:

- stable baseline + turn states  
- `PASS→FAIL` with `introduction.status = ATTRIBUTED`  
- `introduction.turnOrdinal = 3` (or your break turn)  
- package under `.faultline/turn-proof-bundles/…`  
- printed **root** digest  

```powershell
node $FaultLineCli verify .faultline\turn-proof-bundles\<dir> `
  --expect-root sha256:<printed-root>

node $FaultLineCli serve `
  --bundle .faultline\turn-proof-bundles\<dir> `
  --expect-root sha256:<printed-root>
```

### Common fail-closed cases

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Sandbox unavailable | Docker down | Start daemon; retry |
| Env / lockfile skew | Witness needs `node_modules` or lockfile changed across turns | Keep witness dependency-free (plain `.mjs`) |
| UNATTRIBUTED | Missing baseline / ordinal gaps | Re-session with hooks trusted |
| EXIT_* rejected | No structured JSON line | Emit `faultline.witness-result.v1` |
| INCOMPATIBLE | Trees missing witness files | Add `tools/faultline` before baseline |

---

## 9. Hand artifacts back to FaultLine

```text
mumbcs-later-turn-handoff/
  README.md
  ROOT.sha256
  frozen-digest.txt
  image.txt
  ledger/                 # redacted
  turn-proof-bundle/      # full verify-able directory
  screenshots/            # optional
```

Example handoff README:

```markdown
# MUMBCS × FaultLine later-turn handoff

- Repo: https://github.com/monashblockchain/MUMBCS (private)
- Branch / tip: <sha>
- Session: Codex + FaultLine sidecar (public hooks only)
- Story: baseline PASS, turns 1–2 PASS, turn 3 FAIL
- introduction.turnOrdinal: 3
- Frozen witness: sha256:…
- Turn package root: sha256:…
- Image: node@sha256:…
- Consent: <what may be published>
```

FaultLine will typically land this under `docs/samples/later-turn-hero/` and update `docs/later-turn-incident.md`, still labeled **`EXPERIMENTAL_TURN`**.

---

## 10. Optional second beat — Git `COMMIT_PROOF`

Once CI exists and you have a real failing log:

```powershell
node $FaultLineCli investigate --ci-log .\ci.log `
  --repo . `
  --command "node tools/faultline/witness.mjs" `
  --runtime node
```

Approve → Freeze → Docker replay → mature **`COMMIT_PROOF`** package. Use that for MUMBCS incident history; keep the turn package for the Codex-native story.

---

## 11. Starter CI/CD for MUMBCS (phased)

You already have `.github/workflows/playwright.yml` and `tests/status-check.spec.ts` (route 200s for `/`, `/team`, `/events`, `/blog`). Build outward from that.

### Phase 0 — PR quality gate (add beside Playwright)

**Triggers:** `pull_request` + `push` to `master`  
**Job ideas:**

```yaml
# sketch — adapt into .github/workflows/ci.yml
name: ci
on:
  pull_request:
  push:
    branches: [master]

jobs:
  quality:
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
      - run: pnpm lint
      - run: pnpm exec tsc --noEmit
      # optional demo invariant (once tools/faultline exists):
      # - run: node tools/faultline/witness.mjs
```

Keep secrets out of PR logs (`.env`, Mongo, Blob, Resend, auth).

### Phase 1 — harden Playwright

Your current tests hit live routes via `request.get`. Make CI deterministic:

1. **Preview/deploy URL** job (Vercel preview) *or*  
2. **Ephemeral app** in CI:

```bash
# conceptual
pnpm build
pnpm start &   # needs env + often Mongo — only if you provide CI services
pnpm exec playwright test
```

Practical recommendation for MUMBCS:

- Keep Playwright on **Vercel preview URL** (`PLAYWRIGHT_BASE_URL`) for PRs when previews exist  
- Or split: “smoke against production/staging” on `workflow_dispatch` / nightly  
- Don’t block every PR on a flaky full Payload boot until Compose is solid  

### Phase 2 — Docker Compose for local + CI integration

Add `docker-compose.yml` for Mongo (and only what you need):

```yaml
# sketch
services:
  mongo:
    image: mongo:7
    ports: ["27017:27017"]
    volumes: ["mumbcs_mongo:/data/db"]
volumes:
  mumbcs_mongo:
```

CI job (later):

- start Compose  
- apply seed scripts if safe (`seed:faq` / committees) with **CI-only** env  
- run a small API/content smoke  
- tear down  

Still keep FaultLine’s **frozen witness** on the pure `tools/faultline` predicate so proofs stay offline.

### Phase 3 — Payload / content safety

On PRs touching `src/collections` or `payload.config.ts`:

- `pnpm generate:types` drift check (fail if generated types weren’t committed)  
- Optional: GraphQL schema dump diff  

Do **not** run `reset:db` against shared environments from CI.

### Phase 4 — deploy

| Pipeline | When | Notes |
| --- | --- | --- |
| Vercel production | Push to `master` after CI green | Keep env in Vercel; never echo secrets |
| Preview deployments | Every PR | Feed `PLAYWRIGHT_BASE_URL` for smoke |
| Blob / media scripts | Manual / ops | `download:blob` / cleanup stay human-triggered |

### Phase 5 — optional FaultLine job

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

Keep proof-grade Docker investigate on a **manual** workflow until stable — don’t flake the whole site CI on it.

### Suggested near-term priority order for MUMBCS

1. `ci.yml`: lint + `tsc --noEmit` on every PR  
2. Make Playwright URL-configurable; attach to preview or nightly  
3. `docker-compose` Mongo for contributors  
4. Types drift check for Payload  
5. Optional FaultLine doctor / witness script in CI  

---

## 12. Facilitation checklist

1. [ ] Consent / redaction agreed (private repo → what can be public)  
2. [ ] `doctor` OK for Docker proof  
3. [ ] Sidecar installed + trusted in Codex `/hooks`  
4. [ ] Known-good `tools/faultline` witness committed  
5. [ ] Fresh session: baseline + T1/T2 PASS + T3 FAIL  
6. [ ] Ledger has baseline + 3 turn trees  
7. [ ] Witness frozen (Approve ≠ Freeze)  
8. [ ] `investigate turns` attributes Turn 3  
9. [ ] `verify --expect-root` MATCH  
10. [ ] Handoff zip / shared folder prepared  
11. [ ] Optional screenshots for Build Week video  
12. [ ] Follow-up: Phase 0 `ci.yml` PR on MUMBCS  

---

## 13. Claim language

**Good:**

> We ran FaultLine on the MUMBCS Next.js site with the opt-in Codex sidecar. Under a human-frozen witness, the earliest recorded stable PASS→FAIL was Turn 3. The turn package verifies offline as experimental evidence.

**Avoid:**

> FaultLine proved the agent’s intent / unique root cause / this is COMMIT_PROOF / MUMBCS production is FaultLine-powered / we replayed full Payload+Mongo in the sandbox on day one.

---

## 14. Pointers inside FaultLine

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

## 15. FAQ

**Q: Why not Playwright as the FaultLine witness?**  
A: Needs a running server (and often DB/content). Perfect for CI; awkward for the first no-network Docker proof. Use `tools/faultline/witness.mjs` for FaultLine; keep Playwright in GitHub Actions.

**Q: Do we need Mongo for #42?**  
A: No. Keep the frozen witness offline.

**Q: Repo is private — can FaultLine still publish digests?**  
A: Yes, with consent: publish digests + redacted narrative; keep ledger/bundle in a limited handoff or private sample path.

**Q: Dirty worktrees during Codex turns?**  
A: Expected. Turn-tree snapshots handle dirty Stops.

**Q: Should MUMBCS depend on FaultLine in `package.json`?**  
A: Not required. Treat FaultLine as an external CLI for the collab.

---

*Partner runbook for FaultLine Build Week issue #42 using MUMBCS as the Docker-friendly external website repo.*
