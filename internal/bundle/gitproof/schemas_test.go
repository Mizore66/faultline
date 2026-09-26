package gitproof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func baseFile(t *testing.T, base, rel string) jsjson.Value {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "difftest", "testdata", "bases", base, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsjson.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCommittedBasesSatisfySchemas(t *testing.T) {
	for _, base := range []string{"git-unbound", "git-fully-bound", "git-two-states", "git-sample-self-incident"} {
		for rel, s := range map[string]schema.Schema{
			"manifest.json":        GitProofBundleManifestSchema,
			"investigation.json":   GitInvestigationResultSchema,
			"witness/frozen.json":  FrozenWitnessSchema,
			"source/metadata.json": GitProofSourceMetadataSchema,
		} {
			if _, issues, ok := schema.Parse(s, baseFile(t, base, rel)); !ok {
				t.Errorf("%s/%s: %s", base, rel, schema.ErrorMessage(issues))
			}
		}
	}
}

func TestSafeGitRevision(t *testing.T) {
	for in, want := range map[string]bool{"HEAD~2": true, "-x": false, "": false, "a\nb": false, "a\x00": false} {
		if safeGitRevision(in) != want {
			t.Errorf("safeGitRevision(%q) != %v", in, want)
		}
	}
}

func TestCanonicalBase64(t *testing.T) {
	for in, want := range map[string]bool{"QQ==": true, "QR==": false, "QUI=": true, "QUJD": true, "QQ": false, "": true} {
		if isCanonicalBase64(in) != want {
			t.Errorf("isCanonicalBase64(%q) != %v", in, want)
		}
	}
}
