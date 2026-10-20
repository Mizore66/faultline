// Package allowlist ports src/secret-allowlist.ts: the digest-bound
// .faultline-secret-allowlist.json that lets reviewed HIGH secret
// occurrences into a turn-tree snapshot.
package allowlist

import (
	"errors"
	"os"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Port of src/secret-allowlist.ts:7-8.
const (
	Filename      = ".faultline-secret-allowlist.json"
	SchemaVersion = "faultline.secret-allowlist.v1"
)

// parseLimit is the 256 KiB cap of src/secret-allowlist.ts:42.
const parseLimit = 256 * 1024

// Port of src/secret-allowlist.ts:10 DigestSchema.
var digestSchema = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), ""))

// Port of src/secret-allowlist.ts:12 AllowlistEntrySchema.
var entrySchema = s.Object(
	s.F("path", s.String(s.MinLength(1), s.MaxLength(4_096))),
	s.F("kind", s.String(s.MinLength(1), s.MaxLength(128))),
	s.F("occurrenceDigest", digestSchema),
	s.F("fileDigest", s.Optional(digestSchema)),
	s.F("note", s.Optional(s.String(s.MinLength(1), s.MaxLength(512)))),
).Strict()

// Schema ports src/secret-allowlist.ts:20 SecretAllowlistSchema.
var Schema = s.Object(
	s.F("schemaVersion", s.LiteralString(SchemaVersion)),
	s.F("entries", s.Array(entrySchema, s.MaxItems(2_048))),
).Strict()

// Entry is one allowlist entry (SecretAllowlistEntry). FileDigest is nil
// when the entry has none.
type Entry struct {
	Path, Kind, OccurrenceDigest string
	FileDigest                   *string
}

// Allowlist is a parsed document (SecretAllowlist). The zero value is the
// empty allowlist.
type Allowlist struct {
	value   jsjson.Value // zod's output, keys in schema order
	Entries []Entry
}

// Empty is the allowlist a missing file means.
func Empty() Allowlist {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(SchemaVersion))
	o.Set("entries", jsjson.MakeArray([]jsjson.Value{}))
	return Allowlist{value: jsjson.MakeObject(o)}
}

// Value is the parsed document as zod returns it.
func (a Allowlist) Value() jsjson.Value {
	if a.value.Kind() == jsjson.Undefined {
		return Empty().value
	}
	return a.value
}

// FromValue is SecretAllowlistSchema.parse(v); failures are *schema.Error.
func FromValue(v jsjson.Value) (Allowlist, error) {
	parsed, err := bundle.ParseValue(v, Schema)
	if err != nil {
		return Allowlist{}, err
	}
	a := Allowlist{value: parsed}
	for _, e := range parsed.Get("entries").Items() {
		entry := Entry{Path: e.Get("path").Str(), Kind: e.Get("kind").Str(), OccurrenceDigest: e.Get("occurrenceDigest").Str()}
		if d := e.Get("fileDigest"); d.Kind() == jsjson.String {
			digest := d.Str()
			entry.FileDigest = &digest
		}
		a.Entries = append(a.Entries, entry)
	}
	return a, nil
}

// Path ports src/secret-allowlist.ts:28 secretAllowlistPath.
func Path(repositoryRoot string) string {
	return nodefs.Join(repositoryRoot, Filename)
}

// Load ports src/secret-allowlist.ts:32 loadSecretAllowlist. Errors keep
// their Node, JSON or zod types (read messages with jsexc.Message).
func Load(repo string) (Allowlist, error) {
	path := Path(repo)
	if !nodefs.Exists(path) {
		return Empty(), nil
	}
	stat, err := nodefs.Lstat(path)
	if err != nil {
		return Allowlist{}, err
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		return Allowlist{}, errors.New(Filename + " must be a regular non-symlink file.")
	}
	if stat.Size() > parseLimit {
		return Allowlist{}, errors.New(Filename + " exceeds FaultLine's 256 KiB parse limit.")
	}
	v, err := bundle.ParseJSONFile(path)
	if err != nil {
		return Allowlist{}, err
	}
	return FromValue(v)
}

// FileContentDigest ports src/secret-allowlist.ts:47 fileContentDigest: it
// reads the whole file and hashes the first maxBytes.
func FileContentDigest(path string, maxBytes int) (string, error) {
	data, err := nodefs.ReadBytes(path)
	if err != nil {
		return "", err
	}
	return "sha256:" + canonical.SHA256HexBytes(data[:min(len(data), maxBytes)]), nil
}

// ConfigDigest ports src/secret-allowlist.ts:52 allowlistConfigDigest
// (plain JSON.stringify, not canonical JSON).
func ConfigDigest(a Allowlist) string {
	return "sha256:" + canonical.SHA256Hex(jsjson.Stringify(a.Value()))
}

// IsHighOccurrenceAllowlisted ports src/secret-allowlist.ts:60: path, kind
// and occurrence digest must match, and the file digest too when the entry
// has one.
func IsHighOccurrenceAllowlisted(a Allowlist, relativePath, kind, occurrenceDigest, currentFileDigest string) bool {
	for _, e := range a.Entries {
		if e.Path == relativePath && e.Kind == kind && e.OccurrenceDigest == occurrenceDigest &&
			(e.FileDigest == nil || *e.FileDigest == currentFileDigest) {
			return true
		}
	}
	return false
}

// FilterHighOccurrences ports src/secret-allowlist.ts:75
// filterAllowlistedHighOccurrences: it returns the HIGH occurrences no entry
// covers, in order. Kinds gives TS's remainingKinds, and the number of HIGH
// occurrences minus len(result) is its allowlistedCount.
func FilterHighOccurrences(occ []redaction.Occurrence, fileDigest, relPath string, a Allowlist) []redaction.Occurrence {
	var out []redaction.Occurrence
	for _, o := range occ {
		if o.Confidence != "HIGH" {
			continue
		}
		if IsHighOccurrenceAllowlisted(a, relPath, o.Kind, o.Digest, fileDigest) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// Kinds is [...new Set(occ.map((o) => o.kind))]: first-seen order.
func Kinds(occ []redaction.Occurrence) []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range occ {
		if !seen[o.Kind] {
			seen[o.Kind] = true
			out = append(out, o.Kind)
		}
	}
	return out
}

// IsEntropyTokenAllowlisted ports src/secret-allowlist.ts:100.
func IsEntropyTokenAllowlisted(token, fileDigest, relPath string, a Allowlist) bool {
	return IsHighOccurrenceAllowlisted(a, relPath, "HIGH_ENTROPY_TOKEN", "sha256:"+canonical.SHA256Hex(token), fileDigest)
}
