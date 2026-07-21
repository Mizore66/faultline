/**
 * Submission freeze path classifier (C-1).
 * When SUBMISSION_FROZEN exists at repo root, pushes that touch blocked paths fail
 * unless workflow_dispatch unfreeze_override is true.
 */

/** Paths that freeze blocks on main. */
export const SUBMISSION_FROZEN_BLOCKED_PREFIXES = Object.freeze([
  "src/",
  "package.json",
  "pnpm-lock.yaml"
]);

export function normalizeRepoPath(path) {
  return String(path).replace(/\\/g, "/").replace(/^\.\//, "");
}

/** Returns true when a changed path is blocked under submission freeze. */
export function isSubmissionFrozenBlockedPath(path) {
  const normalized = normalizeRepoPath(path);
  if (normalized === "package.json" || normalized === "pnpm-lock.yaml") return true;
  return normalized.startsWith("src/");
}

/**
 * Classify a list of changed paths.
 * @returns {{ blocked: string[], allowed: string[] }}
 */
export function classifySubmissionFreezePaths(paths) {
  const blocked = [];
  const allowed = [];
  for (const path of paths) {
    if (isSubmissionFrozenBlockedPath(path)) blocked.push(normalizeRepoPath(path));
    else allowed.push(normalizeRepoPath(path));
  }
  return { blocked, allowed };
}

/**
 * Whether CI should fail on blocked path changes.
 */
export function shouldEnforceSubmissionFreeze(options) {
  const { freezeFileExists, eventName, unfreezeOverride, ref } = options;
  if (!freezeFileExists) return false;
  if (eventName === "workflow_dispatch" && unfreezeOverride === true) return false;
  if (eventName === "push" && ref === "refs/heads/main") return true;
  if (eventName === "pull_request") return true;
  return false;
}

const classifyIndex = process.argv.indexOf("--classify");
if (classifyIndex !== -1) {
  const paths = process.argv.slice(classifyIndex + 1);
  const result = classifySubmissionFreezePaths(paths);
  process.stdout.write(`${JSON.stringify(result)}\n`);
  process.exit(result.blocked.length > 0 ? 2 : 0);
}
