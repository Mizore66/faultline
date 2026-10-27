// Package attest ports src/attestation.ts: the separately stored,
// write-once bundle integrity receipt. A receipt is deliberately not a
// signature; it gives a reviewer a small record to compare with an
// externally retained receipt digest.
package attest

import (
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsdate"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
)

// src/attestation.ts:25-26.
const (
	SchemaVersion = "faultline.bundle-attestation.v1"
	Limitation    = "Integrity attestation only; not a cryptographic signature, proof of authorship, identity verification, provenance verification, or ownership claim."
)

func str(v string) jsjson.Value { return jsjson.MakeString(v) }

// Port of the IdentifierSchema refines (src/attestation.ts:37-38).
func untrimmed(v jsjson.Value) bool { return v.Str() == jsstr.Trim(v.Str()) }

func noControlCharacters(v jsjson.Value) bool {
	for i := 0; i < len(v.Str()); i++ {
		if c := v.Str()[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

var (
	// src/attestation.ts:28-42. zod 3 runs a refinement unless the string
	// checks aborted, and min and max do not abort, so both refines of
	// IdentifierSchema and the refine of TimestampSchema run after a failed
	// length or regex check too.
	digestSchema     = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "must be a sha256:<64-lowercase-hex> digest"))
	receiptIDSchema  = s.String(s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`), "must use only letters, numbers, underscores, or hyphens and cannot be a path"))
	identifierSchema = s.Refine(s.Refine(s.String(s.MinLength(1), s.MaxLength(512)),
		untrimmed, "cannot start or end with whitespace"),
		noControlCharacters, "cannot contain control characters")
	timestampSchema = s.Refine(s.String(s.Regex(s.Pattern(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`), "must be a canonical UTC ISO-8601 timestamp")),
		func(v jsjson.Value) bool { return jsdate.IsCanonicalISO(v.Str()) }, "must be a valid canonical UTC ISO-8601 timestamp")

	// src/attestation.ts:44-60.
	unsignedSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(SchemaVersion)),
		s.F("attestationKind", s.LiteralString("BUNDLE_INTEGRITY_ATTESTATION")),
		s.F("limitation", s.LiteralString(Limitation)),
		s.F("receiptId", receiptIDSchema),
		s.F("subject", identifierSchema),
		s.F("issuer", identifierSchema),
		s.F("issuedAt", timestampSchema),
		s.F("bundleRootDigest", digestSchema),
		s.F("sourceDigest", s.Optional(digestSchema)),
		s.F("gitCheckpointDigest", s.Optional(digestSchema)),
		s.F("witnessDigest", s.Optional(digestSchema)),
	).Strict()

	// src/attestation.ts:62-64.
	attestationSchema = unsignedSchema.Extend(s.F("receiptDigest", digestSchema)).Strict()

	// src/attestation.ts:66-75. issuedAt (a string or a Date) is never
	// passed by the CLI, so it is not a member of CreationInput and is
	// always undefined here; the shape keeps its key for the field order.
	creationInputSchema = s.Object(
		s.F("receiptId", receiptIDSchema),
		s.F("subject", identifierSchema),
		s.F("issuer", identifierSchema),
		s.F("issuedAt", s.Optional(s.String())),
		s.F("bundleRootDigest", digestSchema),
		s.F("sourceDigest", s.Optional(digestSchema)),
		s.F("gitCheckpointDigest", s.Optional(digestSchema)),
		s.F("witnessDigest", s.Optional(digestSchema)),
	).Strict()

	// The receipt path split of ensureRealDirectory (src/attestation.ts:135).
	pathSeparators = regexp.MustCompile(`[\\/]+`)
)

// CreationInput is BundleAttestationInput without issuedAt (the CLI never
// passes it, so createBundleAttestation reads the clock). A nil pointer is
// undefined.
type CreationInput struct {
	ReceiptID, Subject, Issuer, BundleRootDigest     string
	SourceDigest, GitCheckpointDigest, WitnessDigest *string
}

