#!/bin/sh
# Usage: ci_scope.sh EVENT BASE HEAD [WORKDIR]
# Prints "scope=code|docs-only" then "path=<file>" lines. Fails closed to code.
EVENT=${1-}
BASE=${2-}
HEAD_REV=${3-}
WORKDIR=${4-.}

code() {
	printf 'scope=code\n'
	if [ -n "${PATHS_FILE-}" ] && [ -s "$PATHS_FILE" ]; then
		tr '\0' '\n' <"$PATHS_FILE" | sort | sed 's/^/path=/'
	fi
	exit 0
}

TMPDIR_=$(mktemp -d 2>/dev/null) || code
trap 'rm -rf "$TMPDIR_"' EXIT
PATHS_FILE=$TMPDIR_/paths

cd "$WORKDIR" 2>/dev/null || code

case "$EVENT" in
pull_request) RANGE_OP='...' ;;
push) RANGE_OP='..' ;;
*) code ;;
esac

case "$BASE" in
'' | -* | *[!0-9a-fA-F]*) code ;;
esac
case "$BASE" in
*[!0]*) ;;
*) code ;;
esac
case "$HEAD_REV" in
'' | -*) code ;;
esac

git rev-parse --verify --quiet "$BASE^{commit}" >/dev/null 2>&1 || code
git rev-parse --verify --quiet "$HEAD_REV^{commit}" >/dev/null 2>&1 || code

RANGE="$BASE$RANGE_OP$HEAD_REV"
git -c core.quotepath=off diff --no-renames --name-only -z "$RANGE" -- >"$PATHS_FILE" 2>/dev/null || code
# Newline-bearing paths cannot be listed safely; treat as code.
[ -s "$PATHS_FILE" ] || code
tr '\0' '\n' <"$PATHS_FILE" | grep -c '' >"$TMPDIR_/count"
NUL=$(tr -cd '\0' <"$PATHS_FILE" | wc -c | tr -d ' ')
LINES=$(cat "$TMPDIR_/count")
[ "$NUL" = "$LINES" ] || code

# Docs-only is exactly docs/superpowers/**: other docs, README.md, LICENSE,
# NOTICE and CHANGELOG.md can be read by tests, so they are code.
DOCS=1
while IFS= read -r p; do
	case "$p" in
	docs/superpowers/?*) ;;
	*) DOCS=0 ;;
	esac
done <<EOT
$(tr '\0' '\n' <"$PATHS_FILE")
EOT
[ "$DOCS" = 1 ] || code

git diff --check "$RANGE" -- >/dev/null 2>&1 || code

printf 'scope=docs-only\n'
tr '\0' '\n' <"$PATHS_FILE" | sort | sed 's/^/path=/'
