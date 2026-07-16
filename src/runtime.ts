import { spawn } from "node:child_process";

/**
 * A deliberately small catalog for the first-run path. These are tags, not
 * proof inputs: resolving one always produces a separate immutable RepoDigest
 * before it can be handed to the sandbox.
 */
export const CURATED_RUNTIME_CATALOG = Object.freeze({
  node: Object.freeze({
    alias: "node",
    displayName: "Node.js 22 (Alpine)",
    tag: "node:22-alpine"
  }),
  python: Object.freeze({
    alias: "python",
    displayName: "Python 3.13 (Alpine)",
    tag: "python:3.13-alpine"
  }),
  go: Object.freeze({
    alias: "go",
    displayName: "Go 1.24 (Alpine)",
    tag: "golang:1.24-alpine"
  })
});

export type CuratedRuntimeAlias = keyof typeof CURATED_RUNTIME_CATALOG;
export type CuratedRuntime = (typeof CURATED_RUNTIME_CATALOG)[CuratedRuntimeAlias];

/** Docker's Go template returns a JSON array of locally-known RepoDigests. */
export const DOCKER_REPO_DIGESTS_FORMAT = "{{json .RepoDigests}}";
export const DOCKER_IMAGE_INSPECT_TIMEOUT_MS = 5_000;
export const DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES = 64 * 1024;

const REPO_DIGEST = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;

export type DockerImageInspectCommand = {
  readonly executable: "docker";
  readonly arguments: readonly string[];
  readonly timeoutMs: number;
};

export type DockerImageInspectResult = {
  readonly exitCode: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly timedOut: boolean;
  readonly outputLimitExceeded: boolean;
};

/**
 * The resolver accepts an injected argv runner so hosts and tests never need
 * a shell. Production callers can use createNodeDockerImageInspectRunner().
 */
export type DockerImageInspectRunner = {
  run(command: DockerImageInspectCommand): Promise<DockerImageInspectResult>;
};

export type CuratedRuntimeResolutionErrorCode =
  | "UNKNOWN_RUNTIME"
  | "DOCKER_INSPECT_FAILED"
  | "DOCKER_INSPECT_TIMED_OUT"
  | "DOCKER_INSPECT_OUTPUT_LIMIT"
  | "UNRESOLVED_REPO_DIGEST"
  | "UNEXPECTED_REPOSITORY";

export class CuratedRuntimeResolutionError extends Error {
  readonly code: CuratedRuntimeResolutionErrorCode;

  constructor(code: CuratedRuntimeResolutionErrorCode, message: string) {
    super(message);
    this.name = "CuratedRuntimeResolutionError";
    this.code = code;
  }
}

export type ResolvedCuratedRuntime = {
  /** The curated catalog choice, never an arbitrary image reference. */
  readonly runtime: CuratedRuntime;
  /** The alias or fixed tag that the caller selected. */
  readonly requested: string;
  /** The local immutable Docker RepoDigest returned by image inspect. */
  readonly image: string;
};

function catalogEntries(): readonly CuratedRuntime[] {
  return Object.values(CURATED_RUNTIME_CATALOG);
}

/**
 * Accept a short curated alias or its exact, catalog-owned tag. Arbitrary
 * tags, latest, and already-pinned images are intentionally not first-run
 * selections: the caller must choose a catalog entry or supply an explicit
 * proof-grade image through the advanced flow.
 */
export function selectCuratedRuntime(requested: string): CuratedRuntime {
  const entry = catalogEntries().find((candidate) => candidate.alias === requested || candidate.tag === requested);
  if (entry === undefined) {
    throw new CuratedRuntimeResolutionError(
      "UNKNOWN_RUNTIME",
      `Unknown curated runtime ${JSON.stringify(requested)}. Choose node, python, or go.`
    );
  }
  return entry;
}

/** Build the fixed, local-only Docker inspection command without a shell. */
export function createDockerImageInspectCommand(runtime: CuratedRuntime): DockerImageInspectCommand {
  return Object.freeze({
    executable: "docker",
    arguments: Object.freeze([
      "image",
      "inspect",
      "--format",
      DOCKER_REPO_DIGESTS_FORMAT,
      runtime.tag
    ]),
    timeoutMs: DOCKER_IMAGE_INSPECT_TIMEOUT_MS
  });
}

function repositoryFromTaggedReference(reference: string): string {
  const slash = reference.lastIndexOf("/");
  const colon = reference.lastIndexOf(":");
  return colon > slash ? reference.slice(0, colon) : reference;
}

function repositoryFromRepoDigest(value: string): string {
  const at = value.indexOf("@");
  return at < 0 ? "" : value.slice(0, at);
}

/** A RepoDigest names a repository plus a digest; it must not retain a tag. */
function isDigestPinnedRepoDigest(value: string): boolean {
  if (!REPO_DIGEST.test(value)) return false;
  const repository = repositoryFromRepoDigest(value);
  const lastSlash = repository.lastIndexOf("/");
  const lastColon = repository.lastIndexOf(":");
  // A colon after the final slash is a mutable tag. Registry ports appear
  // before that slash and remain valid (for example registry:5000/node).
  return lastColon <= lastSlash;
}

