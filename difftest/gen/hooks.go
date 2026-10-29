package gen

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
)

// HookEvent is one Codex hook document (codex-sidecar.ts:68-113): Fields
// in the order they are written; Raw, when set, replaces the document (a
// malformed or oversized input).
type HookEvent struct {
	Fields [][2]any // {key, value}; value is string, float64, bool, nil or json.RawMessage
	Raw    []byte
}

// HookStep is one hook invocation of a sequence, with the workspace change
// applied before it.
type HookStep struct {
	Event HookEvent
	Op    HookOp
}

// HookOp changes the workspace before a step.
type HookOp struct {
	Kind string // "", "dirty", "clean", "commit", "plant-fresh-lock", "plant-stale-lock", "plant-receipt", "plant-stage", "symlink-storage", "remove-receipt", "truncate-ledger"
	Arg  string
}

// HookOpKinds are the HookOp kinds HookSequence draws from, "" (no change)
// included.
var HookOpKinds = []string{"", "dirty", "clean", "commit", "plant-fresh-lock", "plant-stale-lock", "plant-receipt", "plant-stage", "symlink-storage", "remove-receipt", "truncate-ledger"}

// HookPromptLimit is UserPromptSubmitHookSchema's prompt bound (4 MiB);
// HookSequence's oversized prompt is one character longer.
const HookPromptLimit = 4 * 1024 * 1024

// HookCwd is the cwd placeholder of a generated document: a live test
// replaces it (JSON-escaped) with the repository each side runs in.
const HookCwd = "<CWD>"

