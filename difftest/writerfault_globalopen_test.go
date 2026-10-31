//go:build faultline_test && linux

package difftest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Wiring and evidence validation for the test-only GLOBALOPEN helper
// (difftest/testdata/globalopen) used by the ordinary first-open fault rows of
// TestLiveWriterFaults. The comparison itself (counts, output, exit, tree,
// argv, modes) stays in writerfault_live_test.go; this file only builds the
// helper command line and decides whether the helper's own evidence is
// complete and healthy. Evidence is labeled GLOBALOPEN and is never stock
// strace output.

// globalOpenCommand is the helper invocation: errno, the absolute path
// template (with the literal <PID> left for the helper to replace by the root
// tracee's pid), the log, then the complete writer command. No shell and no
// $$ expansion, so the helper's root tracee is the writer command itself.
func globalOpenCommand(helper, errno, absTemplate, trace string, command []string) []string {
	return append([]string{helper, "-errno", errno, "-path", absTemplate, "-log", trace, "--"}, command...)
}

// checkGlobalOpenEvidence reads the helper's raw trace for one run. The raw
// bytes are retained BEFORE validation (so a failing run keeps its evidence)
// when FAULTLINE_GLOBALOPEN_EVIDENCE_DIR names a directory. A missing,
// truncated, malformed or unhealthy trace is fatal even if the writer's own
// result matches; a nonzero writer status is NOT a tool-health failure.
func checkGlobalOpenEvidence(t *testing.T, f writerFault, tsSide bool, root string, b []byte, readErr error) {
	t.Helper()
	if readErr != nil {
		t.Fatalf("globalopen trace unreadable for %s: %v", f.name(), readErr)
	}
	if dir := os.Getenv("FAULTLINE_GLOBALOPEN_EVIDENCE_DIR"); dir != "" {
		retainGlobalOpenEvidence(t, dir, f, tsSide, root, b)
	}
	if err := validateGlobalOpenTrace(string(b), f.errno); err != nil {
		t.Fatalf("globalopen evidence invalid for %s (tsSide=%t): %v\n--- raw GLOBALOPEN trace\n%s", f.name(), tsSide, err, strings.ReplaceAll(string(b), root+"/", "<R>/"))
	}
}

// retainGlobalOpenEvidence writes the exact, unmodified trace bytes to a fresh
// <side>-*.trace in dir and a paired <same name>.meta, created exclusively,
// with the test name, case id, label, errno, root and side as quoted text. Any
// failure is fatal; nothing is overwritten. Useful as a CI artifact.
func retainGlobalOpenEvidence(t *testing.T, dir string, f writerFault, tsSide bool, root string, b []byte) {
	t.Helper()
	side := "go"
	if tsSide {
		side = "ts"
	}
	tf, err := os.CreateTemp(dir, side+"-*.trace")
	if err != nil {
		t.Fatalf("evidence trace create in %s: %v", dir, err)
	}
	if n, err := tf.Write(b); err != nil || n != len(b) {
		tf.Close()
		t.Fatalf("evidence trace write %s: n=%d/%d err=%v", tf.Name(), n, len(b), err)
	}
	if err := tf.Close(); err != nil {
		t.Fatalf("evidence trace close %s: %v", tf.Name(), err)
	}
	mf, err := os.OpenFile(tf.Name()+".meta", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("evidence metadata create for %s: %v", tf.Name(), err)
	}
	meta := fmt.Sprintf("test=%q\ncase=%q\nlabel=%q\nerrno=%q\nroot=%q\ntsSide=%t\ntrace=%q\n", t.Name(), f.caseID, f.label, f.errno, root, tsSide, filepath.Base(tf.Name()))
	if n, err := mf.WriteString(meta); err != nil || n != len(meta) {
		mf.Close()
		t.Fatalf("evidence metadata write %s: n=%d/%d err=%v", mf.Name(), n, len(meta), err)
	}
	if err := mf.Close(); err != nil {
		t.Fatalf("evidence metadata close %s: %v", mf.Name(), err)
	}
}

var globalOpenErrnoRet = map[string]string{"ENOSPC": "-28", "EIO": "-5", "EACCES": "-13", "ENOENT": "-2"}

func globalOpenFields(body string) map[string]string {
	m := map[string]string{}
	for _, field := range strings.Fields(body) {
		if k, v, ok := strings.Cut(field, "="); ok {
			m[k] = v
		}
	}
	return m
}