/** Normalize the Docker Hub spellings that Docker may use in RepoDigests. */
function normalizeRepository(repository: string): string {
  const withoutHubPrefix = repository
    .replace(/^index\.docker\.io\//, "")
    .replace(/^docker\.io\//, "");
  if (!withoutHubPrefix.includes("/")) return `library/${withoutHubPrefix}`;
  return withoutHubPrefix;
}

function isExpectedRepository(runtime: CuratedRuntime, repoDigest: string): boolean {
  return normalizeRepository(repositoryFromTaggedReference(runtime.tag))
    === normalizeRepository(repositoryFromRepoDigest(repoDigest));
}

function parseRepoDigests(stdout: string): readonly string[] {
  const trimmed = stdout.trim();
  if (!trimmed) return [];
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (!Array.isArray(parsed) || !parsed.every((value): value is string => typeof value === "string")) return [];
    return parsed;
  } catch {
    return [];
  }
}

function inspectFailureDetail(result: DockerImageInspectResult): string {
  const detail = result.stderr.replace(/[\r\n]+/g, " ").trim();
  return detail ? `: ${detail.slice(0, 320)}` : "";
}

/**
 * Resolve a catalog alias/tag only from the already-local Docker image store.
 * It never pulls an image, accesses the network, or mutates Docker state.
 */
export async function resolveCuratedRuntime(
  requested: string,
  runner?: DockerImageInspectRunner
): Promise<ResolvedCuratedRuntime> {
  const runtime = selectCuratedRuntime(requested);
  const command = createDockerImageInspectCommand(runtime);
  const effectiveRunner = runner ?? createNodeDockerImageInspectRunner();

  let result: DockerImageInspectResult;
  try {
    result = await effectiveRunner.run(command);
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : "";
    throw new CuratedRuntimeResolutionError(
      "DOCKER_INSPECT_FAILED",
      `FaultLine could not inspect the local ${runtime.tag} image${detail}`
    );
  }

  if (result.timedOut) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_INSPECT_TIMED_OUT",
      `Docker image inspection timed out for ${runtime.tag}.`
    );
  }
  if (result.outputLimitExceeded) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_INSPECT_OUTPUT_LIMIT",
      `Docker image inspection exceeded FaultLine's ${DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES}-byte output limit for ${runtime.tag}.`
    );
  }
  if (result.exitCode !== 0) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_INSPECT_FAILED",
      `Docker could not inspect the local ${runtime.tag} image${inspectFailureDetail(result)}`
    );
  }

  const repoDigests = parseRepoDigests(result.stdout);
  const digestPinned = repoDigests.filter(isDigestPinnedRepoDigest);
  if (digestPinned.length === 0) {
    throw new CuratedRuntimeResolutionError(
      "UNRESOLVED_REPO_DIGEST",
      `Docker did not return a digest-pinned RepoDigest for ${runtime.tag}. Pull it explicitly, then try again.`
    );
  }

  const expected = digestPinned.find((candidate) => isExpectedRepository(runtime, candidate));
  if (expected === undefined) {
    throw new CuratedRuntimeResolutionError(
      "UNEXPECTED_REPOSITORY",
      `Docker returned a RepoDigest for a repository other than curated ${runtime.tag}.`
    );
  }

  return Object.freeze({ runtime, requested, image: expected });
}

/**
 * Local production runner for the fixed inspect argv. No shell is involved;
 * the resolver itself still performs only `docker image inspect`, never pull.
 */
export function createNodeDockerImageInspectRunner(): DockerImageInspectRunner {
  return {
    run(command: DockerImageInspectCommand): Promise<DockerImageInspectResult> {
      return new Promise((resolveResult) => {
        let settled = false;
        let timedOut = false;
        let outputLimitExceeded = false;
        let stdout = "";
        let stderr = "";
        let timeout: NodeJS.Timeout | undefined;

        const finish = (exitCode: number | null): void => {
          if (settled) return;
          settled = true;
          if (timeout !== undefined) clearTimeout(timeout);
          resolveResult({ exitCode, stdout, stderr, timedOut, outputLimitExceeded });
        };

        let child;
        try {
          child = spawn(command.executable, [...command.arguments], {
            shell: false,
            stdio: ["ignore", "pipe", "pipe"],
            windowsHide: true
          });
        } catch (error) {
          stderr = error instanceof Error ? error.message : "Unable to start Docker image inspection.";
          finish(null);
          return;
        }

        const appendOutput = (stream: "stdout" | "stderr", chunk: Buffer): void => {
          const bytes = Buffer.from(chunk);
          const used = Buffer.byteLength(stdout, "utf8") + Buffer.byteLength(stderr, "utf8");
          const remaining = DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES - used;
          if (remaining <= 0) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
            return;
          }
          const retained = bytes.subarray(0, remaining).toString("utf8");
          if (stream === "stdout") stdout += retained;
          else stderr += retained;
          if (bytes.length > remaining) {
            outputLimitExceeded = true;
            child.kill("SIGKILL");
          }
        };
        child.stdout?.on("data", (chunk: Buffer) => appendOutput("stdout", chunk));
        child.stderr?.on("data", (chunk: Buffer) => appendOutput("stderr", chunk));
        child.once("error", (error) => {
          stderr = `${stderr}${stderr ? "\n" : ""}${error.message}`;
          finish(null);
        });
        child.once("close", (exitCode) => finish(exitCode));
        timeout = setTimeout(() => {
          timedOut = true;
          child.kill("SIGKILL");
        }, command.timeoutMs);
      });
    }
  };
}
