//go:build unix

package difftest

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/canonical"
)

// sameDirectoryCases are writer templates whose bundles embed the
// workspace path; TestWriterSameDirectory compares them without relocation.
var sameDirectoryCases = []string{
	"investigate-two-states", "investigate-heterogeneous-mapped", "investigate-ledger-full",
	"investigate-cjk-overlays", "investigate-many-states", "minimize-git",
}

// TestWriterSameDirectory runs frozen TS and then Go in the same case
// directory (the workspace rebuilt between them, same seed) and compares
// stdout, stderr, exit, the recorded argv and every workspace file byte
// for byte, the written bundles included: no path masking, no digest
// relocation. It checks what the replay's <PATHDIGEST:…> names hide.
func TestWriterSameDirectory(t *testing.T) {
	oracle.Skip(t)
	setWriterUmask(t)
	repo := repoRoot(t)
	templates := map[string]writerTemplate{}
	for _, tpl := range loadWriterTemplates(t, repo) {
		templates[tpl.ID] = tpl
	}
	raw := func(_ string, b []byte) string { return canonical.SHA256HexBytes(b) }
	for _, id := range sameDirectoryCases {
		tpl, ok := templates[id]
		if !ok {
			t.Fatalf("no writer template %s", id)
		}
		t.Run(id, func(t *testing.T) {
			r := makeWriterRoots(t)
			type outcome struct {
				stdout, stderr string
				exit           int
				argv           []string
				tree           map[string]string
			}
			run := func(command []string) []outcome {
				for _, dir := range []string{r.ws, r.tmp} {
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				prepareWriterCase(t, repo, tpl, r)
				var out []outcome
				for _, argv := range tpl.Argv {
					os.Remove(r.argvLog)
					args := slices.Clone(command)
					for _, a := range argv {
						args = append(args, substituteRoots(a, r))
					}
					cmd := exec.Command(args[0], args[1:]...)
					cmd.Dir = r.ws
					cmd.Env = writerEnv(repo, r, tpl.ID, tpl)
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					var exitErr *exec.ExitError
					if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
						t.Fatal(err)
					}
					out = append(out, outcome{stdout.String(), stderr.String(), cmd.ProcessState.ExitCode(), readArgvLog(r.argvLog), treeEntries(t, r.ws, raw)})
				}
				return out
			}
			seed := "file://" + filepath.Join(repo, "difftest", "gen", "seed.mjs")
			ts := run([]string{"node", "--import", seed, filepath.Join(repo, "dist", "cli.js")})
			goOut := run([]string{flBinary})
			for i := range ts {
				want, got := ts[i], goOut[i]
				if got.stdout != want.stdout || got.stderr != want.stderr || got.exit != want.exit {
					t.Fatalf("inv %d:\n--- Go (exit %d)\n%s%s\n--- TS (exit %d)\n%s%s", i, got.exit, got.stdout, got.stderr, want.exit, want.stdout, want.stderr)
				}
				if !slices.Equal(got.argv, want.argv) {
					t.Fatalf("inv %d: argv differ\n--- Go\n%s\n--- TS\n%s", i, strings.Join(got.argv, "\n"), strings.Join(want.argv, "\n"))
				}
				for _, path := range slices.Sorted(maps.Keys(want.tree)) {
					if got.tree[path] != want.tree[path] {
						t.Errorf("inv %d: %s: Go %q, TS %q", i, path, got.tree[path], want.tree[path])
					}
				}
				for path := range got.tree {
					if _, ok := want.tree[path]; !ok {
						t.Errorf("inv %d: Go wrote %s, TS did not", i, path)
					}
				}
				if _, ok := writtenBundle(want.stdout); !ok && jsonField(want.stdout, "resultFile") == "" {
					t.Errorf("inv %d: no proof bundle or minimization result written; the comparison needs one", i)
				}
			}
		})
	}
}
