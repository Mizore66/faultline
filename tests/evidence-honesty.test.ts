import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";
import { soakRowsSatisfyPromotion } from "../src/turn-proof-promotion.js";

const ROOT = process.cwd();

/** Committed SIDE_CAR samples that are allowed (organic, external, or labeled fixture). */
const ALLOWED_SIDE_CAR_SAMPLE_PREFIXES = [
  "docs/samples/faultline-self-sidecar-soak/",
  "docs/samples/mumbcs-sidecar-ledger/",
  // Labeled recorded/redacted fixture — not synthetic multi-OS soak fabrication.
  "docs/samples/later-turn-ledger/"
];

function walkJsonFiles(directory: string): string[] {
  const found: string[] = [];
  for (const name of readdirSync(directory)) {
    const absolute = join(directory, name);
    const st = statSync(absolute);
    if (st.isDirectory()) found.push(...walkJsonFiles(absolute));
    else if (name.endsWith(".json")) found.push(absolute);
  }
  return found;
}

function mentionsSideCar(payload: unknown): boolean {
  const text = JSON.stringify(payload);
  return text.includes('"SIDE_CAR"') || text.includes('"transport":"SIDE_CAR"');
}

describe("evidence honesty regressions (Fable soak / SIDE_CAR policy)", () => {
  it("does not ship a multi-os-sidecar-soak sample directory", () => {
    expect(() => statSync(join(ROOT, "docs", "samples", "multi-os-sidecar-soak"))).toThrow();
    expect(() => statSync(join(ROOT, "scripts", "generate-multi-os-soak.mjs"))).toThrow();
  });

  it("refuses seeded soak tables as TURN_PROOF promotion evidence", () => {
    expect(soakRowsSatisfyPromotion([
      { platform: "windows", origin: "organic" },
      { platform: "linux", origin: "seeded_real_ledger" },
      { platform: "macos", origin: "synthetic" }
    ])).toBe(false);
  });

  it("allows SIDE_CAR only in designated organic/external sample paths under docs/samples", () => {
    const samplesRoot = join(ROOT, "docs", "samples");
    const offenders: string[] = [];
    for (const absolute of walkJsonFiles(samplesRoot)) {
      let payload: unknown;
      try {
        payload = JSON.parse(readFileSync(absolute, "utf8"));
      } catch {
        continue;
      }
      if (!mentionsSideCar(payload)) continue;
      const rel = relative(ROOT, absolute).replaceAll("\\", "/");
      const allowed = ALLOWED_SIDE_CAR_SAMPLE_PREFIXES.some((prefix) => rel.startsWith(prefix));
      if (!allowed) offenders.push(rel);
    }
    expect(offenders).toEqual([]);
  });
});
