# Draft release notes — v0.1.9-buildweek

> **Draft only.** Do not cut or move tags from this file. The human cuts and SSH-signs `v0.1.9-buildweek` after CI is green on the pin commit. Until then, enforceable pin surfaces (`README`, `package-smoke`, `tag-checkout`, CLI quickstart) remain on signed `v0.1.8-buildweek`. `v0.1.8-buildweek` remains historical and must not be moved or deleted.

## This is the only release intended for judging (once cut)

Independent review identified a **restored-clean staleness bug** in incremental dirty-tree staging (`cf11326`): when the temp index was seeded from a previous accepted dirty turn tree, a tracked file restored to HEAD bytes was neither in `dirtyPaths` nor absent from the stale base, so prior-turn bytes were retained. Fixed by always seeding dirty staging from `HEAD^{tree}`, with an oracle equivalence property test that kills the class.

### Also included

- Sol P0 hardening carried from `v0.1.8-buildweek`: hardened repaired-worktree Git (no host `git add` / clean filters), streaming fingerprint size caps, content-bound `PREVENTION_VERIFIED` execution IDs, Codespaces `.devcontainer`, npm `@mizore66/faultline@0.1.2`, paused continuous-release during freeze.
- Fresh turn-snapshot and scale benchmark numbers regenerated on the fixed HEAD-base staging code (see `docs/turn-snapshot-overhead.md`, `benchmarks/scale/SUMMARY.md`).
- CDX-09: optional Codex thread identifiers (`witnessDraftThreadId`, `repairThreadId`) in git/turn proof-bundle manifests — identifier strings only, bound into the package root; existing samples without the fields still verify.
- C-1: `SUBMISSION_FROZEN` CI path guard (file not created until the human freezes).
- Experimental non-blocking macOS Colima Docker E2E job (does not claim macOS coverage unless an artifact is produced).
- Experimental Codex plugin packaging scaffold under `plugin/` (not live-fire verified).

### Historical / not for judging

| Tag | Status |
| --- | --- |
| `v0.1.8-buildweek` | Historical previous pin (retain; do not move) |
| `v0.1.7-buildweek` and earlier buildweek tags | Historical |
| `v0.1.4` | **RETRACTED** — do not cite |

### Still human-gated (do not claim)

- Cold stranger Codespaces / comprehension scoresheet
- Demo video / `YOUTUBE_URL`
- SEC-08 cold-machine `git verify-tag` transcript
- TIME-01 / TIME-03 soak and `TURN_PROOF` promotion
- Plugin live-fire (CDX-08)
- External security review