// validateGlobalOpenTrace requires one unambiguous, complete, healthy GLOBALOPEN
// record set with exactly one injection delivered with the expected errno. It
// does not look at the writer's exit status (a writer may validly exit
// nonzero) and it does not touch the raw (INJECTED) count that the comparator
// uses. It also does NOT require every loss counter to be zero: HEALTH ok=true
// already encodes which losses are acceptable (an after-delivery ESRCH with
// nothing pending, a resume ESRCH, each reconciled by the real terminal wait).
// The non-leader exec counters are an unsupported case and must be zero for an
// ordinary healthy trace.
func validateGlobalOpenTrace(trace, errno string) error {
	wantRet, known := globalOpenErrnoRet[errno]
	if !known {
		return fmt.Errorf("unknown errno %q", errno)
	}
	if trace == "" {
		return fmt.Errorf("empty trace")
	}
	if !strings.HasSuffix(trace, "\n") {
		return fmt.Errorf("truncated trace: no final newline")
	}
	var header, health, summary, target, fatal int
	var healthLine string
	injectedLines := 0
	var injected map[string]string
	type rec struct{ tid string }
	entries, exits := map[string]rec{}, map[string]rec{}
	for _, line := range strings.Split(strings.TrimSuffix(trace, "\n"), "\n") {
		if !strings.HasPrefix(line, "GLOBALOPEN ") {
			return fmt.Errorf("line without the GLOBALOPEN label: %.120q", line)
		}
		body := strings.TrimPrefix(line, "GLOBALOPEN ")
		switch {
		case strings.HasPrefix(body, "FATAL"):
			fatal++
		case strings.HasPrefix(body, "HEADER"):
			header++
		case strings.HasPrefix(body, "TARGET"):
			target++
		case strings.HasPrefix(body, "HEALTH"):
			health++
			healthLine = body
		case strings.HasPrefix(body, "SUMMARY"):
			summary++
		case strings.HasPrefix(body, "ENTRY "):
			m := globalOpenFields(body)
			if _, dup := entries[m["seq"]]; dup || m["seq"] == "" {
				return fmt.Errorf("duplicate or missing ENTRY seq %q: %.160s", m["seq"], body)
			}
			entries[m["seq"]] = rec{m["tid"]}
		case strings.HasPrefix(body, "EXIT "):
			m := globalOpenFields(body)
			if _, dup := exits[m["seq"]]; dup || m["seq"] == "" {
				return fmt.Errorf("duplicate or missing EXIT seq %q: %.160s", m["seq"], body)
			}
			exits[m["seq"]] = rec{m["tid"]}
			if strings.HasSuffix(body, "(INJECTED)") {
				injectedLines++
				injected = m
			}
		}
	}
	if fatal != 0 {
		return fmt.Errorf("%d FATAL record(s)", fatal)
	}
	if header != 1 || target != 1 || health != 1 || summary != 1 {
		return fmt.Errorf("record counts header/target/health/summary = %d/%d/%d/%d, want 1/1/1/1", header, target, health, summary)
	}
	m := globalOpenFields(healthLine)
	if m["ok"] != "true" {
		return fmt.Errorf("HEALTH is not ok: %s", healthLine)
	}
	if m["injected"] != "1" {
		return fmt.Errorf("HEALTH injected=%q, want exactly 1: %s", m["injected"], healthLine)
	}
	if got := strings.Count(trace, "(INJECTED)"); got != 1 || injectedLines != 1 {
		return fmt.Errorf("(INJECTED) occurs %d times (%d on EXIT lines), want exactly 1 on an EXIT line", got, injectedLines)
	}
	spawned, e1 := strconv.Atoi(m["spawned"])
	reaped, e2 := strconv.Atoi(m["reaped"])
	if e1 != nil || e2 != nil {
		return fmt.Errorf("HEALTH spawned=%q reaped=%q are not numbers: %s", m["spawned"], m["reaped"], healthLine)
	}
	// An ordinary healthy trace has no non-leader exec: both counters must be
	// present and zero (an unsupported case never reaches ok=true anyway).
	if m["exec_retired"] != "0" || m["exec_refused"] != "0" {
		return fmt.Errorf("HEALTH exec_retired=%q exec_refused=%q, want 0 and 0 for an ordinary trace: %s", m["exec_retired"], m["exec_refused"], healthLine)
	}
	if spawned != reaped {
		return fmt.Errorf("HEALTH spawned=%d reaped=%d: %s", spawned, reaped, healthLine)
	}
	// Pairing: a healthy trace pairs every ENTRY with one EXIT of the same tid.
	if len(entries) != len(exits) {
		return fmt.Errorf("%d ENTRY records but %d EXIT records", len(entries), len(exits))
	}
	for seq, e := range entries {
		x, ok := exits[seq]
		if !ok {
			return fmt.Errorf("ENTRY seq %s has no paired EXIT", seq)
		}
		if x.tid != e.tid {
			return fmt.Errorf("seq %s: EXIT tid %s != ENTRY tid %s", seq, x.tid, e.tid)
		}
	}
	// The injected EXIT: raw -ENOSYS (the suppressed call) rewritten to the
	// configured errno.
	if injected["raw"] != "-38" || injected["raw_errno"] != "ENOSYS" {
		return fmt.Errorf("injected EXIT raw=%q raw_errno=%q, want -38 ENOSYS", injected["raw"], injected["raw_errno"])
	}
	if injected["ret"] != wantRet || injected["errno"] != errno {
		return fmt.Errorf("injected EXIT ret=%q errno=%q, want %s %s", injected["ret"], injected["errno"], wantRet, errno)
	}
	return nil
}

