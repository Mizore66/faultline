package proofview

import (
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// loadPrevention ports the prevention half of loadVerifiedIncidentAttachments
// using the artifact-returning shared verifier twice (no separate read).
// Prevention binds to witnessDigest, unlike repair's frozenDigest.
func loadPrevention(directory string, expected *string, investigation, frozenWitness jsjson.Value, proofRoot string, deps attachmentDependencies) *VerifiedPreventionAttachment {
	root := nodefs.Resolve(directory)
	want, provided := optionalDigest(expected)
	first, manifest, body := deps.prevention(root, want, provided)
	if !first.Valid || absent(manifest) || absent(body) || first.RootDigest == nil {
		attachmentFailure(jsexc.Concat("Refusing to attach an invalid prevention proof: ", jsexc.Join(first.Errors, "; ")))
	}
	if body.Get("originalProofRoot").Str() != proofRoot {
		attachmentFailure("Refusing to attach a prevention proof bound to a different Git proof root.")
	}
	if body.Get("frozenWitnessDigest").Str() != frozenWitness.Get("witnessDigest").Str() {
		attachmentFailure("Refusing to attach a prevention proof for a different frozen witness.")
	}
	if investigationDigest := body.Get("investigationDigest"); investigationDigest.Kind() != jsjson.Undefined &&
		investigationDigest.Str() != bundle.Must(canonical.DigestJSON(investigation)) {
		attachmentFailure("Refusing to attach a prevention proof from a different Git investigation.")
	}
	second, manifest2, body2 := deps.prevention(root, want, provided)
	if !second.Valid || absent(manifest2) || absent(body2) || second.RootDigest == nil ||
		*second.RootDigest != *first.RootDigest || !bundle.SameCanonical(body, body2) {
		detail := jsexc.Join(second.Errors, "; ")
		if detail == "" {
			detail = "digest changed"
		}
		attachmentFailure(jsexc.Concat("Refusing to attach a prevention proof that changed during verification: ", detail))
	}
	return &VerifiedPreventionAttachment{Manifest: manifest2, Prevention: body2, RootDigest: *second.RootDigest, ExternalDigestStatus: second.ExternalRootStatus}
}
