package demo

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// WriteOptions is ProofBundleWriteOptions (src/proof-bundle.ts:41). A nil
// ProofRoot is an omitted option (defaultProofRoot); a pointer to "" is an
// explicit empty root, which path.resolve turns into the current directory.
type WriteOptions struct{ ProofRoot *string }

// Written is the writeProofBundle result. ManifestDigest is the digest of the
// canonical manifest, not of the pretty manifest.json bytes.
type Written struct{ Directory, ManifestDigest, RootDigest string }

// DefaultProofRoot ports defaultProofRoot (src/proof-bundle.ts:57).
func DefaultProofRoot() string { return nodefs.Resolve(".faultline", "bundles") }

// Write ports writeProofBundle (src/proof-bundle.ts:353) for a supplied
// analysis: nothing is schema-parsed, executed or reconstructed. A thrown TS
// error is returned.
func Write(outputDirectory string, analysis jsjson.Value, options WriteOptions) (written Written, err error) {
	err = jsexc.Try(func() { written = write(outputDirectory, analysis, options) })
	if err != nil {
		return Written{}, err
	}
	return written, nil
}

// artifact is one entry of the TS Map<string, string>. A run log body is
// supplied data that JS stores unchecked and only rejects when it is hashed,
// so raw keeps the value and body is valid only once logBody accepted it.
type artifact struct {
	path, body string
	raw        *jsjson.Value
}

// artifacts is the insertion-ordered Map<string, string> of the TS writer.
type artifacts struct {
	list []artifact
	seen map[string]bool
}

// put ports put (src/proof-bundle.ts:165).
func (a *artifacts) put(path, body string) { a.putEntry(path, body, nil) }

func (a *artifacts) putEntry(path, body string, raw *jsjson.Value) {
	normalized := strings.ReplaceAll(path, `\`, "/")
	if a.seen[normalized] {
		bundle.Throw(errors.New(jsexc.Concat("Proof bundle attempted to write the same path twice: ", normalized)))
	}
	jsexc.SetAdd(a.seen, normalized)
	a.list = append(a.list, artifact{normalized, body, raw})
}

// hashBody is sha256(body): a non-string body throws here, in sorted order.
func (a artifact) hashBody() string {
	if a.raw != nil {
		return canonical.SHA256Hex(logBody(*a.raw))
	}
	return canonical.SHA256Hex(a.body)
}

// asJSON is `${JSON.stringify(value, null, 2)}\n`.
func asJSON(value jsjson.Value) string {
	return jsexc.Concat(jsjson.StringifyIndent(value, "  "), "\n")
}

// canonicalStrings is canonicalJson of a string array.
func canonicalStrings(values []string) string {
	items := make([]jsjson.Value, len(values))
	for i, v := range values {
		items[i] = jsjson.MakeString(v)
	}
	return bundle.Must(canonical.CanonicalJSON(jsjson.MakeArray(items)))
}

func write(outputDirectory string, analysis jsjson.Value, options WriteOptions) Written {
	proofRoot := DefaultProofRoot()
	if options.ProofRoot != nil {
		proofRoot = *options.ProofRoot
	}
	output := assertSafeProofOutput(outputDirectory, proofRoot)
	files := &artifacts{seen: map[string]bool{}}
	// The inherited validateAnalysisCoverage reads with non-throwing helpers;
	// the frozen writer's own collection throws, so this call uses the local
	// equivalent (jsCoverage) and leaves the shared verifier unchanged.
	coverageErrors := jsCoverage(analysis)
	if len(coverageErrors) > 0 {
		bundle.Throw(errors.New(jsexc.Concat("Cannot write incomplete proof bundle: ", jsexc.Join(coverageErrors, "; "))))
	}
	// Evaluated in the TS object literal's order; each `a.b` access throws on
	// a null or undefined receiver, and `sample-${id}` coerces with ToString.
	fixtureID := prop(prop(analysis, "fixture"), "id")
	investigationID := jsexc.Concat("sample-", templateString(prop(prop(analysis, "fixture"), "id")))
	generatedAt := prop(analysis, "generatedAt")
	analysisDigest := bundle.Must(canonical.DigestJSON(analysis))
	witnessDigest := prop(prop(analysis, "witness"), "digest")
	environmentDigest := prop(prop(prop(analysis, "prevention"), "firstBad"), "environmentDigest")
	manifest := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.proof-bundle.v2"),
		"investigationId", jsjson.MakeString(investigationID),
		"fixtureId", fixtureID,
		"generatedAt", generatedAt,
		"analysisDigest", jsjson.MakeString(analysisDigest),
		"witnessDigest", witnessDigest,
		"environmentDigest", environmentDigest,
		"mode", prop(analysis, "mode"),
		"integrityScope", jsjson.MakeString("complete-declared-file-set"))
	files.put("manifest.json", asJSON(manifest))
	files.put("report.md", report(analysis))
	files.put("witness/witness.json", asJSON(analysis.Get("witness")))
	files.put("analysis.json", asJSON(analysis))
	files.put("minimization/attempts.json", asJSON(analysis.Get("minimization")))
	files.put("prevention/three-state.json", asJSON(analysis.Get("prevention")))
	files.put("VERIFY.md", verifyInstructions)
	for _, run := range jsIterate(prop(analysis, "runCatalog"), "analysis.runCatalog") {
		base := jsexc.Concat("runs/", safeRunFileName(jsRunID(run)))
		files.put(jsexc.Concat(base, "/result.json"), asJSON(run))
		stdout, stderr := prop(run, "stdout"), prop(run, "stderr")
		files.putEntry(jsexc.Concat(base, "/stdout.log"), stdout.Str(), &stdout)
		files.putEntry(jsexc.Concat(base, "/stderr.log"), stderr.Str(), &stderr)
	}
	names := make([]string, len(files.list))
	byName := make(map[string]artifact, len(files.list))
	for i, f := range files.list {
		names[i] = f.path
		byName[f.path] = f
	}
	actual := canonical.SortLocale(names)
	if canonicalStrings(requiredDeclaredFiles(analysis)) != canonicalStrings(actual) {
		bundle.Throw(errors.New("Proof bundle writer did not produce the complete expected file set"))
	}
	lines := make([]string, len(actual))
	for i, name := range actual {
		lines[i] = jsexc.Concat(byName[name].hashBody(), "  ", name)
	}
	hashes := jsexc.Concat(jsexc.Join(lines, "\n"), "\n")
	rootDigest := jsexc.Concat("sha256:", canonical.SHA256Hex(hashes))
	publishStage(output, files.list, hashes, rootDigest)
	return Written{
		Directory:      output,
		ManifestDigest: bundle.Must(canonical.DigestJSON(manifest)),
		RootDigest:     rootDigest,
	}
}
