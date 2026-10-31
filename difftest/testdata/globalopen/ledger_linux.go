//go:build linux

package main

// The generation ledger: pure bookkeeping, no ptrace and no signals, so its
// rules are unit-testable without a kernel (ledger_test.go, ledger_staged_test.go).
//
// Ownership rule: a tracee is OWNED only because the kernel handed this
// process a wait status (a stop or a death) for its tid through wait4(-1). A
// parent's clone/fork/vfork event only ANNOUNCES a tid; it never creates a
// live owned tracee, never gives a tid a signal target, and never proves that
// the tid's first stop comes after it (the adopt-first order is real and
// routine). Events are reconciled against the generations the waits showed.
//
// Generations. Every tid first observed through a wait status starts a new
// generation (gen id from a monotonic counter). A generation is reaped at most
// once: onExit removes it from `live` and counts it. A reaped generation whose
// announcing event has not yet been seen is remembered in `awaiting` (a
// per-tid-number count), so the late event is consumed by it instead of
// creating a phantom live entry. `awaiting` is not a permanent "gone" set:
// each entry is consumed by exactly one event and an entry left over at the
// end is reported as `unannounced`, informational only.
//
// When tid number N has a LIVE unannounced generation AND an older reaped
// unannounced generation still awaiting its event, one event naming N could
// belong to either. onEvent does not guess: it counts `ambiguous` (HEALTH
// false), returns evAmbiguous and changes NOTHING.
//
// A non-leader execve gives the execing thread the old leader's tid and no
// terminal wait status exists for its former tid: retireExec retires exactly
// that generation without onExit (an UNSUPPORTED case that keeps HEALTH false).

type tracee struct {
	gen       int
	announced bool // an event named this generation (or it was announced first)
	seenStop  bool // the initial SIGSTOP of a cloned tracee was consumed
	pend      *pending
	lost      bool // a request failed ESRCH or a stop was abandoned; terminal wait expected
	tgid      int
	group     *group
}

type ledger struct {
	live         map[int]*tracee // generations observed through waits and not yet reaped
	placeholders map[int]bool    // tids announced by an event, never yet observed (not owned)
	awaiting     map[int]int     // reaped, unannounced generations per tid number
	nextGen      int
	spawned      int // generations observed (root, adopted, promoted, or seen only by death)
	reaped       int // generations whose terminal status was consumed (at most once each)
	conflicts    int // an event contradicting a live announced generation, or a duplicate placeholder
	ambiguous    int // two unannounced reaped generations of one number, or live+awaiting for one event
	execRetired  int // generations retired by a non-leader exec (no terminal wait exists for them)
	execRefused  int // exec reconciliations refused conservatively (nothing changed)
}

func newLedger() *ledger {
	return &ledger{live: map[int]*tracee{}, placeholders: map[int]bool{}, awaiting: map[int]int{}}
}

func (l *ledger) newGen(announced bool) *tracee {
	l.nextGen++
	l.spawned++
	return &tracee{gen: l.nextGen, announced: announced}
}

// addRoot registers the process this tracer itself started. It is owned from
// the start (a direct, unreaped child).
func (l *ledger) addRoot(pid int) *tracee {
	t := l.newGen(true)
	t.seenStop = true
	l.live[pid] = t
	return t
}

type stopKind int

const (
	stopKnown    stopKind = iota // a live generation
	stopPromoted                 // an announced placeholder, now observed
	stopAdopted                  // observed before any event named it
)

// onStop records a real stop of tid.
func (l *ledger) onStop(tid int) (*tracee, stopKind) {
	if t := l.live[tid]; t != nil {
		return t, stopKnown
	}
	if l.placeholders[tid] {
		delete(l.placeholders, tid)
		t := l.newGen(true)
		l.live[tid] = t
		return t, stopPromoted
	}
	t := l.newGen(false)
	l.live[tid] = t
	return t, stopAdopted
}

