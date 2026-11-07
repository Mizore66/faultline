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
	"github.com/Mizore66/faultline/internal/proofview"
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

// Package-private seams let the CLI tests stop publication at each real
// filesystem boundary without adding test behavior to the built executable.
var (
	judgeDemoRunNativeAnalysis    = demofixture.RunNativeAnalysis
	judgeDemoWriteBundle          = demo.Write
	judgeDemoWriteNativeEvidence  = demo.WriteNativeEvidence
	judgeDemoRemoveNativeEvidence = demo.RemoveNativeEvidence
	judgeDemoStartIncidentServer  = proofview.StartIncidentServer
	judgeDemoAwaitInterrupt       = awaitBundleInterrupt
)

type judgeDemoPublication struct {
	written      demo.Written
	verification bundle.Result
	analysis     jsjson.Value
	sidecar      string
}

// publishJudgeDemo performs the complete artifact sequence. In particular, a
// native callback does not expose its analysis until the bundle, independent
// verification, root match, and native sidecar have all completed.
func publishJudgeDemo(ctx context.Context, output string, rerun bool, approval demofixture.NativeApproval) (judgeDemoPublication, error) {
	var publication judgeDemoPublication
	analysis := demofixture.CreateReplayAnalysis()
	var provenance jsjson.Value
	if rerun {
		result, err := judgeDemoRunNativeAnalysis(ctx, approval)
		if err != nil {
			return publication, err
		}
		analysis, provenance = result.Analysis, result.Provenance
	}
	written, err := judgeDemoWriteBundle(output, analysis, demo.WriteOptions{})
	if err != nil {
		return publication, err
	}
	publication.written = written
	verification, err := verifyGeneratedBundle(written)
	if err != nil {
		return publication, err
	}
	publication.verification = verification
	publication.analysis = analysis
	if rerun {
		if publication.sidecar, err = judgeDemoWriteNativeEvidence(written, verification, analysis, provenance); err != nil {
			return publication, err
		}
	} else if err := judgeDemoRemoveNativeEvidence(written.Directory); err != nil {
		return publication, err
	}
	return publication, nil
}

func writeJudgeDemoSummary(e *env, publication judgeDemoPublication, rerun bool) {
	mode := "cached replay"
	if rerun {
		mode = "executed"
	}
	boundary := "not certified from cached replay"
	for _, transition := range publication.analysis.Get("transitions").Items() {
		if transition.Get("kind").Str() == "PASS_TO_FAIL" && transition.Get("stable").Bool() {
			boundary = transition.Get("beforeStateId").Str() + " → " + transition.Get("afterStateId").Str()
			break
		}
	}
	e.out("FaultLine " + mode + " investigation prepared.\n")
	e.out("Stable sample boundary: " + boundary + "\n")
	e.out("Proof bundle self-consistency: VALID (" + strconv.Itoa(publication.verification.CheckedFiles) + " declared files)\n")
	e.out("Bundle root: " + publication.written.RootDigest + "\n")
	e.out("Proof bundle: " + describeBundlePath(publication.written.Directory) + "\n")
	if rerun {
		e.out("Native execution evidence: " + publication.sidecar + "\n")
	}
}

// judgeDemoCommand publishes the selected initial analysis, then either returns
// for --export-only or serves the interactive incident page. An approval given
// with interactive REPLAY enables later native RERUNs; export-only REPLAY keeps
// its original refusal.
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

	rerun := hasFlag(args, "--rerun-all")
	exportOnly := hasFlag(args, "--export-only")
	approved := hasFlag(args, "--approve-native")
	if exportOnly && !rerun && approved {
		return fail("--approve-native requires --rerun-all.")
	}
	if rerun && !approved {
		return fail("Native RERUN requires --approve-native <proposal-digest>.")
	}
	approvalText := ""
	var err error
	if approved {
		if approvalText, err = requiredOption(args, "--approve-native"); err != nil {
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
	approval := demofixture.NativeApproval{}
	if approved {
		approval = demofixture.NativeApproval{ProposalDigest: approvalText, ApprovedAt: clock.ISO(clock.Now())}
		if err := demofixture.ValidateNativeApproval(approval); err != nil {
			return err
		}
	}

	publication, err := publishJudgeDemo(context.Background(), output, rerun, approval)
	if err != nil {
		return err
	}
	writeJudgeDemoSummary(e, publication, rerun)
	if exportOnly {
		return nil
	}
	port, ok := option(args, "--port")
	if !ok {
		port = "4173"
	}
	return judgeDemoAwaitInterrupt(func() (func() error, error) {
		options := proofview.IncidentServerOptions{Analysis: publication.analysis, RerunEnabled: approved}
		if approved {
			options.Rerun = func(ctx context.Context) (jsjson.Value, error) {
				rerunPublication, err := publishJudgeDemo(ctx, output, true, approval)
				if err != nil {
					return jsjson.Value{}, err
				}
				return rerunPublication.analysis, nil
			}
		}
		server, err := judgeDemoStartIncidentServer(options, jsjson.ToNumber(port))
		if err != nil {
			return nil, err
		}
		e.out("🚀 Launching FaultLine Judge Demo at " + server.URL() + "...\n")
		e.out("Press Ctrl+C to stop.\n")
		return server.Close, nil
	})
}
