package cli

import (
	"errors"

	"github.com/Mizore66/faultline/internal/attest"
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

type proofSummary struct {
	rootDigest    string
	witnessDigest *string
	kind          string
}

func witnessDigestOf(manifest jsjson.Value) *string {
	if manifest.Kind() == jsjson.Undefined {
		return nil
	}
	w := manifest.Get("witnessDigest")
	if w.Kind() == jsjson.Undefined {
		return nil
	}
	s := w.Str()
	return &s
}

// Port of src/cli-app.ts:2431 proofBundleSummary.
func proofBundleSummary(directory string) (proofSummary, error) {
	root := nodefs.Resolve(directory)
	schemaVersion := ""
	// Every read or parse error falls through to the offline verifier.
	_ = jsexc.Try(func() {
		value := jsexc.Must(bundle.ParseJSONFile(nodefs.Join(root, "manifest.json")))
		if value.Kind() == jsjson.Object {
			if v := value.Get("schemaVersion"); v.Kind() == jsjson.String {
				schemaVersion = v.Str()
			}
		}
	})
	if schemaVersion == "faultline.git-proof-bundle.v1" {
		result, manifest := gitproof.VerifyWithManifest(root, "", false)
		if !result.Valid || result.RootDigest == nil {
			return proofSummary{}, errors.New(jsexc.Concat("Git proof bundle is invalid: ", joinErrors(result.Errors)))
		}
		return proofSummary{*result.RootDigest, witnessDigestOf(manifest), "GIT"}, nil
	}
	if schemaVersion == "faultline.turn-proof-bundle.v1" {
		return proofSummary{}, fail("Attestation create currently supports Git proof bundles only. Verify turn packages with `fl verify` (EXPERIMENTAL_TURN).")
	}
	result, manifest := demo.VerifyWithManifest(root, "", false)
	if !result.Valid || result.RootDigest == nil {
		return proofSummary{}, errors.New(jsexc.Concat("Proof bundle is invalid: ", joinErrors(result.Errors)))
	}
	return proofSummary{*result.RootDigest, witnessDigestOf(manifest), "DEMO"}, nil
}

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out = jsexc.Concat(out, "; ")
		}
		out = jsexc.Concat(out, e)
	}
	return out
}

func optionPtr(args []string, flag string) *string {
	if v, ok := option(args, flag); ok {
		return &v
	}
	return nil
}

// Port of src/cli-app.ts:2461 attestationCommand.
func attestCommand(e *env, args []string) error {
	var action, receiptID string
	if len(args) > 0 {
		action = args[0]
	}
	if len(args) > 1 {
		receiptID = args[1]
	}
	// attestationStore: resolve(option("--store") ?? defaultAttestationStore()).
	store, ok := option(args, "--store")
	if ok {
		store = nodefs.Resolve(store)
	} else {
		store = nodefs.Resolve(attest.DefaultStore())
	}
	switch action {
	case "create":
		bundlePath, err := requiredOption(args, "--bundle")
		if err != nil {
			return err
		}
		summary, err := proofBundleSummary(bundlePath)
		if err != nil {
			return err
		}
		in := attest.CreationInput{BundleRootDigest: summary.rootDigest, WitnessDigest: summary.witnessDigest}
		if in.ReceiptID, err = requiredOption(args, "--receipt"); err != nil {
			return err
		}
		if in.Subject, err = requiredOption(args, "--subject"); err != nil {
			return err
		}
		if in.Issuer, err = requiredOption(args, "--issuer"); err != nil {
			return err
		}
		in.SourceDigest = optionPtr(args, "--source-digest")
		in.GitCheckpointDigest = optionPtr(args, "--checkpoint-digest")
		written, err := attest.Write(store, in, attest.DefaultStore())
		if err != nil {
			return err
		}
		e.printJSON(obj("status", "ATTESTED", "path", written.Path, "receiptDigest", written.ReceiptDigest,
			"bundleKind", summary.kind, "limitation", written.Attestation.Get("limitation")))
		return nil
	case "verify":
		if receiptID == "" {
			return fail("Usage: fl attest verify <receipt-id> [--expect-digest <sha256:...>] [--store <directory>]")
		}
		verification := attest.VerifyStored(store, receiptID, optionPtr(args, "--expect-digest"), attest.DefaultStore())
		e.printJSON(verification.JSON())
		if verification.Valid {
			e.exitCode = 0
		} else {
			e.exitCode = 1
		}
		return nil
	default:
		return fail("Usage: fl attest create|verify ...")
	}
}
