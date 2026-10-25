package incident

import (
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	"github.com/Mizore66/faultline/internal/witness"
)

// The write-once local store for review-only intake records
// (src/incident-store.ts). It has no connection to approval or freezing,
// which proves that an intake did not turn a pasted CI command into an
// approved witness behind the user's back.

const storeSymlinkMessage = "Incident draft store path must be a real non-symlink directory: "

// Port of src/incident-store.ts:37 ensureSafeDirectory.
func ensureSafeDirectory(path string) {
	witness.EnsureSafeDirectory(path, storeSymlinkMessage)
}

// Port of src/incident-store.ts:51 assertSafeExistingDirectory.
func assertSafeExistingDirectory(path string) {
	witness.AssertSafeExistingDirectory(path, storeSymlinkMessage, "Incident draft store directory does not exist: ")
}

// Port of src/incident-store.ts:64 storeRoot.
func storeRoot(storeDirectory string) string {
	return jsexc.Must(safedir.ResolveTrustedSystemPath(storeDirectory))
}

// Port of src/incident-store.ts:68 draftDirectory.
func draftDirectory(storeDirectory string) string {
	return nodefs.Join(storeRoot(storeDirectory), "drafts")
}

// Port of src/incident-store.ts:72 draftPath.
func draftPath(storeDirectory, draftID string) string {
	id := parse(str(draftID), witness.IncidentDraftIDSchema).Str()
	root := storeRoot(storeDirectory)
	path := nodefs.Resolve(draftDirectory(root), id+".json")
	nested := jsexc.Must(safedir.RelativeTrustedSystemPath(root, path))
	if nested == "" || strings.HasPrefix(nested, "..") || strings.Contains(nested, ":") {
		throw("Incident draft path escaped its configured store")
	}
	return path
}

// WriteDraft ports writeIncidentDraft (src/incident-store.ts:88): it
// persists a valid draft as canonical, write-once JSON (flag "wx", mode
// 0o600) and returns its path.
func WriteDraft(storeDir string, draft jsjson.Value) (string, error) {
	var path string
	err := jsexc.Try(func() { path = writeIncidentDraft(storeDir, draft) })
	return path, err
}

func writeIncidentDraft(storeDirectory string, input jsjson.Value) string {
	parsed := parse(input, DraftSchema)
	verified := verifyIncidentDraft(parsed)
	if !verified.Get("valid").Bool() {
		throw(jsexc.Concat("Refusing to persist an invalid incident draft: ", errorsOf(verified)))
	}
	directory := draftDirectory(storeDirectory)
	ensureSafeDirectory(directory)
	path := draftPath(storeDirectory, parsed.Get("draftId").Str())
	text := jsexc.Concat(jsexc.Must(canonical.CanonicalJSON(parsed)), "\n")
	if err := nodefs.WriteFileUTF8(path, text, 0o600, true); err != nil {
		if nodefs.IsExist(err) {
			throw(jsexc.Concat("Immutable incident draft already exists: ", path))
		}
		jsexc.Throw(err)
	}
	return path
}

// ReadDraft ports readIncidentDraft (src/incident-store.ts:106): it reads a
// complete stored draft only after checking directory and file safety, and
// returns the parsed, verified draft. ReadDraftPath also returns its path.
func ReadDraft(storeDir, id string) (jsjson.Value, error) {
	draft, _, err := ReadDraftPath(storeDir, id)
	return draft, err
}

// ReadDraftPath is ReadDraft with the stored path (StoredIncidentDraft.path).
func ReadDraftPath(storeDir, id string) (draft jsjson.Value, path string, err error) {
	err = jsexc.Try(func() { draft, path = readIncidentDraft(storeDir, id) })
	return
}

func readIncidentDraft(storeDirectory, draftID string) (jsjson.Value, string) {
	directory := draftDirectory(storeDirectory)
	assertSafeExistingDirectory(directory)
	path := draftPath(storeDirectory, draftID)
	info := jsexc.Must(nodefs.Lstat(path))
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		throw(jsexc.Concat("Incident draft must be a regular non-symlink file: ", path))
	}
	value, err := readJSON(path)
	if err != nil {
		throw(jsexc.Concat("Incident draft is not valid JSON: ", jsexc.Message(err)))
	}
	parsed := parse(value, DraftSchema)
	verified := verifyIncidentDraft(parsed)
	if !verified.Get("valid").Bool() {
		throw(jsexc.Concat("Stored incident draft failed verification: ", errorsOf(verified)))
	}
	if parsed.Get("draftId").Str() != draftID {
		throw("Stored incident draft identifier does not match the requested identifier")
	}
	return parsed, path
}

// readJSON is JSON.parse(readFileSync(path, "utf8")) inside TS's try: a
// read failure is caught there too.
func readJSON(path string) (jsjson.Value, error) {
	text, err := nodefs.ReadText(path)
	if err != nil {
		return jsjson.Value{}, err
	}
	return jsjson.Parse(text)
}
