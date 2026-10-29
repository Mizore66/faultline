package codexloop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func incident(t *testing.T) jsjson.Value {
	v, err := jsjson.Parse(`{"symptom":"s","ciLog":"l","repositoryLanguage":"Go","repositorySummary":"r"}`)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestImplementTemplateOnlyAndThrowingRunner(t *testing.T) {
	out := filepath.Join(t.TempDir(), "overlay")
	r, err := Implement(ImplementOptions{ProposalID: "demo-1", Incident: incident(t), Behavior: "a\nb", OverlayOut: out})
	if err != nil || r.Get("status").Str() != "TEMPLATE_ONLY" || !strings.HasPrefix(r.Get("note").Str(), "Template written for blinded symptom digest sha256:") {
		t.Fatalf("%v %s", err, jsjson.Stringify(r))
	}
	b, _ := os.ReadFile(filepath.Join(out, "witness.mjs"))
	if !strings.Contains(string(b), "// FaultLine witness overlay — behavior: a b\n") {
		t.Fatalf("%q", b)
	}
	throwing := func([]string, string, string) (RunResult, error) {
		return RunResult{}, errors.New("Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex.")
	}
	r, _ = Implement(ImplementOptions{ProposalID: "demo-1", Incident: incident(t), Behavior: "b", OverlayOut: out, Runner: throwing})
	if r.Get("status").Str() != "CODEX_UNAVAILABLE" || r.Get("transport").Str() != "NONE" ||
		r.Get("note").Str() != "Codex CLI could not be started: Codex drafting requires explicit --allow-codex-full-auto in addition to --with-codex." {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}

func TestImplementNullExit(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o")
	run := func([]string, string, string) (RunResult, error) { return RunResult{}, nil }
	r, _ := Implement(ImplementOptions{ProposalID: "p", Incident: incident(t), Behavior: "b", OverlayOut: out, Runner: run})
	if r.Get("note").Str() != "Codex CLI exited null. Template retained for human edit." || r.Get("transport").Str() != "CODEX_CLI" {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}

func TestImplementRunnerCallDraftedAndExitCode(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o")
	var gotArgs []string
	var gotCwd, gotInput string
	zero, two := 0, 2
	code := &zero
	run := func(args []string, cwd, input string) (RunResult, error) {
		gotArgs, gotCwd, gotInput = args, cwd, input
		return RunResult{ExitCode: code}, nil
	}
	r, err := Implement(ImplementOptions{ProposalID: "p", Incident: incident(t), Behavior: "beh", OverlayOut: out, Runner: run})
	want := "Create or overwrite witness.mjs as an executable Node witness.\n" +
		`It must print one JSON line: {"protocol":"faultline.witness-result.v1","outcome":"PREDICATE_PASS"|"PREDICATE_FAIL"|...}.` + "\n" +
		"Behavior to test: beh\nSymptom: s\nDo not claim root cause. Do not modify unrelated files."
	if err != nil || len(gotArgs) != 3 || gotArgs[0] != "exec" || gotArgs[1] != "--full-auto" || gotArgs[2] != want || gotInput != want || gotCwd != out {
		t.Fatalf("%v %q %q %q", err, gotArgs, gotCwd, gotInput)
	}
	wantJSON := `{"schemaVersion":"faultline.codex-loop.v1","status":"OVERLAY_DRAFTED","transport":"CODEX_CLI","proposalId":"p","overlayPath":` +
		jsjson.Quote(filepath.Join(out, "witness.mjs")) + `,"commandSuggestion":"node witness.mjs","note":"Codex CLI completed a draft overlay. Human review and freeze are still required.",` +
		`"limitation":"Codex may draft overlay bytes in an isolated review directory only. A human must approve and freeze before localization."}`
	if jsjson.Stringify(r) != wantJSON {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
	code = &two
	r, _ = Implement(ImplementOptions{ProposalID: "p", Incident: incident(t), Behavior: "beh", OverlayOut: out, Runner: run})
	if r.Get("note").Str() != "Codex CLI exited 2. Template retained for human edit." || r.Get("status").Str() != "CODEX_UNAVAILABLE" {
		t.Fatalf("%s", jsjson.Stringify(r))
	}
}

func TestImplementInvalidIncidentThrowsBeforeWrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o")
	bad, _ := jsjson.Parse(`{"symptom":"","ciLog":"l","repositoryLanguage":"Go","repositorySummary":"r","x":1}`)
	if _, err := Implement(ImplementOptions{ProposalID: "p", Incident: bad, Behavior: "b", OverlayOut: out}); err == nil {
		t.Fatal("want ZodError")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("overlay dir written: %v", err)
	}
}

func TestImplementBehaviorSliceSplitsSurrogate(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o")
	// 199 ASCII units then U+1F600 (two units): slice(0, 200) keeps the
	// high surrogate alone, which Node writes as U+FFFD.
	behavior := strings.Repeat("x", 199) + "\U0001F600tail"
	if _, err := Implement(ImplementOptions{ProposalID: "p", Incident: incident(t), Behavior: behavior, OverlayOut: out}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(out, "witness.mjs"))
	if !strings.Contains(string(b), "behavior: "+strings.Repeat("x", 199)+"�\n") {
		t.Fatalf("%q", b)
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.faultline.txt"))
	if !strings.HasPrefix(string(readme), "Proposal id: p\n1. Review and edit witness.mjs\n") {
		t.Fatalf("%q", readme)
	}
}
