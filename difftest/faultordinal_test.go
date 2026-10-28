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
// with a native assertion Go cannot reproduce, see KNOWN_DIFFERENCES) and
// then rewrites it (open O_WRONLY, write, close). The writer's close is the
// third close of the file on each side (TS: one thread; Go under GOMAXPROCS=1
// as the fault run sets it). Measured by the fault-free traces retained in the
// follow-up report.
var faultOrdinals = map[string]faultOrdinal{
	"turns-proof/turn-binding/close": {ts: 3, goSide: 3, onlyCalls: true},
	// turns-proof/turn-pack-verify-tmp/mkdir: the verifier's temporary bare
	// repository (src/turn-proof-bundle.ts mkdtempSync). It is neither side's
	// first mkdir: the bundle root, the stage and the investigation root come
	// first, so failing every mkdir would fail those instead. Measured in a
	// fault-free podman trace (retained in the follow-up report): TS's main
	// thread makes it its 19th mkdir call. Go makes it by the same seeded name on
	// a thread that varies between runs (strace counts when= per thread), so
	// Go is faulted by path (goSide is the call's ordinal in that thread of
	// the fault-free trace, kept for the record).
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
