//go:build faultline_test && unix

package difftest

import (
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
)

// TestLiveGitRefusals runs investigate git and minimize git under the seed
// preload (TS) and Go with a fake git ahead of the case's recorder on PATH
// (the recorder logs the calls the fake passes on), in the git-two-states writer scenario, and compares them like the
// writer replay (stdout, stderr, exit, argv, tree). The fakes make git
// misbehave where a real repository cannot do so the same way on every git
// release (round 8 review): merge-base failing with exit 128 ("Unable to
// verify ancestry"), and rev-parse printing a non-object-id for a commit
// or a tree ("Git returned an invalid … object id for …").
func TestLiveGitRefusals(t *testing.T) {
	oracle.Skip(t)
	setWriterUmask(t)
	repo := repoRoot(t)
	image := pinnedImage
	digest := "sha256:a1171dcd4e14e30d27b92009ede4756b36a0a9318ecd534190a8bc2a1da165d6"
	investigate := []string{"investigate", "git", "--repo", "repo", "--from", "HEAD~1", "--to", "HEAD", "--proposal", "demo-1", "--image", image}
	minimize := []string{"minimize", "git", "--repo", "repo", "--before", "HEAD~1", "--after", "HEAD", "--proposal", "demo-1", "--expect-digest", digest, "--image", image}
	cases := []struct {
		name, fake string
		argv       []string
		want       string
	}{
		{"investigate-merge-base-fails", "fail-merge-base", investigate, "Unable to verify ancestry: error: planted merge-base failure"},
		{"investigate-bad-commit-oid", "bad-oid", investigate, "Git returned an invalid commit object id for revision"},
		{"investigate-bad-tree-oid", "bad-tree-oid", investigate, "Git returned an invalid tree object id for commit"},
		{"minimize-bad-commit-oid", "bad-oid", minimize, "Git returned an invalid commit object id for revision"},
		{"minimize-bad-tree-oid", "bad-tree-oid", minimize, "Git returned an invalid tree object id for revision"},
	}
	defer func() {
		liveSummary(t, "%d fake-git refusals compared", len(cases))
		// investigate.go's literals: these refusals plus every golden.
		requireReached(t, "internal/gitinv/investigate.go", recordedOutputs(t))
	}()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := writerTemplate{ID: "git-refusal-" + tc.name, Scenario: "git-two-states", Argv: [][]string{tc.argv}, FakeDocker: "pass-fail", PosixOnly: true}
			fake := repo + "/difftest/testdata/fakegit/" + tc.fake
			side := func(command []string) writerResult {
				return runWriterCaseWith(t, repo, tpl, writerRun{
					noVerify: true,
					command: func(r writerRoots, _ int, args []string) []string {
						// The fake goes first: it strips its own directory and
						// runs the next git on PATH, the case's recorder, for
						// every call it does not fail.
						return append([]string{"sh", "-c", `PATH="$1:$PATH"; shift; exec "$@"`, "sh", fake}, append(command, args...)...)
					},
				})[0]
			}
			ts, goRes := side(tsCommand(repo)), side([]string{flBinary})
			compareWriter(t, tc.name, goRes, asGolden(ts))
			if !strings.Contains(ts.Stdout+ts.Stderr, tc.want) {
				t.Fatalf("TS did not print %q:\n%s%s", tc.want, ts.Stdout, ts.Stderr)
			}
			recordOutput(t, ts.Stdout+ts.Stderr)
		})
	}
}
