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

## 5. Replay the frozen witness and share the real incident page

Use the exact range, frozen digest, and image digest recorded in the previous steps:

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

That page is read-only and verifies the package before rendering. It shows the real boundary, verification status, lifecycle binding when recorded, and explicit "not attached" states for minimization or repair artifacts that were not included. It never exposes the raw command, overlay bytes, repository path, or raw output.

## Failure modes and support boundary

| Situation | What FaultLine does | What to do |
| --- | --- | --- |
| Docker CLI or daemon unavailable | `fl doctor` reports the exact preflight check as unavailable. No proof is claimed. | Start Docker / fix daemon access, then rerun doctor. |
| Dirty worktree | Doctor reports an actionable count without leaking changed paths. | Commit, stash, or explicitly model changes before recording a checkpoint. |
| Root or merge `HEAD` | Intake refuses a guessed predecessor. | Supply both `--from` and `--to` after human review. |
| Image tag exists but has no local digest | Runtime resolution refuses the mutable/unresolved tag. | Pull a reviewed tag yourself and resolve it again, or use a known digest-pinned image. |
| Command needs installs or network | The Docker runner's isolated policy prevents a proof-grade run. | Prepare a suitable image with dependencies or use an explicit non-proof local diagnostic outside the claim. |
| Runs are noisy or Docker errors | FaultLine records an inconclusive/non-proof result and does not publish a portable proof package. | Fix the witness/environment and preserve the distinction from proof. |

Supported CLI development platforms are Windows, macOS, and Ubuntu with Node 22+. Native Docker proof is exercised on Ubuntu CI. The curated runtime catalog covers only the listed Node/Python/Go base families; it is not a claim that arbitrary applications, secrets, or private registries work without additional reviewed setup.
