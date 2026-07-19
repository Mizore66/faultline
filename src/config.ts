import { existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { z } from "zod";
import { relativeTrustedSystemPath, resolveTrustedSystemPath } from "./safe-directory.js";

export const FAULTLINE_CONFIG_SCHEMA_VERSION = "faultline.config.v1" as const;
export const FAULTLINE_CONFIG_FILENAME = "config.json" as const;

const DIGEST_PINNED_IMAGE = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;

function isTagFreeDigestImage(value: string): boolean {
  if (!DIGEST_PINNED_IMAGE.test(value)) return false;
  const repository = value.slice(0, value.indexOf("@"));
  return repository.lastIndexOf(":") <= repository.lastIndexOf("/");
}

export const FaultLineConfigSchema = z.object({
  schemaVersion: z.literal(FAULTLINE_CONFIG_SCHEMA_VERSION),
  /** Optional digest-pinned image used when `--image` is omitted. */
  image: z.string().refine(isTagFreeDigestImage, "image must be a tag-free digest-pinned reference").optional(),
  runtime: z.object({
    alias: z.enum(["node", "python", "go"]),
    image: z.string().refine(isTagFreeDigestImage, "runtime.image must be a tag-free digest-pinned reference")
  }).strict().optional(),
  snapshot: z.object({
    /** Default A: include tracked + untracked eligible paths. */
    trackedFilesOnly: z.boolean()
  }).strict(),
  stores: z.object({
    incidents: z.string().min(1),
    witnesses: z.string().min(1),
    recordings: z.string().min(1)
  }).strict()
}).strict();

export type FaultLineConfig = z.infer<typeof FaultLineConfigSchema>;

export function defaultFaultLineConfig(repository: string = process.cwd()): FaultLineConfig {
  const root = resolve(repository);
  return FaultLineConfigSchema.parse({
    schemaVersion: FAULTLINE_CONFIG_SCHEMA_VERSION,
    snapshot: { trackedFilesOnly: false },
    stores: {
      incidents: join(root, ".faultline", "incidents"),
      witnesses: join(root, ".faultline", "witnesses"),
      recordings: join(root, ".faultline", "recordings")
    }
  });
}

export function faultLineConfigPath(repository: string = process.cwd()): string {
  return join(resolve(repository), ".faultline", FAULTLINE_CONFIG_FILENAME);
}

export function readFaultLineConfig(repository: string = process.cwd()): FaultLineConfig | null {
  const path = faultLineConfigPath(repository);
  if (!existsSync(path)) return null;
  const stat = lstatSync(path);
  if (stat.isSymbolicLink() || !stat.isFile()) {
    throw new Error(`FaultLine config must be a regular non-symlink file: ${path}`);
  }
  return FaultLineConfigSchema.parse(JSON.parse(readFileSync(path, "utf8")));
}

export function writeFaultLineConfig(repository: string, config: FaultLineConfig): string {
  const parsed = FaultLineConfigSchema.parse(config);
  const path = faultLineConfigPath(repository);
  const directory = dirname(path);
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const safeDir = resolveTrustedSystemPath(directory);
  const nested = relativeTrustedSystemPath(safeDir, path);
  if (!nested || nested.startsWith("..") || nested.includes(":")) {
    throw new Error("FaultLine config path escaped .faultline");
  }
  writeFileSync(path, `${JSON.stringify(parsed, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
  return path;
}

/** Seed or refresh project config during `fl init`. */
export function ensureFaultLineConfig(options: {
  repository: string;
  runtimeAlias?: "node" | "python" | "go";
  image?: string;
}): { path: string; config: FaultLineConfig; status: "CREATED" | "UPDATED" | "UNCHANGED" } {
  const existing = readFaultLineConfig(options.repository);
  const base = existing ?? defaultFaultLineConfig(options.repository);
  const next = FaultLineConfigSchema.parse({
    ...base,
    ...(options.image === undefined ? {} : { image: options.image }),
    ...(options.runtimeAlias !== undefined && options.image !== undefined
      ? { runtime: { alias: options.runtimeAlias, image: options.image } }
      : options.runtimeAlias !== undefined && base.runtime !== undefined
        ? { runtime: { ...base.runtime, alias: options.runtimeAlias } }
        : {}),
    snapshot: { trackedFilesOnly: false }
  });
  const path = writeFaultLineConfig(options.repository, next);
  if (existing === null) return { path, config: next, status: "CREATED" };
  if (JSON.stringify(existing) === JSON.stringify(next)) return { path, config: next, status: "UNCHANGED" };
  return { path, config: next, status: "UPDATED" };
}

/** Persist a resolved/prepared digest-pinned image into project config. */
export function updateFaultLineConfigImage(options: {
  repository: string;
  image: string;
  runtimeAlias?: "node" | "python" | "go";
}): FaultLineConfig {
  const base = readFaultLineConfig(options.repository) ?? defaultFaultLineConfig(options.repository);
  const next = FaultLineConfigSchema.parse({
    ...base,
    image: options.image,
    ...(options.runtimeAlias === undefined
      ? {}
      : { runtime: { alias: options.runtimeAlias, image: options.image } })
  });
  writeFaultLineConfig(options.repository, next);
  return next;
}

/** CLI `--image` overrides config; otherwise use persisted digest-pinned image. */
export function resolveConfiguredImage(repository: string, cliImage: string | undefined): string | undefined {
  if (cliImage !== undefined) return cliImage;
  return readFaultLineConfig(repository)?.image;
}
