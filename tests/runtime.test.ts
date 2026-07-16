import { mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  CURATED_RUNTIME_CATALOG,
  CuratedRuntimeResolutionError,
  DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS,
  DOCKER_IMAGE_PULL_TIMEOUT_MS,
  DOCKER_REPO_DIGESTS_FORMAT,
  PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
  ProjectRuntimeImagePreparationError,
  createDockerImageInspectCommand,
  createDockerImagePullCommand,
  createDockerProjectImageBuildCommand,
  describeProjectRuntimeImageBuild,
  prepareCuratedRuntime,
  prepareProjectRuntimeImage,
  resolveCuratedRuntime,
  resolveProjectRuntimeImage,
  selectCuratedRuntime,
  type DockerImageInspectCommand,
  type DockerImageInspectRunner,
  type DockerImagePullCommand,
  type DockerImagePullRunner,
  type DockerProjectImageBuildCommand,
  type DockerProjectImageBuildRunner
} from "../src/runtime.js";

const nodeRepoDigest = `node@sha256:${"a".repeat(64)}`;

class FakeDockerRunner implements DockerImageInspectRunner {
  readonly calls: DockerImageInspectCommand[] = [];

  constructor(
    private readonly result: {
      readonly exitCode: number | null;
      readonly stdout: string;
      readonly stderr?: string;
      readonly timedOut?: boolean;
      readonly outputLimitExceeded?: boolean;
    }
  ) {}

  async run(command: DockerImageInspectCommand) {
    this.calls.push(command);
    return {
      exitCode: this.result.exitCode,
      stdout: this.result.stdout,
      stderr: this.result.stderr ?? "",
      timedOut: this.result.timedOut ?? false,
      outputLimitExceeded: this.result.outputLimitExceeded ?? false
    };
  }
}

class FakeDockerPullRunner implements DockerImagePullRunner {
  readonly calls: DockerImagePullCommand[] = [];

  constructor(
    private readonly result: {
      readonly exitCode: number | null;
      readonly stdout?: string;
      readonly stderr?: string;
      readonly timedOut?: boolean;
      readonly outputLimitExceeded?: boolean;
    }
  ) {}

  async run(command: DockerImagePullCommand) {
    this.calls.push(command);
    return {
      exitCode: this.result.exitCode,
      stdout: this.result.stdout ?? "",
      stderr: this.result.stderr ?? "",
      timedOut: this.result.timedOut ?? false,
      outputLimitExceeded: this.result.outputLimitExceeded ?? false
    };
  }
}

class FakeDockerProjectImageBuildRunner implements DockerProjectImageBuildRunner {
  readonly calls: DockerProjectImageBuildCommand[] = [];

  constructor(
    private readonly result: {
      readonly exitCode: number | null;
      readonly stdout?: string;
      readonly stderr?: string;
      readonly timedOut?: boolean;
      readonly outputLimitExceeded?: boolean;
    }
  ) {}

  async run(command: DockerProjectImageBuildCommand) {
    this.calls.push(command);
    return {
      exitCode: this.result.exitCode,
      stdout: this.result.stdout ?? "",
      stderr: this.result.stderr ?? "",
      timedOut: this.result.timedOut ?? false,
      outputLimitExceeded: this.result.outputLimitExceeded ?? false
    };
  }
}

function projectBuildFixture(): { readonly contextDirectory: string; readonly dockerfile: string; cleanup(): void } {
  const createdDirectory = mkdtempSync(join(tmpdir(), "faultline-project-runtime-"));
  const contextDirectory = realpathSync(createdDirectory);
  const dockerfile = join(contextDirectory, "Dockerfile");
  writeFileSync(dockerfile, "FROM node:22-alpine\nRUN npm --version\n", "utf8");
  return {
    contextDirectory,
    dockerfile,
    cleanup(): void {
      rmSync(createdDirectory, { recursive: true, force: true });
    }
  };
}

