package demofixture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

// NativeApproval records the caller's authorization of the complete proposal.
// The caller must obtain a real authorization event; this API authenticates no person.
type NativeApproval struct {
	ProposalDigest string
	ApprovedAt     string
}

type NativeResult struct {
	Analysis   jsjson.Value
	Provenance jsjson.Value
}

type NativeAnalysisError struct {
	Kind    string
	Ordinal int
	StateID string
	Err     error
}

func (e *NativeAnalysisError) Error() string {
	return fmt.Sprintf("native analysis %s (invocation %d, state %q): %v", e.Kind, e.Ordinal, e.StateID, e.Err)
}
func (e *NativeAnalysisError) Unwrap() error { return e.Err }

type nativeExecutor interface {
	Environment() jsjson.Value
	Run(context.Context, nativefixture.Materialized) (nativefixture.Capture, error)
}
type nativeDeps struct {
	newExecutor func() (nativeExecutor, error)
	now         func() time.Time
	nonce       func() string
}
type nativeSession struct {
	proposal, approval, witness, program jsjson.Value
	programDigest                        string
	environmentPreimage                  jsjson.Value
	environmentDigest, nonce             string
	executor                             nativeExecutor
	ordinal                              int
	receipts                             []jsjson.Value
	seenIDs                              map[string]struct{}
}

// NativeWitnessProposal describes only the fixed reviewed Go model. It neither
// executes a child nor reuses the historical JavaScript witness's approval.
func NativeWitnessProposal() jsjson.Value {
	program := nativefixture.Program()
	programDigest := digest(program)
	incident := jsjson.MustObject(
		"symptom", jsjson.MakeString("A completed refund can overwrite settlement currency with display currency."),
		"referenceFixture", jsjson.MakeString("parallel-settlement-regression (frozen JavaScript demo)"),
		"model", jsjson.MakeString("Reviewed Go refund model under native JSON configuration"),
		"scope", jsjson.MakeString("Built-in DEMO_SAMPLE only; not external repository execution."))
	template := jsjson.MustObject(
		"id", jsjson.MakeString("refund-settlement-currency-go-model"),
		"version", jsjson.MakeNumber(1),
		"behavior", jsjson.MakeString("Completed refunds preserve the order settlement currency."),
		"command", jsjson.MakeString("fl "+nativefixture.ChildArg),
		"overlay", strArray(nativefixture.ConfigFile),
		"policy", jsjson.MustObject(
			"network", jsjson.MakeString("disabled"),
			"credentials", jsjson.MakeString("redacted"),
			"runner", jsjson.MakeString("built-in-sample"),
			"timeoutSeconds", jsjson.MakeNumber(2),
			"note", jsjson.MakeString("Only the reviewed Go model is executed. Program descriptor "+programDigest+". No network operations or inherited credentials; not an OS sandbox.")),
		"proposedBeforeLocalization", jsjson.MakeBool(true),
		"incidentPacketDigest", jsjson.MakeString(digest(incident)))
	proposal := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.native-witness-proposal.v1"),
		"program", program,
		"programDigest", jsjson.MakeString(programDigest),
		"incidentPacket", incident,
		"witnessTemplate", template)
	proposal.Obj().Set("digest", jsjson.MakeString(digest(proposal)))
	return proposal
}

func cloneNativeValue(v jsjson.Value) jsjson.Value {
	return jsexc.Must(jsjson.Parse(jsjson.Stringify(v)))
}

func newNativeSession(approval NativeApproval, deps nativeDeps, startedAt time.Time) (*nativeSession, error) {
	proposal := NativeWitnessProposal()
	approvedAt, err := time.Parse(time.RFC3339Nano, approval.ApprovedAt)
	if approval.ProposalDigest != proposal.Get("digest").Str() || err != nil || clock.ISO(approvedAt) != approval.ApprovedAt || approvedAt.After(startedAt) {
		return nil, &NativeAnalysisError{Kind: "APPROVAL", Err: errors.New("approval must match the proposal and record a canonical UTC millisecond timestamp no later than execution entry")}
	}
	executor, err := deps.newExecutor()
	if err != nil {
		return nil, &NativeAnalysisError{Kind: "ENVIRONMENT", Err: err}
	}
	environment := cloneNativeValue(executor.Environment())
	witness := jsjson.NewObj()
	template := proposal.Get("witnessTemplate")
	for _, key := range template.Obj().Keys() {
		if key == "incidentPacketDigest" {
			witness.Set("approvedAt", jsjson.MakeString(approval.ApprovedAt))
		}
		witness.Set(key, cloneNativeValue(template.Get(key)))
	}
	witness.Set("digest", jsjson.MakeString(digest(jsjson.MakeObject(witness))))
	return &nativeSession{
		proposal: proposal,
		approval: jsjson.MustObject("proposalDigest", jsjson.MakeString(approval.ProposalDigest), "approvedAt", jsjson.MakeString(approval.ApprovedAt)),
		witness:  jsjson.MakeObject(witness), program: cloneNativeValue(proposal.Get("program")), programDigest: proposal.Get("programDigest").Str(),
		environmentPreimage: environment, environmentDigest: digest(environment),
		nonce: deps.nonce(), executor: executor, seenIDs: make(map[string]struct{}),
	}, nil
}