// JSON is the hook document as it is written to the hook's stdin: Raw, or
// Fields as a JSON object in their order, cwd placeholders replaced by cwd.
func (e HookEvent) JSON(cwd string) []byte {
	if e.Raw != nil {
		return []byte(strings.ReplaceAll(string(e.Raw), HookCwd, jsonEscape(cwd)))
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range e.Fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustJSON(f[0]))
		b.WriteByte(':')
		switch v := f[1].(type) {
		case json.RawMessage:
			b.Write(v)
		case string:
			b.Write(mustJSON(strings.ReplaceAll(v, HookCwd, cwd)))
		default:
			b.Write(mustJSON(v))
		}
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// Field is the value of the document's key, and whether it is present.
func (e HookEvent) Field(key string) (any, bool) {
	for _, f := range e.Fields {
		if f[0] == key {
			return f[1], true
		}
	}
	return nil, false
}

// mustJSON is v as JSON without HTML escaping (<, > and & stay as they
// are, as JSON.stringify writes them).
func mustJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// jsonEscape is s as the inside of a JSON string.
func jsonEscape(s string) string {
	b := mustJSON(s)
	return string(b[1 : len(b)-1])
}

// hookGen builds one sequence: the session's model and cwd, the turns so
// far, and the steps written.
type hookGen struct {
	r       *rand.Rand
	session string
	model   string
	steps   []HookStep
	turns   []string // turn ids started, in order
	tools   int
}

func (g *hookGen) pick(options ...string) string { return options[g.r.IntN(len(options))] }

// base is the fields every document starts with, in Codex's order; Codex
// lists hook_event_name first.
func (g *hookGen) base(name string) [][2]any {
	session, cwd, model := any(g.session), any(HookCwd), any(g.model)
	switch g.r.IntN(24) {
	case 0: // a wrong session id
		session = g.session + "-other"
	case 1: // a session id the schema refuses
		session = g.pick("", "-x", "a b", strings.Repeat("s", 161))
	case 2: // a changed model
		model = g.model + "-changed"
	case 3: // a changed or relative cwd
		cwd = g.pick(HookCwd+"/sub", "relative/dir", HookCwd+"/missing")
	case 4: // a model the schema refuses
		model = g.pick("", strings.Repeat("m", 201))
	}
	return [][2]any{{"hook_event_name", name}, {"session_id", session}, {"cwd", cwd}, {"model", model}}
}

// extras are fields Codex sends that the sidecar must strip (or, for the
// strict events, that pass through zod's default strip as well).
func (g *hookGen) extras(fields [][2]any) [][2]any {
	if g.r.IntN(4) != 0 {
		return fields
	}
	switch g.r.IntN(3) {
	case 0:
		return append(fields, [2]any{"transcript_path", "/home/user/.codex/sessions/transcript.jsonl"})
	case 1:
		return append(fields, [2]any{"tool_input", json.RawMessage(`{"command":["cat","secret.txt"],"workdir":"/tmp"}`)})
	}
	return append(fields, [2]any{"permission_mode", "default"}, [2]any{"stop_hook_active", false})
}

// turnID is a turn id, sometimes missing (nil) or malformed.
func (g *hookGen) turnID(id string) (any, bool) {
	switch g.r.IntN(30) {
	case 0:
		return nil, false
	case 1:
		return g.pick("", "bad turn", "-t"), true
	case 2:
		return float64(7), true
	}
	return id, true
}

func (g *hookGen) add(fields [][2]any) {
	g.steps = append(g.steps, HookStep{Event: HookEvent{Fields: g.extras(fields)}, Op: g.op()})
}

// op is the workspace change before a step: mostly none.
func (g *hookGen) op() HookOp {
	if g.r.IntN(5) != 0 {
		return HookOp{}
	}
	kind := HookOpKinds[1+g.r.IntN(len(HookOpKinds)-1)]
	op := HookOp{Kind: kind}
	switch kind {
	case "dirty":
		op.Arg = g.pick("state.txt", "notes.txt", "sub/new.txt")
	case "plant-receipt", "remove-receipt", "plant-stage":
		if len(g.turns) > 0 {
			op.Arg = g.turns[g.r.IntN(len(g.turns))]
		} else {
			op.Arg = "t-1"
		}
	case "truncate-ledger":
		op.Arg = strconv.Itoa(1 + g.r.IntN(64))
	}
	return op
}

func (g *hookGen) sessionStart() {
	g.add(g.base("SessionStart"))
}

func (g *hookGen) userPromptSubmit(turn string) {
	fields := g.base("UserPromptSubmit")
	if id, ok := g.turnID(turn); ok {
		fields = append(fields, [2]any{"turn_id", id})
	}
	switch g.r.IntN(40) {
	case 0: // one character over the schema's bound, as Raw so it is not copied per field
		doc := HookEvent{Fields: append(fields, [2]any{"prompt", "\x00"})}.JSON(HookCwd)
		raw := strings.Replace(string(doc), `"\u0000"`, `"`+strings.Repeat("a", HookPromptLimit+1)+`"`, 1)
		g.steps = append(g.steps, HookStep{Event: HookEvent{Raw: []byte(raw)}, Op: g.op()})
		return
	case 1:
		fields = append(fields, [2]any{"prompt", float64(1)})
	case 2: // no prompt
	default:
		fields = append(fields, [2]any{"prompt", g.pick("", "Fix the failing test.", "Use é and 中 and 😀.", "line one\nline two")})
	}
	g.add(fields)
}

func (g *hookGen) toolUse(name, turn string) {
	fields := g.base(name)
	if id, ok := g.turnID(turn); ok {
		fields = append(fields, [2]any{"turn_id", id})
	}
	fields = append(fields, [2]any{"tool_name", g.pick("shell", "apply_patch", "Bash", "bad tool", "")})
	if g.r.IntN(3) != 0 {
		g.tools++
		fields = append(fields, [2]any{"tool_call_id", "call-" + strconv.Itoa(g.tools)})
	}
	g.add(fields)
}

func (g *hookGen) stop(turn string) {
	fields := g.base("Stop")
	if id, ok := g.turnID(turn); ok {
		fields = append(fields, [2]any{"turn_id", id})
	}
	g.add(fields)
}

// malformed is a document the hook cannot parse or the schema refuses
// whole.
func (g *hookGen) malformed() {
	raw := g.pick(`{"hook_event_name":"SessionStart"`, `[]`, `null`, `{"hook_event_name":"SessionEnd","session_id":"s","cwd":"`+HookCwd+`","model":"m"}`, "\xff\xfe", ``)
	g.steps = append(g.steps, HookStep{Event: HookEvent{Raw: []byte(raw)}, Op: g.op()})
}

// redeliver repeats an earlier step (Codex may deliver a hook twice, also
// out of order), without its workspace change.
func (g *hookGen) redeliver() {
	if len(g.steps) == 0 {
		return
	}
	prev := g.steps[g.r.IntN(len(g.steps))]
	g.steps = append(g.steps, HookStep{Event: prev.Event, Op: g.op()})
}

// HookSequence returns the i-th generated sequence for session s: a
// SessionStart, then up to 4 turns of UserPromptSubmit, Pre/PostToolUse
// pairs and Stop, with redeliveries (any step repeated, also out of
// order), wrong session ids, a changed model or cwd, a missing turn_id,
// unknown extra fields (transcript_path, tool_input) that must be
// stripped, a 4 MiB+1 prompt, and HookOps between steps. Deterministic in
// (i, s).
func HookSequence(r *rand.Rand, i int, s string) []HookStep {
	g := &hookGen{r: r, session: s, model: "gpt-5.6"}
	if i%3 == 1 {
		g.model = "o4-mini"
	}
	g.sessionStart()
	// The first SessionStart is a well-formed one: a sequence that never
	// starts its session would test nothing past the first refusal.
	g.steps[0].Event.Fields = g.steps[0].Event.Fields[:4]
	g.steps[0].Event.Fields[1][1], g.steps[0].Event.Fields[2][1], g.steps[0].Event.Fields[3][1] = s, HookCwd, g.model
	g.steps[0].Op = HookOp{}
	turns := r.IntN(5)
	for n := 1; n <= turns; n++ {
		turn := "t-" + strconv.Itoa(n)
		g.turns = append(g.turns, turn)
		g.userPromptSubmit(turn)
		for range r.IntN(3) {
			g.toolUse("PreToolUse", turn)
			if r.IntN(5) != 0 {
				g.toolUse("PostToolUse", turn)
			}
		}
		if r.IntN(8) != 0 {
			g.stop(turn)
		}
		if r.IntN(4) == 0 {
			g.redeliver()
		}
		if r.IntN(12) == 0 {
			g.malformed()
		}
	}
	if r.IntN(3) == 0 {
		g.redeliver()
	}
	if r.IntN(6) == 0 {
		// A tool event or Stop for a turn never started.
		g.toolUse("PreToolUse", "t-"+strconv.Itoa(turns+1))
	}
	return g.steps
}
