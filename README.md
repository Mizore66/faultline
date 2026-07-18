# FaultLine

> **Idea claim:** a portable, offline-verifiable evidence package for one human-frozen predicate — another engineer can verify “where does *this* reviewed witness first go bad?” **without re-running repository code**, and without trusting model intent.

**Track fit:** Developer Tools (CI / DevOps / agentic debugging evidence). Complements Git bisect, CI logs, and repro cases — does not replace them.

## Start here (Idea first)

**Platforms:** Node.js 22+, pnpm 10, Windows / macOS / Linux.

| Priority | Goal | Command / path | What you get |
| --- | --- | --- | --- |
| 1 | Product Idea (`COMMIT_PROOF`) | `pnpm fl judge-proof` | Opens the **installed judge sample** (live-git root `sha256:f85c446d…`) |
| 1b | Zero-install Idea snapshot | [`docs/self-incident-proof-preview.html`](docs/self-incident-proof-preview.html) | Static HTML of that same sample (run `pnpm fl commit-proof-preview` to regenerate) |
| 2 | Fixture sandbox | `pnpm fl judge-demo` or [`docs/judge-preview.html`](docs/judge-preview.html) | Evidence-model UI only — **not** a real incident |
| 3 | Live Docker smoke | `pnpm fl demo live-git --export-only` | Fresh portable Git proof (daemon required) |

**COMMIT_PROOF sample** (see [`docs/samples/COMMIT_PROOF_SAMPLE.md`](docs/samples/COMMIT_PROOF_SAMPLE.md)):

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout main
pnpm install --frozen-lockfile
pnpm fl judge-proof
```

Default sample root is the live-git package `sha256:f85c446d…`. The historical self-incident root `sha256:f6a391b3…` is separate — see [docs/faultline-self-incident.md](docs/faultline-self-incident.md). Fixture fallback:

```powershell
pnpm fl judge-demo
```

**Windows (PowerShell):** if `pnpm` fails with ExecutionPolicy, use `pnpm.cmd`. Do not paste markdown backticks. Do not insert `--` between `fl` and the subcommand.

## What it proves / never claims

FaultLine is a **CLI evidence tool** for regressions after agent-assisted coding. A human freezes the predicate; FaultLine replays it and proves only what those executions support.

**Break → Find → Prove → Fix → Prevent**

| Proves | Never claims |
| --- | --- |
| Earliest stable PASS→FAIL under a frozen witness (Git range; turn trees experimental) | Model intent / “the agent meant to…” |
| Portable offline-verifiable proof packages (`COMMIT_PROOF`) | Unique semantic root cause |
| Bidirectional edit necessity/sufficiency when certified (Git path) | Private Codex interception or hidden model state |
| Structured `PREDICATE_*` outcomes (compile ≠ FAIL) | That one Docker image fits every lockfile era |

| Path | Grade | Role |
| --- | --- | --- |
| `fl investigate git` / self-incident / `demo live-git` | **`COMMIT_PROOF`** | Shippable product path — lead demos here |
| `fl investigate turns` | **`EXPERIMENTAL_TURN`** | Codex-native localization preview — not interchangeable with commit proof yet |
| `fl judge-demo` | Sample | Evidence-model UI for judges who cannot run Docker |

## Demo hierarchy

1. **Lead with the Idea claim** — portable offline-verifiable predicate proof.
2. **Show runnable proof:** `pnpm fl judge-proof` (installed sample root `sha256:f85c446d…` — see [docs/samples/COMMIT_PROOF_SAMPLE.md](docs/samples/COMMIT_PROOF_SAMPLE.md)).
3. **Cite historical dogfood separately:** provenance-workflow self-incident — [docs/faultline-self-incident.md](docs/faultline-self-incident.md). That package is **not** what `judge-proof` opens unless you replace the sample.
4. **Optional Codex beat (experimental):** later-turn First Bad Turn — baseline PASS → Turns 1–2 PASS → Turn 3 FAIL — [docs/later-turn-incident.md](docs/later-turn-incident.md) (`EXPERIMENTAL_TURN`; not interchangeable with `COMMIT_PROOF`).
5. **Sandbox UI last:** `pnpm fl judge-demo` or static preview — fixture only. Do not equate turn packages with commit proof.

## Real incident dogfood

FaultLine’s first completed proof is this repository’s provenance-workflow regression (`PASS→FAIL` at `97c3290`, recovery at `07ee7f1`). Full record: [docs/faultline-self-incident.md](docs/faultline-self-incident.md). Impact notes: [docs/impact-validation-self-incident.md](docs/impact-validation-self-incident.md). Guided path for your own incident: [docs/first-incident.md](docs/first-incident.md).

## Codex + GPT-5.6

Build Week must show **GPT-5.6** (`fl witness propose --live` and/or `fl repair brief --live`) and **Codex** (build acceleration + optional sidecar hooks) on camera — see [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md).

- **Codex (build):** implementation, adversarial tests, hardening.
- **Codex (runtime):** opt-in sidecar records public hooks + turn tree snapshots (including dirty Stops).
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only. Never assigns PASS/FAIL.

**Qualifying `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

