import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { loadVerifiedGitProofView, renderGitProofIncidentPage } from "./git-proof-view.js";

/** Recorded 2026-07-17 self-incident portable-bundle root (see docs/faultline-self-incident.md). */
export const RECORDED_SELF_INCIDENT_ROOT =
  "sha256:f6a391b3407731d766bd19510c4e4172ad44f28771f1d034030fc56513625b75";

/**
 * Default root for the checked-in judge sample under docs/samples/self-incident-commit-proof.
 * This is the `fl demo live-git` package used for Design/judge open — not the historical self-incident root.
 */
export const JUDGE_COMMIT_PROOF_SAMPLE_ROOT =
  "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9";

const sampleRelativeDirectory = join("docs", "samples", "self-incident-commit-proof");

export function defaultSelfIncidentSampleDirectory(workspace = process.cwd()): string {
  return resolve(workspace, sampleRelativeDirectory);
}

export function defaultCommitProofPreviewPath(workspace = process.cwd()): string {
  return resolve(workspace, "docs", "self-incident-proof-preview.html");
}

export function assertSelfIncidentSamplePresent(directory: string): void {
  const root = resolve(directory);
  const manifest = join(root, "manifest.json");
  if (!existsSync(manifest)) {
    throw new Error(
      [
        `Missing COMMIT_PROOF sample at ${root}.`,
        "Copy a verified Git proof bundle into that directory (must include manifest.json; no extra files like README.md),",
        `then open with: pnpm fl judge-proof --expect-root ${JUDGE_COMMIT_PROOF_SAMPLE_ROOT}`,
        "Or regenerate with Docker: pnpm fl demo live-git --export-only and copy the printed bundle path.",
        "See docs/samples/COMMIT_PROOF_SAMPLE.md. Historical self-incident root remains",
        RECORDED_SELF_INCIDENT_ROOT
      ].join(" ")
    );
  }
}

export function loadSelfIncidentProofView(options: {
  readonly directory?: string;
  readonly expectedRoot?: string;
  readonly workspace?: string;
}) {
  const directory = resolve(options.directory ?? defaultSelfIncidentSampleDirectory(options.workspace));
  assertSelfIncidentSamplePresent(directory);
  const expectedRoot = options.expectedRoot ?? JUDGE_COMMIT_PROOF_SAMPLE_ROOT;
  return loadVerifiedGitProofView(directory, expectedRoot);
}

/**
 * Write a static HTML snapshot of a verified COMMIT_PROOF page for zero-install judges.
 * Does not execute repository code; the page is already verified before render.
 */
export function writeCommitProofPreview(options: {
  readonly directory?: string;
  readonly expectedRoot?: string;
  readonly outputFile?: string;
  readonly workspace?: string;
}): { path: string; bytes: number; rootDigest: string } {
  const view = loadSelfIncidentProofView(options);
  const html = renderGitProofIncidentPage(view).replace(
    "</title>",
    "</title>\n<meta name=\"faultline-preview\" content=\"static-commit-proof-snapshot\" />"
  );
  // Insert an honesty banner after chrome notice: rewrite the product notice to name the snapshot.
  const page = html.replace(
    "Read-only verified bundle: FaultLine checked its complete declared file set before rendering. This page does not execute repository code or rerun the witness.",
    "STATIC SNAPSHOT of a recorded COMMIT_PROOF package. Opened from disk HTML — not a live Docker run. Verify the retained root before treating this as authoritative evidence."
  );
  const output = resolve(options.outputFile ?? defaultCommitProofPreviewPath(options.workspace));
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, page, { encoding: "utf8", mode: 0o644 });
  return { path: output, bytes: Buffer.byteLength(page, "utf8"), rootDigest: view.rootDigest };
}
