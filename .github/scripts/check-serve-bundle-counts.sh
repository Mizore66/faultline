#!/bin/sh
# check-serve-bundle-counts.sh LOG: the serve-bundle live log must show every one
# of the 68 original CLI cases (46 refusals + 22 servers) of TestLiveServeBundle
# and the 5 supplemental cases of TestLiveServeBundleBinding passing, each exactly
# once and none failed or skipped, so a green top-level result cannot hide a
# dropped subtest. The ids are counted from the log, not from the corpus; the
# Go-side audit (TestServeBundleEvidenceAudit) separately binds them to the corpus.
log=$1
[ -n "$log" ] || { echo "usage: $0 LOG" >&2; exit 2; }
tr -d '\r' < "$log" | awk '
function note(msg) { print "::error title=serve-bundle counts::" msg; bad = 1 }
/^ *--- (PASS|FAIL|SKIP): TestLiveServeBundle\/cli-[a-z0-9-]+ / {
  verdict = $2; sub(/:$/, "", verdict); id = $3
  if (verdict != "PASS") note("original case did not pass: " id " (" verdict ")")
  else if (id in orig) note("original case reported twice: " id)
  else { orig[id] = 1; no++ }
}
/^ *--- (PASS|FAIL|SKIP): TestLiveServeBundleBinding\/binding-[a-z-]+ / {
  verdict = $2; sub(/:$/, "", verdict); id = $3
  if (verdict != "PASS") note("supplemental case did not pass: " id " (" verdict ")")
  else if (id in supp) note("supplemental case reported twice: " id)
  else { supp[id] = 1; ns++ }
}
END {
  if (no != 68) note("original TestLiveServeBundle cases passed: " no + 0 ", want 68")
  if (ns != 5) note("supplemental TestLiveServeBundleBinding cases passed: " ns + 0 ", want 5")
  if (bad) exit 1
  print "serve-bundle live counts: 68 original and 5 supplemental cases passed"
}'
