package difftest

import (
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
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
	"GitCommitStateSchema":                 gitproof.GitCommitStateSchema,
	"GitInvestigationRunFactSchema":        gitproof.GitInvestigationRunFactSchema,
	"StableGitStateSchema":                 gitproof.StableGitStateSchema,
	"StableGitTransitionSchema":            gitproof.StableGitTransitionSchema,
	"GitInvestigationResultSchema":         gitproof.GitInvestigationResultSchema,
	"EnvironmentFingerprintSchema":         gitproof.EnvironmentFingerprintSchema,
	"MaterializedOverlaySchema":            gitproof.MaterializedOverlaySchema,
	"FrozenWitnessSchema":                  gitproof.FrozenWitnessSchema,
	"GitProofSourceMetadataSchema":         gitproof.GitProofSourceMetadataSchema,
	"GitProofBundleManifestSchema":         gitproof.GitProofBundleManifestSchema,
	"CodexLifecycleLedgerSchema":           gitproof.CodexLifecycleLedgerSchema,
}

// namedSeeds lists base files (relative to difftest/testdata/bases) that satisfy each schema.
var namedSeeds = map[string][]string{
	"PreventionProofBodySchema":            {"prevention-summary/prevention.json", "prevention-verified/prevention.json"},
	"PreventionProofManifestSchema":        {"prevention-summary/manifest.json", "prevention-verified/manifest.json"},
	"PreventionRepairedRunsArtifactSchema": {"prevention-verified/repaired-runs.json"},
	"GitProofBundleManifestSchema":         {"git-unbound/manifest.json", "git-partially-bound/manifest.json", "git-fully-bound/manifest.json", "git-sample-self-incident/manifest.json"},
	"GitInvestigationResultSchema":         {"git-unbound/investigation.json", "git-two-states/investigation.json"},
	"FrozenWitnessSchema":                  {"git-unbound/witness/frozen.json", "git-sample-self-incident/witness/frozen.json"},
	"GitProofSourceMetadataSchema":         {"git-unbound/source/metadata.json", "git-two-states/source/metadata.json"},
	"StableGitTransitionSchema":            {"git-unbound/transitions/0000.json"},
	"GitInvestigationRunFactSchema":        {firstRunArtifact("git-unbound")},
	"GitCommitStateSchema":                 {"git-unbound/investigation.json#states.0"},
	"StableGitStateSchema":                 {"git-unbound/investigation.json#stableStates.0"},
	"EnvironmentFingerprintSchema":         {"git-unbound/investigation.json#environment.fingerprints.0.fingerprint"},
	"MaterializedOverlaySchema":            {"git-unbound/investigation.json#runs.0.overlays.0"},
	"CodexLifecycleLedgerSchema":           {"git-partially-bound/lifecycle/ledger.json", "git-fully-bound/lifecycle/ledger.json"},
}

// firstRunArtifact returns base + "/runs/" + the first file name in that directory.
func firstRunArtifact(base string) string {
	entries, err := os.ReadDir(filepath.Join("testdata", "bases", base, "runs"))
	if err != nil || len(entries) == 0 {
		return base + "/runs/missing"
	}
	return base + "/runs/" + entries[0].Name()
}

// seedText reads a seed; "file#a.0.b" selects a sub-document by dotted path
// (numeric segments index arrays).
func seedText(t *testing.T, bases, seed string) string {
	file, path, sub := strings.Cut(seed, "#")
	raw, err := os.ReadFile(filepath.Join(bases, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	if !sub {
		return string(raw)
	}
	v, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, segment := range strings.Split(path, ".") {
		if i, err := strconv.Atoi(segment); err == nil && v.Kind() == jsjson.Array {
			v = v.Items()[i]
		} else {
			v = v.Get(segment)
		}
	}
	return jsjson.Stringify(v)
}

func TestLiveNamedSchemas(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	bases := filepath.Join(oracle.RepoRoot(t), "difftest", "testdata", "bases")
	r := rand.New(rand.NewPCG(13, 14))
	// Sorted names keep the shared RNG's draws, and so the inputs, fixed.
	names := slices.Sorted(maps.Keys(namedSeeds))
	for _, name := range names {
		seeds := namedSeeds[name]
		for _, seed := range seeds {
			raw := seedText(t, bases, seed)
			for i := 0; i < liveCases/len(namedSeeds)/len(seeds); i++ {
				text := raw
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
