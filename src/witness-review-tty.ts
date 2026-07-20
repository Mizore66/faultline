import { createInterface } from "node:readline/promises";
import { stdin as input, stdout as output } from "node:process";
import type { IncidentDraft } from "./incident.js";
import {
  approveAndFreezeWitnessReview,
  openWitnessReview,
  type WitnessReview
} from "./witness-review.js";
import type { FrozenWitness, WitnessApproval } from "./witness-lock.js";
import {
  buildHumanizedWitnessReview,
  formatHumanizedReviewText,
  readGitUserName
} from "./witness-review-presentation.js";

export type TtyApproveFreezeResult = {
  readonly approval: WitnessApproval;
  readonly frozen: FrozenWitness;
  readonly approvedBy: string;
};

export class WitnessReviewTtyError extends Error {
  public readonly code: "NON_TTY" | "DECLINED" | "EMPTY_ANSWER";

  public constructor(code: WitnessReviewTtyError["code"], message: string) {
    super(message);
    this.name = "WitnessReviewTtyError";
    this.code = code;
  }
}

export function assertInteractiveTty(
  stream: { readonly isTTY?: boolean } = input
): void {
  if (stream.isTTY !== true) {
    throw new WitnessReviewTtyError(
      "NON_TTY",
      "TTY-native approval requires an interactive terminal. Open the local review URL instead; FaultLine will not auto-approve under piped stdin."
    );
  }
}

/**
 * Render the humanized review inline, then require an explicit y/N confirmation
 * bound to the local git user.name. Produces the same approve+freeze records as
 * the browser atomic path.
 */
export async function runWitnessReviewTty(options: {
  readonly store: string;
  readonly proposalId: string;
  readonly draft?: IncidentDraft | null;
  readonly reviewUrlHint?: string;
  readonly approvedBy?: string;
  readonly note?: string;
  readonly approvedAt?: string;
  readonly frozenAt?: string;
  readonly stdin?: NodeJS.ReadableStream & { readonly isTTY?: boolean };
  readonly stdout?: NodeJS.WritableStream & { readonly isTTY?: boolean };
}): Promise<TtyApproveFreezeResult> {
  const stdin = options.stdin ?? input;
  const stdout = options.stdout ?? output;
  assertInteractiveTty(stdin);
  if (stdout.isTTY !== true) {
    throw new WitnessReviewTtyError(
      "NON_TTY",
      "TTY-native approval requires an interactive stdout. Open the local review URL instead."
    );
  }

  const review: WitnessReview = openWitnessReview(options.store, options.proposalId);
  const view = buildHumanizedWitnessReview({
    review,
    draft: options.draft ?? null
  });
  stdout.write(formatHumanizedReviewText(view));

  const approvedBy = (options.approvedBy ?? readGitUserName()).trim();
  if (approvedBy === "") {
    throw new WitnessReviewTtyError("EMPTY_ANSWER", "Reviewer identity is empty.");
  }

  const rl = createInterface({ input: stdin, output: stdout, terminal: true });
  try {
    const answer = (await rl.question(`Approve & freeze as "${approvedBy}"? [y/N] `)).trim().toLowerCase();
    if (answer !== "y" && answer !== "yes") {
      throw new WitnessReviewTtyError("DECLINED", "Approval declined; no records were written.");
    }
  } finally {
    rl.close();
  }

  const { approval, frozen } = approveAndFreezeWitnessReview(options.store, review, {
    reviewDigest: review.reviewDigest,
    approvedBy,
    ...(options.note === undefined ? {} : { note: options.note }),
    ...(options.approvedAt === undefined ? {} : { approvedAt: options.approvedAt }),
    ...(options.frozenAt === undefined ? {} : { frozenAt: options.frozenAt })
  });

  stdout.write(
    `Frozen ${frozen.proposal.proposalId} · approvedBy ${approval.approvedBy} · frozenDigest ${frozen.frozenDigest}\n`
  );
  return { approval, frozen, approvedBy };
}

export function nonTtyReviewRefusalMessage(reviewUrl?: string): string {
  const urlLine = reviewUrl === undefined || reviewUrl === ""
    ? "Start `fl witness review <id> --browser` on an interactive terminal and open the printed URL."
    : `Open the local review URL: ${reviewUrl}`;
  return `Refusing non-interactive approval. ${urlLine} FaultLine never auto-approves under yes| piping.`;
}
