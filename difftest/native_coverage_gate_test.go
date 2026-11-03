//go:build faultline_test

package difftest

import (
	"encoding/json"
	"path/filepath"
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
	if err != nil || out != "\"native analysis rejected the invocation\"\n" {
		t.Fatalf("valid observed output: %q %v", out, err)
	}
}

func nativeCoverageEvents() []map[string]any {
	pkg := "github.com/Mizore66/faultline/internal/demofixture"
	var events []map[string]any
	add := func(action, test string) {
		event := map[string]any{"Action": action, "Package": pkg}
		if test != "" {
			event["Test"] = test
		}
		events = append(events, event)
	}
	add("start", "")
	for _, parent := range []string{"TestNativeDiagnostics", "TestNativeDiagnosticInvariants", "TestNativeAnalysisFullContract"} {
		add("run", parent)
		children := []string{"rejected"}
		if parent == "TestNativeDiagnosticInvariants" {
			children = []string{"timeline", "partial", "subsets", "environment"}
		}
		if parent == "TestNativeAnalysisFullContract" {
			children = []string{"ordinary"}
		}
		for _, child := range children {
			name := parent + "/" + child
			add("run", name)
			if parent == "TestNativeDiagnostics" {
				events = append(events, map[string]any{"Action": "output", "Package": pkg, "Test": name, "Output": "    test.go:1: NATIVE-DIAGNOSTIC \"native analysis rejected the invocation\"\n"})
			}
			add("pass", name)
		}
		add("pass", parent)
	}
	add("pass", "")
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
			if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: tc.output, blocks: b}, tc.exempt); err == nil {
				t.Fatal("invalid positional evidence accepted")
			}
		})
	}
	if err := validateNativeEvidence(repo, sites, nativeEvidence{outputs: text, blocks: blocks()}, nil); err != nil {
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
	if err != nil || len(blocks) != 3 {
		t.Fatalf("valid native profile: %v", err)
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
	native := nativeEvidence{outputs: "native-only diagnostic"}
	for _, file := range []string{"internal/demofixture/replay.go", "internal/repair/live.go", "internal/demofixture/native_run.go.extra", "internal/demofixture/native_analysis_test.go"} {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); got != "CLI golden" {
			t.Fatalf("native output satisfied non-native file %s: %q", file, got)
		}
	}
	for _, file := range nativeCoverageFiles {
		pos := filepath.Join(repo, filepath.FromSlash(file)) + ":10:2"
		if got := nativeLiteralOutputs(repo, pos, "CLI golden", native); got != "native-only diagnostic" {
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
