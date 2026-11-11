package proofview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHubFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeHubBundle(t *testing.T, dir, manifest string) {
	t.Helper()
	writeHubFile(t, filepath.Join(dir, "ROOT.sha256"), "x")
	writeHubFile(t, filepath.Join(dir, "manifest.json"), manifest)
	writeHubFile(t, filepath.Join(dir, "investigation.json"), "{}")
}

const hubGitManifest = `{"schemaVersion":"faultline.git-proof.v1"}`
const hubTurnManifest = `{"schemaVersion":"faultline.turn-proof.v1"}`

func TestDiscoverFaultLineArtifactsOrderAndKinds(t *testing.T) {
	root := t.TempDir()
	fl := filepath.Join(root, ".faultline")
	writeHubBundle(t, filepath.Join(fl, "git-proof-bundles", "git"), hubGitManifest)
	writeHubBundle(t, filepath.Join(fl, "turn-proof-bundles", "turn"), hubTurnManifest)
	writeHubBundle(t, filepath.Join(fl, "bundles", "any"), hubGitManifest)
	writeHubBundle(t, filepath.Join(fl, "proofs", "nested"), hubTurnManifest)
	writeHubFile(t, filepath.Join(fl, "incidents", "drafts", "a.json"), "{}")
	writeHubFile(t, filepath.Join(fl, "recordings", "ledger.json"), "{}")
	// Ignored entries.
	writeHubBundle(t, filepath.Join(fl, "git-proof-bundles", ".hidden"), hubGitManifest)
	writeHubBundle(t, filepath.Join(fl, "git-proof-bundles", "turnish"), hubTurnManifest)
	writeHubFile(t, filepath.Join(fl, "recordings", "turn-snapshot-cache.json"), "{}")
	writeHubFile(t, filepath.Join(fl, "recordings", "note.txt"), "x")
	writeHubFile(t, filepath.Join(fl, "recordings", ".h.json"), "{}")
	writeHubFile(t, filepath.Join(root, "outside", "o.json"), "{}")
	if err := os.Symlink(filepath.Join(root, "outside", "o.json"), filepath.Join(fl, "recordings", "link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fl, "bundles", "any"), filepath.Join(fl, "bundles", "linkdir")); err != nil {
		t.Fatal(err)
	}

	got := DiscoverFaultLineArtifacts(root)
	want := []string{"git-proof-bundle/git", "turn-proof-bundle/turn", "git-proof-bundle/any", "turn-proof-bundle/nested", "incident-draft/a", "ledger/ledger"}
	if len(got) != len(want) {
		t.Fatalf("artifacts=%v", got)
	}
	for i, artifact := range got {
		if artifact.Kind+"/"+artifact.ID != want[i] {
			t.Fatalf("artifact %d = %s/%s, want %s", i, artifact.Kind, artifact.ID, want[i])
		}
		if !filepath.IsAbs(artifact.Path) {
			t.Fatalf("path not absolute: %s", artifact.Path)
		}
	}
	if got[5].Label != "ledger.json" || got[4].Label != "a" {
		t.Fatalf("labels: %v", got)
	}
}

func TestDiscoverFaultLineArtifactsMalformedTurnFallsBackToGit(t *testing.T) {
	root := t.TempDir()
	fl := filepath.Join(root, ".faultline")
	writeHubBundle(t, filepath.Join(fl, "bundles", "bad"), `{not json`)
	writeHubBundle(t, filepath.Join(fl, "bundles", "num"), `{"schemaVersion":5}`)
	// A git manifest under turn-proof-bundles is not a turn bundle.
	writeHubBundle(t, filepath.Join(fl, "turn-proof-bundles", "notturn"), hubGitManifest)
	got := DiscoverFaultLineArtifacts(root)
	if len(got) != 2 || got[0].Kind != "git-proof-bundle" || got[1].Kind != "git-proof-bundle" {
		t.Fatalf("artifacts=%v", got)
	}
}

func TestDiscoverFaultLineArtifactsMissingAndDuplicates(t *testing.T) {
	root := t.TempDir()
	got := DiscoverFaultLineArtifacts(root)
	if got == nil || len(got) != 0 {
		t.Fatalf("missing .faultline: %#v", got)
	}
	writeHubFile(t, filepath.Join(root, ".faultline"), "x")
	if got := DiscoverFaultLineArtifacts(root); got == nil || len(got) != 0 {
		t.Fatalf("file .faultline: %#v", got)
	}
	root2 := t.TempDir()
	real := filepath.Join(root2, "real")
	writeHubBundle(t, filepath.Join(real, "bundles", "a"), hubGitManifest)
	if err := os.Symlink(real, filepath.Join(root2, ".faultline")); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverFaultLineArtifacts(root2); len(got) != 0 {
		t.Fatalf("symlinked .faultline followed: %v", got)
	}
	root3 := t.TempDir()
	writeHubBundle(t, filepath.Join(root3, ".faultline", "bundles", "a"), hubGitManifest)
	got = DiscoverFaultLineArtifacts(root3 + string(filepath.Separator) + "." + string(filepath.Separator))
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("artifacts=%v", got)
	}
}

func TestRenderHubIndexEmptyAndEscaped(t *testing.T) {
	empty := RenderHubIndex(`/tmp/<b>&"repo`, []DiscoveredFaultLineArtifact{})
	for _, want := range []string{`<p class="empty empty-state">No bundles, incidents, or ledgers found under <code>.faultline</code>.<br>Next: <code class="next-command">fl quickstart</code></p>`, "<h1>FaultLine UI</h1>\n<p class=\"lede\">Local hub for <code>/tmp/&lt;b&gt;&amp;&quot;repo</code>.", "\n</main></body></html>"} {
		if !strings.Contains(empty, want) {
			t.Fatalf("missing %q in %s", want, empty)
		}
	}
	if UIHubEmptyNextCommand != "fl quickstart" {
		t.Fatal(UIHubEmptyNextCommand)
	}
	html := RenderHubIndex("/r", []DiscoveredFaultLineArtifact{
		{Kind: "git-proof-bundle", ID: "g", Path: "/p/<g>", Label: "g"},
		{Kind: "ledger", ID: "l", Path: "/p/l.json", Label: `<script>alert(1)</script>`},
	})
	if strings.Contains(html, "<script>") || strings.Contains(html, `class="empty-state"`) {
		t.Fatalf("unescaped or empty state: %s", html)
	}
	for _, want := range []string{
		`<li><a href="/artifact/0"><strong>git-proof-bundle</strong> — g</a><small>/p/&lt;g&gt;</small></li>`,
		`<li><a href="/raw/1"><strong>ledger</strong> — &lt;script&gt;alert(1)&lt;/script&gt;</a><small>/p/l.json</small></li>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}
