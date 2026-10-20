package turnsnap

import (
	"os"
	"time"

	"github.com/Mizore66/faultline/internal/allowlist"
	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	s "github.com/Mizore66/faultline/internal/schema"
)

// CaptureOptions are the CaptureTurnTreeSnapshotOptions FaultLine's call
// sites pass (src/turn-snapshot.ts:305).
type CaptureOptions struct {
	// SessionCachePath is sessionCachePath; "" is undefined (no cache).
	SessionCachePath string
	// TrackedFilesOnly never stages untracked paths.
	TrackedFilesOnly bool
	// OnWarning receives each accepted warning, in order, before the
	// capture time is read.
	OnWarning func(string)
	// Sleep is the quiescence delay; nil blocks for it (blockingSleep).
	Sleep func(time.Duration)

	// Test seams (the TS runGit, now, maxQuiescenceAttempts and caps).
	runGit      gitRunner
	now         func() time.Time
	maxAttempts int
	caps        caps
}

// RepoCachePath ports src/turn-snapshot.ts:802 turnSnapshotRepoCachePath:
// the repository-level cache under Git's common directory, which `fl init`
// pre-warms and the Codex sidecar reuses.
func RepoCachePath(repositoryRoot string) (path string, err error) {
	defer jsexc.Catch(func(e error) { path, err = "", e })
	root := nodefs.Resolve(repositoryRoot)
	reported, err := defaultRunner(root, []string{"rev-parse", "--git-common-dir"}, runOpts{})
	if err != nil {
		return "", err
	}
	return nodefs.Join(nodefs.Resolve(root, jsstr.Trim(reported)), "faultline", "turn-snapshot-cache.json"), nil
}

// blockingSleep ports src/turn-snapshot.ts:288.
func blockingSleep(d time.Duration) {
	if d <= 0 {
		return
	}
	time.Sleep(d)
}

// signSnapshot ports src/turn-snapshot.ts:290 signTurnTreeSnapshot.
func signSnapshot(unsigned jsjson.Value) (jsjson.Value, error) {
	parsed, err := bundle.ParseValue(unsigned, gitproof.UnsignedTurnTreeSnapshotSchema)
	if err != nil {
		return jsjson.Value{}, err
	}
	digest, err := canonical.DigestJSON(parsed)
	if err != nil {
		return jsjson.Value{}, err
	}
	signed := jsjson.NewObj()
	for _, k := range parsed.Obj().Keys() {
		signed.Set(k, parsed.Obj().Field(k))
	}
	signed.Set("digest", jsjson.MakeString(digest))
	return bundle.ParseValue(jsjson.MakeObject(signed), gitproof.TurnTreeSnapshotSchema)
}

var (
	cacheHash     = s.String(s.Regex(s.Pattern(`^sha256:[a-f0-9]{64}$`), "Expected a sha256 digest"))
	cacheObjectID = s.String(s.Regex(s.Pattern(`^[a-f0-9]{40}(?:[a-f0-9]{24})?$`), "Expected a Git object id"))

	// src/turn-snapshot.ts:327 PathCacheEntrySchema.
	pathCacheEntrySchema = s.Object(
		s.F("status", s.String(s.MinLength(1), s.MaxLength(8))),
		s.F("contentDigest", cacheHash),
		s.F("mode", s.Enum("100644", "100755")),
	).Strict()

	// src/turn-snapshot.ts:334 TurnSnapshotSessionCacheSchema.
	sessionCacheSchema = s.Object(
		s.F("schemaVersion", s.LiteralString("faultline.turn-snapshot-cache.v2")),
		s.F("headCommit", cacheObjectID),
		s.F("treeDigest", cacheObjectID),
		s.F("policyDigest", cacheHash),
		s.F("allowlistDigest", cacheHash),
		s.F("contentFingerprint", cacheHash),
		s.F("deletedPaths", s.Array(s.String(), s.MaxItems(2_000))),
		s.F("paths", s.Record(pathCacheEntrySchema)),
		s.F("pathDigests", s.Record(cacheHash)),
		s.F("capturedAt", gitproof.CanonicalTimestampSchema),
	).Strict()
)

