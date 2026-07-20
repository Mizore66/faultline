# Scale benchmark protocol (RIG-04)

## Purpose

Retain a reproducible ≥10k-file worktree scale measurement for FaultLine
localization-adjacent costs (fixture generation, file walk, Git commit, and
turn-snapshot attempt / refuse-at-cap).

## Method

```powershell
pnpm build
pnpm measure:scale
```

Optional: `FAULTLINE_SCALE_FILE_COUNT=12000 pnpm measure:scale`

Outputs:

- `benchmarks/scale/report.json` — raw measured timings
- `benchmarks/scale/SUMMARY.md` — human summary

## Caps

`TURN_SNAPSHOT_MAX_FILE_COUNT` is **2000**. A refuse-at-cap / failure outcome on
the turn-snapshot attempt at ≥10k files is an expected, honest result — not a
silent truncation claim.

## Non-claims

- Not a claim about Docker proof latency at 10k files.
- Not a claim about multi-machine reproducibility without re-running.
- Not a substitute for coverage-matrix adversarial scenarios.
