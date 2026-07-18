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

## What it is

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

## Demo hierarchy (one story)

1. **Lead with the Idea claim** — portable offline-verifiable predicate proof (not “better bisect,” not the fixture UI, not experimental turns).
2. **Show runnable proof:** `pnpm fl judge-proof` (installed sample root `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` — see [docs/samples/COMMIT_PROOF_SAMPLE.md](docs/samples/COMMIT_PROOF_SAMPLE.md)).
3. **Cite historical dogfood separately:** provenance-workflow self-incident `PASS→FAIL` at `97c3290`, root `sha256:f6a391b3…` — [docs/faultline-self-incident.md](docs/faultline-self-incident.md). That package is **not** what `judge-proof` opens unless you replace the sample.
4. **Sandbox UI last:** `pnpm fl judge-demo` or static preview — fixture only.
5. **Never headline:** `fl investigate turns` (`EXPERIMENTAL_TURN`).

Build Week must also show **GPT-5.6** (`fl witness propose --live` and/or `fl repair brief --live`) and **Codex** (build acceleration + optional sidecar hooks) on camera — see [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md).

## What to do next

1. **Real incident** — [docs/first-incident.md](docs/first-incident.md) (`fl doctor`, then `fl investigate --ci-log`).
2. **Docker proof smoke** — `pnpm fl demo live-git --export-only`.
3. **Submission finish** — [docs/submission-finish-runbook.md](docs/submission-finish-runbook.md) (video teleprompter, external N=1, Devpost paste). Full kit: [docs/build-week-submission-kit.md](docs/build-week-submission-kit.md).

Everything below is reference material: install boundaries, expert flows, vocabulary, and handoff checklists.

---

## Advanced reference

### Install and package boundary

