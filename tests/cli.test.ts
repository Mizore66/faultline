import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const workspace = process.cwd();
const tsxCli = join(workspace, "node_modules", "tsx", "dist", "cli.mjs");
const faultLineCli = join(workspace, "src", "cli.ts");

function runFl(args: string[], options: { cwd?: string; input?: string } = {}): { status: number | null; stdout: string; stderr: string } {
  const result = spawnSync(process.execPath, [tsxCli, faultLineCli, ...args], {
    cwd: options.cwd ?? workspace,
    input: options.input,
    encoding: "utf8"
  });
  return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function git(repository: string, args: string[]): void {
  const result = spawnSync("git", ["-C", repository, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(result.stderr || `git ${args.join(" ")} failed`);
}

describe("FaultLine CLI workflows", () => {
  it("reports the package version", () => {
    const result = runFl(["--version"]);
    const metadata = JSON.parse(readFileSync(join(workspace, "package.json"), "utf8")) as { version: string };
    expect(result).toMatchObject({ status: 0, stdout: `${metadata.version}\n`, stderr: "" });
  });

  it("creates an externally retained integrity receipt for a verified bundle", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-attestation-"));
    try {
      const bundle = join(directory, ".faultline", "bundles", "receipt-demo");
      const receipts = join(directory, ".faultline", "attestations");
      expect(runFl(["judge-demo", "--rerun-all", "--export-only", "--output", bundle], { cwd: directory }).status).toBe(0);
      expect(runFl(["verify", bundle], { cwd: directory }).status).toBe(0);
      const created = runFl([
        "attest", "create", "--bundle", bundle, "--receipt", "cli-receipt",
        "--subject", "FaultLine CLI test bundle", "--issuer", "test-retention-boundary", "--store", receipts
      ], { cwd: directory });
      expect(created.status).toBe(0);
      const receipt = JSON.parse(created.stdout) as { receiptDigest: string };
      const verified = runFl(["attest", "verify", "cli-receipt", "--expect-digest", receipt.receiptDigest, "--store", receipts], { cwd: directory });
      expect(verified.status).toBe(0);
      expect(JSON.parse(verified.stdout)).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("persists a proposal through human approval and immutable witness freeze", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-witness-"));
    try {
      const store = join(directory, "witnesses");
      const inputFile = join(directory, "proposal.json");
      writeFileSync(inputFile, JSON.stringify({
        proposalId: "cli-witness",
        proposalOrigin: "HUMAN",
        incidentPacket: {
          symptom: "CI failure",
          ciLog: "expected true, received false",
          repositoryLanguage: "TypeScript",
          repositorySummary: "temporary CLI test"
        },
        witness: {
          behavior: "A true value remains true.",
          command: "node witness.mjs",
          overlays: [{ path: "witness.mjs", bytesBase64: Buffer.from("process.exit(0)\n", "utf8").toString("base64") }],
          policy: { network: "disabled", credentials: "redacted", timeoutSeconds: 10 }
        }
      }), "utf8");

      expect(runFl(["witness", "propose", "--input", inputFile, "--store", store]).status).toBe(0);
      expect(runFl(["witness", "approve", "cli-witness", "--approved-by", "reviewer@example.test", "--store", store]).status).toBe(0);
      const freeze = runFl(["witness", "freeze", "cli-witness", "--store", store]);
      expect(freeze.status).toBe(0);
      const frozen = JSON.parse(freeze.stdout) as { frozenDigest: string };
      const verification = runFl(["witness", "verify", "cli-witness", "--expect-digest", frozen.frozenDigest, "--store", store]);
      expect(verification.status).toBe(0);
      expect(JSON.parse(verification.stdout)).toMatchObject({ valid: true, externalDigestStatus: "MATCH" });
      expect(existsSync(join(store, "frozen", "cli-witness.json"))).toBe(true);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("records an observed lifecycle stream and a real clean Git checkpoint", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-cli-ledger-"));
    const repository = join(directory, "repo");
    const ledger = join(directory, "recordings", "session.json");
    try {
      git(directory, ["init", "repo"]);
      git(repository, ["config", "user.email", "faultline@example.invalid"]);
      git(repository, ["config", "user.name", "FaultLine CLI test"]);
      writeFileSync(join(repository, "sample.txt"), "stable\n", "utf8");
      git(repository, ["add", "sample.txt"]);
      git(repository, ["commit", "-m", "clean checkpoint"]);

      expect(runFl(["record", "init", "--session", "cli-session", "--ledger", ledger, "--repo", repository, "--transport", "CODEX_CLI", "--actor", "reviewer@example.test"]).status).toBe(0);
      const attached = runFl([
        "record", "attach", "--ledger", ledger, "--repo", repository, "--turn", "turn-1", "--ordinal", "1",
        "--prompt-digest", `sha256:${"a".repeat(64)}`, "--output-digest", `sha256:${"b".repeat(64)}`,
        "--contribution", "refund settlement change", "--checkpoint"
      ]);
      expect(attached.status).toBe(0);
      expect(JSON.parse(attached.stdout)).toMatchObject({ status: "ATTACHED", valid: true, eventCount: 4, checkpoint: "RECORDED" });
      const verification = runFl(["record", "verify", "--ledger", ledger]);
      expect(verification.status).toBe(0);
      expect(JSON.parse(verification.stdout)).toMatchObject({ valid: true, eventCount: 4 });
      const ledgerContents = readFileSync(ledger, "utf8");
      expect(ledgerContents).toContain("WORKTREE_CHECKPOINT");
      expect(ledgerContents).toContain("refund settlement change");
      expect(ledgerContents).toContain("reviewer@example.test");
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
