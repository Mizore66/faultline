package turnsnap

import (
	"os"
	"runtime"
	"strings"

	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
)

// gitObjectID is src/turn-snapshot.ts:57 GitObjectIdSchema's pattern.
var gitObjectID = jsre.MustCompile(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`, "")

// runOpts are a Git call's env additions (KEY=value pairs spread after the
// hardened environment) and its optional stdin.
type runOpts struct {
	env      []string
	input    []byte
	hasInput bool
}

// gitRunner is TurnSnapshotGitRunner (src/turn-snapshot.ts:160). Failures
// are *Error.
type gitRunner func(root string, args []string, o runOpts) (string, error)

// hardenedEnv ports src/turn-snapshot.ts:177 hardenedGitEnvironment. Its
// keys and order are gitinv.HardenedGitEnvironment's with no GIT_DIR,
// GIT_WORK_TREE or GIT_INDEX_FILE (src/git-materialization.ts:72 builds the
// same literal), Windows' SystemRoot, ComSpec and PATHEXT included.
func hardenedEnv() []string { return gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{}) }

// defaultRunner ports src/turn-snapshot.ts:253 defaultTurnSnapshotGitRunner:
// spawnSync("git", [...DOCTOR_SAFE_GIT_CONFIG, -c core.attributesFile=…,
// -C, root, ...args], { encoding: "utf8", env: {...hardened, ...env},
// input }) with Node's default 1 MiB maxBuffer.
func defaultRunner(root string, args []string, o runOpts) (string, error) {
	argv := append(safegit.TurnSnapshot(), "-C", root)
	argv = append(argv, args...)
	env := append(hardenedEnv(), o.env...)
	if runtime.GOOS != "windows" {
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{Env: env, Input: o.input, HasInput: o.hasInput, MaxBuffer: 1024 * 1024})
	if r.Err != nil || r.Status == nil || *r.Status != 0 {
		detail := jsstr.DecodeUTF8(r.Stderr)
		if r.Err != nil {
			detail = jsexc.Concat(detail, r.Err.Error())
		}
		detail = jsstr.Trim(detail)
		if detail == "" {
			status := "unknown"
			if r.Status != nil {
				status = jsjson.FormatNumber(float64(*r.Status))
			}
			detail = "exit " + status
		}
		return "", &Error{Message: jsexc.Concat("FaultLine could not safely capture a turn tree snapshot: ", detail)}
	}
	return jsstr.DecodeUTF8(r.Stdout), nil
}

// absolutePath is join(root, ...relativePath.split("/")).
func absolutePath(root, relativePath string) string {
	return nodefs.Join(append([]string{root}, strings.Split(relativePath, "/")...)...)
}

// ObjectDirectory ports src/turn-snapshot.ts:194
// faultlineSnapshotObjectDirectory.
func ObjectDirectory(root string) string {
	return nodefs.Join(nodefs.Resolve(root), ".git", "faultline", "objects")
}

// PrimaryObjectDirectory ports src/turn-snapshot.ts:198
// primaryGitObjectDirectory.
func PrimaryObjectDirectory(root string) string {
	return nodefs.Join(nodefs.Resolve(root), ".git", "objects")
}

// ensureObjectStore ports src/turn-snapshot.ts:207
// ensureFaultlineSnapshotObjectStore.
func ensureObjectStore(repositoryRoot string) (string, error) {
	root := nodefs.Resolve(repositoryRoot)
	quarantine := ObjectDirectory(root)
	if err := nodefs.MkdirAll(nodefs.Join(quarantine, "info"), 0o700); err != nil {
		return "", err
	}
	if err := nodefs.MkdirAll(nodefs.Join(quarantine, "pack"), 0o700); err != nil {
		return "", err
	}
	alternatesPath := nodefs.Join(PrimaryObjectDirectory(root), "info", "alternates")
	if err := nodefs.MkdirAll(nodefs.Join(PrimaryObjectDirectory(root), "info"), 0o700); err != nil {
		return "", err
	}
	existing := ""
	if nodefs.Exists(alternatesPath) {
		text, err := nodefs.ReadText(alternatesPath)
		if err != nil {
			return "", err
		}
		existing = text
	}
	var lines []string
	found := false
	for _, line := range splitLines(existing) {
		if line = jsstr.Trim(line); line != "" {
			lines = append(lines, line)
			found = found || line == SnapshotAlternatesEntry
		}
	}
	if !found {
		text := jsexc.Join(append(lines, SnapshotAlternatesEntry), "\n") + "\n"
		if err := nodefs.WriteFileUTF8(alternatesPath, text, 0o666, false); err != nil {
			return "", err
		}
	}
	return quarantine, nil
}

// objectWriteEnv ports src/turn-snapshot.ts:234
// turnSnapshotObjectWriteEnvironment.
func objectWriteEnv(repositoryRoot string) ([]string, error) {
	root := nodefs.Resolve(repositoryRoot)
	quarantine, err := ensureObjectStore(root)
	if err != nil {
		return nil, err
	}
	return []string{"GIT_OBJECT_DIRECTORY=" + quarantine, "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + PrimaryObjectDirectory(root)}, nil
}

// splitNullPaths ports src/turn-snapshot.ts:682.
func splitNullPaths(value string) []string {
	var out []string
	for _, entry := range strings.Split(value, "\x00") {
		if entry = NormalizePath(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// fullFileContentDigest ports src/turn-snapshot.ts:439. The stream's
// open and read failures are Node errors (callers catch them), the rest
// *Error.
func fullFileContentDigest(absolute string, maxFileBytes int) (string, error) {
	if maxFileBytes < 1 {
		return "", &Error{Message: "Turn snapshot max file bytes must be a positive integer."}
	}
	stat, err := nodefs.Lstat(absolute)
	if err != nil {
		return "", &Error{Message: jsexc.Concat("FaultLine could not stat ", absolute, " for content fingerprinting.")}
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		return "", &Error{Message: jsexc.Concat("Refusing to fingerprint non-regular path: ", absolute)}
	}
	if stat.Size() > int64(maxFileBytes) {
		return "", &Error{Message: jsexc.Concat("Refusing turn-tree fingerprint: ", absolute, " is ", itoa(int(stat.Size())),
			" bytes (max ", itoa(maxFileBytes), " per file).")}
	}
	data, err := nodefs.ReadBytes(absolute)
	if err != nil {
		return "", err
	}
	if len(data) > maxFileBytes {
		return "", &Error{Message: jsexc.Concat("Refusing turn-tree fingerprint: ", absolute, " grew past ", itoa(maxFileBytes), " bytes while hashing.")}
	}
	return "sha256:" + canonical.SHA256HexBytes(data), nil
}

func itoa(n int) string { return jsjson.FormatNumber(float64(n)) }

func stringArray(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, s := range items {
		out[i] = jsjson.MakeString(s)
	}
	return jsjson.MakeArray(out)
}

// ignorePolicyDigest ports src/turn-snapshot.ts:482. A .faultlineignore
// that cannot be read fails with Node's error.
func ignorePolicyDigest(root string) (string, error) {
	ignorePath := nodefs.Join(root, ".faultlineignore")
	userIgnore := ""
	if nodefs.Exists(ignorePath) {
		text, err := nodefs.ReadText(ignorePath)
		if err != nil {
			return "", err
		}
		userIgnore = text
	}
	return canonical.DigestJSON(jsjson.MustObject(
		"defaults", stringArray(DefaultIgnorePatterns),
		"userIgnore", jsjson.MakeString(userIgnore),
		"protectedDescriptors", stringArray(gitinv.EnvironmentDescriptorFiles)))
}

// caps are the resolved maxFileBytes, maxTotalBytes and maxFileCount.
type caps struct{ fileBytes, totalBytes, fileCount int }

func (c caps) withDefaults() caps {
	if c.fileBytes == 0 {
		c.fileBytes = MaxFileBytes
	}
	if c.totalBytes == 0 {
		c.totalBytes = MaxTotalBytes
	}
	if c.fileCount == 0 {
		c.fileCount = MaxFileCount
	}
	return c
}

// policyDigest ports src/turn-snapshot.ts:492 snapshotPolicyDigest.
func policyDigest(root, allowlistDigest string, trackedFilesOnly bool, c caps) (string, error) {
	ignore, err := ignorePolicyDigest(root)
	if err != nil {
		return "", err
	}
	return canonical.DigestJSON(jsjson.MustObject(
		"ignorePolicy", jsjson.MakeString(ignore),
		"allowlistDigest", jsjson.MakeString(allowlistDigest),
		"trackedFilesOnly", jsjson.MakeBool(trackedFilesOnly),
		"maxFileBytes", jsjson.MakeNumber(float64(c.fileBytes)),
		"maxTotalBytes", jsjson.MakeNumber(float64(c.totalBytes)),
		"maxFileCount", jsjson.MakeNumber(float64(c.fileCount))))
}

type porcelainPath struct{ status, path string }

// parsePorcelainStatusZ ports src/turn-snapshot.ts:513 (lengths and
// slices in UTF-16 units).
func parsePorcelainStatusZ(status string) []porcelainPath {
	var parts []string
	for _, p := range strings.Split(status, "\x00") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	var out []porcelainPath
	for index := 0; index < len(parts); index++ {
		record := jsstr.ToUTF16(parts[index])
		if len(record) < 3 {
			continue
		}
		xy := jsstr.FromUTF16(record[:2])
		pathPart := jsstr.FromUTF16(record[3:])
		if strings.HasPrefix(xy, "R") || strings.HasPrefix(xy, "C") {
			if index+1 >= len(parts) {
				continue
			}
			out = append(out, porcelainPath{xy, NormalizePath(parts[index+1])})
			index++
			continue
		}
		out = append(out, porcelainPath{xy, NormalizePath(pathPart)})
	}
	return out
}

// dirtyFingerprint is DirtyContentFingerprint. paths is the sorted
// record as TS rebuilds it with Object.fromEntries, so integer-like keys
// come first.
type dirtyFingerprint struct {
	contentFingerprint string
	deletedPaths       []string
	paths              *jsjson.Obj
}

func modeOf(info os.FileInfo) string {
	if info.Mode().Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// computeDirtyFingerprint ports src/turn-snapshot.ts:537
// computeDirtyContentFingerprint with its options given.
func computeDirtyFingerprint(root, statusPorcelain string, trackedFilesOnly bool, rules []Rule, maxFileBytes int) (dirtyFingerprint, error) {
	paths := jsjson.NewObj()
	deleted := []string{}
	for _, entry := range parsePorcelainStatusZ(statusPorcelain) {
		if trackedFilesOnly && entry.status == "??" {
			continue
		}
		if PathExcluded(entry.path, rules) {
			continue
		}
		absolute := absolutePath(root, entry.path)
		if strings.Contains(entry.status, "D") || !nodefs.Exists(absolute) {
			deleted = append(deleted, entry.path)
			continue
		}
		stat, err := nodefs.Lstat(absolute)
		if err != nil {
			deleted = append(deleted, entry.path)
			continue
		}
		if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
			continue
		}
		digest, err := fullFileContentDigest(absolute, maxFileBytes)
		if err != nil {
			if _, ok := err.(*Error); ok {
				return dirtyFingerprint{}, err
			}
			deleted = append(deleted, entry.path)
			continue
		}
		// `paths["__proto__"] = …` sets the prototype: no own entry.
		if entry.path != "__proto__" {
			paths.Set(entry.path, jsjson.MustObject("status", jsjson.MakeString(entry.status),
				"contentDigest", jsjson.MakeString(digest), "mode", jsjson.MakeString(modeOf(stat))))
		}
	}
	d := dirtyFingerprint{deletedPaths: canonical.SortLocale(deleted), paths: jsjson.NewObj()}
	for _, k := range canonical.SortLocale(paths.Keys()) {
		d.paths.Set(k, paths.Field(k))
	}
	fp, err := canonical.DigestJSON(jsjson.MustObject("paths", jsjson.MakeObject(d.paths), "deletedPaths", stringArray(d.deletedPaths)))
	if err != nil {
		return dirtyFingerprint{}, err
	}
	d.contentFingerprint = fp
	return d, nil
}

// treeObjectExists ports src/turn-snapshot.ts:588 gitTreeObjectExists:
// any failure, the object store's included, is false.
func treeObjectExists(runGit gitRunner, root, treeDigest string) bool {
	env, err := objectWriteEnv(root)
	if err != nil {
		return false
	}
	_, err = runGit(root, []string{"cat-file", "-e", treeDigest + "^{tree}"}, runOpts{env: env})
	return err == nil
}

// listTreePaths ports src/turn-snapshot.ts:603; the set keeps first-seen
// order.
func listTreePaths(runGit gitRunner, root, treeDigest string) ([]string, error) {
	env, err := objectWriteEnv(root)
	if err != nil {
		return nil, err
	}
	listed, err := runGit(root, []string{"ls-tree", "-r", "--name-only", "-z", treeDigest}, runOpts{env: env})
	if err != nil {
		return nil, err
	}
	return dedupe(splitNullPaths(listed)), nil
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

type indexDelta struct {
	baseTree                 string
	updatePaths, removePaths []string
}

// computeIndexDelta ports src/turn-snapshot.ts:626
// computeTurnSnapshotIndexDelta.
func computeIndexDelta(baseTree string, basePaths, planPaths []string, root string, dirtyPaths map[string]bool) indexDelta {
	var desiredExisting []string
	desired := map[string]bool{}
	for _, rel := range planPaths {
		stat, err := nodefs.Lstat(absolutePath(root, rel))
		if err != nil || stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
			continue
		}
		desiredExisting = append(desiredExisting, rel)
		desired[rel] = true
	}
	base := map[string]bool{}
	var remove []string
	for _, p := range basePaths {
		base[p] = true
		if !desired[p] {
			remove = append(remove, p)
		}
	}
	var update []string
	for _, p := range desiredExisting {
		if !base[p] || dirtyPaths[p] {
			update = append(update, p)
		}
	}
	return indexDelta{baseTree, canonical.SortLocale(update), canonical.SortLocale(remove)}
}

// planOptions are planTurnSnapshotPaths' options; nil pointers are
// undefined.
type planOptions struct {
	trackedFilesOnly        bool
	caps                    caps
	allowlist               *allowlist.Allowlist
	previousPathDigests     *jsjson.Obj
	previousAllowlistDigest *string
	allowlistDigest         *string
	skipSecretScan          bool
}

type candidate struct {
	relativePath string
	tracked      bool
	exists       bool
	bytes        int
}

type plan struct {
	paths               []string
	warnings            []string
	pathDigests         *jsjson.Obj
	secretScanPathCount int
}

// computePathDigests ports src/turn-snapshot.ts:804. "__proto__" is
// dropped: assigning it sets the record's prototype.
func computePathDigests(root string, candidates []candidate) (*jsjson.Obj, error) {
	out := jsjson.NewObj()
	for _, c := range candidates {
		if !c.exists {
			continue
		}
		digest, err := allowlist.FileContentDigest(absolutePath(root, c.relativePath), SecretScanBytes)
		if err != nil {
			return nil, err
		}
		if c.relativePath != "__proto__" {
			out.Set(c.relativePath, jsjson.MakeString(digest))
		}
	}
	return out, nil
}

// pathsNeedingSecretScan ports src/turn-snapshot.ts:817; nil is "all".
func pathsNeedingSecretScan(pathDigests, previous *jsjson.Obj, previousAllowlistDigest *string, allowlistDigest string) map[string]bool {
	if previous == nil || previousAllowlistDigest == nil || *previousAllowlistDigest != allowlistDigest {
		return nil
	}
	needing := map[string]bool{}
	for _, path := range pathDigests.Keys() {
		prev, ok := previous.Get(path)
		if !ok || prev.Kind() != jsjson.String || prev.Str() != pathDigests.Field(path).Str() {
			needing[path] = true
		}
	}
	return needing
}

// planPaths ports src/turn-snapshot.ts:837 planTurnSnapshotPaths: mode,
// then path, then caps, then the secret scan, before any object write.
func planPaths(root string, runGit gitRunner, o planOptions) (plan, error) {
	c := o.caps.withDefaults()
	rules, err := LoadIgnoreRules(root)
	if err != nil {
		return plan{}, err
	}
	var a allowlist.Allowlist
	if o.allowlist != nil {
		a = *o.allowlist
	} else {
		a, err = allowlist.Load(root)
		if err != nil {
			return plan{}, &Error{Message: jsexc.Concat("Could not load secret allowlist: ", jsexc.Message(err))}
		}
	}
	allowlistDigest := ""
	if o.allowlistDigest != nil {
		allowlistDigest = *o.allowlistDigest
	} else {
		allowlistDigest = allowlist.ConfigDigest(a)
	}

	listed, err := runGit(root, []string{"ls-files", "-z"}, runOpts{})
	if err != nil {
		return plan{}, err
	}
	tracked := dedupe(splitNullPaths(listed))
	isTracked := map[string]bool{}
	for _, p := range tracked {
		isTracked[p] = true
	}
	var untracked []string
	if !o.trackedFilesOnly {
		others, err := runGit(root, []string{"ls-files", "-z", "--others", "--exclude-standard"}, runOpts{})
		if err != nil {
			return plan{}, err
		}
		untracked = splitNullPaths(others)
	}

	warnings := []string{}
	var candidates []candidate
	for _, rel := range append(append([]string{}, tracked...), untracked...) {
		ignored := IsIgnored(rel, rules)
		protected := IsProtectedDescriptor(rel)
		if ignored && !protected {
			continue
		}
		if ignored && protected {
			warnings = append(warnings, jsexc.Concat("Protected environment descriptor remains snapshot-eligible despite .faultlineignore: ", rel))
		}
		exists, bytes := false, 0
		stat, err := nodefs.Lstat(absolutePath(root, rel))
		if err == nil {
			if stat.Mode()&os.ModeSymlink != 0 {
				return plan{}, &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: symbolic link is not snapshot-eligible: ", rel)}
			}
			if stat.Mode().IsRegular() {
				exists, bytes = true, int(stat.Size())
			} else if stat.IsDir() {
				continue
			}
		} else if !isTracked[rel] {
			// Missing path: tracked deletions are staged; vanished untracked noise is skipped.
			continue
		}
		if !isTracked[rel] && exists {
			warnings = append(warnings, jsexc.Concat("Including previously untracked file in turn-tree snapshot: ", rel))
		}
		candidates = append(candidates, candidate{rel, isTracked[rel], exists, bytes})
	}

	if len(candidates) > c.fileCount {
		return plan{}, &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: ", itoa(len(candidates)), " eligible files exceed the maximum of ", itoa(c.fileCount), ".")}
	}
	total := 0
	for _, cand := range candidates {
		if !cand.exists {
			continue
		}
		if cand.bytes > c.fileBytes {
			return plan{}, &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: ", cand.relativePath, " is ", itoa(cand.bytes), " bytes (max ", itoa(c.fileBytes), " per file).")}
		}
		total += cand.bytes
		if total > c.totalBytes {
			return plan{}, &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: eligible content exceeds the maximum total of ", itoa(c.totalBytes), " bytes.")}
		}
	}

	pathDigests, err := computePathDigests(root, candidates)
	if err != nil {
		return plan{}, err
	}
	scanned := 0
	if !o.skipSecretScan {
		needing := pathsNeedingSecretScan(pathDigests, o.previousPathDigests, o.previousAllowlistDigest, allowlistDigest)
		for _, cand := range candidates {
			if !cand.exists || (needing != nil && !needing[cand.relativePath]) {
				continue
			}
			scanned++
			if err := AssertNoSnapshotSecrets(cand.relativePath, absolutePath(root, cand.relativePath), cand.bytes, a); err != nil {
				return plan{}, err
			}
		}
	}
	rels := make([]string, len(candidates))
	for i, cand := range candidates {
		rels[i] = cand.relativePath
	}
	return plan{paths: canonical.SortLocale(rels), warnings: warnings, pathDigests: pathDigests, secretScanPathCount: scanned}, nil
}
