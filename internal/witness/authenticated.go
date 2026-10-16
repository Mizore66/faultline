package witness

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"runtime"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/authenticated-witness-approval.ts:15-18.
const (
	authenticatedApprovalVersion = "faultline.authenticated-witness-approval.v1"
	reviewerKeyringVersion       = "faultline.reviewer-keyring.v1"
	authenticatedLimitation      = "A verified receipt proves that the holder of a key trusted for the recorded reviewer identity signed this frozen witness approval. It does not prove a legal identity, review quality, CI provenance, or host enforcement."
	maxApprovalBytes             = 500_000
)

// SignOptions is SignAuthenticatedWitnessApprovalInput; SignedAt nil means now.
type SignOptions struct {
	PrivateKeyPEM string
	Keyring       jsjson.Value
	SignedAt      *string
}

// VerifyAuthOptions is VerifyAuthenticatedWitnessApprovalOptions: nil
// pointers are TS's undefined.
type VerifyAuthOptions struct {
	ExpectedFrozenDigest *string
	Keyring              *jsjson.Value
	RequireSignature     bool
}

var (
	// Port of src/authenticated-witness-approval.ts:24-30.
	authReviewerIdentity = s.Refine(s.String(s.Trim(), s.MinLength(1), s.MaxLength(512)), noControlCharacters, "must not contain control characters")
	authTimestamp        = s.Refine(s.String(s.Datetime()), func(v jsjson.Value) bool { return gitproof.CanonicalTimestamp(v.Str()) }, "expected a canonical UTC ISO-8601 timestamp")
	authSignature        = s.Refine(s.String(s.MinLength(1), s.MaxLength(16_384)), canonicalBase64, "expected canonical base64 signature bytes")

	// ReviewerKeyringSchema ports src/authenticated-witness-approval.ts:39.
	reviewerKeyringSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(reviewerKeyringVersion)),
		s.F("reviewers", s.Array(s.Object(
			s.F("approvedBy", authReviewerIdentity),
			s.F("keyId", gitproof.WitnessDigestSchema),
			s.F("algorithm", s.LiteralString("ED25519")),
			s.F("publicKeyPem", s.String(s.MinLength(1), s.MaxLength(32_000))),
		).Strict(), s.MinItems(1), s.MaxItems(1_024))),
	).Strict()

	// Port of src/authenticated-witness-approval.ts:49-65.
	unsignedReceiptSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(authenticatedApprovalVersion)),
		s.F("recordKind", s.LiteralString("FROZEN_WITNESS_APPROVAL")),
		s.F("proposalId", gitproof.ProposalIDSchema),
		s.F("frozenDigest", gitproof.WitnessDigestSchema),
		s.F("witnessDigest", gitproof.WitnessDigestSchema),
		s.F("approvalDigest", gitproof.WitnessDigestSchema),
		s.F("approvedBy", authReviewerIdentity),
		s.F("keyId", gitproof.WitnessDigestSchema),
		s.F("algorithm", s.LiteralString("ED25519")),
		s.F("signedAt", authTimestamp),
		s.F("signatureBase64", authSignature),
	).Strict()
	receiptSchema = unsignedReceiptSchema.Extend(s.F("receiptDigest", gitproof.WitnessDigestSchema)).Strict()
)

