package difftest

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/cli"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// portedTypes gates golden replay per bundle type; verifier tasks add entries.
var portedTypes = map[string]bool{"demo": true}

func TestMain(m *testing.M) {
	home, _ := os.MkdirTemp("", "faultline-difftest-home-")
	empty := filepath.Join(home, "empty.gitconfig")
	os.WriteFile(empty, nil, 0o600)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", empty)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func loadTemplates(t *testing.T, path string) []template {
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	var out []template
	for _, item := range v.Items() {
		out = append(out, template{item.Obj()})
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoldenReplay(t *testing.T) {
	repo := oracle.RepoRoot(t)
	templates := loadTemplates(t, filepath.Join(repo, "difftest", "testdata", "mutations.json"))
	goldenDir := filepath.Join(repo, "difftest", "testdata", "golden")
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		base := strings.TrimSuffix(entry.Name(), ".jsonl")
		kind, _, _ := strings.Cut(base, "-")
		if !portedTypes[kind] {
			continue
		}
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(repo, "difftest", "testdata", "bases", base)
			golden := map[string]*jsjson.Obj{}
			var order []string
			f, _ := os.Open(filepath.Join(goldenDir, entry.Name()))
			defer f.Close()
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 1<<20), 1<<26)
			for scanner.Scan() {
				v, err := jsjson.Parse(scanner.Text())
				if err != nil {
					t.Fatal(err)
				}
				key := v.Obj().Field("case").Str() + "|" + v.Obj().Field("inv").Str()
				golden[key] = v.Obj()
				if v.Obj().Field("inv").Str() == "plain" {
					order = append(order, v.Obj().Field("case").Str())
				}
			}
			cases := expandCases(base, root, templates)
			if len(cases) != len(order) {
				t.Fatalf("Go expands %d cases, goldens have %d", len(cases), len(order))
			}
			rootDigest := baseRoot(root, base)
			for i, c := range cases {
				if c.id != order[i] {
					t.Fatalf("case %d: Go %q, golden %q", i, c.id, order[i])
				}
				t.Run(c.id, func(t *testing.T) {
					t.Parallel()
					work := t.TempDir()
					bundle := filepath.Join(work, "bundle")
					copyTree(t, root, bundle)
					if !applyMutation(bundle, c) {
						t.Skip("platform cannot create this case (symlink)")
					}
					digest := treeDigest(bundle)
					invocations := [][2]string{{"plain", ""}}
					if c.tpl == nil {
						invocations = append(invocations, [2]string{"root-ok", rootDigest}, [2]string{"root-bad", "sha256:" + strings.Repeat("0", 64)})
					}
					for _, inv := range invocations {
						want := golden[c.id+"|"+inv[0]]
						if want == nil {
							t.Fatalf("no golden for %s %s", c.id, inv[0])
						}
						if digest != want.Field("treeDigest").Str() {
							t.Fatalf("tree digest mismatch: the Go and TS mutation engines built different trees")
						}
						args := []string{"verify", bundle}
						if inv[1] != "" {
							args = append(args, "--expect-root", inv[1])
						}
						var stdout, stderr bytes.Buffer
						exit := cli.Run(args, &stdout, &stderr)
						gotOut, gotErr := normalize(stdout.String(), bundle), normalize(stderr.String(), bundle)
						if gotOut != want.Field("stdout").Str() || gotErr != want.Field("stderr").Str() || float64(exit) != want.Field("exit").Num() {
							t.Fatalf("%s:\n--- Go (exit %d)\n%s%s\n--- TS (exit %v)\n%s%s", inv[0], exit, gotOut, gotErr, want.Field("exit").Num(), want.Field("stdout").Str(), want.Field("stderr").Str())
						}
					}
				})
			}
		})
	}
}
