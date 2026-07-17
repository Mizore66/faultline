import { spawn } from "node:child_process";
import { lstatSync, readFileSync, readdirSync, realpathSync, statSync } from "node:fs";
import { isAbsolute, parse, relative, resolve } from "node:path";
import { digestJson, sha256 } from "./canonical.js";

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
/** Image pulls are an explicit first-run setup action, not proof execution. */
export const DOCKER_IMAGE_PULL_TIMEOUT_MS = 5 * 60_000;
export const DOCKER_IMAGE_PULL_MAX_OUTPUT_BYTES = 64 * 1024;
/**
 * A dependency image build can legitimately take longer than a pull, but it
 * remains a bounded, explicitly requested setup operation.
 */
export const DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS = 10 * 60_000;
export const DOCKER_PROJECT_IMAGE_BUILD_MAX_OUTPUT_BYTES = 128 * 1024;
/**
 * A reviewed project context must remain small enough to fingerprint before a
 * build. This is deliberately a product safety limit, not a Docker limit.
 */
export const PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_FILES = 10_000;
export const PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_BYTES = 256 * 1024 * 1024;

/** Required acknowledgement before FaultLine asks Docker to execute a project Dockerfile. */
export const PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION = "BUILD_PROJECT_RUNTIME_IMAGE" as const;

const REPO_DIGEST = /^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/;
const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;

export type DockerImageInspectCommand = {
  readonly executable: "docker";
  readonly arguments: readonly string[];
  readonly timeoutMs: number;
};

/** A fixed, catalog-owned Docker pull command. Never constructed from arbitrary input. */
export type DockerImagePullCommand = {
  readonly executable: "docker";
  readonly arguments: readonly string[];
  readonly timeoutMs: number;
};

/**
 * A project-scoped setup command. It deliberately has no build args, cache
 * imports, or secret mounts: those can change a build's inputs invisibly and
 * must be handled by a future, separately audited workflow.
 */
