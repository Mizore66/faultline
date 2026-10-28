#!/bin/sh
# check-crossverify.sh LOG: fail unless the `go test -v` output LOG holds the
# TestWriterReplay summary (difftest/writer_test.go liveSummary) with a
# POSITIVE count of Go bundles verified by TS. A summary that says 0 (the
# oracle off, or no bundle written) or no summary at all (the test did not run
# to its deferred summary) fails: test names alone prove nothing.
log=$1
line=$(grep -E 'LIVE-SUMMARY TestWriterReplay: .* [0-9]+ Go bundles verified by TS' "$log" | tail -1)
if [ -z "$line" ]; then
  echo "::error title=no cross-verify summary::TestWriterReplay printed no 'Go bundles verified by TS' summary"
  exit 1
fi
count=$(printf '%s\n' "$line" | sed -E 's/.* ([0-9]+) Go bundles verified by TS.*/\1/')
if [ "$count" -le 0 ]; then
  echo "::error title=no cross-verification::TestWriterReplay verified $count Go bundles under TS (the oracle must be on)"
  exit 1
fi
echo "::notice title=cross-verify::$count Go bundles verified by TS"
