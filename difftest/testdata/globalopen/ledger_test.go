//go:build linux

package main

import "testing"

// Pure bookkeeping tests: no ptrace, no kernel, no signals.

func TestLedgerLateEventAfterAdoptedChildReaped(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	if _, k := l.onStop(16); k != stopAdopted {
		t.Fatalf("first stop of an unannounced tid: %v, want adopted", k)
	}
	if _, known := l.onExit(16); !known {
		t.Fatal("a stopped tid must be known at exit")
	}
	if l.outstanding() != 1 { // only the root
		t.Fatalf("outstanding %d, want 1 (the root)", l.outstanding())
	}
	if out := l.onEvent(16); out != evMatchedReaped {
		t.Fatalf("late event outcome %v, want matched-reaped", out)
	}
	if _, ok := l.live[16]; ok {
		t.Fatal("a phantom live entry was created for a reaped tid")
	}
	if l.outstanding() != 1 || l.unannounced() != 0 {
		t.Fatalf("outstanding %d unannounced %d", l.outstanding(), l.unannounced())
	}
	if l.spawned != 2 || l.reaped != 1 {
		t.Fatalf("spawned %d reaped %d, want 2 and 1 (root still live)", l.spawned, l.reaped)
	}
}

func TestLedgerEventBeforeStopPromotes(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	if out := l.onEvent(20); out != evPlaceholder {
		t.Fatalf("event for an unobserved tid: %v, want placeholder", out)
	}
	if l.spawned != 1 {
		t.Fatalf("a placeholder must not count as an observed generation (spawned %d)", l.spawned)
	}
	if l.outstanding() != 2 {
		t.Fatalf("outstanding %d, want root plus the announced tid", l.outstanding())
	}
	tr, k := l.onStop(20)
	if k != stopPromoted || !tr.announced {
		t.Fatalf("stop of an announced tid: %v announced=%t", k, tr.announced)
	}
	if _, ok := l.placeholders[20]; ok {
		t.Fatal("the placeholder survived its promotion")
	}
}

func TestLedgerStopThenEventAnnouncesLive(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	l.onStop(30)
	if out := l.onEvent(30); out != evAnnouncedLive {
		t.Fatalf("event for an adopted live tid: %v", out)
	}
	if out := l.onEvent(30); out != evConflict || l.conflicts != 1 {
		t.Fatalf("a second event for the same live generation must conflict (%v, conflicts %d)", out, l.conflicts)
	}
}

func TestLedgerDeathBeforeAnyStopOrEvent(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	if _, known := l.onExit(40); known {
		t.Fatal("a tid never seen must report known=false")
	}
	if l.spawned != 2 || l.reaped != 1 || l.unannounced() != 1 {
		t.Fatalf("spawned %d reaped %d unannounced %d", l.spawned, l.reaped, l.unannounced())
	}
	if out := l.onEvent(40); out != evMatchedReaped || l.unannounced() != 0 {
		t.Fatalf("the late event must consume the death record (%v, unannounced %d)", out, l.unannounced())
	}
}

func TestLedgerEventThenDeathNeverObserved(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	l.onEvent(50)
	if _, known := l.onExit(50); !known {
		t.Fatal("an announced tid dying unobserved is known")
	}
	if l.outstanding() != 1 || l.spawned != 2 || l.reaped != 1 || l.unannounced() != 0 {
		t.Fatalf("outstanding %d spawned %d reaped %d unannounced %d", l.outstanding(), l.spawned, l.reaped, l.unannounced())
	}
}

func TestLedgerReapedAtMostOnceAndAmbiguousNumberReuse(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	l.onStop(60)
	l.onExit(60)
	l.onStop(60) // the number is reused by an unrelated, unannounced generation
	l.onExit(60)
	if l.ambiguous != 1 {
		t.Fatalf("two unannounced reaped generations of one number must be ambiguous (%d)", l.ambiguous)
	}
	if l.reaped != 2 || l.spawned != 3 {
		t.Fatalf("reaped %d spawned %d: each generation is reaped once", l.reaped, l.spawned)
	}
}

func TestLedgerConflictingPlaceholders(t *testing.T) {
	l := newLedger()
	l.addRoot(10)
	l.onEvent(70)
	if out := l.onEvent(70); out != evConflict || l.conflicts != 1 {
		t.Fatalf("two events for one unobserved tid: %v conflicts %d", out, l.conflicts)
	}
}
