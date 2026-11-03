package demofixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

// Test-only locator; production proposals and sessions never read a corpus.
var nativeAnalysisContractPath = flag.String("native-contract", "../../difftest/testdata/native-demo/native-contract.jsonl", "independent native analysis contract")

func nativeContractRows(t *testing.T) []jsjson.Value {
	t.Helper()
	data, err := os.ReadFile(*nativeAnalysisContractPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "c98e88c7098eae93e6dd9c7f6e8c8d12fd458186422566a935ba5725c5f538ef" {
		t.Fatalf("independent contract binding changed: %s", got)
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) != 4 || len(lines[3]) != 0 {
		t.Fatal("expected three complete contract rows and final LF")
	}
	rows := make([]jsjson.Value, 3)
	for i, line := range lines[:3] {
		rows[i], err = jsjson.Parse(string(line))
		if err != nil {
			t.Fatal(err)
		}
	}
	return rows
}
func nativeContractExpected(t *testing.T, row jsjson.Value) jsjson.Value {
	t.Helper()
	v, err := jsjson.Parse(row.Get("expected").Str())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func nativeContractTime(t *testing.T, text string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func nativeApprovalFromInput(input jsjson.Value) NativeApproval {
	return NativeApproval{ProposalDigest: input.Get("approval", "proposalDigest").Str(), ApprovedAt: input.Get("approval", "approvedAt").Str()}
}

// Wrong program/policy/packet bytes, a digest covering itself, or shared nested
// storage must not silently preserve the independently approved proposal.
func TestNativeProposalReferenceAndFreshness(t *testing.T) {
	for _, row := range nativeContractRows(t) {
		t.Run(row.Get("id").Str(), func(t *testing.T) {
			want := jsjson.Stringify(row.Get("input", "proposal"))
			got := NativeWitnessProposal()
			if text := jsjson.Stringify(got); text != want {
				t.Fatalf("complete proposal differs\nwant=%s\ngot=%s", want, text)
			}
			body := jsjson.NewObj()
			for _, key := range got.Obj().Keys() {
				if key != "digest" {
					body.Set(key, got.Get(key))
				}
			}
			d, err := canonical.DigestJSON(jsjson.MakeObject(body))
			if err != nil || d != "sha256:c41f0d62c2665c89f2e19b8ce15d24356818995a86655efb135beb99c186252f" || got.Get("digest").Str() != d {
				t.Fatalf("proposal identity=%q error=%v", d, err)
			}
			got.Obj().Set("digest", jsjson.MakeString("mutated"))
			got.Get("program", "fields", "displayDefault").Items()[0] = jsjson.MakeString("mutated")
			got.Get("witnessTemplate", "policy").Obj().Set("note", jsjson.MakeString("mutated"))
			got.Get("incidentPacket").Obj().Set("model", jsjson.MakeString("mutated"))
			if next := jsjson.Stringify(NativeWitnessProposal()); next != want {
				t.Fatal("proposal shares nested mutable storage across calls")
			}
		})
	}
}

// Accepting any noncanonical/future approval, or resolving an executable or
// acquiring entropy before rejection, violates the authorization boundary.
func TestNativeApprovalRejectsBeforeDependencies(t *testing.T) {
	input := nativeContractRows(t)[0].Get("input")
	valid := nativeApprovalFromInput(input)
	entry := nativeContractTime(t, input.Get("startedAt").Str())
	for _, tc := range []struct {
		name     string
		approval NativeApproval
	}{
		{"absent", NativeApproval{}},
		{"missing-digest", NativeApproval{ApprovedAt: valid.ApprovedAt}},
		{"wrong-digest", NativeApproval{ProposalDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", ApprovedAt: valid.ApprovedAt}},
		{"missing-time", NativeApproval{ProposalDigest: valid.ProposalDigest}},
		{"malformed", NativeApproval{valid.ProposalDigest, "not a timestamp"}},
		{"non-utc", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:00.000+00:00"}},
		{"missing-milliseconds", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:00Z"}},
		{"short-milliseconds", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:00.00Z"}},
		{"extra-milliseconds", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:00.0000Z"}},
		{"future", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:01.001Z"}},
		{"whitespace", NativeApproval{valid.ProposalDigest, "2026-11-03T00:00:00.000Z\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factories, nonces, reads := 0, 0, 0
			deps := nativeDeps{
				newExecutor: func() (nativeExecutor, error) { factories++; return nil, errors.New("must not resolve executable") },
				nonce:       func() string { nonces++; return "must not acquire nonce" },
				now:         func() time.Time { reads++; return entry },
			}
			s, err := newNativeSession(tc.approval, deps, entry)
			var typed *NativeAnalysisError
			if s != nil || !errors.As(err, &typed) || typed.Kind != "APPROVAL" || typed.Ordinal != 0 || typed.StateID != "" {
				t.Fatalf("invalid approval created session=%v error=%v", s != nil, err)
			}
			if factories != 0 || nonces != 0 || reads != 0 {
				t.Fatalf("rejected approval invoked dependencies: executor=%d nonce=%d time=%d", factories, nonces, reads)
			}
		})
	}
}

type nativeWitnessTestExecutor struct {
	environment jsjson.Value
	runs        int
}

func (e *nativeWitnessTestExecutor) Environment() jsjson.Value { return e.environment }
func (e *nativeWitnessTestExecutor) Run(context.Context, nativefixture.Materialized) (nativefixture.Capture, error) {
	e.runs++
	return nativefixture.Capture{}, errors.New("witness construction must not execute")
}

// Independent final witness bytes catch historical Node identity reuse,
// misplaced approval, per-config digests, and incomplete identity freezing.
func TestNativeWitnessReferenceAndEnvironmentFreeze(t *testing.T) {
	for _, row := range nativeContractRows(t) {
		t.Run(row.Get("id").Str(), func(t *testing.T) {
			input := row.Get("input")
			expected := nativeContractExpected(t, row)
			environment := input.Get("environmentPreimage")
			environmentBytes := jsjson.Stringify(environment)
			executor := &nativeWitnessTestExecutor{environment: environment}
			factories, nonces := 0, 0
			deps := nativeDeps{
				newExecutor: func() (nativeExecutor, error) { factories++; return executor, nil },
				nonce:       func() string { nonces++; return input.Get("nonce").Str() },
				now: func() time.Time {
					t.Fatal("constructor must use the single supplied entry timestamp")
					return time.Time{}
				},
			}
			s, err := newNativeSession(nativeApprovalFromInput(input), deps, nativeContractTime(t, input.Get("startedAt").Str()))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := jsjson.Stringify(s.witness), jsjson.Stringify(expected.Get("analysis", "witness")); got != want {
				t.Fatalf("complete final witness differs\nwant=%s\ngot=%s", want, got)
			}
			if jsjson.Stringify(s.proposal) != jsjson.Stringify(input.Get("proposal")) || jsjson.Stringify(s.approval) != jsjson.Stringify(input.Get("approval")) || jsjson.Stringify(s.program) != jsjson.Stringify(input.Get("proposal", "program")) {
				t.Fatal("session proposal/approval/program bytes changed")
			}
			if s.programDigest != "sha256:5e185b391f08919f50268647cbb60fe6fd5db14aff30f711d3211b22dd3b23e0" || s.environmentDigest != expected.Get("provenance", "environmentDigest").Str() || s.nonce != input.Get("nonce").Str() {
				t.Fatal("session identity differs from independent contract")
			}
			if factories != 1 || nonces != 1 || executor.runs != 0 || s.ordinal != 0 || len(s.receipts) != 0 || s.seenIDs == nil {
				t.Fatal("session performed execution or did not initialize a single frozen identity")
			}
			executor.environment.Obj().Set("runtime", jsjson.MakeString("mutated"))
			executor.environment.Get("environment").Items()[0] = jsjson.MakeString("PATH=mutated")
			if jsjson.Stringify(s.environmentPreimage) != environmentBytes {
				t.Fatal("executor mutation changed frozen environment storage")
			}
		})
	}
}

// Mutating one frozen session object must not silently rewrite another
// authorization artifact through shared nested policy, overlay, or program data.
func TestNativeWitnessIndependentSessionStorage(t *testing.T) {
	row := nativeContractRows(t)[0]
	for _, field := range []string{"policy", "overlay", "program"} {
		t.Run(field, func(t *testing.T) {
			s, _ := nativeScriptSession(t, row)
			before := jsjson.Stringify(s.proposal)
			switch field {
			case "policy":
				s.witness.Get("policy").Obj().Set("note", jsjson.MakeString("changed"))
			case "overlay":
				s.witness.Get("overlay").Items()[0] = jsjson.MakeString("changed.json")
			case "program":
				s.program.Get("fields", "displayDefault").Items()[0] = jsjson.MakeString("changed")
			}
			if jsjson.Stringify(s.proposal) != before {
				t.Fatal("frozen proposal shares mutable session storage")
			}
		})
	}
}

func TestNativeApprovalAtEntryIsPreserved(t *testing.T) {
	input := nativeContractRows(t)[0].Get("input")
	approval := nativeApprovalFromInput(input)
	approval.ApprovedAt = input.Get("startedAt").Str()
	executor := &nativeWitnessTestExecutor{environment: input.Get("environmentPreimage")}
	s, err := newNativeSession(approval, nativeDeps{newExecutor: func() (nativeExecutor, error) { return executor, nil }, nonce: func() string { return input.Get("nonce").Str() }}, nativeContractTime(t, approval.ApprovedAt))
	if err != nil || s == nil || s.witness.Get("approvedAt").Str() != approval.ApprovedAt {
		t.Fatalf("approval exactly at entry not preserved: %v", err)
	}
}
func TestNativeApprovalExecutorErrorRetainsCause(t *testing.T) {
	input := nativeContractRows(t)[0].Get("input")
	cause := errors.New("executable observation unavailable")
	factories, nonces := 0, 0
	s, err := newNativeSession(nativeApprovalFromInput(input), nativeDeps{
		newExecutor: func() (nativeExecutor, error) { factories++; return nil, cause },
		nonce:       func() string { nonces++; return "not available" },
	}, nativeContractTime(t, input.Get("startedAt").Str()))
	var typed *NativeAnalysisError
	if s != nil || !errors.As(err, &typed) || typed.Kind != "ENVIRONMENT" || !errors.Is(err, cause) || factories != 1 || nonces != 0 {
		t.Fatalf("lost environment failure or fabricated session: %v", err)
	}
}
