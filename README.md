# FaultLine

> **First Bad Turn** evidence for agent-assisted regressions: freeze one reviewed predicate, replay historical states, prove only what executions support.

FaultLine executes historical states to prove where a frozen witness changed from pass to fail — without claiming agent intent or a unique semantic root cause.

**Break → Find → Prove → Fix → Prevent**

| Proves | Never claims |
| --- | --- |
| Earliest stable PASS→FAIL under a frozen witness | Model intent / “the agent meant to…” |
| Bidirectional edit necessity/sufficiency when certified | Unique semantic root cause |
| Turn tree snapshots from dirty Codex Stops (when sidecar installed) | Private Codex interception |
| Structured `PREDICATE_*` outcomes (compile ≠ FAIL) | That one Docker image fits every lockfile era |

**Hero demo for judges (real self-incident — prefer this in the video):**

1. Open [docs/faultline-self-incident.md](docs/faultline-self-incident.md)
2. Show historical CI failure → frozen witness → `PASS→FAIL` at `97c3290` → recorded root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`
3. Optional live re-verify path in that doc
4. Optional: `fl investigate turns` when a Codex sidecar ledger with dirty-turn snapshots is available

The deterministic `judge-demo` fixture is only the **protocol sample** (no Docker/API key). Do not center the submission video on its synthetic session metrics.

## Protocol sample (no Docker, no API key)

```powershell
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout main
pnpm install --frozen-lockfile
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
```

Zero-install visual: open [`docs/judge-preview.html`](docs/judge-preview.html).

With Docker (product path smoke): `pnpm fl -- demo live-git --export-only`

## Install

**Canonical path today:** clone `main` (MIT) and use `pnpm fl -- …` as above.

Public package name is `@mizore66/faultline` (the unscoped `faultline` name is taken). Registry install is supported **after** the first `pnpm publish`:

```powershell
npm install -g @mizore66/faultline
fl --version
fl doctor --repo .
fl doctor --proof-ready   # nonzero unless Docker proof-grade is READY
```

Until publish completes, do not treat `npm install -g` as the judge path.

## One guided command

```powershell
pnpm fl -- investigate --ci-log .\ci.log --repo . --command "pnpm test"
```

Creates a review-only draft, prints next steps (`witness review` → freeze → `incident continue`). Never auto-approves.

## One product path (expert)

1. `fl doctor --repo .` / `fl doctor --proof-ready`
2. `fl incident start` or `fl investigate --ci-log`
3. `fl witness review` → Approve → Freeze (retain digest)
4. Optional: `fl witness implement` (Codex drafts overlay; human still freezes)
5. `fl incident continue` / `fl investigate git`
6. `fl serve --bundle` / `fl verify --expect-root`
7. Optional: `fl repair --bundle … --with-codex` then re-verify three-state prevention

Full guide: [docs/first-incident.md](docs/first-incident.md).

## What this is validated on (impact)

Dogfood only: FaultLine’s first completed proof is **this repository’s provenance-workflow regression** (`PASS→FAIL` at `97c3290`, recovery at `07ee7f1`). No third-party adoption metrics.

## How Codex and GPT-5.6 are used

**Qualifying `/feedback`:** `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`

- **Codex (build):** implementation, adversarial tests, hardening.
- **Codex (runtime):** opt-in sidecar records public hooks + **turn tree snapshots even on dirty Stops**; clean checkpoints when possible. `fl witness implement` / `fl repair --with-codex` draft overlays/repairs for human review.
- **GPT-5.6:** blinded witness proposal + evidence-cited repair brief only. Never assigns PASS/FAIL.

## Benchmark matrix

`pnpm benchmark` writes `benchmarks/REPORT.md` (8 adversarial incident expectations, 0 unsupported exact-cause claims).

## What is implemented (advanced)

See prior surface: Docker-isolated Git replay, env-fingerprint refusal on lockfile skew, structured witness results, minimization, proof packages, attestations, GitHub Actions. Parallel multi-session *fleet blame* is **not** claimed — contribution attribution in the sample is fixture theater; production proves Git/turn-tree boundaries under one frozen witness.

## Fast judge check (no Docker or API key)

The fastest reproducible product check is the deterministic, export-only path:

```powershell
pnpm install --frozen-lockfile
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
```

This produces a managed judge bundle and verifies its stored evidence without executing repository code during verification. It lets a reviewer inspect the frozen witness, evidence vocabulary, stable-boundary rules, counterfactual result, and scope limits without Docker, a network call, or an API key. It is not a live customer incident and does not establish proof-grade Docker execution; use `fl demo live-git` below for that separate path.

For a zero-install visual walkthrough, download and open the committed [static judge preview](docs/judge-preview.html) in any browser. It is intentionally read-only and visibly labeled as a deterministic sample; regenerate the exact artifact with `pnpm fl -- judge-preview`.

`--replay` is available for the instant sample view, but it is visibly cached and cannot certify a stable boundary, an A-grade claim, prevention, or minimization.

## One-command live Git demo

On a machine with Docker, run the actual product path—not the deterministic sample—against a disposable good → bad → repaired Git repository:

```powershell
pnpm fl -- demo live-git --export-only
```

The command pulls `node:22-alpine` only to resolve a concrete immutable image digest; all witness executions then use that digest with `--pull=never`. It creates immutable witness-review records and disposable source material beneath `.faultline\live-git-demo\`, replays the frozen witness in native Docker, writes a verified Git proof package, and prints its root. Omit `--export-only` to open the read-only proof view. Pass `--image registry.example/name@sha256:<64-lowercase-hex>` to use an already-resolved image instead.

## First real incident (guided intake)

Before making a real claim, let FaultLine surface local prerequisites:

```powershell
pnpm fl -- doctor --repo .
```

If you do not know the Git bracket, ask FaultLine for **local-only suggestions** first. It may show a locally cached upstream merge-base and/or the immediate parent, but it never fetches, contacts a remote, parses CI, or chooses one for you:

```powershell
pnpm fl -- incident suggest --repo .
```

Then record a review-only draft from the command that is failing. It never runs the command, auto-selects a suggested or remote base, approves a witness, or freezes it. Pass a reviewed suggestion as `--from` / `--to`; with no explicit range, FaultLine uses only an unambiguous locally observed one-parent `HEAD~1 -> HEAD` bracket:

```powershell
pnpm fl -- incident start `
  --repo . `
  --command "pnpm test -- checkout"
