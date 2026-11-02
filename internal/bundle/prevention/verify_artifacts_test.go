package prevention

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/schema"
)

func baseDirectory(name string) string {
	return filepath.Join("..", "..", "..", "difftest", "testdata", "bases", name)
}

// copyBase copies a committed prevention base into a fresh directory so a test
// can damage it without touching retained bytes.
func copyBase(t *testing.T, name string) string {
	t.Helper()
	source := baseDirectory(name)
	destination := filepath.Join(t.TempDir(), "package")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

// schemaParsed is the independent expectation: the retained bytes parsed by the
// JSON parser and then by the given schema, like TS Schema.parse(JSON.parse()).
func schemaParsed(t *testing.T, directory, name string, parse func(jsjson.Value) (jsjson.Value, string, bool)) jsjson.Value {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsjson.Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	out, problem, ok := parse(value)
	if !ok {
		t.Fatalf("%s does not parse: %s", name, problem)
	}
	return out
}

func parseManifest(v jsjson.Value) (jsjson.Value, string, bool) {
	out, issues, ok := schema.Parse(ManifestSchema, v)
	if !ok {
		return jsjson.Value{}, schema.ErrorMessage(issues), false
	}
	return out, "", true
}

func parseBody(v jsjson.Value) (jsjson.Value, string, bool) {
	out, issues, ok := schema.Parse(BodySchema, v)
	if !ok {
		return jsjson.Value{}, schema.ErrorMessage(issues), false
	}
	return out, "", true
}

// Catches a refactor that changes the verdict while adding artifacts, or that
// returns objects other than the ones the single verification pass parsed.
func TestVerifyWithArtifactsPreservesVerdict(t *testing.T) {
	for _, name := range []string{"prevention-summary", "prevention-verified"} {
		t.Run(name, func(t *testing.T) {
			directory := baseDirectory(name)
			want := Verify(directory, "", false)
			got, manifest, body := VerifyWithArtifacts(directory, "", false)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
			if !got.Valid || manifest.Kind() != jsjson.Object || body.Kind() != jsjson.Object {
				t.Fatalf("missing verified artifacts")
			}
			// Independent expectation: the retained bytes parsed through the
			// schema, which fixes member order (the disk order is canonical
			// alphabetical, not the schema-parsed order TS returns).
			t.Run("manifest", func(t *testing.T) {
				want := schemaParsed(t, directory, "manifest.json", parseManifest)
				if g, w := jsjson.Stringify(manifest), jsjson.Stringify(want); g != w {
					t.Fatalf("manifest differs from schema-parsed retained bytes\n got %s\nwant %s", g, w)
				}
			})
			t.Run("body", func(t *testing.T) {
				want := schemaParsed(t, directory, "prevention.json", parseBody)
				if g, w := jsjson.Stringify(body), jsjson.Stringify(want); g != w {
					t.Fatalf("body differs from schema-parsed retained bytes\n got %s\nwant %s", g, w)
				}
			})
			t.Run("verdict-ties-to-manifest", func(t *testing.T) {
				if got.Classification == nil || manifest.Get("classification").Str() != *got.Classification {
					t.Fatalf("returned manifest is not the one the verdict classified")
				}
				if got.RootDigest == nil || manifest.Get("rootDigest").Str() != *got.RootDigest {
					t.Fatalf("returned manifest root differs from the verified root")
				}
			})
		})
	}
}

// Catches a wrapper pair that diverges on rootProvided or on early/catch
// returns: every failing shape must give the sibling's exact result and expose
// only artifacts that were really parsed.
func TestVerifyWithArtifactsPreservesInvalidAndPartialParses(t *testing.T) {
	const wrongRoot = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	type shape int
	const (
		neither shape = iota
		manifestOnly
		bodyOnly
		both
	)
	cases := []struct {
		name         string
		prepare      func(t *testing.T) (directory, root string, provided bool)
		wantArtifact shape
		wantValid    bool
	}{
		{"missing-directory", func(t *testing.T) (string, string, bool) {
			return filepath.Join(t.TempDir(), "absent"), "", false
		}, neither, false},
		{"file-instead-of-directory", func(t *testing.T) (string, string, bool) {
			file := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return file, "", false
		}, neither, false},
		{"wrong-external-root", func(t *testing.T) (string, string, bool) {
			return copyBase(t, "prevention-summary"), wrongRoot, true
		}, both, false},
		{"empty-external-root-is-provided", func(t *testing.T) (string, string, bool) {
			return copyBase(t, "prevention-verified"), "", true
		}, both, false},
		{"missing-body", func(t *testing.T) (string, string, bool) {
			directory := copyBase(t, "prevention-summary")
			if err := os.Remove(filepath.Join(directory, "prevention.json")); err != nil {
				t.Fatal(err)
			}
			return directory, "", false
		}, manifestOnly, false},
		{"malformed-manifest", func(t *testing.T) (string, string, bool) {
			directory := copyBase(t, "prevention-summary")
			if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			return directory, "", false
		}, bodyOnly, false},
		{"malformed-body", func(t *testing.T) (string, string, bool) {
			directory := copyBase(t, "prevention-summary")
			if err := os.WriteFile(filepath.Join(directory, "prevention.json"), []byte("[]"), 0o600); err != nil {
				t.Fatal(err)
			}
			return directory, "", false
		}, manifestOnly, false},
		{"extra-artifact", func(t *testing.T) (string, string, bool) {
			directory := copyBase(t, "prevention-summary")
			if err := os.WriteFile(filepath.Join(directory, "extra.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return directory, "", false
		}, both, false},
		{"missing-repaired-runs-for-verified", func(t *testing.T) (string, string, bool) {
			directory := copyBase(t, "prevention-verified")
			if err := os.Remove(filepath.Join(directory, "repaired-runs.json")); err != nil {
				t.Fatal(err)
			}
			return directory, "", false
		}, both, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			directory, root, provided := c.prepare(t)
			want := Verify(directory, root, provided)
			got, manifest, body := VerifyWithArtifacts(directory, root, provided)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("verdict differs from sibling\n got %+v\nwant %+v", got, want)
			}
			if got.Valid != c.wantValid {
				t.Fatalf("valid = %v, want %v: %+v", got.Valid, c.wantValid, got)
			}
			hasManifest := manifest.Kind() == jsjson.Object
			hasBody := body.Kind() == jsjson.Object
			if wantManifest := c.wantArtifact == manifestOnly || c.wantArtifact == both; hasManifest != wantManifest {
				t.Fatalf("manifest present = %v, want %v", hasManifest, wantManifest)
			}
			if wantBody := c.wantArtifact == bodyOnly || c.wantArtifact == both; hasBody != wantBody {
				t.Fatalf("body present = %v, want %v", hasBody, wantBody)
			}
			if !hasManifest && manifest.Kind() != jsjson.Undefined {
				t.Fatalf("absent manifest must be Undefined, got kind %v", manifest.Kind())
			}
			if !hasBody && body.Kind() != jsjson.Undefined {
				t.Fatalf("absent body must be Undefined, got kind %v", body.Kind())
			}
			// The verdict's classification exists exactly when a manifest parsed,
			// and then it names that very manifest.
			if hasManifest {
				if got.Classification == nil || *got.Classification != manifest.Get("classification").Str() {
					t.Fatalf("classification %v does not match returned manifest", got.Classification)
				}
			} else if got.Classification != nil {
				t.Fatalf("classification %q without a parsed manifest", *got.Classification)
			}
		})
	}
}
