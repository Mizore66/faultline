#!/bin/sh
# Exercises check-crossverify.sh on SYNTHETIC logs (not hosted-CI output):
# a positive summary passes; a zero summary, an absent summary and a summary
# of another test fail.
here=$(cd "$(dirname "$0")" && pwd)
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
status=0
expect() { # name want(0|1) log text
  printf '%s\n' "$4" > "$dir/log"
  if "$here/check-crossverify.sh" "$dir/log" > /dev/null; then got=0; else got=1; fi
  if [ "$got" != "$2" ]; then echo "FAIL $1: exit $got, want $2"; status=1; else echo "ok $1"; fi
}
expect positive 0 x "    writer_test.go:1521: LIVE-SUMMARY TestWriterReplay: 330 of 330 writer cases run (329 golden invocations), 120 Go bundles verified by TS"
expect zero 1 x "    writer_test.go:1521: LIVE-SUMMARY TestWriterReplay: 330 of 330 writer cases run (329 golden invocations), 0 Go bundles verified by TS"
expect absent 1 x "--- PASS: TestWriterReplay (10.00s)"
expect other-test 1 x "    x_test.go:1: LIVE-SUMMARY TestLiveOther: 5 Go bundles verified by TS"
exit $status