// ---- unit tests: the validator and the command line (no oracle, no Node) ----

func goodGlobalOpenTrace() string {
	return strings.Join([]string{
		`GLOBALOPEN HEADER arch=arm64 errno=ENOSPC template="<R>/x" cmd="w" cleanup=pidfd (not stock strace)`,
		`GLOBALOPEN TARGET root_pid=9 path="<R>/x"`,
		`GLOBALOPEN ENTRY seq=1 tid=9 syscall=openat dirfd=-100 raw="<R>/x" flags=0x80241:O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC mode=0600 resolved="<R>/x" decision=inject`,
		`GLOBALOPEN EXIT seq=1 tid=9 raw=-38 raw_errno=ENOSYS ret=-28 errno=ENOSPC injected=true (INJECTED)`,
		`GLOBALOPEN ENTRY seq=2 tid=9 syscall=openat dirfd=-100 raw="<R>/x" flags=0x80000:O_RDONLY|O_CLOEXEC mode=00 resolved="<R>/x" decision=pass`,
		`GLOBALOPEN EXIT seq=2 tid=9 ret=3 errno=OK injected=false`,
		`GLOBALOPEN HEALTH ok=true spawned=2 reaped=2 candidates=2 injected=1 injected_started=1 injection_lost=0 passed=1 exec_retired=0 exec_refused=0`,
		`GLOBALOPEN SUMMARY root_status=1 root_signaled=false`,
		``,
	}, "\n")
}

