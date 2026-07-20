# Distribution (PROD-02)

FaultLine ships as the npm package `@mizore66/faultline` with bin `fl`.

## Local / CI distribution path (no registry required)

```powershell
pnpm pack
npm install -g .\mizore66-faultline-0.1.0.tgz
fl --help
```

CI already runs `pnpm pack --dry-run` and `pnpm test:package` (install from
tarball + smoke).

## Registry publish (maintainer)

Requires `NPM_TOKEN` / npm login for scope `@mizore66`:

```powershell
pnpm publish --access public
```

If unpublished, the pack+tarball path remains the supported distribution.

## Node SEA

Optional experimental SEA build is not required for Build Week. Prefer the
packed tarball until a SEA pipeline is retained in CI.