describe("curated runtime resolution", () => {
  it("keeps fixed Node, Python, and Go aliases and accepts their catalog tags", () => {
    expect(CURATED_RUNTIME_CATALOG.node.tag).toBe("node:22-alpine");
    expect(CURATED_RUNTIME_CATALOG.python.tag).toBe("python:3.13-alpine");
    expect(CURATED_RUNTIME_CATALOG.go.tag).toBe("golang:1.24-alpine");
    expect(selectCuratedRuntime("node")).toBe(CURATED_RUNTIME_CATALOG.node);
    expect(selectCuratedRuntime("python:3.13-alpine")).toBe(CURATED_RUNTIME_CATALOG.python);
    expect(selectCuratedRuntime("golang:1.24-alpine")).toBe(CURATED_RUNTIME_CATALOG.go);
  });

  it("uses a fixed no-shell docker image inspect argv and returns a local RepoDigest", async () => {
    const runner = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([nodeRepoDigest])
    });

    await expect(resolveCuratedRuntime("node", runner)).resolves.toEqual({
      runtime: CURATED_RUNTIME_CATALOG.node,
      requested: "node",
      image: nodeRepoDigest
    });
    expect(runner.calls).toEqual([{
      executable: "docker",
      arguments: ["image", "inspect", "--format", DOCKER_REPO_DIGESTS_FORMAT, "node:22-alpine"],
      timeoutMs: 5_000
    }]);
  });

  it("accepts Docker Hub's fully qualified library RepoDigest spelling", async () => {
    const runner = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([`docker.io/library/node@sha256:${"b".repeat(64)}`])
    });

    await expect(resolveCuratedRuntime("node:22-alpine", runner)).resolves.toMatchObject({
      image: `docker.io/library/node@sha256:${"b".repeat(64)}`
    });
  });

  it("rejects unknown aliases and mutable tags before Docker is invoked", async () => {
    expect(() => selectCuratedRuntime("node:latest")).toThrow(CuratedRuntimeResolutionError);
    expect(() => selectCuratedRuntime("ruby")).toThrow(/Unknown curated runtime/);

    const runner = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([nodeRepoDigest]) });
    await expect(resolveCuratedRuntime("node:latest", runner)).rejects.toMatchObject({ code: "UNKNOWN_RUNTIME" });
    expect(runner.calls).toEqual([]);
  });

  it("fails closed when Docker cannot return an immutable RepoDigest", async () => {
    const mutableOnly = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify(["node:22-alpine"]) });
    await expect(resolveCuratedRuntime("node", mutableOnly)).rejects.toMatchObject({
      code: "UNRESOLVED_REPO_DIGEST"
    });

    const malformed = new FakeDockerRunner({ exitCode: 0, stdout: "node@sha256:not-a-real-digest" });
    await expect(resolveCuratedRuntime("node", malformed)).rejects.toMatchObject({
      code: "UNRESOLVED_REPO_DIGEST"
    });

    const taggedDigest = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([`node:22-alpine@sha256:${"d".repeat(64)}`])
    });
    await expect(resolveCuratedRuntime("node", taggedDigest)).rejects.toMatchObject({
      code: "UNRESOLVED_REPO_DIGEST"
    });
  });

  it("rejects a digest from a different repository, timeouts, and inspect failures", async () => {
    const wrongRepository = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([`evil/node@sha256:${"c".repeat(64)}`])
    });
    await expect(resolveCuratedRuntime("node", wrongRepository)).rejects.toMatchObject({
      code: "UNEXPECTED_REPOSITORY"
    });

    const timedOut = new FakeDockerRunner({ exitCode: null, stdout: "", timedOut: true });
    await expect(resolveCuratedRuntime("go", timedOut)).rejects.toMatchObject({
      code: "DOCKER_INSPECT_TIMED_OUT"
    });

    const missing = new FakeDockerRunner({ exitCode: 1, stdout: "", stderr: "No such image" });
    await expect(resolveCuratedRuntime("python", missing)).rejects.toMatchObject({
      code: "DOCKER_INSPECT_FAILED"
    });

    const outputLimited = new FakeDockerRunner({ exitCode: null, stdout: "", outputLimitExceeded: true });
    await expect(resolveCuratedRuntime("node", outputLimited)).rejects.toMatchObject({
      code: "DOCKER_INSPECT_OUTPUT_LIMIT"
    });
  });

  it("exposes the same fixed command builder for hosts that want to inspect it", () => {
    expect(createDockerImageInspectCommand(CURATED_RUNTIME_CATALOG.go)).toEqual({
      executable: "docker",
      arguments: ["image", "inspect", "--format", DOCKER_REPO_DIGESTS_FORMAT, "golang:1.24-alpine"],
      timeoutMs: 5_000
    });
  });

  it("explicitly pulls only a catalog-owned tag, then records its immutable local RepoDigest", async () => {
    const pullRunner = new FakeDockerPullRunner({ exitCode: 0 });
    const inspectRunner = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([nodeRepoDigest])
    });

    await expect(prepareCuratedRuntime("node", { pullRunner, inspectRunner })).resolves.toEqual({
      runtime: CURATED_RUNTIME_CATALOG.node,
      requested: "node",
      image: nodeRepoDigest,
      pull: { tag: "node:22-alpine", resolvedRepoDigest: nodeRepoDigest }
    });
    expect(pullRunner.calls).toEqual([{
      executable: "docker",
      arguments: ["pull", "node:22-alpine"],
      timeoutMs: DOCKER_IMAGE_PULL_TIMEOUT_MS
    }]);
    expect(inspectRunner.calls).toEqual([{
      executable: "docker",
      arguments: ["image", "inspect", "--format", DOCKER_REPO_DIGESTS_FORMAT, "node:22-alpine"],
      timeoutMs: 5_000
    }]);
  });

  it("fails closed when explicit setup cannot pull or cannot resolve a RepoDigest", async () => {
    const failedPull = new FakeDockerPullRunner({ exitCode: 1, stderr: "daemon unavailable" });
    const inspect = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([nodeRepoDigest]) });
    await expect(prepareCuratedRuntime("node", { pullRunner: failedPull, inspectRunner: inspect })).rejects.toMatchObject({
      code: "DOCKER_PULL_FAILED"
    });
    expect(inspect.calls).toEqual([]);

    const timedOutPull = new FakeDockerPullRunner({ exitCode: null, timedOut: true });
    await expect(prepareCuratedRuntime("node", { pullRunner: timedOutPull, inspectRunner: inspect })).rejects.toMatchObject({
      code: "DOCKER_PULL_TIMED_OUT"
    });

    const successfulPull = new FakeDockerPullRunner({ exitCode: 0 });
    const noDigest = new FakeDockerRunner({ exitCode: 0, stdout: "[]" });
    await expect(prepareCuratedRuntime("node", { pullRunner: successfulPull, inspectRunner: noDigest })).rejects.toMatchObject({
      code: "UNRESOLVED_REPO_DIGEST"
    });

    const unknownPull = new FakeDockerPullRunner({ exitCode: 0 });
    await expect(prepareCuratedRuntime("ruby", { pullRunner: unknownPull, inspectRunner: inspect })).rejects.toMatchObject({
      code: "UNKNOWN_RUNTIME"
    });
    expect(unknownPull.calls).toEqual([]);
  });

  it("exposes a fixed prepare command distinct from the read-only resolver", () => {
    expect(createDockerImagePullCommand(CURATED_RUNTIME_CATALOG.go)).toEqual({
      executable: "docker",
      arguments: ["pull", "golang:1.24-alpine"],
      timeoutMs: DOCKER_IMAGE_PULL_TIMEOUT_MS
    });
  });
});

