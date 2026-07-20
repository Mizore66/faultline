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

### 1. Trusted Publishing (recommended)

On https://www.npmjs.com/package/@mizore66/faultline → **Settings** → **Trusted Publisher**:

| Field | Value |
| --- | --- |
| Provider | GitHub Actions |
| Organization/user | `Mizore66` |
| Repository | `faultline` |
| Workflow filename | `release.yml` |
| Environment | *(leave empty)* |

Save. The next workflow run can publish with provenance and no long-lived token.

### 2. Fallback: npm Automation token + GitHub secret

1. https://www.npmjs.com/settings/~/tokens → **Automation** (or granular with publish + **bypass 2FA**)
2. Repo → **Settings → Secrets and variables → Actions** → `NPM_TOKEN`

```powershell
gh secret set NPM_TOKEN --repo Mizore66/faultline
```

**Rotate** any token that was pasted into chat — treat it as burned.

### 3. Scope access

The publishing identity (trusted publisher or token owner) must be able to publish `@mizore66/faultline`.

## Manual release

Actions → **Release and publish** → **Run workflow** → choose `patch` / `minor` / `major`.

If a GitHub Release/tag already exists but npm failed (auth), re-run with **`publish_only: true`** — that publishes the current `package.json` version without bumping again.

### npm E404 on publish

A `404 Not Found` on `PUT …/@scope%2fpackage` almost always means **auth/permissions**, not “package missing”:

- Token revoked, truncated when pasted into the secret, or not an **Automation** / publish-capable granular token
- Token user is not a member/owner of `@mizore66` on npm
- 2FA required and the token cannot bypass it for CI

Fix: rotate the token, update `NPM_TOKEN`, then run **publish_only**.
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
