#!/bin/sh
# check-skips.sh LOG ALLOWED [LIST]: fail when the `go test -v` output LOG
# shows a skip whose test name does not match the extended regular
# expression ALLOWED, or a package that ran no tests (a -run pattern that
# matches nothing exits 0). With LIST, a file of top-level test names (the
# output of `go test -list`), every one of them must report a result, so a
# renamed test cannot drop out of a -run alternation unnoticed (round 6).
# Each problem is also reported as a check-run annotation.
log=$1
allowed=$2
list=$3
fail=0
unexpected=$(grep -oE -- '--- SKIP: [^ ]+' "$log" | sed 's/^--- SKIP: //' | grep -vE "$allowed" || true)
for name in $unexpected; do echo "::error title=unexpected skip::$name"; fail=1; done
if grep -q 'no tests to run' "$log"; then
  echo "::error title=no tests ran::a package matched no test (see 'no tests to run' in the log)"
  fail=1
fi
if [ -n "$list" ]; then
  for name in $(grep -E '^Test' "$list"); do
    if ! grep -qE -- "^--- (PASS|FAIL|SKIP): $name " "$log"; then
      echo "::error title=test did not run::$name"
      fail=1
    fi
  done
fi
[ $fail = 0 ] || exit 1
echo "skips: $(grep -c -- '--- SKIP:' "$log"), all expected"
