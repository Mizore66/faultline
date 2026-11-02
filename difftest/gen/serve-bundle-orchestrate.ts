// Import-safe orchestration of per-case capture. The default capture function is
// the actual oracle capture; the parameter exists only so a harness test can
// drive this loop with a cheap stand-in. Production behavior is unchanged.
// Evidence retention outranks cleanup: every capture's scratch directory is
// preserved and its path logged.
import type { ServeBundleCase } from "./serve-bundle-cases.js";
import { captureServeBundleCaseDetailed, type CapturedCase } from "./serve-bundle-capture.js";

export type Rendered = { goldenLine: string; page: Buffer | null; pageName: string | null; scratch: string };
export type CaptureFunction = (root: string, row: ServeBundleCase) => Promise<CapturedCase>;

export async function captureAll(
  root: string,
  rows: readonly ServeBundleCase[],
  capture: CaptureFunction = (r, row) => captureServeBundleCaseDetailed(r, row)
): Promise<Rendered[]> {
  const out: Rendered[] = [];
  for (const row of rows) {
    const captured = await capture(root, row);
    out.push({ goldenLine: JSON.stringify(captured.golden), page: captured.pageBytes, pageName: captured.golden.page, scratch: captured.scratch });
    console.log(`captured ${row.id} (scratch preserved at ${captured.scratch})`);
  }
  return out;
}
