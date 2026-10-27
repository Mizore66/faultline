package repair

import (
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/repair-brief-store.ts:28 REPAIR_BRIEF_ARTIFACT_VERSION.
const BriefArtifactVersion = "faultline.repair-brief-artifact.v1"

// Port of src/repair-brief-store.ts:32-52.
var (
	briefArtifactSourceSchema = s.Object(
		s.F("kind", s.Enum("GPT-5.6", "OFFLINE_INPUT")),
		s.F("model", s.Optional(s.String(s.MinLength(1), s.MaxLength(256)))),
	).Strict()

	// BriefArtifactManifestSchema ports RepairBriefArtifactManifestSchema.
	BriefArtifactManifestSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(BriefArtifactVersion)),
		s.F("classification", s.LiteralString("INFERRED")),
		s.F("source", briefArtifactSourceSchema),
		s.F("evidencePacket", s.Object(
			s.F("path", s.LiteralString("evidence-packet.json")),
			s.F("digest", digestSchema),
		).Strict()),
		s.F("repairBrief", s.Object(
			s.F("path", s.LiteralString("repair-brief.json")),
			s.F("digest", digestSchema),
		).Strict()),
		s.F("limitations", s.Array(s.String(s.MinLength(1)), s.MinItems(1), s.MaxItems(16))),
		s.F("manifestDigest", digestSchema),
	).Strict()
)

// Port of src/repair-brief-store.ts:71 EXPECTED_ARTIFACTS (insertion order).
var expectedArtifacts = []string{"README.md", "evidence-packet.json", "repair-brief.json", "manifest.json"}

// Port of src/repair-brief-store.ts:287-288 (the manifest limitations).
var briefArtifactLimitations = []string{
	"INFERRED guidance is not an executed verdict, proof of model intent, unique semantic cause, or identified culprit.",
	"This package retains only a privacy-minimized evidence packet and citation-validated recommendations; it contains no source text, commands, overlays, raw logs, credentials, or repository paths.",
}

// Source is RepairBriefArtifactSource: kind and an optional model.
type Source struct {
	Kind     string
	Model    string
	HasModel bool
}

func (src Source) value() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("kind", str(src.Kind))
	if src.HasModel {
		o.Set("model", str(src.Model))
	}
	return jsjson.MakeObject(o)
}

// WrittenBrief is WrittenRepairBriefArtifact; Manifest is the parsed one.
type WrittenBrief struct {
	Directory string
	Manifest  jsjson.Value
}

// JSON is the TS object { directory, manifest }.
func (w WrittenBrief) JSON() jsjson.Value {
	return jsjson.MustObject("directory", str(w.Directory), "manifest", w.Manifest)
}

// BriefVerification is RepairBriefArtifactVerification. An Undefined
// Manifest is TS null.
type BriefVerification struct {
	Valid                bool
	Errors               []string
	Manifest             jsjson.Value
	ExternalDigestStatus string
}

// JSON is the TS object { valid, errors, manifest, externalDigestStatus }.
func (v BriefVerification) JSON() jsjson.Value {
	errs := make([]jsjson.Value, len(v.Errors))
	for i, e := range v.Errors {
		errs[i] = str(e)
	}
	manifest := v.Manifest
	if manifest.Kind() == jsjson.Undefined {
		manifest = jsjson.MakeNull()
	}
	return jsjson.MustObject("valid", jsjson.MakeBool(v.Valid), "errors", jsjson.MakeArray(errs),
		"manifest", manifest, "externalDigestStatus", str(v.ExternalDigestStatus))
}

// DefaultBriefRoot ports src/repair-brief-store.ts:88 defaultRepairBriefRoot:
// resolve() reads the process working directory (and throws its uv_cwd
// error).
func DefaultBriefRoot() string { return nodefs.Resolve(".faultline", "repair-briefs") }

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

// Port of src/repair-brief-store.ts:92 assertNoLinksOrSpecialFiles.
func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New(jsexc.Concat("Repair brief directory must be a real directory: ", directory)))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New(jsexc.Concat("Repair brief directory contains a symbolic link or special file: ", child)))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
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

// Port of src/repair-brief-store.ts:108 ensureRealDirectoryTree (the
// prevention writer's clone, prevention/write.go, with the repair texts).
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
			bundle.Throw(errors.New(jsexc.Concat("Repair brief root is not a real directory: ", current)))
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
			bundle.Throw(errors.New(jsexc.Concat("Repair brief output cannot traverse a symbolic link or non-directory: ", current)))
		}
		current = safe
	}
}

// Port of src/repair-brief-store.ts:130 isNested.
func isNested(root, candidate string) bool {
	nested := bundle.Must(safedir.RelativeTrustedSystemPath(root, candidate))
	return nested != "" && !strings.HasPrefix(nested, "..") && !nodefs.IsAbsolute(nested)
}

