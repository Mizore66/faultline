# Submission freeze marker (C-1)

Presence of this file activates `.github/workflows/verify.yml` job
`submission-freeze`: pushes/PRs to `main` that touch `src/**`, `package.json`,
or `pnpm-lock.yaml` fail unless `workflow_dispatch` sets `unfreeze_override=true`.

Docs, tests, scripts, workflows, and this file itself remain allowed.

**Judging pin:** `v0.1.10-buildweek` (EXT-01 fixes). Do **not** re-cut or move
that tag for routine improvements. `v0.1.9-buildweek` is historical (lacks
EXT-01) and must not move either.

The only carved exception for touching `src/` under freeze was incorporating the
EXT-01 Critical/High fixes into the judging artifact. After `v0.1.10-buildweek`
is cut and this file is present again: **mean it** — post-submission `src/`
work stays on feature branches (see Bucket 2 / `docs/post-submission/` when
present) until a human lifts the freeze.

Re-asserted: 2026-07-21 after the v0.1.10 pin-surface cut.
