# Zero-install & Codespaces paths (DEMO-01)

Three ways to touch FaultLine without building the world from scratch. Prefer the pinned Build Week tag for judges.

**Pin:** `v0.1.10-buildweek` (SSH-signed — [SEC-08](security-chain.md))  
**Judging path:** clone / Codespaces on the pin (B or C). Published npm may lag and is **not** the EXT-01 judging artifact.

## A. npm global (non-judging / convenience only)

Requires Node.js 22+. Prefer paths B/C for Build Week judging.

```bash
npm i -g @mizore66/faultline@0.1.2
fl --help
fl doctor --proof-ready
```

```powershell
npm i -g @mizore66/faultline@0.1.2
fl --help
fl doctor --proof-ready
```

Global `fl` does **not** ship the repo’s committed sample bundles and may lack EXT-01 fixes. For the judge sample UI, use path B or C (clone / Codespaces) and run `pnpm fl judge-proof`.

## B. GitHub Codespaces (one-click workspace)

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/Mizore66/faultline?ref=v0.1.10-buildweek)

Or: repo → **Code** → **Codespaces** → **Create codespace on `v0.1.10-buildweek`**.

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
git checkout v0.1.10-buildweek
corepack enable
pnpm install --frozen-lockfile
pnpm fl doctor --proof-ready
pnpm fl judge-proof
```

**Windows:** enable Corepack / install pnpm **before** any `npm install` in the repo. `prepare` runs `pnpm run build`; plain `npm install` fails with `'pnpm' is not recognized` if pnpm is missing (retained stranger friction: [samples/comprehension/2026-07-21-subject-b.md](samples/comprehension/2026-07-21-subject-b.md)). Prefer `pnpm.cmd` if ExecutionPolicy blocks `pnpm`.

## What each path is for

| Path | Best for | Needs Docker? |
| --- | --- | --- |
| A npm global | Smoke `fl` CLI | No |
| B Codespaces | Judges / strangers with a browser | Only for live proof; `judge-proof` / HTML preview: no |
| C clone | Full rehearsal + video from pin | Same as B |

## Honesty

- Codespaces remains the preferred one-click judge path; a **local Windows** stranger setup + cold comprehension **PASS** is retained in [samples/comprehension/2026-07-21-subject-b.md](samples/comprehension/2026-07-21-subject-b.md) (DEMO-03). That sheet is **not** a Codespaces success claim.  
- Do not invent a YouTube URL — video is [#162](https://github.com/Mizore66/faultline/issues/162).  
- Related rehearsal: [demo-rehearsal.md](demo-rehearsal.md), Q&A [demo-qa.md](demo-qa.md), fallback [demo-fallback.md](demo-fallback.md), comprehension protocol [demo-comprehension-test.md](demo-comprehension-test.md).
