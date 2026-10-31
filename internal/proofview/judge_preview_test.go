package proofview

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/demofixture"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
)

const (
	judgePreviewDir   = "../../difftest/testdata/demo-artifacts/"
	judgePreviewNotRe = "Judge preview only renders the deterministic REPLAY fixture."
)

type previewGoldenEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Base64 string `json:"base64"`
	Target string `json:"target"`
}

type previewGoldenCase struct {
	Request struct {
		ID string `json:"id"`
	} `json:"request"`
	Call *struct {
		ValueJSON *string `json:"valueJSON"`
		Error     *string `json:"error"`
	} `json:"call"`
}

// previewGoldenRender returns the pinned-TS rendered page captured for a
// render case, decoded from the capture's JSON string.
func previewGoldenRender(t *testing.T, id string) string {
	t.Helper()
	file, err := os.Open(judgePreviewDir + "golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		var c previewGoldenCase
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if c.Request.ID != id {
			continue
		}
		var page string
		if c.Call == nil || c.Call.ValueJSON == nil {
			t.Fatalf("%s has no rendered value", id)
		}
		if err := json.Unmarshal([]byte(*c.Call.ValueJSON), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("golden case %s missing", id)
	return ""
}

func previewGoldenHTML(t *testing.T) []byte {
	t.Helper()
	want, err := os.ReadFile(judgePreviewDir + "judge-preview.html")
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func TestJudgePreviewPageExact(t *testing.T) {
	want := previewGoldenHTML(t)
	got, err := RenderJudgePreviewPage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(jsstr.ToUTF8(got)), want) {
		t.Fatal("complete preview HTML differs")
	}
	a := demofixture.CreateReplayAnalysis()
	explicit, err := RenderJudgePreviewPage(&a)
	if err != nil || explicit != got {
		t.Fatal("explicit REPLAY differs", err)
	}
	a.Obj().Set("mode", jsjson.MakeString("RERUN"))
	_, err = RenderJudgePreviewPage(&a)
	if err == nil || err.Error() != judgePreviewNotRe {
		t.Fatal(err)
	}
}

func TestJudgePreviewRenderVariants(t *testing.T) {
	t.Run("no-boundary", func(t *testing.T) {
		a := demofixture.CreateReplayAnalysis()
		a.Obj().Set("transitions", jsjson.MakeArray(nil))
		got, err := RenderJudgePreviewPage(&a)
		if err != nil {
			t.Fatal(err)
		}
		if want := previewGoldenRender(t, "render-no-boundary"); got != want {
			t.Fatal("no-boundary page differs from pinned-TS capture")
		}
		if !strings.Contains(got, "<p><strong>No sample pass-to-fail boundary was recorded.</strong></p>") {
			t.Fatal("fallback boundary copy missing")
		}
	})
	t.Run("escape-fallback", func(t *testing.T) {
		a := demofixture.CreateReplayAnalysis()
		a.Get("witness").Obj().Set("behavior", jsjson.MakeString("<>&\"'"))
		a.Get("claims").Items()[0].Obj().Set("statement", jsjson.MakeString("<>&\"'"))
		a.Get("prevention", "lastGood").Obj().Set("stateId", jsjson.MakeString("<unknown-state>"))
		got, err := RenderJudgePreviewPage(&a)
		if err != nil {
			t.Fatal(err)
		}
		if want := previewGoldenRender(t, "render-escape-fallback"); got != want {
			t.Fatal("escape-fallback page differs from pinned-TS capture")
		}
		for _, frag := range []string{
			"<strong>&lt;&gt;&amp;&quot;&#039;</strong>",
			"<tr><td>&lt;unknown-state&gt;</td>",
			"<p>&lt;&gt;&amp;&quot;&#039;</p>",
		} {
			if !strings.Contains(got, frag) {
				t.Fatalf("missing %q", frag)
			}
		}
	})
	t.Run("unknown-ids-first-boundary-and-badges", func(t *testing.T) {
		a := demofixture.CreateReplayAnalysis()
		boundary := func(kind, before, after string) jsjson.Value {
			return jsjson.MustObject("kind", jsjson.MakeString(kind),
				"beforeStateId", jsjson.MakeString(before), "afterStateId", jsjson.MakeString(after),
				"stable", jsjson.MakeBool(true))
		}
		a.Obj().Set("transitions", jsjson.MakeArray([]jsjson.Value{
			boundary("FAIL_TO_PASS", "ignored-a", "ignored-b"),
			boundary("PASS_TO_FAIL", "mystery-'before'", "<mystery-after>"),
			boundary("PASS_TO_FAIL", "second-before", "second-after"),
		}))
		for name, verdict := range map[string]string{"lastGood": "UNSTABLE", "firstBad": "ERROR", "repaired": "INAPPLICABLE"} {
			a.Get("prevention", name).Obj().Set("verdict", jsjson.MakeString(verdict))
		}
		got, err := RenderJudgePreviewPage(&a)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{
			"<p><strong>mystery-&#039;before&#039; to &lt;mystery-after&gt;</strong></p>",
			`<span class="verdict unstable">UNSTABLE</span>`,
			`<span class="verdict error">ERROR</span>`,
			`<span class="verdict inapplicable">INAPPLICABLE</span>`,
		} {
			if !strings.Contains(got, frag) {
				t.Fatalf("missing %q", frag)
			}
		}
		if strings.Contains(got, "second-before") || strings.Contains(got, "ignored-a") {
			t.Fatal("boundary is not the first PASS_TO_FAIL transition")
		}
		// The default fixture carries the PASS and FAIL badges.
		def, _ := RenderJudgePreviewPage(nil)
		for _, frag := range []string{`<span class="verdict pass">PASS</span>`, `<span class="verdict fail">FAIL</span>`} {
			if !strings.Contains(def, frag) {
				t.Fatalf("default page missing %q", frag)
			}
		}
	})
	t.Run("guardrails", func(t *testing.T) {
		got, _ := RenderJudgePreviewPage(nil)
		if strings.Contains(got, "<script") {
			t.Fatal("static preview must not contain a script")
		}
		if strings.Contains(got, "/api/") {
			t.Fatal("static preview must not reference an API")
		}
		if !strings.Contains(got, "NOT A LIVE DOCKER PROOF.") || !strings.Contains(got, "pnpm fl judge-proof") {
			t.Fatal("historical wording missing")
		}
	})
}

// ---- file-writer policy ----

func previewRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func previewInventory(t *testing.T, root string) []previewGoldenEntry {
	t.Helper()
	var out []previewGoldenEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		e := previewGoldenEntry{Path: filepath.ToSlash(rel)}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			e.Target, err = os.Readlink(path)
		case info.IsDir():
			e.Kind = "directory"
		default:
			e.Kind = "file"
			var b []byte
			b, err = os.ReadFile(path)
			e.Base64 = base64.StdEncoding.EncodeToString(b)
		}
		out = append(out, e)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestJudgePreviewOutputPolicy(t *testing.T) {
	want := previewGoldenHTML(t)
	const nonHTML = "Judge preview output must be an .html file."

	success := func(t *testing.T, root, output string, wantPath string) {
		t.Helper()
		got, err := WriteJudgePreview(output)
		if err != nil {
			t.Fatal(err)
		}
		if got.Path != wantPath || got.Bytes != len(want) {
			t.Fatalf("result %+v, want path %s bytes %d", got, wantPath, len(want))
		}
		onDisk, err := os.ReadFile(wantPath)
		if err != nil || !bytes.Equal(onDisk, want) {
			t.Fatalf("file bytes differ: %v", err)
		}
	}

	t.Run("default", func(t *testing.T) {
		root := previewRoot(t)
		if got, wantPath := DefaultJudgePreviewPath(), filepath.Join(root, "docs", "judge-preview.html"); got != wantPath {
			t.Fatalf("default %q, want %q", got, wantPath)
		}
		success(t, root, DefaultJudgePreviewPath(), filepath.Join(root, "docs", "judge-preview.html"))
	})
	t.Run("nested-relative-absolute", func(t *testing.T) {
		root := previewRoot(t)
		success(t, root, "a/b/c/out.html", filepath.Join(root, "a", "b", "c", "out.html"))
		success(t, root, filepath.Join(root, "abs", "x.html"), filepath.Join(root, "abs", "x.html"))
	})
	for _, name := range []string{"out.HTML", ".hidden.HTML", "..html", "Mixed.HtMl"} {
		t.Run("accepted-"+name, func(t *testing.T) {
			root := previewRoot(t)
			success(t, root, name, filepath.Join(root, name))
		})
	}
	for _, c := range []struct{ name, output string }{
		{"txt", "newdir/out.txt"},
		{"dot-html-only", "newdir/.html"},
		{"no-extension", "newdir/out"},
		{"trailing-dot", "newdir/out."},
		{"empty", ""},
	} {
		t.Run("rejected-"+c.name, func(t *testing.T) {
			root := previewRoot(t)
			_, err := WriteJudgePreview(c.output)
			if err == nil || err.Error() != nonHTML {
				t.Fatalf("err = %v", err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, "newdir")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("parent was created before extension rejection: %v", statErr)
			}
		})
	}
	t.Run("parent-is-file", func(t *testing.T) {
		root := previewRoot(t)
		if err := os.WriteFile("blocked", []byte("blocker\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := WriteJudgePreview("blocked/out.html")
		if want := "Judge preview output cannot traverse a symbolic link or non-directory: " + filepath.Join(root, "blocked"); err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if b, _ := os.ReadFile("blocked"); string(b) != "blocker\n" {
			t.Fatal("blocker modified")
		}
	})
	t.Run("output-is-directory", func(t *testing.T) {
		root := previewRoot(t)
		if err := os.MkdirAll("out.html", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("out.html/sentinel.txt", []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := WriteJudgePreview("out.html")
		if want := "Judge preview output must be a regular non-symlink file: " + filepath.Join(root, "out.html"); err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if b, _ := os.ReadFile("out.html/sentinel.txt"); string(b) != "keep\n" {
			t.Fatal("sentinel modified")
		}
	})
	t.Run("overwrite-in-place-truncates", func(t *testing.T) {
		root := previewRoot(t)
		out := filepath.Join(root, "out.html")
		if err := os.WriteFile(out, bytes.Repeat([]byte("old preview\n"), 5000), 0o644); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		success(t, root, "out.html", out)
		after, err := os.Stat(out)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("overwrite replaced the file instead of writing in place: %v", err)
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 1 {
			t.Fatalf("stage/backup files left behind: %v", entries)
		}
	})
}

func TestJudgePreviewSymlinks(t *testing.T) {
	link := func(t *testing.T, target, name string) {
		t.Helper()
		if err := os.Symlink(target, name); err != nil {
			t.Skipf("fixture-local symlink creation unavailable on this host: %v", err)
		}
	}
	const regular = "Judge preview output must be a regular non-symlink file: "

	t.Run("linked-parent", func(t *testing.T) {
		root := previewRoot(t)
		if err := os.Mkdir("actual-parent", 0o755); err != nil {
			t.Fatal(err)
		}
		link(t, "actual-parent", "linked")
		before := previewInventory(t, root)
		_, err := WriteJudgePreview("linked/out.html")
		if want := "Judge preview output cannot traverse a symbolic link or non-directory: " + filepath.Join(root, "linked"); err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
		if after := previewInventory(t, root); !equalInventory(before, after) {
			t.Fatalf("tree changed: %v -> %v", before, after)
		}
	})
	t.Run("linked-output", func(t *testing.T) {
		root := previewRoot(t)
		if err := os.WriteFile("target.html", []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("sentinel.txt", []byte("unrelated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link(t, "target.html", "out.html")
		before := previewInventory(t, root)
		_, err := WriteJudgePreview("out.html")
		if err == nil || err.Error() != regular+filepath.Join(root, "out.html") {
			t.Fatalf("err = %v", err)
		}
		if after := previewInventory(t, root); !equalInventory(before, after) {
			t.Fatalf("tree changed: %v -> %v", before, after)
		}
	})
	t.Run("dangling-output", func(t *testing.T) {
		root := previewRoot(t)
		link(t, "target.html", "out.html")
		before := previewInventory(t, root)
		_, err := WriteJudgePreview("out.html")
		if err == nil || err.Error() != regular+filepath.Join(root, "out.html") {
			t.Fatalf("err = %v", err)
		}
		if _, statErr := os.Lstat("target.html"); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("dangling target was created: %v", statErr)
		}
		if after := previewInventory(t, root); !equalInventory(before, after) {
			t.Fatalf("tree changed: %v -> %v", before, after)
		}
	})
}

func equalInventory(a, b []previewGoldenEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
