# Continuous release + npm publish

On every **code** push to `main`, GitHub Actions:

1. Runs typecheck / test / build  
2. Bumps `package.json` (patch by default)  
3. Commits `chore(release): x.y.z` and pushes annotated tag `vx.y.z`  
4. Creates a GitHub Release  
5. Publishes `@mizore66/faultline@x.y.z` to npm  

Docs-only / benchmark-only pushes do **not** release. Commits that already start with `chore(release):` do not re-enter the workflow.

Build Week pins (`v*-buildweek`) stay manual and separate from these npm semver tags.

## One-time setup (required)

Prefer **Trusted Publishing** (OIDC). Keep `NPM_TOKEN` only as a fallback.

### Trusted Publishing checklist (OIDC)

On https://www.npmjs.com/package/@mizore66/faultline → **Settings → Trusted Publisher**:

| Field | Value |
| --- | --- |
| Provider | GitHub Actions |
| Organization/user | `Mizore66` |
| Repository | `faultline` |
| Workflow filename | `release.yml` (basename only) |
| Environment | *(leave empty — must match CI)* |
| Allowed actions | **`npm publish`** checked |

CI requirements (already in `release.yml`):

- Job permission `id-token: write`
- **No** `registry-url` on `actions/setup-node` (placeholder `_authToken` disables OIDC → fake `E404`)
- **No** `NODE_AUTH_TOKEN` / auth `.npmrc` during publish
- `npm install -g npm@latest` then plain `npm publish --provenance` (not `pnpm publish`)

If publish still `E404`s after this, the trusted-publisher row almost always mismatches (workflow name, env, or allowed actions).

## Manual release

Actions → **Release and publish** → **Run workflow** → choose `patch` / `minor` / `major`.

If a GitHub Release/tag already exists but npm failed (auth), re-run with **`publish_only: true`** — that publishes the current `package.json` version without bumping again.

### npm E404 on publish

A `404 Not Found` on `PUT …/@scope%2fpackage` during Trusted Publishing is usually a **masked OIDC failure**, not “package missing”:

- `registry-url` / `_authToken` / `NODE_AUTH_TOKEN` present → npm skips OIDC
- npm CLI older than **11.5.1**
- Trusted publisher workflow/env/allowed-actions mismatch

Fix the checklist above, then re-run with **`publish_only: true`**.

## Local dry-run

```powershell
node scripts/ci-bump-version.mjs patch   # edits package.json — discard after
pnpm pack --dry-run
```

## Honesty

npm’s public registry GET can 404 for a minute after the first publish; the workflow waits and then continues. Always confirm with:

```powershell
npm view @mizore66/faultline version
```
