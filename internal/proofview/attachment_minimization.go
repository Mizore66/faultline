package proofview

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// validateMinimizationBinding accumulates errors in TS order.
func validateMinimizationBinding(result, investigation, frozen jsjson.Value) []string {
	var errs []string
	w := result.Get("witness")
	if w.Kind() == jsjson.Null {
		errs = append(errs, "minimization has no frozen-witness summary")
	} else {
		if w.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() {
			errs = append(errs, "minimization frozen digest does not match the Git proof witness")
		}
		if w.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
			errs = append(errs, "minimization witness digest does not match the Git proof witness")
		}
	}
	before, after := result.Get("before"), result.Get("after")
	if before.Kind() == jsjson.Null || after.Kind() == jsjson.Null {
		errs = append(errs, "minimization has no resolved before/after states")
	} else {
		matches := false
		for _, tr := range investigation.Get("transitions").Items() {
			if tr.Get("kind").Str() == "PASS_TO_FAIL" &&
				tr.Get("before", "commit").Str() == before.Get("commit").Str() &&
				tr.Get("before", "tree").Str() == before.Get("tree").Str() &&
				tr.Get("after", "commit").Str() == after.Get("commit").Str() &&
				tr.Get("after", "tree").Str() == after.Get("tree").Str() {
				matches = true
				break
			}
		}
		if !matches {
			errs = append(errs, "minimization range is not a stable PASS_TO_FAIL transition in the Git proof package")
		}
	}
	return errs
}

// loadMinimization ports the minimization half of loadVerifiedIncidentAttachments:
// the exact existing verifier twice, with the binding between the passes.
func loadMinimization(file string, expected *string, investigation, frozenWitness jsjson.Value, deps attachmentDependencies) *VerifiedMinimizationAttachment {
	want, provided := optionalDigest(expected)
	first := deps.minimize(nodefs.Resolve(file), want, provided)
	if !first.Valid || first.Result == nil || first.ResultDigest == nil {
		attachmentFailure(jsexc.Concat("Refusing to attach an invalid minimization record: ", jsexc.Join(first.Errors, "; ")))
	}
	if linkage := validateMinimizationBinding(*first.Result, investigation, frozenWitness); len(linkage) != 0 {
		attachmentFailure(jsexc.Concat("Refusing to attach a minimization record for a different incident: ", jsexc.Join(linkage, "; ")))
	}
	second := deps.minimize(nodefs.Resolve(file), want, provided)
	if !second.Valid || second.Result == nil || second.ResultDigest == nil ||
		*second.ResultDigest != *first.ResultDigest || !bundle.SameCanonical(*second.Result, *first.Result) {
		detail := jsexc.Join(second.Errors, "; ")
		if detail == "" {
			detail = "digest changed"
		}
		attachmentFailure(jsexc.Concat("Refusing to attach a minimization record that changed during verification: ", detail))
	}
	return &VerifiedMinimizationAttachment{Result: *second.Result, ResultDigest: *second.ResultDigest, ExternalDigestStatus: second.ExternalDigestStatus}
}
