//go:build faultline_test

package difftest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/cli"
)

func BenchmarkVerifyGitFullyBound(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "git-fully-bound")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}

func BenchmarkVerifyDemoRerun(b *testing.B) {
	dir := filepath.Join(oracle.RepoRoot(b), "difftest", "testdata", "bases", "demo-rerun")
	for b.Loop() {
		var out, errOut bytes.Buffer
		if cli.Run([]string{"verify", dir}, &out, &errOut) != 0 {
			b.Fatal(out.String(), errOut.String())
		}
	}
}

// BenchmarkInvestigateGit times `fl investigate git` on git-many-states (12
// states, 36 runs) with the pass-fail fake Docker, the writer case
// investigate-many-states, for frozen TS (node dist/cli.js, when node and
// dist/ are present) and Go; building each workspace is not timed. Besides
// ns/op it reports the median, fastest and slowest run in ms (BENCHMARK.md).
func BenchmarkInvestigateGit(b *testing.B) {
	if windows {
		b.Skip("POSIX fake Docker and recorders")
	}
	repo := oracle.RepoRoot(b)
	var tpl writerTemplate
	for _, candidate := range loadWriterTemplates(b, repo) {
		if candidate.ID == "investigate-many-states" {
			tpl = candidate
		}
	}
	if len(tpl.Argv) != 1 {
		b.Fatal("no single-invocation writer template investigate-many-states")
	}
	setWriterUmask(b)
	benchmarkSides(b, repo, tpl, func(b *testing.B) writerRoots {
		r := makeWriterRoots(b)
		prepareWriterCase(b, repo, tpl, r)
		return r
	}, func(exit int, _ string) bool { return exit == 0 })
}

// benchmarkSides times tpl's single invocation under Go and, when node and
// dist/ are present, frozen TS (seeded like the writer replay), in a
// workspace prepare builds untimed for each run; ok says whether an exit
// status and output (stdout then stderr) are the expected ones. Besides ns/op it reports the median, fastest
// and slowest run in ms (BENCHMARK.md).
func benchmarkSides(b *testing.B, repo string, tpl writerTemplate, prepare func(b *testing.B) writerRoots, ok func(exit int, out string) bool) {
	sides := []struct {
		name    string
		command []string
	}{{"Go", []string{flBinary}}}
	if _, err := exec.LookPath("node"); err == nil {
		if _, err := os.Stat(filepath.Join(repo, "dist", "cli.js")); err == nil {
			sides = append(sides, struct {
				name    string
				command []string
			}{"TS", []string{"node", "--import", "file://" + filepath.Join(repo, "difftest", "gen", "seed.mjs"), filepath.Join(repo, "dist", "cli.js")}})
		}
	}
	for _, side := range sides {
		b.Run(side.name, func(b *testing.B) {
			var runs []time.Duration
			for range b.N {
				b.StopTimer()
				r := prepare(b)
				cmd := exec.Command(side.command[0], append(slices.Clone(side.command[1:]), substituteArgs(tpl.Argv[0], r)...)...)
				cmd.Dir = r.ws
				cmd.Env = writerEnv(repo, r, tpl.ID, tpl)
				var out bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &out
				b.StartTimer()
				start := time.Now()
				err := cmd.Run()
				runs = append(runs, time.Since(start))
				b.StopTimer()
				var exitErr *exec.ExitError
				if (err != nil && !errors.As(err, &exitErr)) || !ok(cmd.ProcessState.ExitCode(), out.String()) {
					b.Fatalf("%s: exit %d, %v\n%s", side.name, cmd.ProcessState.ExitCode(), err, out.String())
				}
				b.StartTimer()
			}
			slices.Sort(runs)
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			b.ReportMetric(ms(runs[len(runs)/2]), "median-ms")
			b.ReportMetric(ms(runs[0]), "min-ms")
			b.ReportMetric(ms(runs[len(runs)-1]), "max-ms")
		})
	}
}

