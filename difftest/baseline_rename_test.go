//go:build faultline_test && unix

package difftest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// A repository (or a directory holding one) that a mutation renames keeps its
// beginning-of-case baseline: the objects and reflogs it held before the case
// stay "existed before" (never named, never aliased, raw bytes compared) under
// the new path; an object the case creates there is still named; a lookalike
// prefix, an unrelated repository and a rename that did not happen leave the
// baseline alone; renames chain in the order they succeed. The test drives the
// real mutation helper (applyDueMutations) and the real relocation.
func TestBaselineFollowsRenamedRepository(t *testing.T) {
	r := makeWriterRoots(t)
	mk := func(rel string) string {
		dir := filepath.Join(r.ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "", "init", "-q")
		blob := gitIn(t, dir, "pre "+rel+"\n", "hash-object", "-w", "--stdin")
		tree := gitIn(t, dir, "100644 blob "+blob+"\tf\n", "mktree")
		commit := gitIn(t, dir, "", "commit-tree", tree, "-m", "pre")
		gitIn(t, dir, "", "update-ref", "-m", "pre", "HEAD", commit)
		return blob
	}
	preRepo, preLike, preInner := mk("repo"), mk("repo-x"), mk("outer/inner")
	base := snapshotGit(t, r.ws)
	if len(base.objects["repo"]) == 0 || len(base.logs) == 0 {
		t.Fatalf("baseline %v %v", base.objects, base.logs)
	}
	mut := func(from, to string) template {
		v, err := jsjson.Parse(`{"op":"rename","file":"` + from + `","to":"` + to + `","inv":1}`)
		if err != nil {
			t.Fatal(err)
		}
		return template{v.Obj()}
	}
	// repo -> repo2 -> repo3 (chained), outer -> outer2 (ancestor), and a
	// rename of a path that does not exist (must move nothing).
	tpl := writerTemplate{ID: "baseline-rename", Mutations: []template{mut("repo", "repo2"), mut("repo2", "repo3"), mut("outer", "outer2"), mut("nowhere", "repo-x2")}}
	applyDueMutations(t, tpl, r, 1, base)
	if _, err := os.Lstat(filepath.Join(r.ws, "repo3", ".git")); err != nil {
		t.Fatal("the chained rename was not applied")
	}
	// A genuinely new object in the moved repository.
	created := gitIn(t, filepath.Join(r.ws, "repo3"), "new in the case\n", "hash-object", "-w", "--stdin")

	rel := relocation(t, r, nil, base)
	for _, id := range []string{preRepo, preInner} {
		for dir, objs := range rel.created {
			if _, ok := objs[id]; ok {
				t.Errorf("pre-existing %s is classified created in %s", id, dir)
			}
		}
		if n, ok := rel.ids[id]; ok {
			t.Errorf("pre-existing %s is named %s", id, n)
		}
	}
	if _, ok := rel.created["repo3"][created]; !ok || rel.ids[created] == "" {
		t.Errorf("the object the case created in the moved repository is not named: %v", rel.ids)
	}
	if _, ok := rel.created["repo-x"][preLike]; ok || rel.ids[preLike] != "" {
		t.Error("a lookalike prefix repository must keep its own baseline")
	}
	loose := "repo3/.git/objects/" + preRepo[:2] + "/" + preRepo[2:]
	if got, ok := rel.alias(loose); !ok || got != loose {
		t.Errorf("a pre-existing loose object is aliased: %q %v", got, ok)
	}
	b, err := os.ReadFile(filepath.Join(r.ws, filepath.FromSlash(loose)))
	if err != nil {
		t.Fatal(err)
	}
	if got := rel.fileDigest(loose, b); got != canonical.SHA256HexBytes(b) {
		t.Error("a pre-existing loose object must keep its byte digest")
	}
	// The reflog baseline followed the move: an unchanged reflog keeps its bytes digest.
	logPath := "repo3/.git/logs/HEAD"
	lb, err := os.ReadFile(filepath.Join(r.ws, filepath.FromSlash(logPath)))
	if err != nil {
		t.Fatal(err)
	}
	if got := rel.fileDigest(logPath, lb); got != canonical.SHA256HexBytes(lb) {
		t.Error("an unchanged pre-existing reflog was treated as new after the move")
	}
	if _, ok := base.objects["outer2/inner"]; !ok {
		t.Errorf("the ancestor move did not carry the nested repository: %v", base.objects)
	}
	if _, ok := base.objects["repo-x"]; !ok || len(base.objects) != 3 {
		t.Errorf("the lookalike or a failed rename disturbed the baseline: %v", base.objects)
	}
}
