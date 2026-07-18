# Proof bundles

Git investigation packages, offline verify, minimization, integrity/provenance receipts, prevention proof, and repair-brief serve attachments. Witness freeze first: [witness-protocol.md](witness-protocol.md). Runtime images: [runtime-preparation.md](runtime-preparation.md).

## One-command live Git demo

On a machine with Docker, run the actual product path—not the deterministic sample—against a disposable good → bad → repaired Git repository:

```powershell
pnpm fl demo live-git --export-only
```

The command pulls `node:22-alpine` only to resolve a concrete immutable image digest; all witness executions then use that digest with `--pull=never`. It creates immutable witness-review records and disposable source material beneath `.faultline\live-git-demo\`, replays the frozen witness in native Docker, writes a verified Git proof package, and prints its root. Omit `--export-only` to open the read-only proof view. Pass `--image registry.example/name@sha256:<64-lowercase-hex>` to use an already-resolved image instead.

## Investigate git

The preferred operator path is `fl investigate --ci-log` (or the modular `fl incident start` → `fl witness review` → `fl incident continue`). The lower-level commands below remain available for automation or a pre-existing witness store.

Then replay a frozen witness across a Git range in a digest-pinned Docker image:

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

Pass a chosen `--ledger` path to `fl investigate git` or `fl incident continue` to embed and validate matching lifecycle checkpoints in the Git package. Lifecycle recording: [codex-sidecar.md](codex-sidecar.md).

## Serve (read-only proof page)

Review a completed portable Git package without rerunning its witness or executing repository code:

```powershell
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root>
```

FaultLine verifies the complete package before opening this read-only page. It renders the same **BREAK -> FIND -> PROVE -> FIX -> PREVENT** incident structure as the demo from immutable Git range, frozen-witness digest, Docker evidence, stable transitions, lifecycle binding when present, and external-root status. The view has no rerun endpoint, script, or mutation control; it explicitly says when minimization/repair artifacts are not attached.

A rendered package labels its real coverage as `FULLY_BOUND`, `PARTIALLY_BOUND`, or conservative **LEGACY BOUND** for old packages; it never treats a descendant-only checkpoint as coverage of every replayed state.

### Repair brief and attachment serve flags

The CLI accepts only a fully verified portable Git proof package—not an arbitrary investigation JSON—and stores the repair brief as a write-once `INFERRED` artifact:

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

Optional: `fl repair --bundle …` prepares instructions (`REPAIR_INSTRUCTIONS_PREPARED`); `--with-codex` is opt-in and never auto-merges.

## Turn boundary → counterfactual (experimental chain)

After a verified turn package, chain the selected PASS→FAIL trees into Git-path minimization without manually copying digests:

```powershell
pnpm fl investigate turns ... --minimize --transition 0
# or later:
pnpm fl prove transition .faultline\turn-proof-bundles\<dir> `
  --repo . --proposal <id> --expect-digest sha256:… --image <digest-pinned> --transition 0
```

This creates synthetic orphan commits from turn `treeDigest`s so existing `minimizeGitDiff` can run. It does **not** promote `EXPERIMENTAL_TURN` to `TURN_PROOF` / `COMMIT_PROOF`.

## Prevention proof

Recovery evidence and a stable boundary do not establish prevention. FaultLine packages prevention as `faultline.prevention-proof.v1` with two classifications:

- **`PREVENTION_VERIFIED`** — written via `fl prevention write --from-bundle` (or automatic export after a verified Codex repair) binding last-good/first-bad run IDs from the Git proof bundle plus three repaired-state NATIVE_DOCKER run bindings and the repair patch digest. CLI/UI may say **Prevention verified** only for this classification.
- **`PREVENTION_EVIDENCE_SUMMARY`** — caller-supplied fields via `fl prevention write --input` without bundle grounding. Do not overclaim from repair instructions alone.

```powershell
pnpm fl prevention write --input .\prevention-input.json
pnpm fl prevention verify .faultline\prevention-proofs\<directory> --expect-root sha256:<recorded-root>
```

`fl verify` on a directory whose manifest is `faultline.prevention-proof.v1` prints `Prevention verified` or `Prevention evidence summary` according to the package classification.

Attach a verified prevention package to the read-only Git proof page (both flags required):

```powershell
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root sha256:<recorded-root> `
  --prevention .faultline\prevention-proofs\<directory> `
  --expect-prevention sha256:<retained-prevention-root>
```

## Verify

Offline verification never executes repository code:

```powershell
pnpm fl verify <proof-bundle-directory> --expect-root sha256:<recorded-root>
```

Retain the root outside the package.

## Minimize

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

## Verify and retain integrity evidence

FaultLine can write a separate, write-once receipt:

```powershell
pnpm fl attest create `
  --bundle <proof-bundle-directory> `
  --receipt <receipt-id> `
  --subject "FaultLine incident" `
  --issuer "CI or release system"
pnpm fl attest verify <receipt-id> --expect-digest sha256:<recorded-receipt-digest>
```

`fl attest` is an **integrity-only** receipt. Host-attestation limits: [security-model.md](security-model.md#integrity-receipts-fl-attest--not-host-attestation).

### Signed GitHub CI provenance

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

Start from [`faultline-github-attestation-trust.example.json`](faultline-github-attestation-trust.example.json), then retain a reviewed copy alongside the downloaded root and artifact bundle.

Example trust shape:

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

`sourceDigest` is optional; when set it must be the recorded 40- or 64-hex Git source commit. Retain the trust file and its referenced root file with the evidence, review every allowlisted value before using it, and rotate or replace them deliberately when CI policy changes.

Signed provenance host/Docker limits: [security-model.md](security-model.md#signed-github-ci-provenance--host--docker-limits).

## Package and clean-install use

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

## Reusable GitHub Action (proof replay)

The root `action.yml` is the review-only [incident-intake action](github-action.md). Proof replay lives at `actions/proof` so the two surfaces do not collide. Commit a reviewed `faultline.frozen-witness.v1` record, pin the proof action to a release tag or full commit SHA, and use a digest-pinned runtime image. Support matrix and YAML example: [github-action.md](github-action.md).
