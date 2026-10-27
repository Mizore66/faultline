package incident

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	s "github.com/Mizore66/faultline/internal/schema"
)

// RangeError ports IncidentDraftRangeError (src/incident.ts:155). Code is
// LOCAL_HEAD_REQUIRED, ROOT_HEAD, AMBIGUOUS_HEAD or DEGENERATE_RANGE.
type RangeError struct{ Code, Message string }

func (e *RangeError) Error() string { return e.Message }

// DraftInput is createIncidentDraft's input (src/incident.ts:88,
// IncidentDraftInputSchema): the members createIncidentDraft reads are
// draftId (ID), createdAt, repository, command, range, localHead, runtime
// and proposal. An Undefined jsjson.Value (the zero value, "nil") is an
// absent member; any other value, null included, is validated as TS
// validates it. The symptom, language, behavior and timeout of `incident
// start` are not draft members: the command passes them to the witness
// proposal directly (cli-app.ts:630-646).
type DraftInput struct {
	ID, Repository, Command string
	// CreatedAt is the draft's createdAt. "" reads clock.Now() once, inside
	// CreateDraft, where TS's caller reads `new Date().toISOString()`
	// (src/cli-app.ts:617). `incident start` builds the draft twice from
	// one draftInput (cli-app.ts:629 and 647), so its second CreateDraft
	// must pass the first draft's createdAt.
	CreatedAt string
	// Range is the explicit {ancestor, descendant} bracket (--from/--to).
	// A resolved range (ResolveRange's result, with source) is accepted
	// too: EXPLICIT is the same bracket, and LOCAL_HEAD_PARENT is the
	// observed facts {head: descendant, parents: [ancestor]}, which resolve
	// to the same range.
	Range jsjson.Value
	// LocalHead is the observed {head, parents} (LocalHeadFacts), used when
	// Range is absent. TS passes the raw facts to createIncidentDraft, so
	// zod reports their issues together with the other members'.
	LocalHead jsjson.Value
	// Runtime is {requested, image}; Proposal is the proposal binding
	// {proposalId, proposalDigest, incidentPacketDigest, commandDigest}.
	Runtime, Proposal jsjson.Value
}

func throw(message string) { jsexc.Throw(errors.New(message)) }

func throwRange(code, message string) { jsexc.Throw(&RangeError{Code: code, Message: message}) }

// parse is zod Schema.parse: it throws the ZodError.
func parse(v jsjson.Value, schema s.Schema) jsjson.Value {
	return jsexc.Must(bundle.ParseValue(v, schema))
}

// tryValue runs fn like a TS function body whose throw becomes the error.
func tryValue(fn func() jsjson.Value) (v jsjson.Value, err error) {
	err = jsexc.Try(func() { v = fn() })
	return
}

func str(v string) jsjson.Value { return jsjson.MakeString(v) }

// setDefined is `{ key: value }` where an undefined value is no member.
func setDefined(o *jsjson.Obj, key string, v jsjson.Value) {
	if v.Kind() != jsjson.Undefined {
		o.Set(key, v)
	}
}

// LocalHeadFacts is the { head, parents } object locallyObservedHead
// returns (src/cli-app.ts:401).
func LocalHeadFacts(head string, parents []string) jsjson.Value {
	items := make([]jsjson.Value, len(parents))
	for i, p := range parents {
		items[i] = str(p)
	}
	return jsjson.MustObject("head", str(head), "parents", jsjson.MakeArray(items))
}

// Port of src/incident.ts:170 assertNonDegenerateRange.
func assertNonDegenerateRange(rangeValue jsjson.Value) {
	if rangeValue.Get("ancestor").Str() == rangeValue.Get("descendant").Str() {
		throwRange("DEGENERATE_RANGE", "incident range ancestor and descendant must be different")
	}
}

// ResolveRange ports resolveIncidentRange (src/incident.ts:185) on the
// facts `incident start` passes (src/cli-app.ts:619-621): the explicit
// --from/--to bracket when either is non-empty, otherwise the locally
// observed HEAD and its parents.
func ResolveRange(head string, parents []string, from, to string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value { return resolveIncidentRange(rangeFacts(head, parents, from, to)) })
}

