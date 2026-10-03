#!/bin/sh
# check-skips.sh LOG ALLOWED: fail when a test in the `go test -v` output LOG
# skipped and its name does not match the extended regular expression
# ALLOWED. Each unexpected skip is also reported as a check-run annotation.
log=$1
allowed=$2
unexpected=$(grep -oE -- '--- SKIP: [^ ]+' "$log" | sed 's/^--- SKIP: //' | grep -vE "$allowed" || true)
if [ -n "$unexpected" ]; then
  for name in $unexpected; do echo "::error title=unexpected skip::$name"; done
  exit 1
fi
echo "skips: $(grep -c -- '--- SKIP:' "$log"), all expected"
