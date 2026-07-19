import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

export const AGENTS_MD_BEGIN = "<!-- FAULTLINE:BEGIN -->" as const;
export const AGENTS_MD_END = "<!-- FAULTLINE:END -->" as const;

export type AgentsMdInvariantInput = {
  readonly repository: string;
  readonly classification: "PREVENTION_VERIFIED";
  readonly originalProofRoot: string;
  readonly frozenWitnessDigest: string;
  readonly preventionRootDigest: string;
  readonly lastGoodRunIds: readonly string[];
  readonly firstBadRunIds: readonly string[];
  readonly repairedRunIds: readonly string[];
};

function assertDigestOnly(value: string, label: string): void {
  if (!/^sha256:[a-f0-9]{64}$/.test(value) && !/^[a-f0-9]{40,64}$/.test(value)) {
    throw new Error(`Refusing to write non-digest ${label} into AGENTS.md`);
  }
}

/** Build a secret-free invariant snippet from verified prevention parameters. */
export function renderFaultLineAgentsBlock(input: Omit<AgentsMdInvariantInput, "repository">): string {
  assertDigestOnly(input.originalProofRoot, "originalProofRoot");
  assertDigestOnly(input.frozenWitnessDigest, "frozenWitnessDigest");
  assertDigestOnly(input.preventionRootDigest, "preventionRootDigest");
  for (const id of [...input.lastGoodRunIds, ...input.firstBadRunIds, ...input.repairedRunIds]) {
    assertDigestOnly(id, "runId");
  }
  const lines = [
    AGENTS_MD_BEGIN,
    "## FaultLine prevention invariants",
    "",
    "Auto-materialized from a `PREVENTION_VERIFIED` package. Digests and run IDs only — no commands, overlays, or transcripts.",
    "",
    `- Classification: \`${input.classification}\``,
    `- Original proof root: \`${input.originalProofRoot}\``,
    `- Frozen witness digest: \`${input.frozenWitnessDigest}\``,
    `- Prevention package root: \`${input.preventionRootDigest}\``,
    `- Last-good run IDs: ${input.lastGoodRunIds.map((id) => `\`${id}\``).join(", ")}`,
    `- First-bad run IDs: ${input.firstBadRunIds.map((id) => `\`${id}\``).join(", ")}`,
    `- Repaired run IDs: ${input.repairedRunIds.map((id) => `\`${id}\``).join(", ")}`,
    "",
    "Do not weaken these bindings without re-running `fl prevention write --from-bundle` and `fl prevention verify`.",
    AGENTS_MD_END
  ];
  return `${lines.join("\n")}\n`;
}

/**
 * Create or replace the marked FaultLine block in project-root AGENTS.md.
 * Idempotent: repeated writes with the same snippet converge.
 */
export function upsertFaultLineAgentsMd(input: AgentsMdInvariantInput): {
  readonly path: string;
  readonly status: "CREATED" | "UPDATED" | "UNCHANGED";
} {
  const path = join(resolve(input.repository), "AGENTS.md");
  const block = renderFaultLineAgentsBlock(input);
  const existing = existsSync(path) ? readFileSync(path, "utf8") : "";
  const begin = existing.indexOf(AGENTS_MD_BEGIN);
  const end = existing.indexOf(AGENTS_MD_END);
  let next: string;
  if (begin >= 0 && end > begin) {
    const afterEnd = end + AGENTS_MD_END.length;
    const trailing = existing.slice(afterEnd).replace(/^\r?\n/, "");
    next = `${existing.slice(0, begin)}${block}${trailing}`;
  } else if (existing.trim().length === 0) {
    next = block;
  } else {
    next = `${existing.replace(/\s*$/, "")}\n\n${block}`;
  }
  if (next === existing) {
    return { path, status: "UNCHANGED" };
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, next, { encoding: "utf8", mode: 0o644 });
  return { path, status: existing.length === 0 ? "CREATED" : "UPDATED" };
}
