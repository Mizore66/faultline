package cli

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const previewGoldenPath = "../../difftest/testdata/demo-artifacts/golden.jsonl"

type previewCLIEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Base64 string `json:"base64"`
	Target string `json:"target"`
}

type previewCLICase struct {
	Request struct {
		ID     string   `json:"id"`
		Kind   string   `json:"kind"`
		Output string   `json:"output"`
		Argv   []string `json:"argv"`
	} `json:"request"`
	Process *struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	} `json:"process"`
	Before []previewCLIEntry `json:"before"`
	After  []previewCLIEntry `json:"after"`
}

var previewLinkCases = map[string]bool{"cli-linked-parent": true, "cli-linked-output": true, "cli-dangling-output": true}

func loadPreviewCLICases(t *testing.T) []previewCLICase {
	t.Helper()
	file, err := os.Open(previewGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	var cases []previewCLICase
	for scanner.Scan() {
		var c previewCLICase
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if c.Request.Kind == "cli" {
			cases = append(cases, c)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// previewStream substitutes the capture's <TMP> placeholder with the real
// case root, turning the slash-separated remainder of that path into the
// platform's separators.
func previewStream(s, root string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, "<TMP>")
		if i < 0 {
			return out.String() + s
		}
		out.WriteString(s[:i] + root)
		s = s[i+len("<TMP>"):]
		end := strings.IndexByte(s, '\n')
		if end < 0 {
			end = len(s)
		}
		out.WriteString(filepath.FromSlash(s[:end]))
		s = s[end:]
	}
}

func previewMaterialize(t *testing.T, root string, entries []previewCLIEntry) {
	t.Helper()
	for _, e := range entries {
		path := filepath.Join(root, filepath.FromSlash(e.Path))
		switch e.Kind {
		case "directory":
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		case "file":
			body, err := base64.StdEncoding.DecodeString(e.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
		case "symlink":
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.FromSlash(e.Target), path); err != nil {
				t.Skipf("fixture-local symlink creation unavailable on this host: %v", err)
			}
		}
	}
}

func previewTree(t *testing.T, root string) []previewCLIEntry {
	t.Helper()
	var out []previewCLIEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		e := previewCLIEntry{Path: filepath.ToSlash(rel)}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			e.Target = filepath.ToSlash(target)
		case info.IsDir():
			e.Kind = "directory"
		default:
			e.Kind = "file"
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e.Base64 = base64.StdEncoding.EncodeToString(b)
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func samePreviewTree(a, b []previewCLIEntry) bool {
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

func runPreviewCase(t *testing.T, c previewCLICase) (root, stdout, stderr string, code int) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	previewMaterialize(t, root, c.Before)
	argv := []string{"judge-preview"}
	for _, a := range c.Request.Argv {
		if c.Request.ID == "cli-absolute" && a == c.Request.Output {
			a = filepath.Join(root, filepath.FromSlash(a))
		}
		argv = append(argv, a)
	}
	var out, errb bytes.Buffer
	code = Run(argv, &out, &errb)
	return root, out.String(), errb.String(), code
}

func TestJudgePreviewCLI(t *testing.T) {
	n := 0
	for _, c := range loadPreviewCLICases(t) {
		if previewLinkCases[c.Request.ID] {
			continue
		}
		n++
		t.Run(c.Request.ID, func(t *testing.T) {
			root, stdout, stderr, code := runPreviewCase(t, c)
			wantOut, wantErr := previewStream(c.Process.Stdout, root), previewStream(c.Process.Stderr, root)
			if stdout != wantOut || stderr != wantErr || code != c.Process.ExitCode {
				t.Fatalf("argv=%q\nstdout=%q want=%q\nstderr=%q want=%q\nexit=%d want=%d",
					c.Request.Argv, stdout, wantOut, stderr, wantErr, code, c.Process.ExitCode)
			}
			if c.Process.ExitCode != 0 && stdout != "" {
				t.Fatalf("failure wrote stdout: %q", stdout)
			}
			if after := previewTree(t, root); !samePreviewTree(after, c.After) {
				t.Fatalf("tree differs\ngot=%v\nwant=%v", after, c.After)
			}
		})
	}
	if n != 17 {
		t.Fatalf("non-link CLI cases = %d, want 17", n)
	}
}

func TestJudgePreviewCLISymlinks(t *testing.T) {
	seen := 0
	for _, c := range loadPreviewCLICases(t) {
		if !previewLinkCases[c.Request.ID] {
			continue
		}
		seen++
		t.Run(c.Request.ID, func(t *testing.T) {
			root, stdout, stderr, code := runPreviewCase(t, c)
			if stdout != "" || code != 1 {
				t.Fatalf("stdout=%q exit=%d", stdout, code)
			}
			if c.Request.ID == "cli-dangling-output" {
				// Raw pinned TS follows the dangling link, creates target.html and
				// exits 0; the approved Go policy refuses. Both facts are asserted
				// separately and exactly, never merged.
				if c.Process.ExitCode != 0 || !strings.HasPrefix(c.Process.Stdout, "FaultLine deterministic static judge preview written.\n") {
					t.Fatalf("raw TS capture changed: %+v", c.Process)
				}
				wantErr := "FaultLine error: Judge preview output must be a regular non-symlink file: " + filepath.Join(root, "out.html") + "\nNext: pnpm fl help\n"
				if stderr != wantErr {
					t.Fatalf("stderr=%q want=%q", stderr, wantErr)
				}
				if _, err := os.Lstat(filepath.Join(root, "target.html")); !os.IsNotExist(err) {
					t.Fatalf("dangling target was created: %v", err)
				}
				want := []previewCLIEntry{{Path: "out.html", Kind: "symlink", Target: "target.html"}}
				if after := previewTree(t, root); !samePreviewTree(after, want) {
					t.Fatalf("tree changed: %v", after)
				}
				return
			}
			if wantErr := previewStream(c.Process.Stderr, root); stderr != wantErr || code != c.Process.ExitCode {
				t.Fatalf("stderr=%q want=%q exit=%d want=%d", stderr, wantErr, code, c.Process.ExitCode)
			}
			if after := previewTree(t, root); !samePreviewTree(after, c.After) {
				t.Fatalf("tree differs\ngot=%v\nwant=%v", after, c.After)
			}
		})
	}
	if seen != 3 {
		t.Fatalf("link CLI cases = %d, want 3", seen)
	}
}

func TestJudgePreviewDispatchIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"judge-demo"}, "FaultLine error: not yet ported: judge-demo\nNext: pnpm fl help\n"},
		{[]string{"judge-demo", "--export-only"}, "FaultLine error: not yet ported: judge-demo\nNext: pnpm fl help\n"},
		{[]string{"serve"}, "FaultLine error: not yet ported: serve\nNext: pnpm fl help\n"},
		{[]string{"judge-previews"}, "FaultLine error: Unknown command: judge-previews\nNext: pnpm fl help\n"},
	} {
		var out, errb bytes.Buffer
		code := Run(c.args, &out, &errb)
		if out.Len() != 0 || errb.String() != c.want || code != 1 {
			t.Errorf("%q: stdout=%q stderr=%q exit=%d", c.args, out.String(), errb.String(), code)
		}
	}
}
