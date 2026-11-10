package proofview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func turnDashboardFixture(t *testing.T) jsjson.Value {
	t.Helper()
	str := jsjson.MakeString
	state := func(id string, ordinal int, verdict string) jsjson.Value {
		return jsjson.MustObject(
			"turnId", str(id), "turnOrdinal", jsjson.MakeNumber(float64(ordinal)),
			"verdict", str(verdict), "treeDigest", str("tree-"+id), "snapshotDigest", str("snapshot-"+id),
			"executionIds", jsjson.MakeArray([]jsjson.Value{str("exec-" + id)}), "runIds", jsjson.MakeArray([]jsjson.Value{str("run-" + id)}))
	}
	return jsjson.MustObject(
		"status", str("COMPLETED"),
		"proof", jsjson.MustObject(
			"isProof", jsjson.MakeBool(true), "reason", str("<reason>"),
			"evidenceGrade", str("TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL"), "evidenceLabel", str("label")),
		"environmentHomogeneity", str("HOMOGENEOUS"),
		"introduction", jsjson.MustObject("status", str("ATTRIBUTED"), "reason", str("<intro>")),
		"errors", jsjson.MakeArray([]jsjson.Value{str("example error")}),
		"stableStates", jsjson.MakeArray([]jsjson.Value{state("turn-1", 1, "PASS"), state("turn-2", 2, "FAIL")}),
		"transitions", jsjson.MakeArray([]jsjson.Value{jsjson.MustObject(
			"kind", str("PASS_TO_FAIL"),
			"before", jsjson.MustObject("turnOrdinal", jsjson.MakeNumber(1)),
			"after", jsjson.MustObject("turnOrdinal", jsjson.MakeNumber(2)))}),
		"repository", str("repo"), "ledgerDigest", str("ledger"), "executionsPerState", jsjson.MakeNumber(3),
		"recorder", str("codex-turn-tree-replay"), "nativeCodexInterception", jsjson.MakeBool(false),
		"runtimeMapping", jsjson.MustObject())
}

func TestRenderTurnDashboardContract(t *testing.T) {
	got := RenderTurnDashboard(turnDashboardFixture(t))
	for _, text := range []string{"COMPLETED", "PROOF", "TURN_PATH_EVIDENCE_GRADE_EXPERIMENTAL", "turn-1", "turn-2", "PASS_TO_FAIL", "run-turn-1", "run-turn-2", "tree-turn-1", "snapshot-turn-1"} {
		if !strings.Contains(got, text) {
			t.Errorf("dashboard lacks %q", text)
		}
	}
	if strings.Contains(got, "<script>") || strings.Contains(got, "<reason>") {
		t.Fatal("unescaped dynamic content")
	}
	if !strings.Contains(got, "&lt;reason&gt;") {
		t.Fatal("reason not escaped")
	}
	want, err := os.ReadFile("../../difftest/testdata/ui-hub/turn-dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("dashboard differs from golden:\n%s", got)
	}
}

func TestRenderTurnDashboardVariants(t *testing.T) {
	str := jsjson.MakeString
	result := jsjson.MustObject(
		"status", str("FAILED"),
		"proof", jsjson.MustObject("isProof", jsjson.MakeBool(false)),
		"errors", jsjson.MakeArray(nil),
		"stableStates", jsjson.MakeArray(nil),
		"transitions", jsjson.MakeArray(nil),
		"nativeCodexInterception", jsjson.MakeBool(true),
		"runtimeMapping", jsjson.MustObject("a<b", str("c&d")))
	got := RenderTurnDashboard(result)
	for _, text := range []string{"NOT PROOF", "No errors", "No stable states", "No transitions", "Runtime mapping a&lt;b", "c&amp;d", "<span>true</span>"} {
		if !strings.Contains(got, text) {
			t.Errorf("dashboard lacks %q", text)
		}
	}
}

func TestRenderTurnVerificationFailure(t *testing.T) {
	got := RenderTurnVerificationFailure([]string{"a<b", "c&d"})
	want := "<!doctype html><html><body><h1>Turn package failed verification</h1><pre>a&lt;b\nc&amp;d</pre></body></html>"
	if got != want {
		t.Fatalf("failure page = %q", got)
	}
}

func TestRenderRawJSONPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x<y>.json")
	if err := os.WriteFile(path, []byte(`{"a":"<script>&'\""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RenderRawJSONPage(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>x&lt;y&gt;.json</title>
<style>body{margin:0;background:#111;color:#e7e5e4;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
pre{margin:0;padding:1.25rem;white-space:pre-wrap;word-break:break-word}</style></head>
<body><pre>{&quot;a&quot;:&quot;&lt;script&gt;&amp;&#039;\&quot;&quot;}</pre></body></html>`
	if got != want {
		t.Fatalf("raw page = %q", got)
	}
	if _, err := RenderRawJSONPage(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing file must error")
	}
}
