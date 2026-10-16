// Package release checks a release build of fl (no faultline_test tag, so
// the wall clock and real randomness) where the seeded difftest replays
// cannot look.
package release

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// repoRoot is the directory holding go.mod.
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// copyScenario fills ws from the committed writer scenario name
// (difftest/testdata/scenarios: dirs.txt, then tree/ with each dot-git
// component back to .git), as difftest's copyScenario does.
func copyScenario(t *testing.T, repo, name, ws string) {
	t.Helper()
	dir := filepath.Join(repo, "difftest", "testdata", "scenarios", name)
	dirs, err := os.ReadFile(filepath.Join(dir, "dirs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range strings.Split(string(dirs), "\n") {
		if rel != "" {
			if err := os.MkdirAll(filepath.Join(ws, filepath.FromSlash(rel)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	tree := filepath.Join(dir, "tree")
	err = filepath.WalkDir(tree, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tree, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for i, p := range parts {
			if p == "dot-git" {
				parts[i] = ".git"
			}
		}
		dest := filepath.Join(ws, filepath.FromSlash(strings.Join(parts, "/")))
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

type runFact struct {
	StartedAt  string  `json:"startedAt"`
	FinishedAt string  `json:"finishedAt"`
	DurationMs float64 `json:"durationMs"`
}

// TestLiveReleaseClock runs `investigate git` from a release build (and,
// with FAULTLINE_NODE_ORACLE=1, frozen TS without the seed preload) against
// a fake Docker whose every run sleeps one second, then `minimize git`, then
// `witness propose` three times, and checks that the wall-clock reads are where TS makes them
// (round 7 #8). The seeded clock of the replays ticks once per read, so it
// cannot tell a read before Docker from one after it, nor a clock cut to
// whole seconds:
//   - each run fact's startedAt is read before Docker and finishedAt after
//     it: durationMs >= 900 and finishedAt - startedAt == durationMs;
//   - the run timestamps keep their milliseconds (not all .000Z);
//   - the same for every minimization run fact;
//   - the manifest's generatedAt (new Date()) is not before the last run's
//     finishedAt;
//   - proposedAt (new Date()) lies between the times the test read just
//     before and after the command, and not every one of three is .000Z.
func TestLiveReleaseClock(t *testing.T) {
	if tagged {
		t.Skip("runs in the untagged pass (it builds its own release fl either way)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fake Docker is a POSIX sh script")
	}
	repo := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "fl")
	build := exec.Command("go", "build", "-o", bin, "./cmd/fl") // no faultline_test tag: a release build
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	sides := []struct {
		name string
		argv []string
	}{{"Go", []string{bin}}}
	if os.Getenv("FAULTLINE_NODE_ORACLE") == "1" {
		sides = append(sides, struct {
			name string
			argv []string
		}{"TS", []string{"node", filepath.Join(repo, "dist", "cli.js")}})
	}
	for _, side := range sides {
		t.Run(side.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ws, tmp, home := filepath.Join(root, "ws"), filepath.Join(root, "tmp"), filepath.Join(root, "home")
			for _, d := range []string{ws, tmp, home} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			copyScenario(t, repo, "git-two-states", ws)
			var env []string
			for _, kv := range os.Environ() {
				if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "GIT_") && !strings.HasPrefix(k, "FAULTLINE_") && k != "CI" && k != "GITHUB_ACTIONS" {
					env = append(env, kv)
				}
			}
			env = append(env, "HOME="+home, "TMPDIR="+tmp, "LC_ALL=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
				"FAULTLINE_NO_BROWSER=1", "PATH="+filepath.Join(repo, "difftest", "testdata", "fakedocker", "slow")+string(os.PathListSeparator)+os.Getenv("PATH"))
			run := func(args ...string) (string, time.Time, time.Time) {
				t.Helper()
				cmd := exec.Command(side.argv[0], append(side.argv[1:], args...)...)
				cmd.Dir, cmd.Env = ws, env
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				before := time.Now()
				if err := cmd.Run(); err != nil {
					t.Fatalf("%v: %v\n%s%s", args, err, stdout.String(), stderr.String())
				}
				return stdout.String(), before, time.Now()
			}
			out, _, _ := run("investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD", "--proposal", "demo-1",
				"--image", "registry.example/faultline-node@sha256:"+strings.Repeat("b", 64))
			var result struct {
				Investigation struct {
					Runs []runFact `json:"runs"`
				} `json:"investigation"`
				ProofBundle struct {
					Directory string `json:"directory"`
				} `json:"proofBundle"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Investigation.Runs) != 6 {
				t.Fatalf("investigate output: %v (%d runs)\n%s", err, len(result.Investigation.Runs), out)
			}
			wholeSeconds := true
			var lastFinished time.Time
			for i, r := range result.Investigation.Runs {
				started, err1 := time.Parse(time.RFC3339Nano, r.StartedAt)
				finished, err2 := time.Parse(time.RFC3339Nano, r.FinishedAt)
				if err1 != nil || err2 != nil {
					t.Fatalf("run %d: timestamps %q %q", i, r.StartedAt, r.FinishedAt)
				}
				if r.DurationMs < 900 || !started.Before(finished) || float64(finished.Sub(started).Milliseconds()) != r.DurationMs {
					t.Errorf("run %d: startedAt %s, finishedAt %s, durationMs %v: the clock is not read on both sides of a 1 s Docker run", i, r.StartedAt, r.FinishedAt, r.DurationMs)
				}
				wholeSeconds = wholeSeconds && strings.HasSuffix(r.StartedAt, ".000Z") && strings.HasSuffix(r.FinishedAt, ".000Z")
				lastFinished = finished
			}
			if wholeSeconds {
				t.Errorf("every run timestamp is a whole second: Date.now() lost its milliseconds")
			}
			manifest, err := os.ReadFile(filepath.Join(result.ProofBundle.Directory, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m struct {
				GeneratedAt string `json:"generatedAt"`
			}
			if err := json.Unmarshal(manifest, &m); err != nil {
				t.Fatal(err)
			}
			if generated, err := time.Parse(time.RFC3339Nano, m.GeneratedAt); err != nil || generated.Before(lastFinished) {
				t.Errorf("generatedAt %s is before the last run's finishedAt %s", m.GeneratedAt, lastFinished.Format(time.RFC3339Nano))
			}
			// minimize git: each run fact's startedAt is read before
			// mkdtemp and finishedAt after Docker and cleanup.
			out, _, _ = run("minimize", "git", "--repo", "repo", "--before", "HEAD~1", "--after", "HEAD", "--proposal", "demo-1",
				"--expect-digest", "sha256:a1171dcd4e14e30d27b92009ede4756b36a0a9318ecd534190a8bc2a1da165d6",
				"--image", "registry.example/faultline-node@sha256:"+strings.Repeat("b", 64))
			var minimized struct {
				Minimization struct {
					Runs []runFact `json:"runs"`
				} `json:"minimization"`
			}
			if err := json.Unmarshal([]byte(out), &minimized); err != nil || len(minimized.Minimization.Runs) == 0 {
				t.Fatalf("minimize output: %v\n%s", err, out)
			}
			wholeSeconds = true
			for i, r := range minimized.Minimization.Runs {
				started, err1 := time.Parse(time.RFC3339Nano, r.StartedAt)
				finished, err2 := time.Parse(time.RFC3339Nano, r.FinishedAt)
				if err1 != nil || err2 != nil {
					t.Fatalf("minimization run %d: timestamps %q %q", i, r.StartedAt, r.FinishedAt)
				}
				if r.DurationMs < 900 || !started.Before(finished) || float64(finished.Sub(started).Milliseconds()) != r.DurationMs {
					t.Errorf("minimization run %d: startedAt %s, finishedAt %s, durationMs %v: the clock is not read on both sides of a 1 s Docker run", i, r.StartedAt, r.FinishedAt, r.DurationMs)
				}
				wholeSeconds = wholeSeconds && strings.HasSuffix(r.StartedAt, ".000Z") && strings.HasSuffix(r.FinishedAt, ".000Z")
			}
			if wholeSeconds {
				t.Errorf("every minimization run timestamp is a whole second")
			}
			wholeSeconds = true
			input := filepath.Join(ws, "proposal.json")
			b, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			for i := range 3 {
				text := strings.Replace(string(b), `"demo-1"`, `"clock-`+string(rune('a'+i))+`"`, 1)
				if err := os.WriteFile(input, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				_, before, after := run("witness", "propose", "--input", "proposal.json")
				stored, err := os.ReadFile(filepath.Join(ws, ".faultline", "witnesses", "proposals", "clock-"+string(rune('a'+i))+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var p struct {
					ProposedAt string `json:"proposedAt"`
				}
				if err := json.Unmarshal(stored, &p); err != nil {
					t.Fatal(err)
				}
				at, err := time.Parse(time.RFC3339Nano, p.ProposedAt)
				if err != nil || at.Before(before.Truncate(time.Millisecond)) || at.After(after) {
					t.Errorf("proposedAt %s is outside [%s, %s]", p.ProposedAt, before.UTC().Format(time.RFC3339Nano), after.UTC().Format(time.RFC3339Nano))
				}
				wholeSeconds = wholeSeconds && strings.HasSuffix(p.ProposedAt, ".000Z")
			}
			if wholeSeconds {
				t.Errorf("every proposedAt is a whole second: new Date() lost its milliseconds")
			}
		})
	}
}
