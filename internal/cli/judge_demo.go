package cli

import (
	"context"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/demo"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// verifyGeneratedBundle independently verifies a bundle just written and
// requires its verified root to equal the written root.
func verifyGeneratedBundle(written demo.Written) (bundle.Result, error) {
	verification := demo.Verify(written.Directory, "", false)
	if !verification.Valid {
		return verification, fail("Generated proof bundle failed integrity verification: %s", strings.Join(verification.Errors, "; "))
	}
	if verification.RootDigest == nil || *verification.RootDigest != written.RootDigest {
		return verification, fail("Generated proof bundle root digest differs from verified root")
	}
	return verification, nil
}

// judgeDemoCommand serves native proposal inspection and --export-only. The
// interactive judge-demo server is not ported. --port has no effect.
func judgeDemoCommand(e *env, args []string) error {
	if hasFlag(args, "--native-proposal") {
		for _, flag := range []string{"--rerun-all", "--export-only", "--approve-native", "--output"} {
			if hasFlag(args, flag) {
				return fail("Native proposal inspection cannot be combined with execution or output options.")
			}
		}
		e.out(jsjson.StringifyIndent(demofixture.NativeWitnessProposal(), "  ") + "\n")
		return nil
	}
	if !hasFlag(args, "--export-only") {
		return fail("not yet ported: judge-demo server")
	}
	rerun := hasFlag(args, "--rerun-all")
	if !rerun && hasFlag(args, "--approve-native") {
		return fail("--approve-native requires --rerun-all.")
	}
	if rerun && !hasFlag(args, "--approve-native") {
		return fail("Native RERUN requires --approve-native <proposal-digest>.")
	}
	approval := ""
	if rerun {
		var err error
		if approval, err = requiredOption(args, "--approve-native"); err != nil {
			return err
		}
	}
	output := nodefs.Resolve(".faultline/bundles/judge-demo")
	if _, ok := option(args, "--output"); ok {
		value, err := requiredOption(args, "--output")
		if err != nil {
			return err
		}
		output = nodefs.Resolve(value)
	}

	analysis := demofixture.CreateReplayAnalysis()
	var provenance jsjson.Value
	if rerun {
		result, err := demofixture.RunNativeAnalysis(context.Background(), demofixture.NativeApproval{
			ProposalDigest: approval, ApprovedAt: clock.ISO(clock.Now()),
		})
		if err != nil {
			return err
		}
		analysis, provenance = result.Analysis, result.Provenance
	}
	written, err := demo.Write(output, analysis, demo.WriteOptions{})
	if err != nil {
		return err
	}
	verification, err := verifyGeneratedBundle(written)
	if err != nil {
		return err
	}
	sidecar := ""
	if rerun {
		if sidecar, err = demo.WriteNativeEvidence(written, verification, analysis, provenance); err != nil {
			return err
		}
	} else if err := demo.RemoveNativeEvidence(written.Directory); err != nil {
		return err
	}

	mode := "cached replay"
	if rerun {
		mode = "executed"
	}
	boundary := "not certified from cached replay"
	for _, t := range analysis.Get("transitions").Items() {
		if t.Get("kind").Str() == "PASS_TO_FAIL" && t.Get("stable").Bool() {
			boundary = t.Get("beforeStateId").Str() + " → " + t.Get("afterStateId").Str()
			break
		}
	}
	e.out("FaultLine " + mode + " investigation prepared.\n")
	e.out("Stable sample boundary: " + boundary + "\n")
	e.out("Proof bundle self-consistency: VALID (" + strconv.Itoa(verification.CheckedFiles) + " declared files)\n")
	e.out("Bundle root: " + written.RootDigest + "\n")
	e.out("Proof bundle: " + describeBundlePath(written.Directory) + "\n")
	if rerun {
		e.out("Native execution evidence: " + sidecar + "\n")
	}
	return nil
}
