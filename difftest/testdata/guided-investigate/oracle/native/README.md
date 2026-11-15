# Native Windows guided-investigate oracle corpora

`windows-amd64/` and `windows-arm64/` each hold exactly 31 files: `baseline.json`, `cases.json` and 29 `captures/<case>.json`. They are the default corpora the Go replay loads on native Windows (`guidedCorpusDir`); there is no POSIX fallback.

## Provenance

- Workflow run: https://github.com/Mizore66/faultline/actions/runs/43719746283 (attempt 1), source head e04908b2459ed0ea41f22eb527e1e1216686d6c2, workflow `guided-oracle-capture.yml`.
- Captured by the frozen TypeScript CLI, commit ce99a793af0b020bf1b9cf21114f8d7897b37207, with Node 22.22.2 on the native runner, using `difftest/gen/gen-guided-investigate.mjs --native-capture` at two different roots, compared equal. Expected values come from the Node capture only; nothing was derived by Go.
- windows-amd64: corpus artifact 14164062133 (ZIP sha256 5306f9f271abaa8d4effb37a7ff8fb5a91ab92af422438bde0e6267a339ebde2), receipts artifact 14164171754 (sha256 6da5e7178829622e18576cac34f7da7261cf2882b726a434551c06a274084bc5).
- windows-arm64: corpus artifact 14163774893 (ZIP sha256 e984da3d9ebb2e3d4f3c6d3c7ae7cccda026ce45502b28242946b7a4e1229b94), receipts artifact 14164615922 (sha256 5df22f7b6bb80a0aa2805851b3fc86c6554742fe67b7c16d2e6995f368354ba0).
- Files are byte-identical to the extracted artifacts and match each receipt's `corpus-sha256sums.txt`.
- Qualification in that run, both architectures: 12 Go replay/control tests, 29 cases / 75 steps, 3 shutdown regressions, 4 guided CLI tests, no FAIL or SKIP; all 10 step outcomes success. Those replays used `FAULTLINE_GUIDED_CORPUS_DIR` pointing at the fresh capture; replaying these committed files by default (override variables unset) is required by the workflow's second replay but was still pending at import time (historical; it has since passed, see Current qualification below): it had no hosted result until an actual run on both Windows targets passed after this import is published.

## Scope

- Stored captures are byte-identical between the two architectures. The `baseline.json` files differ only in the `platform` block (`goarch`, `nodeArch`). Each architecture was still captured and replayed by its own real native execution; both executions are independent evidence.
- These are Windows fakebox/scripted parity fixtures. They are not native Docker proof, a release gate, TypeScript retirement, or the full 5g gates.
- `.gitattributes` marks `difftest/testdata/**` as `-text`, so the bytes are kept as is (LF, no CRLF).

## Refresh

Do not edit these files by hand or derive expected values with Go. To refresh: run two fresh native Node captures at different roots, compare them, run the native Go qualification (including the default-path replay) on both architectures, review the data and provenance, then replace the files together with a new provenance entry. Keep the earlier receipts.

## Current default qualification (2026-11-15)

Actual committed-default qualification [run 43726508255](https://github.com/Mizore66/faultline/actions/runs/43726508255) at `3cc648f9b0947ceb87cec4ef1799926f61dbbd51` succeeded on both native Windows architectures. Per architecture, the fresh-capture override replay and the committed-default replay (corpus/evidence overrides unset, real `TEMP`/`TMP` unchanged) each passed 14 named Go replay/control tests with all 29 cases / 75 steps passing exactly once and 0 FAIL/SKIP; the fresh 31-file data equals the committed files above. The earlier 12-test count in the e049 provenance is historical and not rewritten. Still Windows fakebox parity, not native Docker proof; see `difftest/KNOWN_DIFFERENCES.md`.
