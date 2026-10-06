package gen

import (
	"math/rand/v2"
	"slices"

	"github.com/Mizore66/faultline/internal/jsjson"
)

// calendarEdges are ISO-8601 timestamps around the checks a canonical-date
// validator makes: 2000 and 2400 are leap years, 1900 and 2100 are not; the
// expanded-year forms and the ends of the Date range (8.64e15 ms either side
// of 1970); month, day, hour, minute and second one past their range.
var calendarEdges = []string{
	"2000-02-29T00:00:00.000Z", "2400-02-29T12:00:00.000Z", "1900-02-29T00:00:00.000Z", "2100-02-29T00:00:00.000Z",
	"2024-02-29T23:59:59.999Z", "2023-02-29T00:00:00.000Z", "2000-02-30T00:00:00.000Z", "1600-02-29T00:00:00.000Z",
	"0000-01-01T00:00:00.000Z", "9999-12-31T23:59:59.999Z", "2026-12-31T23:59:60.000Z", "2026-01-01T24:00:00.000Z",
	"2026-04-31T00:00:00.000Z", "2026-13-01T00:00:00.000Z",
	"+000000-01-01T00:00:00.000Z", "-000000-01-01T00:00:00.000Z", "+002026-01-01T00:00:00.000Z", "-000001-12-31T23:59:59.999Z",
	"-271821-04-20T00:00:00.000Z", "-271821-04-19T23:59:59.999Z", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z",
	"2026-00-15T00:00:00.000Z", "2026-01-00T00:00:00.000Z", "2026-01-01T00:60:00.000Z", "2026-01-01T00:00:61.000Z",
}

// calendarPairs are two instants a day boundary apart, with the later one
// earlier in its day; the first three cross the March 1 start of a 400-year
// era's year count (the days-from-civil computation's era step).
var calendarPairs = [][2]string{
	{"2001-02-28T12:00:00.000Z", "2001-03-01T00:00:00.000Z"},
	{"2401-02-28T23:59:59.999Z", "2401-03-01T00:00:00.000Z"},
	{"+010001-02-28T12:00:00.000Z", "+010001-03-01T00:00:00.000Z"},
	{"2099-12-31T23:00:00.000Z", "2100-01-01T01:00:00.000Z"},
	{"2100-02-28T12:00:00.000Z", "2100-03-01T00:00:00.000Z"},
}

// MutateLedgerEvents makes one structural change to a lifecycle ledger's
// event list, the kind the lifecycle state machine checks: a duplicated,
// moved, dropped or re-typed event, a retargeted turn id or ordinal, an
// occurredAt earlier than the snapshot or checkpoint it carries, a wrong
// completedTurns count, a timestamp on a calendar edge, or every event moved
// across a day boundary. Sequence numbers are renumbered, so after re-signing
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
	switch r.IntN(11) {
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
	case 9: // a timestamp on a calendar edge: leap days of century years,
		// the ends of the four-digit range, second 60, hour 24
		stamp := calendarEdges[r.IntN(len(calendarEdges))]
		if r.IntN(3) == 0 {
			root.Obj().Set("createdAt", jsjson.MakeString(stamp))
		} else {
			events[i].Obj().Set("occurredAt", jsjson.MakeString(stamp))
		}
	case 10: // every event on one side of a day boundary, in order
		pair := calendarPairs[r.IntN(len(calendarPairs))]
		for k, e := range events {
			if e.Kind() == jsjson.Object {
				stamp := pair[0]
				if k > i {
					stamp = pair[1]
				}
				e.Obj().Set("occurredAt", jsjson.MakeString(stamp))
			}
		}
	}
	for k, e := range events {
		if e.Kind() == jsjson.Object {
			e.Obj().Set("sequence", jsjson.MakeNumber(float64(k+1)))
		}
	}
	root.Obj().Set("events", jsjson.MakeArray(events))
	return jsjson.Stringify(root)
}