// value is the input object as TS builds it: absent members are no keys.
func (in CreationInput) value() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("receiptId", str(in.ReceiptID))
	o.Set("subject", str(in.Subject))
	o.Set("issuer", str(in.Issuer))
	o.Set("bundleRootDigest", str(in.BundleRootDigest))
	for _, opt := range []struct {
		key string
		v   *string
	}{{"sourceDigest", in.SourceDigest}, {"gitCheckpointDigest", in.GitCheckpointDigest}, {"witnessDigest", in.WitnessDigest}} {
		if opt.v != nil {
			o.Set(opt.key, str(*opt.v))
		}
	}
	return jsjson.MakeObject(o)
}

// Written is WrittenBundleAttestation.
type Written struct {
	Directory, Path string
	Attestation     jsjson.Value
	ReceiptDigest   string
}

// Verification is BundleAttestationVerification. ReceiptDigest nil is
// null; an Undefined Attestation is an absent member.
type Verification struct {
	Valid                bool
	Errors               []string
	ReceiptDigest        *string
	ExternalDigestStatus string
	Attestation          jsjson.Value
}

// JSON is the verification object in TS key order.
func (v Verification) JSON() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(v.Valid))
	errs := make([]jsjson.Value, len(v.Errors))
	for i, e := range v.Errors {
		errs[i] = str(e)
	}
	o.Set("errors", jsjson.MakeArray(errs))
	if v.ReceiptDigest == nil {
		o.Set("receiptDigest", jsjson.MakeNull())
	} else {
		o.Set("receiptDigest", str(*v.ReceiptDigest))
	}
	o.Set("externalDigestStatus", str(v.ExternalDigestStatus))
	if v.Attestation.Kind() != jsjson.Undefined {
		o.Set("attestation", v.Attestation)
	}
	return jsjson.MakeObject(o)
}

func throw(message string) { jsexc.Throw(errors.New(message)) }

// parse is zod Schema.parse: it throws the ZodError.
func parse(v jsjson.Value, schema s.Schema) jsjson.Value {
	return jsexc.Must(bundle.ParseValue(v, schema))
}

// Port of src/attestation.ts:107 defaultAttestationStore.
func DefaultStore() string { return nodefs.Resolve(".faultline", "attestations") }

// Port of src/attestation.ts:119 isDescendantOrSame.
func isDescendantOrSame(root, candidate string) bool {
	pathFromRoot := jsexc.Must(safedir.RelativeTrustedSystemPath(root, candidate))
	return pathFromRoot == "" || (!strings.HasPrefix(pathFromRoot, "..") && !nodefs.IsAbsolute(pathFromRoot))
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return resolved, ok
}

// Port of src/attestation.ts:129 ensureRealDirectory: each existing
// component is checked with lstat, never followed; a missing one is created
// (mkdirSync's default mode 0777) only after its parent was checked.
func ensureRealDirectory(directory string, create bool) {
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
			throw(jsexc.Concat("Attestation path root must be a real directory: ", current))
		}
		current = safe
	}

	for _, part := range parts {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			if !create {
				throw(jsexc.Concat("Attestation store does not exist: ", absolute))
			}
			if err := nodefs.Mkdir(current, 0o777); err != nil {
				jsexc.Throw(err)
			}
		}
		safe, ok := safeSegment(current)
		if !ok {
			throw(jsexc.Concat("Attestation store cannot traverse a symbolic link or non-directory: ", current))
		}
		current = safe
	}
}

// Port of src/attestation.ts:160 safeStoreDirectory.
func safeStoreDirectory(storeDirectory, attestationRoot string, create bool) string {
	if storeDirectory == "" || jsstr.Trim(storeDirectory) == "" {
		throw("Attestation store directory must be non-empty.")
	}
	root := nodefs.Resolve(attestationRoot)
	store := nodefs.Resolve(storeDirectory)
	if !isDescendantOrSame(root, store) {
		throw(jsexc.Concat("Attestation store must be inside the managed root: ", root))
	}
	ensureRealDirectory(root, create)
	ensureRealDirectory(store, create)
	return store
}

// Port of src/attestation.ts:178 assertSafeAttestationStore.
func assertSafeAttestationStore(storeDirectory, attestationRoot string) string {
	return safeStoreDirectory(storeDirectory, attestationRoot, true)
}