func rangeFacts(head string, parents []string, from, to string) jsjson.Value {
	facts := jsjson.NewObj()
	if from != "" || to != "" {
		facts.Set("range", jsjson.MustObject("ancestor", str(from), "descendant", str(to)))
	} else {
		facts.Set("localHead", LocalHeadFacts(head, parents))
	}
	return jsjson.MakeObject(facts)
}

// Port of src/incident.ts:185 resolveIncidentRange. It selects only an
// explicit caller-provided range or one observed local parent; it never
// performs Git I/O and never guesses an upstream, PR base, or remote
// branch. A merge HEAD is rejected rather than silently using first-parent
// semantics.
func resolveIncidentRange(input jsjson.Value) jsjson.Value {
	facts := parse(input, rangeFactsSchema)

	if explicit := facts.Get("range"); explicit.Kind() != jsjson.Undefined {
		assertNonDegenerateRange(explicit)
		return jsjson.MustObject("ancestor", explicit.Get("ancestor"), "descendant", explicit.Get("descendant"), "source", str("EXPLICIT"))
	}

	localHead := facts.Get("localHead")
	if localHead.Kind() == jsjson.Undefined {
		throwRange("LOCAL_HEAD_REQUIRED", "an explicit range is required when locally observed HEAD facts are unavailable")
	}

	parents := localHead.Get("parents").Items()
	if len(parents) == 0 {
		throwRange("ROOT_HEAD", "HEAD is a root commit; select an explicit range instead of guessing a base")
	}

	if len(parents) != 1 {
		throwRange("AMBIGUOUS_HEAD", "HEAD has multiple parents; select an explicit range instead of guessing a parent")
	}

	// TS then refuses an undefined parents[0] (src/incident.ts:215, "locally
	// observed HEAD parent is unavailable"); a parsed array of one string
	// always has it, so the check is dead and not ported.
	ancestor := parents[0]
	resolved := jsjson.MustObject("ancestor", ancestor, "descendant", localHead.Get("head"))
	assertNonDegenerateRange(resolved)
	// This is the concrete, locally observed equivalent of HEAD~1 -> HEAD.
	resolved.Obj().Set("source", str("LOCAL_HEAD_PARENT"))
	return resolved
}

// Port of src/incident.ts:228 unsignedDraft: the draft without draftDigest,
// in its own key order.
func unsignedDraft(draft jsjson.Value) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range draft.Obj().Keys() {
		if k != "draftDigest" {
			out.Set(k, draft.Obj().Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

func reviewObject(proposal jsjson.Value) jsjson.Value {
	review := jsjson.MustObject(
		"required", jsjson.MakeBool(true),
		"state", str("PENDING_HUMAN_REVIEW"),
		"witnessState", str("NOT_PROPOSED"),
		"freezeState", str("NOT_FROZEN"),
		"autoFreeze", jsjson.MakeBool(false))
	if proposal.Kind() != jsjson.Undefined {
		review.Obj().Set("witnessState", str("PROPOSED"))
		review.Obj().Set("proposal", proposal)
	}
	return review
}

// CreateDraft ports createIncidentDraft (src/incident.ts:247): an
// immutable, review-only draft built from supplied facts. It does not
// create, approve, or freeze a witness itself; an adapter may bind a
// separately created proposal, but approval and freeze remain later
// explicit human actions. The result is the parsed draft, in
// IncidentDraftSchema's key order.
func CreateDraft(in DraftInput) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		createdAt := in.CreatedAt
		if createdAt == "" {
			createdAt = clock.ISO(clock.Now())
		}
		raw := jsjson.NewObj()
		raw.Set("draftId", str(in.ID))
		raw.Set("createdAt", str(createdAt))
		raw.Set("repository", str(in.Repository))
		raw.Set("command", str(in.Command))
		rangeValue, localHead := in.Range, in.LocalHead
		if rangeValue.Kind() == jsjson.Object {
			switch source := rangeValue.Get("source"); {
			case source.Kind() == jsjson.String && source.Str() == "LOCAL_HEAD_PARENT":
				rangeValue = jsjson.Value{}
				localHead = jsjson.MustObject("head", in.Range.Get("descendant"), "parents", jsjson.MakeArray([]jsjson.Value{in.Range.Get("ancestor")}))
			case source.Kind() == jsjson.String && source.Str() == "EXPLICIT":
				rangeValue = jsjson.MustObject("ancestor", in.Range.Get("ancestor"), "descendant", in.Range.Get("descendant"))
			}
		}
		setDefined(raw, "range", rangeValue)
		setDefined(raw, "localHead", localHead)
		setDefined(raw, "runtime", in.Runtime)
		setDefined(raw, "proposal", in.Proposal)
		return createIncidentDraft(jsjson.MakeObject(raw))
	})
}

