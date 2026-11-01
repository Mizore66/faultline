package proofview

import (
	"errors"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/minimize"
	"github.com/Mizore66/faultline/internal/repair"
)

type repairContents struct{ packet, brief jsjson.Value }

// attachmentDependencies are the verifiers an attachment load calls. They are
// created per call; there is no package-level replacement.
type attachmentDependencies struct {
	minimize   func(string, string, bool) minimize.Verification
	repair     func(string, *string) repair.BriefVerification
	readRepair func(string) repairContents
	prevention func(string, string, bool) (bundle.Result, jsjson.Value, jsjson.Value)
}

func realAttachmentDependencies() attachmentDependencies {
	return attachmentDependencies{
		minimize:   minimize.VerifyFileForAttachment,
		repair:     repair.VerifyBriefArtifactForAttachment,
		readRepair: readRepairContents,
		prevention: prevention.VerifyWithArtifactsForAttachment,
	}
}

// optionalDigest maps a nil pointer (undefined) to "not provided"; a supplied
// empty string stays provided.
func optionalDigest(expected *string) (string, bool) {
	if expected == nil {
		return "", false
	}
	return *expected, true
}

func attachmentFailure(message string) { jsexc.Throw(errors.New(message)) }

// absent is the TS undefined/null test of an optional parsed value.
func absent(v jsjson.Value) bool {
	return v.Kind() == jsjson.Undefined || v.Kind() == jsjson.Null
}

// loadAttachments ports loadVerifiedIncidentAttachments: minimization, repair,
// then prevention, sequentially. A nil path is undefined; an expected digest
// without a path touches nothing.
func loadAttachments(options AttachmentOptions, investigation, frozenWitness jsjson.Value, proofRoot *string, deps attachmentDependencies) VerifiedAttachments {
	var attachments VerifiedAttachments
	if options.MinimizationFile != nil {
		attachments.Minimization = loadMinimization(*options.MinimizationFile, options.ExpectedMinimizationDigest, investigation, frozenWitness, deps)
	}
	if options.RepairDirectory != nil {
		attachments.Repair = loadRepair(*options.RepairDirectory, options.ExpectedRepairDigest, investigation, frozenWitness, deps)
	}
	if options.PreventionDirectory != nil {
		if proofRoot == nil {
			attachmentFailure("Prevention attachment requires the verified Git proof root digest.")
		}
		attachments.Prevention = loadPrevention(*options.PreventionDirectory, options.ExpectedPreventionDigest, investigation, frozenWitness, *proofRoot, deps)
	}
	return attachments
}
