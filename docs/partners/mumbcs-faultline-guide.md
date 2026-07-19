# MUMBCS × FaultLine — partner runbook (later-turn hero + starter CI)

**Audience:** developer / maintainer of [monashblockchain/MUMBCS](https://github.com/monashblockchain/MUMBCS) (MUMBCS website).  
**FaultLine repo:** [Mizore66/faultline](https://github.com/Mizore66/faultline)  
**Goal:** Run FaultLine on a **real MUMBCS Codex session** so FaultLine can publish a later-turn **First Bad Turn** evidence package (`EXPERIMENTAL_TURN`) with retained digests — closing the external validation gap (issues **#42** / **#46** / **#55**). Separately: practical CI/CD suggestions for MUMBCS.

This is a collaboration guide, not a claim that FaultLine is already part of MUMBCS production.

**What this run proves (honest framing):**  
*An independent developer successfully used FaultLine on their repository to record a real Codex session and independently verify a later-turn boundary* (**protocol / interoperability validation**).

**What this scripted run does *not* prove:**  
That FaultLine “caught a real bug in production.” A second, naturally occurring incident would be needed for that impact claim.

**Mandatory evidence rule:** the frozen witness **overlay bytes** must be identical on every turn state. Codex must change **production code/data only** — never the witness. Fill [../external-case-study-template.md](../external-case-study-template.md) with digests, timings, onboarding friction, one criticism, and one approved quote.

**#46 landed (permissioned publish):** the 2026-07-19 MUMBCS protocol validation is
published in-repo as [../publishable-summary-mumbcs.md](../publishable-summary-mumbcs.md),
[../external-case-study-mumbcs.md](../external-case-study-mumbcs.md), and
[../impact-validation-external-01.md](../impact-validation-external-01.md) (`Status: completed`).
Future partner runs still use [../external-case-study-template.md](../external-case-study-template.md).

**Not the case study:** [../samples/later-turn-ledger/](../samples/later-turn-ledger/) is a
recorded CI fixture (`RECORDED_REDACTED_FIXTURE`), not a MUMBCS session export.

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

### For FaultLine (#42 / #46)

| Artifact / field | Why it matters |
| --- | --- |
| Codex lifecycle **ledger** with baseline + turn-tree snapshots (turns 1–3+) | Real session, not a unit-test fixture |
| Human-**frozen** witness digest | Model never assigns PASS/FAIL |
| Turn proof bundle + `fl verify --expect-root` **MATCH** | Independent check |
| `introduction.turnOrdinal` (e.g. 3) + transition kind | First Bad Turn beyond Turn 1 |
| Optional `fl investigate turns … --minimize` / `fl prove transition` | Chains turn boundary → counterfactual edit isolation |
| Immutable overlay + production-only Turn 3 | Same frozen witness across states (#55/#56) |
| Structured case-study fields + criticism + approved quote | Protocol-validation testimony — not generic praise |
| Explicit consent for private-repo redaction | Required before any public write-up |

Grade stays **`EXPERIMENTAL_TURN`**. Publish as **protocol validation** unless the failure was organic.

### Case-study fields you must capture (not optional)

Copy into [../external-case-study-template.md](../external-case-study-template.md):

- Repository language and approximate size  
- Whether the repository can be **named** publicly  
- Original symptom  
- Setup time and any assistance required  
- Number of recorded turns or states  
- Frozen-witness digest  
- Located transition  
- Proof-package root digest  
- Whether the result changed the developer’s diagnosis  
- Time to locate the boundary  
- Codex repair outcome (if any)  
- One approved direct quote  
- Rough edges or failures encountered  

**Private repo rule:** publish a redacted incident summary, scrubbed terminal capture, proof-root digest, and named/anonymized quote **with permission**. Do not publish source snapshots or witness material without the owner’s approval.

### Onboarding metrics (product experience — Sol priority)

Attempt as much as possible **from the FaultLine README with minimal help**. Record:

| Metric | Your note |
| --- | --- |
| Time to install | |
| Time to proof-ready | |
| Commands before first useful result | |
| Where you became confused | |
| Witness creation understandable? | |
| Docker / runtime preparation blocked you? | |
| Did the result change your next action? | |

**Weak quote:** “It worked well on my project.”  
**Strong quote:** “I installed it without help, reached a verified boundary in N minutes, and it identified an edit I had blamed incorrectly.”

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

## 4. Recommended demo design (immutable overlay + real production)

### 4.0 Correctness model (mandatory — Sol #55 / #56)

FaultLine freezes **overlay bytes + command**, then replays that **same** predicate on every historical tree. If Codex edits the witness file on Turn 3, each state carries a *different* predicate and the central claim collapses.

```text
Frozen overlay witness  (.faultline-witness/check-slug.mjs)  ← identical on every state
        │
        └── imports / calls real production behavior (src/utils/slug.ts)

Turn 1  → harmless docs/comment (not the witness)
Turn 2  → another harmless change (not the witness)
Turn 3  → changes production code or production data ONLY
```

**Codex must never edit:**

- the witness / overlay file  
- expected output strings  
- fixtures used solely by the witness  
- the structured-result emitter (`faultline.witness-result.v1` print path)  

**Do not duplicate production logic inside the witness** for the published case study. A copied regex proves the copy, not MUMBCS. Require one of:

- `import` the real helper (Node 22 `--experimental-strip-types` can load `.ts` in `node:22-alpine`)  
- compile the helper into the prepared image  
- invoke a real CLI/API contract  
- inspect a real generated artifact  
- call a production validation function  

A duplicated ten-line rule is OK only as a *mechanical* FaultLine exercise — **not** for external testimony that claims meaningful product validation.

### 4.1 Files to add in MUMBCS

```text
src/utils/slug.ts                 ← production contract Codex may break on Turn 3
.faultline-witness/check-slug.mjs ← frozen overlay (never edited after freeze)
.faultline-witness/README.md
tools/faultline/scenarios/later-turn.md
```

**Production helper (example — adapt to a real MUMBCS rule if you already have one):**

```ts
// src/utils/slug.ts
export function isValidSlug(value: string): boolean {
  return typeof value === "string" && /^[a-z0-9-]+$/.test(value) && value.length > 0;
}
```

**Frozen overlay (imports production — no shadow regex):**

```js
// .faultline-witness/check-slug.mjs
import { isValidSlug } from "../src/utils/slug.ts";

const protocol = "faultline.witness-result.v1";
const sample = "committee-2026";
let pass = false;
try {
  pass = isValidSlug(sample) === true && isValidSlug("") === false;
} catch {
  pass = false;
}
const outcome = pass ? "PREDICATE_PASS" : "PREDICATE_FAIL";
console.log(JSON.stringify({ protocol, outcome }));
process.exit(pass ? 0 : 1);
```

**Frozen command (host + Docker):**

```text
node --experimental-strip-types .faultline-witness/check-slug.mjs
```

Prefer an existing pure helper under `src/utils/` (e.g. date helpers) if it already encodes a club invariant — same pattern: overlay imports it; Codex breaks *that* module on Turn 3.

### 4.2 What not to freeze first

| Avoid as first witness | Why |
| --- | --- |
| `pnpm build` / full Next build | Slow, env-heavy, brittle in no-network sandbox |
| Playwright against `next start` | Needs server + often DB/content |
| Payload seed / Mongo scripts | Needs credentials + network/DB |
| Auth / passkey flows | Secrets + external services |
| Editing the overlay after freeze | Breaks “same frozen witness” |

### 4.3 Target Codex timeline

| Moment | Worktree intent | Frozen overlay |
| --- | --- | --- |
| Session baseline | Production helper correct | PASS |
| Turn 1 | Harmless docs/comment | PASS |
| Turn 2 | Another harmless change | PASS |
| Turn 3 | Break `src/utils/slug.ts` (or real helper) only | FAIL |

```text
Session baseline  PASS
Turn 1            PASS
Turn 2            PASS
Turn 3            FAIL  ← ATTRIBUTED (same overlay bytes every state)
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

## 6. Freeze the overlay **before** the Codex session

Work on a dedicated branch, e.g. `faultline/later-turn-demo`.

### 6.1 Commit known-good production + overlay

1. Land `src/utils/slug.ts` (or chosen real helper) in a **passing** state  
2. Land `.faultline-witness/check-slug.mjs` that imports it  
3. Commit  
4. Confirm:

```powershell
node --experimental-strip-types .\.faultline-witness\check-slug.mjs
# PREDICATE_PASS, exit 0
```

### 6.2 Human freeze (Approve ≠ Freeze)

FaultLine must not auto-freeze. Freeze **before** the scripted Codex turns whenever practical so the overlay cannot drift.

1. Propose a witness with:
   - **command:** `node --experimental-strip-types .faultline-witness/check-slug.mjs`
   - **overlays:** copy `.faultline-witness/check-slug.mjs` into the proposal overlay root as the same relative path (so every replayed tree gets identical overlay bytes even if someone later dirties the worktree copy)
2. Human **Approve**  
3. Separate action: **Freeze**  
4. Retain proposal id + `sha256:…` frozen digest  

Optional: `fl witness propose --live` — still human-reviewed before freeze.

After freeze, treat `.faultline-witness/check-slug.mjs` as **read-only for the rest of the session**.

---

## 7. Scripted Codex session (#42 protocol capture)

Hooks on. Fresh Codex session after freeze.

### 7.1 Turn prompts (production-only edits)

**Turn 1 (keep green):**

> Add a one-line comment to `src/utils/slug.ts` documenting that FaultLine exercises `isValidSlug`. Do **not** change the function body. Do **not** edit anything under `.faultline-witness/`.

**Turn 2 (still green):**

> Update `tools/faultline/scenarios/later-turn.md` (or a short README) describing the protocol-validation session. Do **not** edit `.faultline-witness/` or change `isValidSlug` behavior.

**Turn 3 (introduce regression in production only):**

> Change `src/utils/slug.ts` so `isValidSlug` incorrectly accepts an empty string (or inverts the regex). Do **not** edit `.faultline-witness/`, fixtures, or any FaultLine overlay. We are capturing a protocol regression under a frozen witness.

Stop after each turn. Confirm host-side with the **same** frozen command:

```powershell
node --experimental-strip-types .\.faultline-witness\check-slug.mjs
# PASS after T1/T2; PREDICATE_FAIL after Turn 3
```

If the overlay file hash changed after Turn 3, **abort and restart** — that session is not publishable as “same frozen witness.”

### 7.2 Verify the ledger

```powershell
node $FaultLineCli codex sidecar status --repo .
```

Confirm baseline + turn-tree snapshots for ordinals 1–3. If missing → new session (don’t hand-edit a fake ledger).
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
  --image $Image `
  --minimize
```

`--minimize` (optional but preferred for the continuous product arc) synthesizes orphan commits from the PASS/FAIL turn trees and runs Git-path counterfactual minimization. Equivalent follow-up:

```powershell
node $FaultLineCli prove transition .faultline\turn-proof-bundles\<dir> `
  --repo $MumbcsRepo `
  --proposal $Proposal `
  --expect-digest $Digest `
  --image $Image `
  --transition 0
```

Expect:

- stable baseline + turn states  
- `PASS→FAIL` with `introduction.status = ATTRIBUTED`  
- `introduction.turnOrdinal = 3` (or your break turn)  
- package under `.faultline/turn-proof-bundles/…`  
- printed **root** digest  
- optional minimization result path + digest  

```powershell
node $FaultLineCli verify .faultline\turn-proof-bundles\<dir> `
  --expect-root sha256:<printed-root>
```

Turn packages remain `EXPERIMENTAL_TURN` even when minimization succeeds.

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
  --command "node --experimental-strip-types .faultline-witness/check-slug.mjs" `
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
      # optional demo invariant (once overlay exists):
      # - run: node --experimental-strip-types .faultline-witness/check-slug.mjs
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

### Product experience first (minimal intervention)

1. [ ] Start timer; point partner at FaultLine README only  
2. [ ] Record install / proof-ready / freeze→verify times, command count, confusion points  
3. [ ] Record one criticism (not only praise)  
4. [ ] Only assist after a documented blocker  

### Correctness capture

5. [ ] Consent / redaction agreed  
6. [ ] Overlay imports **real** production helper (no shadow logic)  
7. [ ] Witness **frozen before** Codex turns; overlay hash stable through Turn 3  
8. [ ] Codex Turn 3 edits production only (forbid list enforced)  
9. [ ] Sidecar ledger: baseline + turns 1–3  
10. [ ] `investigate turns` attributes later turn; optional `--minimize`  
11. [ ] Partner independently ran `fl verify --expect-root`  
12. [ ] Case study framed as **protocol validation** (not “caught a prod bug”)  
13. [ ] [external-case-study-template.md](../external-case-study-template.md) complete  
14. [ ] Handoff zip scrubbed  
15. [ ] Optional video screenshots  
16. [ ] Follow-up CI Phase 0 on MUMBCS  

---

## 13. Claim language

**Good:**

> An independent developer used FaultLine on the MUMBCS repository to record a real Codex session and independently verify a later-turn boundary. Under one human-frozen overlay witness importing production slug validation, the earliest recorded stable PASS→FAIL was Turn 3 (`EXPERIMENTAL_TURN`).

**Avoid:**

> FaultLine caught a real production bug / proved agent intent / unique root cause / this is COMMIT_PROOF / Prevention verified from self-asserted JSON / we mutated the witness on Turn 3.

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
A: Needs a running server (and often DB/content). Perfect for CI; awkward for the first no-network Docker proof. Use an immutable `.faultline-witness/` overlay that imports production; keep Playwright in GitHub Actions.

**Q: Did we catch a real production bug?**  
A: Not with this scripted protocol run. Publish it as interoperability / usability evidence unless a naturally occurring failure is used instead.

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
