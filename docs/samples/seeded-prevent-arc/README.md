# Seeded Break → Find → Prove → Fix → Prevent (TIME-02)

Honest claim: this is a **seeded** regression arc produced by FaultLine's own
`fl demo full` path (disposable good→bad→repaired toy repo), not an unplanted
production outage. Digests and packages are real Docker/Git artifacts when the
demo completes; they are not hand-invented.

## How to regenerate

```powershell
# Requires Docker + digest-pinned Node image
pnpm fl demo full --export-only | Tee-Object -FilePath docs/samples/seeded-prevent-arc/demo-full.json
```

Retain under `.faultline/` (gitignored) the emitted proof bundle, minimization
result, and `PREVENTION_VERIFIED` package. Publish only digests + claim boundary
here.

## Arc checklist

| Phase | Command / artifact | Status |
| --- | --- | --- |
| Break | Toy `state.txt` flips good→bad in demo repo | seeded |
| Find | `investigateGitRange` PASS→FAIL transitions | real when demo runs |
| Prove | `COMMIT_PROOF` Git proof bundle + `fl verify` | real when demo runs |
| Fix | Minimization + repair patch in demo-full | real when demo runs |
| Prevent | `PREVENTION_VERIFIED` + AGENTS.md | real when demo runs |

## Related organic evidence

Maintainer organic CI freeze (not the full Prevent arc alone):
`docs/impact-validation-codex-self-dogfood.md` §B (`ci-29694948356-pnpm-test`).

## Claim boundary

- **Is:** Seeded end-to-end Prevent arc using real FaultLine machinery.
- **Is not:** An unplanted third-party production incident.
