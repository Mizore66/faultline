//go:build faultline_test

package difftest

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Missing tests, fake diagnostics, incomplete execution and skipped cases must
// not turn static corpus text into native execution evidence.
func TestNativeCoverageEvents(t *testing.T) {
	valid := nativeCoverageEvents()
	for _, tc := range []struct {
		name   string
		mutate func([]map[string]any) []map[string]any
	}{
		{"missing-parent", func(e []map[string]any) []map[string]any { return nativeWithoutTest(e, "TestNativeDiagnostics") }},
		{"renamed-parent", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Test"] == "TestNativeDiagnostics" {
					x["Test"] = "TestRenamed"
				}
			}
			return e
		}},
		{"missing-contract-row", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestNativeAnalysisFullContract/ordinary")
		}},
		{"missing-invariant", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestNativeDiagnosticInvariants/environment")
		}},
		{"missing-diagnostic", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Output"] != nil {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"skip", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Action"] == "pass" && x["Test"] == "TestNativeDiagnostics/rejected" {
					x["Action"] = "skip"
				}
			}
			return e
		}},
		{"fail", func(e []map[string]any) []map[string]any { e[len(e)-1]["Action"] = "fail"; return e }},
		{"missing-package-end", func(e []map[string]any) []map[string]any { return e[:len(e)-1] }},
		{"duplicate-diagnostic", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Output"] != nil {
					return append(e[:i], append([]map[string]any{x}, e[i:]...)...)
				}
			}
			return e
		}},
		{"malformed-diagnostic", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Output"] != nil {
					x["Output"] = "NATIVE-DIAGNOSTIC not-json\n"
				}
			}
			return e
		}},
		{"wrong-emitter", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Output"] != nil {
					delete(x, "Test")
				}
			}
			return e
		}},
		{"missing-cli-parent", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestJudgeDemoNativeDiagnostics")
		}},
		{"wrong-cli-emitter-package", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeCLIPkg && x["Output"] != nil {
					x["Package"] = nativeDemoPkg
				}
			}
			return e
		}},
		{"cli-parent-in-demo-package", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeCLIPkg && x["Test"] != nil {
					x["Package"] = nativeDemoPkg
				}
			}
			return e
		}},
		{"cli-demofixture-flag-attempt", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeCLIPkg && x["Action"] == "start" {
					flag := map[string]any{"Action": "output", "Package": nativeCLIPkg, "Output": "flag provided but not defined: -native-contract\n"}
					fail := map[string]any{"Action": "fail", "Package": nativeCLIPkg}
					return append(e[:i+1], append([]map[string]any{flag, fail}, e[i+1:]...)...)
				}
			}
			return e
		}},
		{"cli-parent-level-emitter", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeCLIPkg && x["Output"] != nil {
					x["Test"] = "TestJudgeDemoNativeDiagnostics"
					break
				}
			}
			return e
		}},
		{"proofview-parent-missing", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestIncidentServerClose")
		}},
		{"plain-demo-parent-missing", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestPlainDemoServerClose")
		}},
		{"proofview-drift-without-children", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(nativeWithoutTest(e, "TestInteractiveIncidentListenerDrift/listener-start-drift"), "TestInteractiveIncidentListenerDrift/listener-end-drift")
		}},
		{"proofview-non-diagnostic-emitter", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "output", "Package": nativeProofviewPkg, "Test": "TestIncidentServerClose", "Output": "NATIVE-DIAGNOSTIC \"x\"\n"})
		}},
		{"socket-parent-missing", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, nativeSocketOverlappedParent)
		}},
		{"socket-parent-without-children", func(e []map[string]any) []map[string]any {
			for _, child := range nativeSocketChildren {
				e = nativeWithoutTest(e, nativeSocketOverlappedParent+"/"+child)
			}
			return e
		}},
		{"socket-child-run-event-missing", func(e []map[string]any) []map[string]any {
			// Only run is removed; output/pass remain and must be rejected.
			for i, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && x["Action"] == "run" {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"socket-child-without-record", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[1] && x["Output"] != nil {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"socket-duplicate-record", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && x["Output"] != nil {
					return append(e[:i], append([]map[string]any{x}, e[i:]...)...)
				}
			}
			return e
		}},
		{"socket-parent-level-emitter", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && x["Output"] != nil {
					x["Test"] = nativeSocketOverlappedParent
					break
				}
			}
			return e
		}},
		{"socket-record-from-non-diagnostic-parent", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && x["Output"] != nil {
					x["Test"] = "TestIncidentServerClose/" + nativeSocketChildren[0]
					run := map[string]any{"Action": "run", "Package": nativeProofviewPkg, "Test": "TestIncidentServerClose/" + nativeSocketChildren[0]}
					return append(e[:i], append([]map[string]any{run}, e[i:]...)...)
				}
			}
			return e
		}},
		{"socket-wrong-package-owner", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && x["Output"] != nil {
					x["Package"] = nativeGuidedPkg
				}
			}
			return e
		}},
		{"socket-child-skip", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Action"] == "pass" && x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[2] {
					x["Action"] = "skip"
				}
			}
			return e
		}},
		{"socket-child-fail", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Action"] == "pass" && x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[2] {
					x["Action"] = "fail"
				}
			}
			return e
		}},
		{"socket-parent-skip", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Action"] == "pass" && x["Test"] == nativeSocketOverlappedParent {
					x["Action"] = "skip"
				}
			}
			return e
		}},
		{"socket-truncated-record", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[1] && x["Output"] != nil {
					x["Output"] = strings.TrimSuffix(x["Output"].(string), "\n")
					x["Output"] = strings.TrimSuffix(x["Output"].(string), "\"")
				}
			}
			return e
		}},
		{"socket-truncated-marker", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[1] && x["Output"] != nil {
					x["Output"] = "    test.go:1: NATIVE-DIAGN"
				}
			}
			return e
		}},
		{"cli-parent-without-children", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(nativeWithoutTest(e, "TestJudgeDemoNativeDiagnostics/server"), "TestJudgeDemoNativeDiagnostics/generated-bundle-invalid")
		}},
		{"missing-demo-parent", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestNativeEvidenceDiagnostics")
		}},
		{"demo-parent-without-children", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(nativeWithoutTest(e, "TestNativeEvidenceDiagnostics/nil-root"), "TestNativeEvidenceDiagnostics/root-mismatch")
		}},
		{"missing-demo-diagnostic", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeDemoPkg && x["Output"] != nil {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"missing-cli-diagnostic", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeCLIPkg && x["Output"] != nil {
					e = append(e[:i], e[i+1:]...)
					break
				}
			}
			for i, x := range e {
				if x["Package"] == nativeCLIPkg && x["Output"] != nil {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"duplicate-demo-completion", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "pass", "Package": nativeDemoPkg})
		}},
		{"duplicate-cli-completion", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "pass", "Package": nativeCLIPkg})
		}},
		{"duplicate-cli-start", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "start", "Package": nativeCLIPkg})
		}},
		{"event-after-cli-completion", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "run", "Package": nativeCLIPkg, "Test": "TestJudgeDemoNativeDiagnostics"})
		}},
		{"foreign-package", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "start", "Package": "github.com/Mizore66/faultline/internal/repair"})
		}},
		{"guided-parent-missing", func(e []map[string]any) []map[string]any {
			return nativeWithoutTest(e, "TestGuidedNativeDiagnostics")
		}},
		{"guided-child-without-diagnostic", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeGuidedPkg && x["Output"] != nil {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"guided-parent-level-emitter", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeGuidedPkg && x["Output"] != nil {
					x["Test"] = "TestGuidedNativeDiagnostics"
					break
				}
			}
			return e
		}},
		{"guided-unrelated-test", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "run", "Package": nativeGuidedPkg, "Test": "TestGuidedRunResume"})
		}},
		{"guided-skip", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeGuidedPkg && x["Action"] == "pass" && x["Test"] == "TestGuidedNativeDiagnostics" {
					x["Action"] = "skip"
				}
			}
			return e
		}},
		{"guided-duplicate-start", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"Action": "start", "Package": nativeGuidedPkg})
		}},
		{"cli-skip", func(e []map[string]any) []map[string]any {
			for _, x := range e {
				if x["Package"] == nativeCLIPkg && x["Action"] == "pass" && x["Test"] == "TestJudgeDemoNativeDiagnostics" {
					x["Action"] = "skip"
				}
			}
			return e
		}},
		{"cli-pass-without-run", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeCLIPkg && x["Action"] == "run" {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
		{"cli-unrelated-test", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Package"] == nativeCLIPkg && x["Action"] == "start" {
					return append(e[:i+1], append([]map[string]any{{"Action": "run", "Package": nativeCLIPkg, "Test": "TestJudgeDemoExport"}}, e[i+1:]...)...)
				}
			}
			return e
		}},
		{"pass-without-run", func(e []map[string]any) []map[string]any {
			for i, x := range e {
				if x["Action"] == "run" {
					return append(e[:i], e[i+1:]...)
				}
			}
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := nativeOutputs(nativeEventJSON(t, tc.mutate(nativeCoverageEvents())), []string{"ordinary"}); err == nil {
				t.Fatal("invalid native evidence accepted")
			}
		})
	}
	for _, data := range [][]byte{[]byte("{broken"), append(nativeEventJSON(t, valid), []byte("{truncated")...)} {
		if _, err := nativeOutputs(data, []string{"ordinary"}); err == nil {
			t.Fatal("malformed event stream accepted")
		}
	}
	out, err := nativeOutputs(nativeEventJSON(t, valid), []string{"ordinary"})
	for pkg, want := range map[string]string{
		nativeDemofixturePkg: `"native analysis rejected the invocation"`,
		nativeCLIPkg:         `"FaultLine error: cli server refused"`,
		nativeDemoPkg:        `"Native evidence nil-root refused"`,
		nativeProofviewPkg:   `"proofview: interactive incident listener-start-drift"`,
		nativeGuidedPkg:      `"guided: missing_inputs"`,
	} {
		if err != nil || !strings.Contains(out[pkg], want+"\n") {
			t.Fatalf("package %s lacks %s: %q %v", pkg, want, out, err)
		}
		for other, text := range out {
			if other != pkg && strings.Contains(text, want) {
				t.Fatalf("%s text leaked into %s", pkg, other)
			}
		}
	}
}

