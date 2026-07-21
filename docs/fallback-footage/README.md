# Fallback footage (machine-captured)

Pin: `v0.1.10-buildweek`  
Generator: `node scripts/capture-fallback-footage.mjs`

| Asset | Purpose |
| --- | --- |
| `judge-path.cast` | asciinema v2 cast of clone → pin → doctor → judge-proof → verify → live-git export |
| `judge-path.log` | Plain-text twin for editors who do not use asciinema |

**Not captured here (human / env-gated):** `fl witness propose --live`, `fl demo full` (Docker), live Codex `/feedback`.

Replay: `asciinema play docs/fallback-footage/judge-path.cast`  
Render (optional, local): `agg docs/fallback-footage/judge-path.cast docs/fallback-footage/judge-path.gif`

Honesty: this is the same command sequence as [video-teleprompter.md](../video-teleprompter.md) machine-safe beats — not a substitute for the narrated Devpost video.
