// Package snapgc ports src/snapshot-gc.ts (the quarantined turn-snapshot
// object store's garbage collection) and src/git-snapshot.ts (the clean
// Git sidecar snapshot).
package snapgc

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/turnsnap"
)

// Result ports SnapshotGcResult (src/snapshot-gc.ts:14).
type Result struct {
	RepositoryRoot, ObjectDirectory string
	RemovedEntries                  int
	Status                          string   // "PURGED" or "ALREADY_EMPTY"
	ReferencingPaths                []string // never nil
}

// gitObjectID is GIT_OBJECT_ID (src/snapshot-gc.ts:22).
var gitObjectID = jsre.MustCompile(`\b([0-9a-f]{40})\b`, "gi")

var (
	bucketName   = jsre.MustCompile(`^[0-9a-f]{2}$`, "i")
	looseRest    = jsre.MustCompile(`^[0-9a-f]{38}$`, "")
	bundleDir    = jsre.MustCompile(`(?:^|\/)(?:recordings|turn-proof-bundles|git-proof-bundles|bundles|proofs|turn-proofs|git-proofs)\/`, "")
	retainedName = jsre.MustCompile(`(?:ledger|investigation|manifest|ROOT\.sha256|hashes\.txt)$`, "i")
)

// maxReferenceScanBytes is the 8 MiB size above which a file is not read
// (src/snapshot-gc.ts:121).
const maxReferenceScanBytes = 8 * 1024 * 1024

// asciiLower is toLowerCase for the names it is applied to: a name that
// holds anything but ASCII fails the hex test either way (no non-ASCII
// character lower-cases into [0-9a-f]).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// countEntriesRecursive ports src/snapshot-gc.ts:24: regular files by
// Dirent type, recursing into directories; links and special files are not
// counted.
func countEntriesRecursive(directory string) (int, error) {
	if !nodefs.Exists(directory) {
		return 0, nil
	}
	entries, err := nodefs.ReadDirents(directory)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		path := nodefs.Join(directory, entry.Name)
		if entry.IsDir {
			n, err := countEntriesRecursive(path)
			if err != nil {
				return 0, err
			}
			count += n
			continue
		}
		if entry.IsFile {
			count++
		}
	}
	return count, nil
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	parts := strings.Split(text, "\n")
	for i, p := range parts {
		if i < len(parts)-1 {
			parts[i] = strings.TrimSuffix(p, "\r")
		}
	}
	return parts
}

// removeAlternatesEntry ports src/snapshot-gc.ts:38: the quarantine entry
// (and blank lines) leave .git/objects/info/alternates, which is removed
// when nothing else remains. existsSync follows links, so a dangling link is
// left alone; any other link, or a non-file, is refused.
func removeAlternatesEntry(repositoryRoot string) error {
	alternatesPath := nodefs.Join(turnsnap.PrimaryObjectDirectory(repositoryRoot), "info", "alternates")
	if !nodefs.Exists(alternatesPath) {
		return nil
	}
	info, err := nodefs.Lstat(alternatesPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(jsexc.Concat("Refusing to edit non-regular Git alternates file: ", alternatesPath))
	}
	text, err := nodefs.ReadText(alternatesPath)
	if err != nil {
		return err
	}
	var remaining []string
	for _, line := range splitLines(text) {
		if line = jsstr.Trim(line); line != "" && line != turnsnap.SnapshotAlternatesEntry {
			remaining = append(remaining, line)
		}
	}
	if len(remaining) == 0 {
		return nodefs.RemoveAll(alternatesPath)
	}
	return nodefs.WriteFileUTF8(alternatesPath, jsexc.Join(remaining, "\n")+"\n", 0o666, false)
}

// ListQuarantinedObjectIDs ports listQuarantinedObjectIds
// (src/snapshot-gc.ts:57): the loose object ids under objectDirectory,
// buckets and then names in byte order. A bucket is a real directory (by
// Dirent type) named by two hex digits, any case; a name is 38 hex digits,
// any case, not ending in .tmp (SHA-256 names are ignored). Ids are lower
// case.
func ListQuarantinedObjectIDs(objectDirectory string) ([]string, error) {
	ids := []string{}
	if !nodefs.Exists(objectDirectory) {
		return ids, nil
	}
	entries, err := nodefs.ReadDirents(objectDirectory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir || entry.Name == "info" || entry.Name == "pack" || !bucketName.Test(entry.Name) {
			continue
		}
		prefix := asciiLower(entry.Name)
		names, err := nodefs.ReadDirNames(nodefs.Join(objectDirectory, entry.Name))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if strings.HasSuffix(name, ".tmp") {
				continue
			}
			rest := asciiLower(name)
			if !looseRest.Test(rest) {
				continue
			}
			ids = append(ids, prefix+rest)
		}
	}
	return ids, nil
}