// nativeSocketChildren are synthetic stand-ins for the producer's children; the
// real parent has 41 dynamic children and none is pinned by name here.
var nativeSocketChildren = []string{"domain-no-parts", "submit-nil-rawconn", "rawconn-callback-twice"}

const (
	nativeDemofixturePkg = "github.com/Mizore66/faultline/internal/demofixture"
	nativeCLIPkg         = "github.com/Mizore66/faultline/internal/cli"
	nativeDemoPkg        = "github.com/Mizore66/faultline/internal/bundle/demo"
	nativeProofviewPkg   = "github.com/Mizore66/faultline/internal/proofview"
	nativeGuidedPkg      = "github.com/Mizore66/faultline/internal/guided"
)

// nativeCoverageEvents is one complete valid stream per owned package, in the
// order the two sequential commands produce them.
func nativeCoverageEvents() []map[string]any {
	var events []map[string]any
	add := func(pkg, action, test string) {
		event := map[string]any{"Action": action, "Package": pkg}
		if test != "" {
			event["Test"] = test
		}
		events = append(events, event)
	}
	diagnostic := func(pkg, test, text string) {
		events = append(events, map[string]any{"Action": "output", "Package": pkg, "Test": test, "Output": "    test.go:1: NATIVE-DIAGNOSTIC " + text + "\n"})
	}
	add(nativeDemofixturePkg, "start", "")
	for _, parent := range []string{"TestNativeDiagnostics", "TestNativeDiagnosticInvariants", "TestNativeAnalysisFullContract"} {
		add(nativeDemofixturePkg, "run", parent)
		children := []string{"rejected"}
		if parent == "TestNativeDiagnosticInvariants" {
			children = []string{"timeline", "partial", "subsets", "environment"}
		}
		if parent == "TestNativeAnalysisFullContract" {
			children = []string{"ordinary"}
		}
		for _, child := range children {
			name := parent + "/" + child
			add(nativeDemofixturePkg, "run", name)
			if parent == "TestNativeDiagnostics" {
				diagnostic(nativeDemofixturePkg, name, `"native analysis rejected the invocation"`)
			}
			add(nativeDemofixturePkg, "pass", name)
		}
		add(nativeDemofixturePkg, "pass", parent)
	}
	add(nativeDemofixturePkg, "pass", "")
	add(nativeCLIPkg, "start", "")
	add(nativeCLIPkg, "run", "TestJudgeDemoNativeDiagnostics")
	for _, child := range []string{"server", "generated-bundle-invalid"} {
		name := "TestJudgeDemoNativeDiagnostics/" + child
		add(nativeCLIPkg, "run", name)
		diagnostic(nativeCLIPkg, name, `"FaultLine error: cli `+child+` refused"`)
		add(nativeCLIPkg, "pass", name)
	}
	add(nativeCLIPkg, "pass", "TestJudgeDemoNativeDiagnostics")
	add(nativeCLIPkg, "pass", "")
	add(nativeDemoPkg, "start", "")
	add(nativeDemoPkg, "run", "TestNativeEvidenceDiagnostics")
	for _, child := range []string{"nil-root", "root-mismatch"} {
		name := "TestNativeEvidenceDiagnostics/" + child
		add(nativeDemoPkg, "run", name)
		diagnostic(nativeDemoPkg, name, `"Native evidence `+child+` refused"`)
		add(nativeDemoPkg, "pass", name)
	}
	add(nativeDemoPkg, "pass", "TestNativeEvidenceDiagnostics")
	add(nativeDemoPkg, "pass", "")
	add(nativeProofviewPkg, "start", "")
	for _, parent := range nativeProofviewPackage.parents {
		add(nativeProofviewPkg, "run", parent)
		if parent == "TestInteractiveIncidentListenerDrift" {
			for _, child := range []string{"listener-start-drift", "listener-end-drift"} {
				name := parent + "/" + child
				add(nativeProofviewPkg, "run", name)
				diagnostic(nativeProofviewPkg, name, `"proofview: interactive incident `+child+`"`)
				add(nativeProofviewPkg, "pass", name)
			}
		}
		if parent == nativeSocketOverlappedParent {
			for _, child := range nativeSocketChildren {
				name := parent + "/" + child
				add(nativeProofviewPkg, "run", name)
				diagnostic(nativeProofviewPkg, name, `"windows socket write: `+child+`"`)
				add(nativeProofviewPkg, "pass", name)
			}
		}
		add(nativeProofviewPkg, "pass", parent)
	}
	add(nativeProofviewPkg, "pass", "")
	add(nativeGuidedPkg, "start", "")
	add(nativeGuidedPkg, "run", "TestGuidedNativeDiagnostics")
	for _, child := range []string{"intake_paired_range", "missing_inputs"} {
		name := "TestGuidedNativeDiagnostics/" + child
		add(nativeGuidedPkg, "run", name)
		diagnostic(nativeGuidedPkg, name, `"guided: `+child+`"`)
		add(nativeGuidedPkg, "pass", name)
	}
	add(nativeGuidedPkg, "pass", "TestGuidedNativeDiagnostics")
	add(nativeGuidedPkg, "pass", "")
	return events
}