export type DockerProjectImageBuildCommand = {
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

export type DockerImagePullResult = DockerImageInspectResult;
export type DockerProjectImageBuildResult = DockerImageInspectResult;

/**
 * The resolver accepts an injected argv runner so hosts and tests never need
 * a shell. Production callers can use createNodeDockerImageInspectRunner().
 */
export type DockerImageInspectRunner = {
  run(command: DockerImageInspectCommand): Promise<DockerImageInspectResult>;
};

export type DockerImagePullRunner = {
  run(command: DockerImagePullCommand): Promise<DockerImagePullResult>;
};

/** Injectable because a project image build is setup, not proof replay. */
export type DockerProjectImageBuildRunner = {
  run(command: DockerProjectImageBuildCommand): Promise<DockerProjectImageBuildResult>;
};

export type CuratedRuntimeResolutionErrorCode =
  | "UNKNOWN_RUNTIME"
  | "DOCKER_PULL_FAILED"
  | "DOCKER_PULL_TIMED_OUT"
  | "DOCKER_PULL_OUTPUT_LIMIT"
  | "DOCKER_INSPECT_FAILED"
  | "DOCKER_INSPECT_TIMED_OUT"
  | "DOCKER_INSPECT_OUTPUT_LIMIT"
  | "UNRESOLVED_REPO_DIGEST"
  | "UNEXPECTED_REPOSITORY";

export type ProjectRuntimeImagePreparationErrorCode =
  | "PROJECT_IMAGE_CONFIRMATION_REQUIRED"
  | "PROJECT_IMAGE_PLAN_DIGEST_REQUIRED"
  | "PROJECT_IMAGE_PLAN_MISMATCH"
  | "PROJECT_IMAGE_INVALID_INPUT"
  | "DOCKER_BUILD_FAILED"
  | "DOCKER_BUILD_TIMED_OUT"
  | "DOCKER_BUILD_OUTPUT_LIMIT"
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

/**
 * Kept separate from curated-runtime errors so callers cannot mistake an
 * arbitrary project Dockerfile build for a reviewed catalog image.
 */
export class ProjectRuntimeImagePreparationError extends Error {
  readonly code: ProjectRuntimeImagePreparationErrorCode;

  constructor(code: ProjectRuntimeImagePreparationErrorCode, message: string) {
    super(message);
    this.name = "ProjectRuntimeImagePreparationError";
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

/** A resolved runtime whose catalog tag was deliberately pulled in this invocation. */
export type PreparedCuratedRuntime = ResolvedCuratedRuntime & {
  readonly pull: {
    readonly tag: string;
    /** Setup succeeds only after Docker reports an immutable local RepoDigest. */
    readonly resolvedRepoDigest: string;
  };
};

export type PrepareCuratedRuntimeOptions = {
  /** Injected only for hosts/tests; production uses Docker's fixed argv runner. */
  readonly pullRunner?: DockerImagePullRunner;
  /** Reuses the read-only resolver after the explicit pull succeeds. */
  readonly inspectRunner?: DockerImageInspectRunner;
};

export type DockerBuildNetwork = "none" | "default";

/**
 * Input for a local project dependency-image build. `dockerfile` may be
 * relative to `contextDirectory`, but is resolved before Docker is invoked.
 * The Dockerfile must remain inside the declared context, so the reviewable
 * context boundary and Dockerfile boundary are the same.
 */
export type ProjectRuntimeImageBuildRequest = {
  readonly contextDirectory: string;
  readonly dockerfile: string;
  /** A mutable local tag used only during setup; proof replay receives the resolved RepoDigest. */
  readonly imageTag: string;
  /** Explicitly choose whether Dockerfile RUN steps receive Docker's default network. */
  readonly network: DockerBuildNetwork;
};

export type ProjectRuntimeImageBuildPlan = {
  readonly kind: "PROJECT_RUNTIME_IMAGE_BUILD";
  readonly setupOnly: true;
  readonly contextDirectory: string;
  readonly dockerfile: string;
  readonly imageTag: string;
  readonly network: DockerBuildNetwork;
  readonly command: DockerProjectImageBuildCommand;
  /**
   * A deterministic fingerprint of the reviewed Dockerfile and every regular
   * file in the build context. `planDigest` is the value callers must echo to
   * prepareProjectRuntimeImage() before Docker is allowed to run.
   */
  readonly review: Readonly<{
    readonly planDigest: string;
    readonly contextDigest: string;
    readonly dockerfileDigest: string;
    readonly fileCount: number;
    readonly totalBytes: number;
  }>;
  /** Structured facts suitable for a human confirmation screen. */
  readonly effects: Readonly<{
    readonly mutatesLocalDockerImageStore: true;
    readonly dockerfileInstructionsExecute: true;
    readonly buildNetwork: DockerBuildNetwork;
    /** Whether Dockerfile RUN instructions receive Docker's default network. */
    readonly dockerfileRunInstructionsMayAccessNetwork: boolean;
    /**
     * Docker's build network flag applies to build-step instructions. FaultLine
     * does not independently certify daemon/base-image network isolation.
     */
    readonly dockerDaemonNetworkBehaviorNotIndependentlyCertified: true;
    /** Docker as a whole may still communicate (for example base-image resolution). */
    readonly mayAccessNetwork: boolean;
    readonly doesNotExecuteProof: true;
    readonly requiresDigestPinnedResultForProof: true;
  }>;
  readonly description: string;
};

export type PrepareProjectRuntimeImageRequest = ProjectRuntimeImageBuildRequest & {
  /** Must equal PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION; no implicit project build exists. */
  readonly confirmation: typeof PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION;
  /** Exact plan.review.planDigest emitted by a separately reviewed build plan. */
  readonly expectedPlanDigest: string;
};

export type PrepareProjectRuntimeImageOptions = {
  readonly buildRunner?: DockerProjectImageBuildRunner;
  readonly inspectRunner?: DockerImageInspectRunner;
};

/**
 * Successful project-image preparation. The mutable build tag is retained
 * only as setup provenance; callers must use `image` for a sandbox plan.
 */
export type PreparedProjectRuntimeImage = {
  readonly kind: "PROJECT_RUNTIME_IMAGE";
  readonly setupOnly: true;
  readonly contextDirectory: string;
  readonly dockerfile: string;
  readonly requestedTag: string;
  readonly buildNetwork: DockerBuildNetwork;
  /** The only value suitable for proof-grade sandbox replay. */
  readonly image: string;
  readonly build: Readonly<{
    readonly resolvedRepoDigest: string;
    readonly timeoutMs: number;
    readonly maxOutputBytes: number;
  }>;
};

/** A read-only lookup of a project tag after the user has made it available locally. */
export type ResolvedProjectRuntimeImage = {
  /** Mutable setup tag used to locate the local image; never suitable for replay itself. */
  readonly requestedTag: string;
  /** Immutable repository digest suitable for a proof-grade sandbox plan. */
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
  return createDockerImageInspectCommandForReference(runtime.tag);
}

/** Build a no-shell, read-only image-inspection command for a validated reference. */
function createDockerImageInspectCommandForReference(reference: string): DockerImageInspectCommand {
  return Object.freeze({
    executable: "docker",
    arguments: Object.freeze([
      "image",
      "inspect",
      "--format",
      DOCKER_REPO_DIGESTS_FORMAT,
      reference
    ]),
    timeoutMs: DOCKER_IMAGE_INSPECT_TIMEOUT_MS
  });
}

/**
 * Construct the only Docker pull FaultLine's guided first-run path permits.
 * The selected tag comes exclusively from the small reviewed catalog.
 */
export function createDockerImagePullCommand(runtime: CuratedRuntime): DockerImagePullCommand {
  return Object.freeze({
    executable: "docker",
    arguments: Object.freeze(["pull", runtime.tag]),
    timeoutMs: DOCKER_IMAGE_PULL_TIMEOUT_MS
  });
}

type NormalizedProjectRuntimeImageBuild = {
  readonly contextDirectory: string;
  readonly dockerfile: string;
  readonly imageTag: string;
  readonly network: DockerBuildNetwork;
};

function projectImageInputError(message: string): never {
  throw new ProjectRuntimeImagePreparationError("PROJECT_IMAGE_INVALID_INPUT", message);
}

function assertSafeProjectBuildPath(label: string, value: string): void {
  if (!value || /[\0\r\n]/.test(value)) {
    projectImageInputError(`Project image ${label} must be non-empty and cannot contain a NUL or line break.`);
  }
}

function isDescendantOrSame(directory: string, candidate: string): boolean {
  const pathFromDirectory = relative(directory, candidate);
  return pathFromDirectory === ""
    || (!pathFromDirectory.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`)
      && pathFromDirectory !== ".."
      && !isAbsolute(pathFromDirectory));
}

function normalizeProjectBuildPaths(request: ProjectRuntimeImageBuildRequest): NormalizedProjectRuntimeImageBuild {
  assertSafeProjectBuildPath("contextDirectory", request.contextDirectory);
  assertSafeProjectBuildPath("dockerfile", request.dockerfile);
  if (!isAbsolute(request.contextDirectory)) {
    projectImageInputError("Project image contextDirectory must be an absolute path.");
  }

  let contextDirectory: string;
  let dockerfile: string;
  try {
    contextDirectory = realpathSync(resolve(request.contextDirectory));
    if (!statSync(contextDirectory).isDirectory()) {
      projectImageInputError(`Project image contextDirectory is not a directory: ${request.contextDirectory}`);
    }
    if (contextDirectory === parse(contextDirectory).root) {
      projectImageInputError("Project image contextDirectory cannot be a filesystem root.");
    }

    // A relative Dockerfile is always relative to the reviewed build context,
    // rather than the caller's process cwd.
    dockerfile = realpathSync(resolve(contextDirectory, request.dockerfile));
    if (!statSync(dockerfile).isFile()) {
      projectImageInputError(`Project image dockerfile is not a regular file: ${request.dockerfile}`);
    }
  } catch (error) {
    if (error instanceof ProjectRuntimeImagePreparationError) throw error;
    const detail = error instanceof Error ? `: ${error.message}` : "";
    projectImageInputError(`FaultLine could not resolve the project image build paths${detail}`);
  }

  if (!isDescendantOrSame(contextDirectory, dockerfile)) {
    projectImageInputError("Project image dockerfile must resolve inside the declared contextDirectory.");
  }
  return { contextDirectory, dockerfile, imageTag: validateProjectImageTag(request.imageTag), network: request.network };
}

type ProjectBuildContextFingerprint = {
  readonly contextDigest: string;
  readonly dockerfileDigest: string;
  readonly fileCount: number;
  readonly totalBytes: number;
};

/**
 * Fingerprint every file Docker can receive from this reviewed context. We
 * reject links and special files rather than silently resolving or omitting
 * them: their Docker semantics can vary across hosts and would make the
 * review digest ambiguous. The limits keep a confirmation screen from
 * unexpectedly hashing a whole home directory or generated dependency tree.
 */
function fingerprintProjectBuildContext(normalized: NormalizedProjectRuntimeImageBuild): ProjectBuildContextFingerprint {
  const files: Array<{ path: string; bytes: number; mode: number; digest: string }> = [];
  let totalBytes = 0;

  const visit = (directory: string, relativeDirectory: string): void => {
    let entries;
    try {
      entries = readdirSync(directory, { withFileTypes: true })
        .sort((left, right) => left.name.localeCompare(right.name));
    } catch (error) {
      const detail = error instanceof Error ? `: ${error.message}` : "";
      projectImageInputError(`FaultLine could not enumerate the project image build context${detail}`);
    }
    for (const entry of entries) {
      const child = resolve(directory, entry.name);
      const relativePath = relativeDirectory ? `${relativeDirectory}/${entry.name}` : entry.name;
      let details;
      try {
        details = lstatSync(child);
      } catch (error) {
        const detail = error instanceof Error ? `: ${error.message}` : "";
        projectImageInputError(`FaultLine could not inspect build-context path ${relativePath}${detail}`);
      }
      if (details.isSymbolicLink()) {
        projectImageInputError(`Project image build context contains a symbolic link (${relativePath}); use a self-contained, regular-file context so the reviewed digest is unambiguous.`);
      }
      if (details.isDirectory()) {
        visit(child, relativePath);
        continue;
      }
      if (!details.isFile()) {
        projectImageInputError(`Project image build context contains a non-regular file (${relativePath}); use a self-contained regular-file context.`);
      }
      if (files.length >= PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_FILES) {
        projectImageInputError(`Project image build context exceeds ${PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_FILES} files; use a smaller reviewed context.`);
      }
      totalBytes += details.size;
      if (totalBytes > PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_BYTES) {
        projectImageInputError(`Project image build context exceeds ${PROJECT_RUNTIME_IMAGE_MAX_CONTEXT_BYTES} bytes; use a smaller reviewed context.`);
      }
      let bytes: Buffer;
      try {
        bytes = readFileSync(child);
      } catch (error) {
        const detail = error instanceof Error ? `: ${error.message}` : "";
        projectImageInputError(`FaultLine could not read build-context file ${relativePath}${detail}`);
      }
      files.push({
        path: relativePath,
        bytes: bytes.length,
        mode: details.mode & 0o777,
        digest: `sha256:${sha256(bytes)}`
      });
    }
  };

  visit(normalized.contextDirectory, "");
  let dockerfileBytes: Buffer;
  try {
    dockerfileBytes = readFileSync(normalized.dockerfile);
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : "";
    projectImageInputError(`FaultLine could not read the reviewed Dockerfile${detail}`);
  }
  return Object.freeze({
    contextDigest: digestJson({ schema: "faultline.project-build-context.v1", files }),
    dockerfileDigest: `sha256:${sha256(dockerfileBytes)}`,
    fileCount: files.length,
    totalBytes
  });
}

function projectBuildPlanDigest(
  normalized: NormalizedProjectRuntimeImageBuild,
  fingerprint: ProjectBuildContextFingerprint
): string {
  return digestJson({
    schema: "faultline.project-build-plan.v1",
    contextDirectory: normalized.contextDirectory,
    dockerfile: normalized.dockerfile,
    imageTag: normalized.imageTag,
    network: normalized.network,
    contextDigest: fingerprint.contextDigest,
    dockerfileDigest: fingerprint.dockerfileDigest
  });
}

function validateProjectImageTag(imageTag: string): string {
  if (!imageTag || /[\0\r\n\s@]/.test(imageTag)) {
    projectImageInputError("Project imageTag must be a non-empty, tag-based Docker reference without whitespace or @.");
  }
  const lastSlash = imageTag.lastIndexOf("/");
  const lastColon = imageTag.lastIndexOf(":");
  if (lastColon <= lastSlash || lastColon === imageTag.length - 1) {
    projectImageInputError("Project imageTag must include an explicit non-latest tag (for example local/faultline:deps-20260717).");
  }
  const repository = imageTag.slice(0, lastColon);
  const tag = imageTag.slice(lastColon + 1);
  if (!/^[a-z0-9][a-z0-9._:/-]*$/.test(repository) || !/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(tag)) {
    projectImageInputError("Project imageTag is not a safe Docker repository:tag reference.");
  }
  if (tag.toLowerCase() === "latest") {
    projectImageInputError("Project imageTag cannot use the mutable latest tag; choose an explicit setup tag.");
  }
  return imageTag;
}

/**
 * Describe, but do not execute, an explicit project dependency-image build.
 * This is intentionally separate from proof replay: it is the caller's place
 * to show these effects and obtain human confirmation.
 */
export function describeProjectRuntimeImageBuild(request: ProjectRuntimeImageBuildRequest): ProjectRuntimeImageBuildPlan {
  assertProjectBuildNetwork(request.network);
  const normalized = normalizeProjectBuildPaths(request);
  const command = createDockerProjectImageBuildCommandForNormalized(normalized);
  const fingerprint = fingerprintProjectBuildContext(normalized);
  const planDigest = projectBuildPlanDigest(normalized, fingerprint);
  return Object.freeze({
    kind: "PROJECT_RUNTIME_IMAGE_BUILD" as const,
    setupOnly: true as const,
    contextDirectory: normalized.contextDirectory,
    dockerfile: normalized.dockerfile,
    imageTag: normalized.imageTag,
    network: normalized.network,
    command,
    review: Object.freeze({
      planDigest,
      contextDigest: fingerprint.contextDigest,
      dockerfileDigest: fingerprint.dockerfileDigest,
      fileCount: fingerprint.fileCount,
      totalBytes: fingerprint.totalBytes
    }),
    effects: Object.freeze({
      mutatesLocalDockerImageStore: true as const,
      dockerfileInstructionsExecute: true as const,
      buildNetwork: normalized.network,
      dockerfileRunInstructionsMayAccessNetwork: normalized.network === "default",
      dockerDaemonNetworkBehaviorNotIndependentlyCertified: true as const,
      mayAccessNetwork: true,
      doesNotExecuteProof: true as const,
      requiresDigestPinnedResultForProof: true as const
    }),
    description: normalized.network === "default"
      ? "Setup only: FaultLine will ask Docker to build the chosen Dockerfile using Docker's default build network. Dockerfile RUN instructions may access the network and the local Docker image store will change; no incident proof is executed."
      : "Setup only: FaultLine will ask Docker to build the chosen Dockerfile with Dockerfile RUN networking disabled. Docker may still communicate for daemon or base-image behavior, which FaultLine does not independently certify. The local Docker image store will change; no incident proof is executed."
  });
}

/**
 * Construct the explicit project build argv without a host shell. `--pull=false`
 * prevents Docker from silently refreshing a mutable base tag; a caller can
 * explicitly prepare or pull that base before the project build.
 */
export function createDockerProjectImageBuildCommand(
  request: Pick<ProjectRuntimeImageBuildRequest, "contextDirectory" | "dockerfile" | "imageTag" | "network">
): DockerProjectImageBuildCommand {
  // This lower-level builder is public for host adapters, so fully normalize
  // its paths and boundary rather than assuming callers went through describe.
  assertProjectBuildNetwork(request.network);
  const normalized = normalizeProjectBuildPaths(request);
  return createDockerProjectImageBuildCommandForNormalized(normalized);
}

function createDockerProjectImageBuildCommandForNormalized(
  request: NormalizedProjectRuntimeImageBuild
): DockerProjectImageBuildCommand {
  return Object.freeze({
    executable: "docker",
    arguments: Object.freeze([
      "build",
      "--file",
      request.dockerfile,
      "--tag",
      request.imageTag,
      "--pull=false",
      `--network=${request.network}`,
      request.contextDirectory
    ]),
    timeoutMs: DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS
  });
}

function assertProjectBuildNetwork(network: DockerBuildNetwork): void {
  if (network !== "none" && network !== "default") {
    projectImageInputError("Project image network must be either none or default.");
  }
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

function isExpectedProjectImageRepository(imageTag: string, repoDigest: string): boolean {
  return normalizeRepository(repositoryFromTaggedReference(imageTag))
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

function dockerFailureDetail(result: DockerImageInspectResult): string {
  const detail = result.stderr.replace(/[\r\n]+/g, " ").trim();
  return detail ? `: ${detail.slice(0, 320)}` : "";
}

/**
 * Resolve the proof-grade reference for a project image that was just built.
 * Docker image IDs are intentionally not accepted here: the sandbox contract
 * requires repository@sha256, which is portable and independently inspectable.
 */
export async function resolveProjectRuntimeImage(
  imageTag: string,
  runner?: DockerImageInspectRunner
): Promise<ResolvedProjectRuntimeImage> {
  const requestedTag = validateProjectImageTag(imageTag);
  const command = createDockerImageInspectCommandForReference(requestedTag);
  const effectiveRunner = runner ?? createNodeDockerImageInspectRunner();

  let result: DockerImageInspectResult;
  try {
    result = await effectiveRunner.run(command);
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : "";
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_INSPECT_FAILED",
      `FaultLine could not inspect the project image ${requestedTag}${detail}`
    );
  }
  if (result.timedOut) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_INSPECT_TIMED_OUT",
      `Docker image inspection timed out for project image ${requestedTag}.`
    );
  }
  if (result.outputLimitExceeded) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_INSPECT_OUTPUT_LIMIT",
      `Docker image inspection exceeded FaultLine's ${DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES}-byte output limit for project image ${requestedTag}.`
    );
  }
  if (result.exitCode !== 0) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_INSPECT_FAILED",
      `Docker could not inspect project image ${requestedTag}${dockerFailureDetail(result)}`
    );
  }

  const repoDigests = parseRepoDigests(result.stdout);
  const digestPinned = repoDigests.filter(isDigestPinnedRepoDigest);
  if (digestPinned.length === 0) {
    throw new ProjectRuntimeImagePreparationError(
      "UNRESOLVED_REPO_DIGEST",
      `Docker did not expose a digest-pinned RepoDigest for ${requestedTag}. A local Docker image ID is not a proof-grade image reference. Push and pull the image through a registry outside FaultLine so Docker records its RepoDigest, then run resolveProjectRuntimeImage(${JSON.stringify(requestedTag)}) again.`
    );
  }
  const expected = digestPinned.find((candidate) => isExpectedProjectImageRepository(imageTag, candidate));
  if (expected === undefined) {
    throw new ProjectRuntimeImagePreparationError(
      "UNEXPECTED_REPOSITORY",
      `Docker returned a RepoDigest for a repository other than project image ${requestedTag}.`
    );
  }
  return Object.freeze({ requestedTag, image: expected });
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
      `Docker could not inspect the local ${runtime.tag} image${dockerFailureDetail(result)}`
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
 * Explicitly prepare one catalog-owned base image for first use, then resolve
 * Docker's immutable local RepoDigest. This is intentionally distinct from
 * `resolveCuratedRuntime`: callers must opt into a networked, mutating Docker
 * pull before this function is invoked. The resulting digest—not the tag—is
 * what an incident stores and a proof-grade sandbox later consumes.
 */
export async function prepareCuratedRuntime(
  requested: string,
  options: PrepareCuratedRuntimeOptions = {}
): Promise<PreparedCuratedRuntime> {
  const runtime = selectCuratedRuntime(requested);
  const command = createDockerImagePullCommand(runtime);
  const runner = options.pullRunner ?? createNodeDockerImagePullRunner();

  let result: DockerImagePullResult;
  try {
    result = await runner.run(command);
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : "";
    throw new CuratedRuntimeResolutionError(
      "DOCKER_PULL_FAILED",
      `FaultLine could not pull the reviewed ${runtime.tag} image${detail}`
    );
  }
  if (result.timedOut) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_PULL_TIMED_OUT",
      `Docker image pull timed out for ${runtime.tag}.`
    );
  }
  if (result.outputLimitExceeded) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_PULL_OUTPUT_LIMIT",
      `Docker image pull exceeded FaultLine's ${DOCKER_IMAGE_PULL_MAX_OUTPUT_BYTES}-byte output limit for ${runtime.tag}.`
    );
  }
  if (result.exitCode !== 0) {
    throw new CuratedRuntimeResolutionError(
      "DOCKER_PULL_FAILED",
      `Docker could not pull the reviewed ${runtime.tag} image${dockerFailureDetail(result)}`
    );
  }

  const resolved = await resolveCuratedRuntime(requested, options.inspectRunner);
  return Object.freeze({
    ...resolved,
    pull: Object.freeze({ tag: runtime.tag, resolvedRepoDigest: resolved.image })
  });
}

