# Post-submission — Batch turn-snapshot staging (oracle-gated)

**Status:** parked until Devpost is submitted and `SUBMISSION_FROZEN` is lifted.  
**Rule:** touches `src/**` (and tests). Must not merge to `main` while freeze is active.  
**Judging pin context:** implement against post-freeze `main`; never retarget or move `v0.1.10-buildweek`.

## Intent

Reduce per-path staging cost in `src/turn-snapshot.ts` (`writeThrowawayTreeDigest` / `updatePaths`) by batching Git object hashing and index updates, without changing tree digests relative to the current per-file implementation.

Today each update path does:

1. `lstat` + mode selection (`100644` / `100755`)
2. full file read
3. `git hash-object -w --stdin`
4. `git update-index --add --cacheinfo MODE,BLOB,PATH`

Multi-file modified rows in `docs/turn-snapshot-overhead.md` show this dominates (medium 100-file modified p50 ~21s). Batching is the next honest performance step.

## Design correction (required)

The earlier draft proposed:

```text
git hash-object -w --stdin-paths
```

with NUL-separated paths. That is incorrect:

- Git documents `--stdin-paths` as **one pathname per line**, not NUL-delimited.
- Filenames containing newline (or CR) cannot be represented safely with newline-delimited `--stdin-paths`.
- Implicit filter behavior is weaker than passing `--no-filters` explicitly for raw file bytes.

### Chosen approach: Option A — restricted fast path

Use newline-delimited `--stdin-paths --no-filters` for the common case; fall back to the existing safe per-file `--stdin` path when a filename is unsafe for that protocol.

#### Fast path (allowed only when every update path is safe)

Preconditions — every relative path in the batch must:

- contain no `U+000A` (LF) and no `U+000D` (CR)
- not begin with `-` (or be passed after `--` / as `./-name` consistently with oracle)
- be a regular file (not symlink, not directory) after the existing plan/secret-scan gates

Then:

1. Build the ordered list of absolute paths for `updatePaths` (same order as today's loop).
2. Run once:

   ```text
   git hash-object -w --stdin-paths --no-filters
   ```

   with **newline-delimited** absolute paths on stdin (one path per line).
3. Parse stdout as one object ID per input line, in the same order.
4. Feed a single `git update-index --index-info` write with lines:

   ```text
   MODE SP OBJECT_ID TAB relative/path
   ```

   using the mode already determined by existing `lstat` / executable logic.
5. Removals: keep today's `update-index --force-remove` loop, **or** fold deletes into `--index-info` with mode `0` if that stays oracle-equivalent.

#### Fallback path (required)

If **any** update path fails the preconditions above, do **not** use `--stdin-paths` for that staging call. Use the existing per-file:

```text
git hash-object -w --stdin --no-filters
git update-index --add --cacheinfo ...
```

Fallback may be whole-batch (simpler, preferred for v1) or per-unsafe-path mixed with a safe sub-batch — either is fine if the oracle gate passes.

### Option B (deferred alternative)

Stream each file's bytes to `hash-object --stdin --no-filters`, preserve a deterministic path→OID map, and batch index updates separately. Prefer Option A first; document any later switch as a new PR with the same oracle gate.

## Must cover (acceptance criteria)

| Topic | Requirement |
|-------|-------------|
| Executable modes | `100644` vs `100755` must match current `lstat` logic |
| Symlinks | Remain rejected / excluded by existing plan gates; never hashed as file bytes via this fast path |
| Deletions | Oracle-equivalent to current `--force-remove` behavior |
| SHA-1 vs SHA-256 repos | Work in both; object IDs are whatever `hash-object` returns for the repo |
| Output ordering | OID lines must map 1:1 to input path order; mismatch → hard error, no partial index write |
| Bounded I/O | Cap stdout/stderr buffers; refuse unbounded capture |
| Partial command failure | Non-zero exit, truncated OID list, or malformed OID → abort staging; leave no half-applied index update for that throwaway index |
| Windows paths | Absolute paths must round-trip through the same Git used for the repo; no silent path rewrite |
| Paths beginning with `-` | Detected as unsafe for bare `--stdin-paths` lists; use fallback (or `./` prefix only if oracle proves equivalence) |
| Newline / CR in filenames | Detected; force fallback |
| `--no-filters` | Always pass on both fast and fallback hash paths so attribute clean filters cannot change bytes |
| Oracle comparison | Digests must match current implementation for identical worktrees |

## Hard gates (ship blockers)

- `FAULTLINE_ORACLE_RUNS=200` green (`tests/turn-snapshot-oracle.test.ts`)
- Restored-clean regression suite green (HEAD-base staging; restored-clean + one dirty)
- `pnpm measure:turn-snapshot` regenerates `docs/turn-snapshot-overhead.md` with **honest** remeasured numbers (no invented speedups)
- No reintroduction of `preferredBaseTree`
- Cache-key fields change only if required for correctness already on main — prefer no cache-key churn

## Non-goals

- Do not reintroduce `preferredBaseTree`
- Do not change quiescence / secret-scan / size-cap policy in this PR
- Do not claim wall-clock improvement without regenerated measurement docs
- Do not merge under `SUBMISSION_FROZEN`

## Implementation sketch (when freeze lifts)

Primary touchpoint: `src/turn-snapshot.ts` → `writeThrowawayTreeDigest` update loop (~`updatePaths`).

Suggested split for review:

1. Extract helpers: `pathsSafeForStdinPaths`, `batchHashObjectsNoFilters`, `batchUpdateIndexInfo`
2. Wire Option A + whole-batch fallback
3. Oracle + restored-clean tests
4. Remeasure and commit overhead doc/json

## Rollback

Revert the implementation PR. Staging returns to per-file `--stdin` + `--cacheinfo`. No pin/tag involvement.