func nativeWithoutTest(events []map[string]any, test string) []map[string]any {
	out := events[:0]
	for _, e := range events {
		if e["Test"] != test {
			out = append(out, e)
		}
	}
	return out
}

func nativeEventJSON(t *testing.T, events []map[string]any) []byte {
	t.Helper()
	var out []byte
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, append(b, '\n')...)
	}
	return out
}

// Text at a different source line must not satisfy an unexecuted error site;
// missing profiles, newly uncovered literals and stale exceptions also fail.
func TestNativeCoveragePositions(t *testing.T) {
	repo := t.TempDir()
	file := "internal/demofixture/native_run.go"
	text := "native invocation must actually be executed"
	sites := []errorSite{{text: text, file: filepath.Join(repo, filepath.FromSlash(file)), line: 10}}
	blocks := func() map[string][]coverBlock {
		out := map[string][]coverBlock{}
		for _, name := range nativeCoverageFiles {
			out[name] = []coverBlock{{1, 300, 1}}
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		change func(map[string][]coverBlock)
		output string
		exempt map[string]string
	}{
		{"missing-file", func(b map[string][]coverBlock) { delete(b, file) }, text, nil},
		{"zero-hit", func(b map[string][]coverBlock) { b[file] = []coverBlock{{1, 300, 0}} }, text, nil},
		{"same-text-other-position", func(b map[string][]coverBlock) { b[file] = []coverBlock{{10, 10, 0}, {20, 20, 1}} }, text, nil},
		{"no-block-at-site", func(b map[string][]coverBlock) { b[file] = []coverBlock{{20, 20, 1}} }, text, nil},
		{"absent-output", func(b map[string][]coverBlock) {}, "unrelated cached golden", nil},
		{"changed-message", func(b map[string][]coverBlock) {}, "native invocation used to be executed", nil},
		{"stale-exemption", func(b map[string][]coverBlock) {}, text, map[string]string{text: "old invariant proof"}},
		{"foreign-file", func(b map[string][]coverBlock) { b["internal/demofixture/replay.go"] = []coverBlock{{1, 300, 1}} }, text, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := blocks()
			tc.change(b)
			if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: nativeOutputsFrom(file, tc.output), blocks: b}, tc.exempt); err == nil {
				t.Fatal("invalid positional evidence accepted")
			}
		})
	}
	if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: nativeOutputsFrom(file, text), blocks: blocks()}, nil); err != nil {
		t.Fatal(err)
	}
	b := blocks()
	b[file] = []coverBlock{{1, 300, 0}}
	if err := validateNativeEvidence(repo, sites, nativeEvidence{blocks: b}, map[string]string{text: "individually proven invariant"}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCoverageProfile(t *testing.T) {
	profile := "mode: set\n"
	for _, file := range nativeCoverageFiles {
		profile += "github.com/Mizore66/faultline/" + file + ":1.1,300.2 1 1\n"
	}
	blocks, err := nativeProfile([]byte(profile))
	if err != nil || len(blocks) != 12 {
		t.Fatalf("valid native profile: %v", err)
	}
	for _, file := range []string{"internal/cli/judge_demo.go", "internal/bundle/demo/native_evidence.go"} {
		missing := strings.Replace(profile, "github.com/Mizore66/faultline/"+file+":1.1,300.2 1 1\n", "", 1)
		if missing == profile {
			t.Fatalf("%s is not a required native file", file)
		}
		if _, err := nativeProfile([]byte(missing)); err == nil {
			t.Fatalf("profile without %s accepted", file)
		}
		zero := strings.Replace(profile, "github.com/Mizore66/faultline/"+file+":1.1,300.2 1 1\n", "github.com/Mizore66/faultline/"+file+":1.1,300.2 1 0\n", 1)
		blocks, err := nativeProfile([]byte(zero))
		if err != nil || blocks[file][0].count != 0 {
			t.Fatalf("zero-hit %s must parse faithfully and be judged later: %v", file, err)
		}
	}
	for _, bad := range []string{"", "mode: set\n", "mode: set\nbroken\n", strings.Replace(profile, "300.2", "bad", 1), strings.Replace(profile, " 1 1", " 1 -1", 1)} {
		if _, err := nativeProfile([]byte(bad)); err == nil {
			t.Fatalf("invalid profile accepted: %q", bad)
		}
	}
	b, err := nativeProfile([]byte(profile + "github.com/Mizore66/faultline/internal/demofixture/replay.go:1.1,2.1 1 1\n"))
	if err != nil || b["internal/demofixture/replay.go"] != nil {
		t.Fatal("supplemental profile may not own cached replay counters")
	}
}

// Native text must never satisfy a cached CLI literal or a different package.
func TestNativeCoverageTextOwnership(t *testing.T) {
	repo := t.TempDir()
	native := nativeEvidence{outputs: nativeAllOutputs("native-only diagnostic")}
	for _, file := range []string{"internal/demofixture/replay.go", "internal/repair/live.go", "internal/demofixture/native_run.go.extra", "internal/demofixture/native_analysis_test.go"} {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); got != "CLI golden" {
			t.Fatalf("native output satisfied non-native file %s: %q", file, got)
		}
	}
	for _, file := range nativeCoverageFiles {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); !strings.HasPrefix(got, "native-only diagnostic") {
			t.Fatalf("native evidence missing for its exact file %s: %q", file, got)
		}
	}
}

func TestNativeCoverageMerge(t *testing.T) {
	cached := "internal/demofixture/replay.go"
	blocks := map[string][]coverBlock{cached: {{1, 2, 0}}}
	if err := mergeNativeCoverage(blocks, map[string][]coverBlock{cached: {{1, 2, 1}}}); err == nil {
		t.Fatal("native evidence replaced cached replay coverage")
	}
	if blocks[cached][0].count != 0 {
		t.Fatal("rejected merge mutated cached coverage")
	}
	file := nativeCoverageFiles[0]
	if err := mergeNativeCoverage(blocks, map[string][]coverBlock{file: {{1, 2, 1}}}); err != nil {
		t.Fatal(err)
	}
	if blocks[cached][0].count != 0 || blocks[file][0].count != 1 {
		t.Fatal("valid native merge changed replay evidence")
	}
}

