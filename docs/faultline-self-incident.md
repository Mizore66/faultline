# FaultLine’s first self-incident — completed evidence and reproducible runbook

**Product Idea (lead with this):** FaultLine produced a portable, offline-verifiable evidence package for one human-frozen workflow predicate — another engineer can verify the recorded facts **without re-running repository code**. This is not a Git-bisect replacement, not model attribution, and not the `judge-demo` fixture.

On 2026-07-17, FaultLine completed a human-reviewed, Docker-isolated replay of its own historical provenance-workflow regression. The portable package verified when given its recorded root `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`. This document records exactly what that replay supports and keeps a reproducible path for a fresh incident; it does not turn the result into model attribution, a unique semantic cause, or a claim about unrecorded CI behavior.

## The observed incident

Commit `97c3290e710db2df9b9c9bd83e51fc5b33340379` added the provenance job’s `tee .faultline/live-git-demo.json` write without first creating `.faultline`. Commit `5546831c87439cb71d2b183b11b76aa4e95ab4ca` did **not** change `.github/workflows/verify.yml`; it is a later state that retained the same workflow predicate. Commit `07ee7f11bb0cc7dabe2e6e20a2b780d9428dae77` fixed the workflow contract by adding `mkdir -p .faultline` before `tee`.

The historical GitHub Actions run reported the operational symptom:

```text
tee: .faultline/live-git-demo.json: No such file or directory
```

