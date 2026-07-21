#!/usr/bin/env bash
# Human cut script for the judging pin (SSH-signed annotated tag).
# Agent environments sign with a different key than Mizore66 — do NOT use an
# agent-signed tag as the judging artifact. Run this on a machine with your
# SEC-08 signing key configured (gpg.format=ssh + user.signingkey).
set -euo pipefail

PIN=v0.1.10-buildweek
PREV=v0.1.9-buildweek

cd "$(dirname "$0")/.."

git fetch origin --tags
git checkout main
git pull origin main

if git rev-parse "$PIN" >/dev/null 2>&1; then
  echo "Refusing to move existing $PIN -> $(git rev-parse "$PIN^{commit}")" >&2
  exit 1
fi

# Prefer the pin-surfaces commit message marker if present on HEAD history.
PIN_COMMIT="${1:-ce99a793af0b020bf1b9cf21114f8d7897b37207}"
if [[ -z "$PIN_COMMIT" ]]; then
  PIN_COMMIT=$(git log origin/main --oneline --grep='Pin submission surfaces to v0.1.10-buildweek' -1 --format=%H || true)
fi
if [[ -z "$PIN_COMMIT" ]]; then
  echo "Pass the pin-surfaces commit SHA as \$1" >&2
  exit 1
fi

echo "Cutting signed $PIN at $PIN_COMMIT"
echo "Previous pin $PREV remains at $(git rev-parse "$PREV^{commit}") (must not move)"

git tag -s "$PIN" -m "$(cat <<EOF
This is the only release intended for judging.

FaultLine v0.1.10-buildweek incorporates EXT-01 (FL-SEC-001 Critical fixed,
FL-SEC-002 High mitigated, FL-SEC-003 Medium fixed) so judges receive the
hardened capture path and exit-consistent witness results.

Historical / not for judging:
- v0.1.9-buildweek — previous pin (retain; do not move; lacks EXT-01 fixes)
- v0.1.8-buildweek and earlier buildweek tags — historical
- v0.1.4 — RETRACTED (do not cite)
EOF
)" "$PIN_COMMIT"

git verify-tag "$PIN"
git push origin "$PIN"

echo "Next: fill docs/devpost-paste-ready.md literal SHA to $PIN_COMMIT"
echo "Next: gh release create $PIN --title \"$PIN — judging pin\" --notes-file docs/release-notes-v0.1.10-buildweek.md"
echo "Next: FAULTLINE_REHEARSE_TAG=$PIN node scripts/rehearse-teleprompter.mjs"
echo "Next: re-assert SUBMISSION_FROZEN on main if temporarily lifted for the pin PR"