**Supported install today:** clone `main` (MIT) and run via pnpm (see [Judge sandbox](#judge-sandbox-start-here)).

```powershell
pnpm fl doctor --repo .
pnpm fl doctor --proof-ready   # nonzero unless Docker proof-grade is READY
```

The public package name `@mizore66/faultline` is reserved for a future registry publish. **npm/global install is not supported yet** — there is no published release. Do not run `npm install -g @mizore66/faultline` or `npm install faultline` until an owner-published version exists. To exercise the packaged binary before publish, use `pnpm pack` and install the local tarball (see [Package and clean-install use](#package-and-clean-install-use)).

### Export-only judge check

Write and verify the deterministic sample without opening a browser:

```powershell
pnpm fl judge-demo --rerun-all --export-only
pnpm fl verify .faultline/bundles/judge-demo
```

Regenerate the static preview with `pnpm fl judge-preview`. `--replay` is cached and cannot certify a stable boundary or A-grade claim.

### Primary investigation paths

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

### Expert product path

1. `fl doctor --repo .` / `fl doctor --proof-ready`
2. Install Codex sidecar hooks; capture turn trees (including dirty Stops)
3. `fl incident start` or `fl investigate --ci-log`
4. `fl witness review` → Approve → Freeze (retain digest)
5. Optional: `fl witness implement` (Codex drafts overlay; human still freezes)
6. Prefer `fl investigate turns` when a turn ledger exists; otherwise `fl investigate git` / `fl incident continue`
7. `fl serve --bundle` / `fl verify --expect-root` (Git packages)
8. Optional: `fl repair --bundle …` prepares instructions (`REPAIR_INSTRUCTIONS_PREPARED`); `--with-codex` is opt-in and never auto-merges

### Impact, models, and capability summary

Dogfood only: FaultLine’s first completed proof is **this repository’s provenance-workflow regression** (`PASS→FAIL` at `97c3290`, recovery at `07ee7f1`). Record: [docs/impact-validation-self-incident.md](docs/impact-validation-self-incident.md). No third-party adoption metrics.

**Qualifying `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

- **Codex (build):** implementation, adversarial tests, hardening.
- **Codex (runtime):** opt-in sidecar records public hooks + turn tree snapshots (including dirty Stops).
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only. Never assigns PASS/FAIL.

`pnpm benchmark` writes `benchmarks/REPORT.md` (8 adversarial incident expectations, 0 unsupported exact-cause claims).

Implemented today: Docker-isolated Git replay (mature proof packages), env-fingerprint refusal on lockfile skew, structured witness results, experimental turn-tree localization, minimization, attestations, GitHub Actions. Parallel multi-session *fleet blame* is **not** claimed.

### One-command live Git demo

On a machine with Docker, run the actual product path—not the deterministic sample—against a disposable good → bad → repaired Git repository:

```powershell
pnpm fl demo live-git --export-only
```

The command pulls `node:22-alpine` only to resolve a concrete immutable image digest; all witness executions then use that digest with `--pull=never`. It creates immutable witness-review records and disposable source material beneath `.faultline\live-git-demo\`, replays the frozen witness in native Docker, writes a verified Git proof package, and prints its root. Omit `--export-only` to open the read-only proof view. Pass `--image registry.example/name@sha256:<64-lowercase-hex>` to use an already-resolved image instead.

### First real incident (guided intake)

Before making a real claim, let FaultLine surface local prerequisites:

```powershell
pnpm fl doctor --repo .
```

#### One-command guided path (`fl investigate --ci-log`)

For a CI log plus a failing command, use the resumable guided workflow. It creates the review-only draft, opens the local witness review workbench, pauses until you Approve and then Freeze (separate clicks), retains the freeze digest in-process, selects the runtime, localizes, and exports the proof package — without forcing you to retype subcommands:

```powershell
docker pull node:22-alpine
pnpm fl runtime resolve node
pnpm fl investigate --ci-log .\ci.log `
  --repo . `
  --command "pnpm test -- checkout" `
  --runtime node
```

If you cancel during review, resume the same durable incident:

```powershell
pnpm fl investigate --resume <incident-id> `
  --repo . `
  --expect-digest <retained-frozen-digest> `
  --runtime node
```

FaultLine never auto-approves or auto-freezes. Internal steps still reuse `incident` / `witness` / `investigate git` primitives; you see one coherent CLI sequence. Use `--unsafe-local` only for non-proof diagnosis when Docker is unavailable.

If you do not know the Git bracket, ask FaultLine for **local-only suggestions** first. It may show a locally cached upstream merge-base and/or the immediate parent, but it never fetches, contacts a remote, parses CI, or chooses one for you:

```powershell
pnpm fl incident suggest --repo .
```

Then record a review-only draft from the command that is failing. It never runs the command, auto-selects a suggested or remote base, approves a witness, or freezes it. Pass a reviewed suggestion as `--from` / `--to`; with no explicit range, FaultLine uses only an unambiguous locally observed one-parent `HEAD~1 -> HEAD` bracket:

```powershell
pnpm fl incident start `
  --repo . `
  --command "pnpm test -- checkout"
```

For a common Node/Python/Go base image, use the guided setup command to review the exact Docker mutation first. Its first invocation does not touch Docker; only the explicit `--yes` invocation pulls the reviewed catalog tag, resolves the local immutable digest, and leaves a concrete next command. Add `--runtime node` (or `python` / `go`) to intake to bind that digest:

```powershell
pnpm fl runtime prepare node
# Review the displayed Docker pull effect, then explicitly confirm it:
pnpm fl runtime prepare node --yes
pnpm fl incident start --repo . --command "pnpm test -- checkout" --runtime node
```

For an ordinary project whose dependencies do not exist in a base image, prepare an explicit **setup-only** dependency image. FaultLine fingerprints the Dockerfile and every regular context file, previews the output tag/network policy/Docker mutation, and requires the printed `plan.review.planDigest` again at build time. The default build network is `none`, which disables Dockerfile `RUN` networking but does not certify daemon or base-image networking; choose `default` explicitly only when the reviewed Dockerfile needs networked `RUN` steps. The proof runner mounts Git source at `/workspace/src`, leaving an image-baked parent `/workspace/node_modules` available to common Node package resolution.

```powershell
pnpm fl runtime project plan `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default

# After reviewing the complete plan, copy its plan.review.planDigest:
pnpm fl runtime project build `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default `
  --expect-plan <plan-review-digest> `
  --yes
```

This build is never a proof and FaultLine never pushes credentials or images. If Docker reports only a local image ID, push and pull the reviewed tag through your own registry, then resolve its immutable digest without rebuilding. If you only need to validate the workflow locally after human witness freeze, `fl incident continue <id> --unsafe-local` is the explicitly non-proof route; it neither uses that local image ID nor exports a portable bundle.

```powershell
pnpm fl runtime project resolve --tag registry.example/acme/my-app:faultline-deps-20260717
pnpm fl incident start --repo . --command "pnpm test -- checkout" --image <resolved-image@sha256:...>
```

The draft and human-origin proposal are write-once local records. Run `pnpm fl witness review <id>` to review the exact command, overlay bytes, and policy in a local browser workbench; it requires separate human approval and freeze clicks. Retain the freeze digest outside the witness store, then use `pnpm fl incident status <id>` and `pnpm fl incident continue <id> --expect-digest <retained-frozen-digest>` to carry that same immutable incident into proof-grade replay without retyping its range or witness identifier. The full happy path, support boundary, CI handoff, and failure modes are in [docs/first-incident.md](docs/first-incident.md) and [docs/github-action.md](docs/github-action.md).

### Real Git investigation

The preferred operator path is `fl investigate --ci-log` (or the modular `fl incident start` → `fl witness review` → `fl incident continue`). The lower-level commands below remain available for automation or a pre-existing witness store. First create and freeze a reviewed witness. The proposal input is a blinded incident packet plus the exact overlay bytes to execute.

```powershell
pnpm fl witness propose --input .\proposal.json
pnpm fl witness review <proposal-id>
# Review the local page, then make separate Approve and Freeze clicks.
pnpm fl witness verify <proposal-id> --expect-digest <frozen-digest>
```

#### Optional authenticated reviewer approval

`fl witness approve` records a reviewed approval but is intentionally not an identity assertion. When a reviewer needs to authenticate the approval, sign the already-frozen witness with an Ed25519 private key and verify it against a separately retained reviewer keyring:

```powershell
pnpm fl witness sign <proposal-id> `
  --private-key .\reviewer-ed25519.pem `
  --keyring .\reviewers.json
pnpm fl witness verify <proposal-id> `
  --expect-digest sha256:<frozen-digest> `
  --keyring .\reviewers.json `
  --require-signature
```

The keyring, not the approval record, decides which reviewer keys are trusted. It contains the reviewer's stable identity, the SHA-256 fingerprint of its SPKI public key, and that public key:

```json
{
  "schemaVersion": "faultline.reviewer-keyring.v1",
  "reviewers": [
    {
      "approvedBy": "reviewer@example.com",
      "keyId": "sha256:<SPKI-public-key-fingerprint>",
      "algorithm": "ED25519",
      "publicKeyPem": "-----BEGIN PUBLIC KEY-----\\n...\\n-----END PUBLIC KEY-----\\n"
    }
  ]
}
```

The signed record binds the proposal, ordinary approval, exact frozen-witness digest, and witness digest. A normal verification may accept a frozen witness without a signature for the local/offline MVP; `--require-signature` fails closed when the authenticated record is missing, untrusted, or altered. Keep private keys outside the repository, rotate keys by changing the retained keyring, and do not treat a displayed `approvedBy` string as authenticated unless this signature check passes.

Then replay it across a Git range in a digest-pinned Docker image:

```powershell
pnpm fl investigate git `
  --repo . `
  --from <known-good-commit> `
  --to <known-bad-commit> `
  --proposal <proposal-id> `
  --expect-digest <frozen-digest> `
  --image registry.example/faultline-node@sha256:<64-lowercase-hex>
```

On a completed proof-grade result, FaultLine creates a fresh write-once package under `.faultline/git-proof-bundles/` (or a child selected by `--output` under that managed root) and prints its root digest. If Docker is unavailable, a command times out, output exceeds the bound, or a result is unstable, FaultLine records an error/inconclusive result and does not publish a proof package.

Review a completed portable Git package without rerunning its witness or executing repository code:

```powershell
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root>
```

FaultLine verifies the complete package before opening this read-only page. It renders the same **BREAK -> FIND -> PROVE -> FIX -> PREVENT** incident structure as the demo from immutable Git range, frozen-witness digest, Docker evidence, stable transitions, lifecycle binding when present, and external-root status. The view has no rerun endpoint, script, or mutation control; it explicitly says when minimization/repair artifacts are not attached.

To counterfactually minimize the selected adjacent good/bad diff, use the same frozen witness and Docker policy:

```powershell
pnpm fl minimize git `
  --repo . `
  --before <last-good-commit> `
  --after <first-bad-commit> `
  --proposal <proposal-id> `
  --expect-digest <frozen-digest> `
  --image registry.example/faultline-node@sha256:<64-lowercase-hex>
```

The minimizer derives binary-safe Git patch units, keeps patch conflicts and execution failures `UNRESOLVED`, enforces an execution budget, and needs three distinct Docker executions in each counterfactual direction before it calls sufficiency and necessity certified. It writes every result under `.faultline/minimizations/`; an unsafe local run remains explicitly non-proof. The write result prints a canonical `resultDigest`; retain it outside the JSON, then verify the stored record without running Git, Docker, or repository code:

```powershell
pnpm fl minimize verify .faultline\minimizations\<result>.json `
  --expect-digest sha256:<recorded-result-digest>
```

Verification rechecks schema, nonce-bound run and execution IDs, attempt links, certificates, and the proof claim. It detects a rewritten record only when an externally retained digest is supplied; it is not a cryptographic signature, identity assertion, or host-attestation claim.

#### Optional observed lifecycle ledger

For an opt-in Codex App/CLI hook path, build FaultLine once and point the installer at that exact built CLI. It generates safe Unix and Windows command variants even when the path contains spaces, shows the complete root `hooks` document for review, and changes nothing until `--yes`. It only creates a new target project's `.codex/hooks.json`; it never overwrites or guesses how to merge an existing hook document.

```powershell
# Run from this FaultLine checkout.
pnpm build
$FaultLineCli = (Resolve-Path .\dist\cli.js).Path
$TargetRepo = (Resolve-Path C:\path\to\the\application).Path

# Preview the exact hook document and its one-file effect first.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli

# After reviewing that output, explicitly create $TargetRepo\.codex\hooks.json.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli --yes
```

Open or restart Codex in `$TargetRepo`, then use `/hooks` to inspect, trust, enable, or disable the FaultLine commands before they run. Project hook configuration is only appropriate for a project you trust. If `.codex/hooks.json` already exists, FaultLine refuses to replace it: emit the same root document with `node $FaultLineCli codex sidecar config --cli $FaultLineCli`, review it, and merge the `hooks` object yourself. Advanced installations may instead pass their own explicit `--command` and (when needed) `--command-windows` values to `sidecar config`.

The generated hooks observe `SessionStart`, `UserPromptSubmit`, and `Stop`; they never read or store `last_assistant_message`, a transcript path, or the raw prompt. Operational records live beneath the repository's local Git metadata directory rather than the worktree, so the recorder cannot hide arbitrary `.faultline` worktree files from a clean-checkpoint check.

After a session, inspect the recorder and copy its returned `ledgerPath` only when it applies to the incident's selected Git states:

```powershell
# Run the rest of the incident workflow from the application repository so
# its managed .faultline stores remain with the incident source.
Push-Location $TargetRepo
node $FaultLineCli codex sidecar status --repo .
node $FaultLineCli incident continue <id> `
  --expect-digest <retained-frozen-digest> `
  --ledger <ledgerPath-from-status>
```

The sidecar captures a checkpoint only when `Stop` sees a clean Git worktree. It reports a durable `CHECKPOINT_SKIPPED_DIRTY` result when agent edits remain uncommitted; it does not fake a clean turn or infer an ephemeral diff checkpoint. Commit/stash/model the state before a later observed checkpoint, or use the manual recording route below for another observed transport.

For manually supplied observed events, record lifecycle facts and clean checkpoints directly:

```powershell
pnpm fl record init --session <session-id> --repo . --transport SIDE_CAR --actor you@example.com
pnpm fl record attach `
  --ledger .faultline\recordings\<session-id>.json `
  --repo . `
  --turn turn-1 `
  --ordinal 1 `
  --prompt-digest sha256:<64-lowercase-hex> `
  --output-digest sha256:<64-lowercase-hex> `
  --contribution "short observed change label" `
  --checkpoint
```

Pass the chosen `--ledger` path to `fl investigate git` or `fl incident continue` to embed and validate matching lifecycle checkpoints in the Git package. A rendered package labels its real coverage as `FULLY_BOUND`, `PARTIALLY_BOUND`, or conservative **LEGACY BOUND** for old packages; it never treats a descendant-only checkpoint as coverage of every replayed state. If every state should be bound to an ordered checkpoint, create a strict sidecar record:

```powershell
pnpm fl ledger bind `
  --ledger .faultline\recordings\<session-id>.json `
  --investigation <proof-bundle>\investigation.json `
  --output .faultline\bindings\<investigation>.json
pnpm fl ledger verify .faultline\bindings\<investigation>.json
```

`record attach` is a convenience path for sidecar session attribution: it appends a started/completed turn pair and, when requested, a clean Git checkpoint for the completed turn. The attribution fields are reviewer-supplied context, not identity proof, private Codex interception, model intent, or turn-level blame. The ledger is observed evidence, not a claim that FaultLine reads private model reasoning.

### Verify and retain integrity evidence

Offline verification never executes repository code:

```powershell
pnpm fl verify <proof-bundle-directory> --expect-root sha256:<recorded-root>
```

Retain the root outside the package. FaultLine can write a separate, write-once receipt for that purpose:

```powershell
pnpm fl attest create `
  --bundle <proof-bundle-directory> `
  --receipt <receipt-id> `
  --subject "FaultLine incident" `
  --issuer "CI or release system"
pnpm fl attest verify <receipt-id> --expect-digest sha256:<recorded-receipt-digest>
```

`fl attest` is an **integrity-only** receipt. An external digest detects an editor who rewrites both local content and local checksums. It is not a cryptographic signature, an identity check, proof of authorship, or a provenance guarantee.

#### Signed GitHub CI provenance

For a CI identity assertion, create a provenance subject from a fully verified Git proof bundle inside GitHub Actions:

```powershell
pnpm fl provenance create `
  --bundle <git-proof-bundle-directory> `
  --output .faultline\provenance\ci-receipt.json
```

This command refuses to run outside GitHub Actions. Its output is deliberately **unsigned**: it binds the verified proof root, source identity, resolved commit/tree range, frozen witness, and recorded execution policy facts, but it has no provenance value until the workflow passes those exact bytes to `actions/attest@v4` and retains the resulting Sigstore attestation bundle. Configure the action with the least privileges required by its current documentation (including its OIDC permission when required), and record the signer workflow, ref, event, and runner policy that the verifier should accept.

Verify the retained receipt, signature bundle, and proof package together without executing repository code:

```powershell
pnpm fl provenance verify `
  --bundle <git-proof-bundle-directory> `
  --receipt .faultline\provenance\ci-receipt.json `
  --attestation-bundle .\sigstore-bundle.json `
  --trust .\faultline-attestation-trust.json
```

The verifier runs `gh attestation verify` against the supplied Sigstore material and then independently reconstructs the FaultLine binding from the Git proof bundle. Its trust file is an explicit allowlist, not metadata copied from the receipt:

Start from [`docs/faultline-github-attestation-trust.example.json`](docs/faultline-github-attestation-trust.example.json), then retain a reviewed copy alongside the downloaded root and artifact bundle.

```json
{
  "schemaVersion": "faultline.github-artifact-attestation-trust.v1",
  "repository": "owner/repository",
  "signerWorkflow": "owner/repository/.github/workflows/verify.yml",
  "sourceRef": "refs/heads/main",
  "eventName": "push",
  "denySelfHostedRunners": true,
  "trustedRootFile": ".\\trusted\\github-attestation-root.jsonl",
  "sourceDigest": "<optional-40-or-64-hex-source-commit>"
}
```

`sourceDigest` is optional; when set it must be the recorded 40- or 64-hex Git source commit. Retain the trust file and its referenced root file with the evidence, review every allowlisted value before using it, and rotate or replace them deliberately when CI policy changes. This signed provenance says that the configured GitHub Actions identity signed the receipt bytes. It does **not** cryptographically prove that a host, Docker client, or Docker daemon enforced FaultLine's recorded sandbox policy, nor does it expand the predicate-specific proof into a general build or authorship claim.

### Package and clean-install use

FaultLine is licensed under the MIT License. **It is not published to npm yet.** The owner still controls the first registry release. Do not treat `npm install -g @mizore66/faultline` or `npm install faultline` as supported installation commands.

A pinned source checkout can still produce and test the exact package that would be released:

```powershell
git checkout <release-tag-or-full-commit-sha>
pnpm install --frozen-lockfile
pnpm test:package
pnpm pack
npm install --global .\faultline-0.1.0.tgz
fl --version
```

`prepack` builds `dist/`, and the package allowlist contains `dist/`, `LICENSE`, `README.md`, the selected `docs/` guides, and npm's required package metadata. CI installs the tarball into an empty project and executes the installed `fl` binary before a release can be considered.

### Reusable GitHub Action (proof replay)

The root `action.yml` is the review-only [incident-intake action](docs/github-action.md). Proof replay lives at `actions/proof` so the two surfaces do not collide.

Commit a reviewed `faultline.frozen-witness.v1` record to the consumer repository, then pin the proof action to an owner-created release tag or, for the strongest immutability, a full commit SHA:

```yaml
name: FaultLine proof
on:
  pull_request:

permissions:
  contents: read

jobs:
  proof:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - id: faultline
        uses: Mizore66/faultline/actions/proof@<release-tag-or-full-commit-sha>
        with:
          witness: .github/faultline/witnesses/refund-regression.json
          base: ${{ github.event.pull_request.base.sha }}
          head: ${{ github.event.pull_request.head.sha }}
          runtime: ghcr.io/your-org/your-project-test@sha256:<64-lowercase-hex>
          proof-mode: required
      - name: Record externally retained root
        run: echo '${{ steps.faultline.outputs.root-digest }}' >> "$GITHUB_STEP_SUMMARY"
```

The proof action uploads `faultline-proof` and exposes `proof-package` and `root-digest` outputs. The runtime image must contain the consumer project's witness dependencies and must be digest-pinned. `proof-mode: required` fails closed unless Docker-isolated replay creates a verified package. `proof-mode: diagnostic` is an explicitly non-proof local replay; it uploads no package and leaves both outputs empty.

The manually runnable `Verify reusable Action` workflow creates a two-commit consumer repository, freezes a deterministic witness, resolves a Docker image digest, invokes this repository through `uses: ./actions/proof`, checks both outputs, verifies the package, and exercises artifact upload.

Support matrix:

- Packaged CLI: Node.js 22 and 24 on current GitHub-hosted Ubuntu, Windows, and macOS runners.
- Proof action: current GitHub-hosted Ubuntu runner with Docker and a Linux digest-pinned runtime image.
- Diagnostic action: current GitHub-hosted Ubuntu runner; it is not evidence suitable for publication.
- Package manager for source builds: pnpm 10. Installed consumers only need a supported Node.js runtime.
- GitHub.com Actions is supported. GitHub Enterprise Server is not currently claimed because `actions/upload-artifact@v4` availability differs by GHES version.
- npm registry installation remains **unsupported**. There are no GitHub Releases for a published CLI package yet. Source checkout + `pnpm fl` is the only judge install path.

### GPT-5.6 boundaries

`fl witness propose --live` uses the Responses API with strict structured output after `OPENAI_API_KEY` is set. The model sees a blinded, redacted incident packet: candidate commits, turns, diffs, timeline data, and localization results are excluded by schema.

After a completed deterministic investigation, FaultLine also has a typed repair-brief boundary for GPT-5.6. It sends a privacy-minimized packet of verified facts and requires every inferred repair or prevention recommendation to cite a supplied evidence ID. It cannot output verdicts, a replacement witness, or a claim of model intent.

The CLI accepts only a fully verified portable Git proof package—not an arbitrary investigation JSON—and stores the result as a write-once `INFERRED` artifact:

```powershell
pnpm fl repair brief `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root> `
  --live
```

For a reviewed offline response, replace `--live` with `--input .\repair-brief.json`. FaultLine redacts retained text, validates every evidence citation, and writes only the minimized packet and inferred guidance under `.faultline\repair-briefs\`.

Verify an existing inferred repair artifact without re-running a model or any repository code:

```powershell
pnpm fl repair verify .faultline\repair-briefs\<repair-id>
```

`fl repair brief` prints an `artifactDigest`; retain it outside the directory and pass it to `fl repair verify --expect-digest ...` or `fl serve --expect-repair ...`.

After retaining the minimization result digest and generating a repair brief, bring those separately verified records into the **same read-only incident page**:

```powershell
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root> `
  --minimization .faultline\minimizations\<result>.json `
  --expect-minimization sha256:<retained-minimization-digest> `
  --repair .faultline\repair-briefs\<repair-id> `
  --expect-repair sha256:<retained-repair-artifact-digest>
```

The page re-verifies each attachment against its retained digest. A minimization must match the proof's frozen witness and stable pass-to-fail transition; a repair artifact must match the exact investigation and frozen witness. The shareable page renders only certification status, counts, and evidence IDs—free-form repair text stays in the private repair artifact. Attachments remain outside the original Git proof root.

### How Codex and GPT-5.6 are used

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening. At runtime, `fl record` accepts a strictly ordered, hash-chained Codex-compatible NDJSON lifecycle stream and records clean Git checkpoints; it labels the supplied transport as `CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR` rather than claiming private event interception. Start a recording with `pnpm fl codex record init --session <id> --repo . --transport CODEX_CLI`, pipe observed events through `fl codex record stdin`, and capture checkpoints with `fl codex record checkpoint`.

#### Turn-tree snapshot storage warning

Turn-tree capture supports experimental turn localization (`EXPERIMENTAL_TURN`). It is not commit-path portable proof (`COMMIT_PROOF`).

Codex sidecar SessionStart/Stop turn-tree snapshots use a **temporary Git index**, but they are **not storage-neutral**: staging still writes blob objects into this repository's object database. Eligible **untracked** files may be included unless you opt out.

Mitigations:

- Add a repository-root `.faultlineignore` (gitignore syntax) for project-specific exclusions.
- Built-in defaults already skip common build/cache trees (`dist/`, `build/`, `.next/`, `coverage/`, …), dependency dirs, and secret-shaped paths.
- Hard caps reject oversized snapshots (per-file, total bytes, and file count) before any blob write.
- Secret scanning (regex + entropy) rejects high-confidence credential material before acceptance.
- Set `FAULTLINE_TURN_SNAPSHOT_TRACKED_ONLY=1` to stage tracked files only.

The sidecar prints this warning when it primes the SessionStart baseline snapshot.

GPT-5.6 is used only through the Responses API for a blinded witness proposal or an evidence-cited repair brief. It never decides a pass/fail verdict, identifies a culprit, replaces the frozen witness, or creates proof evidence.

### Evidence vocabulary

| Label | Meaning |
| --- | --- |
| `EXECUTED` | A stored sandbox result for the frozen witness. |
| `DERIVED` | A deterministic statement reconstructed from stored facts. |
| `INFERRED` | A model or human interpretation that must cite evidence. |
| `UNKNOWN` | A material question the evidence does not answer. |

#### Evidence grades (commit path vs turn path)

| Grade | Path | Meaning |
| --- | --- | --- |
| `COMMIT_PROOF` | Git commit-range investigation | Highest portable proof tier: Docker-isolated replay over immutable commits, write-once Git proof bundle, offline verifier. Shown on `fl investigate git` output and `fl serve --bundle`. |
| `EXPERIMENTAL_TURN` | Turn-tree localization | Explicitly lower tier. May record transitions and (library) packages, but is **not** interchangeable with commit-path portable proof until turn/Git parity lands. Label: “Turn localization — experimental evidence”. |
| `NONE` | Either path | No certified transitions / not proof-eligible. |

Verdicts are only `PASS`, `FAIL`, `UNSTABLE`, `ERROR`, and `INAPPLICABLE`. On the **commit path**, a `PASS -> FAIL` boundary becomes `COMMIT_PROOF` only when each side has three distinct, matching Docker-isolated executions. Turn-path transitions stay experimental even when three-run stability is observed.

### Judge path

1. Start with the [Judge sandbox](#judge-sandbox-start-here) (or the [export-only judge check](#export-only-judge-check)).
2. Inspect the reviewed frozen witness before the sample exposes a suspect state.
3. Watch the timeline keep non-monotonic history visible rather than assuming once-failing means always-failing.
4. Inspect the two-direction counterfactual and the explicit unresolved partial-patch result.
5. Verify the exported bundle with `pnpm fl verify .faultline/bundles/judge-demo` (or `fl verify` after a global install).
6. For a live, publishable incident, use the Git workflow above with a working Docker daemon and a digest-pinned image — or open the recorded [self-incident](docs/faultline-self-incident.md).

The deterministic path proves only its included sample workflow. A real incident claim needs a recorded live Git/Docker run, the exact retained proof root, and an accurate description of what the replay did and did not establish.

#### Judge-facing factual evidence

| Claim or requirement | Factual evidence | Status |
| --- | --- | --- |
| Runnable product path | Judge commands above; MIT `LICENSE` on `main` | Ready on `main` |
| Live proof-grade investigation | Self-incident root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` | Recorded |
| Codex/GPT-5.6 use | Qualifying `/feedback` `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` plus the bounded runtime boundaries above | Recorded session id |
| User or business impact | Self-incident dogfood only; no third-party adoption metrics | Scoped / honest |
| npm install | Not published; clone + `pnpm fl` only | Source-only until first publish |
| Build Week video + Devpost fields | Narrated &lt;3 min YouTube URL and remaining form fields | Still human-owned |

Use the [Devpost description draft](docs/devpost-description-draft.md), [impact-validation template](docs/impact-validation-template.md), and [differentiation comparison](docs/differentiation.md) for the remaining human-reviewed copy.

## Important boundaries

- FaultLine's included demo is deterministic; it is not a claim of a general arbitrary-code runner.
- **Turn path:** observes public Codex lifecycle hooks, captures immutable turn-boundary Git trees (including dirty Stops), and executes a frozen witness across those states. It does not inspect private Codex reasoning. Evidence grade is experimental until portable turn proof bundles match the Git path (#21).
- **Git path:** mature commit-range replay with portable, independently verifiable proof packages. Prefer this for publishable A-grade claims today.
- The sandbox plans are fail-closed. The CLI labels injected runners `INJECTED_RUNNER` and refuses to certify or publish them as Docker proof. The Ubuntu CI gate exercises the native Docker boundary; a local development environment still needs a Docker daemon to create real proof evidence.
- `NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. A signed GitHub CI receipt binds bytes and the configured GitHub Actions identity; it does not change this host/Docker-enforcement limitation.
- A proof is predicate-specific. It does not prove intent, semantic causality, or that one edit is the unique cause.
- Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.

## Build Week handoff

The [Build Week submission kit](docs/build-week-submission-kit.md) has the three-minute run of show and checklist.

- [x] Qualifying Codex `/feedback` session ID: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`
- [x] MIT license on canonical `main` (GitHub default branch: `main`)
- [x] Active Sol audit backlog: issues [#18–#32](https://github.com/Mizore66/faultline/issues/32) (historical #1–#16 closed)
- [ ] Publish `@mizore66/faultline` only when intentionally releasing (not required for judging today)
- [ ] Record a narrated under-three-minute demo (YouTube, public)
- [ ] Complete Devpost fields from [docs/devpost-description-draft.md](docs/devpost-description-draft.md)
- [ ] Recheck official rules and deadline on submission day

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
pnpm test:package
```

The suite includes canonical hashing, adversarial bundle tampering, witness-freeze integrity, authenticated reviewer approvals, lifecycle hash chains, real temporary-Git replay, Docker-plan safety, ledger binding, redaction behavior, integrity and signed-provenance receipts, and CLI workflows.
