package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

// namedSchemas maps oracle names to Go schemas; later tasks add entries.
var namedSchemas = map[string]schema.Schema{
	"PreventionProofBodySchema":            prevention.BodySchema,
	"PreventionProofManifestSchema":        prevention.ManifestSchema,
	"PreventionRepairedRunsArtifactSchema": prevention.RepairedRunsSchema,
}

// namedSeeds lists base files (relative to difftest/testdata/bases) that satisfy each schema.
var namedSeeds = map[string][]string{
	"PreventionProofBodySchema":            {"prevention-summary/prevention.json", "prevention-verified/prevention.json"},
	"PreventionProofManifestSchema":        {"prevention-summary/manifest.json", "prevention-verified/manifest.json"},
	"PreventionRepairedRunsArtifactSchema": {"prevention-verified/repaired-runs.json"},
}

func TestLiveNamedSchemas(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	r := rand.New(rand.NewPCG(13, 14))
	for name, seeds := range namedSeeds {
		for _, seed := range seeds {
			raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(seed)))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < liveCases/len(namedSeeds)/len(seeds); i++ {
				text := string(raw)
				if i > 0 {
					text = gen.CorruptDeep(r, text)
				}
				want := call(t, c, "zodNamed", `{"name":"`+name+`","text":`+jsjson.Quote(text)+`}`)
				value, _ := jsjson.Parse(text)
				out, issues, ok := schema.Parse(namedSchemas[name], value)
				if ok != want.Field("success").Bool() {
					t.Fatalf("%s on %s: ok=%v, zod=%v\n%s", name, text, ok, want.Field("success").Bool(), want.Field("message").Str())
				}
				if !ok {
					if got := schema.ErrorMessage(issues); got != want.Field("message").Str() {
						t.Fatalf("%s on %s:\ngot  %s\nwant %s", name, text, got, want.Field("message").Str())
					}
					continue
				}
				got, err := canonical.CanonicalJSON(out)
				if err != nil {
					got = "ERROR:" + err.Error()
				}
				if got != want.Field("canonical").Str() {
					t.Fatalf("%s on %s: data %s, zod %s", name, text, got, want.Field("canonical").Str())
				}
			}
		}
	}
}
