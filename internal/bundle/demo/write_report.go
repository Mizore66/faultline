package demo

import (
	"errors"
	"math"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

// verifyInstructions is the VERIFY.md body (src/proof-bundle.ts:379); like the
// TS array join it has no final LF.
const verifyInstructions = "# Verify this bundle\n\n" +
	"Run `fl verify <bundle-directory>` for structural self-consistency.\n\n" +
	"For tamper detection against an editor who could update hash metadata, pass an externally recorded root: `fl verify <bundle-directory> --expect-root <sha256:...>`.\n\n" +
	"Verification never executes repository code. Use the explicit judge rerun path to execute the reviewed built-in sample."

// The supplied analysis is not schema-parsed, so the writer's own operations
// on it follow JavaScript: property access on null/undefined throws a
// TypeError, template literals coerce with ToString, `&&` tests truthiness.

func jsTypeName(v jsjson.Value) string {
	if v.Kind() == jsjson.Null {
		return "null"
	}
	return "undefined"
}

// prop is `value.key`: it throws V8's TypeError for a null or undefined
// receiver and is undefined for a missing key.
func prop(v jsjson.Value, key string) jsjson.Value {
	if v.Kind() == jsjson.Undefined || v.Kind() == jsjson.Null {
		bundle.Throw(errors.New(jsexc.Concat("Cannot read properties of ", jsTypeName(v), " (reading '", key, "')")))
	}
	return v.Get(key)
}

// jsIterate is the iteration of `for (const x of value)` / `...value` over a
// JSON-derived value: arrays yield their elements, strings their code points,
// and anything else throws V8's "<label> is not iterable" TypeError.
func jsIterate(v jsjson.Value, label string) []jsjson.Value {
	switch v.Kind() {
	case jsjson.Array:
		return v.Items()
	case jsjson.String:
		units := jsstr.ToUTF16(v.Str())
		var out []jsjson.Value
		for i := 0; i < len(units); i++ {
			n := 1
			if units[i] >= 0xD800 && units[i] <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				n = 2
			}
			out = append(out, jsjson.MakeString(jsstr.FromUTF16(units[i:i+n])))
			i += n - 1
		}
		return out
	}
	bundle.Throw(errors.New(label + " is not iterable"))
	return nil
}

// setKey is the SameValueZero identity of a value used as a Set/Map key:
// strings, numbers, booleans, null and undefined by value; an array or object
// by reference, which for distinct JSON-parsed values is always distinct.
type setKey struct {
	kind string
	text string
	ref  int
}

func jsSetKey(v jsjson.Value, fresh *int) setKey {
	switch v.Kind() {
	case jsjson.Undefined:
		return setKey{kind: "undefined"}
	case jsjson.Null:
		return setKey{kind: "null"}
	case jsjson.Bool:
		return setKey{kind: "bool", text: templateString(v)}
	case jsjson.Number:
		return setKey{kind: "number", text: jsjson.FormatNumber(v.Num())}
	case jsjson.String:
		return setKey{kind: "string", text: v.Str()}
	}
	*fresh++
	return setKey{kind: "ref", ref: *fresh}
}

// jsRunID is `run.id` followed by `.replace(...)` in safeRunFileName: only a
// string has replace, and a null or undefined id throws the property-read
// TypeError rather than the not-a-function one. Non-string ids are never
// coerced into file names.
func jsRunID(run jsjson.Value) string {
	id := prop(run, "id")
	switch id.Kind() {
	case jsjson.String:
		return id.Str()
	case jsjson.Undefined, jsjson.Null:
		bundle.Throw(errors.New("Cannot read properties of " + jsTypeName(id) + " (reading 'replace')"))
	}
	bundle.Throw(errors.New("run.id.replace is not a function"))
	return ""
}

// jsCoverage is the frozen writer's own coverage check (validateAnalysisCoverage
// and referencedRunIds, src/proof-bundle.ts:188-211) with its JS property
// reads, iteration and throw order: the catalog first, then the reference
// array literal (both spreads, then the five member reads), the loop over it,
// the transitions' boundaryRunIds and finally minimization.attempts. It
// returns the refusal messages; the inherited verifier helper (non-throwing
// by design) is left unchanged.
func jsCoverage(analysis jsjson.Value) []string {
	var errs []string
	fresh := 0
	catalog := map[setKey]bool{}
	for _, record := range jsIterate(prop(analysis, "runCatalog"), "analysis.runCatalog") {
		id := prop(record, "id")
		key := jsSetKey(id, &fresh)
		if catalog[key] {
			errs = append(errs, jsexc.Concat("duplicate run id in catalog: ", templateString(id)))
		}
		jsexc.MapSet(catalog, key, true)
	}
	var runs []jsjson.Value
	runs = append(runs, jsIterate(prop(analysis, "contributionRuns"), "analysis.contributionRuns")...)
	runs = append(runs, jsIterate(prop(analysis, "timelineRuns"), "analysis.timelineRuns")...)
	prevention, minimization := prop(analysis, "prevention"), prop(analysis, "minimization")
	runs = append(runs, prop(prevention, "lastGood"), prop(prevention, "firstBad"), prop(prevention, "repaired"),
		prop(minimization, "sufficiency"), prop(minimization, "necessity"))
	ids := map[setKey]jsjson.Value{}
	var order []setKey
	add := func(id jsjson.Value) {
		key := jsSetKey(id, &fresh)
		if _, ok := ids[key]; !ok {
			jsexc.MapSet(ids, key, id)
			order = append(order, key)
		}
	}
	for _, run := range runs {
		add(prop(run, "id"))
	}
	for _, transition := range jsIterate(prop(analysis, "transitions"), "analysis.transitions") {
		for _, id := range jsIterate(prop(transition, "boundaryRunIds"), "transition.boundaryRunIds") {
			add(id)
		}
	}
	for _, attempt := range jsIterate(prop(prop(analysis, "minimization"), "attempts"), "analysis.minimization.attempts") {
		if runID := prop(attempt, "runId"); truthy(runID) {
			add(runID)
		}
	}
	for _, key := range order {
		if !catalog[key] {
			errs = append(errs, jsexc.Concat("analysis references a run not present in the catalog: ", templateString(ids[key])))
		}
	}
	return errs
}