// Port of src/repair-brief-store.ts:139 prepareRepairBriefOutput.
func prepareOutput(outputDirectory, repairRoot string) string {
	root := nodefs.Resolve(repairRoot)
	output := nodefs.Resolve(outputDirectory)
	if !isNested(root, output) {
		bundle.Throw(errors.New(jsexc.Concat("Repair brief output must be a child directory of ", root)))
	}
	ensureRealDirectoryTree(root)
	ensureRealDirectoryTree(nodefs.Dirname(output))
	if nodefs.Exists(output) {
		bundle.Throw(errors.New(jsexc.Concat("Repair brief output already exists and will not be replaced: ", output)))
	}
	return output
}

// writePrivateFile ports src/repair-brief-store.ts:153: open "wx" 0600,
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

// Port of src/repair-brief-store.ts:180 readJson: Undefined on failure.
func readJSON(path, label string, errs *[]string) jsjson.Value {
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		*errs = append(*errs, jsexc.Concat("Unable to read ", label, ": ", jsexc.Message(err)))
		return jsjson.Value{}
	}
	return v
}

// Port of src/repair-brief-store.ts:189 isLegacyRepairEvidencePacket.
func isLegacyEvidencePacket(v jsjson.Value) bool {
	if v.Kind() != jsjson.Object {
		return false
	}
	version := v.Get("schemaVersion")
	return version.Kind() == jsjson.String && version.Str() == "faultline.repair-evidence.v1"
}

// VerifyBriefArtifact ports src/repair-brief-store.ts:195
// verifyRepairBriefArtifact. A nil expected is TS undefined.
//
// The zod messages read here (and in ValidateBrief) use SiteGitProof: the
// last depths that print through `node dist/cli.js repair verify` (Node
// 22.22.2; manifest, packet and brief alike) equal the git row of
// jsjson's stack table on darwin/arm64 (2610/6088/2490/3652) and
// linux/arm64 (1827/3652/1956/2434).
func VerifyBriefArtifact(directory string, expected *string) (result BriefVerification) {
	defer jsjson.UseCallSite(jsjson.SiteGitProof)()
	var errs []string
	var manifest jsjson.Value
	status := "NOT_PROVIDED"
	if expected != nil {
		status = "MISMATCH"
	}
	finish := func() BriefVerification {
		return BriefVerification{Valid: len(errs) == 0, Errors: errs, Manifest: manifest, ExternalDigestStatus: status}
	}
	defer bundle.Catch(func(err error) {
		errs = append(errs, jsexc.Concat("Repair brief package verification failed safely: ", jsexc.Message(err)))
		result = finish()
	})

	root := nodefs.Resolve(directory)
	if !nodefs.Exists(root) {
		return BriefVerification{Errors: []string{"Repair brief directory does not exist"}, ExternalDigestStatus: status}
	}
	assertNoLinksOrSpecialFiles(root)
	// new Set(readdirSync(...).map(name)): decoded names can collide, and a
	// Set keeps the first occurrence only.
	var physical []string
	seen := map[string]bool{}
	for _, name := range bundle.Must(nodefs.ReadDirNames(root)) {
		if !seen[name] {
			jsexc.SetAdd(seen, name)
			physical = append(physical, name)
		}
	}
	for _, want := range expectedArtifacts {
		if !slices.Contains(physical, want) {
			errs = append(errs, jsexc.Concat("Repair brief package is missing ", want))
		}
	}
	for _, actual := range physical {
		if !slices.Contains(expectedArtifacts, actual) {
			errs = append(errs, jsexc.Concat("Repair brief package contains an unexpected artifact: ", actual))
		}
		stat := bundle.Must(nodefs.Lstat(nodefs.Join(root, actual)))
		if !stat.Mode().IsRegular() || isLink(stat) {
			errs = append(errs, jsexc.Concat("Repair brief artifact must be a regular non-symlink file: ", actual))
		}
	}

	if out, issues, ok := s.Parse(BriefArtifactManifestSchema, readJSON(nodefs.Join(root, "manifest.json"), "repair brief manifest", &errs)); !ok {
		errs = append(errs, jsexc.Concat("Repair brief manifest schema validation failed: ", s.ErrorMessage(issues)))
	} else {
		manifest = out
		digest := manifest.Get("manifestDigest").Str()
		if digest != bundle.Must(canonical.DigestJSON(bundle.Without(manifest, "manifestDigest"))) {
			errs = append(errs, "Repair brief manifest digest does not match its canonical contents.")
		}
		if expected != nil {
			if digest == *expected {
				status = "MATCH"
			} else {
				status = "MISMATCH"
				errs = append(errs, "Repair brief manifest digest does not match the externally supplied digest.")
			}
		}
	}

	packetValue := readJSON(nodefs.Join(root, "evidence-packet.json"), "repair evidence packet", &errs)
	packet, issues, packetOK := s.Parse(EvidencePacketSchema, packetValue)
	if !packetOK {
		if isLegacyEvidencePacket(packetValue) {
			errs = append(errs, "Repair evidence packet uses legacy faultline.repair-evidence.v1 witness binding; recreate the repair artifact with the current FaultLine version.")
		} else {
			errs = append(errs, jsexc.Concat("Repair evidence packet schema validation failed: ", s.ErrorMessage(issues)))
		}
	}
	brief, issues, briefOK := s.Parse(BriefSchema, readJSON(nodefs.Join(root, "repair-brief.json"), "repair brief", &errs))
	if !briefOK {
		errs = append(errs, jsexc.Concat("Repair brief schema validation failed: ", s.ErrorMessage(issues)))
	}

	if manifest.Kind() != jsjson.Undefined && packetOK && briefOK {
		if manifest.Get("evidencePacket", "digest").Str() != packet.Get("packetDigest").Str() {
			errs = append(errs, "Repair brief manifest evidence packet digest does not match evidence-packet.json.")
		}
		if manifest.Get("repairBrief", "digest").Str() != bundle.Must(canonical.DigestJSON(brief)) {
			errs = append(errs, "Repair brief manifest digest does not match repair-brief.json.")
		}
		if validation := ValidateBrief(packet, brief); !validation.Valid {
			for _, e := range validation.Errors {
				errs = append(errs, jsexc.Concat("Stored repair brief is invalid: ", e))
			}
		}
	}
	return finish()
}

