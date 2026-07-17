# FaultLine

> The evidence layer for agent-assisted regressions: freeze the question, replay it over immutable source states, and prove only what the executions support.

FaultLine is a Developer Tool for the moment after agent-assisted work turns CI red. A human reviews and freezes one executable witness before localization. FaultLine runs that exact witness against recorded Git states, records stable `PASS -> FAIL` and `FAIL -> PASS` transitions, can minimize a failure-inducing diff, and writes a portable proof package another engineer can verify offline.

It deliberately does **not** claim model intent, a unique semantic root cause, or native Codex interception unless a recorded lifecycle ledger supplies the relevant observed facts. Models can propose a witness and an evidence-bounded repair brief; they never assign verdicts or manufacture proof.

## What is implemented

- `fl judge-demo` is a deterministic, runnable five-beat product demo: **BREAK -> FIND -> PROVE -> FIX -> PREVENT**.
- `fl serve --bundle <proof>` renders that same five-beat incident experience from a verified, real Git proof package. Optionally supplied minimization and repair records require separately retained digests, are independently re-verified, and appear in the same page only when their applicable evidence binding matches; they are never mislabeled as part of the original proof root.
- `fl doctor` makes Git, clean-worktree, Node, Docker CLI/daemon, and likely-runtime prerequisites explicit before a user starts an incident. `fl incident start` records a review-only command/range draft and human-origin witness proposal without executing, approving, or freezing it.
- `fl runtime prepare node|python|go --yes` performs one deliberately confirmed pull of a catalog-owned image, then resolves Docker's immutable `RepoDigest`; `fl runtime resolve` remains read-only. Guided incident intake records only that resolved digest, never a mutable tag.
- `fl witness review <id>` starts a local, token-protected human review workbench and prints its URL for the exact command, canonical overlay bytes, and policy. Approval and freeze are separate explicit actions; malformed or unblinded proposals are refused before a freeze record can be written.
- Witness proposals, human approval, and freeze records are immutable, content-addressed, and verified before an investigation can run.
- `fl codex sidecar install --repo <app> --cli <built-cli.js> --yes` creates one new, reviewable project hook document without overwriting an existing one. Its opt-in sidecar stores only allowlisted public lifecycle fields, a prompt digest, and clean Git checkpoints. `fl codex sidecar status` exposes health and exact ledger paths without exposing prompts, assistant text, or transcript paths.
- `fl record` remains available for a manually supplied hash-chained Codex-compatible lifecycle ledger. All lifecycle transports are labeled (`CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR`) rather than presented as private-Codex interception.
- `fl investigate git` materializes a real Git commit range into detached temporary worktrees and runs the frozen witness three times per state.
- Proof-grade runs require a digest-pinned Docker image with no network, read-only source/root, dropped capabilities, an unprivileged user, bounded resources, and a scrubbed environment. An explicit local escape hatch is always `INAPPLICABLE`, never proof.
- A Git proof package contains the frozen witness, raw run facts, stable transitions, a portable descendant Git bundle, a binary range patch, hashes, and an offline semantic verifier. It rejects rehashed contradictions rather than trusting a checksum alone.
- Optional lifecycle evidence is bound to matching Git checkpoints. The stricter ledger-binding artifact can require every replayed state to map to an ordered checkpoint.
- Incident packets and captured fields are redacted with explicit limited-coverage warnings. FaultLine does not claim perfect secret discovery.
- An external, write-once receipt can retain a proof root. That is integrity attestation, not a signature, identity, authorship, or provenance claim. Optional GitHub Artifact Attestations can instead sign a CI-created provenance subject, and optional reviewer signatures can bind a frozen witness to a trusted reviewer key.

## Quick start

Requirements: Node.js 22+ and pnpm 10.32.1 (pinned in `package.json`). If Corepack is available, run `corepack enable` once and it will select the pinned pnpm version. The CLI is tested on Windows, macOS, and Ubuntu; Docker is required only for a live proof-grade Git investigation. Native Docker replay is exercised on Ubuntu CI.

```powershell
pnpm install --frozen-lockfile
pnpm test
pnpm fl -- judge-demo --rerun-all
```

The demo writes a managed bundle beneath `.faultline/bundles/` and starts a local incident page. Use `Ctrl+C` to stop it.
When supplied, `--output` must also be a new or previously verified child directory under this managed root; FaultLine intentionally rejects arbitrary output paths.

