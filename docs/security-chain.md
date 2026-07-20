# Security verification chain (SEC-03)

Executable stranger checklist binding **pinned tag → green CI → attested artifacts → offline verify**.

## Chain

1. **Pinned submission tag**  
   Checkout `v0.1.4-buildweek` (or the current pin in README).  
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

## Honesty

- Tag pin is a Git ref; cryptographic tag *signing* may still be absent — GitHub
  artifact attestation covers the CI-produced receipt/subject.
- Trust file is an allowlist example; strangers must review issuer identity.
