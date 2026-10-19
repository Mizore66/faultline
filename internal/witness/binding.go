package witness

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	s "github.com/Mizore66/faultline/internal/schema"
)

// IncidentSessionBindingSchemaVersion ports src/incident-binding.ts:17.
const IncidentSessionBindingSchemaVersion = "faultline.incident-session-binding.v1"

var (
	// IncidentDraftIDSchema ports IncidentDraftIdSchema (src/incident.ts:18).
	IncidentDraftIDSchema = s.String(s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`), "draftId must be a safe identifier"))

	// incidentSessionBindingSchema ports src/incident-binding.ts:22.
	incidentSessionBindingSchema = s.Object(
		s.F("schemaVersion", s.LiteralString(IncidentSessionBindingSchemaVersion)),
		s.F("incidentId", IncidentDraftIDSchema),
		s.F("expectDigest", s.Optional(gitproof.WitnessDigestSchema)),
		s.F("ledgerPath", s.Optional(s.String(s.MinLength(1), s.MaxLength(16_384)))),
		s.F("updatedAt", s.String(s.Datetime())),
	).Strict()

	pathSeparators = regexp.MustCompile(`[\\/]+`)
)

// BindingInput is writeIncidentSessionBinding's input; nil is undefined.
type BindingInput struct {
	IncidentID                          string
	ExpectDigest, LedgerPath, UpdatedAt *string
}

// InheritInput is resolveInheritedSessionFacts's options; nil is undefined.
type InheritInput struct {
	StoreDirectory, IncidentID string
	ExpectDigest, LedgerPath   *string
}

// InheritedFacts ports InheritedSessionFacts (src/incident-binding.ts:109).
type InheritedFacts struct {
	ExpectDigest, LedgerPath                   *string
	InheritedExpectDigest, InheritedLedgerPath bool
}

// Port of src/incident-binding.ts:32 assertNonSymlinkDirectory.
func assertNonSymlinkDirectory(path string) string {
	safe, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	if !ok {
		throw(jsexc.Concat("Incident binding store path must be a real non-symlink directory: ", path))
	}
	return safe
}

// volumeRoot is path.parse(p).root for the absolute paths
// resolveTrustedSystemPath returns.
func volumeRoot(p string) string {
	if runtime.GOOS != "windows" {
		if strings.HasPrefix(p, "/") {
			return "/"
		}
		return ""
	}
	root := filepath.VolumeName(p)
	if len(p) > len(root) && (p[len(root)] == '\\' || p[len(root)] == '/') {
		root += p[len(root) : len(root)+1]
	}
	return root
}

// Port of src/incident-binding.ts:40 ensureSafeDirectory: each missing
// segment is created on its own (mode 0o700) and checked before descending.
func ensureSafeDirectory(path string) {
	target := jsexc.Must(safedir.ResolveTrustedSystemPath(path))
	root := volumeRoot(target)
	current := root
	for _, segment := range pathSeparators.Split(nodefs.Relative(root, target), -1) {
		if segment == "" {
			continue
		}
		current = nodefs.Join(current, segment)
		if !nodefs.Exists(current) {
			if err := nodefs.Mkdir(current, 0o700); err != nil {
				jsexc.Throw(err)
			}
		}
		current = assertNonSymlinkDirectory(current)
	}
}

// Port of src/incident-binding.ts:54 bindingDirectory.
func bindingDirectory(storeDirectory string) string {
	return nodefs.Join(jsexc.Must(safedir.ResolveTrustedSystemPath(storeDirectory)), "bindings")
}

// Port of src/incident-binding.ts:58 bindingPath.
func bindingPath(storeDirectory, incidentID string) string {
	id := parse(jsjson.MakeString(incidentID), IncidentDraftIDSchema).Str()
	root := jsexc.Must(safedir.ResolveTrustedSystemPath(storeDirectory))
	path := nodefs.Resolve(bindingDirectory(root), id+".json")
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(root, path))
	if nested == "" || strings.HasPrefix(nested, "..") || strings.Contains(nested, ":") {
		throw("Incident binding path escaped its configured store")
	}
	return path
}

// ReadSessionBinding ports readIncidentSessionBinding
// (src/incident-binding.ts:69); ok is false for TS's null.
func ReadSessionBinding(storeDirectory, incidentID string) (binding jsjson.Value, ok bool, err error) {
	err = jsexc.Try(func() { binding, ok = readSessionBinding(storeDirectory, incidentID) })
	return
}

func readSessionBinding(storeDirectory, incidentID string) (jsjson.Value, bool) {
	path := bindingPath(storeDirectory, incidentID)
	if !nodefs.Exists(path) {
		return jsjson.Value{}, false
	}
	info := jsexc.Must(nodefs.Lstat(path))
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		throw(jsexc.Concat("Incident session binding must be a regular non-symlink file: ", path))
	}
	text := jsexc.Must(nodefs.ReadText(path))
	parsed := parse(jsexc.Must(jsjson.Parse(text)), incidentSessionBindingSchema)
	if parsed.Get("incidentId").Str() != incidentID {
		throw("Stored incident session binding identifier does not match the requested identifier")
	}
	return parsed, true
}

// orPrevious is `explicit ?? previous?.[key]`.
func orPrevious(explicit *string, previous jsjson.Value, key string) jsjson.Value {
	if explicit != nil {
		return jsjson.MakeString(*explicit)
	}
	return previous.Get(key)
}

// WriteSessionBinding ports writeIncidentSessionBinding
// (src/incident-binding.ts:86): it overwrites the binding (flag "w", mode
// 0o600), not atomically. updatedAt is read from the clock after the
// previous binding is read.
func WriteSessionBinding(storeDirectory string, in BindingInput) (jsjson.Value, error) {
	return tryValue(func() jsjson.Value {
		previous, _ := readSessionBinding(storeDirectory, in.IncidentID)
		raw := jsjson.NewObj()
		raw.Set("schemaVersion", jsjson.MakeString(IncidentSessionBindingSchemaVersion))
		raw.Set("incidentId", jsjson.MakeString(in.IncidentID))
		// Undefined members disappear from the canonical record.
		if v := orPrevious(in.ExpectDigest, previous, "expectDigest"); v.Kind() != jsjson.Undefined {
			raw.Set("expectDigest", v)
		}
		if v := orPrevious(in.LedgerPath, previous, "ledgerPath"); v.Kind() != jsjson.Undefined {
			raw.Set("ledgerPath", v)
		}
		if in.UpdatedAt != nil {
			raw.Set("updatedAt", jsjson.MakeString(*in.UpdatedAt))
		} else {
			raw.Set("updatedAt", jsjson.MakeString(now()))
		}
		binding := parse(jsjson.MakeObject(raw), incidentSessionBindingSchema)
		ensureSafeDirectory(bindingDirectory(storeDirectory))
		path := bindingPath(storeDirectory, binding.Get("incidentId").Str())
		text := jsexc.Concat(jsexc.Must(canonical.CanonicalJSON(binding)), "\n")
		if err := nodefs.WriteFileUTF8(path, text, 0o600, false); err != nil {
			jsexc.Throw(err)
		}
		return binding
	})
}

func optionalString(v jsjson.Value) *string {
	if v.Kind() == jsjson.Undefined {
		return nil
	}
	s := v.Str()
	return &s
}

// ResolveInheritedSessionFacts ports resolveInheritedSessionFacts
// (src/incident-binding.ts:119): explicit CLI values win over the local
// binding. A failed binding read throws (jsexc), as in TS.
func ResolveInheritedSessionFacts(in InheritInput) InheritedFacts {
	binding, _ := readSessionBinding(in.StoreDirectory, in.IncidentID)
	bound := func(key string) *string { return optionalString(binding.Get(key)) }
	out := InheritedFacts{ExpectDigest: in.ExpectDigest, LedgerPath: in.LedgerPath}
	if out.ExpectDigest == nil {
		out.ExpectDigest = bound("expectDigest")
	}
	if out.LedgerPath == nil {
		out.LedgerPath = bound("ledgerPath")
	}
	out.InheritedExpectDigest = in.ExpectDigest == nil && bound("expectDigest") != nil
	out.InheritedLedgerPath = in.LedgerPath == nil && bound("ledgerPath") != nil
	return out
}

// DefaultIncidentDraftStore ports defaultIncidentDraftStore
// (src/incident-store.ts:83).
func DefaultIncidentDraftStore(repository string) string {
	return nodefs.Join(nodefs.Resolve(repository), ".faultline", "incidents")
}
