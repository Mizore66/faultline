//go:build faultline_test && linux

package difftest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Source-to-shard drift guard: every caseID in writerFaultMatrix() must be
// listed exactly once across the six `cases:` lines of the writer-fault shard
// matrix in .github/workflows/go.yml, with no extra or duplicate caseIDs. A new
// caseID that is not added to a shard would otherwise never run in CI.

var shardCasesLine = regexp.MustCompile(`(?m)^ {12}cases: (.*)$`)

// shardCaseIDs returns every caseID listed on a shard `cases:` line, in order,
// keeping duplicates, and the number of lines found.
func shardCaseIDs(workflow string) ([]string, int) {
	var ids []string
	lines := shardCasesLine.FindAllStringSubmatch(workflow, -1)
	for _, m := range lines {
		ids = append(ids, strings.Fields(m[1])...)
	}
	return ids, len(lines)
}

// shardDrift reports problems between the source caseIDs and the shard lists.
func shardDrift(source, shards []string) []string {
	var problems []string
	want := map[string]bool{}
	for _, id := range source {
		want[id] = true
	}
	seen := map[string]int{}
	for _, id := range shards {
		seen[id]++
	}
	for id := range want {
		if seen[id] == 0 {
			problems = append(problems, "missing from shards: "+id)
		}
	}
	for id, n := range seen {
		if !want[id] {
			problems = append(problems, "extra in shards: "+id)
		}
		if n > 1 {
			problems = append(problems, "duplicate in shards: "+id)
		}
	}
	sort.Strings(problems)
	return problems
}

func TestWriterFaultShardDriftGuardFixtures(t *testing.T) {
	if p := shardDrift([]string{"a", "b"}, []string{"b", "a"}); len(p) != 0 {
		t.Errorf("matching sets reported drift: %v", p)
	}
	for name, c := range map[string]struct {
		shards []string
		want   string
	}{
		"missing":   {[]string{"a"}, "missing from shards: b"},
		"extra":     {[]string{"a", "b", "c"}, "extra in shards: c"},
		"duplicate": {[]string{"a", "b", "b"}, "duplicate in shards: b"},
	} {
		p := shardDrift([]string{"a", "b"}, c.shards)
		if len(p) != 1 || p[0] != c.want {
			t.Errorf("%s: got %v, want [%s]", name, p, c.want)
		}
	}
	ids, n := shardCaseIDs("            cases: x y\n          - shard: z\n            cases: w\n")
	if n != 2 || strings.Join(ids, ",") != "x,y,w" {
		t.Errorf("parse fixture: %v %d", ids, n)
	}
}

func TestWriterFaultShardsCoverSource(t *testing.T) {
	repo := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	shards, lines := shardCaseIDs(string(data))
	if lines != 6 {
		t.Fatalf("go.yml has %d shard cases lines, want 6", lines)
	}
	var source []string
	seen := map[string]bool{}
	for _, f := range writerFaultMatrix() {
		if !seen[f.caseID] {
			seen[f.caseID] = true
			source = append(source, f.caseID)
		}
	}
	if len(source) == 0 {
		t.Fatal("writerFaultMatrix has no caseIDs")
	}
	for _, p := range shardDrift(source, shards) {
		t.Error(p)
	}
}
