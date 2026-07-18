# Concepts

Evidence vocabulary, grades, claim boundaries, and capability scope. For judge install and the one-command path, start at [README Start here](../README.md#start-here-idea-first).

## Evidence vocabulary

| Label | Meaning |
| --- | --- |
| `EXECUTED` | A stored sandbox result for the frozen witness. |
| `DERIVED` | A deterministic statement reconstructed from stored facts. |
| `INFERRED` | A model or human interpretation that must cite evidence. |
| `UNKNOWN` | A material question the evidence does not answer. |

## Evidence grades (commit path vs turn path)

| Grade | Path | Meaning |
| --- | --- | --- |
| `COMMIT_PROOF` | Git commit-range investigation | Highest portable proof tier: Docker-isolated replay over immutable commits, write-once Git proof bundle, offline verifier. Shown on `fl investigate git` output and `fl serve --bundle`. |
| `EXPERIMENTAL_TURN` | Turn-tree localization | Explicitly lower tier. May record transitions and (library) packages, but is **not** interchangeable with commit-path portable proof until turn/Git parity lands. Label: “Turn localization — experimental evidence”. |
| `NONE` | Either path | No certified transitions / not proof-eligible. |

Verdicts are only `PASS`, `FAIL`, `UNSTABLE`, `ERROR`, and `INAPPLICABLE`. On the **commit path**, a `PASS -> FAIL` boundary becomes `COMMIT_PROOF` only when each side has three distinct, matching Docker-isolated executions. Turn-path transitions stay experimental even when three-run stability is observed.

| Path | Grade | Role |
| --- | --- | --- |
| `fl investigate git` / self-incident / `demo live-git` | **`COMMIT_PROOF`** | Shippable product path — lead demos here |
| `fl investigate turns` | **`EXPERIMENTAL_TURN`** | Codex-native localization preview — not interchangeable with commit proof yet |
| `fl judge-demo` | Sample | Evidence-model UI for judges who cannot run Docker |

## What it proves / never claims

| Proves | Never claims |
| --- | --- |
| Earliest stable PASS→FAIL under a frozen witness (Git range; turn trees experimental) | Model intent / “the agent meant to…” |
| Portable offline-verifiable proof packages (`COMMIT_PROOF`) | Unique semantic root cause |
| Bidirectional edit necessity/sufficiency when certified (Git path) | Private Codex interception or hidden model state |
| Structured `PREDICATE_*` outcomes (compile ≠ FAIL) | That one Docker image fits every lockfile era |

## Important boundaries

- FaultLine's included demo is deterministic; it is not a claim of a general arbitrary-code runner.
- **Turn path:** observes public Codex lifecycle hooks, captures immutable turn-boundary Git trees (including dirty Stops), and executes a frozen witness across those states. It does not inspect private Codex reasoning. Evidence grade is experimental until portable turn proof bundles match the Git path (#21).
- **Git path:** mature commit-range replay with portable, independently verifiable proof packages. Prefer this for publishable A-grade claims today.
- The sandbox plans are fail-closed. The CLI labels injected runners `INJECTED_RUNNER` and refuses to certify or publish them as Docker proof. The Ubuntu CI gate exercises the native Docker boundary; a local development environment still needs a Docker daemon to create real proof evidence.
- `NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. A signed GitHub CI receipt binds bytes and the configured GitHub Actions identity; it does not change this host/Docker-enforcement limitation.
- A proof is predicate-specific. It does not prove intent, semantic causality, or that one edit is the unique cause.
- Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.

Host/Docker attestation limits, signature caveats, turn-tree storage, and package sensitivity are expanded in [security-model.md](security-model.md).

## Impact, models, and capability summary

Dogfood only: FaultLine’s first completed proof is **this repository’s provenance-workflow regression** (`PASS→FAIL` at `97c3290`, recovery at `07ee7f1`). Record: [impact-validation-self-incident.md](impact-validation-self-incident.md). No third-party adoption metrics.

**Qualifying `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

- **Codex (build):** implementation, adversarial tests, hardening.
- **Codex (runtime):** opt-in sidecar records public hooks + turn tree snapshots (including dirty Stops).
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only. Never assigns PASS/FAIL.

`pnpm coverage-matrix` (alias `pnpm benchmark`) writes `benchmarks/REPORT.md` (8 adversarial incident expectations, 0 unsupported exact-cause claims). It is an adversarial coverage matrix, not an E2E benchmark.

Implemented today: Docker-isolated Git replay (mature proof packages), env-fingerprint refusal on lockfile skew, structured witness results, experimental turn-tree localization, minimization, attestations, GitHub Actions. Parallel multi-session *fleet blame* is **not** claimed.

### GPT-5.6 boundaries

`fl witness propose --live` uses the Responses API with strict structured output after `OPENAI_API_KEY` is set. The model sees a blinded, redacted incident packet: candidate commits, turns, diffs, timeline data, and localization results are excluded by schema.

After a completed deterministic investigation, FaultLine also has a typed repair-brief boundary for GPT-5.6. It sends a privacy-minimized packet of verified facts and requires every inferred repair or prevention recommendation to cite a supplied evidence ID. It cannot output verdicts, a replacement witness, or a claim of model intent.

GPT-5.6 is used only through the Responses API for a blinded witness proposal or an evidence-cited repair brief. It never decides a pass/fail verdict, identifies a culprit, replaces the frozen witness, or creates proof evidence.

Repair-brief CLI and shareable serve attachments: [proof-bundles.md](proof-bundles.md).

### How Codex is used at runtime

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening. At runtime, `fl record` accepts a strictly ordered, hash-chained Codex-compatible NDJSON lifecycle stream and records clean Git checkpoints; it labels the supplied transport as `CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR` rather than claiming private event interception. Start a recording with `pnpm fl codex record init --session <id> --repo . --transport CODEX_CLI`, pipe observed events through `fl codex record stdin`, and capture checkpoints with `fl codex record checkpoint`.

Sidecar install, dirty checkpoints, and ledger bind: [codex-sidecar.md](codex-sidecar.md).

## Primary investigation paths

**Guided intake from a CI log (preferred product path; does not auto-approve):**

```powershell
pnpm fl investigate --ci-log .\ci.log --repo . --command "pnpm test"
```

**Turn localization (experimental evidence grade):**

```powershell
pnpm fl investigate turns --repo . --ledger .faultline\ledgers\session.json --proposal <id> --expect-digest sha256:... --image <digest-pinned-image>
```

**Mature Git commit-range proof:**

```powershell
pnpm fl investigate git --repo . --from <good> --to <bad> --proposal <id> --expect-digest sha256:... --image <digest-pinned-image>
```

Full guided narrative: [first-incident.md](first-incident.md). Git packages: [proof-bundles.md](proof-bundles.md).

## Expert product path

1. `fl doctor --repo .` / `fl doctor --proof-ready`
2. Install Codex sidecar hooks; capture turn trees (including dirty Stops)
3. `fl incident start` or `fl investigate --ci-log`
4. `fl witness review` → Approve → Freeze (retain digest)
5. Optional: `fl witness implement` (Codex drafts overlay; human still freezes)
6. Prefer `fl investigate turns` when a turn ledger exists; otherwise `fl investigate git` / `fl incident continue`
7. `fl serve --bundle` / `fl verify --expect-root` (Git packages)
8. Optional: `fl repair --bundle …` prepares instructions (`REPAIR_INSTRUCTIONS_PREPARED`); `--with-codex` is opt-in and never auto-merges

## Judge path

1. Start with [Start here](../README.md#start-here-idea-first) (or the export-only judge check below).
2. Inspect the reviewed frozen witness before the sample exposes a suspect state.
3. Watch the timeline keep non-monotonic history visible rather than assuming once-failing means always-failing.
4. Inspect the two-direction counterfactual and the explicit unresolved partial-patch result.
5. Verify the exported bundle with `pnpm fl verify .faultline/bundles/judge-demo` (or `fl verify` after a global install).
6. For a live, publishable incident, use the Git workflow with a working Docker daemon and a digest-pinned image — or open the recorded [self-incident](faultline-self-incident.md).

The deterministic path proves only its included sample workflow. A real incident claim needs a recorded live Git/Docker run, the exact retained proof root, and an accurate description of what the replay did and did not establish.

### Export-only judge check

Write and verify the deterministic sample without opening a browser:

```powershell
pnpm fl judge-demo --rerun-all --export-only
pnpm fl verify .faultline/bundles/judge-demo
```

Regenerate the static preview with `pnpm fl judge-preview`. `--replay` is cached and cannot certify a stable boundary or A-grade claim.

### Judge-facing factual evidence

| Claim or requirement | Factual evidence | Status |
| --- | --- | --- |
| Runnable product path | Judge commands in README; MIT `LICENSE` on `main` | Ready on `main` |
| Live proof-grade investigation | Self-incident root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` | Recorded |
| Codex/GPT-5.6 use | Qualifying `/feedback` `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` plus the bounded runtime boundaries above | Recorded session id |
| User or business impact | Self-incident dogfood only; no third-party adoption metrics | Scoped / honest |
| npm install | Not published; clone + `pnpm fl` only | Source-only until first publish |
| Build Week video + Devpost fields | Narrated &lt;3 min YouTube URL and remaining form fields | Still human-owned |

Use the [Devpost description draft](devpost-description-draft.md), [impact-validation template](impact-validation-template.md), and [differentiation comparison](differentiation.md) for the remaining human-reviewed copy.
