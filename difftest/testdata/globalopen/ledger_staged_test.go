//go:build linux && ledger_staged

package main

import "testing"

// Live plus awaiting for one event is flagged ambiguous without assigning
// ownership (tag ledger_staged: pure ledger, no kernel).
func TestStagedLedgerLiveAndAwaitingSameTidIsAmbiguous(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	l.onStop(80) // generation A, adopt-first
	l.onExit(80) // A reaped, unannounced: awaiting[80] = 1
	l.onStop(80) // generation B of the same number, live, unannounced
	if l.awaiting[80] != 1 || l.live[80] == nil || l.ambiguous != 0 {
		t.Fatalf("setup invalid: awaiting=%d live=%v ambiguous=%d", l.awaiting[80], l.live[80] != nil, l.ambiguous)
	}
	liveBefore, reapedBefore := len(l.live), l.reaped
	out := l.onEvent(80)
	if l.ambiguous == 0 {
		t.Errorf("onEvent(80) with a live unannounced generation AND an awaiting reaped one returned %v and flagged nothing (ambiguous=0): the event was silently attributed", out)
	}
	if len(l.live) != liveBefore || l.reaped != reapedBefore {
		t.Errorf("the event changed ownership: live %d->%d reaped %d->%d", liveBefore, len(l.live), reapedBefore, l.reaped)
	}
}

// ---- exec retirement (pure ledger) ----
//
// Interface under test: (*ledger).retireExec(former, newTid int) (*tracee,
// execOutcome). A non-leader exec leaves the surviving process under newTid
// and gives the execing thread's former tid NO terminal wait status. The
// ledger retires exactly the generation under `former` (no onExit, no reaped,
// no terminal event) after cross-checking that both tids are live and share
// the SAME held group whose tgid is newTid. Counters: execRetired (retired),
// execRefused (unknown or mismatched, nothing changed).

func execFixture() (l *ledger, g *group) {
	l = newLedger()
	rt := l.addRoot(9) // the surviving tid (old leader's tid)
	g = &group{tgid: 9, members: 3}
	rt.group, rt.tgid = g, 9
	return
}

func adoptInto(l *ledger, tid int, g *group) *tracee {
	t, _ := l.onStop(tid)
	t.group, t.tgid = g, g.tgid
	return t
}

// A retired generation that no event ever announced still awaits its parent's
// clone event, exactly as a reaped unannounced generation does: the late event
// must be matched (no phantom placeholder, no phantom outstanding entry).
func TestStagedExecRetireLateAnnouncementIsMatchedNotPhantom(t *testing.T) {
	l, g := execFixture()
	adoptInto(l, 13, g) // adopt-first: unannounced
	other := adoptInto(l, 12, g)
	_ = other
	spawned, reaped := l.spawned, l.reaped
	f, out := l.retireExec(13, 9)
	if out != execOK || f == nil {
		t.Fatalf("retireExec(13, 9) = %v, %v; want execOK and the former generation", f, out)
	}
	if _, ok := l.live[13]; ok {
		t.Fatal("the former generation is still live after its retirement (phantom)")
	}
	if l.live[9] == nil {
		t.Fatal("the surviving identity under the new tid was removed")
	}
	if l.reaped != reaped || l.spawned != spawned {
		t.Fatalf("a retirement must not reap or spawn: spawned %d->%d reaped %d->%d", spawned, l.spawned, reaped, l.reaped)
	}
	if l.execRetired != 1 {
		t.Fatalf("execRetired = %d, want 1", l.execRetired)
	}
	if l.awaiting[13] != 1 || l.unannounced() != 1 {
		t.Fatalf("an unannounced retired generation must await its event: awaiting[13]=%d unannounced=%d", l.awaiting[13], l.unannounced())
	}
	before := l.outstanding()
	if ev := l.onEvent(13); ev != evMatchedReaped {
		t.Fatalf("the late clone event for the retired tid: %v, want matched-reaped", ev)
	}
	if len(l.placeholders) != 0 || l.outstanding() != before {
		t.Fatalf("the late event created a phantom: placeholders=%d outstanding %d->%d", len(l.placeholders), before, l.outstanding())
	}
	if l.awaiting[13] != 0 || l.unannounced() != 0 {
		t.Fatalf("the event did not consume the awaiting record: %d", l.awaiting[13])
	}
	// Close out: the survivor and the sibling reap normally; the arithmetic
	// spawned == reaped + execRetired holds once nothing is live.
	l.onExit(12)
	l.onExit(9)
	if l.outstanding() != 0 || l.spawned != l.reaped+l.execRetired {
		t.Fatalf("accounting: spawned %d != reaped %d + execRetired %d (outstanding %d)", l.spawned, l.reaped, l.execRetired, l.outstanding())
	}
}

