// Package redaction is src/redaction.ts: best-effort removal of recognized
// credentials from text and JSON values, replaced by digest-bearing
// placeholders, with a report that never claims full coverage.
//
// captureAllowlistedEvent and its helpers (405–475) are not ported: nothing
// in src/ calls them. The non-JSON branches of redactValueInContext (cycles,
// accessors, non-plain objects, bigint/symbol/function/undefined) cannot be
// reached from a jsjson.Value and are left out.
package redaction

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// Port of src/redaction.ts:9 REDACTION_LIMITATIONS.
var limitations = []string{
	"LIMITED_COVERAGE: only the documented credential formats and secret-named fields are recognized.",
	"UNKNOWN_PATTERNS: encoded, split, encrypted, binary, transformed, or unrecognized credentials may remain.",
	"DIGEST_LIMITATION: a digest-bearing placeholder supports correlation; it is not encryption or proof that a secret is unrecoverable elsewhere.",
}

// Port of src/redaction.ts:15 REDACTION_PLACEHOLDER_PREFIX.
const placeholderPrefix = "<FAULTLINE_REDACTED"

// Occurrence is one redaction (src/redaction.ts:30 RedactionOccurrence).
type Occurrence struct {
	Kind, Confidence, Digest, Placeholder, Location string
}

// Report is src/redaction.ts:40 RedactionReport. Kinds lists each
// occurrence's kind, in occurrence order.
type Report struct {
	Redacted                                    bool
	Count                                       int
	HighConfidenceCount, LimitedConfidenceCount int
	Kinds                                       []string
	Occurrences                                 []Occurrence
	Warnings                                    []string
}

// JSON is the report as TS returns it, keys in reportFor's order.
func (r Report) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("redacted", jsjson.MakeBool(r.Redacted))
	o.Set("redactedCount", jsjson.MakeNumber(float64(r.Count)))
	o.Set("highConfidenceCount", jsjson.MakeNumber(float64(r.HighConfidenceCount)))
	o.Set("limitedConfidenceCount", jsjson.MakeNumber(float64(r.LimitedConfidenceCount)))
	o.Set("coverage", jsjson.MakeString("LIMITED"))
	occurrences := make([]jsjson.Value, len(r.Occurrences))
	for i, oc := range r.Occurrences {
		e := jsjson.NewObj()
		e.Set("kind", jsjson.MakeString(oc.Kind))
		e.Set("confidence", jsjson.MakeString(oc.Confidence))
		e.Set("digest", jsjson.MakeString(oc.Digest))
		e.Set("placeholder", jsjson.MakeString(oc.Placeholder))
		e.Set("location", jsjson.MakeString(oc.Location))
		occurrences[i] = jsjson.MakeObject(e)
	}
	o.Set("occurrences", jsjson.MakeArray(occurrences))
	o.Set("unknownPatternWarnings", stringArray(r.Warnings))
	o.Set("limitations", stringArray(limitations))
	return jsjson.MakeObject(o)
}

func stringArray(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

// Port of src/redaction.ts:95 Candidate. start and end are UTF-16 offsets.
type candidate struct {
	start, end       int
	value            string
	kind, confidence string
	priority         int
}

const unsupportedValueWarning = "UNSUPPORTED_VALUE: non-JSON values are replaced before event capture."

// Port of src/redaction.ts:104 RedactionContext.
type context struct {
	occurrences []Occurrence
	warnings    []string // a set, in insertion order
}

// Port of src/redaction.ts:109-151.
var (
	placeholderPattern  = jsre.MustCompile(`^<FAULTLINE_REDACTED:[A-Z_]+:sha256:[a-f0-9]{64}>$`, "")
	sensitiveFieldNames = map[string]bool{
		"apikey": true, "xapikey": true, "secret": true, "secretkey": true, "token": true, "accesstoken": true,
		"refreshtoken": true, "idtoken": true, "password": true, "passwd": true, "pwd": true, "credential": true,
		"credentials": true, "authorization": true, "proxyauthorization": true, "accesskey": true, "privatekey": true,
		"clientsecret": true, "sessiontoken": true,
	}
	sensitiveFieldSuffixes = []string{"apikey", "secret", "secretkey", "token", "password", "credential", "credentials", "privatekey", "clientsecret"}

	privateKeyBlock     = jsre.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, "g")
	bearerToken         = jsre.MustCompile(`\bBearer[ \t]+([A-Za-z0-9\-._~+/]+=*)`, "gi")
	basicToken          = jsre.MustCompile(`\bBasic[ \t]+([A-Za-z0-9+/]{4,}={0,2})`, "gi")
	openAIKey           = jsre.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,}\b`, "g")
	githubToken         = jsre.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`, "g")
	awsAccessKey        = jsre.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`, "g")
	awsSecretAssignment = jsre.MustCompile(`\b(?:aws[-_])?secret[-_]?access[-_]?key\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))`, "gi")
	genericAssignment   = jsre.MustCompile(`\b(?:api[-_]?key|apikey|secret(?:[-_]?key)?|token|access[-_]?token|refresh[-_]?token|id[-_]?token|password|passwd|pwd|credential(?:s)?|authorization|access[-_]?key|private[-_]?key|client[-_]?secret|session[-_]?token)\b\s*(?:=|:)\s*(?:"((?:\\.|[^"\\\r\n])*)"|'((?:\\.|[^'\\\r\n])*)'|([^\s,;}&]+))`, "gi")
	literalValue        = jsre.MustCompile(`^(?:null|undefined|true|false)$`, "i")
)