// The two new owners are exact required paths, never prefixes or exemptions.
func TestNativeCoverageOwnedFiles(t *testing.T) {
	want := []string{
		"internal/demofixture/native_analysis.go",
		"internal/demofixture/native_run.go",
		"internal/demofixture/native_witness.go",
		"internal/cli/judge_demo.go",
		"internal/bundle/demo/native_evidence.go",
		"internal/proofview/incident.go",
		"internal/proofview/incident_server.go",
		"internal/proofview/demo_server.go",
		"internal/guided/intake.go",
		"internal/guided/guided.go",
		"internal/guided/freeze.go",
		"internal/proofview/socket_overlapped.go",
	}
	if strings.Join(nativeCoverageFiles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("native files: %v", nativeCoverageFiles)
	}
	repo := t.TempDir()
	native := nativeEvidence{outputs: nativeAllOutputs("native-only diagnostic")}
	for _, file := range want[3:] {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); !strings.HasPrefix(got, "native-only diagnostic") {
			t.Fatalf("%s not natively owned: %q", file, got)
		}
	}
	for _, file := range []string{"internal/guided/projection.go", "internal/guided/guided_test.go", "internal/guided/freeze.go.bak", "internal/cli/judge_preview.go", "internal/cli/judge_demo_test.go", "internal/bundle/demo/write.go", "internal/bundle/demo/native_evidence.go.bak",
		"internal/proofview/socket_overlapped_windows.go", "internal/proofview/socket_overlapped_unix.go", "internal/proofview/socket_writer.go", "internal/proofview/socket_overlapped.go.bak"} {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); got != "CLI golden" {
			t.Fatalf("native output satisfied neighbour %s", file)
		}
	}
}

// Every one of the five files needs its own positive counter and produced text.
func TestNativeCoverageExportSites(t *testing.T) {
	repo := t.TempDir()
	cli := "internal/cli/judge_demo.go"
	writer := "internal/bundle/demo/native_evidence.go"
	siteOf := func(file, text string) errorSite {
		return errorSite{text: text, file: filepath.Join(repo, filepath.FromSlash(file)), line: 10}
	}
	blocks := func() map[string][]coverBlock {
		out := map[string][]coverBlock{}
		for _, name := range nativeCoverageFiles {
			out[name] = []coverBlock{{1, 300, 1}}
		}
		return out
	}
	sites := []errorSite{siteOf(cli, "native CLI literal text"), siteOf(writer, "native writer literal text")}
	good := nativeEvidence{outputs: map[string]string{nativeCLIPkg: "native CLI literal text", nativeDemoPkg: "native writer literal text"}, blocks: blocks()}
	if err := validateNativeEvidence(repo, sites, good, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*nativeEvidence)
	}{
		{"zero-hit-judge-demo", func(e *nativeEvidence) { e.blocks[cli] = []coverBlock{{1, 300, 0}} }},
		{"zero-hit-native-evidence", func(e *nativeEvidence) { e.blocks[writer] = []coverBlock{{1, 300, 0}} }},
		{"missing-judge-demo-file", func(e *nativeEvidence) { delete(e.blocks, cli) }},
		{"missing-native-evidence-file", func(e *nativeEvidence) { delete(e.blocks, writer) }},
		{"missing-original-file", func(e *nativeEvidence) { delete(e.blocks, nativeCoverageFiles[0]) }},
		{"missing-cli-diagnostic", func(e *nativeEvidence) { e.outputs = map[string]string{nativeDemoPkg: "native writer literal text"} }},
		{"missing-writer-diagnostic", func(e *nativeEvidence) { e.outputs = map[string]string{nativeCLIPkg: "native CLI literal text"} }},
		{"foreign-cli-counter", func(e *nativeEvidence) { e.blocks["internal/cli/judge_preview.go"] = []coverBlock{{1, 300, 1}} }},
		{"foreign-writer-counter", func(e *nativeEvidence) { e.blocks["internal/bundle/demo/write.go"] = []coverBlock{{1, 300, 1}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := nativeEvidence{outputs: maps.Clone(good.outputs), blocks: blocks()}
			tc.change(&e)
			if err := validateNativeEvidence(repo, sites, e, nil); err == nil {
				t.Fatal("invalid export evidence accepted")
			}
		})
	}
	// There is no per-file waiver: a literal needs produced text and a counter.
	for _, text := range []string{"--approve-native", "Generated proof bundle root digest differs from verified root"} {
		one := []errorSite{siteOf(cli, text)}
		if err := validateNativeEvidence(repo, one, nativeEvidence{blocks: blocks()}, nil); err == nil {
			t.Fatalf("%q accepted without produced text", text)
		}
		zero := blocks()
		zero[cli] = []coverBlock{{1, 300, 0}}
		if err := validateNativeEvidence(repo, one, nativeEvidence{outputs: nativeOutputsFrom(cli, text), blocks: zero}, nil); err == nil {
			t.Fatalf("%q accepted without an executed position", text)
		}
		if err := validateNativeEvidence(repo, one, nativeEvidence{outputs: nativeOutputsFrom(cli, text), blocks: blocks()}, nil); err != nil {
			t.Fatal(err)
		}
	}
	// An exemption never hides a reached or covered new site.
	if err := validateNativeEvidence(repo, sites, good, map[string]string{"native CLI literal text": "claimed unreachable"}); err == nil {
		t.Fatal("stale exemption of a reached CLI site accepted")
	}
}

func TestNativeCoverageMergeKeepsOriginalCounters(t *testing.T) {
	cached := "internal/cli/judge_preview.go"
	blocks := map[string][]coverBlock{cached: {{1, 2, 0}}}
	for _, file := range []string{"internal/cli/judge_preview.go", "internal/demofixture/replay.go", "internal/bundle/demo/write.go"} {
		if err := mergeNativeCoverage(blocks, map[string][]coverBlock{nativeCoverageFiles[3]: {{1, 2, 1}}, file: {{1, 2, 1}}}); err == nil {
			t.Fatalf("merge accepted foreign %s", file)
		}
		if len(blocks) != 1 || blocks[cached][0].count != 0 {
			t.Fatalf("rejected merge changed original counters after %s", file)
		}
	}
	if err := mergeNativeCoverage(blocks, map[string][]coverBlock{nativeCoverageFiles[3]: {{1, 2, 1}}, nativeCoverageFiles[4]: {{1, 2, 1}}}); err != nil {
		t.Fatal(err)
	}
	if blocks[cached][0].count != 0 || blocks[nativeCoverageFiles[3]][0].count != 1 || blocks[nativeCoverageFiles[4]][0].count != 1 {
		t.Fatal("valid merge changed cached evidence")
	}
}

// Ownership is exact produced text: the output for an owned file is exactly the
// fresh evidence, with no per-file addition, and no waiver table exists.
func TestNativeCoverageNoWaiverBypass(t *testing.T) {
	repo := t.TempDir()
	for _, file := range nativeCoverageFiles {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "golden", nativeEvidence{outputs: nativeAllOutputs("fresh")}); got != "fresh" {
			t.Fatalf("%s output is not exactly the fresh evidence: %q", file, got)
		}
		if got := nativeLiteralOutputs(repo, pos, "golden", nativeEvidence{}); got != "" {
			t.Fatalf("%s gained output without evidence: %q", file, got)
		}
	}
	for _, name := range []string{"native_coverage_test.go", "native_coverage_gate_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"nativeExport" + "Waivers", "nativeWaived" + "Text", "nativeWaiver" + " struct", "nativeArgument" + "Literals"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s reintroduces %s", name, forbidden)
			}
		}
	}
	if err := validateNativeEvidence(repo, []errorSite{{text: "unlogged refusal text", file: filepath.Join(repo, "internal/cli/judge_demo.go"), line: 5}},
		nativeEvidence{blocks: map[string][]coverBlock{
			"internal/demofixture/native_analysis.go": {{1, 9, 1}}, "internal/demofixture/native_run.go": {{1, 9, 1}},
			"internal/demofixture/native_witness.go": {{1, 9, 1}}, "internal/cli/judge_demo.go": {{1, 9, 0}},
			"internal/bundle/demo/native_evidence.go": {{1, 9, 1}}}}, nil); err == nil {
		t.Fatal("unproduced, unexecuted site accepted")
	}
}