For the actual user path—not the deterministic sample—start with [your first FaultLine incident](docs/first-incident.md). It documents the early `fl doctor` preflight, conservative human-reviewed intake, explicit runtime digest resolution, freeze, proof-grade replay, and verified incident page.

For the project’s own reproducible historical CI case, see the clearly scoped [FaultLine self-incident evidence and runbook](docs/faultline-self-incident.md). A human-reviewed Docker proof package is now recorded for its bounded workflow predicate, including the recorded root and measured transitions; the page also preserves the fresh-run workflow and its limits.

## CI and distribution boundary

Use the reusable [CI incident-intake action](docs/github-action.md) to preserve a failed command as a review-required FaultLine proposal without executing it. With a full Git checkout, it safely proposes the current GitHub event's `before -> after` or PR `base -> head` bracket from the locally supplied event payload; explicit reviewed inputs still win, and no network request or fetch is hidden in that convenience. The package has a checked `bin` entry (`fl --version`) and a lean `pnpm pack --dry-run` contract, but it remains deliberately `private` until the repository owner selects a license and an available public npm namespace. That owner decision is required before claiming an `npx` install path; the GitHub Action is the supported reusable entry point today.

## Fast judge check (no Docker or API key)

The fastest reproducible product check is the deterministic, export-only path:

```powershell
pnpm install --frozen-lockfile
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
```

This produces a managed judge bundle and verifies its stored evidence without executing repository code during verification. It lets a reviewer inspect the frozen witness, evidence vocabulary, stable-boundary rules, counterfactual result, and scope limits without Docker, a network call, or an API key. It is not a live customer incident and does not establish proof-grade Docker execution; use `fl demo live-git` below for that separate path.

For a zero-install visual walkthrough, download and open the committed [static judge preview](docs/judge-preview.html) in any browser. It is intentionally read-only and visibly labeled as a deterministic sample; regenerate the exact artifact with `pnpm fl -- judge-preview`.

For an export-only judge run:

```powershell
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/bundles/judge-demo
```

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
pnpm fl -- record init --session <session-id> --repo . --transport SIDE_CAR
Get-Content .\events.ndjson | pnpm fl -- record stdin --ledger .faultline\recordings\<session-id>.json
pnpm fl -- record checkpoint --ledger .faultline\recordings\<session-id>.json --repo . --after-turn 1
```

Pass the chosen `--ledger` path to `fl investigate git` or `fl incident continue` to embed and validate matching lifecycle checkpoints in the Git package. A rendered package labels its real coverage as `FULLY_BOUND`, `PARTIALLY_BOUND`, or conservative **LEGACY BOUND** for old packages; it never treats a descendant-only checkpoint as coverage of every replayed state. If every state should be bound to an ordered checkpoint, create a strict sidecar record:

```powershell
pnpm fl -- ledger bind `
  --ledger .faultline\recordings\<session-id>.json `
  --investigation <proof-bundle>\investigation.json `
  --output .faultline\bindings\<investigation>.json
pnpm fl -- ledger verify .faultline\bindings\<investigation>.json
```

The ledger is observed evidence, not a claim that FaultLine reads private model reasoning.

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

## How Codex and GPT-5.6 are used

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening. At runtime, the opt-in `fl codex sidecar` consumes documented public hook envelopes, hashes prompts in memory, and records clean Git checkpoints under local Git metadata; it does not claim access to private model state, reasoning, assistant text, or transcripts. `fl record` remains the manual route for a strictly ordered, hash-chained Codex-compatible NDJSON stream, labeled `CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR` rather than claimed as native interception. Start a manual recording with `pnpm fl -- codex record init --session <id> --repo . --transport CODEX_CLI`, pipe observed events through `fl codex record stdin`, and capture checkpoints with `fl codex record checkpoint`.

GPT-5.6 is used only through the Responses API for a blinded witness proposal or an evidence-cited repair brief. It never decides a pass/fail verdict, identifies a culprit, replaces the frozen witness, or creates proof evidence.

## Evidence vocabulary

| Label | Meaning |
| --- | --- |
| `EXECUTED` | A stored sandbox result for the frozen witness. |
| `DERIVED` | A deterministic statement reconstructed from stored facts. |
| `INFERRED` | A model or human interpretation that must cite evidence. |
| `UNKNOWN` | A material question the evidence does not answer. |

