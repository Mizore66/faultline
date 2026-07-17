import { lstat, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { randomUUID } from "node:crypto";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { z } from "zod";
import { sha256 } from "./canonical.js";
import type { FrozenWitness } from "./witness-lock.js";

const SAFE_OVERLAY_PATH = /^[^\\/\0]+(?:\/[^\\/\0]+)*$/;

export const MaterializedOverlaySchema = z.object({
  path: z.string().min(1),
  bytesDigest: z.string().regex(/^sha256:[a-f0-9]{64}$/),
  bytesLength: z.number().int().nonnegative()
}).strict();

export type MaterializedOverlay = z.infer<typeof MaterializedOverlaySchema>;

function sha256Digest(value: string | Buffer): string {
  return `sha256:${sha256(value)}`;
}

function safeOverlayParts(value: string): string[] {
  if (!SAFE_OVERLAY_PATH.test(value) || value.startsWith("/") || value.includes("\\")) {
    throw new Error(`Frozen overlay path is unsafe: ${value}`);
  }
  const parts = value.split("/");
  if (parts.some((part) => !part || part === "." || part === "..")) {
    throw new Error(`Frozen overlay path is unsafe: ${value}`);
  }
  return parts;
}

async function lstatIfPresent(path: string): Promise<Awaited<ReturnType<typeof lstat>> | null> {
  try {
    return await lstat(path);
  } catch (error) {
    const code = error instanceof Error && "code" in error ? (error as NodeJS.ErrnoException).code : undefined;
    if (code === "ENOENT") return null;
    throw error;
  }
}

/**
 * Resolve an overlay path that must remain inside the temporary worktree.
 * Rejects absolute paths, `..`, symlink ancestors, and symlink destinations.
 */
export async function safeOverlayTarget(worktree: string, overlayPath: string): Promise<string> {
  const parts = safeOverlayParts(overlayPath);
  const target = resolve(worktree, ...parts);
  const containment = relative(worktree, target);
  if (!containment || containment === ".." || containment.startsWith("../") || containment.startsWith("..\\") || isAbsolute(containment)) {
    throw new Error(`Frozen overlay path escaped its worktree: ${overlayPath}`);
  }

  let current = worktree;
  for (const part of parts.slice(0, -1)) {
    current = join(current, part);
    const entry = await lstatIfPresent(current);
    if (entry === null) {
      await mkdir(current, { mode: 0o755 });
      const created = await lstat(current);
      if (!created.isDirectory() || created.isSymbolicLink()) {
        throw new Error(`Could not safely create overlay directory: ${overlayPath}`);
      }
      continue;
    }
    if (!entry.isDirectory() || entry.isSymbolicLink()) {
      throw new Error(`Frozen overlay parent is not a safe directory: ${overlayPath}`);
    }
  }
  const existing = await lstatIfPresent(target);
  if (existing?.isSymbolicLink()) {
    throw new Error(`Frozen overlay target is a symbolic link: ${overlayPath}`);
  }
  return target;
}

/** Materialize, then re-read, every approved overlay byte-for-byte. */
export async function materializeFrozenOverlays(
  worktree: string,
  frozenWitness: FrozenWitness
): Promise<MaterializedOverlay[]> {
  const facts: MaterializedOverlay[] = [];
  for (const overlay of frozenWitness.proposal.witness.overlays) {
    const bytes = Buffer.from(overlay.bytesBase64, "base64");
    const expectedDigest = sha256Digest(bytes);
    if (expectedDigest !== overlay.bytesDigest) {
      throw new Error(`Frozen overlay digest does not match its bytes: ${overlay.path}`);
    }
    const target = await safeOverlayTarget(worktree, overlay.path);
    const staged = join(dirname(target), `.faultline-overlay-${randomUUID()}`);
    try {
      await writeFile(staged, bytes, { encoding: undefined, flag: "wx", mode: 0o644 });
      await rename(staged, target);
    } finally {
      await rm(staged, { force: true });
    }
    const reread = await readFile(target);
    if (!reread.equals(bytes) || sha256Digest(reread) !== overlay.bytesDigest) {
      throw new Error(`FaultLine could not verify exact materialized overlay bytes: ${overlay.path}`);
    }
    facts.push({ path: overlay.path, bytesDigest: overlay.bytesDigest, bytesLength: bytes.length });
  }
  return facts;
}