// loadSessionCache ports src/turn-snapshot.ts:772: any read, parse or
// schema failure is no cache.
func loadSessionCache(path string) (jsjson.Value, bool) {
	if path == "" || !nodefs.Exists(path) {
		return jsjson.Value{}, false
	}
	var cache jsjson.Value
	err := jsexc.Try(func() {
		v, err := bundle.ParseFile(path, sessionCacheSchema)
		if err != nil {
			jsexc.Throw(err)
		}
		cache = v
	})
	if err != nil {
		return jsjson.Value{}, false
	}
	return cache, true
}

// writeSessionCache ports src/turn-snapshot.ts:781.
func writeSessionCache(path string, cache jsjson.Value) error {
	if path == "" {
		return nil
	}
	if err := nodefs.MkdirAll(nodefs.Dirname(path), 0o777); err != nil {
		return err
	}
	return nodefs.WriteFileUTF8(path, jsjson.StringifyIndent(cache, "  ")+"\n", 0o666, false)
}

type scanContext struct {
	allowlist               allowlist.Allowlist
	allowlistDigest         string
	previousPathDigests     *jsjson.Obj
	previousAllowlistDigest *string
}

type throwaway struct {
	treeDigest  string
	warnings    []string
	pathDigests *jsjson.Obj
}

func trimmed(runGit gitRunner, root string, args ...string) (string, error) {
	out, err := runGit(root, args, runOpts{})
	return jsstr.Trim(out), err
}

// writeThrowaway ports src/turn-snapshot.ts:992 writeThrowawayTreeDigest:
// a clean worktree with no ignore-filtered tracked path reuses
// HEAD^{tree}; otherwise the plan's paths are staged incrementally into a
// temporary index seeded from HEAD^{tree}, with objects quarantined.
func writeThrowaway(runGit gitRunner, root string, o *CaptureOptions, c caps, scan scanContext, statusPorcelain string) (throwaway, error) {
	rules, err := LoadIgnoreRules(root)
	if err != nil {
		return throwaway{}, err
	}
	if statusPorcelain == "" {
		listed, err := runGit(root, []string{"ls-files", "-z"}, runOpts{})
		if err != nil {
			return throwaway{}, err
		}
		ignoredTracked := false
		for _, rel := range splitNullPaths(listed) {
			if IsIgnored(rel, rules) && !IsProtectedDescriptor(rel) {
				ignoredTracked = true
			}
		}
		if !ignoredTracked {
			headTree, err := trimmed(runGit, root, "rev-parse", "HEAD^{tree}")
			if err != nil {
				return throwaway{}, err
			}
			if !gitObjectID.MatchString(headTree) {
				return throwaway{}, &Error{Message: "Git did not return a valid HEAD tree object id for the clean worktree fast path."}
			}
			warning := "Reused HEAD^{tree} for clean worktree with no ignore-filtered tracked paths."
			if o.TrackedFilesOnly {
				warning = "Reused HEAD^{tree} for clean tracked worktree (scale fast path)."
			}
			return throwaway{treeDigest: headTree, warnings: []string{warning}, pathDigests: jsjson.NewObj()}, nil
		}
	}

	// Gates 1-4 run here, before any temporary-index object write.
	p, err := planPaths(root, runGit, planOptions{
		trackedFilesOnly:        o.TrackedFilesOnly,
		caps:                    c,
		allowlist:               &scan.allowlist,
		allowlistDigest:         &scan.allowlistDigest,
		previousPathDigests:     scan.previousPathDigests,
		previousAllowlistDigest: scan.previousAllowlistDigest,
	})
	if err != nil {
		return throwaway{}, err
	}
	dirty, err := computeDirtyFingerprint(root, statusPorcelain, o.TrackedFilesOnly, rules, c.fileBytes)
	if err != nil {
		return throwaway{}, err
	}
	dirtyPaths := map[string]bool{}
	for _, k := range dirty.paths.Keys() {
		dirtyPaths[k] = true
	}

	// Always seed from HEAD^{tree}, never from a previous dirty turn tree.
	baseTree, err := trimmed(runGit, root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return throwaway{}, err
	}
	if !gitObjectID.MatchString(baseTree) {
		return throwaway{}, &Error{Message: "Git did not return a valid HEAD tree object id for incremental turn staging."}
	}
	basePaths, err := listTreePaths(runGit, root, baseTree)
	if err != nil {
		return throwaway{}, err
	}
	delta := computeIndexDelta(baseTree, basePaths, p.paths, root, dirtyPaths)

	temporaryIndexPath := nodefs.Join(nodefs.Tmpdir(), "faultline-turn-tree-"+clock.NewUUID()+".index")
	objectEnv, err := objectWriteEnv(root)
	if err != nil {
		return throwaway{}, err
	}
	objectEnv = append(objectEnv, "GIT_INDEX_FILE="+temporaryIndexPath)
	result, err := stageAndWrite(runGit, root, delta, objectEnv, c.fileBytes)
	// finally: rmSync(temporaryIndexPath, { force: true }); its failure wins.
	if rmErr := nodefs.RemoveAll(temporaryIndexPath); rmErr != nil {
		return throwaway{}, rmErr
	}
	if err != nil {
		return throwaway{}, err
	}
	return throwaway{treeDigest: result, warnings: p.warnings, pathDigests: p.pathDigests}, nil
}

