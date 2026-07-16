# Your first FaultLine incident

This is the shortest honest path from a failing command to a shareable proof package. It is designed for a developer who has a Git worktree, a command that should pass, and Docker available for the final proof step.

FaultLine has two deliberately different states:

| State | What it means | Can it create a proof package? |
| --- | --- | --- |
| Review-only intake | A command and Git bracket are recorded for a human to inspect. Nothing has run, been approved, or been frozen. | No |
| Proof-grade replay | The exact frozen witness ran in isolated Docker against immutable Git states. | Only if the recorded executions establish the required stable transition. |

## Before you start: build FaultLine once and target the application repository

FaultLine is currently source-distributed. Run this once from a FaultLine checkout, then keep using the resolved CLI path while your current directory is the application repository. This keeps that application's `.faultline` records beside the source under investigation rather than in the FaultLine checkout.

```powershell
# In the FaultLine checkout.
pnpm install --frozen-lockfile
pnpm build
$FaultLineCli = (Resolve-Path .\dist\cli.js).Path
$TargetRepo = (Resolve-Path C:\path\to\the\application).Path

# All remaining commands in this guide run from the application repository.
Push-Location $TargetRepo
```

## 1. Check the machine before you invest in setup

```powershell
node $FaultLineCli doctor --repo .
```

`fl doctor` is read-only. It checks Git, repository/worktree state, Node, the Docker CLI, the Docker daemon, and conventional runtime markers. It returns a nonzero exit code whenever the **machine preflight** is not ready, and `--json` emits the same structured report for CI or another UI. It deliberately reports image selection as `NOT_CHECKED`: a ready machine is not a selected image, a runnable witness, or a proof.

It does **not** pull an image, execute your command, access the network, or certify a proof. A dirty worktree is shown as an actionable condition because a clean Git checkpoint must be unambiguous.

## 1a. Optional: observe a Codex session without claiming private access

If Codex is helping with the incident, FaultLine can observe the documented public hook lifecycle (`SessionStart`, `UserPromptSubmit`, and `Stop`). This is opt-in: point the already-built CLI at the target application repository, review the complete hook document, and explicitly trust it in Codex. The installer writes nothing until `--yes`, creates only a new project `.codex/hooks.json`, and never replaces an existing hook document.

```powershell
# Preview the exact project hook document first; this makes no change.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli

# After review, create $TargetRepo\.codex\hooks.json exactly once.
node $FaultLineCli codex sidecar install --repo $TargetRepo --cli $FaultLineCli --yes
```

Open or restart Codex in `$TargetRepo`, then run `/hooks` to inspect, trust, enable, or disable FaultLine's commands before they run. Project-scoped hook configuration belongs only in a project you trust. The installer generates Unix and Windows-safe command variants automatically. If `$TargetRepo\.codex\hooks.json` already exists, FaultLine refuses to replace it; emit the same root document with `node $FaultLineCli codex sidecar config --cli $FaultLineCli`, review it, and merge the `hooks` object yourself. Advanced users can instead supply their own explicit `--command` and, when needed, `--command-windows` values to `sidecar config`.

The sidecar stores only allowlisted public IDs/metadata and a SHA-256 prompt digest; it never stores the prompt itself, assistant text, or a transcript path. Its operational ledger lives under local Git metadata rather than the worktree. On each `Stop`, it records a real checkpoint only if the worktree is clean. An ordinary agent edit therefore produces `CHECKPOINT_SKIPPED_DIRTY`, not a made-up turn checkpoint.

Before binding observed evidence to a frozen incident, inspect it and use the exact returned `ledgerPath`:

```powershell
node $FaultLineCli codex sidecar status --repo .
```

`status` exposes integrity, the latest checkpoint/skip state, and the path without printing prompt or transcript content. Add `--ledger <ledgerPath>` to the later `incident continue` command only when its clean checkpoints truly map to the selected Git states. A lifecycle binding can be full, partial, or absent; it is never a claim of model intent.