// onExit records a real terminal status of tid. known is false only when the
// tid was never seen before: it died before any stop and before any event.
func (l *ledger) onExit(tid int) (t *tracee, known bool) {
	known = true
	switch {
	case l.live[tid] != nil:
		t = l.live[tid]
		delete(l.live, tid)
	case l.placeholders[tid]:
		delete(l.placeholders, tid)
		t = l.newGen(true)
	default:
		t = l.newGen(false)
		known = false
	}
	l.reaped++
	if !t.announced {
		l.awaiting[tid]++
		if l.awaiting[tid] > 1 {
			l.ambiguous++
		}
	}
	return t, known
}

type evOutcome int

const (
	evAnnouncedLive evOutcome = iota // announced a live, adopted generation
	evMatchedReaped                  // matched a reaped, unannounced generation: no phantom
	evPlaceholder                    // not yet observed: remembered, NOT owned
	evConflict                       // contradicts what the waits showed
	evAmbiguous                      // could belong to a live OR a reaped generation: nothing assigned
)

func (o evOutcome) String() string {
	switch o {
	case evAnnouncedLive:
		return "announced-live"
	case evMatchedReaped:
		return "matched-reaped"
	case evPlaceholder:
		return "placeholder"
	case evAmbiguous:
		return "ambiguous"
	}
	return "conflict"
}

// onEvent records a parent's clone/fork/vfork event naming child.
func (l *ledger) onEvent(child int) evOutcome {
	if t := l.live[child]; t != nil {
		if t.announced {
			l.conflicts++
			return evConflict
		}
		if l.awaiting[child] > 0 {
			l.ambiguous++ // live unannounced AND an older reaped one await: do not guess
			return evAmbiguous
		}
		t.announced = true
		return evAnnouncedLive
	}
	if n := l.awaiting[child]; n > 0 {
		if n == 1 {
			delete(l.awaiting, child)
		} else {
			l.awaiting[child] = n - 1
		}
		return evMatchedReaped
	}
	if l.placeholders[child] {
		l.conflicts++
		return evConflict
	}
	l.placeholders[child] = true
	return evPlaceholder
}

type execOutcome int

const (
	execOK      execOutcome = iota // the former generation was retired
	execSame                       // former == new: an ordinary (leader) exec, nothing retires
	execRefused                    // unknown tid, group mismatch or inconsistent members: nothing changed
)

func (o execOutcome) String() string {
	switch o {
	case execOK:
		return "retired"
	case execSame:
		return "same"
	}
	return "refused"
}

// retireExec reconciles a NON-LEADER execve. The kernel gives the execing
// thread the old leader's tid (newTid, where the exec event stop is reported)
// and delivers NO terminal wait status for the thread's former tid, so the
// generation under `former` is retired here, once, without onExit: no reaped++,
// no terminal event, no fabricated wait. The generation under newTid stays as
// the single surviving identity (its own pending state is the caller's to
// account: it was the old leader's task).
//
// Cross-check before any change (conservative; execRefused counts a refusal
// and changes nothing): both tids are live; both hold the SAME group pointer;
// that group's tgid is newTid; the group has at least two members (the former
// and the survivor). A retired generation that no event ever announced
// awaits its parent's clone event exactly like a reaped one (awaiting), so a
// late event is matched instead of becoming a phantom placeholder. The group
// loses ONE member and is never closed here (the survivor still holds it).
func (l *ledger) retireExec(former, newTid int) (*tracee, execOutcome) {
	if former == newTid {
		return l.live[newTid], execSame
	}
	f, s := l.live[former], l.live[newTid]
	if f == nil || s == nil || f.group == nil || f.group != s.group || f.group.tgid != newTid || f.group.members < 2 {
		l.execRefused++
		return nil, execRefused
	}
	delete(l.live, former)
	f.group.members--
	l.execRetired++
	if !f.announced {
		l.awaiting[former]++
		if l.awaiting[former] > 1 {
			l.ambiguous++
		}
	}
	return f, execOK
}

// outstanding is what the main loop must keep waiting for: live generations
// and announced tids not yet observed. A wait failure (ECHILD) while this is
// nonzero is a loud failure.
func (l *ledger) outstanding() int { return len(l.live) + len(l.placeholders) }

// unannounced counts reaped generations whose announcing event never came.
func (l *ledger) unannounced() int {
	n := 0
	for _, c := range l.awaiting {
		n += c
	}
	return n
}