## Important boundaries

- Deterministic demo ≠ a general arbitrary-code runner.
- **Git path** is the mature portable `COMMIT_PROOF` path; **turn path** is experimental and does not inspect private Codex reasoning.
- Sandbox plans are fail-closed; injected runners are never certified as Docker proof. A local Docker daemon is required for real proof evidence.
- Offline verify reconstructs recorded policy/data; it is **not** cryptographic host/Docker-enforcement attestation. Signed CI provenance binds bytes + GitHub Actions identity only.
- A proof is predicate-specific — not intent, semantic causality, or unique cause. Treat portable packages as sensitive incident material.

## Learn more

| Doc | Contents |
| --- | --- |
| [docs/concepts.md](docs/concepts.md) | Evidence vocabulary, grades, proves/never claims, impact/capability summary |
| [docs/security-model.md](docs/security-model.md) | Turn-tree storage, attest/provenance host limits, package sensitivity, signature caveats |
| [docs/witness-protocol.md](docs/witness-protocol.md) | Propose / review / approve / freeze / sign / verify + keyring |
| [docs/runtime-preparation.md](docs/runtime-preparation.md) | Doctor, runtime prepare / project plan / build / resolve |
| [docs/proof-bundles.md](docs/proof-bundles.md) | Investigate git, serve, verify, minimize, attest, provenance, prevention proof |
| [docs/codex-sidecar.md](docs/codex-sidecar.md) | Sidecar install/status, dirty checkpoint, ledger bind, record attach |
| [docs/first-incident.md](docs/first-incident.md) | Full first-incident narrative |
| [docs/github-action.md](docs/github-action.md) | Incident intake + proof replay Actions |
| [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md) | Submission kit / run of show |
| [docs/turn-snapshot-overhead.md](docs/turn-snapshot-overhead.md) | Turn-snapshot storage overhead measurements |
| [docs/later-turn-incident.md](docs/later-turn-incident.md) | Later-turn First Bad Turn fixture narrative (`EXPERIMENTAL_TURN`) |
| [docs/differentiation.md](docs/differentiation.md) | Scope comparison vs bisect / CI / repro / provenance |

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
pnpm test:package
```

`pnpm coverage-matrix` (alias `pnpm benchmark`) writes an adversarial coverage matrix to `benchmarks/REPORT.md`. It is **not** an E2E benchmark.

The suite includes canonical hashing, adversarial bundle tampering, witness-freeze integrity, authenticated reviewer approvals, lifecycle hash chains, real temporary-Git replay, Docker-plan safety, ledger binding, redaction behavior, integrity and signed-provenance receipts, and CLI workflows.