// Dirty repository of the doctor and init benchmarks (spec 4.2 point 7):
// benchFiles committed files (package.json and the rest in benchDirs
// directories), the first benchModified of them modified, and
// benchUntracked untracked files. With the .faultlineignore `fl init --yes`
// writes, its pre-warm snapshot covers exactly 2,000 eligible files, the
// limit (src/turn-snapshot.ts:93); one more and it is SKIPPED.
const (
	benchFiles     = 1_989
	benchDirs      = 20
	benchModified  = 100
	benchUntracked = 10
)

// buildBenchRepository makes the dirty repository in dir with fixed dates
// and identity, and the empty global git config of the writer cases.
func buildBenchRepository(b *testing.B, dir string) {
	b.Helper()
	write := func(rel, text string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=FaultLine", "GIT_AUTHOR_EMAIL=faultline@example.test",
			"GIT_COMMITTER_NAME=FaultLine", "GIT_COMMITTER_EMAIL=faultline@example.test",
			"GIT_AUTHOR_DATE=2026-07-16T10:00:00Z", "GIT_COMMITTER_DATE=2026-07-16T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	file := func(i int) string { return fmt.Sprintf("src/m%02d/f%04d.js", i%benchDirs, i) }
	write("package.json", "{\"name\":\"bench\",\"private\":true}\n")
	for i := range benchFiles - 1 {
		write(file(i), fmt.Sprintf("export const value%d = %d;\n", i, i))
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "bench")
	for i := range benchModified {
		write(file(i), fmt.Sprintf("export const value%d = %d; // changed\n", i, -i))
	}
	for i := range benchUntracked {
		write(fmt.Sprintf("notes/n%02d.txt", i), fmt.Sprintf("untracked note %d\n", i))
	}
}

// benchTemplate is a single-invocation writer template for the benchmark
// repository with the ready fake toolchain.
func benchTemplate(id string, argv ...string) writerTemplate {
	return writerTemplate{ID: id, Argv: [][]string{argv}, FakeTools: "ready", PosixOnly: true}
}

// BenchmarkDoctor times `fl doctor` (fake toolchain ready) on the 2,000-file
// dirty repository, for frozen TS and Go; doctor writes nothing, so every
// run uses the same workspace.
func BenchmarkDoctor(b *testing.B) {
	if windows {
		b.Skip("POSIX fake toolchain and recorders")
	}
	repo := oracle.RepoRoot(b)
	setWriterUmask(b)
	r := makeWriterRoots(b)
	buildBenchRepository(b, r.ws)
	benchmarkSides(b, repo, benchTemplate("bench-doctor", "doctor"), func(*testing.B) writerRoots { return r },
		func(exit int, out string) bool {
			return exit == 0 && strings.Contains(out, fmt.Sprintf("(%d changed paths reported by Git.)", benchModified+benchUntracked))
		})
}

// BenchmarkInitYes times `fl init --yes` (fake toolchain ready, so the
// pre-warm snapshot runs real git) on a fresh copy of the 2,000-file dirty
// repository each run, for frozen TS and Go.
func BenchmarkInitYes(b *testing.B) {
	if windows {
		b.Skip("POSIX fake toolchain and recorders")
	}
	repo := oracle.RepoRoot(b)
	setWriterUmask(b)
	base := filepath.Join(b.TempDir(), "repo")
	buildBenchRepository(b, base)
	benchmarkSides(b, repo, benchTemplate("bench-init-yes", "init", "--yes"), func(b *testing.B) writerRoots {
		r := makeWriterRoots(b)
		if err := os.Remove(r.ws); err != nil {
			b.Fatal(err)
		}
		if err := os.CopyFS(r.ws, os.DirFS(base)); err != nil {
			b.Fatal(err)
		}
		return r
	}, func(exit int, out string) bool {
		return exit == 0 && strings.Contains(out, `"status": "PREWARMED"`) && strings.Contains(out, "\nfirst-turn snapshot pre-warmed\n")
	})
}

// substituteArgs is argv with <WS>, <TMP> and <HOME> replaced.
func substituteArgs(argv []string, r writerRoots) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = substituteRoots(a, r)
	}
	return out
}
