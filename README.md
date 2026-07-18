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
| `fl investigate turns` | **`EXPERIMENTAL_TURN`** | Codex-native First Bad Turn — see [promotion criteria](docs/concepts.md#why-turn-stays-experimental_turn-not-missing-a-portable-package) |
| `fl judge-demo` | Sample | Evidence-model UI only |

## Judges: three commands

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout main
pnpm install --frozen-lockfile
pnpm fl judge-proof
```

Zero-install snapshot: [`docs/self-incident-proof-preview.html`](docs/self-incident-proof-preview.html).  
Fixture sandbox (not a real incident): `pnpm fl judge-demo`.

**Platforms:** Node.js 22+, pnpm 10, Windows / macOS / Linux. Docker required for live proof.  
**Windows:** use `pnpm.cmd` if ExecutionPolicy blocks `pnpm`. Do not insert `--` between `fl` and the subcommand.

## Validation

- **Dogfood (published):** Git self-incident at `97c3290` — [docs/faultline-self-incident.md](docs/faultline-self-incident.md)
- **External test (underway):** permissioned partner capture — [docs/external-case-study-template.md](docs/external-case-study-template.md), [docs/partners/mumbcs-faultline-guide.md](docs/partners/mumbcs-faultline-guide.md)

Until the external case study is published with retained digests and approved quote, public evidence remains dogfood + fixtures.

## Codex + GPT-5.6

- **Codex (build):** implementation, adversarial tests, hardening. Qualifying `/feedback`: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`
- **Codex (runtime):** opt-in sidecar records public hooks + dirty turn-tree snapshots
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only — never PASS/FAIL

Continuous product arc (turn path still experimental):  
`investigate turns` → optional `--minimize` / `fl prove transition` → repair → `fl prevention verify`

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
