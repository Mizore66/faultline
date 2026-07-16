import { describe, expect, it } from "vitest";
import {
  CURATED_RUNTIME_CATALOG,
  CuratedRuntimeResolutionError,
  DOCKER_REPO_DIGESTS_FORMAT,
  createDockerImageInspectCommand,
  resolveCuratedRuntime,
  selectCuratedRuntime,
  type DockerImageInspectCommand,
  type DockerImageInspectRunner
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
});