// Port of src/redaction.ts:153 digest.
func digest(value string) string { return "sha256:" + canonical.SHA256Hex(value) }

// Port of src/redaction.ts:166 locationForText.
func locationForText(base string, start, end int) string {
	return base + "[" + strconv.Itoa(start) + ":" + strconv.Itoa(end) + "]"
}

// Port of src/redaction.ts:170 pushCandidate.
func pushCandidate(candidates []candidate, start int, value, kind, confidence string, priority int) []candidate {
	if value == "" || placeholderPattern.Test(value) {
		return candidates
	}
	return append(candidates, candidate{start, start + jsstr.UTF16Len(value), value, kind, confidence, priority})
}

// Port of src/redaction.ts:182 addWholeMatchCandidates.
func addWholeMatchCandidates(candidates []candidate, text string, pattern *jsre.Regexp, kind, confidence string, priority int) []candidate {
	units := jsstr.ToUTF16(text)
	for _, m := range pattern.FindAll(text) {
		candidates = pushCandidate(candidates, m.Start, jsstr.FromUTF16(units[m.Start:m.End]), kind, confidence, priority)
	}
	return candidates
}

// Port of src/redaction.ts:198 addCapturedValueCandidates.
func addCapturedValueCandidates(candidates []candidate, text string, pattern *jsre.Regexp, kind, confidence string, priority int) []candidate {
	units := jsstr.ToUTF16(text)
	for _, m := range pattern.FindAll(text) {
		var value *string
		for _, g := range m.Groups {
			if g != nil {
				value = g
				break
			}
		}
		if value == nil || *value == "" || literalValue.Test(*value) {
			continue
		}
		// TS skips a negative offset, which cannot happen: every capture
		// lies inside the match.
		relativeOffset := lastIndexOf(units[m.Start:m.End], jsstr.ToUTF16(*value))
		candidates = pushCandidate(candidates, m.Start+relativeOffset, *value, kind, confidence, priority)
	}
	return candidates
}