// nativeOutputsFrom is text produced only by the package that owns file.
func nativeOutputsFrom(file, text string) map[string]string {
	return map[string]string{nativeTextKey(file): text}
}

// nativeAllOutputs is the same text produced by every owning package.
func nativeAllOutputs(text string) map[string]string {
	return map[string]string{nativeDemofixturePkg: text, nativeCLIPkg: text, nativeDemoPkg: text, nativeProofviewPkg: text, nativeGuidedPkg: text,
		nativeFreshTextKeys[nativeSocketOverlappedFile]: text}
}

// Diagnostic text is owned by the package that produced it, in both the literal
// lookup and the validator, even when another package logs identical text.
func TestNativeCoverageOutputIsolation(t *testing.T) {
	repo := t.TempDir()
	const same = "approval must match the proposal and record a canonical time"
	files := map[string]string{
		nativeDemofixturePkg: "internal/demofixture/native_run.go",
		nativeCLIPkg:         "internal/cli/judge_demo.go",
		nativeDemoPkg:        "internal/bundle/demo/native_evidence.go",
		nativeProofviewPkg:   "internal/proofview/incident_server.go",
		nativeGuidedPkg:      "internal/guided/guided.go",
	}
	for _, file := range nativeProofviewFiles {
		if nativeOwner(file) != nativeProofviewPkg {
			t.Fatalf("%s owner %q", file, nativeOwner(file))
		}
	}
	for _, file := range nativeDemofixtureFiles {
		if nativeOwner(file) != nativeDemofixturePkg {
			t.Fatalf("%s owner %q", file, nativeOwner(file))
		}
	}
	blocks := map[string][]coverBlock{}
	for _, name := range nativeCoverageFiles {
		blocks[name] = []coverBlock{{1, 300, 1}}
	}
	for producer := range files {
		for consumer, file := range files {
			evidence := nativeEvidence{outputs: map[string]string{producer: strconv.Quote(same) + "\n"}, blocks: blocks}
			site := []errorSite{{text: same, file: filepath.Join(repo, filepath.FromSlash(file)), line: 10}}
			pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
			got := nativeLiteralOutputs(repo, pos, "golden", evidence)
			err := validateNativeEvidence(repo, site, evidence, nil)
			if producer == consumer {
				if !strings.Contains(got, same) || err != nil {
					t.Fatalf("%s own text rejected: %q %v", file, got, err)
				}
				continue
			}
			if strings.Contains(got, same) || err == nil {
				t.Fatalf("%s text satisfied %s: %q %v", producer, file, got, err)
			}
		}
	}
	// Complete contract prose is demofixture-owned and never reaches the export files.
	prose := nativeEvidence{outputs: map[string]string{nativeDemofixturePkg: "complete contract prose\n"}, blocks: blocks}
	for _, file := range nativeExportFiles {
		site := []errorSite{{text: "complete contract prose", file: filepath.Join(repo, filepath.FromSlash(file)), line: 10}}
		if err := validateNativeEvidence(repo, site, prose, nil); err == nil {
			t.Fatalf("demofixture prose satisfied %s", file)
		}
	}
}

// Each profile subset keeps exactly its expected files and requires all of them.
func TestNativeCoverageProfileSubsets(t *testing.T) {
	line := func(file string, count int) string {
		return "github.com/Mizore66/faultline/" + file + ":1.1,300.2 1 " + strconv.Itoa(count) + "\n"
	}
	var demofixtureOnly, exportOnly string
	for _, file := range nativeDemofixtureFiles {
		demofixtureOnly += line(file, 1)
	}
	for _, file := range nativeExportFiles {
		exportOnly += line(file, 1)
	}
	// The CLI binary incidentally emits zero-hit demofixture counters; the
	// demofixture binary incidentally emits zero-hit CLI/demo counters.
	incidentalDemofixture := strings.ReplaceAll(demofixtureOnly, " 1 1\n", " 1 0\n")
	incidentalExport := strings.ReplaceAll(exportOnly, " 1 1\n", " 1 0\n")
	native, err := nativeProfileFor([]byte("mode: set\n"+demofixtureOnly+incidentalExport), nativeDemofixtureFiles)
	if err != nil || len(native) != 3 || native[nativeExportFiles[0]] != nil || native[nativeExportFiles[1]] != nil {
		t.Fatalf("demofixture subset kept export counters: %v %v", native, err)
	}
	export, err := nativeProfileFor([]byte("mode: set\n"+incidentalDemofixture+exportOnly), nativeExportFiles)
	if err != nil || len(export) != 2 {
		t.Fatalf("export subset: %v %v", export, err)
	}
	for _, file := range nativeDemofixtureFiles {
		if export[file] != nil {
			t.Fatalf("export subset kept %s", file)
		}
		if native[file][0].count != 1 {
			t.Fatalf("incidental counter replaced original evidence for %s", file)
		}
	}
	for _, missing := range nativeDemofixtureFiles {
		var data string
		for _, file := range nativeDemofixtureFiles {
			if file != missing {
				data += line(file, 1)
			}
		}
		if _, err := nativeProfileFor([]byte("mode: set\n"+data+exportOnly), nativeDemofixtureFiles); err == nil {
			t.Fatalf("demofixture subset accepted without %s", missing)
		}
	}
	for _, missing := range nativeExportFiles {
		var data string
		for _, file := range nativeExportFiles {
			if file != missing {
				data += line(file, 1)
			}
		}
		if _, err := nativeProfileFor([]byte("mode: set\n"+incidentalDemofixture+data), nativeExportFiles); err == nil {
			t.Fatalf("export subset accepted without %s", missing)
		}
	}
	var proofviewOnly string
	for _, file := range nativeProofviewFiles {
		proofviewOnly += line(file, 1)
	}
	proofview, err := nativeProfileFor([]byte("mode: set\n"+incidentalDemofixture+incidentalExport+proofviewOnly), nativeProofviewFiles)
	if err != nil || len(proofview) != 4 || proofview[nativeExportFiles[0]] != nil || proofview[nativeDemofixtureFiles[0]] != nil {
		t.Fatalf("proofview subset: %v %v", proofview, err)
	}
	for _, missing := range nativeProofviewFiles {
		var data string
		for _, file := range nativeProofviewFiles {
			if file != missing {
				data += line(file, 1)
			}
		}
		if _, err := nativeProfileFor([]byte("mode: set\n"+data), nativeProofviewFiles); err == nil {
			t.Fatalf("proofview subset accepted without %s", missing)
		}
	}
	var guidedOnly string
	for _, file := range nativeGuidedFiles {
		guidedOnly += line(file, 1)
	}
	guided, err := nativeProfileFor([]byte("mode: set\n"+incidentalDemofixture+incidentalExport+guidedOnly), nativeGuidedFiles)
	if err != nil || len(guided) != 3 || guided[nativeExportFiles[0]] != nil || guided[nativeProofviewFiles[0]] != nil {
		t.Fatalf("guided subset: %v %v", guided, err)
	}
	for _, missing := range nativeGuidedFiles {
		var data string
		for _, file := range nativeGuidedFiles {
			if file != missing {
				data += line(file, 1)
			}
		}
		if _, err := nativeProfileFor([]byte("mode: set\n"+data), nativeGuidedFiles); err == nil {
			t.Fatalf("guided subset accepted without %s", missing)
		}
	}
	merged, err := mergeDisjointNativeProfiles(native, export)
	if err == nil {
		merged, err = mergeDisjointNativeProfiles(merged, proofview)
	}
	if err == nil {
		merged, err = mergeDisjointNativeProfiles(merged, guided)
	}
	if err != nil || len(merged) != 12 || len(native) != 3 || len(export) != 2 || len(proofview) != 4 {
		t.Fatalf("disjoint merge: %v %d", err, len(merged))
	}
	original := native[nativeDemofixtureFiles[0]][0].count
	overlap := map[string][]coverBlock{nativeDemofixtureFiles[0]: {{1, 2, 0}}}
	if _, err := mergeDisjointNativeProfiles(native, overlap); err == nil || native[nativeDemofixtureFiles[0]][0].count != original || len(native) != 3 {
		t.Fatalf("overlapping profiles accepted or originals changed: %v", err)
	}
}

