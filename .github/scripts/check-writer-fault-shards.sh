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
function def(sh, n, list,   a, i, k) {
	exp_n[sh] = n
	k = split(list, a, " ")
	for (i = 1; i <= k; i++) owner[a[i]] = sh
}
BEGIN {
	def("core", 105, "propose-input policy-freeze ratify freeze ledger-bind minimize-git investigate-two-states commit-proof-preview")
	def("runtime-init", 114, "runtime-resolve-node runtime-mapping-nested-output init-yes-dirty init-standing init-cli doctor-security")
	def("incident-prevention", 147, "incident-start-command incident-continue-unapproved incident-continue-inherit-digest prevention-write-bundle-ok prevention-write-input-summary prevention-write-input-agents-append")
	def("turns", 75, "turns-proof turns-minimize")
	def("attest-repair", 111, "attest-create-git provenance-create-ok repair-brief-input-ok witness-implement-template repair-bundle-codex-ok")
	def("record-sidecar", 102, "record-init record-checkpoint codex-snapshot-clean codex-gc-force codex-gc-alternates-other-entries sidecar-hook-session-quiet sidecar-hook-full-turn")
	bad = 0
}
{
	line = $0
	sub(/^[ \t]+/, "", line)
	if (line !~ /^--- (PASS|FAIL|SKIP): TestLiveWriterFaults\//) next
	status = line; sub(/^--- /, "", status); sub(/:.*/, "", status)
	name = line; sub(/^--- [A-Z]+: /, "", name); sub(/[ \t].*/, "", name)
	n = split(name, p, "/")
	if (n < 4) next               # case or label parent, not a leaf
	cid = p[2]
	call = p[n]
	if (p[3] == "" || call !~ /^[^-]+-[A-Za-z0-9_]+$/) {
		printf "check-writer-fault-shards: malformed leaf %s in %s\n", name, FILENAME > "/dev/stderr"
		bad = 1; next
	}
	leaf = name
	if (!(cid in owner) || (shard != "total" && owner[cid] != shard)) {
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
	count[owner[cid]]++
	total++
}
END {
	if (shard == "total") {
		for (c in exp_n)
			if (count[c] != exp_n[c]) {
				printf "check-writer-fault-shards: shard %s has %d unique PASS leaves, expected %d\n", c, count[c] + 0, exp_n[c] > "/dev/stderr"
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