// /[\u0000-\u001f\u007f]/: WTF-8 bytes of non-ASCII code points are >= 0x80.
func noControlCharacters(v jsjson.Value) bool {
	for i := 0; i < len(v.Str()); i++ {
		if c := v.Str()[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

var canonicalBase64Shape = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)

// Port of src/authenticated-witness-approval.ts:32 isCanonicalBase64.
func canonicalBase64(v jsjson.Value) bool {
	if !canonicalBase64Shape.MatchString(v.Str()) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(v.Str())
	return err == nil && base64.StdEncoding.EncodeToString(decoded) == v.Str()
}

// keyObject is the part of a Node KeyObject this module reads.
type keyObject struct {
	ed25519 bool
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func publicKeyDER(k keyObject) []byte {
	der, err := x509.MarshalPKIXPublicKey(k.public)
	if err != nil {
		throw("Reviewer public key could not be exported as SPKI DER.")
	}
	return der
}

// Port of src/authenticated-witness-approval.ts:115 publicKeyFingerprint.
func publicKeyFingerprint(k keyObject) string {
	if !k.ed25519 {
		throw("Reviewer key must use Ed25519.")
	}
	sum := sha256.Sum256(publicKeyDER(k))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func jsStrings(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

// Port of src/authenticated-witness-approval.ts:128 parseReviewerKeyring.
func parseReviewerKeyring(input jsjson.Value) jsjson.Value {
	keyring := parse(input, reviewerKeyringSchema)
	seen := map[string]bool{}
	for _, reviewer := range keyring.Get("reviewers").Items() {
		keyID := reviewer.Get("keyId").Str()
		if seen[keyID] {
			throw(jsexc.Concat("Reviewer keyring contains a duplicate keyId: ", keyID))
		}
		seen[keyID] = true
		key, err := parsePublic(reviewer.Get("publicKeyPem").Str())
		if err != nil {
			throw(jsexc.Concat("Reviewer keyring public key ", keyID, " is invalid: ", err.Error()))
		}
		if !key.ed25519 {
			throw(jsexc.Concat("Reviewer keyring public key ", keyID, " is not Ed25519."))
		}
		if publicKeyFingerprint(key) != keyID {
			throw(jsexc.Concat("Reviewer keyring keyId does not match its public key: ", keyID))
		}
	}
	return keyring
}

// splitSeparators is split(/[\\/]+/).filter(Boolean).
func splitSeparators(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

// pathRoot is path.parse(p).root.
func pathRoot(absolute string) string {
	if runtime.GOOS != "windows" {
		if strings.HasPrefix(absolute, "/") {
			return "/"
		}
		return ""
	}
	if strings.HasPrefix(absolute, `\\`) {
		parts := strings.SplitN(absolute[2:], `\`, 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1] + `\`
		}
	}
	if len(absolute) >= 3 && absolute[1] == ':' {
		return absolute[:3]
	}
	return `\`
}

func safeSegment(path string) (string, bool) {
	safe, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return safe, ok
}

// Port of src/authenticated-witness-approval.ts:146 ensureRealDirectoryTree.
func ensureRealDirectoryTree(directory string) {
	absolute := nodefs.Resolve(directory)
	root := pathRoot(absolute)
	parts := splitSeparators(nodefs.Relative(root, absolute))
	current := root
	if nodefs.Exists(current) {
		safe, ok := safeSegment(current)
		if !ok {
			throw(jsexc.Concat("Witness approval root must be a real directory: ", current))
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
			throw(jsexc.Concat("Witness approval path cannot traverse a symbolic link or non-directory: ", current))
		}
		current = safe
	}
}

// Port of src/authenticated-witness-approval.ts:165 assertExistingSafeDirectories.
func assertExistingSafeDirectories(root, directory string) {
	normalizedRoot := nodefs.Resolve(root)
	normalizedDirectory := nodefs.Resolve(directory)
	relativePath := jsexc.Must(safedir.RelativeTrustedSystemPath(normalizedRoot, normalizedDirectory))
	if strings.HasPrefix(relativePath, "..") || nodefs.IsAbsolute(relativePath) {
		throw("Witness approval path escaped its configured store")
	}
	parts := splitSeparators(relativePath)
	current := normalizedRoot
	if nodefs.Exists(current) {
		if _, ok := safeSegment(current); !ok {
			throw(jsexc.Concat("Witness approval root is not a real directory: ", current))
		}
	}
	for _, part := range parts {
		current = nodefs.Join(current, part)
		if nodefs.Exists(current) {
			if _, ok := safeSegment(current); !ok {
				throw(jsexc.Concat("Witness approval path cannot traverse a symbolic link or non-directory: ", current))
			}
		}
	}
}

// Port of src/authenticated-witness-approval.ts:183 receiptPath.
func receiptPath(storeDirectory, proposalID string, createDirectories bool) string {
	id := parse(jsjson.MakeString(proposalID), gitproof.ProposalIDSchema).Str()
	root := nodefs.Resolve(storeDirectory)
	directory := nodefs.Join(root, "authenticated-approvals")
	if createDirectories {
		ensureRealDirectoryTree(directory)
	} else {
		assertExistingSafeDirectories(root, directory)
	}
	path := nodefs.Resolve(directory, id+".json")
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(root, path))
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		throw("Witness approval path escaped its configured store")
	}
	return path
}

// Port of src/authenticated-witness-approval.ts:195 readReceipt.
func readReceipt(path string) jsjson.Value {
	info := jsexc.Must(nodefs.Lstat(path))
	if !info.Mode().IsRegular() {
		throw("Authenticated witness approval must be a regular non-symlink file")
	}
	if info.Size() > maxApprovalBytes {
		throw("Authenticated witness approval exceeds the 500000-byte limit")
	}
	var receipt jsjson.Value
	if err := jsexc.Try(func() {
		receipt = parse(jsexc.Must(bundle.ParseJSONFile(path)), receiptSchema)
	}); err != nil {
		throw(jsexc.Concat("Authenticated witness approval is not valid JSON: ", jsexc.Message(err)))
	}
	return receipt
}

func without(v jsjson.Value, key string) *jsjson.Obj {
	out := jsjson.NewObj()
	for _, k := range v.Obj().Keys() {
		if k != key {
			out.Set(k, v.Obj().Field(k))
		}
	}
	return out
}

// Port of src/authenticated-witness-approval.ts:93 unsignedPayload.
func unsignedPayload(receipt jsjson.Value) jsjson.Value {
	return parse(jsjson.MakeObject(without(receipt, "receiptDigest")), unsignedReceiptSchema)
}

// Port of src/authenticated-witness-approval.ts:103 signingBytes.
func signingBytes(receipt jsjson.Value) []byte {
	payload := jsjson.MakeObject(without(unsignedPayload(receipt), "signatureBase64"))
	o := jsjson.NewObj()
	o.Set("domain", jsjson.MakeString(authenticatedApprovalVersion))
	o.Set("payload", payload)
	return []byte(jsstr.ToUTF8(jsexc.Must(canonical.CanonicalJSON(jsjson.MakeObject(o)))))
}

// Port of src/authenticated-witness-approval.ts:110 canonicalTimestamp.
func authCanonicalTimestamp(value *string) string {
	timestamp := now()
	if value != nil {
		timestamp = *value
	}
	return parse(jsjson.MakeString(timestamp), authTimestamp).Str()
}

// Port of src/authenticated-witness-approval.ts:206 receiptFromFrozen.
func receiptFromFrozen(frozen jsjson.Value, keyID, signedAt string, privateKey keyObject) jsjson.Value {
	unsigned := jsjson.NewObj()
	unsigned.Set("schemaVersion", jsjson.MakeString(authenticatedApprovalVersion))
	unsigned.Set("recordKind", jsjson.MakeString("FROZEN_WITNESS_APPROVAL"))
	unsigned.Set("proposalId", frozen.Get("proposal", "proposalId"))
	unsigned.Set("frozenDigest", frozen.Get("frozenDigest"))
	unsigned.Set("witnessDigest", frozen.Get("witnessDigest"))
	unsigned.Set("approvalDigest", frozen.Get("approval", "approvalDigest"))
	unsigned.Set("approvedBy", frozen.Get("approval", "approvedBy"))
	unsigned.Set("keyId", jsjson.MakeString(keyID))
	unsigned.Set("algorithm", jsjson.MakeString("ED25519"))
	unsigned.Set("signedAt", jsjson.MakeString(signedAt))
	signed := jsjson.NewObj()
	signed.Set("domain", jsjson.MakeString(authenticatedApprovalVersion))
	signed.Set("payload", jsjson.MakeObject(unsigned))
	text := jsstr.ToUTF8(jsexc.Must(canonical.CanonicalJSON(jsjson.MakeObject(signed))))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey.private, []byte(text)))
	withSignature := jsjson.NewObj()
	for _, k := range unsigned.Keys() {
		withSignature.Set(k, unsigned.Field(k))
	}
	withSignature.Set("signatureBase64", jsjson.MakeString(signature))
	payload := parse(jsjson.MakeObject(withSignature), unsignedReceiptSchema)
	out := jsjson.NewObj()
	for _, k := range payload.Obj().Keys() {
		out.Set(k, payload.Obj().Field(k))
	}
	out.Set("receiptDigest", jsjson.MakeString(jsexc.Must(canonical.DigestJSON(payload))))
	return parse(jsjson.MakeObject(out), receiptSchema)
}

// SignAuthenticatedApproval ports signAuthenticatedWitnessApproval
// (src/authenticated-witness-approval.ts:248): it signs the already-frozen
// witness with a reviewer-held Ed25519 key matched to the supplied keyring.
func SignAuthenticatedApproval(storeDirectory, proposalID string, o SignOptions) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		frozenVerification := VerifyFrozen(storeDirectory, proposalID, "", false)
		if !frozenVerification.Valid {
			throw(jsexc.Concat("Cannot sign an invalid frozen witness: ", jsexc.Join(frozenVerification.Errors, "; ")))
		}
		frozen := jsexc.Must(ReadFrozen(storeDirectory, proposalID))
		current := gitproof.VerifyFrozenWitnessRecord(frozen, "", false)
		if !current.Valid {
			throw(jsexc.Concat("Cannot sign a frozen witness that changed during verification: ", jsexc.Join(current.Errors, "; ")))
		}
		keyring := parseReviewerKeyring(o.Keyring)
		privateKey, err := parsePrivate(o.PrivateKeyPEM)
		if err != nil {
			throw(jsexc.Concat("Reviewer private key is invalid: ", err.Error()))
		}
		if !privateKey.ed25519 {
			throw("Reviewer private key must use Ed25519.")
		}
		keyID := publicKeyFingerprint(privateKey)
		var configured jsjson.Value
		for _, reviewer := range keyring.Get("reviewers").Items() {
			if reviewer.Get("keyId").Str() == keyID {
				configured = reviewer
				break
			}
		}
		if configured.Kind() == jsjson.Undefined {
			throw("Reviewer private key is not present in the supplied reviewer keyring.")
		}
		if configured.Get("approvedBy").Str() != frozen.Get("approval", "approvedBy").Str() {
			throw("Reviewer keyring identity does not match the frozen witness approval actor.")
		}
		configuredKey, err := parsePublic(configured.Get("publicKeyPem").Str())
		if err != nil {
			jsexc.Throw(err)
		}
		if string(publicKeyDER(configuredKey)) != string(publicKeyDER(privateKey)) {
			throw("Reviewer private key does not match the trusted reviewer public key.")
		}
		receipt := receiptFromFrozen(frozen, keyID, authCanonicalTimestamp(o.SignedAt), privateKey)
		path := receiptPath(storeDirectory, proposalID, true)
		text := jsexc.Concat(jsexc.Must(canonical.CanonicalJSON(receipt)), "\n")
		if err := nodefs.WriteFileUTF8(path, text, 0o600, true); err != nil {
			if nodefs.IsExist(err) {
				throw(jsexc.Concat("Authenticated witness approval already exists and cannot be overwritten: ", path))
			}
			jsexc.Throw(err)
		}
		return receipt
	})
}

// ReadAuthenticatedApproval ports readAuthenticatedWitnessApproval
// (src/authenticated-witness-approval.ts:291).
func ReadAuthenticatedApproval(storeDirectory, proposalID string) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		return readReceipt(receiptPath(storeDirectory, proposalID, false))
	})
}

// Port of src/authenticated-witness-approval.ts:295 baseFailure.
func authResult(valid bool, errs []string, witness jsjson.Value, status string, receipt *jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("valid", jsjson.MakeBool(valid))
	o.Set("errors", jsStrings(errs))
	o.Set("witness", witness)
	o.Set("signatureStatus", jsjson.MakeString(status))
	if receipt != nil {
		o.Set("receipt", *receipt)
	}
	o.Set("limitation", jsjson.MakeString(authenticatedLimitation))
	return jsjson.MakeObject(o)
}

// VerifyAuthenticatedApproval ports verifyAuthenticatedWitnessApproval
// (src/authenticated-witness-approval.ts:317). Throws TS does not catch
// propagate as jsexc panics, as in VerifyFrozen.
func VerifyAuthenticatedApproval(storeDirectory, proposalID string, o VerifyAuthOptions) jsjson.Value {
	expected, provided := "", false
	if o.ExpectedFrozenDigest != nil {
		expected, provided = *o.ExpectedFrozenDigest, true
	}
	verification := VerifyFrozen(storeDirectory, proposalID, expected, provided)
	witness := verification.JSON()
	failure := func(errs []string, status string, receipt *jsjson.Value) jsjson.Value {
		return authResult(false, errs, witness, status, receipt)
	}
	if !verification.Valid {
		return failure(append([]string{}, verification.Errors...), "INVALID", nil)
	}
	if o.RequireSignature && o.Keyring == nil {
		return failure([]string{"--require-signature needs a reviewer keyring"}, "NOT_CHECKED", nil)
	}
	var path string
	if err := jsexc.Try(func() { path = receiptPath(storeDirectory, proposalID, false) }); err != nil {
		return failure([]string{jsexc.Concat("Authenticated witness approval lookup failed safely: ", jsexc.Message(err))}, "INVALID", nil)
	}
	if !nodefs.Exists(path) {
		if o.RequireSignature {
			return failure([]string{"authenticated reviewer signature is required but missing"}, "NOT_PRESENT", nil)
		}
		return authResult(true, nil, witness, "NOT_PRESENT", nil)
	}
	var receipt jsjson.Value
	if err := jsexc.Try(func() { receipt = readReceipt(path) }); err != nil {
		return failure([]string{jsexc.Concat("Authenticated witness approval could not be read safely: ", jsexc.Message(err))}, "INVALID", nil)
	}
	if o.Keyring == nil {
		if o.RequireSignature {
			return failure([]string{"authenticated reviewer signature cannot be trusted without a reviewer keyring"}, "NOT_CHECKED", &receipt)
		}
		return authResult(true, nil, witness, "NOT_CHECKED", &receipt)
	}
	var frozen, keyring jsjson.Value
	var early *jsjson.Value
	if err := jsexc.Try(func() {
		frozen = jsexc.Must(ReadFrozen(storeDirectory, proposalID))
		current := gitproof.VerifyFrozenWitnessRecord(frozen, expected, provided)
		if !current.Valid {
			v := failure(append([]string{"frozen witness changed during authenticated approval verification"}, current.Errors...), "INVALID", &receipt)
			early = &v
			return
		}
		keyring = parseReviewerKeyring(*o.Keyring)
	}); err != nil {
		return failure([]string{jsexc.Concat("Authenticated witness approval trust setup failed safely: ", jsexc.Message(err))}, "UNTRUSTED", &receipt)
	}
	if early != nil {
		return *early
	}
	var errs []string
	if receipt.Get("proposalId").Str() != frozen.Get("proposal", "proposalId").Str() {
		errs = append(errs, "signed proposalId does not match the frozen witness")
	}
	if receipt.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() {
		errs = append(errs, "signed frozenDigest does not match the frozen witness")
	}
	if receipt.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() {
		errs = append(errs, "signed witnessDigest does not match the frozen witness")
	}
	if receipt.Get("approvalDigest").Str() != frozen.Get("approval", "approvalDigest").Str() {
		errs = append(errs, "signed approvalDigest does not match the frozen human approval")
	}
	if receipt.Get("approvedBy").Str() != frozen.Get("approval", "approvedBy").Str() {
		errs = append(errs, "signed reviewer identity does not match the frozen human approval")
	}
	if receipt.Get("receiptDigest").Str() != jsexc.Must(canonical.DigestJSON(unsignedPayload(receipt))) {
		errs = append(errs, "authenticated approval receiptDigest does not match its canonical contents")
	}
	var configured jsjson.Value
	for _, reviewer := range keyring.Get("reviewers").Items() {
		if reviewer.Get("keyId").Str() == receipt.Get("keyId").Str() {
			configured = reviewer
			break
		}
	}
	if configured.Kind() == jsjson.Undefined {
		errs = append(errs, "signed reviewer keyId is not present in the trusted keyring")
		return failure(errs, "UNTRUSTED", &receipt)
	}
	if configured.Get("approvedBy").Str() != receipt.Get("approvedBy").Str() {
		errs = append(errs, "trusted reviewer key identity does not match the signed reviewer identity")
		return failure(errs, "UNTRUSTED", &receipt)
	}
	var publicKey keyObject
	if err := jsexc.Try(func() {
		key, err := parsePublic(configured.Get("publicKeyPem").Str())
		if err != nil {
			jsexc.Throw(err)
		}
		publicKey = key
		if !key.ed25519 {
			throw("trusted public key is not Ed25519")
		}
		if publicKeyFingerprint(key) != receipt.Get("keyId").Str() {
			throw("trusted public-key fingerprint does not match the signed keyId")
		}
	}); err != nil {
		errs = append(errs, jsexc.Concat("trusted reviewer public key is invalid: ", jsexc.Message(err)))
		return failure(errs, "UNTRUSTED", &receipt)
	}
	// TS wraps verify() in a try/catch ("verification failed safely") for
	// exceptions; Node's verify only throws on a malformed key, which was
	// ruled out above, and ed25519.Verify never panics, so there is no
	// Go counterpart. The signature is canonical base64 (schema), so the
	// strict decoder agrees with Node's.
	signature, _ := base64.StdEncoding.DecodeString(receipt.Get("signatureBase64").Str())
	if !ed25519.Verify(publicKey.public, signingBytes(receipt), signature) {
		errs = append(errs, "Ed25519 signature does not verify against the trusted reviewer key")
	}
	if len(errs) > 0 {
		return failure(errs, "INVALID", &receipt)
	}
	return authResult(true, nil, witness, "VERIFIED", &receipt)
}