/**
 * Explicitly build a project-owned dependency image, then require Docker to
 * resolve it to a portable RepoDigest. This function is setup-only and is
 * intentionally never used by proof replay; replay consumes only the returned
 * immutable `image` value. The acknowledgement makes the potentially
 * effectful Dockerfile execution impossible to invoke accidentally.
 *
 * A plain local `docker build` commonly exposes only an image ID, not a
 * RepoDigest. In that case this fails closed rather than pretending an image
 * ID is a portable proof reference. The caller can push/pull through its own
 * registry outside FaultLine, then use resolveProjectRuntimeImage() without
 * rebuilding before a proof-grade replay can proceed.
 */
export async function prepareProjectRuntimeImage(
  request: PrepareProjectRuntimeImageRequest,
  options: PrepareProjectRuntimeImageOptions = {}
): Promise<PreparedProjectRuntimeImage> {
  if (request.confirmation !== PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION) {
    throw new ProjectRuntimeImagePreparationError(
      "PROJECT_IMAGE_CONFIRMATION_REQUIRED",
      `Project image setup requires confirmation: ${PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION}. Review describeProjectRuntimeImageBuild() before confirming.`
    );
  }
  const plan = describeProjectRuntimeImageBuild(request);
  if (!SHA256_DIGEST.test(request.expectedPlanDigest)) {
    throw new ProjectRuntimeImagePreparationError(
      "PROJECT_IMAGE_PLAN_DIGEST_REQUIRED",
      "Project image setup requires the exact plan.review.planDigest from a separately reviewed fl runtime project plan response."
    );
  }
  if (request.expectedPlanDigest !== plan.review.planDigest) {
    throw new ProjectRuntimeImagePreparationError(
      "PROJECT_IMAGE_PLAN_MISMATCH",
      "The project build plan no longer matches the reviewed context, Dockerfile, tag, or network policy. Run fl runtime project plan again, review its new digest, then retry."
    );
  }
  const runner = options.buildRunner ?? createNodeDockerProjectImageBuildRunner();

  let result: DockerProjectImageBuildResult;
  try {
    result = await runner.run(plan.command);
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : "";
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_BUILD_FAILED",
      `FaultLine could not build project image ${plan.imageTag}${detail}`
    );
  }
  if (result.timedOut) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_BUILD_TIMED_OUT",
      `Docker project image build timed out after ${DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS}ms for ${plan.imageTag}.`
    );
  }
  if (result.outputLimitExceeded) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_BUILD_OUTPUT_LIMIT",
      `Docker project image build exceeded FaultLine's ${DOCKER_PROJECT_IMAGE_BUILD_MAX_OUTPUT_BYTES}-byte output limit for ${plan.imageTag}.`
    );
  }
  if (result.exitCode !== 0) {
    throw new ProjectRuntimeImagePreparationError(
      "DOCKER_BUILD_FAILED",
      `Docker could not build project image ${plan.imageTag}${dockerFailureDetail(result)}`
    );
  }

  const resolved = await resolveProjectRuntimeImage(plan.imageTag, options.inspectRunner);
  return Object.freeze({
    kind: "PROJECT_RUNTIME_IMAGE" as const,
    setupOnly: true as const,
    contextDirectory: plan.contextDirectory,
    dockerfile: plan.dockerfile,
    requestedTag: plan.imageTag,
    buildNetwork: plan.network,
    image: resolved.image,
    build: Object.freeze({
      resolvedRepoDigest: resolved.image,
      timeoutMs: DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS,
      maxOutputBytes: DOCKER_PROJECT_IMAGE_BUILD_MAX_OUTPUT_BYTES
    })
  });
}