// The two invocations are built purely and exactly.
func TestNativeEvidenceCommands(t *testing.T) {
	commands := nativeEvidenceCommands("/repo", "/tmp/a.txt", "/tmp/b.txt", "/tmp/c.txt", "/tmp/d.txt")
	if len(commands) != 4 {
		t.Fatalf("commands: %d", len(commands))
	}
	want := [][]string{
		{"test", "-json", "-count=1", "-timeout=150s", "-coverprofile=/tmp/a.txt",
			"-run=^(TestNativeDiagnostics|TestNativeDiagnosticInvariants|TestNativeAnalysisFullContract)$", "./internal/demofixture",
			"-native-contract=" + filepath.Join("/repo", "difftest", "testdata", "native-demo", "native-contract.jsonl")},
		{"test", "-json", "-count=1", "-timeout=150s", "-coverprofile=/tmp/b.txt",
			"-run=^(TestJudgeDemoNativeDiagnostics|TestNativeEvidenceDiagnostics)$", "./internal/cli", "./internal/bundle/demo"},
		{"test", "-json", "-count=1", "-timeout=150s", "-coverprofile=/tmp/c.txt",
			"-run=^(TestInteractiveIncidentPage|TestInteractiveIncidentReplaceOnceDrift|TestInteractiveIncidentListenerDrift|TestIncidentServerHTTP|TestIncidentServerSingleFlight|TestIncidentServerClose|TestPlainDemoServerClose|TestSocketOverlappedNativeDiagnostics)$", "./internal/proofview"},
		{"test", "-json", "-count=1", "-timeout=150s", "-coverprofile=/tmp/d.txt",
			"-run=^TestGuidedNativeDiagnostics$", "-p=1", "./internal/guided"},
	}
	for i, command := range commands {
		if strings.Join(command.args, "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("command %d argv: %q", i, command.args)
		}
	}
	for i, flagCount := range []int{1, 0, 0, 0} {
		n := 0
		for _, arg := range commands[i].args {
			if strings.HasPrefix(arg, "-native-contract=") {
				n++
			}
		}
		if n != flagCount {
			t.Fatalf("command %d has %d -native-contract flags", i, n)
		}
	}
	if commands[0].profile != "/tmp/a.txt" || commands[1].profile != "/tmp/b.txt" || commands[2].profile != "/tmp/c.txt" || commands[3].profile != "/tmp/d.txt" {
		t.Fatal("profiles are crossed")
	}
	if len(commands[0].packages) != 1 || commands[0].packages[0].importPath != nativeDemofixturePkg ||
		len(commands[1].packages) != 2 || commands[1].packages[0].importPath != nativeCLIPkg || commands[1].packages[1].importPath != nativeDemoPkg ||
		len(commands[2].packages) != 1 || commands[2].packages[0].importPath != nativeProofviewPkg ||
		len(commands[3].packages) != 1 || commands[3].packages[0].importPath != nativeGuidedPkg {
		t.Fatal("package ownership is wrong")
	}
	// One shared 240-second context covers all exec commands.
	data, err := os.ReadFile("native_coverage_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "context.WithTimeout(context.Background(), 240*time.Second)") != 1 ||
		strings.Count(string(data), "exec.CommandContext(ctx") != 1 {
		t.Fatal("all commands must share one 240-second context")
	}
}

// The compatibility note states the real requirement and no waiver.
func TestNativeKnownDifferencesNoWaiver(t *testing.T) {
	data, err := os.ReadFile("KNOWN_DIFFERENCES.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "nativeExport"+"Waivers") || !strings.Contains(text, "produced diagnostic text and a positive counter") {
		t.Fatal("KNOWN_DIFFERENCES.md misstates native export coverage")
	}
}

// ---- Portable overlapped-socket core (internal/proofview/socket_overlapped.go) ----

// socketRegions are synthetic site positions, one per region of the core. Each
// needs its own positive counter and its own produced text.
var socketRegions = []struct {
	name string
	line int
	text string
}{
	{"domain", 20, "synthetic domain refusal text"},
	{"submit", 120, "synthetic submit refusal text"},
	{"post", 220, "synthetic post refusal text"},
	{"fence", 320, "synthetic fence refusal text"},
	{"cancel", 420, "synthetic cancel refusal text"},
	{"release", 520, "synthetic release refusal text"},
}

func socketSites(repo string) []errorSite {
	var sites []errorSite
	for _, r := range socketRegions {
		sites = append(sites, errorSite{text: r.text, file: filepath.Join(repo, filepath.FromSlash(nativeSocketOverlappedFile)), line: r.line})
	}
	return sites
}

func socketProduced() string {
	var out strings.Builder
	for _, r := range socketRegions {
		out.WriteString(strconv.Quote(r.text) + "\n")
	}
	return out.String()
}

// Narrow one-line blocks: a counter at one position never covers another.
func socketBlocks(zeroLine int) map[string][]coverBlock {
	out := map[string][]coverBlock{}
	for _, name := range nativeCoverageFiles {
		out[name] = []coverBlock{{1, 5, 1}}
	}
	out[nativeSocketOverlappedFile] = nil
	for _, r := range socketRegions {
		count := 1
		if r.line == zeroLine {
			count = 0
		}
		out[nativeSocketOverlappedFile] = append(out[nativeSocketOverlappedFile], coverBlock{r.line - 2, r.line + 2, count})
	}
	return out
}

func TestNativeSocketOverlappedPositions(t *testing.T) {
	repo := t.TempDir()
	sites := socketSites(repo)
	good := nativeEvidence{outputs: nativeOutputsFrom(nativeSocketOverlappedFile, socketProduced()), blocks: socketBlocks(-1)}
	if err := validateNativeEvidence(repo, sites, good, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*nativeEvidence)
	}{
		{"file-blocks-absent", func(e *nativeEvidence) { delete(e.blocks, nativeSocketOverlappedFile) }},
		{"file-blocks-empty", func(e *nativeEvidence) { e.blocks[nativeSocketOverlappedFile] = nil }},
		{"all-zero-hit", func(e *nativeEvidence) { e.blocks[nativeSocketOverlappedFile] = []coverBlock{{1, 600, 0}} }},
		{"site-outside-every-block", func(e *nativeEvidence) {
			e.blocks[nativeSocketOverlappedFile] = []coverBlock{{1, 5, 1}}
		}},
		{"text-absent-despite-positive-counters", func(e *nativeEvidence) { e.outputs = map[string]string{} }},
		{"text-only-in-sibling-proofview-key", func(e *nativeEvidence) {
			e.outputs = map[string]string{nativeProofviewPkg: socketProduced()}
		}},
		{"expected-text-borrowed-from-package-key", func(e *nativeEvidence) {
			// The package-wide key also carries committed expected text; it must not count.
			e.outputs[nativeProofviewPkg] = socketProduced()
			delete(e.outputs, nativeTextKey(nativeSocketOverlappedFile))
		}},
		{"text-only-in-other-package", func(e *nativeEvidence) {
			e.outputs = map[string]string{nativeGuidedPkg: socketProduced(), nativeCLIPkg: socketProduced()}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := nativeEvidence{outputs: maps.Clone(good.outputs), blocks: socketBlocks(-1)}
			tc.change(&e)
			if err := validateNativeEvidence(repo, sites, e, nil); err == nil {
				t.Fatal("invalid socket evidence accepted")
			}
		})
	}
	for _, r := range socketRegions {
		t.Run("single-zero-hit-"+r.name, func(t *testing.T) {
			e := nativeEvidence{outputs: maps.Clone(good.outputs), blocks: socketBlocks(r.line)}
			if err := validateNativeEvidence(repo, sites, e, nil); err == nil {
				t.Fatalf("zero-hit %s position accepted although every text is produced", r.name)
			}
		})
		t.Run("single-text-absent-"+r.name, func(t *testing.T) {
			text := strings.Replace(socketProduced(), strconv.Quote(r.text)+"\n", "", 1)
			e := nativeEvidence{outputs: nativeOutputsFrom(nativeSocketOverlappedFile, text), blocks: socketBlocks(-1)}
			if err := validateNativeEvidence(repo, sites, e, nil); err == nil {
				t.Fatalf("absent %s text accepted although every counter is positive", r.name)
			}
		})
		t.Run("exemption-never-hides-"+r.name, func(t *testing.T) {
			if err := validateNativeEvidence(repo, sites, good, map[string]string{r.text: "claimed unreachable"}); err == nil {
				t.Fatal("exemption of a reached core site accepted")
			}
		})
	}
	// A neighbour file's counter or text cannot satisfy the core.
	for _, neighbour := range []string{"internal/proofview/socket_overlapped_windows.go", "internal/proofview/socket_writer.go", "internal/proofview/incident_server.go"} {
		t.Run("neighbour-"+filepath.Base(neighbour), func(t *testing.T) {
			e := nativeEvidence{outputs: maps.Clone(good.outputs), blocks: socketBlocks(-1)}
			e.blocks[nativeSocketOverlappedFile] = nil
			e.blocks[neighbour] = []coverBlock{{1, 600, 1}}
			if err := validateNativeEvidence(repo, sites, e, nil); err == nil {
				t.Fatalf("counter of %s satisfied the core", neighbour)
			}
		})
	}
}

