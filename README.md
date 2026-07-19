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

`EXPERIMENTAL_TURN` stays until: an external repository validates the full workflow, turn-boundary minimization is exercised end to end, prevention evidence is automatically bound from verified run artifacts, and the recorder completes platform soak testing. Details: [docs/concepts.md](docs/concepts.md#why-turn-stays-experimental_turn-not-missing-a-portable-package).

## Judges: four commands

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.0-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

`fl doctor --proof-ready` routes Docker-less machines deliberately: if it is not READY, stay on `judge-proof` / the static preview — do not chase `demo live-git`.  
`judge-proof` opens a **verified `COMMIT_PROOF` sample** package (disposable live-git sample root `sha256:f85c446d…`) — not the historical self-incident unless you open that package separately.  
Headless: `FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only` (clean exit; verified root printed last).  
Zero-install snapshot: [`docs/self-incident-proof-preview.html`](docs/self-incident-proof-preview.html).  
Fixture sandbox (not a real incident): `pnpm fl judge-demo`.  
GPT-5.6 shapes without a key: [`docs/samples/gpt-5.6/`](docs/samples/gpt-5.6/).

**Platforms:** Node.js 22+, pnpm 10, Windows / macOS / Linux. Docker required for live proof.  
**Windows:** use `pnpm.cmd` if ExecutionPolicy blocks `pnpm`. Do not insert `--` between `fl` and the subcommand.  
**CI:** green Verify run on the post-pin fix ([run](https://github.com/Mizore66/faultline/actions/runs/29673784462)), including the **[native Docker proof E2E gate](https://github.com/Mizore66/faultline/actions/runs/29673784462/job/88157296321)**. Submission pin / Release: [`v0.1.0-buildweek`](https://github.com/Mizore66/faultline/releases/tag/v0.1.0-buildweek).

## Validation

- **Dogfood (published):** Git self-incident at `97c3290` — [docs/faultline-self-incident.md](docs/faultline-self-incident.md)
- **External protocol test (underway):** independent developer + real Codex session + verified later-turn boundary — [docs/partners/mumbcs-faultline-guide.md](docs/partners/mumbcs-faultline-guide.md), [docs/external-case-study-template.md](docs/external-case-study-template.md)

The scripted partner run validates interoperability/usability. It is **not** automatically “FaultLine caught a real production bug.” Until the permissioned case study lands, public evidence remains dogfood + fixtures.

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
| [docs/first-incident.md](docs/first-incident.md) | Guided first incident |
| [docs/proof-bundles.md](docs/proof-bundles.md) | Verify / serve / minimize / prevention |
| [docs/codex-sidecar.md](docs/codex-sidecar.md) | Sidecar install / status |
| [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md) | Video run of show |
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

`pnpm coverage-matrix` writes an **adversarial scenario coverage matrix** (spec/unit coverage — not an E2E benchmark).
