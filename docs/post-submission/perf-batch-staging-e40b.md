# Post-submission queue — Batch update-index/--stdin-paths staging (oracle-gated)

**Status:** parked until Devpost is submitted and `SUBMISSION_FROZEN` is lifted.

**Rule:** touches `src/**` (and possibly tests). Must not merge to main while freeze is active.

## Intent
Batch update-index/--stdin-paths staging (oracle-gated)

## Gate
- Oracle equivalence (`tests/turn-snapshot-oracle.test.ts`) for any staging/perf change
- No pin/tag moves
- No evidence-grade / TURN_PROOF promotion machinery
- No plugin live-fire claims

## Next engineering steps
See parent note in PR / issue body when work begins.

## Concrete approach (when freeze lifts)

In `src/turn-snapshot.ts` staging loop (~updatePaths):

1. Build a list of `{mode, absPath, relativePath}` for updatePaths (existing lstat/mode logic).
2. Batch hash: `git hash-object -w --stdin-paths` with NUL-separated absolute paths on stdin (still no path-based clean filters — stdin-paths hashes file bytes without applying clean filters when `-w` writes objects; verify with oracle).
3. Batch index: feed `git update-index --index-info` lines of the form `MODE SP SHA1 TAB path` (one write).
4. Keep removals as today (`update-index --force-remove`) or fold into index-info with mode `0` / delete records if cleaner.
5. Refuse to ship without:
   - `FAULTLINE_ORACLE_RUNS=200` green
   - restored-clean regression suite green
   - `pnpm measure:turn-snapshot` multi-file-modified row regenerating `docs/turn-snapshot-overhead.md` with honest numbers

## Non-goals
- Do not reintroduce `preferredBaseTree`
- Do not change cache-key fields except as required for correctness already on main
