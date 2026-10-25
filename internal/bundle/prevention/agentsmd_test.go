package prevention

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
)

func TestUpsertAgentsMdCreatesThenUnchanged(t *testing.T) {
	repo := t.TempDir()
	body := groundedBodyFixture(t) // a PREVENTION_VERIFIED body from the slice 1 base fixtures
	if s, err := UpsertAgentsMd(repo, body); err != nil || s != "CREATED" {
		t.Fatalf("%s %v", s, err)
	}
	if s, _ := UpsertAgentsMd(repo, body); s != "UNCHANGED" {
		t.Fatal(s)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(string(b), "<!-- FAULTLINE:BEGIN -->") {
		t.Fatal(string(b))
	}
	if info, _ := os.Stat(filepath.Join(repo, "AGENTS.md")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o644&^currentUmask() {
		t.Fatal(info.Mode())
	}
}

func TestUpsertAgentsMdWhitespaceIsUpdated(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("  \n"), 0o644)
	if s, _ := UpsertAgentsMd(repo, groundedBodyFixture(t)); s != "UPDATED" {
		t.Fatal(s)
	}
}

// The root digest written into the block is the base's own.
func TestUpsertAgentsMdUsesPackageRoot(t *testing.T) {
	repo := t.TempDir()
	if _, err := UpsertAgentsMd(repo, groundedBodyFixture(t)); err != nil {
		t.Fatal(err)
	}
	m, _ := os.ReadFile(filepath.Join(basesDir, "prevention-verified", "manifest.json"))
	manifest, _ := jsjson.Parse(string(m))
	b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(string(b), "- Prevention package root: `"+manifest.Get("rootDigest").Str()+"`\n") {
		t.Fatal(string(b))
	}
}

func agentsBlock(t *testing.T, root string) string {
	t.Helper()
	block, err := renderAgentsBlock(agentsInput{
		originalProofRoot:    "sha256:" + strings.Repeat("a", 64),
		frozenWitnessDigest:  "sha256:" + strings.Repeat("b", 64),
		preventionRootDigest: root,
		lastGoodRunIDs:       []string{"sha256:" + strings.Repeat("d", 64)},
		firstBadRunIDs:       []string{"sha256:" + strings.Repeat("e", 64)},
		repairedRunIDs:       []string{"sha256:" + strings.Repeat("f", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return block
}

// Ports tests/agents-md.test.ts "creates and idempotently replaces the
// marked FaultLine block".
func TestAgentsMdReplacesBlock(t *testing.T) {
	first := agentsBlock(t, "sha256:"+strings.Repeat("c", 64))
	if next := mergeAgentsMd("", first); next != first {
		t.Fatal(next)
	}
	if mergeAgentsMd(first, first) != first {
		t.Fatal("not idempotent")
	}
	third := agentsBlock(t, "sha256:"+strings.Repeat("1", 64))
	updated := mergeAgentsMd(first, third)
	if !strings.Contains(updated, strings.Repeat("1", 64)) || strings.Count(updated, agentsMdBegin) != 1 {
		t.Fatal(updated)
	}
}

// Ports "refuses non-digest payload fields".
func TestAgentsMdRefusesNonDigest(t *testing.T) {
	repo := t.TempDir()
	body := groundedBodyFixture(t)
	body.Obj().Set("frozenWitnessDigest", jsjson.MakeString("not-a-digest"))
	if _, err := UpsertAgentsMd(repo, body); err == nil || err.Error() != "Refusing to write non-digest frozenWitnessDigest into AGENTS.md" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Ports "preserves surrounding AGENTS.md content outside the marker block".
func TestAgentsMdPreservesSurroundings(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Project\n\nKeep me.\n"), 0o644)
	if s, err := UpsertAgentsMd(repo, groundedBodyFixture(t)); err != nil || s != "UPDATED" {
		t.Fatal(s, err)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if !strings.HasPrefix(string(b), "# Project\n\nKeep me.\n\n"+agentsMdBegin) {
		t.Fatal(string(b))
	}
}

func TestMergeAgentsMdPlacement(t *testing.T) {
	block := agentsBlock(t, "sha256:"+strings.Repeat("c", 64))
	for _, c := range []struct{ existing, want string }{
		{"a\n" + agentsMdBegin + "old" + agentsMdEnd + "\r\nz", "a\n" + block + "z"},
		{agentsMdBegin + "old" + agentsMdEnd + "\n\nz", block + "\nz"},
		{agentsMdEnd + agentsMdBegin + "x", agentsMdEnd + agentsMdBegin + "x\n\n" + block},
		{"x \u00a0\u2028\n", "x\n\n" + block},
		{"\u3000\ufeff", block},
		{"x" + agentsMdBegin + agentsMdBegin + agentsMdEnd + agentsMdEnd, "x" + block + agentsMdEnd},
	} {
		if got := mergeAgentsMd(c.existing, block); got != c.want {
			t.Errorf("%q:\ngot  %q\nwant %q", c.existing, got, c.want)
		}
	}
}