// walkRegularFiles ports src/snapshot-gc.ts:76. Only the top directory's
// readdirSync can throw: each entry's lstat, and the recursion into a
// subdirectory, run inside a try whose errors are ignored.
func walkRegularFiles(directory string, into []string) ([]string, error) {
	if !nodefs.Exists(directory) {
		return into, nil
	}
	entries, err := nodefs.ReadDirents(directory)
	if err != nil {
		return into, err
	}
	for _, entry := range entries {
		path := nodefs.Join(directory, entry.Name)
		info, err := nodefs.Lstat(path)
		if err != nil {
			continue // ignore raced/unreadable entries under .faultline
		}
		mode := info.Mode()
		switch {
		case !mode.IsRegular() && !mode.IsDir():
			// a link, or a special file
		case mode.IsDir():
			// A failure below is caught here, but what was pushed into the
			// shared array before it stays.
			into, _ = walkRegularFiles(path, into)
		default:
			into = append(into, path)
		}
	}
	return into, nil
}

// FindReferences ports findQuarantinedObjectReferences
// (src/snapshot-gc.ts:99): the ledger and bundle files under .faultline
// (by relative path) of at most 8 MiB whose text names one of ids as a
// whole 40-hex word, any case, sorted with localeCompare.
func FindReferences(repositoryRoot string, ids map[string]bool) ([]string, error) {
	referencing := []string{}
	if len(ids) == 0 {
		return referencing, nil
	}
	root := nodefs.Resolve(repositoryRoot)
	faultlineRoot := nodefs.Join(root, ".faultline")
	if !nodefs.Exists(faultlineRoot) {
		return referencing, nil
	}
	files, err := walkRegularFiles(faultlineRoot, nil)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, filePath := range files {
		relativePath := strings.ReplaceAll(nodefs.Relative(nodefs.Resolve(repositoryRoot), filePath), `\`, "/")
		if !bundleDir.Test(relativePath) && !retainedName.Test(relativePath) && !strings.HasSuffix(relativePath, ".json") {
			continue
		}
		info, err := nodefs.Lstat(filePath)
		if err != nil || info.Size() > maxReferenceScanBytes {
			continue
		}
		body, err := nodefs.ReadText(filePath)
		if err != nil {
			continue
		}
		for _, m := range gitObjectID.FindAll(body) {
			if ids[asciiLower(*m.Groups[0])] {
				if !seen[relativePath] {
					seen[relativePath] = true
					referencing = append(referencing, relativePath)
				}
				break
			}
		}
	}
	return canonical.SortLocale(referencing), nil
}

// Purge ports purgeFaultlineSnapshotObjects (src/snapshot-gc.ts:143). The
// store is the literal <resolve(repository)>/.git/faultline/objects (no
// Git lookup). It refuses while .faultline files still name a quarantined
// object, unless force.
func Purge(repository string, force bool) (Result, error) {
	repositoryRoot := nodefs.Resolve(repository)
	objectDirectory := turnsnap.ObjectDirectory(repositoryRoot)
	if !nodefs.Exists(objectDirectory) {
		if err := removeAlternatesEntry(repositoryRoot); err != nil {
			return Result{}, err
		}
		return Result{RepositoryRoot: repositoryRoot, ObjectDirectory: objectDirectory, Status: "ALREADY_EMPTY", ReferencingPaths: []string{}}, nil
	}
	quarantined, err := ListQuarantinedObjectIDs(objectDirectory)
	if err != nil {
		return Result{}, err
	}
	set := map[string]bool{}
	for _, id := range quarantined {
		set[id] = true
	}
	referencingPaths, err := FindReferences(repositoryRoot, set)
	if err != nil {
		return Result{}, err
	}
	if len(referencingPaths) > 0 && !force {
		named := jsexc.Join(referencingPaths[:min(5, len(referencingPaths))], ", ")
		more := ""
		if len(referencingPaths) > 5 {
			more = " (+" + strconv.Itoa(len(referencingPaths)-5) + " more)"
		}
		return Result{}, errors.New(jsexc.Concat("Refusing to purge quarantined turn-snapshot objects: still referenced by ", strconv.Itoa(len(referencingPaths)),
			" path(s) under .faultline (", named, more, "). ",
			"Re-run with --force after confirming those ledgers/turn bundles no longer need the trees, or archive them first."))
	}
	removedEntries, err := countEntriesRecursive(objectDirectory)
	if err != nil {
		return Result{}, err
	}
	if err := nodefs.RemoveAll(objectDirectory); err != nil {
		return Result{}, err
	}
	if err := removeAlternatesEntry(repositoryRoot); err != nil {
		return Result{}, err
	}
	status := "PURGED"
	if removedEntries == 0 {
		status = "ALREADY_EMPTY"
	}
	return Result{RepositoryRoot: repositoryRoot, ObjectDirectory: objectDirectory, RemovedEntries: removedEntries, Status: status, ReferencingPaths: referencingPaths}, nil
}