// Committed proofview expected text and the other parents' records live under
// the package key only; they must never satisfy the core, and the core's own
// records must never satisfy the legacy proofview files.
func TestNativeSocketOverlappedTextIsolation(t *testing.T) {
	repo := t.TempDir()
	const same = "approval must match the proposal and record a canonical time"
	coreKey := nativeTextKey(nativeSocketOverlappedFile)
	if coreKey == nativeProofviewPkg || nativeOwner(nativeSocketOverlappedFile) != nativeProofviewPkg {
		t.Fatalf("core key %q owner %q", coreKey, nativeOwner(nativeSocketOverlappedFile))
	}
	for _, file := range nativeProofviewFiles {
		if file != nativeSocketOverlappedFile && nativeTextKey(file) != nativeProofviewPkg {
			t.Fatalf("legacy owner changed for %s: %q", file, nativeTextKey(file))
		}
	}
	blocks := map[string][]coverBlock{}
	for _, name := range nativeCoverageFiles {
		blocks[name] = []coverBlock{{1, 300, 1}}
	}
	site := func(file string) []errorSite {
		return []errorSite{{text: same, file: filepath.Join(repo, filepath.FromSlash(file)), line: 10}}
	}
	pkgOnly := nativeEvidence{outputs: map[string]string{nativeProofviewPkg: strconv.Quote(same) + "\n"}, blocks: blocks}
	if err := validateNativeEvidence(repo, site(nativeSocketOverlappedFile), pkgOnly, nil); err == nil {
		t.Fatal("package-wide proofview text satisfied the core")
	}
	if err := validateNativeEvidence(repo, site("internal/proofview/incident_server.go"), pkgOnly, nil); err != nil {
		t.Fatalf("legacy proofview owner weakened: %v", err)
	}
	// Decode a real event fixture whose core child collides with a legacy site.
	// Do not hand-build the isolated map: the old decoder leaked this record
	// into the package key and would incorrectly accept the legacy sites below.
	events := nativeCoverageEvents()
	for _, event := range events {
		if event["Test"] == nativeSocketOverlappedParent+"/"+nativeSocketChildren[0] && event["Action"] == "output" {
			event["Output"] = "NATIVE-DIAGNOSTIC " + strconv.Quote(same) + "\n"
		}
	}
	decoded, err := nativeOutputs(nativeEventJSON(t, events), []string{"ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	coreOnly := nativeEvidence{outputs: decoded, blocks: blocks}
	if err := validateNativeEvidence(repo, site(nativeSocketOverlappedFile), coreOnly, nil); err != nil {
		t.Fatal(err)
	}
	for _, file := range nativeProofviewFiles {
		if file == nativeSocketOverlappedFile {
			continue
		}
		if err := validateNativeEvidence(repo, site(file), coreOnly, nil); err == nil {
			t.Fatalf("core-parent text satisfied legacy file %s", file)
		}
	}
}

// An entirely absent dynamic child is accepted by the event decoder while other
// checked children remain. The full validator must still require its unique
// site's text and counter; redundant children are not a pinned manifest.
func TestNativeSocketOverlappedWholeChildRemoval(t *testing.T) {
	repo := t.TempDir()
	var sites []errorSite
	blocks := map[string][]coverBlock{}
	for _, file := range nativeCoverageFiles {
		blocks[file] = []coverBlock{{1, 5, 1}}
	}
	blocks[nativeSocketOverlappedFile] = nil
	for i, child := range nativeSocketChildren {
		line := 20 + 100*i
		sites = append(sites, errorSite{
			text: "windows socket write: " + child,
			file: filepath.Join(repo, filepath.FromSlash(nativeSocketOverlappedFile)), line: line,
		})
		blocks[nativeSocketOverlappedFile] = append(blocks[nativeSocketOverlappedFile], coverBlock{line, line, 1})
	}
	complete, err := nativeOutputs(nativeEventJSON(t, nativeCoverageEvents()), []string{"ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: complete, blocks: blocks}, nil); err != nil {
		t.Fatalf("complete child evidence rejected: %v", err)
	}
	removed := nativeWithoutTest(nativeCoverageEvents(), nativeSocketOverlappedParent+"/"+nativeSocketChildren[0])
	decoded, err := nativeOutputs(nativeEventJSON(t, removed), []string{"ordinary"})
	if err != nil {
		t.Fatalf("decoder must accept an entirely absent unpinned child: %v", err)
	}
	if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: decoded, blocks: blocks}, nil); err == nil {
		t.Fatal("whole-child removal accepted despite uniquely required text being absent")
	}
	// Restore only the missing text to distinguish counter enforcement from
	// text enforcement. These counters are synthetic, not measured execution.
	textRestored := maps.Clone(decoded)
	textRestored[nativeTextKey(nativeSocketOverlappedFile)] += strconv.Quote(sites[0].text) + "\n"
	if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: textRestored, blocks: blocks}, nil); err != nil {
		t.Fatalf("restored text with positive counters rejected: %v", err)
	}
	for _, missingBlock := range []bool{false, true} {
		name := "unique-counter-zero"
		if missingBlock {
			name = "unique-counter-absent"
		}
		t.Run(name, func(t *testing.T) {
			withoutCounter := maps.Clone(blocks)
			coreBlocks := slices.Clone(blocks[nativeSocketOverlappedFile])
			if missingBlock {
				coreBlocks = coreBlocks[1:]
			} else {
				coreBlocks[0].count = 0
			}
			withoutCounter[nativeSocketOverlappedFile] = coreBlocks
			if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: textRestored, blocks: withoutCounter}, nil); err == nil {
				t.Fatal("whole-child removal accepted without its unique counter despite restored text")
			}
		})
	}
}

