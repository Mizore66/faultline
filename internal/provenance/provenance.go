// Package provenance ports src/github-provenance.ts: the unsigned GitHub
// artifact provenance receipt derived from a verified Git proof bundle, its
// write-once storage, the offline binding check against the bundle and a
// trust configuration, and the `gh attestation verify` call that checks the
// receipt's signature.
package provenance

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/collation"
	"github.com/Mizore66/faultline/internal/jsdate"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/proofview"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// src/github-provenance.ts:9-12.
const (
	SchemaVersion      = "faultline.github-artifact-provenance.v1"
	TrustSchemaVersion = "faultline.github-artifact-attestation-trust.v1"
	Limitation         = "A GitHub artifact attestation can bind this receipt's exact bytes to a configured GitHub Actions workflow. It does not independently attest the runner, Docker daemon, wall-clock time, or host enforcement of the recorded sandbox policy."
)

// MAX_RECEIPT_BYTES (src/github-provenance.ts:17).
const maxReceiptBytes = 1_000_000

func str(v string) jsjson.Value { return jsjson.MakeString(v) }

// The refine of NonEmptyIdentifierSchema (src/github-provenance.ts:26):
// /[\u0000-\u001f\u007f]/ on the trimmed value. In WTF-8 these code points
// are exactly the bytes below 0x20 and 0x7f.
func noControlCharacters(v jsjson.Value) bool {
	for i := 0; i < len(v.Str()); i++ {
		if c := v.Str()[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

var (
	// src/github-provenance.ts:19-26. zod 3 runs a refinement unless the
	// string checks aborted; min, max, regex and datetime do not abort.
	digestSchema      = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "expected sha256:<64 lowercase hex characters>"))
	gitObjectIDSchema = s.String(s.Regex(s.Pattern(`^[0-9a-f]{40,64}$`), "expected a Git object id"))
	timestampSchema   = s.Refine(s.String(s.Datetime()),
		func(v jsjson.Value) bool { return jsdate.IsCanonicalISO(v.Str()) }, "expected a canonical UTC ISO-8601 timestamp")
	repositorySchema         = s.String(s.Regex(s.Pattern(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`), "expected an owner/repository identifier"))
	nonEmptyIdentifierSchema = s.Refine(s.String(s.Trim(), s.MinLength(1), s.MaxLength(1_024)),
		noControlCharacters, "must not contain control characters")

	// src/github-provenance.ts:28-32.
	gitEndpointSchema = s.Object(
		s.F("index", s.Number(s.Int(), s.Nonnegative())),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
	).Strict()

	// src/github-provenance.ts:34-38.
	sandboxBindingSchema = s.Object(
		s.F("image", s.String(s.Regex(s.Pattern(`@sha256:[a-f0-9]{64}$`), "expected a digest-pinned container image"))),
		s.F("policyDigest", digestSchema),
		s.F("environmentPolicyDigest", digestSchema),
	).Strict()

	// src/github-provenance.ts:40-50 GithubActionsIdentitySchema.
	identitySchema = s.Object(
		s.F("provider", s.LiteralString("GITHUB_ACTIONS")),
		s.F("repository", repositorySchema),
		s.F("workflowRef", nonEmptyIdentifierSchema),
		s.F("ref", nonEmptyIdentifierSchema),
		s.F("commit", gitObjectIDSchema),
		s.F("tree", gitObjectIDSchema),
		s.F("runId", s.String(s.Regex(s.Pattern(`^\d+$`), "expected a numeric GitHub run id"))),
		s.F("runAttempt", s.Number(s.Int(), s.Positive())),
		s.F("eventName", s.LiteralString("push")),
	).Strict()

	// src/github-provenance.ts:52-82 UnsignedGithubProvenanceReceiptSchema.
	unsignedReceiptSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("receiptKind", s.LiteralString("GITHUB_ARTIFACT_ATTESTATION_SUBJECT")),
		s.F("createdAt", timestampSchema),
		s.F("signing", s.Object(
			s.F("mechanism", s.LiteralString("GITHUB_ARTIFACT_ATTESTATION")),
			s.F("state", s.LiteralString("AWAITING_EXTERNAL_SIGNATURE")),
		).Strict()),
		s.F("proof", s.Object(
			s.F("rootDigest", digestSchema),
			s.F("manifestDigest", digestSchema),
			s.F("investigationDigest", digestSchema),
			s.F("frozenDigest", digestSchema),
			s.F("witnessDigest", digestSchema),
			s.F("approvalDigest", digestSchema),
			s.F("range", s.Object(
				s.F("ancestor", gitEndpointSchema),
				s.F("descendant", gitEndpointSchema),
			).Strict()),
			s.F("sandboxBindings", s.Array(sandboxBindingSchema, s.MinItems(1))),
			s.F("execution", s.Object(
				s.F("runIds", s.Array(digestSchema, s.MinItems(1))),
				s.F("executionIds", s.Array(digestSchema, s.MinItems(1))),
				s.F("startedAt", timestampSchema),
				s.F("finishedAt", timestampSchema),
				s.F("executionCount", s.Number(s.Int(), s.Positive())),
			).Strict()),
		).Strict()),
		s.F("ci", identitySchema),
		s.F("limitation", s.LiteralString(Limitation)),
	).Strict()

	// src/github-provenance.ts:84-86 GithubProvenanceReceiptSchema.
	receiptSchema = unsignedReceiptSchema.Extend(s.F("receiptDigest", digestSchema)).Strict()

	// src/github-provenance.ts:88-97 GithubArtifactAttestationTrustSchema.
	trustSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(TrustSchemaVersion)),
		s.F("repository", repositorySchema),
		s.F("signerWorkflow", nonEmptyIdentifierSchema),
		s.F("sourceRef", nonEmptyIdentifierSchema),
		s.F("eventName", s.LiteralString("push")),
		s.F("denySelfHostedRunners", s.Boolean()),
		s.F("trustedRootFile", s.String(s.Trim(), s.MinLength(1), s.MaxLength(4_096))),
		s.F("sourceDigest", s.Optional(gitObjectIDSchema)),
	).Strict()

	// The path split of ensureRealDirectoryTree (src/github-provenance.ts:269).
	pathSeparators = regexp.MustCompile(`[\\/]+`)
)

func throw(message string) { jsexc.Throw(errors.New(message)) }

// parse is zod Schema.parse: it throws the ZodError.
func parse(v jsjson.Value, schema s.Schema) jsjson.Value {
	return jsexc.Must(bundle.ParseValue(v, schema))
}

func stringList(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = str(item)
	}
	return jsjson.MakeArray(values)
}

// without is `const { key: _, ...rest } = v` for an object.
func without(v jsjson.Value, key string) jsjson.Value {
	out := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != key {
			out.Set(k, v.Obj().Field(k))
		}
	}
	return jsjson.MakeObject(out)
}

// Port of src/github-provenance.ts:133 receiptPayload.
func receiptPayload(receipt jsjson.Value) jsjson.Value {
	return parse(without(receipt, "receiptDigest"), unsignedReceiptSchema)
}

// Port of src/github-provenance.ts:138 canonicalTimestamp: nil is
// undefined, the one clock read (`new Date().toISOString()`).
func canonicalTimestamp(value *string) jsjson.Value {
	var timestamp string
	if value == nil {
		timestamp = clock.ISO(clock.Now())
	} else {
		timestamp = *value
	}
	return parse(str(timestamp), timestampSchema)
}

// sortLocale is `[...values].sort(cmp)` with cmp a localeCompare in V8's
// sort; descending is `(l, r) => r.localeCompare(l)`.
func sortLocale(values []string, descending bool) []string {
	if !descending {
		return canonical.SortLocale(values)
	}
	prepared := make([]collation.Prepared, len(values))
	for i, v := range values {
		prepared[i] = collation.Prepare(v)
	}
	v8sort.Sort(prepared, func(a, b collation.Prepared) int { return collation.ComparePrepared(b, a) })
	out := make([]string, len(prepared))
	for i, p := range prepared {
		out[i] = p.S
	}
	return out
}

// Port of src/github-provenance.ts:143 sortedUnique: a Set keeps the first
// occurrence of each value, in insertion order, before the sort.
func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			unique = append(unique, v)
		}
	}
	return sortLocale(unique, false)
}

// Port of src/github-provenance.ts:152 createGithubProvenanceReceiptFromView.
// createdAt nil is undefined (the clock is read).
func createFromView(view proofview.View, ciIdentity jsjson.Value, createdAt *string) jsjson.Value {
	ci := parse(ciIdentity, identitySchema)
	// The Map keyed by canonicalJson(binding): a repeated key keeps its
	// first position and takes the later (equal) value.
	var bindingKeys []string
	bindingValues := map[string]jsjson.Value{}
	var runIDs, executionIDs, startedAt, finishedAt []string

	for _, run := range view.Investigation.Get("runs").Items() {
		if run.Get("result", "executor").Str() != "NATIVE_DOCKER" || run.Get("sandbox", "kind").Str() != "DOCKER_ISOLATED" ||
			run.Get("sandbox", "runtime", "image").Kind() == jsjson.Null {
			throw("GitHub provenance requires a verified native-Docker proof bundle.")
		}
		binding := parse(jsjson.MustObject(
			"image", run.Get("sandbox", "runtime", "image"),
			"policyDigest", run.Get("sandbox", "policyDigest"),
			"environmentPolicyDigest", run.Get("sandbox", "environmentPolicyDigest"),
		), sandboxBindingSchema)
		key := jsexc.Must(canonical.CanonicalJSON(binding))
		if _, ok := bindingValues[key]; !ok {
			bindingKeys = append(bindingKeys, key)
		}
		bindingValues[key] = binding
		runIDs = append(runIDs, run.Get("runId").Str())
		executionIDs = append(executionIDs, run.Get("executionId").Str())
		startedAt = append(startedAt, run.Get("startedAt").Str())
		finishedAt = append(finishedAt, run.Get("finishedAt").Str())
	}

	if len(runIDs) == 0 {
		throw("GitHub provenance requires at least one recorded execution.")
	}
	// Each binding's canonicalJson is its Map key, so sorting the keys
	// makes the comparisons TS's sort makes.
	sortedKeys := sortLocale(bindingKeys, false)
	bindings := make([]jsjson.Value, len(sortedKeys))
	for i, k := range sortedKeys {
		bindings[i] = bindingValues[k]
	}

	created := canonicalTimestamp(createdAt)
	unsigned := jsjson.MustObject(
		"schemaVersion", str(SchemaVersion),
		"receiptKind", str("GITHUB_ARTIFACT_ATTESTATION_SUBJECT"),
		"createdAt", created,
		"signing", jsjson.MustObject(
			"mechanism", str("GITHUB_ARTIFACT_ATTESTATION"),
			"state", str("AWAITING_EXTERNAL_SIGNATURE"),
		),
		"proof", jsjson.MustObject(
			"rootDigest", str(view.RootDigest),
			"manifestDigest", str(jsexc.Must(canonical.DigestJSON(view.Manifest))),
			"investigationDigest", view.Manifest.Get("investigationDigest"),
			"frozenDigest", view.FrozenWitness.Get("frozenDigest"),
			"witnessDigest", view.FrozenWitness.Get("witnessDigest"),
			"approvalDigest", view.FrozenWitness.Get("approval", "approvalDigest"),
			"range", view.Manifest.Get("resolvedRange"),
			"sandboxBindings", jsjson.MakeArray(bindings),
			"execution", jsjson.MustObject(
				"runIds", stringList(sortedUnique(runIDs)),
				"executionIds", stringList(sortedUnique(executionIDs)),
				"startedAt", str(sortLocale(startedAt, false)[0]),
				"finishedAt", str(sortLocale(finishedAt, true)[0]),
				"executionCount", jsjson.MakeNumber(float64(len(runIDs))),
			),
		),
		"ci", ci,
		"limitation", str(Limitation),
	)
	payload := parse(unsigned, unsignedReceiptSchema)
	signed := jsjson.NewObj()
	for _, k := range payload.Obj().Keys() {
		signed.Set(k, payload.Obj().Field(k))
	}
	signed.Set("receiptDigest", str(jsexc.Must(canonical.DigestJSON(payload))))
	return parse(jsjson.MakeObject(signed), receiptSchema)
}

// loadView is loadVerifiedGitProofView(bundleDirectory) with no expected
// root and no attachment options; its throws propagate.
func loadView(bundleDirectory string) proofview.View {
	return jsexc.Must(proofview.LoadVerifiedGitProofView(bundleDirectory, nil))
}

// CreateReceipt ports createGithubProvenanceReceipt
// (src/github-provenance.ts:215) as the CLI calls it, without createdAt:
// the bundle is verified, then the identity parsed, then the clock read.
func CreateReceipt(bundleDirectory string, ci jsjson.Value) (receipt jsjson.Value, err error) {
	err = jsexc.Try(func() { receipt = createFromView(loadView(bundleDirectory), ci, nil) })
	return receipt, err
}

// CreateReceiptAt is createGithubProvenanceReceipt(bundleDirectory, ci,
// createdAt) with a createdAt string: no clock read.
func CreateReceiptAt(bundleDirectory string, ci jsjson.Value, createdAt string) (receipt jsjson.Value, err error) {
	err = jsexc.Try(func() { receipt = createFromView(loadView(bundleDirectory), ci, &createdAt) })
	return receipt, err
}

// Port of src/github-provenance.ts:223 requiredGithubEnvironment:
// process.env[name], refused when undefined, "" or blank, and trimmed.
func requiredGithubEnvironment(name string) string {
	value, _ := nodeproc.Getenv(name)
	if value == "" || jsstr.Trim(value) == "" {
		throw(jsexc.Concat("GitHub Actions provenance requires ", name, "."))
	}
	return jsstr.Trim(value)
}

// Port of src/github-provenance.ts:233 githubActionsIdentityFromEnvironment
// with process.env: the commit tree comes from `git rev-parse` in the
// workspace (the full inherited environment, no -c list), never from a
// label. The object literal's members are evaluated in order, so the
// remaining variables are read after the git call.
func identityFromEnvironment(workspace string) jsjson.Value {
	if v, ok := nodeproc.Getenv("GITHUB_ACTIONS"); !ok || v != "true" {
		throw("GitHub provenance receipts can only be created inside GitHub Actions; local metadata is not a signed CI receipt.")
	}
	commit := requiredGithubEnvironment("GITHUB_SHA")
	parse(str(commit), gitObjectIDSchema)
	stdout, err := nodeproc.ExecFileSync("git", []string{"rev-parse", jsexc.Concat(commit, "^{tree}")},
		nodeproc.SpawnSyncOptions{Cwd: nodefs.Resolve(workspace)})
	if err != nil {
		throw(jsexc.Concat("GitHub provenance could not resolve the checked-out commit tree: ", jsexc.Message(err)))
	}
	tree := jsstr.Trim(jsstr.DecodeUTF8(stdout))
	identity := jsjson.NewObj()
	identity.Set("provider", str("GITHUB_ACTIONS"))
	identity.Set("repository", str(requiredGithubEnvironment("GITHUB_REPOSITORY")))
	identity.Set("workflowRef", str(requiredGithubEnvironment("GITHUB_WORKFLOW_REF")))
	identity.Set("ref", str(requiredGithubEnvironment("GITHUB_REF")))
	identity.Set("commit", str(commit))
	identity.Set("tree", str(tree))
	identity.Set("runId", str(requiredGithubEnvironment("GITHUB_RUN_ID")))
	identity.Set("runAttempt", jsjson.MakeNumber(jsjson.ToNumber(requiredGithubEnvironment("GITHUB_RUN_ATTEMPT"))))
	identity.Set("eventName", str(requiredGithubEnvironment("GITHUB_EVENT_NAME")))
	return parse(jsjson.MakeObject(identity), identitySchema)
}

// IdentityFromEnvironment ports githubActionsIdentityFromEnvironment
// (src/github-provenance.ts:233) with process.env: the identity, or the
// thrown error (a ZodError for a schema failure).
func IdentityFromEnvironment(workspace string) (identity jsjson.Value, err error) {
	err = jsexc.Try(func() { identity = identityFromEnvironment(workspace) })
	return identity, err
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return resolved, ok
}

// Port of src/github-provenance.ts:266 ensureRealDirectoryTree: each
// existing component is checked with lstat, never followed; a missing one
// is created with mode 0700 after its parent was checked.
func ensureRealDirectoryTree(directory string) {
	absolute := nodefs.Resolve(directory)
	current := nodefs.ParseRoot(absolute)
	var parts []string
	if suffix := nodefs.Relative(current, absolute); suffix != "" {
		for _, p := range pathSeparators.Split(suffix, -1) {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	if nodefs.Exists(current) {
		safe, ok := safeSegment(current)
		if !ok {
			throw(jsexc.Concat("Provenance root must be a real directory: ", current))
		}
		current = safe
	}
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				jsexc.Throw(err)
			}
		}
		safe, ok := safeSegment(current)
		if !ok {
			throw(jsexc.Concat("Provenance output cannot traverse a symbolic link or non-directory: ", current))
		}
		current = safe
	}
}

// DefaultRoot ports defaultGithubProvenanceRoot (src/github-provenance.ts:285).
func DefaultRoot(workspace string) string {
	return nodefs.Resolve(workspace, ".faultline", "provenance")
}

// Port of src/github-provenance.ts:290 writeGithubProvenanceReceipt.
func writeReceipt(outputFile string, receipt jsjson.Value, provenanceRoot string) string {
	root := nodefs.Resolve(provenanceRoot)
	output := nodefs.Resolve(outputFile)
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(root, output))
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		throw(jsexc.Concat("GitHub provenance output must be a file beneath ", root))
	}
	ensureRealDirectoryTree(root)
	ensureRealDirectoryTree(nodefs.Dirname(output))
	if nodefs.Exists(output) {
		throw(jsexc.Concat("GitHub provenance receipt already exists and will not be replaced: ", output))
	}
	// Inside TS's try: a ZodError or a canonicalJson throw has no code and
	// is rethrown as is.
	body := jsexc.Concat(jsexc.Must(canonical.CanonicalJSON(parse(receipt, receiptSchema))), "\n")
	if err := nodefs.WriteFileUTF8(output, body, 0o600, true); err != nil {
		if nodefs.IsExist(err) {
			throw(jsexc.Concat("GitHub provenance receipt already exists and will not be replaced: ", output)) // coverage gate: race only
		}
		jsexc.Throw(err)
	}
	return output
}

// WriteReceipt ports writeGithubProvenanceReceipt
// (src/github-provenance.ts:290): the written path, or the thrown error.
func WriteReceipt(outputFile string, receipt jsjson.Value, root string) (path string, err error) {
	err = jsexc.Try(func() { path = writeReceipt(outputFile, receipt, root) })
	return path, err
}

// lstat is lstatSync, whose path validation refuses a NUL byte first.
func lstat(path string) (fs.FileInfo, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return nil, nodeproc.InvalidArgValue("The argument 'path'", path, "must be a string, Uint8Array, or URL without null bytes")
	}
	return nodefs.Lstat(path)
}

func regularFile(stat fs.FileInfo) bool {
	return stat.Mode()&os.ModeSymlink == 0 && stat.Mode().IsRegular()
}

// Port of src/github-provenance.ts:314 readRegularJson. The lstat error
// propagates; a read error and a SyntaxError are both "not valid JSON".
func readRegularJSON(path, label string) jsjson.Value {
	file := nodefs.Resolve(path)
	stat := jsexc.Must(lstat(file))
	if !regularFile(stat) {
		throw(jsexc.Concat(label, " must be a regular non-symlink file"))
	}
	if stat.Size() > maxReceiptBytes {
		throw(jsexc.Concat(label, " exceeds the ", strconv.Itoa(maxReceiptBytes), "-byte limit"))
	}
	text, err := nodefs.ReadText(file)
	var v jsjson.Value
	if err == nil {
		v, err = jsjson.Parse(text)
	}
	if err != nil {
		throw(jsexc.Concat(label, " is not valid JSON: ", jsexc.Message(err)))
	}
	return v
}

// Port of src/github-provenance.ts:326 readGithubProvenanceReceipt.
func readReceipt(path string) jsjson.Value {
	return parse(readRegularJSON(path, "GitHub provenance receipt"), receiptSchema)
}

// ReadTrust ports readGithubArtifactAttestationTrust
// (src/github-provenance.ts:330): the parsed trust configuration, or the
// thrown error.
func ReadTrust(path string) (trust jsjson.Value, err error) {
	err = jsexc.Try(func() {
		trust = parse(readRegularJSON(path, "GitHub attestation trust configuration"), trustSchema)
	})
	return trust, err
}

// Port of src/github-provenance.ts:334 trustErrors.
func trustErrors(receipt, trust jsjson.Value, errs *[]string) {
	ci := receipt.Get("ci")
	if ci.Get("repository").Str() != trust.Get("repository").Str() {
		*errs = append(*errs, "receipt repository is not permitted by the trust configuration")
	}
	if ci.Get("ref").Str() != trust.Get("sourceRef").Str() {
		*errs = append(*errs, "receipt ref is not permitted by the trust configuration")
	}
	// Both eventNames are z.literal("push"), so they always agree.
	if ci.Get("eventName").Str() != trust.Get("eventName").Str() {
		*errs = append(*errs, "receipt event is not permitted by the trust configuration")
	}
	if ci.Get("workflowRef").Str() != jsexc.Concat(trust.Get("signerWorkflow").Str(), "@", trust.Get("sourceRef").Str()) {
		*errs = append(*errs, "receipt workflow reference is not the configured signer workflow at the configured source ref")
	}
	if digest := trust.Get("sourceDigest"); digest.Kind() != jsjson.Undefined && ci.Get("commit").Str() != digest.Str() {
		*errs = append(*errs, "receipt source commit does not match the configured source digest")
	}
}

// bindingResult is a ProvenanceBindingVerification in TS key order: valid,
// errors, receiptDigest, proofRootDigest, then receipt when present. An
// empty digest is null; an Undefined receipt is no member.
func bindingResult(valid bool, errs []string, receiptDigest, proofRootDigest string, receipt jsjson.Value) jsjson.Value {
	nullable := func(v string) jsjson.Value {
		if v == "" {
			return jsjson.MakeNull()
		}
		return str(v)
	}
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(valid))
	o.Set("errors", stringList(errs))
	o.Set("receiptDigest", nullable(receiptDigest))
	o.Set("proofRootDigest", nullable(proofRootDigest))
	if receipt.Kind() != jsjson.Undefined {
		o.Set("receipt", receipt)
	}
	return jsjson.MakeObject(o)
}

// VerifyBinding ports verifyGithubProvenanceBinding
// (src/github-provenance.ts:351): both schemas first (both failures are
// reported), the digest check, then a re-derivation of every receipt field
// from the re-verified bundle and the trust constraints. A RangeError from
// a ZodError's message getter is thrown (jsexc), as in TS.
func VerifyBinding(bundleDirectory string, receiptInput, trustInput jsjson.Value) jsjson.Value {
	var errs []string
	receipt, receiptIssues, receiptOK := s.Parse(receiptSchema, receiptInput)
	trust, trustIssues, trustOK := s.Parse(trustSchema, trustInput)
	if !receiptOK {
		errs = append(errs, jsexc.Concat("GitHub provenance receipt schema validation failed: ", s.ErrorMessage(receiptIssues)))
	}
	if !trustOK {
		errs = append(errs, jsexc.Concat("GitHub attestation trust schema validation failed: ", s.ErrorMessage(trustIssues)))
	}
	if !receiptOK || !trustOK {
		return bindingResult(false, errs, "", "", jsjson.Value{})
	}
	calculated := jsexc.Must(canonical.DigestJSON(receiptPayload(receipt)))
	if receipt.Get("receiptDigest").Str() != calculated {
		errs = append(errs, "receiptDigest does not match the canonical provenance receipt contents")
	}
	var result jsjson.Value
	err := jsexc.Try(func() {
		view := loadView(bundleDirectory)
		createdAt := receipt.Get("createdAt").Str()
		expected := createFromView(view, receipt.Get("ci"), &createdAt)
		if jsexc.Must(canonical.CanonicalJSON(receiptPayload(receipt))) != jsexc.Must(canonical.CanonicalJSON(receiptPayload(expected))) {
			errs = append(errs, "receipt proof facts do not exactly match the verified Git proof bundle")
		}
		trustErrors(receipt, trust, &errs)
		result = bindingResult(len(errs) == 0, errs, calculated, view.RootDigest, receipt)
	})
	if err != nil {
		errs = append(errs, jsexc.Concat("GitHub provenance binding verification failed safely: ", jsexc.Message(err)))
		return bindingResult(false, errs, calculated, "", receipt)
	}
	return result
}

// Port of src/github-provenance.ts:388 assertRegularFile.
func assertRegularFile(path, label string) {
	stat := jsexc.Must(lstat(nodefs.Resolve(path)))
	if !regularFile(stat) {
		throw(jsexc.Concat(label, " must be a regular non-symlink file"))
	}
}

// attestationResult is a GithubArtifactAttestationVerification in TS key
// order: valid, command, errors.
func attestationResult(valid bool, command, errs []string) jsjson.Value {
	return jsjson.MustObject("valid", jsjson.MakeBool(valid), "command", stringList(command), "errors", stringList(errs))
}

// slice8000 is s.slice(0, 8000) in UTF-16 units (it can cut a surrogate
// pair, as slice does).
func slice8000(s string) string {
	if units := jsstr.ToUTF16(s); len(units) > 8_000 {
		return jsstr.FromUTF16(units[:8_000])
	}
	return s
}

// Port of src/github-provenance.ts:398 verifyGithubArtifactAttestation:
// spawnSync(executable, command, { encoding: "utf8", windowsHide: true })
// with the inherited environment, no timeout and no input.
func verifyArtifactAttestation(receiptFile, attestationBundleFile string, trustInput jsjson.Value, sourceCommit, executable string) jsjson.Value {
	command := []string{"attestation", "verify", nodefs.Resolve(receiptFile)}
	full := func() []string { return append([]string{executable}, command...) }
	var result jsjson.Value
	err := jsexc.Try(func() {
		trust := parse(trustInput, trustSchema)
		parse(str(sourceCommit), gitObjectIDSchema)
		assertRegularFile(receiptFile, "GitHub provenance receipt")
		assertRegularFile(attestationBundleFile, "GitHub artifact attestation bundle")
		trustedRoot := nodefs.Resolve(nodefs.Dirname(nodefs.Resolve(attestationBundleFile)), trust.Get("trustedRootFile").Str())
		assertRegularFile(trustedRoot, "GitHub trusted root")
		command = append(command,
			"--repo", trust.Get("repository").Str(),
			"--bundle", nodefs.Resolve(attestationBundleFile),
			"--custom-trusted-root", trustedRoot,
			"--signer-workflow", trust.Get("signerWorkflow").Str(),
			"--source-ref", trust.Get("sourceRef").Str(),
			"--source-digest", sourceCommit,
		)
		if trust.Get("denySelfHostedRunners").Bool() {
			command = append(command, "--deny-self-hosted-runners")
		}
		r := nodeproc.SpawnSyncWith(executable, command, nodeproc.SpawnSyncOptions{})
		if r.Err != nil {
			result = attestationResult(false, full(), []string{jsexc.Concat("Could not run ", executable, ": ", r.Err.Error())})
			return
		}
		if r.Status == nil || *r.Status != 0 {
			detail := slice8000(jsstr.Trim(jsexc.Concat(jsstr.DecodeUTF8(r.Stdout), "\n", jsstr.DecodeUTF8(r.Stderr))))
			message := "GitHub artifact attestation verification failed"
			if detail != "" {
				message = jsexc.Concat(message, ": ", detail)
			}
			result = attestationResult(false, full(), []string{message})
			return
		}
		result = attestationResult(true, full(), []string{})
	})
	if err != nil {
		return attestationResult(false, full(), []string{jsexc.Concat("GitHub artifact attestation verification failed safely: ", jsexc.Message(err))})
	}
	return result
}

// VerifyArtifactAttestation ports verifyGithubArtifactAttestation
// (src/github-provenance.ts:398) with the default executable, "gh".
func VerifyArtifactAttestation(receiptFile, attestationBundleFile string, trust jsjson.Value, sourceCommit string) jsjson.Value {
	return verifyArtifactAttestation(receiptFile, attestationBundleFile, trust, sourceCommit, "gh")
}

// signedResult is a SignedGithubProvenanceVerification in TS key order.
func signedResult(valid bool, errs jsjson.Value, binding, artifactAttestation jsjson.Value) jsjson.Value {
	assurance := "NOT_VERIFIED"
	if valid {
		assurance = "GITHUB_ARTIFACT_ATTESTATION_VERIFIED"
	}
	return jsjson.MustObject(
		"valid", jsjson.MakeBool(valid),
		"assurance", str(assurance),
		"errors", errs,
		"binding", binding,
		"artifactAttestation", artifactAttestation,
		"limitation", str(Limitation),
	)
}

// VerifySigned ports verifySignedGithubProvenance
// (src/github-provenance.ts:445) with the gh runner: the receipt read, its
// binding to the bundle, then gh's signature check. A RangeError from a
// ZodError's message getter is thrown (jsexc), as in TS.
func VerifySigned(bundleDirectory, receiptFile, attestationBundleFile string, trust jsjson.Value) jsjson.Value {
	var receipt jsjson.Value
	if err := jsexc.Try(func() { receipt = readReceipt(receiptFile) }); err != nil {
		errs := []string{jsexc.Concat("GitHub provenance receipt could not be read safely: ", jsexc.Message(err))}
		binding := bindingResult(false, errs, "", "", jsjson.Value{})
		return signedResult(false, stringList(errs), binding,
			attestationResult(false, []string{}, []string{"artifact attestation was not attempted"}))
	}
	binding := VerifyBinding(bundleDirectory, receipt, trust)
	if !binding.Get("valid").Bool() {
		return signedResult(false, binding.Get("errors"), binding,
			attestationResult(false, []string{}, []string{"artifact attestation was not attempted because receipt binding is invalid"}))
	}
	artifactAttestation := VerifyArtifactAttestation(receiptFile, attestationBundleFile, trust, receipt.Get("ci", "commit").Str())
	errs := append(append([]jsjson.Value{}, binding.Get("errors").Items()...), artifactAttestation.Get("errors").Items()...)
	valid := artifactAttestation.Get("valid").Bool()
	return signedResult(valid, jsjson.MakeArray(errs), binding, artifactAttestation)
}
