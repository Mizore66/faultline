# Runtime preparation

Doctor preflight and digest-pinned Docker runtime setup. Full first-incident narrative (CI log intake, freeze, continue): [first-incident.md](first-incident.md). GitHub Action handoff: [github-action.md](github-action.md).

## Install boundary

**Supported install today:** clone `main` (MIT) and run via pnpm (see [Start here](../README.md#start-here-idea-first)).

```powershell
pnpm fl doctor --repo .
pnpm fl doctor --proof-ready   # nonzero unless Docker proof-grade is READY
```

The public package name `@mizore66/faultline` is reserved for a future registry publish. **npm/global install is not supported yet** — there is no published release. Do not run `npm install -g @mizore66/faultline` or `npm install faultline` until an owner-published version exists. To exercise the packaged binary before publish, use `pnpm pack` and install the local tarball (see [Package and clean-install use](proof-bundles.md#package-and-clean-install-use)).

## Doctor

Before making a real claim, let FaultLine surface local prerequisites:

```powershell
pnpm fl doctor --repo .
```

`fl doctor` exits 0 when Node can run the local CLI; `fl doctor --proof-ready` exits nonzero unless Docker proof-grade preflight is READY. Details of what doctor checks: [first-incident.md](first-incident.md#1-check-the-machine-before-you-invest-in-setup).

## Runtime prepare / resolve (catalog images)

For a common Node/Python/Go base image, use the guided setup command to review the exact Docker mutation first. Its first invocation does not touch Docker; only the explicit `--yes` invocation pulls the reviewed catalog tag, resolves the local immutable digest, and leaves a concrete next command. Add `--runtime node` (or `python` / `go`) to intake to bind that digest:

```powershell
pnpm fl runtime prepare node
# Review the displayed Docker pull effect, then explicitly confirm it:
pnpm fl runtime prepare node --yes
pnpm fl incident start --repo . --command "pnpm test -- checkout" --runtime node
```

```powershell
docker pull node:22-alpine
pnpm fl runtime resolve node
```

## Runtime project plan / build / resolve (dependency images)

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

## Guided intake pointer

For a CI log plus a failing command, use the resumable guided workflow. It creates the review-only draft, opens the local witness review workbench, pauses until you Approve and then Freeze (separate clicks), retains the freeze digest in-process, selects the runtime, localizes, and exports the proof package — without forcing you to retype subcommands:

```powershell
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

FaultLine never auto-approves or auto-freezes. Use `--unsafe-local` only for non-proof diagnosis when Docker is unavailable.

Local-only range suggestions (`fl incident suggest`), draft start, and status/continue details: [first-incident.md](first-incident.md).
