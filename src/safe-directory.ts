import { lstatSync, realpathSync } from "node:fs";
import { resolve } from "node:path";

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
  const expectedTarget = lexical === "/var"
    ? "/private/var"
    : lexical === "/tmp"
      ? "/private/tmp"
      : null;
  if (expectedTarget === null) return null;

  try {
    if (realpathSync(lexical) !== expectedTarget) return null;
    const targetStat = lstatSync(expectedTarget);
    return targetStat.isDirectory() && !targetStat.isSymbolicLink() ? expectedTarget : null;
  } catch {
    return null;
  }
}
