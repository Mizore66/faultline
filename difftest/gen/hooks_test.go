package gen

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestHookSequenceDeterministicAndStarts(t *testing.T) {
	a := HookSequence(rand.New(rand.NewPCG(1, 2)), 7, "s-1")
	b := HookSequence(rand.New(rand.NewPCG(1, 2)), 7, "s-1")
	if !reflect.DeepEqual(a, b) || len(a) == 0 {
		t.Fatal("not deterministic")
	}
	if name, _ := a[0].Event.Fields[0][1].(string); a[0].Event.Raw == nil && name != "SessionStart" {
		t.Fatalf("first step %v", a[0].Event.Fields)
	}
}

// Over many sequences every documented shape appears: each event, a
// redelivery, a wrong session, a missing turn_id, stripped extras, the
// oversized prompt, a malformed document and every HookOp kind. Each
// document is JSON (or meant not to be), with keys in the given order.
func TestHookSequenceCoverage(t *testing.T) {
	seen := map[string]bool{}
	r := rand.New(rand.NewPCG(4, 2))
	for i := range 2000 {
		steps := HookSequence(r, i, "s-1")
		first := steps[0].Event
		if first.Raw != nil || len(first.Fields) != 4 || first.Fields[0][1] != "SessionStart" || first.Fields[1][1] != "s-1" || first.Fields[2][1] != HookCwd {
			t.Fatalf("sequence %d starts with %v", i, first.Fields)
		}
		docs := map[string]int{}
		for _, step := range steps {
			seen["op:"+step.Op.Kind] = true
			doc := string(step.Event.JSON("/w/x"))
			docs[doc]++
			if docs[doc] == 2 {
				seen["redelivery"] = true
			}
			if step.Event.Raw != nil {
				if len(step.Event.Raw) > HookPromptLimit {
					seen["oversized"] = true
					var v map[string]any
					if json.Unmarshal(step.Event.Raw, &v) != nil || len(v["prompt"].(string)) != HookPromptLimit+1 {
						t.Fatal("the oversized prompt is not one character over the bound")
					}
				} else {
					seen["malformed"] = true
				}
				continue
			}
			name, _ := step.Event.Field("hook_event_name")
			seen["event:"+name.(string)] = true
			if session, _ := step.Event.Field("session_id"); session != "s-1" {
				seen["wrong-session"] = true
			}
			if _, ok := step.Event.Field("turn_id"); !ok && name != "SessionStart" {
				seen["missing-turn"] = true
			}
			for _, extra := range []string{"transcript_path", "tool_input"} {
				if _, ok := step.Event.Field(extra); ok {
					seen["extra:"+extra] = true
				}
			}
			var keys []string
			dec := json.NewDecoder(strings.NewReader(doc))
			if _, err := dec.Token(); err != nil {
				t.Fatalf("%s: %v", doc, err)
			}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					t.Fatalf("%s: %v", doc, err)
				}
				keys = append(keys, k.(string))
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					t.Fatalf("%s: %v", doc, err)
				}
			}
			want := make([]string, len(step.Event.Fields))
			for i, f := range step.Event.Fields {
				want[i] = f[0].(string)
			}
			if !slices.Equal(keys, want) {
				t.Fatalf("keys %v, want %v", keys, want)
			}
			if strings.Contains(doc, HookCwd) || strings.Contains(doc, `\u003c`) {
				t.Fatalf("placeholder left or HTML-escaped: %s", doc)
			}
		}
	}
	want := []string{"redelivery", "oversized", "malformed", "wrong-session", "missing-turn", "extra:transcript_path", "extra:tool_input",
		"event:SessionStart", "event:UserPromptSubmit", "event:PreToolUse", "event:PostToolUse", "event:Stop"}
	for _, kind := range HookOpKinds {
		want = append(want, "op:"+kind)
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("no sequence has %s", w)
		}
	}
}

func TestHookEventJSONCwd(t *testing.T) {
	e := HookEvent{Fields: [][2]any{{"cwd", HookCwd + "/sub"}, {"n", float64(1)}, {"x", nil}, {"raw", json.RawMessage(`{"a":1}`)}}}
	if got := string(e.JSON(`/a "b"`)); got != `{"cwd":"/a \"b\"/sub","n":1,"x":null,"raw":{"a":1}}` {
		t.Fatal(got)
	}
	raw := HookEvent{Raw: []byte(`{"cwd":"<CWD>"`)}
	if got := string(raw.JSON(`C:\w`)); got != `{"cwd":"C:\\w"` {
		t.Fatal(got)
	}
}
