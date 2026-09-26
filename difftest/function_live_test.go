package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

// seedTexts reads files relative to difftest/testdata/bases.
func seedTexts(t *testing.T, rels ...string) []string {
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	var out []string
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

func canonicalOf(v jsjson.Value) string {
	s, err := canonical.CanonicalJSON(v)
	if err != nil {
		return "ERROR:" + err.Error()
	}
	return s
}

// liveCompare calls an oracle op that returns a string (canonical JSON, or
// "THREW:<message>" when TS threw) and compares it with the Go result. Inputs
// are deduplicated: it runs until liveCases distinct inputs were compared (or
// gives up after 5x attempts) and logs how many there were.
func liveCompare(t *testing.T, op string, next func(c *oracle.Client, r *rand.Rand, i int) (args, got string)) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(15, uint64(len(op))))
	seen := map[string]bool{}
	threw := 0
	for i := 0; len(seen) < liveCases && i < 5*liveCases; i++ {
		args, got := next(c, r, i)
		if seen[args] {
			continue
		}
		seen[args] = true
		raw, err := c.Call(op, args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if strings.HasPrefix(want.Str(), "THREW:") {
			threw++
			continue
		}
		if got != want.Str() {
			t.Fatalf("%s(%s):\ngot  %s\nwant %s", op, args, got, want.Str())
		}
	}
	t.Logf("%s: %d distinct inputs (%d where TS threw)", op, len(seen), threw)
}

// oracleText calls an oracle op that returns a string.
func oracleText(t *testing.T, c *oracle.Client, op, args string) string {
	raw, err := c.Call(op, args)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.Str()
}

func TestLiveWitness(t *testing.T) {
	seeds := seedTexts(t, "git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json")
	liveCompare(t, "verifyFrozenWitnessRecord", func(_ *oracle.Client, r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptDeep(r, text)
		}
		value, _ := jsjson.Parse(text)
		expected, provided := "", false
		switch r.IntN(3) {
		case 1:
			expected, provided = value.Get("frozenDigest").Str(), true
		case 2:
			expected, provided = "nope", true
		}
		args := `{"text":` + jsjson.Quote(text)
		if provided {
			args += `,"expected":` + jsjson.Quote(expected)
		}
		return args + "}", canonicalOf(gitproof.VerifyFrozenWitnessRecord(value, expected, provided).JSON())
	})
}

// Ledger seeds: the two committed ledgers plus a TS-built one with every
// event type (baseline and turn snapshots, tool use). Half of the mutated
// ledgers are re-signed by TS so the semantic checks run behind a valid chain.
func TestLiveLedger(t *testing.T) {
	seeds := seedTexts(t, "git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json")
	var rich string
	liveCompare(t, "verifyCodexLifecycleLedger", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		if rich == "" {
			rich = oracleText(t, c, "richLedger", "{}")
			seeds = append(seeds, rich)
		}
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			for n := r.IntN(2); n >= 0; n-- {
				text = gen.CorruptDeep(r, text)
			}
			if r.IntN(2) == 0 {
				text = oracleText(t, c, "resignLedger", `{"text":`+jsjson.Quote(text)+"}")
			}
		}
		value, _ := jsjson.Parse(text)
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(gitproof.VerifyCodexLifecycleLedger(value).JSON())
	})
	if !strings.Contains(rich, "TURN_TREE_SNAPSHOT") || !strings.Contains(rich, "TOOL_USE_STARTED") || !strings.Contains(rich, "SESSION_BASELINE_SNAPSHOT") {
		t.Fatalf("rich ledger seed lacks snapshot or tool-use events")
	}
}

// Sandbox seeds: the committed Docker audits plus TS-signed legacy v1,
// UNSAFE_LOCAL and non-empty environment variants. Mutations are re-signed
// half the time, and both sides run the schema first, as verify does.
func TestLiveSandbox(t *testing.T) {
	dir := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases", "git-unbound", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seeds []string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		run, _ := jsjson.Parse(string(raw))
		seeds = append(seeds, jsjson.Stringify(run.Get("sandbox")))
	}
	variants := 0
	accepted := map[string]int{}
	liveCompare(t, "validateSandboxPlanAudit", func(c *oracle.Client, r *rand.Rand, i int) (string, string) {
		if variants == 0 {
			docker := seeds[0]
			for _, v := range []string{"legacy", "unsafe-local", "environment"} {
				seeds = append(seeds, oracleText(t, c, "sandboxVariant", `{"text":`+jsjson.Quote(docker)+`,"variant":"`+v+`"}`))
				variants++
			}
		}
		seed := r.IntN(len(seeds))
		text := seeds[seed]
		if i >= len(seeds) {
			for n := r.IntN(3); n >= 0; n-- {
				text = gen.CorruptLeaf(r, text)
			}
			if r.IntN(2) == 0 {
				legacy := "false"
				if r.IntN(3) == 0 {
					legacy = "true"
				}
				text = oracleText(t, c, "resignSandbox", `{"text":`+jsjson.Quote(text)+`,"legacy":`+legacy+"}")
			}
		}
		value, _ := jsjson.Parse(text)
		parsed, issues, ok := schema.Parse(gitproof.SandboxAuditSchema, value)
		if !ok {
			return `{"text":` + jsjson.Quote(text) + "}", "SCHEMA:" + schema.ErrorMessage(issues)
		}
		errs := gitproof.ValidateSandboxPlanAudit(parsed)
		if len(errs) == 0 {
			accepted[parsed.Get("kind").Str()+envTag(text)]++
		}
		items := []jsjson.Value{}
		for _, e := range errs {
			items = append(items, jsjson.MakeString(e))
		}
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(jsjson.MakeArray(items))
	})
	t.Logf("accepted audits among generated inputs: %v", accepted)
	for _, kind := range []string{"DOCKER_ISOLATED", "UNSAFE_LOCAL"} {
		if accepted[kind] == 0 {
			t.Errorf("no %s audit was accepted: the digest-valid variants are not reaching the acceptance path", kind)
		}
	}
}

// envTag marks audits with the non-empty environment policy variant;
// it only labels the acceptance log.
func envTag(text string) string {
	if strings.Contains(text, "FEATURE_FLAG") {
		return "+env"
	}
	return ""
}
