#!/bin/sh
# Validate TestLiveWriterFaults leaf terminal results per shard or in total.
# Usage: check-writer-fault-shards.sh SHARD EXPECTED LOG [LOG...]
# Only "--- PASS|FAIL|SKIP: TestLiveWriterFaults/<case>/<label>/<call>-<errno>"
# lines count. Parent results and printed len(matrix) summaries are ignored.
set -eu

if [ "$#" -lt 3 ]; then
	echo "usage: $0 SHARD EXPECTED LOG [LOG...]" >&2
	exit 2
fi
shard=$1
expected=$2
shift 2

case "$shard" in
core) want=105 ;;
runtime-init) want=114 ;;
incident-prevention) want=147 ;;
turns) want=75 ;;
attest-repair) want=111 ;;
record-sidecar) want=102 ;;
total) want=654 ;;
*)
	echo "check-writer-fault-shards: unknown shard '$shard'" >&2
	exit 2
	;;
esac
if [ "$expected" != "$want" ]; then
	echo "check-writer-fault-shards: expected count $expected does not match required $want for shard $shard" >&2
	exit 1
fi
for f in "$@"; do
	if [ ! -r "$f" ]; then
		echo "check-writer-fault-shards: cannot read log $f" >&2
		exit 2
	fi
done

awk -v shard="$shard" -v want="$want" '
BEGIN {
	exp_n["core"] = 105; exp_n["runtime-init"] = 114
	exp_n["incident-prevention"] = 147; exp_n["turns"] = 75
	exp_n["attest-repair"] = 111; exp_n["record-sidecar"] = 102
	bad = 0
}
{
	line = $0
	sub(/^[ \t]+/, "", line)
	if (line !~ /^--- (PASS|FAIL|SKIP): TestLiveWriterFaults\//) next
	status = line; sub(/^--- /, "", status); sub(/:.*/, "", status)
	name = line; sub(/^--- [A-Z]+: /, "", name); sub(/[ \t].*/, "", name)
	n = split(name, p, "/")
	if (n != 4) next              # parent (case or label level), not a leaf
	if (p[4] !~ /^[^\/]+-[A-Za-z0-9_]+$/) next
	leaf = name
	cid = p[2]
	if (!(cid in exp_n) || (shard != "total" && cid != shard)) {
		printf "check-writer-fault-shards: foreign caseID %s in %s (%s) [%s]\n", cid, FILENAME, shard, leaf > "/dev/stderr"
		bad = 1; next
	}
	if (status != "PASS") {
		printf "check-writer-fault-shards: non-PASS leaf %s: %s in %s\n", status, leaf, FILENAME > "/dev/stderr"
		bad = 1; next
	}
	if (leaf in seen) {
		printf "check-writer-fault-shards: duplicate leaf %s (in %s, first in %s)\n", leaf, FILENAME, seen[leaf] > "/dev/stderr"
		bad = 1; next
	}
	seen[leaf] = FILENAME
	count[cid]++
	total++
}
END {
	if (shard == "total") {
		for (c in exp_n)
			if (count[c] != exp_n[c]) {
				printf "check-writer-fault-shards: case %s has %d unique PASS leaves, expected %d\n", c, count[c] + 0, exp_n[c] > "/dev/stderr"
				bad = 1
			}
	}
	if (total != want) {
		printf "check-writer-fault-shards: %s has %d unique PASS leaves, expected %d\n", shard, total + 0, want > "/dev/stderr"
		bad = 1
	}
	if (bad) exit 1
	printf "check-writer-fault-shards: %s ok (%d leaves)\n", shard, total
}
' "$@"
