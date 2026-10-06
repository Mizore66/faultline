package difftest

import (
	"math/rand/v2"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/difftest/schemadsl"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func TestLiveSchemaDSL(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(11, 12))
	for i := 0; i < liveCases; i++ {
		dslText, valueText := gen.RandomDSL(r)
		want := call(t, c, "zodDsl", `{"schema":`+dslText+`,"text":`+jsjson.Quote(valueText)+`}`)
		dsl, _ := jsjson.Parse(dslText)
		value, _ := jsjson.Parse(valueText)
		out, issues, ok := schema.Parse(schemadsl.Build(dsl), value)
		if ok != want.Field("success").Bool() {
			t.Fatalf("schema %s value %s: ok=%v, zod success=%v (%s)", dslText, valueText, ok, want.Field("success").Bool(), want.Field("message").Str())
		}
		if ok {
			got, err := canonical.CanonicalJSON(out)
			if err != nil {
				got = "ERROR:" + err.Error()
			}
			if got != want.Field("canonical").Str() {
				t.Fatalf("schema %s value %s: data %s, zod %s", dslText, valueText, got, want.Field("canonical").Str())
			}
			continue
		}
		if got := schema.ErrorMessage(issues); got != want.Field("message").Str() {
			t.Fatalf("schema %s value %s:\ngot  %s\nwant %s", dslText, valueText, got, want.Field("message").Str())
		}
	}
}
