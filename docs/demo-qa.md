# Demo Q&A — artifact-backed answers (DEMO-04)

Use these answers on camera. Do not invent statistics. Every answer points at a retained artifact.

## How do I try it with zero install?

Three paths in [demo-zero-install.md](demo-zero-install.md): prefer [GitHub Codespaces on the pin](https://codespaces.new/Mizore66/faultline?ref=v0.1.10-buildweek) or clone `v0.1.10-buildweek` for judging (npm global may lag EXT-01). Rehearsal checklist: [demo-rehearsal.md](demo-rehearsal.md).

## vs. Git bisect?

FaultLine freezes a **human-reviewed witness** (exact command + overlays + cage) and produces an offline-verifiable proof package for “where does *this* predicate first go bad?” Bisect finds a commit that changes *some* behavior; it does not bind a blinded incident packet, Docker cage policy, or portable proof root. See [differentiation.md](differentiation.md).

## Is GPT-5.6 necessary?

No. GPT-5.6 can draft a witness proposal (`fl witness propose --live`); a human still Approves & freezes. The product works with a human-authored proposal and with committed redacted samples under `docs/samples/gpt-5.6/`.

## What’s not proven?

- Host / Docker-daemon tamper-proofing (CI attestation binds receipt bytes + Actions identity only — [security-model.md](security-model.md)).
- Multi-OS organic soak / `TURN_PROOF` until TIME-01 closes ([#152](https://github.com/Mizore66/faultline/issues/152)).
- Third-party adoption scale, stranger loop (COH-10 / [#150](https://github.com/Mizore66/faultline/issues/150)), signed release until SEC-08 lands.

## Why trust the Docker runs happened?

Native Docker proof E2E runs in GitHub Actions on alpine and debian-bookworm images; hybrid coverage-matrix REPORT artifacts are attached to the Verify workflow. RIG-07 also has a **Windows host** fact file: `benchmarks/e2e-executed.json` (`osFamily: "windows"`, eight coverage-matrix IDs, `node:22-alpine` guest). Local `fl doctor` / Docker E2E tests exercise the same gate. This is logged execution, not a slide.

## What breaks at scale?

Turn-tree snapshots still refuse **dirty** worktrees above 2,000 eligible files. Clean worktrees reuse `HEAD^{tree}` (RIG-09). See `benchmarks/scale/SUMMARY.md` after re-running `node scripts/measure-scale-benchmark.mjs`.

## What’s this revert in your history?

Owned disclosure (SUB-03): we fabricated a multi-OS SIDE_CAR soak and briefly self-granted `TURN_PROOF`. Commit `457fabeb9d5ff2261cbbf1ced4673ff28c9074d0` deletes that sample, restores honest gates, and adds `tests/evidence-honesty.test.ts`. Release `v0.1.4-buildweek` is retracted. Full three-sentence disclosure lives in [build-week-submission-kit.md](build-week-submission-kit.md) claims inventory.
