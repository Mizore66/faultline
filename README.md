# FaultLine

> Freeze one reviewed executable question. Replay it over recorded Git or Codex turn states. Prove only what the executions support — without trusting model intent.

**Track:** Developer Tools (CI / DevOps / agentic debugging evidence).

**Break → Find → Prove → Fix → Prevent**

| Proves | Never claims |
| --- | --- |
| Earliest stable PASS→FAIL under a frozen witness | Model intent / unique semantic root cause |
| Portable offline-verifiable packages (`COMMIT_PROOF` on Git path) | Private Codex interception |
| Structured `PREDICATE_*` outcomes (compile ≠ FAIL) | That one Docker image fits every lockfile era |

| Path | Grade | Role |
| --- | --- | --- |
| `fl investigate git` / `judge-proof` | **`COMMIT_PROOF`** | Mature portable proof — lead demos here |
| `fl investigate turns` | **`EXPERIMENTAL_TURN`** | Codex-native First Bad Turn — remains experimental until the criteria below |
| `fl judge-demo` | Sample | Evidence-model UI only |

`EXPERIMENTAL_TURN` stays until promotion criteria are met (honest scoreboard in
[src/turn-proof-promotion.ts](src/turn-proof-promotion.ts) — multi-OS soak still
open, issue #152). Do not invent soak stats. Attestation / host limits:
[docs/security-model.md](docs/security-model.md).

## Judges: four commands

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.5-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

```bash
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.5-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

`pnpm install` runs `prepare` → `pnpm build`, so `pnpm fl` invokes the compiled `node dist/cli.js` entry (not per-invocation `tsx`). Use `pnpm fl:dev` only for local TypeScript iteration.  
`fl doctor --proof-ready` routes Docker-less machines deliberately: if it is not READY, stay on `judge-proof` / the static preview — do not chase `demo live-git`.  
`judge-proof` opens a **verified `COMMIT_PROOF` sample** package (disposable live-git sample root `sha256:f85c446d…`) — not the historical self-incident unless you open that package separately.  
Headless: `FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only` (clean exit; verified root printed last).  
Zero-install snapshot: [`docs/self-incident-proof-preview.html`](docs/self-incident-proof-preview.html).  
Fixture sandbox (not a real incident): `pnpm fl judge-demo`.  
GPT-5.6 shapes without a key: [`docs/samples/gpt-5.6/`](docs/samples/gpt-5.6/).

**Install policy:** source-only for this phase — clone + `pnpm fl` (above). Global `npm install -g @mizore66/faultline` is **not** supported yet.  
**Platforms:** Node.js 22+, pnpm 10, Windows / macOS / Linux. Docker required for live proof.  
**Windows:** use `pnpm.cmd` if ExecutionPolicy blocks `pnpm`. Do not insert `--` between `fl` and the subcommand.  
**CI:** green Verify run on the validated pre-tag commit [`457fabeb9d5ff2261cbbf1ced4673ff28c9074d0`](https://github.com/Mizore66/faultline/commit/457fabeb9d5ff2261cbbf1ced4673ff28c9074d0) ([run](https://github.com/Mizore66/faultline/actions/runs/29734618279)), including the **native Docker proof E2E gate** and its [hybrid Docker E2E coverage-matrix REPORT artifact](https://github.com/Mizore66/faultline/actions/runs/29734618279/artifacts/8457770804). Submission pin / Release: [`v0.1.5-buildweek`](https://github.com/Mizore66/faultline/releases/tag/v0.1.5-buildweek).

## Validation

- **Dogfood (published):** Git self-incident at `97c3290` — [docs/faultline-self-incident.md](docs/faultline-self-incident.md)
- **External protocol validation (permissioned, landed):** consented MUMBCS run (`monashblockchain/MUMBCS`) — offline-verified turn package root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`, Turn 3 `PASS_TO_FAIL` under `EXPERIMENTAL_TURN` — [docs/publishable-summary-mumbcs.md](docs/publishable-summary-mumbcs.md), [docs/external-case-study-mumbcs.md](docs/external-case-study-mumbcs.md), [docs/impact-validation-external-01.md](docs/impact-validation-external-01.md) (`Status: completed`), handoff mirror [docs/partners/mumbcs-later-turn-handoff/](docs/partners/mumbcs-later-turn-handoff/)

This is **protocol / interoperability validation** (freeze → replay → locate → verify) that localized a production-code regression under a frozen witness importing `src/lib/slugify.ts`. It is **not** a claim that FaultLine caught a naturally occurring production bug, agent intent, unique root cause, prevention, or deployed live-site impact. The recorded later-turn sample under `docs/samples/later-turn-ledger/` remains a CI fixture, not the MUMBCS case.

**Re-verification path:** FaultLine publishes digests + consent + redacted narrative (and the partner handoff mirror). Byte-level re-verify of the turn package requires MUMBCS collaborator access or a private redacted handoff (MUMBCS is private). No full proof-bundle fixture is shipped in this repo for that root.

## Codex + GPT-5.6

- **Codex (build):** implementation, adversarial tests, hardening. Qualifying `/feedback`: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`
- **Codex (runtime):** opt-in sidecar records public hooks + dirty turn-tree snapshots
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only — never PASS/FAIL. Inspect redacted samples in [`docs/samples/gpt-5.6/`](docs/samples/gpt-5.6/) without an API key; use `--live` on camera when a key is available.

Continuous product arc (turn path still experimental):  
`investigate turns` → `--minimize` / `fl prove transition` → repair → `fl prevention verify` (grounded path: Prevention verified; otherwise evidence summary)

Help is tiered: `fl` / `fl quickstart` for the short surface; `fl advanced` for the full command list.

## Learn more

| Doc | Contents |
| --- | --- |
| [docs/concepts.md](docs/concepts.md) | Grades, promotion criteria, claim boundaries |
| [docs/first-incident.md](docs/first-incident.md) | Guided first incident (`fl tutorial --yes`) |
| [docs/personas/judge.md](docs/personas/judge.md) | Judge / evaluator entry |
| [docs/personas/new-user.md](docs/personas/new-user.md) | New-user entry |
| [docs/personas/forensics.md](docs/personas/forensics.md) | Forensics entry |
| [docs/distribution.md](docs/distribution.md) | Pack / tarball / publish |
| [docs/security-chain.md](docs/security-chain.md) | Tag → CI → attestation chain |
| [docs/proof-bundles.md](docs/proof-bundles.md) | Verify / serve / minimize / prevention |
| [docs/codex-sidecar.md](docs/codex-sidecar.md) | Sidecar install / status |
| [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md) | Video run of show |
| [docs/publishable-summary-mumbcs.md](docs/publishable-summary-mumbcs.md) | Permissioned MUMBCS external protocol summary |
| [docs/external-case-study-mumbcs.md](docs/external-case-study-mumbcs.md) | Full MUMBCS structured case study |
| [docs/partners/mumbcs-faultline-guide.md](docs/partners/mumbcs-faultline-guide.md) | External partner runbook (MUMBCS) |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting |
| [docs/differentiation.md](docs/differentiation.md) | vs bisect / CI / repro |

Ops (runtime images, attestations, Actions, witness protocol): see `docs/` — not required to understand the product.

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
pnpm coverage-matrix
```

```bash
pnpm typecheck
pnpm test
pnpm build
pnpm coverage-matrix
```

`pnpm coverage-matrix` writes an **adversarial scenario coverage matrix** (spec/unit coverage — not an E2E benchmark).
