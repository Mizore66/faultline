package witnessresult

import "testing"

func TestResolve(t *testing.T) {
	line := Format("PREDICATE_FAIL", "", false)
	if line != `FAULTLINE_WITNESS_RESULT={"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_FAIL"}` {
		t.Fatalf("Format = %s", line)
	}
	p := Resolve("state=bad\r\n" + line + "\n")
	if !p.Ok || p.Result == nil || p.Result.Outcome != "PREDICATE_FAIL" || !ConsistentWithExitCode(*p.Result, 1) || ConsistentWithExitCode(*p.Result, 0) {
		t.Fatalf("%+v", p)
	}
}

func TestResolveConflictAndAbsent(t *testing.T) {
	if p := Resolve("nothing"); !p.Ok || p.Result != nil {
		t.Fatalf("%+v", p)
	}
	conflict := Format("PREDICATE_PASS", "", false) + "\n" + Format("PREDICATE_FAIL", "", false)
	if p := Resolve(conflict); p.Ok || p.Reason != "CONFLICTING_WITNESS_RESULTS" {
		t.Fatalf("%+v", p)
	}
	dup := Format("HARNESS_ERROR", "x", true) + "\n" + `{"protocol":"faultline.witness-result.v1","outcome":"HARNESS_ERROR","detail":"x"}`
	if p := Resolve(dup); !p.Ok || p.Result == nil || p.Result.Detail != "x" || !p.Result.HasDetail {
		t.Fatalf("%+v", p)
	}
	if p := Resolve(`{"protocol":"faultline.witness-result.v1","outcome":"HARNESS_ERROR","extra":1}`); !p.Ok || p.Result != nil {
		t.Fatalf("strict object must reject extra keys: %+v", p)
	}
}

func TestClassify(t *testing.T) {
	if c := Classify("INFRASTRUCTURE_ERROR"); c.Verdict != "ERROR" || c.Reason != "SANDBOX_UNAVAILABLE" {
		t.Fatalf("%+v", c)
	}
}
