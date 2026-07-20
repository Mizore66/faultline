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

### 1. npm Automation token

1. https://www.npmjs.com/settings/~/tokens  
2. Generate **Granular Access Token** (or classic **Automation**) with:
   - Read and write for `@mizore66/faultline`
   - **Bypass 2FA for automation** / CI publish permission enabled  
3. Copy the token once.

### 2. GitHub Actions secret

Repo → **Settings → Secrets and variables → Actions → New repository secret**

| Name | Value |
| --- | --- |
| `NPM_TOKEN` | the npm token from step 1 |

Or from a machine with `gh` auth:

```powershell
gh secret set NPM_TOKEN --repo Mizore66/faultline
# paste token, Enter, Ctrl+Z/Enter (Windows) or Ctrl+D (Unix)
```

**Do not** commit tokens. Rotate any token that was pasted into chat.

### 3. npm package permissions

On https://www.npmjs.com/package/@mizore66/faultline → **Settings**:

- Require 2FA / trusted publishers as you prefer  
- Ensure the token’s user can publish the scope `@mizore66`

Optional (stronger): configure **Trusted Publishing** from GitHub Actions so publishes use OIDC instead of a long-lived token (then `NPM_TOKEN` can be removed later).

## Manual release

Actions → **Release and publish** → **Run workflow** → choose `patch` / `minor` / `major`.

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
