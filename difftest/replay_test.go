package difftest

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// portedTypes gates golden replay per bundle type; verifier tasks add entries.
var portedTypes = map[string]bool{"demo": true, "prevention": true, "git": true}

// flBinary is the fl command built for this test run; replay executes it so
// that anything written outside cli.Run's writers, and the exit status of the
// real process, are compared too.
var flBinary string

func TestMain(m *testing.M) {
	home, _ := os.MkdirTemp("", "faultline-difftest-home-")
	empty := filepath.Join(home, "empty.gitconfig")
	os.WriteFile(empty, nil, 0o600)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", empty)
	// The verifier's temporary `git init --bare` follows GIT_DEFAULT_HASH;
	// goldens are generated with it pinned (base-env.json overrides per base).
	os.Setenv("GIT_DEFAULT_HASH", "sha1")
	flBinary = filepath.Join(home, "fl")
	if runtime.GOOS == "windows" {
		flBinary += ".exe"
	}
	build := exec.Command("go", "build", "-o", flBinary, "github.com/Mizore66/faultline/cmd/fl")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building fl:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// runFl runs the built fl binary like gen-goldens.ts runs dist/cli.js.
func runFl(t *testing.T, repo string, env map[string]string, args ...string) (string, string, int) {
	cmd := exec.Command(flBinary, args...)
	cmd.Dir = repo
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running fl: %v", err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func loadBaseEnv(t *testing.T, repo string) map[string]map[string]string {
	text, err := os.ReadFile(filepath.Join(repo, "difftest", "testdata", "base-env.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(text))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]string{}
	for _, base := range v.Obj().Keys() {
		out[base] = map[string]string{}
		for _, k := range v.Get(base).Obj().Keys() {
			out[base][k] = v.Get(base, k).Str()
		}
	}
	return out
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
	baseEnv := loadBaseEnv(t, repo)
	goldenDir := filepath.Join(repo, "difftest", "testdata", "golden")
	basesDir := filepath.Join(repo, "difftest", "testdata", "bases")
	var bases, goldens []string
	baseEntries, err := os.ReadDir(basesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range baseEntries {
		if e.IsDir() {
			bases = append(bases, e.Name())
		}
	}
	goldenEntries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range goldenEntries {
		goldens = append(goldens, strings.TrimSuffix(e.Name(), ".jsonl"))
	}
	// gen-goldens.ts writes one golden per base directory; a base without a
	// golden (or the reverse) would otherwise never be compared.
	slices.Sort(goldens)
	if !slices.Equal(bases, goldens) {
		t.Fatalf("bases %v and goldens %v differ: run gen-goldens.ts", bases, goldens)
	}
	for _, base := range bases {
		kind, _, _ := strings.Cut(base, "-")
		if !portedTypes[kind] {
			t.Fatalf("base %s has no ported verifier type", base)
		}
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(basesDir, base)
			golden := map[string]*jsjson.Obj{}
			var order []string
			f, err := os.Open(filepath.Join(goldenDir, base+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
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
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
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
						if runtime.GOOS != "windows" {
							t.Fatal("could not apply the mutation")
						}
						t.Skip("platform cannot create this case (symlink)")
					}
					digest := treeDigest(bundle)
					invocations := [][2]string{{"plain", ""}}
					if c.tpl == nil {
						invocations = append(invocations, [2]string{"root-ok", rootDigest}, [2]string{"root-bad", "sha256:" + strings.Repeat("0", 64)},
							[2]string{"root-malformed", "not-a-digest"})
					}
					for _, inv := range invocations {
						want := golden[c.id+"|"+inv[0]]
						if want == nil {
							t.Fatalf("no golden for %s %s", c.id, inv[0])
						}
						if digest != want.Field("treeDigest").Str() {
							t.Fatalf("tree digest mismatch: the Go (difftest/cases.go) and TS (difftest/gen/cases.ts) mutation engines built different trees. " +
								"Rehashing uses internal/canonical and internal/jsjson, so a bug there shows up here too.")
						}
						args := []string{"verify", bundle}
						if inv[1] != "" {
							args = append(args, "--expect-root", inv[1])
						}
						stdout, stderr, exit := runFl(t, repo, baseEnv[base], args...)
						gotOut, slashOut := normalizeChecked(stdout, bundle)
						gotErr, slashErr := normalizeChecked(stderr, bundle)
						if slashOut || slashErr {
							t.Errorf("%s: a path under the bundle uses '/' on Windows, where Node's path.win32 prints '\\':\n%s%s", inv[0], stdout, stderr)
						}
						if gotOut != want.Field("stdout").Str() || gotErr != want.Field("stderr").Str() || float64(exit) != want.Field("exit").Num() {
							t.Fatalf("%s:\n--- Go (exit %d)\n%s%s\n--- TS (exit %v)\n%s%s", inv[0], exit, gotOut, gotErr, want.Field("exit").Num(), want.Field("stdout").Str(), want.Field("stderr").Str())
						}
					}
				})
			}
		})
	}
}
