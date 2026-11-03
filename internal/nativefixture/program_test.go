package nativefixture

import (
	"bytes"
	"flag"
	"os"
	"slices"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// Only test binaries accept this locator; the reviewed program has no corpus dependency.
var nativeContractPath = flag.String("native-contract", "../../difftest/testdata/native-demo/inputs.jsonl", "absolute or package-relative independent native reference input")

func referenceInput(t *testing.T) jsjson.Value {
	t.Helper()
	data, err := os.ReadFile(*nativeContractPath)
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := bytes.Cut(data, []byte{'\n'})
	row, err := jsjson.Parse(string(line))
	if err != nil || row.Get("id").Str() != "ordinary" {
		t.Fatalf("ordinary reference row: %v", err)
	}
	return row.Get("input")
}

// A descriptor change must not silently keep the previously approved identity.
func TestNativeProgramReferenceAndFreshness(t *testing.T) {
	input := referenceInput(t)
	want := jsjson.Stringify(input.Get("proposal", "program"))
	got := Program()
	if text := jsjson.Stringify(got); text != want {
		t.Fatalf("complete program bytes differ\nwant=%s\ngot=%s", want, text)
	}
	digest, err := canonical.DigestJSON(got)
	if err != nil || digest != input.Get("proposal", "programDigest").Str() {
		t.Fatalf("program digest = %q, %v", digest, err)
	}
	got.Obj().Set("runner", jsjson.MakeString("mutated"))
	got.Get("fields", "displayDefault").Items()[0] = jsjson.MakeString("mutated")
	got.Get("edits").Items()[0].Obj().Set("from", jsjson.MakeString("mutated"))
	if next := jsjson.Stringify(Program()); next != want {
		t.Fatalf("returned program aliased another call: %s", next)
	}
}

// Literal full configs catch wrong edit text, lost LF, and order-sensitive edits.
func TestNativeMaterializeExactBytes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hunks   []string
		want    string
		partial bool
	}{
		{"none", nil, "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"ORDER_CURRENCY\"}\n", false},
		{"source", []string{"settlement-display-default"}, "{\"displayDefault\":\"EUR\",\"settlementSource\":\"ORDER_CURRENCY\"}\n", false},
		{"boundary", []string{"settlement-display-boundary"}, "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", false},
		{"both", []string{"settlement-display-default", "settlement-display-boundary"}, "{\"displayDefault\":\"EUR\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", false},
		{"reversed", []string{"settlement-display-boundary", "settlement-display-default"}, "{\"displayDefault\":\"EUR\",\"settlementSource\":\"DISPLAY_CURRENCY\"}\n", false},
		{"partial", []string{"partial-renderer-hunk"}, "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.hunks)
			got, err := Materialize(tc.hunks)
			if err != nil || string(got.Data) != tc.want || got.Partial != tc.partial {
				t.Fatalf("config=%q partial=%v err=%v", got.Data, got.Partial, err)
			}
			if !slices.Equal(tc.hunks, before) {
				t.Fatal("materializer changed caller hunks")
			}
			got.Data[0] = '!'
			next, err := Materialize(tc.hunks)
			if err != nil || string(next.Data) != tc.want {
				t.Fatalf("materialized bytes alias another call: %q, %v", next.Data, err)
			}
		})
	}
}

func TestNativeMaterializeRejectsUnsupportedEdits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hunks []string
		want  string
	}{
		{"unknown", []string{"cedar-turn-5"}, "unknown native fixture hunk \"cedar-turn-5\""},
		{"empty", []string{""}, "unknown native fixture hunk \"\""},
		{"duplicate", []string{"settlement-display-default", "settlement-display-default"}, "duplicate native fixture hunk \"settlement-display-default\""},
		{"partial-source", []string{"partial-renderer-hunk", "settlement-display-default"}, "partial native fixture hunk must be used alone"},
		{"boundary-partial", []string{"settlement-display-boundary", "partial-renderer-hunk"}, "partial native fixture hunk must be used alone"},
		{"partial-both", []string{"settlement-display-default", "settlement-display-boundary", "partial-renderer-hunk"}, "partial native fixture hunk must be used alone"},
		{"duplicate-partial", []string{"partial-renderer-hunk", "partial-renderer-hunk"}, "duplicate native fixture hunk \"partial-renderer-hunk\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Materialize(tc.hunks)
			if err == nil || err.Error() != tc.want || len(got.Data) != 0 || got.Partial {
				t.Fatalf("config=%q partial=%v err=%v; want %q", got.Data, got.Partial, err, tc.want)
			}
		})
	}
}

// These inspect computed currencies, not the predicate: hardcoded USD or a
// verdict table cannot substitute for the refund model.
func TestNativeRefundModel(t *testing.T) {
	eur, empty := "EUR", ""
	for _, tc := range []struct {
		name, order string
		display     *string
		config      modelConfig
		want        refundResult
	}{
		{"base", "USD", nil, modelConfig{"ORDER_CURRENCY", "ORDER_CURRENCY"}, refundResult{"USD", "USD"}},
		{"source", "USD", nil, modelConfig{"EUR", "ORDER_CURRENCY"}, refundResult{"EUR", "USD"}},
		{"boundary", "USD", nil, modelConfig{"ORDER_CURRENCY", "DISPLAY_CURRENCY"}, refundResult{"USD", "USD"}},
		{"interaction", "USD", nil, modelConfig{"EUR", "DISPLAY_CURRENCY"}, refundResult{"EUR", "EUR"}},
		{"other-order", "JPY", nil, modelConfig{"ORDER_CURRENCY", "DISPLAY_CURRENCY"}, refundResult{"JPY", "JPY"}},
		{"explicit-display", "JPY", &eur, modelConfig{"ORDER_CURRENCY", "DISPLAY_CURRENCY"}, refundResult{"EUR", "EUR"}},
		{"explicit-preserved", "JPY", &eur, modelConfig{"EUR", "ORDER_CURRENCY"}, refundResult{"EUR", "JPY"}},
		{"explicit-empty", "USD", &empty, modelConfig{"EUR", "DISPLAY_CURRENCY"}, refundResult{"", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := completeRefund(tc.order, tc.display, tc.config); got != tc.want {
				t.Fatalf("refund=%+v want=%+v", got, tc.want)
			}
		})
	}
}