```

For a common Node/Python/Go base image, use the guided setup command to review the exact Docker mutation first. Its first invocation does not touch Docker; only the explicit `--yes` invocation pulls the reviewed catalog tag, resolves the local immutable digest, and leaves a concrete next command. Add `--runtime node` (or `python` / `go`) to intake to bind that digest:

```powershell
pnpm fl -- runtime prepare node
# Review the displayed Docker pull effect, then explicitly confirm it:
pnpm fl -- runtime prepare node --yes
pnpm fl -- incident start --repo . --command "pnpm test -- checkout" --runtime node
```

For an ordinary project whose dependencies do not exist in a base image, prepare an explicit **setup-only** dependency image. FaultLine fingerprints the Dockerfile and every regular context file, previews the output tag/network policy/Docker mutation, and requires the printed `plan.review.planDigest` again at build time. The default build network is `none`, which disables Dockerfile `RUN` networking but does not certify daemon or base-image networking; choose `default` explicitly only when the reviewed Dockerfile needs networked `RUN` steps. The proof runner mounts Git source at `/workspace/src`, leaving an image-baked parent `/workspace/node_modules` available to common Node package resolution.

```powershell
pnpm fl -- runtime project plan `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default

# After reviewing the complete plan, copy its plan.review.planDigest:
pnpm fl -- runtime project build `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default `
  --expect-plan <plan-review-digest> `
  --yes