// lastIndexOf is String.prototype.lastIndexOf on code units.
func lastIndexOf(haystack, needle []uint16) int {
	for i := len(haystack) - len(needle); i >= 0; i-- {
		match := true
		for j, u := range needle {
			if haystack[i+j] != u {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Port of src/redaction.ts:217 collectCandidates.
func collectCandidates(text string) []candidate {
	var c []candidate
	c = addWholeMatchCandidates(c, text, privateKeyBlock, "PRIVATE_KEY_BLOCK", "HIGH", 100)
	c = addCapturedValueCandidates(c, text, bearerToken, "AUTHORIZATION_BEARER", "HIGH", 90)
	c = addCapturedValueCandidates(c, text, basicToken, "AUTHORIZATION_BASIC", "HIGH", 90)
	c = addWholeMatchCandidates(c, text, openAIKey, "OPENAI_API_KEY", "HIGH", 80)
	c = addWholeMatchCandidates(c, text, githubToken, "GITHUB_TOKEN", "HIGH", 80)
	c = addWholeMatchCandidates(c, text, awsAccessKey, "AWS_ACCESS_KEY_ID", "HIGH", 80)
	c = addCapturedValueCandidates(c, text, awsSecretAssignment, "AWS_SECRET_ACCESS_KEY", "HIGH", 70)
	c = addCapturedValueCandidates(c, text, genericAssignment, "GENERIC_SECRET_ASSIGNMENT", "LIMITED", 10)

	v8sort.Sort(c, func(l, r candidate) int {
		if d := l.start - r.start; d != 0 {
			return d
		}
		if d := r.priority - l.priority; d != 0 {
			return d
		}
		if d := r.end - l.end; d != 0 {
			return d
		}
		return canonical.LocaleCompare(l.kind, r.kind)
	})
	var accepted []candidate
	for _, cand := range c {
		overlaps := false
		for _, existing := range accepted {
			if cand.start < existing.end && cand.end > existing.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			accepted = append(accepted, cand)
		}
	}
	return accepted
}

// Port of src/redaction.ts:239 recordRedaction.
func (ctx *context) recordRedaction(c candidate, location string) string {
	valueDigest := digest(c.value)
	placeholder := placeholderPrefix + ":" + c.kind + ":" + valueDigest + ">"
	ctx.occurrences = append(ctx.occurrences, Occurrence{c.kind, c.confidence, valueDigest, placeholder, location})
	return placeholder
}

// Port of src/redaction.ts:252 redactStringInContext.
func (ctx *context) redactString(value, location string) string {
	candidates := collectCandidates(value)
	if len(candidates) == 0 {
		return value
	}
	units := jsstr.ToUTF16(value)
	cursor := 0
	var out []uint16
	for _, c := range candidates {
		out = append(out, units[cursor:c.start]...)
		out = append(out, jsstr.ToUTF16(ctx.recordRedaction(c, locationForText(location, c.start, c.end)))...)
		cursor = c.end
	}
	return jsstr.FromUTF16(append(out, units[cursor:]...))
}

// Port of src/redaction.ts:266 initialContext.
func initialContext() *context {
	return &context{warnings: []string{limitations[1]}}
}

// Port of src/redaction.ts:273 reportFor.
func (ctx *context) report() Report {
	high := 0
	kinds := make([]string, len(ctx.occurrences))
	for i, o := range ctx.occurrences {
		kinds[i] = o.Kind
		if o.Confidence == "HIGH" {
			high++
		}
	}
	warnings := append([]string(nil), ctx.warnings...)
	v8sort.Sort(warnings, jsstr.CompareUTF16)
	return Report{
		Redacted:               len(ctx.occurrences) > 0,
		Count:                  len(ctx.occurrences),
		HighConfidenceCount:    high,
		LimitedConfidenceCount: len(ctx.occurrences) - high,
		Kinds:                  kinds,
		Occurrences:            ctx.occurrences,
		Warnings:               warnings,
	}
}

// RedactText redacts credentials found in one log line, message, or
// serialized API packet.
// Port of src/redaction.ts:289 redactText.
func RedactText(text, location string) (string, Report) {
	ctx := initialContext()
	out := ctx.redactString(text, location)
	return out, ctx.report()
}

// Port of src/redaction.ts:294 fieldKind.
func fieldKind(key string) string {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(jscase.Lower(key))
	switch normalized {
	case "awssecretaccesskey", "secretaccesskey":
		return "AWS_SECRET_ACCESS_KEY"
	case "awsaccesskeyid", "accesskeyid":
		return "AWS_ACCESS_KEY_ID"
	}
	if sensitiveFieldNames[normalized] {
		return "GENERIC_SECRET_ASSIGNMENT"
	}
	for _, suffix := range sensitiveFieldSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return "GENERIC_SECRET_ASSIGNMENT"
		}
	}
	return ""
}

// Port of src/redaction.ts:313 appendFieldRedaction.
func (ctx *context) appendFieldRedaction(value, kind, location string) string {
	confidence := "HIGH"
	if kind == "GENERIC_SECRET_ASSIGNMENT" {
		confidence = "LIMITED"
	}
	return ctx.recordRedaction(candidate{0, jsstr.UTF16Len(value), value, kind, confidence, 0}, location)
}

// Port of src/redaction.ts:325 redactValueInContext (JSON values only).
func (ctx *context) redactValue(value jsjson.Value, location string) jsjson.Value {
	switch value.Kind() {
	case jsjson.String:
		return jsjson.MakeString(ctx.redactString(value.Str(), location))
	case jsjson.Undefined:
		// src/redaction.ts:328: undefined (an absent member a caller passes,
		// such as a missing incidentPacket) becomes a placeholder string.
		if !slices.Contains(ctx.warnings, unsupportedValueWarning) {
			ctx.warnings = append(ctx.warnings, unsupportedValueWarning)
		}
		return jsjson.MakeString("<FAULTLINE_UNSUPPORTED_VALUE:undefined>")
	case jsjson.Array:
		items := value.Items()
		out := make([]jsjson.Value, len(items))
		for i, item := range items {
			out[i] = ctx.redactValue(item, location+"["+strconv.Itoa(i)+"]")
		}
		return jsjson.MakeArray(out)
	case jsjson.Object:
		obj := value.Obj()
		out := jsjson.NewObj()
		// Canonical key traversal makes both the transformed payload and
		// its report deterministic.
		for _, key := range canonical.SortLocale(obj.Keys()) {
			child := obj.Field(key)
			childLocation := location + "." + key
			if kind := fieldKind(key); kind != "" && child.Kind() == jsjson.String && !placeholderPattern.Test(child.Str()) {
				redacted := ctx.redactString(child.Str(), childLocation)
				if redacted == child.Str() {
					redacted = ctx.appendFieldRedaction(child.Str(), kind, childLocation)
				}
				out.Set(key, jsjson.MakeString(redacted))
			} else {
				out.Set(key, ctx.redactValue(child, childLocation))
			}
		}
		return jsjson.MakeObject(out)
	}
	return value
}

// RedactValue redacts a JSON value without mutating it. TS's redactValue
// always starts at "$"; callers pass that.
// Port of src/redaction.ts:391 redactValue.
func RedactValue(value jsjson.Value, location string) (jsjson.Value, Report) {
	ctx := initialContext()
	out := ctx.redactValue(value, location)
	return out, ctx.report()
}
