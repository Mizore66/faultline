# Zero-install & Codespaces paths (DEMO-01)

Three ways to touch FaultLine without building the world from scratch. Prefer the pinned Build Week tag for judges.

**Pin:** `v0.1.7-buildweek` (SSH-signed — [SEC-08](security-chain.md))  
**Package:** `@mizore66/faultline@0.1.1` on npm

## A. npm global (fastest CLI)

Requires Node.js 22+.

```bash
npm i -g @mizore66/faultline@0.1.1
fl --help
fl doctor --proof-ready
```

```powershell
npm i -g @mizore66/faultline@0.1.1
fl --help
fl doctor --proof-ready
```

Global `fl` does **not** ship the repo’s committed sample bundles. For the judge sample UI, use path B or C (clone / Codespaces) and run `pnpm fl judge-proof`.

## B. GitHub Codespaces (one-click workspace)

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/Mizore66/faultline?ref=v0.1.7-buildweek)

Or: repo → **Code** → **Codespaces** → **Create codespace on `v0.1.7-buildweek`**.

The [`.devcontainer/devcontainer.json`](../.devcontainer/devcontainer.json) image:

1. Enables pnpm via Corepack  
2. Runs `pnpm install --frozen-lockfile` + `pnpm build`  
3. Includes Docker-in-Docker for optional `demo live-git` / proof-ready paths  

Then in the Codespaces terminal:

```bash
pnpm fl doctor --proof-ready
FAULTLINE_NO_BROWSER=1 pnpm fl judge-proof --export-only
```

Static HTML without CLI: open [`docs/self-incident-proof-preview.html`](self-incident-proof-preview.html) from the file tree.

## C. Clone the pin (full artifacts)

```bash
git clone https://github.com/Mizore66/faultline.git
cd faultline
git checkout v0.1.7-buildweek
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

## What each path is for

| Path | Best for | Needs Docker? |
| --- | --- | --- |
| A npm global | Smoke `fl` CLI | No |
| B Codespaces | Judges / strangers with a browser | Only for live proof; `judge-proof` / HTML preview: no |
| C clone | Full rehearsal + video from pin | Same as B |

## Honesty

- Codespaces is **scaffolding** until a stranger run is retained (note date/runner in [#148](https://github.com/Mizore66/faultline/issues/148)).  
- Do not invent a YouTube URL — video is [#162](https://github.com/Mizore66/faultline/issues/162).  
- Related rehearsal: [demo-rehearsal.md](demo-rehearsal.md), Q&A [demo-qa.md](demo-qa.md), fallback [demo-fallback.md](demo-fallback.md), comprehension protocol [demo-comprehension-test.md](demo-comprehension-test.md).
