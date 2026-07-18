# COMMIT_PROOF sample (judge Idea path)

The directory [`self-incident-commit-proof/`](self-incident-commit-proof/) holds a **verified Git proof bundle** (must contain `manifest.json` and **no** extra files such as a README — undeclared files fail verify).

## Current sample

Installed from `pnpm fl demo live-git --export-only`:

`sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9`

That is a real `COMMIT_PROOF` package for Design/judge open. It is **not** the historical 2026-07-17 self-incident root (`sha256:f6a391b3…` in [faultline-self-incident.md](../faultline-self-incident.md)).

## Open it

```powershell
pnpm fl judge-proof
pnpm fl commit-proof-preview
```

Do **not** pass `--expect-root sha256:f6a391…` unless you have installed that exact historical package.

## Replace / regenerate

```powershell
pnpm fl demo live-git --export-only
# copy the printed .faultline\git-proof-bundles\<name>\* into self-incident-commit-proof\
# (overwrite contents; do not leave README.md inside that folder)
pnpm fl verify .\docs\samples\self-incident-commit-proof --expect-root <printed-root>
```
