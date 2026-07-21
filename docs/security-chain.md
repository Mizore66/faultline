# Security verification chain (SEC-03 / SEC-08)

Executable stranger checklist binding **signed tag → green CI → attested artifacts → offline verify**.

## Chain

1. **Pinned submission tag (prefer signed)**  
   Checkout the current pin in README (e.g. `v0.1.8-buildweek`).  
   Prefer a GPG- or SSH-signed annotated tag:
   ```powershell
   git fetch --tags
   git verify-tag v0.1.8-buildweek
   # or: git tag -v v0.1.8-buildweek
   ```
   CI job `tag-checkout` runs `fl judge-proof --export-only` on that tag.

2. **Green Verify workflow**  
   Confirm the GitHub Actions **Verify FaultLine** run is green for the commit
   under evaluation (`test` matrix + `docker-integration` + `tag-checkout`).

3. **Attested provenance (main pushes)**  
   On `main` pushes, job `provenance` creates:
   - a live `fl demo live-git` proof bundle
   - `fl provenance create` receipt
   - GitHub artifact attestation via `actions/attest`
   - uploaded artifact `faultline-ci-provenance-<run_id>-<attempt>`

4. **Stranger offline verify**

```powershell
# After downloading the provenance artifact + proof bundle:
node scripts/verify-security-chain.mjs --checklist
node scripts/verify-security-chain.mjs `
  --tag v0.1.8-buildweek `
  --bundle <git-proof-bundle-directory> `
  --receipt <ci-receipt.json> `
  --attestation <sigstore-bundle.json> `
  --trust docs/faultline-github-attestation-trust.example.json

fl provenance verify `
  --bundle <git-proof-bundle-directory> `
  --receipt <ci-receipt.json> `
  --attestation-bundle <sigstore-bundle.json> `
  --trust docs/faultline-github-attestation-trust.example.json

fl verify <git-proof-bundle-directory> --expect-root <sha256:...>
fl judge-proof --bundle <git-proof-bundle-directory> --expect-root <sha256:...> --export-only
```

## Script helper

```powershell
node scripts/verify-security-chain.mjs --help
```

Cold-machine transcript: retain the stdout of the commands above on a machine that never held the signing key.

## Cold-reader transcript (SEC-08 / #182) — 2026-07-21

Partner cold reader ([#171 comment](https://github.com/Mizore66/faultline/issues/171#issuecomment-5028147284)): public `allowed_signers` only; **no private signing key** on that machine.

| Command | Result |
| --- | --- |
| `git verify-tag v0.1.8-buildweek` | Good ED25519 signature |
| `node scripts/verify-security-chain.mjs --tag v0.1.8-buildweek --require-signed` | `tagStatus: VERIFIED` (exit 0) |
| Same pair for historical pin `v0.1.7-buildweek` | Good ED25519 + `tagStatus: VERIFIED` (exit 0) |

**Not exercised on that pass** (still required for full SEC-08 accept on #171): CI provenance artifact download, receipt, attestation bundle, `fl provenance verify`, and sample-root `fl verify` / `fl judge-proof`.

## Honesty

- Tag pin is a Git ref; cryptographic tag *signing* may still be absent — GitHub
  artifact attestation covers the CI-produced receipt/subject.
- Trust file is an allowlist example; strangers must review issuer identity.
- Judging pin in README is `v0.1.8-buildweek` until the human cuts signed `v0.1.9-buildweek` (see release-notes draft).