// Port of src/attestation.ts:182 receiptPath.
func receiptPath(storeDirectory, receiptID string) string {
	safeID := parse(str(receiptID), receiptIDSchema).Str()
	path := nodefs.Join(storeDirectory, safeID+".json")
	if !isDescendantOrSame(storeDirectory, path) || strings.Contains(nodefs.Relative(storeDirectory, path), "..") {
		throw("Attestation receipt path escaped its managed store.")
	}
	return path
}

// Port of src/attestation.ts:191 unsignedRecord.
func unsignedRecord(attestation jsjson.Value) jsjson.Value {
	record := jsjson.NewObj()
	for _, k := range attestation.Obj().Keys() {
		if k != "receiptDigest" {
			record.Set(k, attestation.Obj().Field(k))
		}
	}
	return parse(jsjson.MakeObject(record), unsignedSchema)
}

// Port of src/attestation.ts:197 bundleAttestationDigest.
func bundleAttestationDigest(attestation jsjson.Value) string {
	return jsexc.Must(canonical.DigestJSON(unsignedRecord(attestation)))
}

// Port of src/attestation.ts:205 createBundleAttestation: the input parse,
// then one clock read (canonicalTimestamp's `new Date()`), then the
// unsigned parse and its digest.
func createBundleAttestation(input jsjson.Value) jsjson.Value {
	parsed := parse(input, creationInputSchema)
	base := jsjson.NewObj()
	base.Set("schemaVersion", str(SchemaVersion))
	base.Set("attestationKind", str("BUNDLE_INTEGRITY_ATTESTATION"))
	base.Set("limitation", str(Limitation))
	base.Set("receiptId", parsed.Get("receiptId"))
	base.Set("subject", parsed.Get("subject"))
	base.Set("issuer", parsed.Get("issuer"))
	base.Set("issuedAt", str(clock.ISO(clock.Now())))
	base.Set("bundleRootDigest", parsed.Get("bundleRootDigest"))
	for _, k := range []string{"sourceDigest", "gitCheckpointDigest", "witnessDigest"} {
		if v := parsed.Get(k); v.Kind() != jsjson.Undefined {
			base.Set(k, v)
		}
	}
	unsigned := parse(jsjson.MakeObject(base), unsignedSchema)
	signed := jsjson.NewObj()
	for _, k := range unsigned.Obj().Keys() {
		signed.Set(k, unsigned.Obj().Field(k))
	}
	signed.Set("receiptDigest", str(jsexc.Must(canonical.DigestJSON(unsigned))))
	return parse(jsjson.MakeObject(signed), attestationSchema)
}

// Create ports createBundleAttestation (src/attestation.ts:205) for a
// CreationInput: the receipt, or the thrown ZodError.
func Create(in CreationInput) (attestation jsjson.Value, err error) {
	err = jsexc.Try(func() { attestation = createBundleAttestation(in.value()) })
	return attestation, err
}

// Port of src/attestation.ts:225 externalDigestStatus. expected is "" when
// the status is NOT_PROVIDED or the supplied digest was malformed.
func externalDigestStatus(expectedReceiptDigest *string, errs *[]string) (status, expected string) {
	if expectedReceiptDigest == nil {
		return "NOT_PROVIDED", ""
	}
	if _, _, ok := s.Parse(digestSchema, str(*expectedReceiptDigest)); !ok {
		*errs = append(*errs, "externally supplied receipt digest is not a sha256 digest")
		return "MISMATCH", ""
	}
	return "MISMATCH", *expectedReceiptDigest
}

// Verify ports verifyBundleAttestation (src/attestation.ts:239). Reading
// the ZodError message may throw (jsexc) a RangeError, as in TS.
func Verify(attestation jsjson.Value, expected *string) Verification {
	var errs []string
	status, external := externalDigestStatus(expected, &errs)
	parsed, issues, ok := s.Parse(attestationSchema, attestation)
	if !ok {
		errs = append(errs, jsexc.Concat("attestation schema validation failed: ", s.ErrorMessage(issues)))
		return Verification{Valid: false, Errors: errs, ExternalDigestStatus: status}
	}

	calculated := bundleAttestationDigest(parsed)
	if parsed.Get("receiptDigest").Str() != calculated {
		errs = append(errs, "receiptDigest does not match the canonical receipt contents")
	}
	if external != "" {
		status = "MISMATCH"
		if external == calculated {
			status = "MATCH"
		}
	}
	if status == "MISMATCH" && expected != nil {
		errs = append(errs, "externally supplied receipt digest does not match")
	}
	return Verification{Valid: len(errs) == 0, Errors: errs, ReceiptDigest: &calculated, ExternalDigestStatus: status, Attestation: parsed}
}

