# FaultLine’s first self-incident — reproducible runbook

This is the canonical preparation runbook for FaultLine’s own historical CI regression. It is deliberately not a claim that a new portable proof bundle has already been recorded. Complete the human review/freeze and Docker-isolated replay below before using it as a demo or submission fact.

## The observed incident

Commit `5546831c87439cb71d2b183b11b76aa4e95ab4ca` added the provenance job’s first write to `.faultline/live-git-demo.json`, but did not create the `.faultline` directory. The GitHub Actions run therefore failed in the provenance job after its ordinary test and Docker jobs had passed:

```text
tee: .faultline/live-git-demo.json: No such file or directory
```

Commit `07ee7f11bb0cc7dabe2e6e20a2b780d9428dae77` fixed that exact workflow contract by adding `mkdir -p .faultline` before the `tee` command. The historical runs are [the failed run](https://github.com/Mizore66/faultline/actions/runs/29504579227) and [the succeeding fix run](https://github.com/Mizore66/faultline/actions/runs/29504804371).

FaultLine’s witness does **not** claim that `5546831` was the first overall CI failure, a semantic root cause, or an agent-intent event. It establishes only a narrower workflow predicate: when that provenance `tee` step exists, the directory-creation command must precede it.

## Prepare the reviewed witness

Use a clone that contains the historical commits. Pull the reviewed runtime yourself; FaultLine will only resolve its local immutable digest.

```powershell
docker pull node:22-alpine
pnpm fl -- runtime resolve node
```

Create a review-only draft. The witness returns nonzero only when the `tee` command exists and is missing a preceding `mkdir -p .faultline` command:

```powershell
pnpm fl -- incident start `
  --repo . `
  --id faultline-ci-provenance-directory `
  --from e8e3064 `
  --to 07ee7f1 `
  --runtime node `
  --command 'node -e "const fs=require(''node:fs''); const y=fs.readFileSync(''.github/workflows/verify.yml'',''utf8''); const tee=y.indexOf(''tee .faultline/live-git-demo.json''); const mkdir=y.indexOf(''mkdir -p .faultline''); if (tee >= 0 && (mkdir < 0 || mkdir > tee)) process.exit(1);"'
```

The command above **only records a draft**. It does not execute the witness, contact GitHub, approve anything, or freeze anything. Start the review workbench, inspect the exact command, and make the separate human approval and freeze actions:

```powershell
pnpm fl -- witness review faultline-ci-provenance-directory
```

Retain the frozen digest shown by the review screen outside `.faultline`.

## Produce the real proof bundle

After the human freeze, check durable state and use the same draft—do not retype the range or substitute a witness:

```powershell
pnpm fl -- incident status faultline-ci-provenance-directory `
  --expect-digest <retained-frozen-digest>
pnpm fl -- incident continue faultline-ci-provenance-directory `
  --expect-digest <retained-frozen-digest>
```

The expected recorded sequence is a stable `PASS -> FAIL` transition for the introduced workflow contract violation and a later stable `FAIL -> PASS` transition at the directory-creation fix. FaultLine executes the frozen predicate three times per Git state in Docker. If Docker is unavailable, noisy, or the range cannot be replayed, it must return a non-proof result; do not replace this with a fixture claim.

On success, retain the emitted bundle root outside the bundle and open the same incident page used for every real investigation:

```powershell
pnpm fl -- serve `
  --bundle .faultline\git-proof-bundles\<generated-bundle> `
  --expect-root <retained-root-digest>
```

The page proves only the frozen workflow predicate and its recorded Git transitions. The historical GitHub runs remain supporting context; they are not substituted for FaultLine’s own replay evidence.

## Demo wording before and after completion

Before a successful local Docker replay, say: “FaultLine includes a prepared self-incident runbook based on a historical CI regression.”

After retaining a verified bundle root, say: “FaultLine’s first recorded self-incident localized a workflow-contract regression from a human-frozen predicate.” Keep the scope explicit: this is not an attribution of model intent or a unique semantic cause.
