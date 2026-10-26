package prevention

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// Written is WrittenPreventionProof (src/prevention-proof.ts:120): Body is
// its `prevention` member (the zod-parsed body), Manifest the parsed
// manifest.
type Written struct {
	Directory  string
	Manifest   jsjson.Value
	Body       jsjson.Value
	RootDigest string
}

// DefaultRoot ports defaultPreventionProofRoot (src/prevention-proof.ts:139)
// for a caller that has already read process.cwd().
func DefaultRoot(cwd string) string { return nodefs.Resolve(cwd, ".faultline", "prevention-proofs") }

// defaultRoot is defaultPreventionProofRoot() itself: resolve() reads the
// process working directory (and throws its uv_cwd error).
func defaultRoot() string { return nodefs.Resolve(".faultline", "prevention-proofs") }

const repairedRunsSchemaVersion = "faultline.prevention-repaired-runs.v1"

// The fixed limitation strings (src/prevention-proof.ts:411-421).
var (
	verifiedLimitations = []string{
		"PREVENTION_VERIFIED binds last-good and first-bad run IDs from a verified Git proof bundle plus three repaired-state NATIVE_DOCKER run bindings under one frozen witness digest.",
		"Offline verify checks package integrity, run-ID self-consistency, and repaired-runs.json; it does not re-execute Docker.",
		"This package does not claim model intent, a unique semantic root cause, or host/Docker-daemon attestation beyond the recorded execution trust.",
	}
	summaryLimitations = []string{
		"PREVENTION_EVIDENCE_SUMMARY records caller-supplied last-good PASS, first-bad FAIL, and repaired PASS fields under one frozen witness digest.",
		"Offline verify checks package integrity and internal field consistency; use fl prevention write --from-bundle for PREVENTION_VERIFIED grounding.",
		"This package does not claim model intent, a unique semantic root cause, or host/Docker-daemon attestation beyond the recorded execution trust.",
	}
)

func stringValues(values []string) jsjson.Value {
	out := make([]jsjson.Value, len(values))
	for i, v := range values {
		out[i] = jsjson.MakeString(v)
	}
	return jsjson.MakeArray(out)
}

// splitPathParts is suffix.split(/[\\/]+/).filter(Boolean).
func splitPathParts(suffix string) []string {
	return strings.FieldsFunc(suffix, func(r rune) bool { return r == '/' || r == '\\' })
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		bundle.Throw(err)
	}
	return resolved, ok
}

// Port of src/prevention-proof.ts:158 ensureRealDirectoryTree (the Git
// writer's clone, gitproof/write.go, with the prevention texts).
func ensureRealDirectoryTree(directory string) {
	absolute := nodefs.Resolve(directory)
	root := nodefs.ParseRoot(absolute)
	var parts []string
	if suffix := nodefs.Relative(root, absolute); suffix != "" {
		parts = splitPathParts(suffix)
	}
	current := root
	if nodefs.Exists(current) {
		safe, ok := safeSegment(current)
		if !ok {
			bundle.Throw(errors.New(jsexc.Concat("Prevention proof root is not a real directory: ", current)))
		}
		current = safe
	}
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				bundle.Throw(err)
			}
		}
		safe, ok := safeSegment(current)
		if !ok {
			bundle.Throw(errors.New(jsexc.Concat("Prevention proof output cannot traverse a symbolic link or non-directory: ", current)))
		}
		current = safe
	}
}

// Port of src/prevention-proof.ts:180 isNested.
func isNested(root, candidate string) bool {
	nested := bundle.Must(safedir.RelativeTrustedSystemPath(root, candidate))
	return nested != "" && !strings.HasPrefix(nested, "..") && !nodefs.IsAbsolute(nested)
}

// Port of src/prevention-proof.ts:185 preparePreventionProofOutput.
func prepareOutput(outputDirectory, preventionRoot string) string {
	root := nodefs.Resolve(preventionRoot)
	output := nodefs.Resolve(outputDirectory)
	if !isNested(root, output) {
		bundle.Throw(errors.New(jsexc.Concat("Prevention proof output must be a child directory of ", root)))
	}
	ensureRealDirectoryTree(root)
	ensureRealDirectoryTree(nodefs.Dirname(output))
	if nodefs.Exists(output) {
		bundle.Throw(errors.New(jsexc.Concat("Prevention proof output already exists and will not be replaced: ", output)))
	}
	return output
}