// readmeText is the README (src/repair-brief-store.ts:298-306) plus "\n".
func readmeText() string {
	return strings.Join([]string{
		"# FaultLine repair brief",
		"",
		"Status: **INFERRED**",
		"",
		"This is evidence-bounded repair guidance, not an executed verdict, proof of model intent, a unique semantic cause, or an identified culprit.",
		"",
		"The evidence packet and every recommendation are stored as canonical JSON and are citation-validated before publication.",
	}, "\n") + "\n"
}

// WriteBriefArtifact ports src/repair-brief-store.ts:265
// writeRepairBriefArtifact into the default root under the process working
// directory. A thrown TS error is returned (a ZodError as *schema.Error,
// whose message is read by the caller).
func WriteBriefArtifact(output string, packet, brief jsjson.Value, source Source) (written WrittenBrief, err error) {
	err = bundle.Try(func() { written = writeBriefArtifact(output, packet, brief, source) })
	return written, err
}

func writeBriefArtifact(outputDirectory string, packetInput, briefInput jsjson.Value, source Source) WrittenBrief {
	packet := bundle.Must(bundle.ParseValue(packetInput, EvidencePacketSchema))
	validation := ValidateBrief(packet, briefInput)
	if !validation.Valid || validation.Brief.Kind() == jsjson.Undefined {
		bundle.Throw(errors.New(jsexc.Concat("Refusing to store an invalid inferred repair brief: ", jsexc.Join(validation.Errors, "; "))))
	}
	brief := validation.Brief
	parsedSource := bundle.Must(bundle.ParseValue(source.value(), briefArtifactSourceSchema))
	output := prepareOutput(outputDirectory, DefaultBriefRoot())
	parent := nodefs.Dirname(output)
	stage := nodefs.Join(parent, jsexc.Concat(".", lastPathSegment(output), ".", clock.NewUUID(), ".tmp"))

	limitations := make([]jsjson.Value, len(briefArtifactLimitations))
	for i, l := range briefArtifactLimitations {
		limitations[i] = str(l)
	}
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", str(BriefArtifactVersion))
	unsigned.Set("classification", str("INFERRED"))
	unsigned.Set("source", parsedSource)
	unsigned.Set("evidencePacket", jsjson.MustObject("path", str("evidence-packet.json"), "digest", packet.Get("packetDigest")))
	unsigned.Set("repairBrief", jsjson.MustObject("path", str("repair-brief.json"), "digest", str(bundle.Must(canonical.DigestJSON(brief)))))
	unsigned.Set("limitations", jsjson.MakeArray(limitations))
	signed := jsjson.NewObj()
	for _, k := range unsigned.Keys() {
		signed.Set(k, unsigned.Field(k))
	}
	signed.Set("manifestDigest", str(bundle.Must(canonical.DigestJSON(jsjson.MakeObject(unsigned)))))
	manifest := bundle.Must(bundle.ParseValue(jsjson.MakeObject(signed), BriefArtifactManifestSchema))

	var written WrittenBrief
	err := bundle.Try(func() {
		if err := nodefs.Mkdir(stage, 0o700); err != nil {
			bundle.Throw(err)
		}
		writePrivateFile(nodefs.Join(stage, "README.md"), readmeText())
		writePrivateFile(nodefs.Join(stage, "evidence-packet.json"), canonicalLine(packet))
		writePrivateFile(nodefs.Join(stage, "repair-brief.json"), canonicalLine(brief))
		writePrivateFile(nodefs.Join(stage, "manifest.json"), canonicalLine(manifest))
		nodefs.SyncDir(stage)

		if v := VerifyBriefArtifact(stage, nil); !v.Valid {
			bundle.Throw(errors.New(jsexc.Concat("Refusing to publish an invalid repair brief package: ", jsexc.Join(v.Errors, "; "))))
		}
		if nodefs.Exists(output) {
			bundle.Throw(errors.New(jsexc.Concat("Repair brief output appeared during assembly and will not be replaced: ", output))) // coverage gate: race only
		}
		if err := nodefs.Rename(stage, output); err != nil {
			bundle.Throw(err)
		}
		nodefs.SyncDir(parent)
		written = WrittenBrief{Directory: output, Manifest: manifest}
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
