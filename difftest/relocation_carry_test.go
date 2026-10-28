//go:build faultline_test && unix

package difftest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// carryFixture is a workspace under a root of the given extra name length.
// inv.json embeds the case root (so it is path-dependent) and hashes.txt
// cites sha256 of inv.json, like a bundle's hashes.txt. The previous
// invocation's names are computed on the unedited tree; then inv.json gets a
// real replace-text edit and hashes.txt is left (cite "original", a stale
// checksum), rewritten to the new digest ("edited") or given an unknown one
// ("fabricated").
type carryFixture struct {
	r        writerRoots
	original string // sha256 hex of inv.json before the edit
	edited   string // sha256 hex after the edit
	carried  map[string]string
}

func newCarryFixture(t *testing.T, name, cite string) carryFixture {
	t.Helper()
	base := makeWriterRoots(t)
	root := filepath.Join(base.root, name)
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	r := base
	r.root, r.ws = root, ws
	write := func(file, text string) {
		if err := os.WriteFile(filepath.Join(ws, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig := `{"recorder":"codex-turn-tree-replay","root":"` + root + `"}` + "\n"
	edited := strings.Replace(orig, "codex-turn-tree-replay", "codex-turn-tree-replaz", 1)
	f := carryFixture{r: r, original: canonical.SHA256Hex(orig), edited: canonical.SHA256Hex(edited)}
	if edited == orig || f.edited == f.original {
		t.Fatal("the edit must really change inv.json")
	}
	write("inv.json", orig)
	write("hashes.txt", "sha256:"+f.original+"  inv.json\n")
	prev := relocationWith(t, r, nil, nil).names
	write("inv.json", edited)
	switch cite {
	case "edited":
		write("hashes.txt", "sha256:"+f.edited+"  inv.json\n")
	case "fabricated":
		write("hashes.txt", "sha256:"+strings.Repeat("ab", 32)+"  inv.json\n")
	}
	o := jsjson.NewObj()
	o.Set("op", jsjson.MakeString("replace-text"))
	o.Set("file", jsjson.MakeString("inv.json"))
	o.Set("inv", jsjson.MakeNumber(1))
	f.carried = carriedNames(prev, []template{{o}}, 1, r)
	return f
}

func (f carryFixture) hashesDigest(t *testing.T, carried map[string]string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.r.ws, "hashes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return relocationCarrying(t, f.r, nil, nil, carried).fileDigest("hashes.txt", b)
}

// A stale citation of the pre-edit digest normalizes identically across
// independent roots only with the carried names (red without carry).
func TestCarriedNamesNormalizeStaleCitation(t *testing.T) {
	a := newCarryFixture(t, "a", "original")
	b := newCarryFixture(t, "a-much-longer-case-root-name", "original")
	if len(a.carried) == 0 || len(b.carried) == 0 {
		t.Fatal("no carried names for a replace-text edit of inv.json")
	}
	if a.hashesDigest(t, nil) == b.hashesDigest(t, nil) {
		t.Fatal("without carry the roots should disagree (the limitation being fixed)")
	}
	if a.hashesDigest(t, a.carried) != b.hashesDigest(t, b.carried) {
		t.Error("with carry the stale citation must normalize identically across roots")
	}
}

// A stale checksum and one rewritten to the new actual digest stay
// distinguishable; the new digest keeps its file: name; an unknown digest
// stays unnamed.
func TestCarriedNamesKeepStaleAndRewrittenApart(t *testing.T) {
	stale := newCarryFixture(t, "x", "original")
	rewritten := newCarryFixture(t, "x", "edited")
	if stale.hashesDigest(t, stale.carried) == rewritten.hashesDigest(t, rewritten.carried) {
		t.Error("a stale checksum and one rewritten to the new digest collapsed to one normalized value")
	}
	if n := relocationCarrying(t, rewritten.r, nil, nil, rewritten.carried).names[rewritten.edited]; n != "file:inv.json" {
		t.Errorf("edited digest named %q", n)
	}
	if n := relocationCarrying(t, stale.r, nil, nil, stale.carried).names[stale.original]; n != "prior:file:inv.json" {
		t.Errorf("original digest named %q", n)
	}
	fab := newCarryFixture(t, "x", "fabricated")
	if _, named := relocationCarrying(t, fab.r, nil, nil, fab.carried).names[strings.Repeat("ab", 32)]; named {
		t.Error("a fabricated digest was named")
	}
	// Different rewritten digests are not collapsed either.
	other := newCarryFixture(t, "x", "fabricated")
	if other.hashesDigest(t, other.carried) == stale.hashesDigest(t, stale.carried) {
		t.Error("a fabricated checksum equals the stale one")
	}
}

// Only flip-byte and replace-text edits carry, for their own invocation and
// file.
func TestCarriedNamesOnlyForFileEdits(t *testing.T) {
	prev := map[string]string{"aa": "file:inv.json", "bb": "json:inv.json#", "cc": "file:other.json"}
	r := writerRoots{ws: "/w", root: "/r"}
	mk := func(op string) []template {
		o := jsjson.NewObj()
		o.Set("op", jsjson.MakeString(op))
		o.Set("file", jsjson.MakeString("inv.json"))
		o.Set("inv", jsjson.MakeNumber(1))
		return []template{{o}}
	}
	for _, op := range []string{"rename", "chmod", "delete-file", "json-edit"} {
		if got := carriedNames(prev, mk(op), 1, r); len(got) != 0 {
			t.Errorf("%s carried %v", op, got)
		}
	}
	got := carriedNames(prev, mk("flip-byte"), 1, r)
	if len(got) != 2 || got["aa"] != "prior:file:inv.json" || got["bb"] != "prior:json:inv.json#" {
		t.Errorf("flip-byte carried %v", got)
	}
	if got := carriedNames(prev, mk("flip-byte"), 2, r); len(got) != 0 {
		t.Errorf("wrong invocation carried %v", got)
	}
}
