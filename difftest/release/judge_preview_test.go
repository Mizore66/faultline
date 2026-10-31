package release

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// judgePreviewInventoryError requires the tree under root to hold exactly the
// expected preview file and its ancestor directories: no stage, backup or
// other entry of any extension. Walk errors are returned, not ignored.
func judgePreviewInventoryError(root, wantFile string) error {
	allowed := map[string]bool{}
	for p := wantFile; p != root && p != filepath.Dir(p); p = filepath.Dir(p) {
		allowed[p] = true
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && !allowed[path] {
			return fmt.Errorf("unexpected entry left behind: %s", path)
		}
		return nil
	})
}

func TestJudgePreviewStandalone(t *testing.T) {
	bin := buildCLIHelpRelease(t, "")
	want, err := os.ReadFile(filepath.Join(repoRoot(t), "difftest", "testdata", "demo-artifacts", "judge-preview.html"))
	if err != nil {
		t.Fatal(err)
	}
	env := cliHelpEmptyPathEnv(t)
	success := func(t *testing.T, cwd, wantPath string, args ...string) {
		t.Helper()
		stdout, stderr, code := runCLIHelpRelease(t, bin, cwd, env, args...)
		wantOut := "FaultLine deterministic static judge preview written.\n" +
			"Preview: " + wantPath + "\n" +
			"Bytes: " + strconv.Itoa(len(want)) + "\n" +
			"Limitation: this read-only replay snapshot is not a live Docker proof, a verified proof bundle, or a record of a fresh execution.\n"
		if stdout != wantOut || stderr != "" || code != 0 {
			t.Fatalf("args=%q\nstdout=%q want=%q\nstderr=%q\nexit=%d", args, stdout, wantOut, stderr, code)
		}
		got, err := os.ReadFile(wantPath)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s bytes differ: %v", wantPath, err)
		}
		if err := judgePreviewInventoryError(cwd, wantPath); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(cwd, ".faultline")); !os.IsNotExist(err) {
			t.Fatalf("judge-preview created a .faultline bundle directory: %v", err)
		}
	}
	fresh := func(t *testing.T) string {
		t.Helper()
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("inventory-rejects-stage-and-backup-files", func(t *testing.T) {
		cwd := fresh(t)
		out := filepath.Join(cwd, "docs", "judge-preview.html")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, want, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := judgePreviewInventoryError(cwd, out); err != nil {
			t.Fatalf("exact inventory rejected: %v", err)
		}
		for _, stray := range []string{"docs/judge-preview.html.bak", "docs/.judge-preview.html.tmp", "docs/stage.html", "stage.html"} {
			path := filepath.Join(cwd, filepath.FromSlash(stray))
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if judgePreviewInventoryError(cwd, out) == nil {
				t.Errorf("unwanted %s was accepted", stray)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if judgePreviewInventoryError(filepath.Join(cwd, "missing"), out) == nil {
			t.Error("walk error was swallowed")
		}
	})
	t.Run("default-and-overwrite", func(t *testing.T) {
		cwd := fresh(t)
		out := filepath.Join(cwd, "docs", "judge-preview.html")
		success(t, cwd, out, "judge-preview")
		if err := os.WriteFile(out, bytes.Repeat([]byte("stale"), 10000), 0o644); err != nil {
			t.Fatal(err)
		}
		success(t, cwd, out, "judge-preview")
	})
	t.Run("custom-nested-and-overwrite", func(t *testing.T) {
		cwd := fresh(t)
		out := filepath.Join(cwd, "out", "nested", "page.html")
		success(t, cwd, out, "judge-preview", "--output", filepath.Join("out", "nested", "page.html"))
		if err := os.WriteFile(out, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		success(t, cwd, out, "judge-preview", "--output", filepath.Join("out", "nested", "page.html"))
	})
	t.Run("ignored-equals-option", func(t *testing.T) {
		cwd := fresh(t)
		success(t, cwd, filepath.Join(cwd, "docs", "judge-preview.html"), "judge-preview", "--output=unused.html")
		if _, err := os.Lstat(filepath.Join(cwd, "unused.html")); !os.IsNotExist(err) {
			t.Fatalf("--output=value must be ignored: %v", err)
		}
	})
	t.Run("trailing-output", func(t *testing.T) {
		cwd := fresh(t)
		success(t, cwd, filepath.Join(cwd, "docs", "judge-preview.html"), "judge-preview", "--output")
	})
}
