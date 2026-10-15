//go:build faultline_test

package difftest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
				r := makeWriterRoots(b)
				prepareWriterCase(b, repo, tpl, r)
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
				if err != nil {
					b.Fatalf("%s: %v\n%s", side.name, err, out.String())
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

// substituteArgs is argv with <WS>, <TMP> and <HOME> replaced.
func substituteArgs(argv []string, r writerRoots) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = substituteRoots(a, r)
	}
	return out
}