describe("project runtime image preparation", () => {
  it("describes an explicit, setup-only no-shell Docker build before any confirmation", () => {
    const fixture = projectBuildFixture();
    try {
      const plan = describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none"
      });

      expect(plan).toMatchObject({
        kind: "PROJECT_RUNTIME_IMAGE_BUILD",
        setupOnly: true,
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none",
        effects: {
          mutatesLocalDockerImageStore: true,
          dockerfileInstructionsExecute: true,
          buildNetwork: "none",
          dockerfileRunInstructionsMayAccessNetwork: false,
          dockerDaemonNetworkBehaviorNotIndependentlyCertified: true,
          mayAccessNetwork: true,
          doesNotExecuteProof: true,
          requiresDigestPinnedResultForProof: true
        },
        review: {
          planDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/),
          contextDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/),
          dockerfileDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/),
          fileCount: 1
        }
      });
      expect(plan.description).toContain("Setup only");
      expect(plan.command).toEqual({
        executable: "docker",
        arguments: [
          "build",
          "--file",
          fixture.dockerfile,
          "--tag",
          "registry.example/faultline/demo:deps-20260717",
          "--pull=false",
          "--network=none",
          fixture.contextDirectory
        ],
        timeoutMs: DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS
      });
    } finally {
      fixture.cleanup();
    }
  });

  it("re-resolves a user-pushed project tag read-only without rebuilding or pushing", async () => {
    const imageTag = "registry.example/faultline/demo:deps-20260717";
    const repoDigest = `registry.example/faultline/demo@sha256:${"f".repeat(64)}`;
    const inspectRunner = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([repoDigest]) });

    await expect(resolveProjectRuntimeImage(imageTag, inspectRunner)).resolves.toEqual({
      requestedTag: imageTag,
      image: repoDigest
    });
    expect(inspectRunner.calls).toEqual([{
      executable: "docker",
      arguments: ["image", "inspect", "--format", DOCKER_REPO_DIGESTS_FORMAT, imageTag],
      timeoutMs: 5_000
    }]);

    const invalid = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([repoDigest]) });
    await expect(resolveProjectRuntimeImage("registry.example/faultline/demo:latest", invalid)).rejects.toMatchObject({
      code: "PROJECT_IMAGE_INVALID_INPUT"
    });
    expect(invalid.calls).toEqual([]);
  });

  it("builds an explicitly confirmed project image through injected argv runners, then returns only its RepoDigest", async () => {
    const fixture = projectBuildFixture();
    const imageTag = "registry.example/faultline/demo:deps-20260717";
    const repoDigest = `registry.example/faultline/demo@sha256:${"e".repeat(64)}`;
    const buildRunner = new FakeDockerProjectImageBuildRunner({ exitCode: 0 });
    const inspectRunner = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([repoDigest]) });
    try {
      const reviewed = describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag,
        network: "default"
      });
      await expect(prepareProjectRuntimeImage({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag,
        network: "default",
        confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
        expectedPlanDigest: reviewed.review.planDigest
      }, { buildRunner, inspectRunner })).resolves.toEqual({
        kind: "PROJECT_RUNTIME_IMAGE",
        setupOnly: true,
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        requestedTag: imageTag,
        buildNetwork: "default",
        image: repoDigest,
        build: {
          resolvedRepoDigest: repoDigest,
          timeoutMs: DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS,
          maxOutputBytes: 128 * 1024
        }
      });
      expect(buildRunner.calls).toEqual([{
        executable: "docker",
        arguments: [
          "build",
          "--file",
          fixture.dockerfile,
          "--tag",
          imageTag,
          "--pull=false",
          "--network=default",
          fixture.contextDirectory
        ],
        timeoutMs: DOCKER_PROJECT_IMAGE_BUILD_TIMEOUT_MS
      }]);
      expect(inspectRunner.calls).toEqual([{
        executable: "docker",
        arguments: ["image", "inspect", "--format", DOCKER_REPO_DIGESTS_FORMAT, imageTag],
        timeoutMs: 5_000
      }]);
    } finally {
      fixture.cleanup();
    }
  });

  it("refuses to execute a project Dockerfile without the exact acknowledgement", async () => {
    const fixture = projectBuildFixture();
    const buildRunner = new FakeDockerProjectImageBuildRunner({ exitCode: 0 });
    try {
      const request = {
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none",
        confirmation: "NOT_CONFIRMED"
      } as unknown as Parameters<typeof prepareProjectRuntimeImage>[0];
      await expect(prepareProjectRuntimeImage(request, { buildRunner })).rejects.toMatchObject({
        code: "PROJECT_IMAGE_CONFIRMATION_REQUIRED"
      });
      expect(buildRunner.calls).toEqual([]);
    } finally {
      fixture.cleanup();
    }
  });

  it("fails closed when Docker builds only a local image ID rather than a portable RepoDigest", async () => {
    const fixture = projectBuildFixture();
    const buildRunner = new FakeDockerProjectImageBuildRunner({ exitCode: 0 });
    const inspectRunner = new FakeDockerRunner({
      exitCode: 0,
      stdout: JSON.stringify([])
    });
    try {
      const reviewed = describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "default"
      });
      await expect(prepareProjectRuntimeImage({
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "default",
        confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
        expectedPlanDigest: reviewed.review.planDigest
      }, { buildRunner, inspectRunner })).rejects.toMatchObject({
        code: "UNRESOLVED_REPO_DIGEST"
      });
      await expect(prepareProjectRuntimeImage({
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "default",
        confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
        expectedPlanDigest: reviewed.review.planDigest
      }, {
        buildRunner: new FakeDockerProjectImageBuildRunner({ exitCode: 0 }),
        inspectRunner: new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([]) })
      })).rejects.toThrow(/local Docker image ID is not a proof-grade image reference/i);
    } finally {
      fixture.cleanup();
    }
  });

  it("does not inspect after a failed, timed-out, or output-limited project build", async () => {
    const fixture = projectBuildFixture();
    const inspectRunner = new FakeDockerRunner({ exitCode: 0, stdout: JSON.stringify([nodeRepoDigest]) });
    const request = {
      contextDirectory: fixture.contextDirectory,
      dockerfile: "Dockerfile",
      imageTag: "registry.example/faultline/demo:deps-20260717",
      network: "none" as const,
      confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
      expectedPlanDigest: describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none"
      }).review.planDigest
    };
    try {
      await expect(prepareProjectRuntimeImage(request, {
        buildRunner: new FakeDockerProjectImageBuildRunner({ exitCode: 1, stderr: "Dockerfile failed" }),
        inspectRunner
      })).rejects.toMatchObject({ code: "DOCKER_BUILD_FAILED" });
      await expect(prepareProjectRuntimeImage(request, {
        buildRunner: new FakeDockerProjectImageBuildRunner({ exitCode: null, timedOut: true }),
        inspectRunner
      })).rejects.toMatchObject({ code: "DOCKER_BUILD_TIMED_OUT" });
      await expect(prepareProjectRuntimeImage(request, {
        buildRunner: new FakeDockerProjectImageBuildRunner({ exitCode: null, outputLimitExceeded: true }),
        inspectRunner
      })).rejects.toMatchObject({ code: "DOCKER_BUILD_OUTPUT_LIMIT" });
      expect(inspectRunner.calls).toEqual([]);
    } finally {
      fixture.cleanup();
    }
  });

  it("refuses ambiguous Dockerfile boundaries and mutable/unsafe build tags before Docker runs", () => {
    const fixture = projectBuildFixture();
    const outsideDirectory = mkdtempSync(join(tmpdir(), "faultline-external-runtime-"));
    const outerDockerfile = join(outsideDirectory, "Dockerfile");
    writeFileSync(outerDockerfile, "FROM scratch\n", "utf8");
    try {
      expect(() => describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: outerDockerfile,
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none"
      })).toThrow(ProjectRuntimeImagePreparationError);
      expect(() => describeProjectRuntimeImageBuild({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:latest",
        network: "none"
      })).toThrow(/cannot use the mutable latest tag/);
      expect(() => createDockerProjectImageBuildCommand({
        contextDirectory: fixture.contextDirectory,
        dockerfile: fixture.dockerfile,
        imageTag: "--tag-escape:deps",
        network: "none"
      })).toThrow(/not a safe Docker repository:tag reference/);
    } finally {
      rmSync(outsideDirectory, { recursive: true, force: true });
      fixture.cleanup();
    }
  });

  it("refuses a stale reviewed plan when any Docker build-context input changes", async () => {
    const fixture = projectBuildFixture();
    const buildRunner = new FakeDockerProjectImageBuildRunner({ exitCode: 0 });
    try {
      const input = {
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none" as const
      };
      const reviewed = describeProjectRuntimeImageBuild(input);
      writeFileSync(join(fixture.contextDirectory, "application.txt"), "changed after review\n", "utf8");
      await expect(prepareProjectRuntimeImage({
        ...input,
        confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
        expectedPlanDigest: reviewed.review.planDigest
      }, { buildRunner })).rejects.toMatchObject({ code: "PROJECT_IMAGE_PLAN_MISMATCH" });
      expect(buildRunner.calls).toEqual([]);
    } finally {
      fixture.cleanup();
    }
  });

  it("requires a separately reviewed plan digest before Docker can execute", async () => {
    const fixture = projectBuildFixture();
    const buildRunner = new FakeDockerProjectImageBuildRunner({ exitCode: 0 });
    try {
      await expect(prepareProjectRuntimeImage({
        contextDirectory: fixture.contextDirectory,
        dockerfile: "Dockerfile",
        imageTag: "registry.example/faultline/demo:deps-20260717",
        network: "none",
        confirmation: PROJECT_RUNTIME_IMAGE_BUILD_CONFIRMATION,
        expectedPlanDigest: "not-a-digest"
      }, { buildRunner })).rejects.toMatchObject({ code: "PROJECT_IMAGE_PLAN_DIGEST_REQUIRED" });
      expect(buildRunner.calls).toEqual([]);
    } finally {
      fixture.cleanup();
    }
  });
});