// Records of the new parent land only under the isolated key; records of the
// other proofview parents land only under the legacy package key.
func TestNativeSocketOverlappedOutputsSplit(t *testing.T) {
	out, err := nativeOutputs(nativeEventJSON(t, nativeCoverageEvents()), []string{"ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	core, pkg := out[nativeTextKey(nativeSocketOverlappedFile)], out[nativeProofviewPkg]
	if !strings.Contains(core, `"windows socket write: domain-no-parts"`) || strings.Contains(core, "interactive incident") {
		t.Fatalf("isolated core text: %q", core)
	}
	if strings.Contains(pkg, "windows socket write") || !strings.Contains(pkg, "interactive incident") {
		t.Fatalf("package text: %q", pkg)
	}
	for other, text := range out {
		if other != nativeProofviewPkg && other != nativeTextKey(nativeSocketOverlappedFile) && strings.Contains(text, "windows socket write") {
			t.Fatalf("core text leaked into %s", other)
		}
	}
}

// The expected-text join stays proofview-only and never mentions the core.
func TestNativeSocketOverlappedNoExpectedTextJoin(t *testing.T) {
	data, err := os.ReadFile("native_coverage_test.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if strings.Count(src, "outputs[nativeProofviewPackage.importPath] += proofviewExpected") != 1 || strings.Count(src, "proofviewExpected") != 2 {
		t.Fatal("proofview expected text is used somewhere new")
	}
	expected, err := nativeProofviewExpectedText(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(expected, "windows socket write") {
		t.Fatal("helper text joined into the committed proofview expected text")
	}
}

// Real-source structure, static only. The Windows ABI adapter has zero
// message-flow sites; the portable core's sites are all owned and unwaived.
func TestNativeSocketOverlappedRealSourceStructure(t *testing.T) {
	repo := repoRoot(t)
	dir := filepath.Join(repo, "internal", "proofview")
	sites, err := errorSites(dir)
	if err != nil {
		t.Fatal(err)
	}
	coreSites := 0
	for _, site := range sites {
		rel, err := filepath.Rel(repo, site.file)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/proofview/socket_overlapped") && rel != nativeSocketOverlappedFile {
			t.Fatalf("%s:%d: %q is a message site outside the portable core", rel, site.line, site.text)
		}
		if rel != nativeSocketOverlappedFile {
			continue
		}
		coreSites++
		if _, ok := unreachableLiterals[site.text]; ok {
			t.Fatalf("core text %q is waived in unreachableLiterals", site.text)
		}
		if _, ok := unreachableSites[siteKey{rel, site.text}]; ok {
			t.Fatalf("core text %q is waived in unreachableSites", site.text)
		}
		if _, ok := liveOnlyUnreachable[site.text]; ok {
			t.Fatalf("core text %q is waived in liveOnlyUnreachable", site.text)
		}
	}
	if coreSites == 0 {
		t.Fatal("the portable core has no message sites; the file is not being judged")
	}
	if liveGatedFiles[nativeSocketOverlappedFile] != "" || !slices.Contains(nativeCoverageFiles, nativeSocketOverlappedFile) || !slices.Contains(nativeProofviewFiles, nativeSocketOverlappedFile) {
		t.Fatal("core file must be natively owned and not live-gated")
	}
	for _, adapter := range []string{"socket_overlapped_windows.go"} {
		if slices.Contains(nativeCoverageFiles, "internal/proofview/"+adapter) || liveGatedFiles["internal/proofview/"+adapter] != "" {
			t.Fatalf("%s must stay outside native ownership", adapter)
		}
	}
	core, err := os.ReadFile(filepath.Join(dir, "socket_overlapped.go"))
	if err != nil {
		t.Fatal(err)
	}
	head := string(core)
	if i := strings.Index(head, "\npackage "); i >= 0 {
		head = head[:i]
	}
	if strings.Contains(head, "windows") && !strings.Contains(head, "linux || darwin || windows") {
		t.Fatalf("core build constraint is not portable: %q", head)
	}
	forbidden, err := socketForbiddenDependencies(core)
	if err != nil {
		t.Fatal(err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("portable core uses forbidden dependencies: %v", forbidden)
	}
}

// Inspect syntax, not raw source: explanatory comments and string literals are
// not dependencies. Import aliases and dot imports must not hide Windows ABI use.
func socketForbiddenDependencies(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "socket_overlapped.go", source, 0)
	if err != nil {
		return nil, err
	}
	var forbidden []string
	syscallNames := map[string]bool{}
	dotSyscall := false
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if path == "unsafe" || path == "windows" || strings.HasSuffix(path, "/windows") {
			forbidden = append(forbidden, "import "+path)
		}
		if path == "syscall" {
			name := "syscall"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			dotSyscall = dotSyscall || name == "."
			syscallNames[name] = true
		}
	}
	windowsABI := func(name string) bool {
		return name == "Handle" || name == "Overlapped" || name == "WSABuf"
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := node.X.(*ast.Ident); ok && syscallNames[pkg.Name] && windowsABI(node.Sel.Name) {
				forbidden = append(forbidden, pkg.Name+"."+node.Sel.Name)
			}
		case *ast.Ident:
			if node.Name == "ERROR_IO_PENDING" || dotSyscall && windowsABI(node.Name) {
				forbidden = append(forbidden, node.Name)
			}
		}
		return true
	})
	return forbidden, nil
}

func TestNativeSocketOverlappedForbiddenDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		forbidden    bool
	}{
		{"comment-and-literal", "package core\nimport \"syscall\"\n// ERROR_IO_PENDING syscall.Handle syscall.Overlapped syscall.WSABuf unsafe\nconst pending = syscall.Errno(997)\nconst explanation = \"ERROR_IO_PENDING syscall.Handle unsafe\"\n", false},
		{"unsafe-import", "package core\nimport _ \"unsafe\"\n", true},
		{"windows-import", "package core\nimport _ \"golang.org/x/sys/windows\"\n", true},
		{"handle", "package core\nimport \"syscall\"\nvar value syscall.Handle\n", true},
		{"overlapped", "package core\nimport \"syscall\"\nvar value syscall.Overlapped\n", true},
		{"wsabuf", "package core\nimport \"syscall\"\nvar value syscall.WSABuf\n", true},
		{"aliased-abi", "package core\nimport abi \"syscall\"\nvar value abi.Overlapped\n", true},
		{"dot-import-abi", "package core\nimport . \"syscall\"\nvar value WSABuf\n", true},
		{"pending-identifier", "package core\nvar value = ERROR_IO_PENDING\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := socketForbiddenDependencies([]byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if (len(found) != 0) != tc.forbidden {
				t.Fatalf("forbidden dependencies = %v, want rejection %v", found, tc.forbidden)
			}
		})
	}
}
