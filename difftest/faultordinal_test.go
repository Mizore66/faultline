//go:build faultline_test

package difftest

import "strings"

// faultOrdinal retargets one fault row from the first call of its syscall
// group to a later one, per side (strace counts `when` per thread, so Go's
// ordinal can differ from TS's for the same operation). onlyCalls makes the
// run check that the injected call is the ordinal-th of the trace and that
// nothing aborted: a row that lands on a different operation (a read's close)
// fails instead of passing vacuously.
type faultOrdinal struct {
	ts, goSide int
	// onlyCalls marks a row whose traced calls are the group itself (close):
	// the injected line must be the ordinal-th line of the trace on each side.
	onlyCalls bool
	// from fails every call of the group from the ordinal on (when=N+, no
	// path filter), for a seeded mkdtemp whose failed mkdir TS retries with
	// the real mkdtemp (difftest/gen/seed.mjs; see everyCallFaultPaths). The
	// first injected line must name onPath, and TS must inject twice (the
	// seeded attempt, then the real one) and Go once.
	from   bool
	onPath string
}

// faultOrdinals is keyed by caseID/label/call. Only rows listed here leave
// the default first-call behaviour.
//
// turns-proof/turn-binding/close: investigate turns reads the binding freeze
// made twice (open, read, close each; a failed close of a read aborts Node
// with the same assertion header in Node and Go, differing only in stack
// traces, see KNOWN_DIFFERENCES) and then rewrites it (open O_WRONLY, write,
// close). The writer's close is the third close of the file on each side
// (fault-free podman traces on linux/arm64, Node 22.22.2; Go ran under
// GOMAXPROCS=1 as the fault run sets it). The check is per thread (strace
// counts when= per thread), so a different thread layout fails the ordinal
// assertion rather than passing on another operation.
var faultOrdinals = map[string]faultOrdinal{
	"turns-proof/turn-binding/close": {ts: 3, goSide: 3, onlyCalls: true},
	// turns-proof/turn-pack-verify-tmp/mkdir: the verifier's temporary bare
	// repository (src/turn-proof-bundle.ts mkdtempSync). It is neither side's
	// first mkdir: the bundle root, the stage and the investigation root come
	// first, so failing every mkdir would fail those instead. In a fault-free
	// linux/arm64 podman trace TS's main thread makes it its 19th mkdir call
	// (ts: 19, applied as when=19+ with no path filter). Go makes it by the
	// same seeded name on a thread that varies between runs, so Go is faulted
	// by path with its default ordinal (goSide: 1 is that when=1 under -P).
	"turns-proof/turn-pack-verify-tmp/mkdir": {ts: 19, goSide: 1, from: true, onPath: "tmp/faultline-turn-proof-pack-"},
}

// firstInjected is the text of the first (INJECTED) line of trace.
func firstInjected(trace string) string {
	for _, line := range strings.Split(trace, "\n") {
		if strings.Contains(line, "(INJECTED)") {
			return line
		}
	}
	return ""
}

// injectedOrdinal reports whether the first (INJECTED) line of trace is the
// n-th line of the traced group (the trace holds only that group's calls),
// and that nothing in it aborted.
func injectedOrdinal(trace string, n int) bool {
	if strings.Contains(trace, "SIGABRT") {
		return false
	}
	count := 0
	for _, line := range strings.Split(trace, "\n") {
		if !strings.Contains(line, "(") || strings.Contains(line, "---") || strings.Contains(line, "+++") {
			continue
		}
		count++
		if strings.Contains(line, "(INJECTED)") {
			return count == n
		}
	}
	return false
}
