# Shared body of every fake gh (README.md), sourced by <variant>/gh after it
# sets variant=. The writer replay puts <variant>/ on PATH after the
# recorders (difftest/gen/scenarios.ts writerEnv, difftest/writer_test.go
# writerEnv). gh inherits fl's whole environment, so the record goes to
# $FAULTLINE_ARGV_LOG: "gh " + the arguments joined by \037, terminated by
# \036.
log=$FAULTLINE_ARGV_LOG
if [ -n "$log" ]; then
  record="gh "
  sep=
  for arg in "$@"; do record="$record$sep$arg"; sep=$(printf '\037'); done
  printf '%s\036' "$record" >> "$log"
fi
# rep prints $1 repeated $2 times.
rep() {
  s=$1 n=$2 r=
  while [ "$n" -gt 0 ]; do
    if [ $((n % 2)) = 1 ]; then r=$r$s; fi
    s=$s$s n=$((n / 2))
  done
  printf '%s' "$r"
}
if [ "$1 $2" = "attestation verify" ] && [ $# -ge 3 ]; then
  case "$variant" in
  pass)
    digest=$( (sha256sum "$3" 2>/dev/null || shasum -a 256 "$3" 2>/dev/null || /usr/bin/shasum -a 256 "$3") | { read -r hex _; printf %s "$hex"; })
    printf 'Loaded digest sha256:%s for file://%s\n' "$digest" "$3"
    exit 0
    ;;
  fail)
    printf 'Error: verifying with issuer "sigstore.dev"\r\n' >&2
    exit 1
    ;;
  flood)
    rep x 1200000
    exit 0
    ;;
  long-fail)
    # stdout: 4,500 é (9,000 bytes, 4,500 UTF-16 units). stderr: 3,498 x,
    # one U+1F600 (4 bytes, 2 units), then 5,498 y (9,000 bytes). fl joins
    # them as stdout + "\n" + stderr and keeps 8,000 units, which ends
    # between the two halves of U+1F600.
    rep 'é' 4500
    { rep x 3498; printf '\360\237\230\200'; rep y 5498; } >&2
    exit 1
    ;;
  esac
fi
printf 'fake gh (%s): unsupported command: %s\n' "$variant" "$*" >&2
exit 125