```

This build is never a proof and FaultLine never pushes credentials or images. If Docker reports only a local image ID, push and pull the reviewed tag through your own registry, then resolve its immutable digest without rebuilding. If you only need to validate the workflow locally after human witness freeze, `fl incident continue <id> --unsafe-local` is the explicitly non-proof route; it neither uses that local image ID nor exports a portable bundle.

```powershell
pnpm fl -- runtime project resolve --tag registry.example/acme/my-app:faultline-deps-20260717
pnpm fl -- incident start --repo . --command "pnpm test -- checkout" --image <resolved-image@sha256:...>
```

The draft and human-origin proposal are write-once local records. Run `pnpm fl -- witness review <id>` to review the exact command, overlay bytes, and policy in a local browser workbench; it requires separate human approval and freeze clicks. Retain the freeze digest outside the witness store, then use `pnpm fl -- incident status <id>` and `pnpm fl -- incident continue <id> --expect-digest <retained-frozen-digest>` to carry that same immutable incident into proof-grade replay without retyping its range or witness identifier. The full happy path, support boundary, CI handoff, and failure modes are in [docs/first-incident.md](docs/first-incident.md) and [docs/github-action.md](docs/github-action.md).

## Real Git investigation

The guided path is `fl incident start` → `fl witness review` → `fl incident continue`. The lower-level commands below remain available for automation or a pre-existing witness store. First create and freeze a reviewed witness. The proposal input is a blinded incident packet plus the exact overlay bytes to execute.

```powershell
pnpm fl -- witness propose --input .\proposal.json
pnpm fl -- witness review <proposal-id>
# Review the local page, then make separate Approve and Freeze clicks.
pnpm fl -- witness verify <proposal-id> --expect-digest <frozen-digest>
```

### Optional authenticated reviewer approval

`fl witness approve` records a reviewed approval but is intentionally not an identity assertion. When a reviewer needs to authenticate the approval, sign the already-frozen witness with an Ed25519 private key and verify it against a separately retained reviewer keyring:

```powershell
pnpm fl -- witness sign <proposal-id> `
  --private-key .\reviewer-ed25519.pem `
  --keyring .\reviewers.json
pnpm fl -- witness verify <proposal-id> `
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
pnpm fl -- investigate git `
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
pnpm fl -- serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root>
```

FaultLine verifies the complete package before opening this read-only page. It renders the same **BREAK -> FIND -> PROVE -> FIX -> PREVENT** incident structure as the demo from immutable Git range, frozen-witness digest, Docker evidence, stable transitions, lifecycle binding when present, and external-root status. The view has no rerun endpoint, script, or mutation control; it explicitly says when minimization/repair artifacts are not attached.

To counterfactually minimize the selected adjacent good/bad diff, use the same frozen witness and Docker policy:

```powershell
pnpm fl -- minimize git `
  --repo . `
  --before <last-good-commit> `
  --after <first-bad-commit> `
  --proposal <proposal-id> `
  --expect-digest <frozen-digest> `
  --image registry.example/faultline-node@sha256:<64-lowercase-hex>
```

The minimizer derives binary-safe Git patch units, keeps patch conflicts and execution failures `UNRESOLVED`, enforces an execution budget, and needs three distinct Docker executions in each counterfactual direction before it calls sufficiency and necessity certified. It writes every result under `.faultline/minimizations/`; an unsafe local run remains explicitly non-proof. The write result prints a canonical `resultDigest`; retain it outside the JSON, then verify the stored record without running Git, Docker, or repository code:

```powershell
pnpm fl -- minimize verify .faultline\minimizations\<result>.json `
  --expect-digest sha256:<recorded-result-digest>
```

Verification rechecks schema, nonce-bound run and execution IDs, attempt links, certificates, and the proof claim. It detects a rewritten record only when an externally retained digest is supplied; it is not a cryptographic signature, identity assertion, or host-attestation claim.

### Optional observed lifecycle ledger

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
pnpm fl -- record init --session <session-id> --repo . --transport SIDE_CAR --actor you@example.com
pnpm fl -- record attach `
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
pnpm fl -- ledger bind `
  --ledger .faultline\recordings\<session-id>.json `
  --investigation <proof-bundle>\investigation.json `
  --output .faultline\bindings\<investigation>.json
