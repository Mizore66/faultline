import { lstatSync, readFileSync } from "node:fs";
import { isAbsolute, join, relative, resolve } from "node:path";

export type ModelOverlayInput = { path: string; bytesBase64: string };

function safeRelativeParts(root: string, path: string): string[] {
  const target = resolve(root, path);
  const nested = relative(root, target);
  if (!nested || nested.startsWith("..") || isAbsolute(nested) || path.includes("\\")) {
    throw new Error(`Model proposed an unsafe overlay path: ${path}`);
  }
  const parts = nested.split(/[\\/]+/).filter(Boolean);
  if (parts.length === 0 || parts.some((part) => part === "." || part === "..")) {
    throw new Error(`Model proposed an unsafe overlay path: ${path}`);
  }
  return parts;
}

/**
 * Read model-selected overlay bytes only through real directories beneath an
 * explicit staging root. Checking each parent with lstat prevents a regular
 * leaf inside a symlinked directory from escaping that root.
 */
export function readModelOverlayInput(overlayRoot: string, paths: readonly string[]): ModelOverlayInput[] {
  const root = resolve(overlayRoot);
  const rootStat = lstatSync(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) {
    throw new Error(`Overlay root must be a real directory: ${root}`);
  }

  return paths.map((path) => {
    const parts = safeRelativeParts(root, path);
    let current = root;
    for (const part of parts.slice(0, -1)) {
      current = join(current, part);
      const stat = lstatSync(current);
      if (!stat.isDirectory() || stat.isSymbolicLink()) {
        throw new Error(`Model overlay cannot traverse a symbolic link or non-directory: ${path}`);
      }
    }
    const target = join(root, ...parts);
    const stat = lstatSync(target);
    if (!stat.isFile() || stat.isSymbolicLink()) {
      throw new Error(`Model overlay must be a regular non-symlink file: ${path}`);
    }
    return { path: parts.join("/"), bytesBase64: readFileSync(target).toString("base64") };
  });
}
