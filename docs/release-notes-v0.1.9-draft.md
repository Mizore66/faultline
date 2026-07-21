# Release notes — v0.1.9-buildweek (HISTORICAL)

> **No longer the judging pin.** Retained SSH-signed annotated tag — do **not** move or delete.  
> **Current judging pin:** [`v0.1.10-buildweek`](release-notes-v0.1.10-buildweek.md) (includes EXT-01 security fixes).  
> Do **not** move or delete `v0.1.8-buildweek` either (historical previous pin).

## This was the judging release before EXT-01

Independent review identified a **restored-clean staleness bug** in incremental dirty-tree staging (`cf11326`): when the temp index was seeded from a previous accepted dirty turn tree, a tracked file restored to HEAD bytes was neither in `dirtyPaths` nor absent from the stale base, so prior-turn bytes were retained. Fixed by always seeding dirty staging from `HEAD^{tree}`, with an oracle equivalence property test that kills the class. Executable mode is included in the dirty content fingerprint / session-cache schema so mode-only changes cannot reuse a stale tree.

### Also included

- Sol P0 hardening carried from `v0.1.8-buildweek`: hardened repaired-worktree Git (no host `git add` / clean filters), streaming fingerprint size caps, content-bound `PREVENTION_VERIFIED` execution IDs, Codespaces `.devcontainer`, npm `@mizore66/faultline@0.1.2`, paused continuous-release during freeze.
- Fresh turn-snapshot and scale benchmark numbers regenerated on the fixed HEAD-base staging code (see `docs/turn-snapshot-overhead.md`, `benchmarks/scale/SUMMARY.md`).
- CDX-09: optional Codex thread identifiers (`witnessDraftThreadId`, `repairThreadId`) in git/turn proof-bundle manifests — identifier strings only, bound into the package root; existing samples without the fields still verify.
- C-1: `SUBMISSION_FROZEN` CI path guard (file not created until the human freezes).
- SEC-08: macOS cold-reader full chain retained in `docs/security-chain.md` (signed tag → CI attestation → provenance verify → sample-root verify).
- Experimental non-blocking macOS Colima Docker E2E job (does not claim macOS coverage unless an artifact is produced).
- Experimental Codex plugin packaging scaffold under `plugin/` (not live-fire verified).

### Why judges should not use this tag anymore

EXT-01 found FL-SEC-001 (Critical) in `fl record` checkpoint capture on unhardened Git. Fixes landed in `14ad89d` **after** this tag. Practical judge-flow risk on the documented demo path was nil, but the signed judging artifact still shipped the known Critical — corrected by `v0.1.10-buildweek`.

### Historical / not for judging

| Tag | Status |
| --- | --- |
| `v0.1.10-buildweek` | **Current** judging pin |
| `v0.1.9-buildweek` | This tag — historical (lacks EXT-01) |
| `v0.1.8-buildweek` | Historical previous pin (retain; do not move) |
| `v0.1.7-buildweek` and earlier buildweek tags | Historical |
| `v0.1.4` | **RETRACTED** — do not cite |
