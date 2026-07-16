# FaultLine

> The evidence layer for agent-assisted regressions: freeze the question, replay it over immutable source states, and prove only what the executions support.

FaultLine is a Developer Tool for the moment after agent-assisted work turns CI red. A human reviews and freezes one executable witness before localization. FaultLine runs that exact witness against recorded Git states, records stable `PASS -> FAIL` and `FAIL -> PASS` transitions, can minimize a failure-inducing diff, and writes a portable proof package another engineer can verify offline.

It deliberately does **not** claim model intent, a unique semantic root cause, or native Codex interception unless a recorded lifecycle ledger supplies the relevant observed facts. Models can propose a witness and an evidence-bounded repair brief; they never assign verdicts or manufacture proof.

## What is implemented

- `fl judge-demo` is a deterministic, runnable five-beat product demo: **BREAK -> FIND -> PROVE -> FIX -> PREVENT**.
- Witness proposals, human approval, and freeze records are immutable, content-addressed, and verified before an investigation can run.
- `fl record` stores a hash-chained, versioned Codex-compatible lifecycle ledger and captures real clean-Git checkpoints. It labels the transport (`CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR`) rather than pretending to intercept private Codex internals.
- `fl investigate git` materializes a real Git commit range into detached temporary worktrees and runs the frozen witness three times per state.
- Proof-grade runs require a digest-pinned Docker image with no network, read-only source/root, dropped capabilities, an unprivileged user, bounded resources, and a scrubbed environment. An explicit local escape hatch is always `INAPPLICABLE`, never proof.
- A Git proof package contains the frozen witness, raw run facts, stable transitions, a portable descendant Git bundle, a binary range patch, hashes, and an offline semantic verifier. It rejects rehashed contradictions rather than trusting a checksum alone.
- Optional lifecycle evidence is bound to matching Git checkpoints. The stricter ledger-binding artifact can require every replayed state to map to an ordered checkpoint.
- Incident packets and captured fields are redacted with explicit limited-coverage warnings. FaultLine does not claim perfect secret discovery.
- An external, write-once receipt can retain a proof root. That is integrity attestation, not a signature, identity, authorship, or provenance claim.

## Quick start

Requirements: Node.js 22+ and pnpm 10.32.1 (pinned in `package.json`). If Corepack is available, run `corepack enable` once and it will select the pinned pnpm version. The CLI is tested on Windows, macOS, and Ubuntu; Docker is required only for a live proof-grade Git investigation. Native Docker replay is exercised on Ubuntu CI.

```powershell
pnpm install --frozen-lockfile
pnpm test
pnpm fl -- judge-demo --rerun-all
```

The demo writes a managed bundle beneath `.faultline/bundles/` and starts a local incident page. Use `Ctrl+C` to stop it.
When supplied, `--output` must also be a new or previously verified child directory under this managed root; FaultLine intentionally rejects arbitrary output paths.

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

## Real Git investigation

First create and freeze a reviewed witness. The proposal input is a blinded incident packet plus the exact overlay bytes to execute.

```powershell
pnpm fl -- witness propose --input .\proposal.json
pnpm fl -- witness approve <proposal-id> --approved-by you@example.com
pnpm fl -- witness freeze <proposal-id>
pnpm fl -- witness verify <proposal-id> --expect-digest <frozen-digest>
```

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

FaultLine verifies the complete package before opening this read-only page. It shows the immutable Git range, frozen witness digest, Docker policy/evidence, stable transitions, lifecycle binding when present, and external-root verification status. The view has no rerun endpoint.

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

Record observed lifecycle events and clean checkpoints first:

```powershell
pnpm fl -- record init --session <session-id> --repo . --transport SIDE_CAR
Get-Content .\events.ndjson | pnpm fl -- record stdin --ledger .faultline\recordings\<session-id>.json
pnpm fl -- record checkpoint --ledger .faultline\recordings\<session-id>.json --repo . --after-turn 1
```

Pass `--ledger .faultline/recordings/<session-id>.json` to `fl investigate git` to embed and validate matching lifecycle checkpoints in the Git package. If every state should be bound to an ordered checkpoint, create a strict sidecar record:

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

An external digest detects an editor who rewrites both local content and local checksums. It is not a cryptographic signature, an identity check, proof of authorship, or a provenance guarantee.

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

## How Codex and GPT-5.6 are used

Codex accelerated FaultLine's implementation, adversarial testing, and product hardening. At runtime, `fl record` accepts a strictly ordered, hash-chained Codex-compatible NDJSON lifecycle stream and records clean Git checkpoints; it labels the supplied transport as `CODEX_CLI`, `CODEX_APP`, or `SIDE_CAR` rather than claiming private event interception. Start a recording with `pnpm fl -- codex record init --session <id> --repo . --transport CODEX_CLI`, pipe observed events through `fl codex record stdin`, and capture checkpoints with `fl codex record checkpoint`.

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

1. Run `pnpm fl -- judge-demo --rerun-all`.
2. Inspect the reviewed frozen witness before the sample exposes a suspect state.
3. Watch the timeline keep non-monotonic history visible rather than assuming once-failing means always-failing.
4. Inspect the two-direction counterfactual and the explicit unresolved partial-patch result.
5. Verify the exported bundle with `pnpm fl -- verify .faultline/bundles/judge-demo`.
6. For a live, publishable incident, use the Git workflow above with a working Docker daemon and a digest-pinned image.

## Important boundaries

- FaultLine's included demo is deterministic; it is not a claim of a general arbitrary-code runner.
- The live implementation is Git commit-range replay. A lifecycle ledger strengthens it only to the degree of its recorded checkpoints; no native Codex interception is implied.
- The sandbox plans are fail-closed. The CLI labels injected runners `INJECTED_RUNNER` and refuses to certify or publish them as Docker proof. The Ubuntu CI gate exercises the native Docker boundary; a local development environment still needs a Docker daemon to create real proof evidence.
- `NATIVE_DOCKER` means FaultLine's direct Docker runner on the host that produced the record. Offline verification reconstructs the recorded policy and data, but it is not cryptographic attestation that a host, Docker client, or daemon enforced that policy. Retain an external root or CI receipt when host provenance matters.
- A proof is predicate-specific. It does not prove intent, semantic causality, or that one edit is the unique cause.
- Portable Git packages deliberately retain the frozen witness, Git object references, and bounded evidence fields so another engineer can verify them. Treat a package as sensitive incident material before sharing it outside the authorized audience.
- No software project can honestly guarantee a 100% probability of winning a judged competition.

## Build Week handoff

The [Build Week submission kit](docs/build-week-submission-kit.md) provides a three-minute narrated demo run of show, concrete judge commands, and a checklist aligned to the Developer Tools track. The remaining submission actions require a human account or recorded material and are intentionally not automated here:

- Capture the qualifying `/feedback` session ID.
- Record a narrated under-three-minute demo of the actual product path.
- Publish a licensed repository and the demo video, then complete the Devpost submission.
- Recheck the official rules, deadline, and category requirements on submission day.

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
```

The suite includes canonical hashing, adversarial bundle tampering, witness-freeze integrity, lifecycle hash chains, real temporary-Git replay, Docker-plan safety, ledger binding, redaction behavior, external receipts, and CLI workflows.
