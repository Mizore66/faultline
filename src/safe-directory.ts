import { lstatSync, realpathSync } from "node:fs";
import { join, relative, resolve } from "node:path";

const DARWIN_SYSTEM_DIRECTORY_ALIASES = [
  ["/var", "/private/var"],
  ["/tmp", "/private/tmp"]
] as const;

/**
 * Resolve a directory segment that is safe to traverse without accepting a
 * user-controlled link. On macOS, `/var` and `/tmp` are operating-system
 * aliases for `/private/var` and `/private/tmp`; accepting only those exact,
 * verified links lets FaultLine use the standard temporary-directory paths
 * while preserving the no-symlink invariant for every other path segment.
 */
export function resolveSafeDirectorySegment(path: string): string | null {
  const stat = lstatSync(path);
  if (stat.isDirectory() && !stat.isSymbolicLink()) return path;
  if (!stat.isSymbolicLink() || process.platform !== "darwin") return null;

  const lexical = resolve(path);
  const expectedTarget = DARWIN_SYSTEM_DIRECTORY_ALIASES.find(([alias]) => lexical === alias)?.[1] ?? null;
  if (expectedTarget === null) return null;

  try {
    if (realpathSync(lexical) !== expectedTarget) return null;
    const targetStat = lstatSync(expectedTarget);
    return targetStat.isDirectory() && !targetStat.isSymbolicLink() ? expectedTarget : null;
  } catch {
    return null;
  }
}

/**
 * Normalize only verified macOS system aliases for path comparisons. This
 * deliberately does not resolve arbitrary paths or follow user-controlled
 * links; callers still perform segment-by-segment safety checks before writes.
 */
export function resolveTrustedSystemPath(path: string): string {
  const absolute = resolve(path);
  if (process.platform !== "darwin") return absolute;

  for (const [alias, target] of DARWIN_SYSTEM_DIRECTORY_ALIASES) {
    if (absolute !== alias && !absolute.startsWith(`${alias}/`)) continue;
    if (resolveSafeDirectorySegment(alias) !== target) return absolute;
    const suffix = relative(alias, absolute);
    return suffix ? join(target, suffix) : target;
  }
  return absolute;
}

/** Compare managed paths while treating verified macOS system aliases as one location. */
export function relativeTrustedSystemPath(root: string, candidate: string): string {
  return relative(resolveTrustedSystemPath(root), resolveTrustedSystemPath(candidate));
}
