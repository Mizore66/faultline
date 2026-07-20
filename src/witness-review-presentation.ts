import { execFileSync } from "node:child_process";
import type { IncidentDraft } from "./incident.js";
import type { WitnessReview } from "./witness-review.js";

const SHA256_LITERAL_SOURCE = "sha256:[a-f0-9]{64}";
const RELATIVE_UNITS: ReadonlyArray<readonly [number, string]> = [
  [60, "second"],
  [60, "minute"],
  [24, "hour"],
  [7, "day"],
  [4.34524, "week"],
  [12, "month"],
  [Number.POSITIVE_INFINITY, "year"]
];

export type HumanizedOverlay = {
  readonly path: string;
  readonly kind: "utf8" | "binary";
  readonly byteLength: number;
  readonly text?: string;
  readonly base64?: string;
};

export type CageBadges = {
  readonly docker: "Docker";
  readonly network: "network OFF";
  readonly timeout: string;
};

export type RangeCommitLine = {
  readonly short: string;
  readonly subject: string;
  readonly author: string;
  readonly relativeAge: string;
};

export type HumanizedRange = {
  readonly summary: string;
  readonly lines: readonly RangeCommitLine[];
  readonly source?: string;
  readonly unavailableReason?: string;
};

export type EvidenceRecord = {
  readonly proposalDigest: string;
  readonly reviewDigest: string;
  readonly commandDigest: string;
  readonly overlayDigest: string;
  readonly incidentPacketDigest: string;
  readonly draftDigest?: string;
  readonly approvalDigest?: string;
  readonly frozenDigest?: string;
};

export type HumanizedWitnessReview = {
  readonly symptom: string;
  readonly command: string;
  readonly behavior: string;
  readonly overlays: readonly HumanizedOverlay[];
  readonly cage: CageBadges;
  readonly range: HumanizedRange;
  readonly evidence: EvidenceRecord;
  readonly lifecycle: string;
};

function relativeAgeFromIso(iso: string, nowMs: number): string {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return "unknown age";
  let seconds = Math.round((nowMs - then) / 1000);
  const suffix = seconds >= 0 ? "ago" : "from now";
  seconds = Math.abs(seconds);
  if (seconds < 5) return "just now";
  let value = seconds;
  let unit = "second";
  for (const [divisor, nextUnit] of RELATIVE_UNITS) {
    if (value < divisor) break;
    value /= divisor;
    unit = nextUnit;
  }
  const rounded = Math.max(1, Math.round(value));
  return `${rounded} ${unit}${rounded === 1 ? "" : "s"} ${suffix}`;
}

function shortRevision(value: string): string {
  if (/^[0-9a-f]{40}$/i.test(value) || /^[0-9a-f]{64}$/i.test(value)) return value.slice(0, 12);
  return value.length > 24 ? `${value.slice(0, 21)}…` : value;
}

/** Decode overlay bytes as UTF-8 when valid; otherwise keep base64 for binary. */
export function humanizeOverlay(path: string, bytesBase64: string): HumanizedOverlay {
  const bytes = Buffer.from(bytesBase64, "base64");
  const text = bytes.toString("utf8");
  const reencoded = Buffer.from(text, "utf8");
  const isUtf8 = reencoded.equals(bytes) && !text.includes("\u0000");
  if (isUtf8) {
    return { path, kind: "utf8", byteLength: bytes.byteLength, text };
  }
  return { path, kind: "binary", byteLength: bytes.byteLength, base64: bytesBase64 };
}

export function cageBadges(timeoutSeconds: number): CageBadges {
  return {
    docker: "Docker",
    network: "network OFF",
    timeout: `timeout ${timeoutSeconds}s`
  };
}

function tryGit(repository: string, args: readonly string[]): string | null {
  try {
    return execFileSync("git", ["-C", repository, ...args], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      timeout: 5_000,
      windowsHide: true,
      maxBuffer: 2 * 1024 * 1024
    }).trim();
  } catch {
    return null;
  }
}

/**
 * Prefer a git-log style range when the draft points at a real local repo.
 * Never surfaces content-address digests here — only short commit ids.
 */
export function humanizeRange(
  draft: IncidentDraft | null,
  options: { readonly nowMs?: number } = {}
): HumanizedRange {
  if (draft === null) {
    return {
      summary: "No linked range",
      lines: [],
      unavailableReason: "Standalone proposal — no incident draft range was supplied."
    };
  }
  const nowMs = options.nowMs ?? Date.now();
  const { ancestor, descendant, source } = draft.range;
  const repository = draft.repository;
  const count = tryGit(repository, ["rev-list", "--count", `${ancestor}..${descendant}`]);
  const nameOnly = tryGit(repository, [
    "diff-tree",
    "--no-commit-id",
    "--name-only",
    "-r",
    `${ancestor}..${descendant}`
  ]);
  const log = tryGit(repository, [
    "log",
    "--format=%H%x09%an%x09%aI%x09%s",
    "--no-decorate",
    `${ancestor}..${descendant}`
  ]);

  if (count === null || log === null) {
    return {
      summary: `Range ${shortRevision(ancestor)}..${shortRevision(descendant)}`,
      lines: [
        {
          short: shortRevision(ancestor),
          subject: "(known good)",
          author: "—",
          relativeAge: "—"
        },
        {
          short: shortRevision(descendant),
          subject: "(known bad)",
          author: "—",
          relativeAge: "—"
        }
      ],
      source,
      unavailableReason: "Git metadata unavailable for this draft repository path; showing abbreviated endpoints only."
    };
  }

  const commitCount = Number(count);
  const files = nameOnly === null || nameOnly === ""
    ? 0
    : new Set(nameOnly.split(/\r?\n/).filter(Boolean)).size;
  const lines: RangeCommitLine[] = log
    .split(/\r?\n/)
    .filter(Boolean)
    .slice(0, 40)
    .map((line) => {
      const [hash = "", author = "", iso = "", ...subjectParts] = line.split("\t");
      return {
        short: shortRevision(hash),
        subject: subjectParts.join("\t") || "(no subject)",
        author,
        relativeAge: relativeAgeFromIso(iso, nowMs)
      };
    });

  return {
    summary: `${Number.isFinite(commitCount) ? commitCount : lines.length} commits · ${files} files`,
    lines,
    source
  };
}

