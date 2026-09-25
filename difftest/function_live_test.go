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
// "THREW:<message>" when TS threw) and compares it with the Go result.
func liveCompare(t *testing.T, op string, next func(r *rand.Rand, i int) (args, got string)) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(15, uint64(len(op))))
	for i := 0; i < liveCases; i++ {
		args, got := next(r, i)
		raw, err := c.Call(op, args)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsjson.Parse(raw)
		if strings.HasPrefix(want.Str(), "THREW:") {
			continue
		}
		if got != want.Str() {
			t.Fatalf("%s(%s):\ngot  %s\nwant %s", op, args, got, want.Str())
		}
	}
}

func TestLiveWitness(t *testing.T) {
	seeds := seedTexts(t, "git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json")
	liveCompare(t, "verifyFrozenWitnessRecord", func(r *rand.Rand, i int) (string, string) {
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
func TestLiveLedger(t *testing.T) {
	seeds := seedTexts(t, "git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json")
	liveCompare(t, "verifyCodexLifecycleLedger", func(r *rand.Rand, i int) (string, string) {
		text := seeds[r.IntN(len(seeds))]
		if i >= len(seeds) {
			text = gen.CorruptDeep(r, text)
		}
		value, _ := jsjson.Parse(text)
		return `{"text":` + jsjson.Quote(text) + "}", canonicalOf(gitproof.VerifyCodexLifecycleLedger(value).JSON())
	})
}
