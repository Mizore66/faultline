package cli

import (
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/provenance"
)

// Port of src/cli-app.ts:2492 provenanceCommand.
func provenanceCommand(e *env, args []string) error {
	var action string
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "create":
		bundleOpt, err := requiredOption(args, "--bundle")
		if err != nil {
			return err
		}
		return jsexc.Try(func() {
			bundleDirectory := nodefs.Resolve(bundleOpt)
			cwd := jsexc.Must(nodefs.Cwd())
			root := provenance.DefaultRoot(cwd)
			outputOpt, ok := option(args, "--output")
			if !ok {
				outputOpt = nodefs.Join(root, "ci-receipt.json")
			}
			output := nodefs.Resolve(outputOpt)
			ci := jsexc.Must(provenance.IdentityFromEnvironment(jsexc.Must(nodefs.Cwd())))
			receipt := jsexc.Must(provenance.CreateReceipt(bundleDirectory, ci))
			path := jsexc.Must(provenance.WriteReceipt(output, receipt, root))
			e.printJSON(obj("status", "AWAITING_GITHUB_ARTIFACT_ATTESTATION", "path", path,
				"receiptDigest", receipt.Get("receiptDigest"), "proofRootDigest", receipt.Get("proof", "rootDigest"),
				"signing", "Run actions/attest@v4 with this exact path as subject-path before calling it signed provenance.",
				"limitation", receipt.Get("limitation")))
		})
	case "verify":
		trustOpt, err := requiredOption(args, "--trust")
		if err != nil {
			return err
		}
		var verification jsjson.Value
		err = jsexc.Try(func() {
			trustFile := nodefs.Resolve(trustOpt)
			trust := jsexc.Must(provenance.ReadTrust(trustFile))
			// A relative root in the policy is relative to the policy, not the
			// caller's current directory or an attacker-supplied bundle. The
			// spread keeps trustedRootFile in its parsed position.
			resolved := jsjson.NewObj()
			for _, k := range trust.Obj().Keys() {
				resolved.Set(k, trust.Obj().Field(k))
			}
			resolved.Set("trustedRootFile", jsjson.MakeString(nodefs.Resolve(nodefs.Dirname(trustFile), trust.Get("trustedRootFile").Str())))
			bundleDirectory := nodefs.Resolve(jsexc.Must(requiredOption(args, "--bundle")))
			receiptFile := nodefs.Resolve(jsexc.Must(requiredOption(args, "--receipt")))
			attestationBundleFile := nodefs.Resolve(jsexc.Must(requiredOption(args, "--attestation-bundle")))
			verification = provenance.VerifySigned(bundleDirectory, receiptFile, attestationBundleFile, jsjson.MakeObject(resolved))
		})
		if err != nil {
			return err
		}
		e.printJSON(verification)
		if verification.Get("valid").Bool() {
			e.exitCode = 0
		} else {
			e.exitCode = 1
		}
		return nil
	default:
		return fail("Usage: fl provenance create|verify ...")
	}
}
