# Security verification chain (SEC-03 / SEC-08)

Executable stranger checklist binding **signed tag → green CI → attested artifacts → offline verify**.

## Chain

1. **Pinned submission tag (prefer signed)**  
   Checkout the current pin in README (e.g. `v0.1.10-buildweek`).  
   Prefer a GPG- or SSH-signed annotated tag:
   ```zsh
   git fetch --tags
   git verify-tag v0.1.10-buildweek
   # or: git tag -v v0.1.10-buildweek
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

```zsh
# After downloading the provenance artifact + proof bundle:
node scripts/verify-security-chain.mjs --checklist
node scripts/verify-security-chain.mjs \
  --tag v0.1.10-buildweek \
  --bundle <git-proof-bundle-directory> \
  --receipt <ci-receipt.json> \
  --attestation <sigstore-bundle.json> \
  --trust docs/faultline-github-attestation-trust.example.json

pnpm fl provenance verify \
  --bundle <git-proof-bundle-directory> \
  --receipt <ci-receipt.json> \
  --attestation-bundle <sigstore-bundle.json> \
  --trust <reviewed-trust.json>

pnpm fl verify <git-proof-bundle-directory> --expect-root <sha256:...>
pnpm fl judge-proof --bundle <git-proof-bundle-directory> --expect-root <sha256:...> --export-only
```

## Script helper

```zsh
node scripts/verify-security-chain.mjs --help
```

Cold-machine transcript: retain the stdout of the commands above on a machine that never held the signing key.

## Cold-reader transcript — tag only (SEC-08 / #182) — 2026-07-21

Partner cold reader ([#171 comment](https://github.com/Mizore66/faultline/issues/171#issuecomment-5028147284)): public `allowed_signers` only; **no private signing key** on that machine.

| Command | Result |
| --- | --- |
| `git verify-tag v0.1.8-buildweek` | Good ED25519 signature |
| `node scripts/verify-security-chain.mjs --tag v0.1.8-buildweek --require-signed` | `tagStatus: VERIFIED` (exit 0) |
| Same pair for historical pin `v0.1.7-buildweek` | Good ED25519 + `tagStatus: VERIFIED` (exit 0) |

Tag-only; full chain recorded below.

## Cold-reader transcript — full chain (SEC-08 / #171) — 2026-07-21

macOS arm64 cold reader (zsh 5.9); public `allowed_signers` only; fingerprint `SHA256:xVMN/Bxjtc90v755kgKaE870bV3185x7fC5lqAel3fo` **not** present as a private key on that machine.

| Step | Result |
| --- | --- |
| Environment | Darwin 25.5.0 arm64; git 2.50.1; node v22.23.1; gh 2.93.0; zsh 5.9 |
| `git verify-tag v0.1.8-buildweek` | Good ED25519 for `anasqumhiyeh@gmail.com` (exit 0) |
| `verify-security-chain.mjs --tag v0.1.8-buildweek --require-signed` | `tagStatus: VERIFIED` (exit 0) |
| CI run `29776398775` | `push` / `main` / `4d8376ab…` / `success` / Verify FaultLine |
| `fl provenance verify` (bundle + receipt + attestation + trust) | `valid: true`, `assurance: GITHUB_ARTIFACT_ATTESTATION_VERIFIED`, `errors: []` (exit 0) |
| Receipt digest | `sha256:f1ee0b1a0582d168679dc8720bd4dbc55777943d0ec91acae19866bc59f01807` |
| Proof root | `sha256:64794369d7d926fa8f44d70d50ec296fad7665b3b7548d24bb7c3719413e37cd` |
| `fl verify <bundle> --expect-root sha256:64794369…` | Integrity VALID; External root MATCH (exit 0) |
| `fl judge-proof --bundle … --expect-root sha256:64794369… --export-only` | COMMIT_PROOF verified; External root MATCH (exit 0) |
| Optional sample `docs/samples/self-incident-commit-proof` root `sha256:f85c446d…` | Integrity VALID; External root MATCH (exit 0) |

## Honesty

- Tag pin is a Git ref; cryptographic tag *signing* is present for `v0.1.10-buildweek` (and historical `v0.1.9-buildweek` / `v0.1.8-buildweek` / `v0.1.7-buildweek`, SSH ED25519). GitHub artifact attestation covers the CI-produced receipt/subject separately.
- Trust file is an allowlist example; strangers must review issuer identity.
- Judging pin in README is signed `v0.1.10-buildweek` (includes EXT-01). `v0.1.9-buildweek` is retained historical and must not move. Cold-reader full-chain evidence above was recorded against signed `v0.1.8-buildweek` before this pin advanced.