func TestValidateGlobalOpenTrace(t *testing.T) {
	good := goodGlobalOpenTrace()
	if err := validateGlobalOpenTrace(good, "ENOSPC"); err != nil {
		t.Fatalf("a healthy trace was rejected: %v", err)
	}
	// A writer's own nonzero status (root_status=1 above) is not a health failure.
	rep := func(old, new string) string { return strings.Replace(good, old, new, 1) }
	for name, tc := range map[string]struct{ trace, errno, want string }{
		"empty":                {"", "ENOSPC", "empty trace"},
		"truncated":            {strings.TrimSuffix(good, "\n"), "ENOSPC", "truncated"},
		"unknown errno":        {good, "EBUSY", "unknown errno"},
		"unlabeled line":       {good + "stray\n", "ENOSPC", "without the GLOBALOPEN label"},
		"fatal":                {good + "GLOBALOPEN FATAL boom\n", "ENOSPC", "FATAL"},
		"two healths":          {rep("GLOBALOPEN SUMMARY", "GLOBALOPEN HEALTH ok=true spawned=2 reaped=2 injected=1 exec_retired=0 exec_refused=0\nGLOBALOPEN SUMMARY"), "ENOSPC", "record counts"},
		"missing summary":      {rep("GLOBALOPEN SUMMARY root_status=1 root_signaled=false\n", ""), "ENOSPC", "record counts"},
		"unhealthy":            {rep("HEALTH ok=true", "HEALTH ok=false"), "ENOSPC", "not ok"},
		"zero injected":        {rep("injected=1 injected_started", "injected=0 injected_started"), "ENOSPC", "injected="},
		"second marker":        {rep("errno=OK injected=false", "errno=OK injected=true (INJECTED)"), "ENOSPC", "(INJECTED) occurs 2"},
		"marker in a path":     {rep(`raw="<R>/x" flags=0x80000`, `raw="<R>/m(INJECTED)" flags=0x80000`), "ENOSPC", "(INJECTED) occurs 2"},
		"spawned != reaped":    {rep("spawned=2 reaped=2", "spawned=3 reaped=2"), "ENOSPC", "spawned=3 reaped=2"},
		"non-numeric reaped":   {rep("reaped=2", "reaped=x"), "ENOSPC", "not numbers"},
		"exec_retired":         {rep("exec_retired=0", "exec_retired=1"), "ENOSPC", "exec_retired"},
		"exec_refused":         {rep("exec_refused=0", "exec_refused=1"), "ENOSPC", "exec_refused"},
		"exec counters absent": {rep(" exec_retired=0 exec_refused=0", ""), "ENOSPC", "exec_retired"},
		"entry without exit":   {rep("GLOBALOPEN EXIT seq=2 tid=9 ret=3 errno=OK injected=false\n", ""), "ENOSPC", "ENTRY records but"},
		"exit tid mismatch":    {rep("EXIT seq=2 tid=9", "EXIT seq=2 tid=10"), "ENOSPC", "EXIT tid 10 != ENTRY tid 9"},
		"duplicate entry seq":  {rep("ENTRY seq=2", "ENTRY seq=1"), "ENOSPC", "duplicate"},
		"raw not ENOSYS":       {rep("raw=-38 raw_errno=ENOSYS", "raw=-1 raw_errno=ENOSYS"), "ENOSPC", "raw="},
		"wrong rewritten ret":  {rep("ret=-28 errno=ENOSPC injected=true", "ret=-5 errno=ENOSPC injected=true"), "ENOSPC", "injected EXIT ret="},
		"wrong errno name":     {rep("errno=ENOSPC injected=true", "errno=EIO injected=true"), "ENOSPC", "injected EXIT ret="},
		"errno mismatch":       {good, "EIO", "injected EXIT ret="},
	} {
		err := validateGlobalOpenTrace(tc.trace, tc.errno)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// An after-delivery lost stop with nothing pending, or a resume ESRCH, is
// already inside HEALTH ok=true (reconciled by the real terminal wait): the
// validator must NOT require every loss counter to be zero.
func TestValidateGlobalOpenTraceAcceptsReconciledNonPendingLoss(t *testing.T) {
	trace := strings.Replace(goodGlobalOpenTrace(), "exec_retired=0 exec_refused=0", "lost_stops=1 lost_after_injection=1 resume_lost=1 reconciled=2 exec_retired=0 exec_refused=0", 1)
	if err := validateGlobalOpenTrace(trace, "ENOSPC"); err != nil {
		t.Fatalf("a reconciled non-pending loss under ok=true was rejected: %v", err)
	}
}

func TestGlobalOpenCommand(t *testing.T) {
	got := globalOpenCommand("/h/globalopen", "EIO", "/r/ws/.git/x.<PID>.tmp", "/t/trace", []string{"/b/fl", "arg one", "--flag"})
	want := []string{"/h/globalopen", "-errno", "EIO", "-path", "/r/ws/.git/x.<PID>.tmp", "-log", "/t/trace", "--", "/b/fl", "arg one", "--flag"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	for _, a := range got {
		if a == "sh" || a == "-c" || strings.Contains(a, "$$") {
			t.Errorf("the helper command must have no shell and no $$ expansion: %q", got)
		}
	}
}

// Only the ordinary first-call open rows use the helper; the matrix size and
// the rows that stay on stock strace are pinned so a table change is deliberate.
func TestGlobalOpenRowClassification(t *testing.T) {
	var helper, stock, pidRows int
	seenPid := map[string]bool{}
	for _, f := range writerFaultMatrix() {
		o := faultOrdinals[f.caseID+"/"+f.label+"/"+f.call]
		if f.call == "open" && !f.everyCall {
			helper++
			if o.ts > 1 || o.goSide > 1 || o.from || o.onlyCalls {
				t.Errorf("%s is an ordinary open row but has an ordinal override %+v", f.name(), o)
			}
			if (strings.Contains(f.glob, "<PID>") || f.stageOf != "") && !seenPid[f.caseID+"/"+f.label] {
				seenPid[f.caseID+"/"+f.label] = true
				pidRows++
			}
		} else {
			stock++
		}
	}
	if helper != 153 {
		t.Errorf("%d ordinary-open leaves, want 153 (51 rows x 3 errnos)", helper)
	}
	if pidRows != 5 {
		t.Errorf("%d <PID> rows among the ordinary-open rows, want 5: %v", pidRows, seenPid)
	}
	if helper+stock != len(writerFaultMatrix()) || stock == 0 {
		t.Errorf("helper %d + stock %d != matrix %d", helper, stock, len(writerFaultMatrix()))
	}
}