export function buildHumanizedWitnessReview(input: {
  readonly review: WitnessReview;
  readonly draft?: IncidentDraft | null;
  readonly approvalDigest?: string;
  readonly frozenDigest?: string;
  readonly nowMs?: number;
}): HumanizedWitnessReview {
  const { review } = input;
  const draft = input.draft ?? null;
  const proposal = review.proposal;
  const overlays = proposal.witness.overlays.map((overlay) =>
    humanizeOverlay(overlay.path, overlay.bytesBase64)
  );
  let lifecycle = "PROPOSED → awaiting human approval";
  if (input.frozenDigest !== undefined) lifecycle = "PROPOSED → APPROVED → FROZEN";
  else if (input.approvalDigest !== undefined) lifecycle = "PROPOSED → APPROVED → awaiting freeze";

  return {
    symptom: proposal.incidentPacket.symptom,
    command: proposal.witness.command,
    behavior: proposal.witness.behavior,
    overlays,
    cage: cageBadges(proposal.witness.policy.timeoutSeconds),
    range: humanizeRange(draft, input.nowMs === undefined ? {} : { nowMs: input.nowMs }),
    evidence: {
      proposalDigest: proposal.proposalDigest,
      reviewDigest: review.reviewDigest,
      commandDigest: proposal.witness.commandDigest,
      overlayDigest: proposal.witness.overlayDigest,
      incidentPacketDigest: proposal.incidentPacketDigest,
      ...(draft === null ? {} : { draftDigest: draft.draftDigest }),
      ...(input.approvalDigest === undefined ? {} : { approvalDigest: input.approvalDigest }),
      ...(input.frozenDigest === undefined ? {} : { frozenDigest: input.frozenDigest })
    },
    lifecycle
  };
}

/** True when the visible (above-fold) surface still contains a raw sha256 digest. */
export function aboveFoldContainsRawDigest(text: string): boolean {
  return new RegExp(SHA256_LITERAL_SOURCE, "i").test(text);
}

export function formatCageBadgeLine(cage: CageBadges): string {
  return `${cage.docker} · ${cage.network} · ${cage.timeout}`;
}

export function formatHumanizedReviewText(view: HumanizedWitnessReview): string {
  const lines: string[] = [
    "FaultLine witness review (local, read-only until you approve)",
    `Lifecycle: ${view.lifecycle}`,
    `Cage: ${formatCageBadgeLine(view.cage)}`,
    "",
    `Symptom: ${view.symptom}`,
    "",
    "Command (not executed):",
    view.command,
    "",
    `Behavior: ${view.behavior}`,
    "",
    `Range: ${view.range.summary}`
  ];
  if (view.range.source !== undefined) lines.push(`Source: ${view.range.source}`);
  if (view.range.unavailableReason !== undefined) lines.push(view.range.unavailableReason);
  for (const commit of view.range.lines) {
    lines.push(`  ${commit.short}  ${commit.subject}  · ${commit.author}  · ${commit.relativeAge}`);
  }
  lines.push("", `Overlays (${view.overlays.length}):`);
  if (view.overlays.length === 0) {
    lines.push("  (none)");
  } else {
    for (const overlay of view.overlays) {
      if (overlay.kind === "utf8") {
        lines.push(`  ${overlay.path} · UTF-8 · ${overlay.byteLength} bytes`);
        for (const row of (overlay.text ?? "").split(/\r?\n/).slice(0, 40)) {
          lines.push(`    | ${row}`);
        }
      } else {
        lines.push(`  ${overlay.path} · binary · ${overlay.byteLength} bytes (base64 below)`);
        lines.push(`    ${overlay.base64 ?? ""}`);
      }
    }
  }
  lines.push(
    "",
    "Evidence record (digests — expand only if needed):",
    `  proposal: ${view.evidence.proposalDigest}`,
    `  review:   ${view.evidence.reviewDigest}`,
    `  command:  ${view.evidence.commandDigest}`,
    `  overlay:  ${view.evidence.overlayDigest}`,
    `  packet:   ${view.evidence.incidentPacketDigest}`
  );
  if (view.evidence.draftDigest !== undefined) lines.push(`  draft:    ${view.evidence.draftDigest}`);
  if (view.evidence.approvalDigest !== undefined) lines.push(`  approval: ${view.evidence.approvalDigest}`);
  if (view.evidence.frozenDigest !== undefined) lines.push(`  frozen:   ${view.evidence.frozenDigest}`);
  return `${lines.join("\n")}\n`;
}

export function readGitUserName(cwd: string = process.cwd()): string {
  const name = tryGit(cwd, ["config", "--get", "user.name"]);
  if (name !== null && name.trim() !== "") return name.trim();
  return "local-reviewer";
}
