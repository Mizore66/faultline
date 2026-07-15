import { existsSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  INTEGRITY_ATTESTATION_LIMITATION,
  bundleAttestationDigest,
  createBundleAttestation,
  readVerifiedBundleAttestation,
  verifyStoredBundleAttestation,
  writeBundleAttestation
} from "../src/attestation.js";

const digest = (character: string): string => `sha256:${character.repeat(64)}`;

function input(receiptId = "demo-receipt") {
  return {
    receiptId,
    subject: "faultline:invoice-settlement-regression",
    issuer: "ci.example.test/build-42",
    issuedAt: "2026-07-16T01:02:03.004Z",
    bundleRootDigest: digest("a"),
    sourceDigest: digest("b"),
    gitCheckpointDigest: digest("c"),
    witnessDigest: digest("d")
  };
}

function temporaryStore(): { root: string; store: string } {
  const root = mkdtempSync(join(tmpdir(), "faultline-attestation-root-"));
  return { root, store: join(root, "receipts") };
}

describe("external FaultLine bundle attestations", () => {
  it("canonically binds the bundle root, subject, issuer, time, and optional evidence digests", () => {
    const { root, store } = temporaryStore();
    try {
      const first = createBundleAttestation(input());
      const reordered = createBundleAttestation({
        witnessDigest: digest("d"),
        sourceDigest: digest("b"),
        bundleRootDigest: digest("a"),
        issuer: "ci.example.test/build-42",
        subject: "faultline:invoice-settlement-regression",
        receiptId: "demo-receipt",
        gitCheckpointDigest: digest("c"),
        issuedAt: "2026-07-16T01:02:03.004Z"
      });
      expect(first.receiptDigest).toBe(reordered.receiptDigest);
      expect(bundleAttestationDigest(first)).toBe(first.receiptDigest);

      const written = writeBundleAttestation(store, input(), { attestationRoot: root });
      const verified = verifyStoredBundleAttestation(store, "demo-receipt", written.receiptDigest, { attestationRoot: root });
      expect(verified).toMatchObject({ valid: true, externalDigestStatus: "MATCH", receiptDigest: written.receiptDigest });
      expect(verified.attestation).toMatchObject({
        bundleRootDigest: digest("a"),
        sourceDigest: digest("b"),
        gitCheckpointDigest: digest("c"),
        witnessDigest: digest("d"),
        limitation: INTEGRITY_ATTESTATION_LIMITATION
      });
      expect(readVerifiedBundleAttestation(store, "demo-receipt", written.receiptDigest, { attestationRoot: root }).issuer).toBe("ci.example.test/build-42");
      expect(readFileSync(written.path, "utf8")).toContain("not a cryptographic signature");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("detects a rewritten receipt even when an editor recomputes its internal checksum", () => {
    const { root, store } = temporaryStore();
    try {
      const written = writeBundleAttestation(store, input("tamper-check"), { attestationRoot: root });
      const tampered = JSON.parse(readFileSync(written.path, "utf8")) as Record<string, unknown>;
      tampered.subject = "faultline:other-incident";
      tampered.receiptDigest = bundleAttestationDigest(tampered as ReturnType<typeof createBundleAttestation>);
      writeFileSync(written.path, `${JSON.stringify(tampered)}\n`, "utf8");

      const verified = verifyStoredBundleAttestation(store, "tamper-check", written.receiptDigest, { attestationRoot: root });
      expect(verified.valid).toBe(false);
      expect(verified.externalDigestStatus).toBe("MISMATCH");
      expect(verified.errors).toContain("externally supplied receipt digest does not match");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("is write-once and never replaces an existing receipt", () => {
    const { root, store } = temporaryStore();
    try {
      const written = writeBundleAttestation(store, input("write-once"), { attestationRoot: root });
      const original = readFileSync(written.path, "utf8");
      expect(() => writeBundleAttestation(store, { ...input("write-once"), subject: "faultline:replacement" }, { attestationRoot: root }))
        .toThrow(/already exists and cannot be overwritten/);
      expect(readFileSync(written.path, "utf8")).toBe(original);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("rejects escaping stores, path-shaped ids, and symlinked output directories", () => {
    const { root, store } = temporaryStore();
    const outside = mkdtempSync(join(tmpdir(), "faultline-attestation-outside-"));
    try {
      expect(() => writeBundleAttestation(join(root, "..", "escaped"), input("escaped"), { attestationRoot: root }))
        .toThrow(/inside the managed root/);
      expect(existsSync(join(root, "..", "escaped"))).toBe(false);
      expect(() => writeBundleAttestation(store, input("../escape"), { attestationRoot: root }))
        .toThrow(/cannot be a path/);

      const linkedStore = join(root, "linked-store");
      try {
        symlinkSync(outside, linkedStore, process.platform === "win32" ? "junction" : "dir");
      } catch (error) {
        const code = error && typeof error === "object" && "code" in error ? (error as { code?: unknown }).code : undefined;
        // Some locked-down Windows environments deny link creation. The
        // production check is still covered on platforms that permit a link.
        if (code === "EPERM" || code === "EACCES") return;
        throw error;
      }
      expect(() => writeBundleAttestation(linkedStore, input("linked"), { attestationRoot: root }))
        .toThrow(/symbolic link|non-directory/);
      expect(existsSync(join(outside, "linked.json"))).toBe(false);
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(outside, { recursive: true, force: true });
    }
  });
});
