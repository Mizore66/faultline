import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { sha256 } from "../src/canonical.js";
import { verifyGitInvestigationProofBundle } from "../src/git-proof-bundle.js";

const FAKE_DIGEST = "0".repeat(64);

function writeHostileRoot(directory: string, hashesBody: string, extras?: {
  readonly manifest?: string;
  readonly rootOverride?: string;
  readonly files?: Readonly<Record<string, string | Buffer>>;
}): void {
  mkdirSync(directory, { recursive: true });
  writeFileSync(join(directory, "hashes.txt"), hashesBody, "utf8");
  writeFileSync(
    join(directory, "ROOT.sha256"),
    extras?.rootOverride ?? `sha256:${sha256(hashesBody)}\n`,
    "utf8"
  );
  writeFileSync(join(directory, "manifest.json"), extras?.manifest ?? "{", "utf8");
  for (const [relative, bytes] of Object.entries(extras?.files ?? {})) {
    const absolute = join(directory, relative);
    mkdirSync(join(absolute, ".."), { recursive: true });
    writeFileSync(absolute, bytes);
  }
}

describe("SEC-01 hostile Git offline verifier corpus", () => {
  it("fails closed for malformed metadata, traversal paths, and hash confusion", () => {
    const cases: Array<() => void> = [
      () => {
        const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-git-malformed-"));
        try {
          writeHostileRoot(directory, `${FAKE_DIGEST}  manifest.json\n`, {
            manifest: "{ not-json",
            files: { "manifest.json": "{ not-json" }
          });
          const verification = verifyGitInvestigationProofBundle(directory);
          expect(verification.valid).toBe(false);
          expect(verification.errors.length).toBeGreaterThan(0);
        } finally {
          rmSync(directory, { recursive: true, force: true });
        }
      },
      () => {
        const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-git-traversal-"));
        try {
          writeHostileRoot(directory, `${FAKE_DIGEST}  ../outside\n`);
          const verification = verifyGitInvestigationProofBundle(directory);
          expect(verification.valid).toBe(false);
          expect(verification.errors.some((error) => /outside|safely|traversal|artifact/i.test(error))).toBe(true);
        } finally {
          rmSync(directory, { recursive: true, force: true });
        }
      },
      () => {
        const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-git-hash-confusion-"));
        try {
          const body = "safe-bytes\n";
          writeHostileRoot(directory, `${FAKE_DIGEST}  payload.txt\n`, {
            files: { "payload.txt": body },
            rootOverride: `sha256:${"f".repeat(64)}\n`
          });
          const verification = verifyGitInvestigationProofBundle(directory, `sha256:${"a".repeat(64)}`);
          expect(verification.valid).toBe(false);
          expect(verification.errors.length).toBeGreaterThan(0);
        } finally {
          rmSync(directory, { recursive: true, force: true });
        }
      },
      () => {
        const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-git-undeclared-"));
        try {
          writeHostileRoot(directory, `${FAKE_DIGEST}  manifest.json\n`, {
            manifest: "{}",
            files: {
              "manifest.json": "{}",
              "sneaky.txt": "undeclared"
            }
          });
          const verification = verifyGitInvestigationProofBundle(directory);
          expect(verification.valid).toBe(false);
        } finally {
          rmSync(directory, { recursive: true, force: true });
        }
      }
    ];

    for (const run of cases) {
      expect(() => run()).not.toThrow();
    }
  });

  it("fails closed for archive-limit hostile catalogs without throwing", () => {
    const directory = mkdtempSync(join(tmpdir(), "faultline-hostile-git-archive-limit-"));
    try {
      // Exceed MAX_GIT_PROOF_ARTIFACTS (2048) with declared paths only — no payload execution.
      const lines = Array.from({ length: 2_050 }, (_, index) => `${FAKE_DIGEST}  artifact-${index}.bin`);
      writeHostileRoot(directory, `${lines.join("\n")}\n`);
      const verification = verifyGitInvestigationProofBundle(directory);
      expect(verification.valid).toBe(false);
      expect(verification.errors.length).toBeGreaterThan(0);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }

    const oversizedCatalog = mkdtempSync(join(tmpdir(), "faultline-hostile-git-hash-catalog-"));
    try {
      // Exceed MAX_HASH_CATALOG_BYTES (4 MiB) with a hostile hashes.txt.
      const pad = "a".repeat(4 * 1024 * 1024 + 64);
      writeFileSync(join(oversizedCatalog, "hashes.txt"), pad, "utf8");
      writeFileSync(join(oversizedCatalog, "ROOT.sha256"), `sha256:${sha256(pad)}\n`, "utf8");
      writeFileSync(join(oversizedCatalog, "manifest.json"), "{}", "utf8");
      const verification = verifyGitInvestigationProofBundle(oversizedCatalog);
      expect(verification.valid).toBe(false);
      expect(verification.errors.length).toBeGreaterThan(0);
    } finally {
      rmSync(oversizedCatalog, { recursive: true, force: true });
    }
  });
});