// stageAndWrite is the try block of src/turn-snapshot.ts:1067-1114.
func stageAndWrite(runGit gitRunner, root string, delta indexDelta, objectEnv []string, maxFileBytes int) (string, error) {
	env := runOpts{env: objectEnv}
	if _, err := runGit(root, []string{"read-tree", delta.baseTree}, env); err != nil {
		return "", err
	}
	for _, rel := range delta.removePaths {
		if _, err := runGit(root, []string{"update-index", "--force-remove", "--", rel}, env); err != nil {
			return "", err
		}
	}
	for _, rel := range delta.updatePaths {
		absolute := absolutePath(root, rel)
		stat, err := nodefs.Lstat(absolute)
		if err != nil {
			return "", err
		}
		if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
			return "", &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: path is not a regular file: ", rel)}
		}
		if stat.Size() > int64(maxFileBytes) {
			return "", &Error{Message: jsexc.Concat("Refusing turn-tree snapshot: ", rel, " is ", itoa(int(stat.Size())), " bytes (max ", itoa(maxFileBytes), " per file).")}
		}
		mode := modeOf(stat)
		// Hash via stdin so path-based clean filters never run.
		content, err := nodefs.ReadBytes(absolute)
		if err != nil {
			return "", err
		}
		blob, err := runGit(root, []string{"hash-object", "-w", "--stdin"}, runOpts{env: objectEnv, input: content, hasInput: true})
		if err != nil {
			return "", err
		}
		blob = jsstr.Trim(blob)
		if !gitObjectID.MatchString(blob) {
			return "", &Error{Message: jsexc.Concat("git hash-object --stdin did not return a blob id for ", rel)}
		}
		if _, err := runGit(root, []string{"update-index", "--add", "--cacheinfo", jsexc.Concat(mode, ",", blob, ",", rel)}, env); err != nil {
			return "", err
		}
	}
	treeDigest, err := runGit(root, []string{"write-tree"}, env)
	if err != nil {
		return "", err
	}
	treeDigest = jsstr.Trim(treeDigest)
	if !gitObjectID.MatchString(treeDigest) {
		return "", &Error{Message: "Git did not return a valid tree object id for this turn tree snapshot."}
	}
	return treeDigest, nil
}

