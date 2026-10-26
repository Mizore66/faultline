# FaultLine prevention proof

Status: **PREVENTION_EVIDENCE_SUMMARY** (not fully grounded Prevention verified)

This package binds an original Git proof root, a frozen witness digest, and three NATIVE_DOCKER state summaries (PASS → FAIL → PASS) with per-state run IDs.

Verify offline with `fl verify <this-directory> --expect-root <retained-root>` (or `fl prevention verify`).
It does not claim model intent or a unique semantic root cause.