// writePrivateFile ports src/prevention-proof.ts:199: open "wx" 0600,
// write, fsync, close.
func writePrivateFile(path, body string) {
	if err := nodefs.WriteFileSyncUTF8(path, body, 0o600); err != nil {
		bundle.Throw(err)
	}
}

// canonicalLine is `${canonicalJson(value)}\n`.
func canonicalLine(v jsjson.Value) string {
	return jsexc.Concat(bundle.Must(canonical.CanonicalJSON(v)), "\n")
}

// lastPathSegment is output.split(/[\\/]/).at(-1): both separators on every
// platform.
func lastPathSegment(p string) string { return p[strings.LastIndexAny(p, `/\`)+1:] }

// readmeText is the README (src/prevention-proof.ts:485-497) plus "\n".
func readmeText(verified bool) string {
	statusLine := "Status: **PREVENTION_EVIDENCE_SUMMARY** (not fully grounded Prevention verified)"
	if verified {
		statusLine = "Status: **Prevention verified** (grounded in verified proof-bundle run IDs + repaired run bindings)"
	}
	return strings.Join([]string{
		"# FaultLine prevention proof",
		"",
		statusLine,
		"",
		"This package binds an original Git proof root, a frozen witness digest, and three NATIVE_DOCKER state summaries (PASS → FAIL → PASS) with per-state run IDs.",
		"",
		"Verify offline with `fl verify <this-directory> --expect-root <retained-root>` (or `fl prevention verify`).",
		"It does not claim model intent or a unique semantic root cause.",
	}, "\n") + "\n"
}

// stateWith is `{ ...input.<state>, role, verdict }`.
func stateWith(state jsjson.Value, role, verdict string) jsjson.Value {
	o := spreadObject(state)
	o.Set("role", jsjson.MakeString(role))
	o.Set("verdict", jsjson.MakeString(verdict))
	return jsjson.MakeObject(o)
}

// Write ports writePreventionProof (src/prevention-proof.ts:398) into the
// default root under the process working directory. A thrown TS error is
// returned.
func Write(input jsjson.Value, output string) (written Written, err error) {
	err = bundle.Try(func() { written = write(output, input) })
	return written, err
}

func write(outputDirectory string, input jsjson.Value) Written {
	// 1. Grounded checks. input.grounding throws on null (JSON input
	// cannot give undefined).
	field := func(name string) jsjson.Value {
		if input.Kind() == jsjson.Null {
			bundle.Throw(errors.New(jsexc.Concat("Cannot read properties of null (reading '", name, "')")))
		}
		return input.Get(name)
	}
	grounding := field("grounding")
	verified := grounding.Kind() == jsjson.String && grounding.Str() == "VERIFIED"
	if verified {
		if !truthy(field("repairPatchDigest")) {
			bundle.Throw(errors.New("Grounded prevention write requires repairPatchDigest."))
		}
		if !truthy(field("repairBaseTree")) {
			bundle.Throw(errors.New("Grounded prevention write requires repairBaseTree."))
		}
		if bindings := field("repairedRunBindings"); !truthy(bindings) || !strictEqualsNumber(jsLength(bindings), 3) {
			bundle.Throw(errors.New("Grounded prevention write requires three repairedRunBindings."))
		}
	}

	// 2. Fixed limitation strings.
	limitations := summaryLimitations
	classification := "PREVENTION_EVIDENCE_SUMMARY"
	if verified {
		limitations, classification = verifiedLimitations, "PREVENTION_VERIFIED"
	}

	// 3. PreventionProofBodySchema.parse of the literal, in its evaluation
	// order (the hard-guard spread throws on a non-iterable).
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(SchemaVersion))
	o.Set("originalProofRoot", input.Get("originalProofRoot"))
	o.Set("frozenWitnessDigest", input.Get("frozenWitnessDigest"))
	if v := input.Get("investigationDigest"); v.Kind() != jsjson.Undefined {
		o.Set("investigationDigest", v)
	}
	o.Set("lastGood", stateWith(input.Get("lastGood"), "LAST_GOOD", "PASS"))
	o.Set("firstBad", stateWith(input.Get("firstBad"), "FIRST_BAD", "FAIL"))
	o.Set("repaired", stateWith(input.Get("repaired"), "REPAIRED", "PASS"))
	for _, k := range []string{"repairBaseTree", "repairPatchDigest", "codexThreadId"} {
		if v := input.Get(k); v.Kind() != jsjson.Undefined {
			o.Set(k, v)
		}
	}
	if v := input.Get("hardGuardArtifactDigests"); v.Kind() != jsjson.Undefined {
		o.Set("hardGuardArtifactDigests", spreadArray(v, "input.hardGuardArtifactDigests"))
	}
	o.Set("verified", jsonTrue)
	o.Set("limitations", stringValues(limitations))
	body := bundle.Must(bundle.ParseValue(jsjson.MakeObject(o), BodySchema))

	// 4. Semantics.
	if semanticErrors := validateSemantics(body); len(semanticErrors) > 0 {
		bundle.Throw(errors.New(jsexc.Concat("Refusing to store an invalid prevention proof: ", strings.Join(semanticErrors, "; "))))
	}

	// 5. Repaired run bindings (written whenever supplied, SUMMARY too).
	var repaired *jsjson.Value
	if bindings := input.Get("repairedRunBindings"); bindings.Kind() != jsjson.Undefined {
		a := jsjson.NewObj()
		a.Set("schemaVersion", jsjson.MakeString(repairedRunsSchemaVersion))
		a.Set("runs", bindings)
		parsed := bundle.Must(bundle.ParseValue(jsjson.MakeObject(a), RepairedRunsSchema))
		repaired = &parsed
		if bindingErrors := validateRepairedRuns(body, parsed); len(bindingErrors) > 0 {
			bundle.Throw(errors.New(jsexc.Concat("Refusing to store inconsistent repaired run bindings: ", strings.Join(bindingErrors, "; "))))
		}
	}

	// 6-8. Output, stage name, digests and manifest.
	output := prepareOutput(outputDirectory, defaultRoot())
	parent := nodefs.Dirname(output)
	stage := nodefs.Join(parent, jsexc.Concat(".", lastPathSegment(output), ".", clock.NewUUID(), ".tmp"))
	unsigned := unsignedManifest(body, classification)
	signed := jsjson.NewObj()
	for _, k := range unsigned.Obj().Keys() {
		signed.Set(k, unsigned.Obj().Field(k))
	}
	signed.Set("rootDigest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(unsigned))))
	manifest := bundle.Must(bundle.ParseValue(jsjson.MakeObject(signed), ManifestSchema))

	// 9. Assemble, verify and publish the stage; 10. remove it on failure.
	var written Written
	err := bundle.Try(func() {
		if err := nodefs.Mkdir(stage, 0o700); err != nil {
			bundle.Throw(err)
		}
		writePrivateFile(nodefs.Join(stage, "README.md"), readmeText(verified))
		writePrivateFile(nodefs.Join(stage, "prevention.json"), canonicalLine(body))
		writePrivateFile(nodefs.Join(stage, "manifest.json"), canonicalLine(manifest))
		if repaired != nil {
			writePrivateFile(nodefs.Join(stage, "repaired-runs.json"), canonicalLine(*repaired))
		}
		nodefs.SyncDir(stage)

		if v := Verify(stage, "", false); !v.Valid {
			bundle.Throw(errors.New(jsexc.Concat("Refusing to publish an invalid prevention proof: ", strings.Join(v.Errors, "; "))))
		}
		if nodefs.Exists(output) {
			bundle.Throw(errors.New(jsexc.Concat("Prevention proof output appeared during assembly and will not be replaced: ", output))) // coverage gate: race only
		}
		if err := nodefs.Rename(stage, output); err != nil {
			bundle.Throw(err)
		}
		nodefs.SyncDir(parent)
		written = Written{Directory: output, Manifest: manifest, Body: body, RootDigest: manifest.Get("rootDigest").Str()}
	})
	if err != nil {
		if nodefs.Exists(stage) {
			if rmErr := nodefs.RemoveAll(stage); rmErr != nil {
				bundle.Throw(rmErr)
			}
		}
		bundle.Throw(err)
	}
	return written
}

// unsignedManifest is the writer's `unsigned` manifest
// (src/prevention-proof.ts:472).
func unsignedManifest(body jsjson.Value, classification string) jsjson.Value {
	preventionRef := jsjson.NewObj()
	preventionRef.Set("path", jsjson.MakeString("prevention.json"))
	preventionRef.Set("digest", jsjson.MakeString(bundle.Must(canonical.DigestJSON(body))))
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(SchemaVersion))
	unsigned.Set("classification", jsjson.MakeString(classification))
	unsigned.Set("prevention", jsjson.MakeObject(preventionRef))
	unsigned.Set("limitations", body.Get("limitations"))
	return jsjson.MakeObject(unsigned)
}

// truthy is JS ToBoolean of a JSON value.
func truthy(v jsjson.Value) bool {
	switch v.Kind() {
	case jsjson.Bool:
		return v.Bool()
	case jsjson.Number:
		return v.Num() != 0 && v.Num() == v.Num()
	case jsjson.String:
		return v.Str() != ""
	case jsjson.Array, jsjson.Object:
		return true
	}
	return false
}

// jsLength is `v.length` of a non-nullish JSON value: a string's UTF-16
// length, an array's length, an object's own "length" member, or undefined.
func jsLength(v jsjson.Value) jsjson.Value {
	switch v.Kind() {
	case jsjson.String:
		return jsjson.MakeNumber(float64(jsstr.UTF16Len(v.Str())))
	case jsjson.Array:
		return jsjson.MakeNumber(float64(len(v.Items())))
	case jsjson.Object:
		return v.Obj().Field("length")
	}
	return jsjson.Value{}
}

// strictEqualsNumber is `v === n`.
func strictEqualsNumber(v jsjson.Value, n float64) bool {
	return v.Kind() == jsjson.Number && v.Num() == n
}

// spreadObject is `{ ...v }` of a JSON value: an object's own members, an
// array's indices, a string's UTF-16 code units; nothing for the rest.
func spreadObject(v jsjson.Value) *jsjson.Obj {
	o := jsjson.NewObj()
	switch v.Kind() {
	case jsjson.Object:
		for _, k := range v.Obj().Keys() {
			o.Set(k, v.Obj().Field(k))
		}
	case jsjson.Array:
		for i, item := range v.Items() {
			o.Set(jsjson.FormatNumber(float64(i)), item)
		}
	case jsjson.String:
		for i, u := range jsstr.ToUTF16(v.Str()) {
			o.Set(jsjson.FormatNumber(float64(i)), jsjson.MakeString(jsstr.FromUTF16([]uint16{u})))
		}
	}
	return o
}

// spreadArray is `[...v]`: an array's items, a string's code points (a lone
// surrogate on its own), or the TypeError V8 throws for a non-iterable,
// named by its source text.
func spreadArray(v jsjson.Value, source string) jsjson.Value {
	switch v.Kind() {
	case jsjson.Array:
		return jsjson.MakeArray(append([]jsjson.Value(nil), v.Items()...))
	case jsjson.String:
		units := jsstr.ToUTF16(v.Str())
		var out []jsjson.Value
		for i := 0; i < len(units); i++ {
			n := 1
			if units[i] >= 0xD800 && units[i] <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				n = 2
			}
			out = append(out, jsjson.MakeString(jsstr.FromUTF16(units[i:i+n])))
			i += n - 1
		}
		return jsjson.MakeArray(out)
	}
	bundle.Throw(errors.New(jsexc.Concat(source, " is not iterable")))
	return jsjson.Value{}
}