The historical runs are [the failed run](https://github.com/Mizore66/faultline/actions/runs/29504579227) and [the succeeding fix run](https://github.com/Mizore66/faultline/actions/runs/29504804371). They are supporting context, not a substitute for FaultLine's replay evidence.

FaultLine's frozen witness establishes only the narrower workflow predicate: when that provenance `tee` step exists, the directory-creation command must precede it. It does **not** claim that `97c3290` was the first overall CI failure, a semantic root cause, or an agent-intent event.

## Recorded proof result

The completed investigation replayed every selected immutable Git state three times with native Docker isolation, disabled network, a read-only source/root filesystem, dropped capabilities, an unprivileged user, and the digest-pinned image `node@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2`.

| State | Stable result | What the replay establishes |
| --- | --- | --- |
| `e8e30649` | PASS | The frozen predicate was satisfied before the provenance job was added. |
| `97c3290e` | FAIL | First `PASS -> FAIL` transition within the selected range. |
| `5546831c` | FAIL | The same predicate remained unsatisfied. |
| `07ee7f11` | PASS | `FAIL -> PASS` transition at the directory-creation fix. |

- Human-frozen witness digest: `sha256:985c0e48258d9a219dd4dc5bb4d377fcc63a283ecb1c4447a13640cd8898d8fd`
- Recorded portable-bundle root: `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75`
- Independent verification: 21 declared files checked; the supplied root matched.
- Lifecycle binding: `UNBOUND`. No caller-supplied Codex lifecycle ledger was attached, so this package makes no turn-level Codex observation claim.

The resulting incident page is the same read-only product surface used for any real proof package. Its root must remain retained outside the bundle to detect later rewrites.

## Judge-openable proof view

**Two different roots — do not mix them:**

| Artifact | Root | How to open |
| --- | --- | --- |
| **Judge sample** (checked in under `docs/samples/self-incident-commit-proof/`) | `sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9` | `pnpm fl judge-proof` / `pnpm fl commit-proof-preview` — see [COMMIT_PROOF_SAMPLE.md](samples/COMMIT_PROOF_SAMPLE.md) |
| **This historical self-incident** (2026-07-17 provenance workflow) | `sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75` | Only if you retain that exact package locally, then `fl serve --bundle … --expect-root sha256:f6a391b3…` |

```powershell
# Historical package only (not the default judge sample)
pnpm fl verify .faultline\git-proof-bundles\<bundle-name> `
  --expect-root sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<bundle-name> `
  --expect-root sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75
```

If verify rejects the root, you do not have this historical package — re-run [Produce the real proof bundle](#produce-the-real-proof-bundle). For Build Week cold open, prefer `pnpm fl judge-proof` (sample root `f85c446d…`) and cite this self-incident as separate dogfood evidence.

## Prepare the reviewed witness

Use a clone that contains the historical commits. FaultLine's guided runtime setup shows the exact Docker mutation before it makes it; only the explicit confirmation pulls the reviewed catalog image and resolves its local immutable digest.

```powershell
pnpm fl runtime prepare node
pnpm fl runtime prepare node --yes
```

Create a review-only draft. The witness returns nonzero only when the `tee` command exists and is missing a preceding `mkdir -p .faultline` command. Store this nested-quote command in a UTF-8-without-BOM command file so PowerShell does not reinterpret it before FaultLine freezes its exact bytes:

```powershell
$WitnessDirectory = Join-Path (Resolve-Path .).Path ".faultline"
New-Item -ItemType Directory -Force -Path $WitnessDirectory | Out-Null
$WitnessFile = Join-Path $WitnessDirectory "faultline-ci-provenance-directory.command"
$Witness = @'
node -e "const fs=require('node:fs'); const y=fs.readFileSync('.github/workflows/verify.yml','utf8'); const tee=y.indexOf('tee .faultline/live-git-demo.json'); const mkdir=y.indexOf('mkdir -p .faultline'); if (tee >= 0 && (mkdir < 0 || mkdir > tee)) process.exit(1);"
'@.Trim()
[System.IO.File]::WriteAllText($WitnessFile, $Witness, [System.Text.UTF8Encoding]::new($false))

pnpm fl incident start `
  --repo . `
  --id <new-incident-id> `
  --from e8e3064 `
  --to 07ee7f1 `
  --runtime node `
  --command-file $WitnessFile
```

The command above **only records a draft**. `--command-file` reads the regular UTF-8 file once and stores the command bytes—not a live file reference—inside the immutable proposal. It does not execute the witness, contact GitHub, approve anything, or freeze anything. Start the review workbench, inspect the exact command, and make the separate human approval and freeze actions:

```powershell
pnpm fl witness review <new-incident-id>
```

Retain the frozen digest shown by the review screen outside `.faultline`.

## Produce the real proof bundle

After the human freeze, check durable state and use the same draft—do not retype the range or substitute a witness:

```powershell
pnpm fl incident status <new-incident-id> `
  --expect-digest <retained-frozen-digest>
pnpm fl incident continue <new-incident-id> `
  --expect-digest <retained-frozen-digest>
```

The expected recorded sequence is a stable `PASS -> FAIL` transition for the introduced workflow contract violation and a later stable `FAIL -> PASS` transition at the directory-creation fix. FaultLine executes the frozen predicate three times per Git state in Docker. If Docker is unavailable, noisy, or the range cannot be replayed, it must return a non-proof result; do not replace this with a fixture claim.

On success, retain the emitted bundle root outside the bundle. For the **recorded** 2026-07-17 package, open with the fixed root in [Judge-openable proof view](#judge-openable-proof-view-idea-artifact). For a **fresh** replay, use the newly printed root instead:

```powershell
pnpm fl serve `
  --bundle .faultline\git-proof-bundles\<generated-bundle> `
  --expect-root <retained-root-digest>
```

The page proves only the frozen workflow predicate and its recorded Git transitions. The historical GitHub runs remain supporting context; they are not substituted for FaultLine’s own replay evidence.

## Demo wording

Say: “FaultLine packages a portable, offline-verifiable answer: where does this human-frozen predicate first go bad? Another engineer verifies the package without re-running the repo. Our first recorded self-incident measured a stable `PASS -> FAIL` at `97c3290` and `FAIL -> PASS` at `07ee7f1`.” One complement line only: it complements Git bisect / CI logs / repro — it does not replace them. Do not lead with model intent, unique semantic root cause, `judge-demo`, or experimental turn localization.