## 2. Prepare a runtime image only after you choose it

FaultLine includes small Node, Python, and Go catalogs for the common path. `fl runtime prepare` prints the exact effect first and does nothing until you add `--yes`. That explicit confirmation pulls only the chosen catalog tag, then records Docker's local immutable `RepoDigest`. The read-only `fl runtime resolve` path remains available when you have already prepared an image yourself.

```powershell
node $FaultLineCli runtime prepare node
# Inspect the declared Docker/network effect, then explicitly confirm it:
node $FaultLineCli runtime prepare node --yes
```

The resolver runs only a fixed, local `docker image inspect` command. It rejects a mutable tag, a digest from the wrong repository, or an unresolved image. The current catalog is:

| Alias | Reviewed tag to pull manually |
| --- | --- |
| `node` | `node:22-alpine` |
| `python` | `python:3.13-alpine` |
| `go` | `golang:1.24-alpine` |

These are starting environments, not universal build environments. The preparation pull is setup only, not a witness execution or proof. The proof runner has no network and mounts source read-only, so dependencies and the witness command must already be runnable in the selected image.

### For a normal project: make the dependency image an explicit setup step

When a base image cannot run the selected command, FaultLine can prepare a project-owned dependency image from a Dockerfile you review. The plan is deliberately separate from replay: it fingerprints every regular file in the reviewable context and emits `plan.review.planDigest`. A build requires that exact digest plus `--yes`, so an edited Dockerfile, context file, tag, or network choice is refused for re-review. It never uses a host shell, pushes an image, or sends registry credentials.

```powershell
node $FaultLineCli runtime project plan `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default

# Copy the printed plan.review.planDigest only after reviewing the full plan.
node $FaultLineCli runtime project build `
  --context . `
  --dockerfile Dockerfile.faultline `
  --tag registry.example/acme/my-app:faultline-deps-20260717 `
  --network default `
  --expect-plan <plan-review-digest> `
  --yes
```

`none` is the default build network and disables network access for Dockerfile `RUN` steps. It does not certify Docker daemon or base-image resolution networking; use `default` only when the reviewed Dockerfile needs networked `RUN` steps. FaultLine refuses contexts with links, special files, more than 10,000 files, or more than 256 MiB, because it cannot bind those safely to the reviewed plan. The proof sandbox binds Git source at `/workspace/src`; bake reusable dependencies outside that mount (for example, a Node image can install into its parent `/workspace/node_modules`) so the read-only source bind does not hide them. The image and selected Git states must still be compatible; a setup image does not prove that changing lockfiles or unavailable private dependencies are reproducible.

Docker often exposes only a local image ID after a build. FaultLine refuses to call that a portable proof image. Push and pull the reviewed tag through your own registry workflow, then resolve the locally reported immutable `RepoDigest` without rebuilding:

```powershell
node $FaultLineCli runtime project resolve --tag registry.example/acme/my-app:faultline-deps-20260717
```

Use the returned `repository@sha256:...` value as `--image` when starting the incident. For another stack, use your own reviewed digest-pinned image directly.

## 3. Suggest a bracket, then create a draft; FaultLine does not execute it

Paste the command that is failing in CI or locally. If you do not know its bracket, first request local-only suggestions. FaultLine can show a locally cached upstream merge-base and/or `HEAD`'s parent; it does not fetch, contact a forge, parse CI, or automatically choose one. Every candidate is a human-review input, not a verdict.

```powershell
node $FaultLineCli incident suggest --repo .
```

After reviewing a candidate, pass its exact commits to intake. With no `--from` / `--to`, FaultLine accepts only a conservative locally observed one-parent `HEAD~1 -> HEAD` bracket. It refuses a root commit or merge head rather than guessing a base.

```powershell
node $FaultLineCli incident start `
  --repo . `
  --command "pnpm test -- checkout" `
  --from <reviewed-from-commit> `
  --to <reviewed-to-commit> `
  --runtime node
```

The command writes two immutable, local artifacts:

- `.faultline/incidents/drafts/<id>.json`: a content-addressed intake record containing the exact command digest, selected Git bracket, optional resolved image digest, and the immutable proposal digests it created. It is `PROPOSED`, never approved or frozen.
- `.faultline/witnesses/proposals/<id>.json`: a human-origin witness proposal with no overlays by default.

It does not execute the command, pull an image, fetch or auto-select a remote base, approve a witness, or freeze a witness. Give `--id <safe-id>` to choose the record identifier, provide both `--from` and `--to` for an explicit bracket, or use `--image <repository@sha256:...>` to bind an already resolved project image instead of a curated `--runtime` alias.

## 4. Review, approve, and freeze explicitly

Open the local review workbench returned by intake (or run the command below). It binds only to `127.0.0.1`, disables browser caching, and shows the exact command, canonical base64 overlay bytes, and execution policy without executing or materializing them. Raw CI-log text stays off the browser page. Treat the visible command and overlays as sensitive incident material.

```powershell
node $FaultLineCli witness review <id>
```

The screen verifies the strict blinded-packet shape and proposal digests before it enables action. A human enters their reviewer identity, explicitly acknowledges that they reviewed the exact command and visible overlay bytes, and clicks **Approve this reviewed witness**, then makes a separate explicit **Freeze this approved witness** click. Each click requires the current review digest and re-reads the write-once proposal; a stale, malformed, unblinded, or digest-mismatched proposal is refused before FaultLine writes an approval or freeze record.

The freeze screen contains the externally retainable frozen digest. Keep it with the incident record before asking FaultLine to localize anything. A displayed reviewer name remains an assertion unless you add the optional reviewer signature workflow.

## 5. Continue the same incident and share the real incident page

Do not retype the Git range, proposal ID, or selected runtime. `fl incident continue` re-reads the immutable draft and the frozen witness, refuses any mismatch, and uses the recorded bracket. Check the durable state first. Proof-grade continuation requires the frozen digest retained outside the witness store after review; without it, `status` reports `RETAIN_DIGEST_REQUIRED` and `continue` refuses to publish proof:

```powershell
node $FaultLineCli incident status <id> --expect-digest <frozen-digest>
node $FaultLineCli incident continue <id> --image <resolved-image@sha256:...> --expect-digest <frozen-digest> [--ledger <ledgerPath-from-sidecar-status>]
```

If intake recorded a curated `--runtime` or explicit `--image`, its resolved digest is already bound to the draft, so omit `--image`. An explicit `--image` at continuation must exactly match that recorded digest. `continue` does not select a new base, mutate the draft, approve/freeze a witness, build/pull an image, or push to a registry. Its default output stays under the incident repository’s `.faultline/git-proof-bundles/` root.

For local diagnosis only, `fl incident continue <id> --unsafe-local` makes the non-proof boundary explicit: it can help validate wiring when Docker is unavailable, but it returns `INVESTIGATION_NOT_PROOF` and cannot publish a portable bundle. It is not a substitute for the Docker-isolated path in a demo or incident claim.

The lower-level command remains available for automation or an already-managed witness store:

```powershell
node $FaultLineCli investigate git `
  --repo . `
  --from <ancestor-from-the-draft> `
  --to <descendant-from-the-draft> `
  --proposal <id> `
  --expect-digest <frozen-digest> `
  --image <resolved-image@sha256:...>
```

A proof package is written only when Docker-isolated executions establish the required stable transition. Inspect it through the same five-beat product experience used by the demo:

```powershell
node $FaultLineCli serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root <retained-root-digest>
```

That page is read-only and verifies the package before rendering. It shows the real boundary, verification status, lifecycle binding when recorded, and explicit "not attached" states for minimization or repair artifacts that were not included. It never exposes witness behavior text, the raw command, overlay bytes, repository path, or raw output.

If you later run the counterfactual minimizer and generate a citation-validated repair brief, render them in this **same incident page** rather than switching back to a fixture-only story:

```powershell
node $FaultLineCli serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root <retained-root-digest> `
  --minimization .faultline\minimizations\<result>.json `
  --expect-minimization <retained-minimization-digest> `
  --repair .faultline\repair-briefs\<repair-id> `
  --expect-repair <retained-repair-artifact-digest>
```

FaultLine independently re-verifies each optional record against its retained digest. A minimization must match the displayed proof's frozen witness and stable pass-to-fail transition; a repair packet must match the exact investigation and frozen witness. The shareable page exposes only certification status, counts, and evidence IDs; free-form repair text stays in the private repair artifact. It labels attachments as separately verified downstream evidence, not as files retroactively covered by the Git bundle root.

## Failure modes and support boundary

| Situation | What FaultLine does | What to do |
| --- | --- | --- |
| Docker CLI or daemon unavailable | `fl doctor` reports the exact preflight check as unavailable. No proof is claimed. | Start Docker / fix daemon access, then rerun doctor. |
| Dirty worktree | Doctor reports an actionable count without leaking changed paths. | Commit, stash, or explicitly model changes before recording a checkpoint. |
| Codex Stop sees agent edits | Sidecar records `CHECKPOINT_SKIPPED_DIRTY`; it does not call a dirty tree an observed clean checkpoint. | Commit/stash/model the state, then wait for a later clean Stop or keep the lifecycle evidence unbound. |
| Sidecar hook did not record | Quiet hooks return control to Codex rather than breaking an agent turn; `status` exposes no ledger or invalid recorder state. | Run `fl codex sidecar status --repo .`, check the reviewed hook command and trust configuration, then start a new session. |
| Root or merge `HEAD` | Intake refuses a guessed predecessor. | Run `fl incident suggest --repo .` for local-only candidates, then supply both `--from` and `--to` after human review. |
| Curated image is not local | `fl runtime prepare <alias>` explains the exact pending Docker mutation and waits. | Review it, run `fl runtime prepare <alias> --yes`, then use the resolved digest. |
| Image tag exists but has no local digest | Runtime resolution refuses the mutable/unresolved tag. | Use `fl runtime prepare <alias> --yes`, or pull a reviewed tag yourself and resolve it again. |
| Project image plan no longer matches | FaultLine refuses to execute the Dockerfile. | Rerun `fl runtime project plan`, review its new context/Dockerfile digest, then supply the new `--expect-plan` value. |
| Project image build has no `RepoDigest` | FaultLine reports that a local image ID is not portable proof input and never pushes it. | For proof, push/pull the reviewed tag with your own registry workflow, then run `fl runtime project resolve --tag <repository:tag>`. For local wiring only, use the existing `incident continue --unsafe-local` route after human freeze; it is explicitly non-proof and never exports a portable bundle. |
| Draft and frozen witness disagree | `fl incident status` marks the incident invalid and `continue` refuses execution. | Review the immutable records; start a new incident rather than changing an approved witness. |
| Frozen digest was not retained outside the witness store | `status` reports `RETAIN_DIGEST_REQUIRED` and proof-grade `continue` refuses to run. | Retrieve the reviewed digest from the freeze screen, retain it independently, and supply `--expect-digest`; do not copy it from the stored record and call that external retention. |
| Command needs installs or network | The Docker runner's isolated policy prevents a proof-grade run. | Prepare a reviewed dependency image with `fl runtime project plan` / `build`, then bind its resolved digest at intake; otherwise use an explicit non-proof local diagnostic outside the claim. |
| Runs are noisy or Docker errors | FaultLine records an inconclusive/non-proof result and does not publish a portable proof package. | Fix the witness/environment and preserve the distinction from proof. |

Supported CLI development platforms are Windows, macOS, and Ubuntu with Node 22+. Native Docker proof is exercised on Ubuntu CI. The curated runtime catalog covers only the listed Node/Python/Go base families; it is not a claim that arbitrary applications, secrets, or private registries work without additional reviewed setup.