// An already-announced former generation needs no awaiting record, and a
// second event naming it would be a plain conflict-free placeholder only if
// the kernel reused the number; here nothing may be left awaiting.
func TestStagedExecRetireAnnouncedFormerLeavesNoAwaiting(t *testing.T) {
	l, g := execFixture()
	l.onEvent(11) // placeholder
	f := adoptIntoPromoted(l, 11, g)
	if !f.announced {
		t.Fatal("setup invalid: the promoted generation must be announced")
	}
	if _, out := l.retireExec(11, 9); out != execOK {
		t.Fatalf("retireExec = %v, want execOK", out)
	}
	if l.awaiting[11] != 0 || l.unannounced() != 0 {
		t.Fatalf("an announced retired generation must not await anything: awaiting[11]=%d", l.awaiting[11])
	}
	if l.live[11] != nil || l.execRetired != 1 {
		t.Fatalf("live[11]=%v execRetired=%d", l.live[11], l.execRetired)
	}
}

func adoptIntoPromoted(l *ledger, tid int, g *group) *tracee {
	t, _ := l.onStop(tid) // promoted from the placeholder
	t.group, t.tgid = g, g.tgid
	return t
}

// Conservative controls: nothing changes and the refusal is counted.
func TestStagedExecRetireRefusesUnknownDuplicateAndMismatch(t *testing.T) {
	// unknown former
	l, g := execFixture()
	adoptInto(l, 13, g)
	if _, out := l.retireExec(77, 9); out != execRefused || l.execRetired != 0 || l.execRefused != 1 {
		t.Fatalf("unknown former: out=%v retired=%d refused=%d", out, l.execRetired, l.execRefused)
	}
	if l.live[13] == nil || l.live[9] == nil {
		t.Fatal("a refused retirement changed the live set")
	}
	// duplicate: the second retirement of the same former is unknown by then
	if _, out := l.retireExec(13, 9); out != execOK {
		t.Fatalf("first retirement: %v", out)
	}
	if _, out := l.retireExec(13, 9); out != execRefused || l.execRetired != 1 || l.execRefused != 2 {
		t.Fatalf("duplicate: out=%v retired=%d refused=%d", out, l.execRetired, l.execRefused)
	}
	// unknown survivor
	l2, g2 := execFixture()
	adoptInto(l2, 13, g2)
	if _, out := l2.retireExec(13, 55); out != execRefused || l2.live[13] == nil {
		t.Fatalf("unknown survivor: out=%v live13=%v", out, l2.live[13] != nil)
	}
	// different group pointers
	l3, g3 := execFixture()
	other := &group{tgid: 9, members: 1}
	f := adoptInto(l3, 13, g3)
	f.group = other
	if _, out := l3.retireExec(13, 9); out != execRefused || l3.live[13] == nil || l3.execRetired != 0 {
		t.Fatalf("group mismatch: out=%v", out)
	}
	// tgid of the shared group is not the new tid
	l4, g4 := execFixture()
	adoptInto(l4, 13, g4)
	g4.tgid = 99
	if _, out := l4.retireExec(13, 9); out != execRefused || l4.live[13] == nil {
		t.Fatalf("tgid mismatch: out=%v", out)
	}
	// no held group at all
	l5 := newLedger()
	l5.addRoot(9)
	l5.onStop(13)
	if _, out := l5.retireExec(13, 9); out != execRefused || l5.live[13] == nil {
		t.Fatalf("no group: out=%v", out)
	}
	// former == new is an ordinary exec and is never a retirement
	l6, _ := execFixture()
	if _, out := l6.retireExec(9, 9); out != execSame || l6.execRetired != 0 || l6.live[9] == nil {
		t.Fatalf("former == new: out=%v", out)
	}
}
