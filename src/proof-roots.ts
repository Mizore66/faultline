/**
 * Well-known COMMIT_PROOF roots used by judge docs and UI identity banners.
 * Keep these as the single source of truth for sample vs historical packages.
 */

/** Recorded 2026-07-17 self-incident portable-bundle root (see docs/faultline-self-incident.md). */
export const RECORDED_SELF_INCIDENT_ROOT =
  "sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75";

/**
 * Default root for the checked-in judge sample under docs/samples/self-incident-commit-proof.
 * This is the `fl demo live-git` package used for Design/judge open — not the historical self-incident root.
 */
export const JUDGE_COMMIT_PROOF_SAMPLE_ROOT =
  "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9";

/** One-line identity copy for the COMMIT_PROOF HTML page. */
export function commitProofPackageIdentityNotice(rootDigest: string): string | undefined {
  if (rootDigest === JUDGE_COMMIT_PROOF_SAMPLE_ROOT) {
    return "Sample package (live-git demo); historical self-incident is a separate package.";
  }
  if (rootDigest === RECORDED_SELF_INCIDENT_ROOT) {
    return "Historical self-incident package; the disposable judge sample root is a separate package.";
  }
  return undefined;
}
