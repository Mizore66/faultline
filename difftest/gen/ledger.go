package gen

import (
	"math/rand/v2"
	"slices"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// MutateLedgerEvents makes one structural change to a lifecycle ledger's
// event list, the kind the lifecycle state machine checks: a duplicated,
// moved, dropped or re-typed event, a retargeted turn id or ordinal, an
// occurredAt earlier than the snapshot or checkpoint it carries, or a wrong
// completedTurns count. Sequence numbers are renumbered, so after re-signing
// the chain the change reaches the semantic checks.
func MutateLedgerEvents(r *rand.Rand, text string) string {
	root, err := jsjson.Parse(text)
	if err != nil || root.Kind() != jsjson.Object {
		return text
	}
	events := slices.Clone(root.Get("events").Items())
	if len(events) < 2 {
		return text
	}
	clone := func(v jsjson.Value) jsjson.Value {
		c, _ := jsjson.Parse(jsjson.Stringify(v))
		return c
	}
	i, j := r.IntN(len(events)), r.IntN(len(events))
	payload := func(e jsjson.Value) *jsjson.Obj { return e.Get("event", "payload").Obj() }
	switch r.IntN(9) {
	case 0: // duplicate an event (same event id, turn id, ordinal)
		events = slices.Insert(events, j, clone(events[i]))
	case 1: // move an event
		e := events[i]
		events = slices.Insert(slices.Delete(events, i, i+1), min(j, len(events)-1), e)
	case 2: // drop an event
		events = slices.Delete(events, i, i+1)
	case 3: // swap neighbours
		if i+1 < len(events) {
			events[i], events[i+1] = events[i+1], events[i]
		}
	case 4: // retarget a turn id
		if p := payload(events[i]); p != nil {
			if _, ok := p.Get("turnId"); ok {
				p.Set("turnId", jsjson.MakeString("turn-unknown"))
			}
		}
	case 5: // shift a turn ordinal
		if p := payload(events[i]); p != nil {
			if n, ok := p.Get("turnOrdinal"); ok {
				p.Set("turnOrdinal", jsjson.MakeNumber(n.Num()+float64(1+r.IntN(2))))
			}
			if n, ok := p.Get("afterTurnOrdinal"); ok {
				p.Set("afterTurnOrdinal", jsjson.MakeNumber(n.Num()+1))
			}
		}
	case 6: // an event dated before what it records
		events[i].Obj().Set("occurredAt", jsjson.MakeString("2020-01-01T00:00:00.000Z"))
	case 7: // wrong completed-turn count
		if p := payload(events[i]); p != nil {
			if n, ok := p.Get("completedTurns"); ok {
				p.Set("completedTurns", jsjson.MakeNumber(n.Num()+1))
			}
		}
	case 8: // duplicate the first event (a second SESSION_STARTED) somewhere later
		events = slices.Insert(events, max(1, j), clone(events[0]))
	}
	for k, e := range events {
		if e.Kind() == jsjson.Object {
			e.Obj().Set("sequence", jsjson.MakeNumber(float64(k+1)))
		}
	}
	root.Obj().Set("events", jsjson.MakeArray(events))
	return jsjson.Stringify(root)
}
