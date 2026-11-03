// Package nativefixture evaluates only the reviewed built-in Go refund model.
// It does not execute the historical JavaScript or arbitrary repository code.
package nativefixture

import (
	"fmt"
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
)

const (
	ChildArg      = "--internal-native-demo-witness-v1"
	ChildEnvName  = "FAULTLINE_NATIVE_DEMO_CHILD"
	ChildEnvValue = "v1"
	ConfigFile    = "native-config.json"
	Runner        = "native-go-config-evaluator-v1"

	maxConfigBytes = 16384
	baseConfig     = "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":\"ORDER_CURRENCY\"}\n"
	passStdout     = "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n"
	failStdout     = "{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n"
	failStderr     = "expected settlement USD; received EUR\n"
)

type Materialized struct {
	Data    []byte
	Partial bool
}

type configEdit struct{ hunk, from, to string }

func reviewedEdits() [3]configEdit {
	return [3]configEdit{
		{"settlement-display-default", `"displayDefault":"ORDER_CURRENCY"`, `"displayDefault":"EUR"`},
		{"settlement-display-boundary", `"settlementSource":"ORDER_CURRENCY"`, `"settlementSource":"DISPLAY_CURRENCY"`},
		{"partial-renderer-hunk", `"settlementSource":"ORDER_CURRENCY"`, `"settlementSource":`},
	}
}

func stringValues(values ...string) jsjson.Value {
	items := make([]jsjson.Value, len(values))
	for i, value := range values {
		items[i] = jsjson.MakeString(value)
	}
	return jsjson.MakeArray(items)
}

// Program returns a fresh ordered descriptor bound by the native proposal.
// It contains no state IDs, invocation schedule, or state-to-verdict table.
func Program() jsjson.Value {
	edits := make([]jsjson.Value, 0, 3)
	for _, edit := range reviewedEdits() {
		edits = append(edits, jsjson.MustObject(
			"hunk", jsjson.MakeString(edit.hunk),
			"from", jsjson.MakeString(edit.from),
			"to", jsjson.MakeString(edit.to)))
	}
	return jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-demo-program.v1"),
		"runner", jsjson.MakeString(Runner),
		"configFile", jsjson.MakeString(ConfigFile),
		"maxConfigBytes", jsjson.MakeNumber(maxConfigBytes),
		"fields", jsjson.MustObject(
			"displayDefault", stringValues("ORDER_CURRENCY", "EUR"),
			"settlementSource", stringValues("ORDER_CURRENCY", "DISPLAY_CURRENCY")),
		"input", jsjson.MustObject(
			"order", jsjson.MustObject("currency", jsjson.MakeString("USD")),
			"refund", jsjson.MustObject()),
		"predicate", jsjson.MakeString("result.settlementCurrency === order.currency"),
		"baseConfig", jsjson.MakeString(baseConfig),
		"edits", jsjson.MakeArray(edits),
		"stdoutPass", jsjson.MakeString(passStdout),
		"stdoutFail", jsjson.MakeString(failStdout),
		"stderrFail", jsjson.MakeString(failStderr),
		"exitCodes", jsjson.MustObject(
			"pass", jsjson.MakeNumber(0),
			"fail", jsjson.MakeNumber(1),
			"configError", jsjson.MakeNumber(3)))
}

// Materialize applies only the reviewed literal edits, in canonical edit order.
func Materialize(hunks []string) (Materialized, error) {
	edits := reviewedEdits()
	selected := make(map[string]bool, len(hunks))
	for _, hunk := range hunks {
		known := false
		for _, edit := range edits {
			known = known || edit.hunk == hunk
		}
		if !known {
			return Materialized{}, fmt.Errorf("unknown native fixture hunk %q", hunk)
		}
		if selected[hunk] {
			return Materialized{}, fmt.Errorf("duplicate native fixture hunk %q", hunk)
		}
		selected[hunk] = true
	}
	partial := selected["partial-renderer-hunk"]
	if partial && len(hunks) != 1 {
		return Materialized{}, fmt.Errorf("partial native fixture hunk must be used alone")
	}
	text := baseConfig
	for _, edit := range edits {
		if !selected[edit.hunk] {
			continue
		}
		if strings.Count(text, edit.from) != 1 {
			return Materialized{}, fmt.Errorf("native fixture edit %q must match exactly once", edit.hunk)
		}
		text = strings.Replace(text, edit.from, edit.to, 1)
	}
	return Materialized{Data: []byte(text), Partial: partial}, nil
}

type displayDefault string
type settlementSource string

type modelConfig struct {
	displayDefault   displayDefault
	settlementSource settlementSource
}

type refundResult struct{ displayCurrency, settlementCurrency string }

func completeRefund(orderCurrency string, refundDisplay *string, config modelConfig) refundResult {
	display := orderCurrency
	if config.displayDefault == "EUR" {
		display = "EUR"
	}
	if refundDisplay != nil {
		display = *refundDisplay
	}
	settlement := orderCurrency
	if config.settlementSource == "DISPLAY_CURRENCY" {
		settlement = display
	}
	return refundResult{displayCurrency: display, settlementCurrency: settlement}
}
