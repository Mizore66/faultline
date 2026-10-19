// Package witnessresult parses the structured outcome line a witness process
// can print, so a real predicate result is distinguishable from a broken
// execution environment.
package witnessresult

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/schema"
)

// Port of src/witness-result.ts:15-18 protocol and marker constants.
const (
	Protocol = "faultline.witness-result.v1"
	Marker   = "FAULTLINE_WITNESS_RESULT="
)

// Port of src/witness-result.ts:20 witnessOutcomeValues.
var OutcomeValues = []string{
	"PREDICATE_PASS",
	"PREDICATE_FAIL",
	"INCOMPATIBLE_STATE",
	"HARNESS_ERROR",
	"INFRASTRUCTURE_ERROR",
}

// Result is WitnessResult; HasDetail distinguishes an absent detail from "".
type Result struct {
	Outcome   string
	Detail    string
	HasDetail bool
}

// Port of src/witness-result.ts:30 WitnessResultSchema.
var resultSchema = schema.Object(
	schema.F("protocol", schema.LiteralString(Protocol)),
	schema.F("outcome", schema.Enum(OutcomeValues...)),
	schema.F("detail", schema.Optional(schema.String(schema.MaxLength(4096)))),
).Strict()

// Port of src/witness-result.ts:40 formatWitnessResult.
func Format(outcome, detail string, hasDetail bool) string {
	o := jsjson.NewObj()
	o.Set("protocol", jsjson.MakeString(Protocol))
	o.Set("outcome", jsjson.MakeString(outcome))
	if hasDetail {
		o.Set("detail", jsjson.MakeString(detail))
	}
	return Marker + jsjson.Stringify(jsjson.MakeObject(o))
}

// Port of src/witness-result.ts:47 tryParseLine.
func tryParseLine(line string) *Result {
	trimmed := jsstr.Trim(line)
	if trimmed == "" {
		return nil
	}
	candidate := trimmed
	if strings.HasPrefix(trimmed, Marker) {
		candidate = jsstr.Trim(trimmed[len(Marker):])
	}
	if !strings.HasPrefix(candidate, "{") {
		return nil
	}
	value, err := jsjson.Parse(candidate)
	if err != nil {
		return nil
	}
	out, _, ok := schema.Parse(resultSchema, value)
	if !ok {
		return nil
	}
	r := &Result{Outcome: out.Get("outcome").Str()}
	if obj := out.Obj(); obj != nil {
		if d, has := obj.Get("detail"); has && d.Kind() == jsjson.String {
			r.Detail, r.HasDetail = d.Str(), true
		}
	}
	return r
}

// Port of src/witness-result.ts:62 outcomeKey.
func outcomeKey(r *Result) string {
	if !r.HasDetail {
		return r.Outcome
	}
	return r.Outcome + "\x00" + r.Detail
}

// splitLines is stdout.split(/\r?\n/).
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i
		if end > start && s[end-1] == '\r' {
			end--
		}
		lines = append(lines, s[start:end])
		start = i + 1
	}
	return append(lines, s[start:])
}

// Port of src/witness-result.ts:69 parseAllWitnessResults.
func parseAll(stdout string) []*Result {
	if stdout == "" {
		return nil
	}
	var found []*Result
	for _, line := range splitLines(stdout) {
		if r := tryParseLine(line); r != nil {
			found = append(found, r)
		}
	}
	return found
}

// Parsed is ParsedWitnessStdout: {ok:true,result} or {ok:false,reason}. Result
// is nil when no record was found (TS result: null).
type Parsed struct {
	Ok     bool
	Result *Result
	Reason string
}

// Port of src/witness-result.ts:87 resolveWitnessResultFromStdout.
func Resolve(stdout string) Parsed {
	results := parseAll(stdout)
	if len(results) == 0 {
		return Parsed{Ok: true}
	}
	key := outcomeKey(results[0])
	for _, next := range results[1:] {
		if outcomeKey(next) != key {
			return Parsed{Reason: "CONFLICTING_WITNESS_RESULTS"}
		}
	}
	// Prefer the last copy when duplicates agree.
	return Parsed{Ok: true, Result: results[len(results)-1]}
}

// Port of src/witness-result.ts:118 witnessResultConsistentWithExitCode.
func ConsistentWithExitCode(r Result, exitCode int) bool {
	switch r.Outcome {
	case "PREDICATE_PASS":
		return exitCode == 0
	case "PREDICATE_FAIL":
		return exitCode != 0
	}
	return true
}

// Classification is WitnessResultClassification.
type Classification struct{ Verdict, Reason string }

// Port of src/witness-result.ts:145 classifyFromWitnessResult.
func Classify(outcome string) Classification {
	switch outcome {
	case "PREDICATE_PASS":
		return Classification{"PASS", "PREDICATE_PASS"}
	case "PREDICATE_FAIL":
		return Classification{"FAIL", "PREDICATE_FAIL"}
	case "INCOMPATIBLE_STATE":
		return Classification{"INAPPLICABLE", "INCOMPATIBLE_STATE"}
	case "HARNESS_ERROR":
		return Classification{"ERROR", "HARNESS_ERROR"}
	case "INFRASTRUCTURE_ERROR":
		return Classification{"ERROR", "SANDBOX_UNAVAILABLE"}
	}
	return Classification{}
}