Verdicts are only `PASS`, `FAIL`, `UNSTABLE`, `ERROR`, and `INAPPLICABLE`. A `PASS -> FAIL` boundary becomes proof only when each side has three distinct, matching Docker-isolated executions.

## Judge path

1. Start with the [fast no-Docker check](#fast-judge-check-no-docker-or-api-key).
2. Inspect the reviewed frozen witness before the sample exposes a suspect state.
3. Watch the timeline keep non-monotonic history visible rather than assuming once-failing means always-failing.
4. Inspect the two-direction counterfactual and the explicit unresolved partial-patch result.
5. Verify the exported bundle with `pnpm fl -- verify .faultline/bundles/judge-demo`.
6. For a live, publishable incident, use the Git workflow above with a working Docker daemon and a digest-pinned image.

The deterministic path proves only its included sample workflow. A real incident claim needs a recorded live Git/Docker run, the exact retained proof root, and an accurate description of what the replay did and did not establish.

### Judge-facing factual evidence

The repository can supply runnable behavior and evidence boundaries; it cannot supply facts that have not been recorded. Before a submission or public claim, retain or fill in the following separately:

| Claim or requirement | Factual evidence to retain | Do not imply |
| --- | --- | --- |
| Runnable product path | Exact commit, platform/runtime, commands, and recorded output | That an unrecorded local run occurred |
| Live proof-grade investigation | Docker-backed bundle and proof root retained outside the bundle | That the deterministic judge sample is a live incident |
| Codex/GPT-5.6 use | The implemented bounded workflow plus an accurate human review of how Codex was used to build it | Native private-Codex interception, model verdicts, or model intent |
| User or business impact | A consented real-incident or interview record with method and limitations | Adoption, time savings, or customer outcomes that have not been measured |
| Signed CI provenance, if shown | The CI receipt, Sigstore bundle, trust file, and trusted root from the same run | Host, Docker-daemon, or general authorship attestation |
| Build Week submission | The qualifying feedback session ID, published narrated demo URL, repository/license, and completed submission fields | That these actions have already happened |

Use the [Devpost description draft](docs/devpost-description-draft.md), [impact-validation template](docs/impact-validation-template.md), and [differentiation comparison](docs/differentiation.md) to prepare those human-reviewed facts without inventing metrics or novelty claims.

## Important boundaries

- FaultLine's included demo is deterministic; it is not a claim of a general arbitrary-code runner.
- The live implementation is Git commit-range replay. A lifecycle ledger strengthens it only to the degree of its recorded checkpoints; no native Codex interception is implied.
- The sandbox plans are fail-closed. The CLI labels injected runners `INJECTED_RUNNER` and refuses to certify or publish them as Docker proof. The Ubuntu CI gate exercises the native Docker boundary; a local development environment still needs a Docker daemon to create real proof evidence.
- `NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. A signed GitHub CI receipt binds bytes and the configured GitHub Actions identity; it does not change this host/Docker-enforcement limitation.
- A proof is predicate-specific. It does not prove intent, semantic causality, or that one edit is the unique cause.
- Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.
- No software project can honestly guarantee a 100% probability of winning a judged competition.

## Build Week handoff

The [Build Week submission kit](docs/build-week-submission-kit.md) provides a three-minute narrated demo run of show, concrete judge commands, and a checklist aligned to the Developer Tools track. The remaining submission actions require a human account or recorded material and are intentionally not automated here:

- Capture the qualifying `/feedback` session ID.
- Record a narrated under-three-minute demo of the actual product path.
- Complete the human-reviewed [Devpost description draft](docs/devpost-description-draft.md), replacing each placeholder with factual evidence.
- Collect consented real-incident or interview evidence with the [impact-validation template](docs/impact-validation-template.md) before making impact claims.
- Use the [differentiation comparison](docs/differentiation.md) to keep positioning precise against Git bisect, CI artifacts, repro cases, and provenance tools.
- If showing CI provenance, retain the GitHub-signed attestation bundle and the exact trust configuration used to verify it.
- Publish a licensed repository and the demo video, then complete the Devpost submission.
- Recheck the official rules, deadline, and category requirements on submission day.

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
```

The suite includes canonical hashing, adversarial bundle tampering, witness-freeze integrity, authenticated reviewer approvals, lifecycle hash chains, real temporary-Git replay, Docker-plan safety, ledger binding, redaction behavior, integrity and signed-provenance receipts, and CLI workflows.
