# CI incident intake action

FaultLine's root action gives a failed CI job a safe handoff into the same human-reviewed incident path as the local CLI. It records a prerequisite report, an immutable incident draft, and a witness proposal. It does **not** execute the supplied command, pull an image, approve a witness, freeze a witness, upload evidence, or assert that CI has produced a proof.

Use it after checking out the repository that contains the failure. The action itself is built from the pinned FaultLine revision, while its `--repo` target is the checked-out application repository.

```yaml
jobs:
  preserve-failed-witness:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - id: faultline
        uses: Mizore66/faultline@main
        with:
          command: pnpm test -- checkout
          from: ${{ github.event.before }}
          to: ${{ github.sha }}
      - uses: actions/upload-artifact@v4
        with:
          name: faultline-intake
          path: |
            ${{ steps.faultline.outputs.doctor-report }}
            ${{ steps.faultline.outputs.intake-report }}
            .faultline/incidents/
            .faultline/witnesses/
```

Pin the action to a reviewed immutable tag or commit for a production workflow. `main` is shown only to make the example readable.

## Inputs and range selection

`command` is required and treated as opaque text. The action passes it as one CLI argument; neither Bash nor FaultLine executes it during intake.

Provide `from` and `to` together when the CI event exposes a reliable green-to-red bracket. If both are omitted, FaultLine permits only its conservative locally observed `HEAD~1 -> HEAD` fallback; merge heads and root commits are refused instead of selecting a remote base. `runtime` is optional and accepts only `node`, `python`, or `go`; it resolves an already-local image digest and never pulls an image.

## Human gate and artifacts

### Restore an intake locally

The action is intentionally review-only and runs from its own checked-out FaultLine revision; it does not install a CLI into the application repository. To continue an intake, use a local checkout of the **same application revision named by the action's `to` input** and a built FaultLine checkout:

```powershell
# Restore the failed application revision first. Extract the `faultline-intake`
# artifact into this directory so its .faultline\incidents and .faultline\witnesses
# folders sit beside the application checkout.
git clone <application-repository> incident-repo
Set-Location incident-repo
git switch --detach <to-commit-from-the-action-run>

# Build FaultLine once, outside the application repository.
git clone https://github.com/Mizore66/faultline.git ..\faultline-tool
Set-Location ..\faultline-tool
pnpm install --frozen-lockfile
pnpm build
Set-Location ..\incident-repo
```

Download the artifact from the workflow run (through the GitHub Actions UI or `gh run download`) and extract it into `incident-repo` before continuing. The workbench prints a localhost URL rather than automatically launching a browser.

The action’s `draft-path` and `proposal-id` outputs point to a proposal in the `PROPOSED`, `NOT_FROZEN` state. A human must review it and then make separate explicit approval and freeze actions before any proof-grade Git replay. Retain the two JSON reports and the `.faultline` records as CI artifacts if they matter to an incident review.

After downloading those artifacts into a local checkout, open the same workbench with the action output’s proposal ID:

```powershell
node ..\faultline-tool\dist\cli.js witness review <proposal-id> `
  --store .faultline\witnesses `
  --draft-store .faultline\incidents
```

The page verifies that the incident draft’s range, optional selected runtime, packet digest, and exact command binding all match the proposal before it enables separate approval and freeze actions.

After freezing, use the same durable object rather than manually copying its range and witness identifiers into `fl investigate git`:

```powershell
node ..\faultline-tool\dist\cli.js incident status <proposal-id> `
  --repo . `
  --store .faultline\witnesses `
  --draft-store .faultline\incidents `
  --expect-digest <retained-frozen-digest>
node ..\faultline-tool\dist\cli.js incident continue <proposal-id> `
  --repo . `
  --store .faultline\witnesses `
  --draft-store .faultline\incidents `
  --image <digest-pinned-image> `
  --expect-digest <retained-frozen-digest>
```

`continue` reuses only the recorded draft range and matching frozen witness. It never reruns the pasted CI command until after the human freeze, selects a remote base, or silently changes a selected runtime. If the action did not resolve a curated runtime, pass a reviewed digest-pinned image explicitly; a machine with Docker alone is not a selected proof environment.

`doctor-report` is produced even when `fl doctor` exits non-zero—Docker or a clean checkpoint may be unavailable in a CI environment. Its `doctor-exit-code` output retains that status without treating it as proof readiness. The action still fails if the requested intake cannot produce a valid review-only draft.