pnpm fl -- ledger verify .faultline\bindings\<investigation>.json
```

`record attach` is a convenience path for sidecar session attribution: it appends a started/completed turn pair and, when requested, a clean Git checkpoint for the completed turn. The attribution fields are reviewer-supplied context, not identity proof, private Codex interception, model intent, or turn-level blame. The ledger is observed evidence, not a claim that FaultLine reads private model reasoning.

## Verify and retain integrity evidence

Offline verification never executes repository code:

```powershell
pnpm fl -- verify <proof-bundle-directory> --expect-root sha256:<recorded-root>
```

Retain the root outside the package. FaultLine can write a separate, write-once receipt for that purpose:

```powershell
pnpm fl -- attest create `
  --bundle <proof-bundle-directory> `
  --receipt <receipt-id> `
  --subject "FaultLine incident" `
  --issuer "CI or release system"
pnpm fl -- attest verify <receipt-id> --expect-digest sha256:<recorded-receipt-digest>
```

`fl attest` is an **integrity-only** receipt. An external digest detects an editor who rewrites both local content and local checksums. It is not a cryptographic signature, an identity check, proof of authorship, or a provenance guarantee.

### Signed GitHub CI provenance

For a CI identity assertion, create a provenance subject from a fully verified Git proof bundle inside GitHub Actions:

```powershell
pnpm fl -- provenance create `
  --bundle <git-proof-bundle-directory> `
  --output .faultline\provenance\ci-receipt.json
```

This command refuses to run outside GitHub Actions. Its output is deliberately **unsigned**: it binds the verified proof root, source identity, resolved commit/tree range, frozen witness, and recorded execution policy facts, but it has no provenance value until the workflow passes those exact bytes to `actions/attest@v4` and retains the resulting Sigstore attestation bundle. Configure the action with the least privileges required by its current documentation (including its OIDC permission when required), and record the signer workflow, ref, event, and runner policy that the verifier should accept.

Verify the retained receipt, signature bundle, and proof package together without executing repository code:

```powershell
pnpm fl -- provenance verify `
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

## Package and clean-install use

FaultLine is licensed under the MIT License. It is not published to npm yet; the owner still controls the first registry release, so do not treat `npm install faultline` as a supported installation command.

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

## Reusable GitHub Action (proof replay)

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
- npm registry installation remains unsupported until the owner performs the first publish. No release or publish automation is enabled.

## GPT-5.6 boundaries

`fl witness propose --live` uses the Responses API with strict structured output after `OPENAI_API_KEY` is set. The model sees a blinded, redacted incident packet: candidate commits, turns, diffs, timeline data, and localization results are excluded by schema.

After a completed deterministic investigation, FaultLine also has a typed repair-brief boundary for GPT-5.6. It sends a privacy-minimized packet of verified facts and requires every inferred repair or prevention recommendation to cite a supplied evidence ID. It cannot output verdicts, a replacement witness, or a claim of model intent.

The CLI accepts only a fully verified portable Git proof package—not an arbitrary investigation JSON—and stores the result as a write-once `INFERRED` artifact:

```powershell
pnpm fl -- repair brief `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root> `
  --live
```

For a reviewed offline response, replace `--live` with `--input .\repair-brief.json`. FaultLine redacts retained text, validates every evidence citation, and writes only the minimized packet and inferred guidance under `.faultline\repair-briefs\`.

Verify an existing inferred repair artifact without re-running a model or any repository code:

```powershell
pnpm fl -- repair verify .faultline\repair-briefs\<repair-id>
```

`fl repair brief` prints an `artifactDigest`; retain it outside the directory and pass it to `fl repair verify --expect-digest ...` or `fl serve --expect-repair ...`.

After retaining the minimization result digest and generating a repair brief, bring those separately verified records into the **same read-only incident page**:

```powershell
pnpm fl -- serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root> `
  --minimization .faultline\minimizations\<result>.json `
  --expect-minimization sha256:<retained-minimization-digest> `
  --repair .faultline\repair-briefs\<repair-id> `
  --expect-repair sha256:<retained-repair-artifact-digest>