// templateString is `${value}` (ToString) for a JSON-derived value: arrays
// join their elements with "," (null and undefined elements are empty) and
// objects are "[object Object]".
func templateString(v jsjson.Value) string {
	switch v.Kind() {
	case jsjson.Undefined:
		return "undefined"
	case jsjson.Null:
		return "null"
	case jsjson.String:
		return v.Str()
	case jsjson.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case jsjson.Number:
		return jsjson.FormatNumber(v.Num())
	case jsjson.Array:
		parts := make([]string, len(v.Items()))
		for i, item := range v.Items() {
			if item.Kind() != jsjson.Null && item.Kind() != jsjson.Undefined {
				parts[i] = templateString(item)
			}
		}
		return jsexc.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// truthy is JS ToBoolean: only false, 0, NaN, "", null and undefined are
// falsy; every array and object is truthy.
func truthy(v jsjson.Value) bool {
	switch v.Kind() {
	case jsjson.Undefined, jsjson.Null:
		return false
	case jsjson.Bool:
		return v.Bool()
	case jsjson.Number:
		return v.Num() != 0 && !math.IsNaN(v.Num())
	case jsjson.String:
		return v.Str() != ""
	default:
		return true
	}
}

// report ports report (src/proof-bundle.ts:159): the lines joined with LF and
// no final LF. The first transition whose kind is the string PASS_TO_FAIL
// (===) and whose stable is truthy supplies the conclusion.
func report(analysis jsjson.Value) string {
	var transition jsjson.Value
	// `analysis.transitions.find(...)`: only an array has find. Coverage
	// collection already iterated the value, so a string (iterable, e.g. "")
	// reaches here and throws exactly this TypeError before any write.
	transitions := prop(analysis, "transitions")
	if transitions.Kind() != jsjson.Array {
		bundle.Throw(errors.New(jsexc.Concat("analysis.transitions.find is not a function")))
	}
	for _, item := range transitions.Items() {
		kind := prop(item, "kind")
		if kind.Kind() == jsjson.String && kind.Str() == "PASS_TO_FAIL" && truthy(prop(item, "stable")) {
			transition = item
			break
		}
	}
	conclusion := "No stable pass-to-fail transition was established."
	if truthy(transition) {
		conclusion = jsexc.Concat("The earliest stable recorded transition for the frozen sample witness is ",
			templateString(prop(transition, "beforeStateId")), " -> ", templateString(prop(transition, "afterStateId")), ".")
	}
	lines := []string{
		"# FaultLine proof bundle",
		"",
		jsexc.Concat("Mode: ", templateString(prop(analysis, "mode"))),
		jsexc.Concat("Fixture: ", templateString(prop(prop(analysis, "fixture"), "title"))),
		jsexc.Concat("Witness: ", templateString(prop(prop(analysis, "witness"), "digest"))),
		"",
		"## Bounded conclusion",
		"",
		conclusion,
		"",
		"This bundle supports a predicate-specific result. It does not establish model intent, semantic root cause, or a unique minimal cause.",
		"",
		"## Prevention proof",
		"",
		jsexc.Concat("- Last-good: ", templateString(prop(prop(prop(analysis, "prevention"), "lastGood"), "verdict"))),
		jsexc.Concat("- First-bad: ", templateString(prop(prop(prop(analysis, "prevention"), "firstBad"), "verdict"))),
		jsexc.Concat("- Repaired: ", templateString(prop(prop(prop(analysis, "prevention"), "repaired"), "verdict"))),
		"",
		"## Integrity boundary",
		"",
		"`fl verify` checks the complete declared file set without running repository code. Pass an externally recorded bundle root with `--expect-root` to detect an editor who updates the mutable hash list too.",
	}
	return jsexc.Join(lines, "\n")
}

// logBody is the body Node's createHash().update accepts: a string. Any other
// value throws the TypeError sha256(body) throws, at hashing time.
func logBody(v jsjson.Value) string {
	if v.Kind() == jsjson.String {
		return v.Str()
	}
	var received string
	switch v.Kind() {
	case jsjson.Undefined:
		received = "undefined"
	case jsjson.Null:
		received = "null"
	case jsjson.Number:
		received = "type number (" + jsjson.FormatNumber(v.Num()) + ")"
	case jsjson.Bool:
		if v.Bool() {
			received = "type boolean (true)"
		} else {
			received = "type boolean (false)"
		}
	case jsjson.Array:
		received = "an instance of Array"
	default:
		received = "an instance of Object"
	}
	bundle.Throw(errors.New(`The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received ` + received))
	return ""
}
