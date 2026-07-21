# FaultLine

> Freeze one reviewed executable question. Replay it over recorded Git or Codex turn states. Prove only what the executions support — without trusting model intent.

**Track:** Developer Tools (CI / DevOps / agentic debugging evidence)

**Break → Find → Prove → Fix → Prevent**

| Proves | Never claims |
| --- | --- |
| Earliest stable PASS→FAIL under a frozen witness | Model intent / unique semantic root cause |
| Portable offline-verifiable packages (`COMMIT_PROOF` on Git path) | Private Codex interception |
| Structured `PREDICATE_*` outcomes (compile ≠ FAIL) | That one Docker image fits every lockfile era |

## Start here (about 2 minutes)

**Judging pin only:** `v0.1.10-buildweek`  
This is the only release intended for judging. Do not evaluate from `main`, npm, or older tags (`v0.1.9-buildweek` lacks EXT-01; `v0.1.4` is **RETRACTED**).

### Option A — Codespaces (fastest)

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/Mizore66/faultline?ref=v0.1.10-buildweek)

Then run:

```bash
pnpm fl judge-proof
```

### Option B — clone (six commands)

```bash
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.10-buildweek
corepack enable
pnpm install --frozen-lockfile
pnpm fl judge-proof
```

`pnpm install` builds the CLI (`prepare` → `pnpm build`). Use `pnpm fl:dev` only for local TypeScript iteration.

### What you should see

- A **verified `COMMIT_PROOF` sample** package (root `sha256:f85c446d…`)
- Not the historical self-incident unless you open that package separately
- No Docker and no API key required for this path

**Headless:** `FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only`  
**Static preview:** [`docs/self-incident-proof-preview.html`](docs/self-incident-proof-preview.html)  
**Offline verify the sample:**

```bash
pnpm fl verify docs/samples/self-incident-commit-proof --expect-root sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9
```

More install detail: [docs/demo-zero-install.md](docs/demo-zero-install.md) · persona guide: [docs/personas/judge.md](docs/personas/judge.md)

## Which path am I looking at?

| Command | Grade | Use it for |
| --- | --- | --- |
| `fl investigate git` / `judge-proof` | **`COMMIT_PROOF`** | Mature portable proof — lead here |
| `fl investigate turns` | **`EXPERIMENTAL_TURN`** | Codex-native First Bad Turn — experimental until promotion criteria |
| `fl judge-demo` | Sample UI | Evidence-model sandbox only — not a real incident |

`EXPERIMENTAL_TURN` stays experimental until the scoreboard in [`src/turn-proof-promotion.ts`](src/turn-proof-promotion.ts) is met (multi-OS soak still open, issue #152). Do not invent soak stats.

**Optional live Docker** (only if `pnpm fl doctor --proof-ready` says READY):

```bash
pnpm fl demo full
```

If doctor is not READY, stay on `judge-proof` / the static preview — do not chase live proof.

## Evidence in this repo

**Self-incident (published dogfood):** Git regression at `97c3290` — [docs/faultline-self-incident.md](docs/faultline-self-incident.md)

**External protocol validation (permissioned, completed):** consented MUMBCS run — Turn 3 earliest stable `PASS_TO_FAIL` under `EXPERIMENTAL_TURN`, package root `sha256:831885ed72814e3c2e68dd3366060f88ee94d1b0e533c4571246efd6957326b1`.

- Summary: [docs/publishable-summary-mumbcs.md](docs/publishable-summary-mumbcs.md)
- Case study: [docs/external-case-study-mumbcs.md](docs/external-case-study-mumbcs.md)

This is **protocol / interoperability validation**, not a claim that FaultLine caught a naturally occurring production bug, agent intent, unique root cause, or live-site impact. Byte-level re-verify of that turn package needs MUMBCS collaborator access (private repo); public readers use digests + consent + redacted narrative.

## Codex + GPT-5.6

| Role | What it does here |
| --- | --- |
| **Codex (build)** | Implementation, adversarial tests, hardening. Qualifying `/feedback`: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` |
| **Codex (runtime)** | Opt-in sidecar records public lifecycle hooks + dirty turn-tree snapshots |
| **GPT-5.6** | Blinded witness proposal + evidence-cited repair brief only — **never** PASS/FAIL |

GPT-5.6 shapes without a key: [`docs/samples/gpt-5.6/`](docs/samples/gpt-5.6/). On camera with a key: `fl witness propose --live`.

Help is tiered: `fl` / `fl quickstart` for the short surface; `fl advanced` for the full command list.

## Platform notes

- **Node.js 22+**, pnpm 10, Windows / macOS / Linux
- **Docker** required only for live proof (`demo full` / investigate), not for `judge-proof`
- **Windows:** use `pnpm.cmd` if ExecutionPolicy blocks `pnpm`. Do not insert `--` between `fl` and the subcommand
- **npm:** published package may lag the pin — do not treat npm as the EXT-01 judging artifact ([docs/continuous-release.md](docs/continuous-release.md))
- **CI evidence:** See Actions on [`v0.1.10-buildweek`](https://github.com/Mizore66/faultline/releases/tag/v0.1.10-buildweek) / commit `ea1e244…` (pin retargeted 2026-07-21 to current `main`, package `0.1.4`). Prefer linking exact successful required jobs rather than an aggregate badge alone.

## Docs by audience

| If you are… | Start with |
| --- | --- |
| A judge / evaluator | [docs/personas/judge.md](docs/personas/judge.md), [docs/concepts.md](docs/concepts.md) |
| A new user | [docs/personas/new-user.md](docs/personas/new-user.md), [docs/first-incident.md](docs/first-incident.md) |
| Doing forensics / proof work | [docs/personas/forensics.md](docs/personas/forensics.md), [docs/proof-bundles.md](docs/proof-bundles.md) |
| Checking security / chain | [SECURITY.md](SECURITY.md), [docs/security-chain.md](docs/security-chain.md), [docs/security-model.md](docs/security-model.md) |
| Comparing to bisect / CI | [docs/differentiation.md](docs/differentiation.md) |
| Recording / submitting | [docs/demo-partner-handoff.md](docs/demo-partner-handoff.md), [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md) |

Demo helpers: [zero-install](docs/demo-zero-install.md) · [rehearsal](docs/demo-rehearsal.md) · [Q&A](docs/demo-qa.md) · [fallback](docs/demo-fallback.md) · [sidecar](docs/codex-sidecar.md)

## Development

```bash
pnpm typecheck
pnpm test
pnpm build
pnpm coverage-matrix
```

On Windows, the same commands work via `pnpm.cmd` if ExecutionPolicy blocks `pnpm`.

`pnpm coverage-matrix` writes an adversarial scenario coverage matrix (spec/unit coverage — not an E2E benchmark).