// Capture ports src/turn-snapshot.ts:1141 captureTurnTreeSnapshot: it
// records { headCommit, treeDigest, dirty, statusDigest } for whatever is
// on disk, proving quiescence with two matching throwaway trees bracketed
// by status samples (or a delayed re-check of a matching session cache),
// and returns the signed snapshot and the accepted warnings. Failures are
// *Error, or the Node error a filesystem step threw.
func Capture(repository string, o CaptureOptions) (snapshot jsjson.Value, warnings []string, err error) {
	defer jsexc.Catch(func(e error) { snapshot, warnings, err = jsjson.Value{}, nil, e })
	return capture(repository, o)
}

func capture(repository string, o CaptureOptions) (jsjson.Value, []string, error) {
	runGit := o.runGit
	if runGit == nil {
		runGit = defaultRunner
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = blockingSleep
	}
	now := o.now
	if now == nil {
		now = clock.Now
	}
	maxAttempts := o.maxAttempts
	if maxAttempts == 0 {
		maxAttempts = MaxQuiescenceAttempts
	}
	if maxAttempts < 1 {
		return jsjson.Value{}, nil, &Error{Message: "Turn tree snapshot quiescence attempt budget must be a positive integer."}
	}
	c := o.caps.withDefaults()
	delay := time.Duration(QuiescenceDelayMs) * time.Millisecond

	requestedRoot := nodefs.Resolve(repository)
	top, err := trimmed(runGit, requestedRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	root := nodefs.Resolve(top)
	sampleStatus := func() (string, error) { return runGit(root, append([]string{}, StatusArgs...), runOpts{}) }

	a, err := allowlist.Load(root)
	if err != nil {
		return jsjson.Value{}, nil, &Error{Message: jsexc.Concat("Could not load secret allowlist: ", jsexc.Message(err))}
	}
	allowlistDigest := allowlist.ConfigDigest(a)
	policy, err := policyDigest(root, allowlistDigest, o.TrackedFilesOnly, c)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	previous, hasPrevious := loadSessionCache(o.SessionCachePath)
	rules, err := LoadIgnoreRules(root)
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	fingerprint := func(status string) (dirtyFingerprint, error) {
		return computeDirtyFingerprint(root, status, o.TrackedFilesOnly, rules, c.fileBytes)
	}

	var (
		accepted                   bool
		acceptedTree, acceptedHead string
		acceptedStatus             string
		acceptedWarnings           []string
		acceptedPathDigests        *jsjson.Obj
		acceptedDirty              dirtyFingerprint
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		headBefore, err := trimmed(runGit, root, "rev-parse", "HEAD")
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		if !gitObjectID.MatchString(headBefore) {
			return jsjson.Value{}, nil, &Error{Message: "Git did not return a resolvable HEAD commit for this turn tree snapshot."}
		}
		statusBeforeA, err := sampleStatus()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		dirtyBefore, err := fingerprint(statusBeforeA)
		if err != nil {
			return jsjson.Value{}, nil, err
		}

		if hasPrevious &&
			previous.Get("headCommit").Str() == headBefore &&
			previous.Get("policyDigest").Str() == policy &&
			previous.Get("contentFingerprint").Str() == dirtyBefore.contentFingerprint &&
			treeObjectExists(runGit, root, previous.Get("treeDigest").Str()) {
			sleep(delay)
			statusAfterCache, err := sampleStatus()
			if err != nil {
				return jsjson.Value{}, nil, err
			}
			headAfterCache, err := trimmed(runGit, root, "rev-parse", "HEAD")
			if err != nil {
				return jsjson.Value{}, nil, err
			}
			dirtyAfter, err := fingerprint(statusAfterCache)
			if err != nil {
				return jsjson.Value{}, nil, err
			}
			if headAfterCache == headBefore &&
				dirtyAfter.contentFingerprint == dirtyBefore.contentFingerprint &&
				treeObjectExists(runGit, root, previous.Get("treeDigest").Str()) {
				accepted = true
				acceptedTree = previous.Get("treeDigest").Str()
				acceptedStatus = statusAfterCache
				acceptedHead = headAfterCache
				acceptedWarnings = []string{}
				acceptedPathDigests = previous.Get("pathDigests").Obj()
				acceptedDirty = dirtyAfter
				break
			}
		}

		firstScan := scanContext{allowlist: a, allowlistDigest: allowlistDigest}
		if hasPrevious {
			prevAllowlist := previous.Get("allowlistDigest").Str()
			firstScan.previousPathDigests = previous.Get("pathDigests").Obj()
			firstScan.previousAllowlistDigest = &prevAllowlist
		}
		first, err := writeThrowaway(runGit, root, &o, c, firstScan, statusBeforeA)
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		statusAfterA, err := sampleStatus()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		if statusBeforeA != statusAfterA {
			continue
		}
		dirtyAfterA, err := fingerprint(statusAfterA)
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		if dirtyAfterA.contentFingerprint != dirtyBefore.contentFingerprint {
			continue
		}

		sleep(delay)

		statusBeforeB, err := sampleStatus()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		second, err := writeThrowaway(runGit, root, &o, c, scanContext{
			allowlist: a, allowlistDigest: allowlistDigest,
			previousPathDigests: first.pathDigests, previousAllowlistDigest: &allowlistDigest,
		}, statusBeforeB)
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		statusAfterB, err := sampleStatus()
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		headAfter, err := trimmed(runGit, root, "rev-parse", "HEAD")
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		if statusBeforeB != statusAfterB {
			continue
		}
		if statusAfterA != statusBeforeB || statusAfterA != statusAfterB {
			continue
		}
		if first.treeDigest != second.treeDigest {
			continue
		}
		if headBefore != headAfter {
			continue
		}
		dirtyAfterB, err := fingerprint(statusAfterB)
		if err != nil {
			return jsjson.Value{}, nil, err
		}
		if dirtyAfterB.contentFingerprint != dirtyBefore.contentFingerprint {
			continue
		}

		accepted = true
		acceptedTree = second.treeDigest
		acceptedStatus = statusAfterB
		acceptedHead = headAfter
		acceptedWarnings = second.warnings
		acceptedPathDigests = second.pathDigests
		acceptedDirty = dirtyAfterB
		break
	}

	if !accepted {
		return jsjson.Value{}, nil, &Error{Message: jsexc.Concat("FaultLine could not prove turn-tree quiescence after ", itoa(maxAttempts),
			" dual-tree attempt(s) and refused a torn turn tree snapshot.")}
	}

	if o.OnWarning != nil {
		for _, warning := range acceptedWarnings {
			o.OnWarning(warning)
		}
	}

	capturedAt := clock.ISO(now())
	cache := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString("faultline.turn-snapshot-cache.v2"),
		"headCommit", jsjson.MakeString(acceptedHead),
		"treeDigest", jsjson.MakeString(acceptedTree),
		"policyDigest", jsjson.MakeString(policy),
		"allowlistDigest", jsjson.MakeString(allowlistDigest),
		"contentFingerprint", jsjson.MakeString(acceptedDirty.contentFingerprint),
		"deletedPaths", stringArray(acceptedDirty.deletedPaths),
		"paths", jsjson.MakeObject(acceptedDirty.paths),
		"pathDigests", jsjson.MakeObject(acceptedPathDigests),
		"capturedAt", jsjson.MakeString(capturedAt))
	if err := writeSessionCache(o.SessionCachePath, cache); err != nil {
		return jsjson.Value{}, nil, err
	}

	snapshot, err := signSnapshot(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SnapshotVersion),
		"repositoryRoot", jsjson.MakeString(root),
		"headCommit", jsjson.MakeString(acceptedHead),
		"treeDigest", jsjson.MakeString(acceptedTree),
		"capturedAt", jsjson.MakeString(capturedAt),
		"dirty", jsjson.MakeBool(acceptedStatus != ""),
		"statusDigest", jsjson.MakeString("sha256:"+canonical.SHA256Hex(acceptedStatus))))
	if err != nil {
		return jsjson.Value{}, nil, err
	}
	return snapshot, acceptedWarnings, nil
}
