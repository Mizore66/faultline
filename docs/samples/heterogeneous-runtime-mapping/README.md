# Heterogeneous runtime-mapping sample (RIG-08)

Lockfile / toolchain changes mid-range produce **heterogeneous** environment
fingerprints. FaultLine refuses a single-image proof until every fingerprint
digest is bound to a digest-pinned image.

## Mapping file shape

```json
{
  "schemaVersion": "faultline.runtime-mapping.v1",
  "generatedAt": "2026-07-21T00:00:00.000Z",
  "source": "fl runtime mapping write",
  "mapping": {
    "sha256:<fingerprint-at-known-good>": "node@sha256:<digest-a>",
    "sha256:<fingerprint-at-known-bad>": "node@sha256:<digest-b>"
  },
  "provenanceDigest": "sha256:..."
}
```

## How to reproduce

```powershell
pnpm fl runtime mapping write `
  --fingerprint sha256:<good> --image <digest-pinned-a> `
  --fingerprint sha256:<bad> --image <digest-pinned-b> `
  --output .\runtime-mapping.json

# Docker CI exercises the certified path in:
# tests/docker-integration.test.ts → "RIG-08: heterogeneous lockfile range..."
```

## Claims

- Without a complete mapping, investigation status is `CONFIGURATION_ERROR`.
- With a complete mapping, `proof.isProof` may be true and the proof reason
  cites per-fingerprint runtime mapping; both digests appear in the result.
- This directory is a **recipe**, not a retained proof root. Live bundles are
  produced by the Docker E2E job.