/**
 * Local production runner for the fixed inspect argv. No shell is involved;
 * the resolver itself still performs only `docker image inspect`, never pull.
 */
function createNodeDockerImageRunner(): {
  run(command: DockerImageInspectCommand | DockerImagePullCommand | DockerProjectImageBuildCommand): Promise<DockerImageInspectResult>;
} {
  return {
    run(command: DockerImageInspectCommand | DockerImagePullCommand | DockerProjectImageBuildCommand): Promise<DockerImageInspectResult> {
      return new Promise((resolveResult) => {
        let settled = false;
        let timedOut = false;
        let outputLimitExceeded = false;
        let stdout = "";
        let stderr = "";
        let timeout: NodeJS.Timeout | undefined;
        const maxOutputBytes = command.arguments[0] === "pull"
          ? DOCKER_IMAGE_PULL_MAX_OUTPUT_BYTES
          : command.arguments[0] === "build"
            ? DOCKER_PROJECT_IMAGE_BUILD_MAX_OUTPUT_BYTES
            : DOCKER_IMAGE_INSPECT_MAX_OUTPUT_BYTES;

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
          stderr = error instanceof Error ? error.message : "Unable to start Docker image command.";
          finish(null);
          return;
        }

        const appendOutput = (stream: "stdout" | "stderr", chunk: Buffer): void => {
          const bytes = Buffer.from(chunk);
          const used = Buffer.byteLength(stdout, "utf8") + Buffer.byteLength(stderr, "utf8");
          const remaining = maxOutputBytes - used;
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

/** Local production runner for read-only local `docker image inspect`. */
export function createNodeDockerImageInspectRunner(): DockerImageInspectRunner {
  return createNodeDockerImageRunner();
}

/** Local production runner for the explicitly requested catalog `docker pull`. */
export function createNodeDockerImagePullRunner(): DockerImagePullRunner {
  return createNodeDockerImageRunner();
}

/**
 * Local production runner for an explicitly confirmed project Docker build.
 * Like the catalog runners, it invokes Docker by argv, bounds output, and
 * never passes a Dockerfile, path, or tag through a host shell.
 */
export function createNodeDockerProjectImageBuildRunner(): DockerProjectImageBuildRunner {
  return createNodeDockerImageRunner();
}