// Port of src/attestation.ts:262 writeNewReceipt: a 0600 `wx` staging file
// (written, fsynced, closed), checked with lstat, then link(2)ed to the
// destination, which cannot replace an existing receipt. The `finally`
// removes the staging file whenever it exists, and an unlink failure
// replaces the result.
func writeNewReceipt(store, destination, body string) {
	temporary := nodefs.Join(store, jsexc.Concat(".attestation-", clock.NewUUID(), ".tmp"))
	err := func() error {
		if err := nodefs.WriteFileSyncUTF8(temporary, body, 0o600); err != nil {
			return err
		}
		stat, err := nodefs.Lstat(temporary)
		if err != nil {
			return err
		}
		if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
			return errors.New("Attestation staging file is not a regular file.")
		}
		return nodefs.Link(temporary, destination)
	}()
	if err != nil && nodefs.IsExist(err) {
		err = errors.New(jsexc.Concat("Attestation receipt already exists and cannot be overwritten: ", destination))
	}
	// finally
	if nodefs.Exists(temporary) {
		if uerr := nodefs.Unlink(temporary); uerr != nil {
			err = uerr
		}
	}
	if err != nil {
		jsexc.Throw(err)
	}
}

// Write ports writeBundleAttestation (src/attestation.ts:296) with an
// explicit attestation root (DefaultStore() from the CLI): the store check
// (creating directories), the receipt, its path, the store re-check, then
// the write-once link.
func Write(storeDirectory string, in CreationInput, attestationRoot string) (w Written, err error) {
	err = jsexc.Try(func() {
		store := assertSafeAttestationStore(storeDirectory, attestationRoot)
		attestation := createBundleAttestation(in.value())
		destination := receiptPath(store, attestation.Get("receiptId").Str())
		// Re-check after construction immediately before the final no-clobber link.
		safeStoreDirectory(store, attestationRoot, false)
		writeNewReceipt(store, destination, jsexc.Concat(jsexc.Must(canonical.CanonicalJSON(attestation)), "\n"))
		w = Written{Directory: store, Path: destination, Attestation: attestation, ReceiptDigest: attestation.Get("receiptDigest").Str()}
	})
	return w, err
}

// Port of src/attestation.ts:311 failedVerification.
func failedVerification(errs []string, expected *string) Verification {
	status, _ := externalDigestStatus(expected, &errs)
	return Verification{Valid: false, Errors: errs, ExternalDigestStatus: status}
}

// VerifyStored ports verifyStoredBundleAttestation (src/attestation.ts:320):
// it reads a receipt without following links and validates its canonical
// checksum and, when supplied, the externally retained digest. Any throw
// inside becomes a failed verification.
func VerifyStored(storeDirectory, receiptID string, expected *string, attestationRoot string) Verification {
	var result Verification
	err := jsexc.Try(func() {
		store := safeStoreDirectory(storeDirectory, attestationRoot, false)
		path := receiptPath(store, receiptID)
		if !nodefs.Exists(path) {
			result = failedVerification([]string{"attestation receipt does not exist"}, expected)
			return
		}
		stat := jsexc.Must(nodefs.Lstat(path))
		if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
			result = failedVerification([]string{"attestation receipt is a symbolic link or non-regular file"}, expected)
			return
		}
		parsed := jsexc.Must(jsjson.Parse(jsexc.Must(nodefs.ReadText(path))))
		result = Verify(parsed, expected)
	})
	if err != nil {
		return failedVerification([]string{jsexc.Concat("attestation verification failed safely: ", jsexc.Message(err))}, expected)
	}
	return result
}
