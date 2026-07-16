# Your first FaultLine incident

This is the shortest honest path from a failing command to a shareable proof package. It is designed for a developer who has a Git worktree, a command that should pass, and Docker available for the final proof step.

FaultLine has two deliberately different states:

| State | What it means | Can it create a proof package? |
| --- | --- | --- |
| Review-only intake | A command and Git bracket are recorded for a human to inspect. Nothing has run, been approved, or been frozen. | No |
| Proof-grade replay | The exact frozen witness ran in isolated Docker against immutable Git states. | Only if the recorded executions establish the required stable transition. |

## 1. Check the machine before you invest in setup

```powershell
pnpm fl -- doctor --repo .
```

`fl doctor` is read-only. It checks Git, repository/worktree state, Node, the Docker CLI, the Docker daemon, and conventional runtime markers. It returns a nonzero exit code whenever the **machine preflight** is not ready, and `--json` emits the same structured report for CI or another UI. It deliberately reports image selection as `NOT_CHECKED`: a ready machine is not a selected image, a runnable witness, or a proof.

It does **not** pull an image, execute your command, access the network, or certify a proof. A dirty worktree is shown as an actionable condition because a clean Git checkpoint must be unambiguous.

## 2. Resolve a curated runtime only after you choose it

FaultLine includes small Node, Python, and Go catalogs for the common path. The curated runtime resolver and guided intake never silently pull an image: pull a reviewed tag yourself, then record Docker's local immutable `RepoDigest`.

```powershell
docker pull node:22-alpine
pnpm fl -- runtime resolve node
```

The resolver runs only a fixed, local `docker image inspect` command. It rejects a mutable tag, a digest from the wrong repository, or an unresolved image. The current catalog is:

| Alias | Reviewed tag to pull manually |
| --- | --- |
| `node` | `node:22-alpine` |
| `python` | `python:3.13-alpine` |
| `go` | `golang:1.24-alpine` |

These are starting environments, not universal build environments. The proof runner has no network and mounts source read-only, so dependencies and the witness command must already be runnable in the selected image. For any other stack, use your own explicit digest-pinned image.

## 3. Create a draft; FaultLine does not execute it

Paste the command that is failing in CI or locally. With no `--from` / `--to`, FaultLine accepts only a conservative, locally observed one-parent `HEAD~1 -> HEAD` bracket. It refuses a root commit or merge head rather than choosing a remote/PR base.

```powershell
pnpm fl -- incident start `
  --repo . `
  --command "pnpm test -- checkout" `
  --runtime node
