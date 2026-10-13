package difftest

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
)

const liveCases = 10000

// call sends args to op and parses the result with jsjson.
func call(t *testing.T, c *oracle.Client, op, args string) *jsjson.Obj {
	t.Helper()
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	v, err := jsjson.Parse(raw)
	if err != nil {
		t.Fatalf("%s: undecodable result %q: %v", op, raw, err)
	}
	return v.Obj()
}

func TestLiveParse(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < liveCases; i++ {
		text := gen.RandomJSONText(r)
		want := call(t, c, "parse", `{"text":`+jsjson.Quote(text)+`}`)
		v, err := jsjson.Parse(text)
		if want.Field("ok").Bool() {
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v; Node accepted it", text, err)
			}
			if got := jsjson.Stringify(v); got != want.Field("compact").Str() {
				t.Fatalf("Stringify(%q) = %q, want %q", text, got, want.Field("compact").Str())
			}
			if got := jsjson.StringifyIndent(v, "  "); got != want.Field("pretty").Str() {
				t.Fatalf("StringifyIndent(%q) = %q, want %q", text, got, want.Field("pretty").Str())
			}
			continue
		}
		if err == nil || err.Error() != want.Field("message").Str() {
			t.Fatalf("Parse(%q) error = %v, want %q", text, err, want.Field("message").Str())
		}
	}
}

func TestLiveFormatNumber(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < liveCases; i++ {
		f := gen.RandomFloat(r)
		raw, err := c.Call("formatNumber", fmt.Sprintf(`{"hex":"%016x"}`, math.Float64bits(f)))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if got := jsjson.FormatNumber(f); got != want.Str() {
			t.Fatalf("FormatNumber(%x) = %q, want %q", math.Float64bits(f), got, want.Str())
		}
	}
}