```

The page re-verifies each attachment against its retained digest. A minimization must match the proof's frozen witness and stable pass-to-fail transition; a repair artifact must match the exact investigation and frozen witness. The shareable page renders only certification status, counts, and evidence IDs—free-form repair text stays in the private repair artifact. Attachments remain outside the original Git proof root.

## Evidence vocabulary

| Label | Meaning |
| --- | --- |
| `EXECUTED` | A stored sandbox result for the frozen witness. |
| `DERIVED` | A deterministic statement reconstructed from stored facts. |
| `INFERRED` | A model or human interpretation that must cite evidence. |
| `UNKNOWN` | A material question the evidence does not answer. |

Verdicts are only `PASS`, `FAIL`, `UNSTABLE`, `ERROR`, and `INAPPLICABLE`. A `PASS -> FAIL` boundary becomes proof only when each side has three distinct, matching Docker-isolated executions.

## Judge path

1. Start with the [three-command judge check](#judges-three-commands-no-docker-no-api-key).
2. Inspect the reviewed frozen witness before the sample exposes a suspect state.
3. Watch the timeline keep non-monotonic history visible rather than assuming once-failing means always-failing.
4. Inspect the two-direction counterfactual and the explicit unresolved partial-patch result.
5. Verify the exported bundle with `pnpm fl -- verify .faultline/bundles/judge-demo` (or `fl verify` after a global install).
6. For a live, publishable incident, use the Git workflow above with a working Docker daemon and a digest-pinned image — or open the recorded [self-incident](docs/faultline-self-incident.md).

The deterministic path proves only its included sample workflow. A real incident claim needs a recorded live Git/Docker run, the exact retained proof root, and an accurate description of what the replay did and did not establish.

### Judge-facing factual evidence

| Claim or requirement | Factual evidence | Status |
| --- | --- | --- |
| Runnable product path | Judge commands above; MIT `LICENSE` on `main` | Ready on `main` |
| Live proof-grade investigation | Self-incident root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` | Recorded |
| Codex/GPT-5.6 use | Qualifying `/feedback` `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09` plus the bounded runtime boundaries above | Recorded session id |
| User or business impact | Self-incident dogfood only; no third-party adoption metrics | Scoped / honest |
| npm install | `@mizore66/faultline` public package name (publish when ready) | Package contract ready |
| Build Week video + Devpost fields | Narrated &lt;3 min YouTube URL and remaining form fields | Still human-owned |

Use the [Devpost description draft](docs/devpost-description-draft.md), [impact-validation template](docs/impact-validation-template.md), and [differentiation comparison](docs/differentiation.md) for the remaining human-reviewed copy.

## Important boundaries

- FaultLine's included demo is deterministic; it is not a claim of a general arbitrary-code runner.
- The live implementation is Git commit-range replay. A lifecycle ledger strengthens it only to the degree of its recorded checkpoints; no native Codex interception is implied.
- The sandbox plans are fail-closed. The CLI labels injected runners `INJECTED_RUNNER` and refuses to certify or publish them as Docker proof. The Ubuntu CI gate exercises the native Docker boundary; a local development environment still needs a Docker daemon to create real proof evidence.
- `NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. A signed GitHub CI receipt binds bytes and the configured GitHub Actions identity; it does not change this host/Docker-enforcement limitation.
- A proof is predicate-specific. It does not prove intent, semantic causality, or that one edit is the unique cause.
- Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.

## Build Week handoff

The [Build Week submission kit](docs/build-week-submission-kit.md) has the three-minute run of show and checklist.

- [x] Qualifying Codex `/feedback` session ID: `019f66bd-0ac1-78f3-8dc1-5968e4f2fa09`
- [x] MIT license on canonical `main`
- [x] Public npm package name `@mizore66/faultline` (run `pnpm publish` when releasing)
- [ ] Record a narrated under-three-minute demo (YouTube, public)
- [ ] Complete Devpost fields from [docs/devpost-description-draft.md](docs/devpost-description-draft.md)
- [ ] Set GitHub default branch to `main` if it is still `master`
- [ ] Recheck official rules and deadline on submission day

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
pnpm test:package
```

The suite includes canonical hashing, adversarial bundle tampering, witness-freeze integrity, authenticated reviewer approvals, lifecycle hash chains, real temporary-Git replay, Docker-plan safety, ledger binding, redaction behavior, integrity and signed-provenance receipts, and CLI workflows.