```

The command writes two immutable, local artifacts:

- `.faultline/incidents/drafts/<id>.json`: a content-addressed intake record containing the exact command digest, selected Git bracket, optional resolved image digest, and the immutable proposal digests it created. It is `PROPOSED`, never approved or frozen.
- `.faultline/witnesses/proposals/<id>.json`: a human-origin witness proposal with no overlays by default.

It does not execute the command, pull an image, infer a remote base, approve a witness, or freeze a witness. Give `--id <safe-id>` to choose the record identifier, or provide both `--from` and `--to` for an explicit bracket.

## 4. Review, approve, and freeze explicitly

Open the local review workbench returned by intake (or run the command below). It binds only to `127.0.0.1`, disables browser caching, and shows the exact command, canonical base64 overlay bytes, and execution policy without executing or materializing them. Raw CI-log text stays off the browser page. Treat the visible command and overlays as sensitive incident material.

```powershell
pnpm fl -- witness review <id>
```

The screen verifies the strict blinded-packet shape and proposal digests before it enables action. A human enters their reviewer identity, explicitly acknowledges that they reviewed the exact command and visible overlay bytes, and clicks **Approve this reviewed witness**, then makes a separate explicit **Freeze this approved witness** click. Each click requires the current review digest and re-reads the write-once proposal; a stale, malformed, unblinded, or digest-mismatched proposal is refused before FaultLine writes an approval or freeze record.

The freeze screen contains the externally retainable frozen digest. Keep it with the incident record before asking FaultLine to localize anything. A displayed reviewer name remains an assertion unless you add the optional reviewer signature workflow.

## 5. Continue the same incident and share the real incident page

Do not retype the Git range, proposal ID, or selected runtime. `fl incident continue` re-reads the immutable draft and the frozen witness, refuses any mismatch, and uses the recorded bracket. Check the durable state first. Proof-grade continuation requires the frozen digest retained outside the witness store after review; without it, `status` reports `RETAIN_DIGEST_REQUIRED` and `continue` refuses to publish proof:

```powershell
pnpm fl -- incident status <id> --expect-digest <frozen-digest>
pnpm fl -- incident continue <id> --image <resolved-image@sha256:...> --expect-digest <frozen-digest>
```

If intake recorded `--runtime node`, `python`, or `go`, its resolved digest is already bound to the draft, so omit `--image`. An explicit `--image` must exactly match that recorded digest. `continue` does not select a new base, mutate the draft, approve/freeze a witness, or pull an image. Its default output stays under the incident repository’s `.faultline/git-proof-bundles/` root.

For local diagnosis only, `fl incident continue <id> --unsafe-local` makes the non-proof boundary explicit: it can help validate wiring when Docker is unavailable, but it returns `INVESTIGATION_NOT_PROOF` and cannot publish a portable bundle. It is not a substitute for the Docker-isolated path in a demo or incident claim.

The lower-level command remains available for automation or an already-managed witness store:

```powershell
pnpm fl -- investigate git `
  --repo . `
  --from <ancestor-from-the-draft> `
  --to <descendant-from-the-draft> `
  --proposal <id> `
  --expect-digest <frozen-digest> `
  --image <resolved-image@sha256:...>
```

A proof package is written only when Docker-isolated executions establish the required stable transition. Inspect it through the same five-beat product experience used by the demo:

```powershell
pnpm fl -- serve `
  --bundle .faultline\git-proof-bundles\<investigation> `
  --expect-root <retained-root-digest>
```

That page is read-only and verifies the package before rendering. It shows the real boundary, verification status, lifecycle binding when recorded, and explicit "not attached" states for minimization or repair artifacts that were not included. It never exposes witness behavior text, the raw command, overlay bytes, repository path, or raw output.

If you later run the counterfactual minimizer and generate a citation-validated repair brief, render them in this **same incident page** rather than switching back to a fixture-only story:

```powershell
pnpm fl -- serve `
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
| Root or merge `HEAD` | Intake refuses a guessed predecessor. | Supply both `--from` and `--to` after human review. |
| Image tag exists but has no local digest | Runtime resolution refuses the mutable/unresolved tag. | Pull a reviewed tag yourself and resolve it again, or use a known digest-pinned image. |
| Draft and frozen witness disagree | `fl incident status` marks the incident invalid and `continue` refuses execution. | Review the immutable records; start a new incident rather than changing an approved witness. |
| Frozen digest was not retained outside the witness store | `status` reports `RETAIN_DIGEST_REQUIRED` and proof-grade `continue` refuses to run. | Retrieve the reviewed digest from the freeze screen, retain it independently, and supply `--expect-digest`; do not copy it from the stored record and call that external retention. |
| Command needs installs or network | The Docker runner's isolated policy prevents a proof-grade run. | Prepare a suitable image with dependencies or use an explicit non-proof local diagnostic outside the claim. |
| Runs are noisy or Docker errors | FaultLine records an inconclusive/non-proof result and does not publish a portable proof package. | Fix the witness/environment and preserve the distinction from proof. |

Supported CLI development platforms are Windows, macOS, and Ubuntu with Node 22+. Native Docker proof is exercised on Ubuntu CI. The curated runtime catalog covers only the listed Node/Python/Go base families; it is not a claim that arbitrary applications, secrets, or private registries work without additional reviewed setup.