// Port of src/incident.ts:247 createIncidentDraft.
func createIncidentDraft(input jsjson.Value) jsjson.Value {
	parsed := parse(input, draftInputSchema)
	facts := jsjson.NewObj()
	setDefined(facts, "range", parsed.Get("range"))
	setDefined(facts, "localHead", parsed.Get("localHead"))
	resolved := resolveIncidentRange(jsjson.MakeObject(facts))
	exactCommandDigest := gitproof.CommandDigest(parsed.Get("command").Str())
	proposal := parsed.Get("proposal")
	if proposal.Kind() != jsjson.Undefined && proposal.Get("proposalId").Str() != parsed.Get("draftId").Str() {
		throw("incident proposal binding must use the same identifier as its draft")
	}
	if proposal.Kind() != jsjson.Undefined && proposal.Get("commandDigest").Str() != exactCommandDigest {
		throw("incident proposal binding does not match the exact draft command")
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", str(DraftSchemaVersion))
	unsigned.Set("draftId", parsed.Get("draftId"))
	unsigned.Set("createdAt", parsed.Get("createdAt"))
	unsigned.Set("status", str("DRAFT_REQUIRES_HUMAN_REVIEW"))
	unsigned.Set("repository", parsed.Get("repository"))
	unsigned.Set("command", parsed.Get("command"))
	unsigned.Set("commandDigest", str(exactCommandDigest))
	unsigned.Set("range", resolved)
	setDefined(unsigned, "runtime", parsed.Get("runtime"))
	unsigned.Set("review", reviewObject(proposal))
	digest := jsexc.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))
	unsigned.Set("draftDigest", str(digest))
	return parse(jsjson.MakeObject(unsigned), DraftSchema)
}

// VerifyDraft ports verifyIncidentDraft (src/incident.ts:290): it validates
// a serialized draft and its content digest without any I/O, and returns
// { valid, errors }. It fails only where TS throws (reading the message of
// a ZodError whose issues are too deeply nested to stringify).
func VerifyDraft(draft jsjson.Value) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value { return verifyIncidentDraft(draft) })
}

func verification(errs ...string) jsjson.Value {
	items := make([]jsjson.Value, len(errs))
	for i, e := range errs {
		items[i] = str(e)
	}
	return jsjson.MustObject("valid", jsjson.MakeBool(len(errs) == 0), "errors", jsjson.MakeArray(items))
}

// Port of src/incident.ts:290 verifyIncidentDraft.
func verifyIncidentDraft(input jsjson.Value) jsjson.Value {
	parsed, issues, ok := s.Parse(DraftSchema, input)
	if !ok {
		return verification(jsexc.Concat("incident draft schema validation failed: ", s.ErrorMessage(issues)))
	}
	expectedDigest := jsexc.Must(canonical.DigestJSON(unsignedDraft(parsed)))
	if parsed.Get("draftDigest").Str() != expectedDigest {
		return verification("draft digest does not match the immutable draft payload")
	}
	return verification()
}

// errorsOf is verification.errors.join("; ").
func errorsOf(v jsjson.Value) string {
	var parts []string
	for _, e := range v.Get("errors").Items() {
		parts = append(parts, e.Str())
	}
	return jsexc.Join(parts, "; ")
}
