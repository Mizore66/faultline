package minimize

import (
	"encoding/base64"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/gitproof"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/collation"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/redaction"
	"github.com/Mizore66/faultline/internal/sandbox"
	"github.com/Mizore66/faultline/internal/schema"
	"github.com/Mizore66/faultline/internal/v8sort"
)

// PatchUnit is PatchUnitInternal (src/git-minimization.ts:385): a
// GitPatchUnit together with its exact patch bytes.
type PatchUnit struct {
	ID              string
	Ordinal         int
	ChangeKind      string
	Path            string
	PathBytesBase64 string
	PathDigest      string
	PatchDigest     string
	PatchBytes      int
	Bytes           []byte
}

// Value is the GitPatchUnit form, without the retained bytes.
func (u PatchUnit) Value() jsjson.Value {
	return jsjson.MustObject(
		"id", jsjson.MakeString(u.ID),
		"ordinal", jsjson.MakeNumber(float64(u.Ordinal)),
		"changeKind", jsjson.MakeString(u.ChangeKind),
		"path", jsjson.MakeString(u.Path),
		"pathBytesBase64", jsjson.MakeString(u.PathBytesBase64),
		"pathDigest", jsjson.MakeString(u.PathDigest),
		"patchDigest", jsjson.MakeString(u.PatchDigest),
		"patchBytes", jsjson.MakeNumber(float64(u.PatchBytes)),
		"binarySafe", jsjson.MakeBool(true),
	)
}

// attempt runs fn as the body of a JS try block: a thrown exception
// (jsexc) and a returned error are both what the catch clause sees.
func attempt(fn func() error) (err error) {
	if thrown := jsexc.Try(func() { err = fn() }); thrown != nil {
		return thrown
	}
	return err
}

func stringArray(items []string) jsjson.Value {
	values := make([]jsjson.Value, len(items))
	for i, item := range items {
		values[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(values)
}

func digestJSON(v jsjson.Value) string { return bundle.Must(canonical.DigestJSON(v)) }

// sha256Digest ports src/git-minimization.ts:466 for a JS string.
func sha256Digest(value string) string { return "sha256:" + canonical.SHA256Hex(value) }

// sha256BytesDigest is sha256Digest for a Buffer.
func sha256BytesDigest(b []byte) string { return "sha256:" + canonical.SHA256HexBytes(b) }

// emptyEnumeration ports src/git-minimization.ts:393.
func emptyEnumeration(note string) jsjson.Value {
	return jsjson.MustObject(
		"status", jsjson.MakeString("NOT_RUN"),
		"searchedCandidateSets", jsjson.MakeNumber(0),
		"maxPatchUnits", jsjson.MakeNumber(MaxEnumerationPatchUnits),
		"maxDistinctMinimalSets", jsjson.MakeNumber(MaxEnumeratedMinimalSets),
		"distinctMinimalSets", jsjson.MakeArray(nil),
		"note", jsjson.MakeString(note),
	)
}

// emptyInteraction ports src/git-minimization.ts:405.
func emptyInteraction(note string) jsjson.Value {
	return jsjson.MustObject("verdict", jsjson.MakeString("INCONCLUSIVE"), "evidence", jsjson.MakeNull(), "note", jsjson.MakeString(note))
}

// minimalityRecord ports src/git-minimization.ts:409.
func minimalityRecord(oneMinimal bool, reason string, enumeration, interaction jsjson.Value) jsjson.Value {
	return jsjson.MustObject(
		"oneMinimal", jsjson.MakeBool(oneMinimal),
		"reason", jsjson.MakeString(reason),
		"enumeration", enumeration,
		"nonMonotonicInteraction", interaction,
	)
}

// candidateKey ports src/git-minimization.ts:418.
func candidateKey(candidateIDs []string) string { return strings.Join(candidateIDs, "\x00") }

// boundedProperSubsets ports src/git-minimization.ts:422. The sort is
// Array.prototype.sort (stable) with length, then localeCompare.
func boundedProperSubsets(unitIDs []string) [][]string {
	var output [][]string
	count := 1 << len(unitIDs)
	for mask := 1; mask < count-1; mask++ {
		var subset []string
		for index, id := range unitIDs {
			if mask&(1<<index) != 0 {
				subset = append(subset, id)
			}
		}
		output = append(output, subset)
	}
	v8sort.Sort(output, func(left, right []string) int {
		if d := len(left) - len(right); d != 0 {
			return d
		}
		return collation.Compare(candidateKey(left), candidateKey(right))
	})
	return output
}

// outcomes is a JS Map<string, outcome>: it keeps insertion order and a
// repeated key keeps its first position.
type outcomes struct {
	keys   []string
	values map[string]string
}

func newOutcomes() *outcomes { return &outcomes{values: map[string]string{}} }

func (o *outcomes) set(key, value string) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *outcomes) get(key string) string { return o.values[key] }

// observedNonMonotonicInteraction ports src/git-minimization.ts:431.
func observedNonMonotonicInteraction(unitIDs []string, recorded *outcomes) jsjson.Value {
	var failures [][]string
	for _, key := range recorded.keys {
		if recorded.values[key] != "FAIL" {
			continue
		}
		if key == "" {
			failures = append(failures, nil)
			continue
		}
		failures = append(failures, strings.Split(key, "\x00"))
	}
	for leftIndex := 0; leftIndex < len(failures); leftIndex++ {
		left := failures[leftIndex]
		for rightIndex := leftIndex + 1; rightIndex < len(failures); rightIndex++ {
			right := failures[rightIndex]
			combined := map[string]bool{}
			for _, id := range left {
				combined[id] = true
			}
			for _, id := range right {
				combined[id] = true
			}
			var union []string
			for _, id := range unitIDs {
				if combined[id] {
					union = append(union, id)
				}
			}
			if len(union) == len(left) || len(union) == len(right) {
				continue
			}
			if recorded.get(candidateKey(union)) == "PASS" {
				return jsjson.MustObject(
					"verdict", jsjson.MakeString("NON_MONOTONIC_INTERACTION"),
					"evidence", jsjson.MustObject(
						"failingLeft", stringArray(left),
						"failingRight", stringArray(right),
						"passingUnion", stringArray(union),
					),
					"note", jsjson.MakeString("Two failing subsets have a recorded passing union; monotonicity is contradicted."),
				)
			}
		}
	}
	return jsjson.MustObject(
		"verdict", jsjson.MakeString("NOT_OBSERVED"),
		"evidence", jsjson.MakeNull(),
		"note", jsjson.MakeString("No recorded pair of failing subsets had a passing union."),
	)
}

// probe is the Probe of src/git-minimization.ts:459.
type probe struct {
	outcome string
	run     *jsjson.Value
	note    string
}

// expectedExecutionID ports expectedMinimizationExecutionId (src/git-minimization.ts:474).
func expectedExecutionID(run jsjson.Value) string {
	return digestJSON(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"nonce", run.Get("executionNonce"),
		"role", run.Get("role"),
		"roleAttempt", run.Get("roleAttempt"),
		"direction", run.Get("application", "direction"),
		"base", run.Get("application", "base"),
		"candidateUnitIds", run.Get("candidateUnitIds"),
		"frozenDigest", run.Get("frozenDigest"),
	))
}

// expectedRunID ports expectedMinimizationRunId (src/git-minimization.ts:486).
func expectedRunID(run jsjson.Value) string { return digestJSON(bundle.Without(run, "runId")) }

// evidencePreview is the result of redactedEvidencePreview.
type evidencePreview struct {
	value               string
	truncated, redacted bool
}

// redactedEvidencePreview ports src/git-minimization.ts:491.
func redactedEvidencePreview(value, stream string) evidencePreview {
	b := []byte(jsstr.ToUTF8(value))
	preview := jsstr.DecodeUTF8(b[:min(len(b), evidenceLogPreviewBytes)])
	safe, report := redaction.RedactText(preview, "$.minimization."+stream)
	return evidencePreview{safe, len(b) > evidenceLogPreviewBytes, report.Redacted}
}

// runGit ports src/git-minimization.ts:507 runGit without stdin.
func runGit(repository string, args []string) gitinv.GitRun {
	return gitinv.RunHardenedGit(args, gitinv.HardenedOptions{Repository: repository})
}

// resolveRepositoryRoot ports src/git-minimization.ts:515.
func resolveRepositoryRoot(repository string) (string, error) {
	requested := nodefs.Resolve(repository)
	root, err := gitinv.HardenedGitText(requested, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return "", err
	}
	if !nodefs.IsAbsolute(root) {
		return "", errors.New("Git did not return an absolute repository root.")
	}
	return nodefs.Resolve(root), nil
}

// resolveCommit ports src/git-minimization.ts:522.
func resolveCommit(repository, revision string) (string, error) {
	value, err := gitinv.HardenedGitText(repository, []string{"rev-parse", "--verify", "--end-of-options", revision + "^{commit}"})
	if err != nil {
		return "", err
	}
	if !gitObjectID.MatchString(value) {
		return "", errors.New(jsexc.Concat("Git returned an invalid commit object id for revision ", revision, "."))
	}
	return value, nil
}

// counterfactualState is a GitCounterfactualState (src/git-minimization.ts:113).
type counterfactualState struct{ commit, tree string }

func (s counterfactualState) value() jsjson.Value {
	return jsjson.MustObject("commit", jsjson.MakeString(s.commit), "tree", jsjson.MakeString(s.tree))
}

// resolveState ports src/git-minimization.ts:528.
func resolveState(repository, revision string) (counterfactualState, error) {
	commit, err := resolveCommit(repository, revision)
	if err != nil {
		return counterfactualState{}, err
	}
	tree, err := gitinv.HardenedGitText(repository, []string{"rev-parse", "--verify", "--end-of-options", commit + "^{tree}"})
	if err != nil {
		return counterfactualState{}, err
	}
	if !gitObjectID.MatchString(tree) {
		return counterfactualState{}, errors.New(jsexc.Concat("Git returned an invalid tree object id for revision ", revision, "."))
	}
	return counterfactualState{commit, tree}, nil
}

// nulParts ports src/git-minimization.ts:535.
func nulParts(value []byte) ([][]byte, error) {
	var parts [][]byte
	offset := 0
	for offset < len(value) {
		ending := -1
		for i := offset; i < len(value); i++ {
			if value[i] == 0 {
				ending = i
				break
			}
		}
		if ending < 0 {
			return nil, errors.New("Git -z output ended without a NUL terminator.")
		}
		parts = append(parts, value[offset:ending])
		offset = ending + 1
	}
	return parts, nil
}

// GIT_OBJECT_ID (src/git-minimization.ts:49).
var gitObjectID = regexp.MustCompile(gitObjectIDSource)

// changeKind ports src/git-minimization.ts:547 on the status bytes; TS
// reads the first unit of their "ascii" decoding, which masks the high bit.
func changeKind(status []byte) string {
	if len(status) == 0 {
		return "UNKNOWN"
	}
	switch status[0] & 0x7f {
	case 'A':
		return "ADD"
	case 'D':
		return "DELETE"
	case 'M':
		return "MODIFY"
	case 'T':
		return "TYPECHANGE"
	}
	return "UNKNOWN"
}

// assertUtf8Path ports src/git-minimization.ts:557.
func assertUtf8Path(pathBytes []byte) (string, error) {
	path := jsstr.DecodeUTF8(pathBytes)
	if path == "" || jsstr.ToUTF8(path) != string(pathBytes) {
		return "", errors.New("Git contains a non-UTF-8 or empty path; FaultLine refuses to pass it through a lossy argv conversion.")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("Git path unexpectedly contains a NUL byte.")
	}
	return path, nil
}

// derivePatchUnits ports src/git-minimization.ts:570.
func derivePatchUnits(repository string, before, after counterfactualState) ([]PatchUnit, error) {
	status := runGit(repository, []string{
		"diff",
		"--name-status",
		"-z",
		"--no-renames",
		"--no-ext-diff",
		before.commit,
		after.commit,
	})
	if status.ExitCode == nil || *status.ExitCode != 0 || status.Error != "" {
		return nil, errors.New(jsexc.Concat("Could not enumerate changed Git paths: ", status.Detail("Git failed.")))
	}
	parts, err := nulParts(status.Stdout)
	if err != nil {
		return nil, err
	}
	if len(parts)%2 != 0 {
		return nil, errors.New("Git --name-status -z output was malformed.")
	}
	var output []PatchUnit
	for index := 0; index < len(parts); index += 2 {
		// TS refuses a missing element (`!statusBytes || !pathBytes`); an
		// empty Buffer is truthy, and the even count rules out a missing one.
		statusBytes, pathBytes := parts[index], parts[index+1]
		path, err := assertUtf8Path(pathBytes)
		if err != nil {
			return nil, err
		}
		pathspec := ":(literal)" + path
		patch := runGit(repository, []string{
			"diff",
			"--binary",
			"--full-index",
			"--no-color",
			"--no-renames",
			"--no-ext-diff",
			before.commit,
			after.commit,
			"--",
			pathspec,
		})
		if patch.ExitCode == nil || *patch.ExitCode != 0 || patch.Error != "" || len(patch.Stdout) == 0 {
			return nil, errors.New(jsexc.Concat("Could not derive an exact binary patch for ", path, ": ", patch.Detail("Git returned no patch.")))
		}
		patchDigest := sha256BytesDigest(patch.Stdout)
		pathDigest := sha256BytesDigest(pathBytes)
		ordinal := len(output)
		id := digestJSON(jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(SchemaVersion),
			"ordinal", jsjson.MakeNumber(float64(ordinal)),
			"pathDigest", jsjson.MakeString(pathDigest),
			"patchDigest", jsjson.MakeString(patchDigest),
		))
		output = append(output, PatchUnit{
			ID:              id,
			Ordinal:         ordinal,
			ChangeKind:      changeKind(statusBytes),
			Path:            path,
			PathBytesBase64: base64.StdEncoding.EncodeToString(pathBytes),
			PathDigest:      pathDigest,
			PatchDigest:     patchDigest,
			PatchBytes:      len(patch.Stdout),
			Bytes:           patch.Stdout,
		})
	}
	return output, nil
}

// planRequestForWorktree ports src/git-minimization.ts:635.
func planRequestForWorktree(sandboxValue jsjson.Value, worktree string, frozenWitness jsjson.Value) jsjson.Value {
	o := jsjson.NewObj()
	o.Set("witness", jsjson.MustObject(
		"digest", frozenWitness.Get("frozenDigest"),
		"command", frozenWitness.Get("proposal", "witness", "command"),
	))
	o.Set("sourceDirectory", jsjson.MakeString(worktree))
	o.Set("mode", sandboxValue.Get("mode"))
	for _, key := range []string{"image", "environment", "allowedEnvironment", "limits"} {
		if v := sandboxValue.Get(key); v.Kind() != jsjson.Undefined {
			o.Set(key, v)
		}
	}
	return jsjson.MakeObject(o)
}

// applicationFact ports src/git-minimization.ts:648. A nil result is TS's
// absent process result (its empty buffers).
func applicationFact(direction string, base counterfactualState, selectedUnitIDs []string, patchBytes []byte,
	status string, result *gitinv.GitRun, errorText *string) jsjson.Value {
	var stdout, stderr []byte
	if result != nil {
		stdout, stderr = result.Stdout, result.Stderr
	}
	errorValue := jsjson.MakeNull()
	if errorText != nil {
		errorValue = jsjson.MakeString(*errorText)
	}
	return jsexc.Must(bundle.ParseValue(jsjson.MustObject(
		"direction", jsjson.MakeString(direction),
		"base", base.value(),
		"selectedUnitIds", stringArray(selectedUnitIDs),
		"candidatePatchDigest", jsjson.MakeString(sha256BytesDigest(patchBytes)),
		"candidatePatchBytes", jsjson.MakeNumber(float64(len(patchBytes))),
		"status", jsjson.MakeString(status),
		"stdoutDigest", jsjson.MakeString(sha256BytesDigest(stdout)),
		"stdoutBytes", jsjson.MakeNumber(float64(len(stdout))),
		"stderrDigest", jsjson.MakeString(sha256BytesDigest(stderr)),
		"stderrBytes", jsjson.MakeNumber(float64(len(stderr))),
		"error", errorValue,
	), PatchApplicationSchema))
}

// persistedResult ports src/git-minimization.ts:675.
func persistedResult(result jsjson.Value) jsjson.Value {
	stdout, stderr := result.Get("stdout").Str(), result.Get("stderr").Str()
	stdoutPreview := redactedEvidencePreview(stdout, "stdout")
	stderrPreview := redactedEvidencePreview(stderr, "stderr")
	return jsjson.MustObject(
		"kind", result.Get("kind"),
		"executor", result.Get("executor"),
		"verdict", result.Get("verdict"),
		"reason", result.Get("reason"),
		"exitCode", result.Get("exitCode"),
		"signal", result.Get("signal"),
		"outputTruncated", result.Get("outputTruncated"),
		"stdoutDigest", jsjson.MakeString(sha256Digest(stdout)),
		"stdoutBytes", jsjson.MakeNumber(float64(len(jsstr.ToUTF8(stdout)))),
		"stdoutPreview", jsjson.MakeString(stdoutPreview.value),
		"stdoutPreviewTruncated", jsjson.MakeBool(stdoutPreview.truncated),
		"stdoutRedacted", jsjson.MakeBool(stdoutPreview.redacted),
		"stderrDigest", jsjson.MakeString(sha256Digest(stderr)),
		"stderrBytes", jsjson.MakeNumber(float64(len(jsstr.ToUTF8(stderr)))),
		"stderrPreview", jsjson.MakeString(stderrPreview.value),
		"stderrPreviewTruncated", jsjson.MakeBool(stderrPreview.truncated),
		"stderrRedacted", jsjson.MakeBool(stderrPreview.redacted),
	)
}

// outcomeForSandbox ports src/git-minimization.ts:698.
func outcomeForSandbox(result jsjson.Value) string {
	// Injected runners can support a developer's observed counterfactual
	// search, but finalize and certification still refuse to label them proof.
	if result.Get("kind").Str() != "DOCKER_ISOLATED" {
		return "UNRESOLVED"
	}
	switch result.Get("verdict").Str() {
	case "PASS":
		return "PASS"
	case "FAIL":
		return "FAIL"
	}
	return "UNRESOLVED"
}

// outcomeNote ports src/git-minimization.ts:707.
func outcomeNote(result jsjson.Value) string {
	if result.Get("kind").Str() == "UNSAFE_LOCAL" {
		return "Unsafe local execution is explicitly inapplicable and cannot be proof."
	}
	if result.Get("reason").Str() == "SANDBOX_UNAVAILABLE" {
		return "Docker was unavailable; this run is unresolved."
	}
	if result.Get("verdict").Str() == "ERROR" {
		return jsexc.Concat("Sandbox execution is unresolved: ", result.Get("reason").Str(), ".")
	}
	return jsexc.Concat("Docker witness returned ", result.Get("verdict").Str(), ".")
}

// chunk ports src/git-minimization.ts:714.
func chunk(values []string, count int) [][]string {
	size := int(math.Ceil(float64(len(values)) / float64(count)))
	var output [][]string
	for index := 0; index < len(values); index += size {
		output = append(output, append([]string(nil), values[index:min(index+size, len(values))]...))
	}
	return output
}

// selectedPatch ports src/git-minimization.ts:721.
func selectedPatch(units []PatchUnit, candidateIDs []string) ([]PatchUnit, []byte, error) {
	selected := map[string]bool{}
	for _, id := range candidateIDs {
		selected[id] = true
	}
	var unitsSelected []PatchUnit
	var bytes []byte
	for _, unit := range units {
		if selected[unit.ID] {
			unitsSelected = append(unitsSelected, unit)
			bytes = append(bytes, unit.Bytes...)
		}
	}
	if len(unitsSelected) != len(selected) {
		return nil, nil, errors.New("Candidate references a patch unit that does not belong to this Git diff.")
	}
	return unitsSelected, bytes, nil
}

func unitIDs(units []PatchUnit) []string {
	out := make([]string, len(units))
	for i, unit := range units {
		out[i] = unit.ID
	}
	return out
}

// SplitModifyUnitIntoHunks ports src/git-minimization.ts:733
// splitModifyUnitIntoHunks: nil is TS's null.
func SplitModifyUnitIntoHunks(unit PatchUnit) []PatchUnit {
	if unit.ChangeKind != "MODIFY" {
		return nil
	}
	text := jsstr.DecodeUTF8(unit.Bytes)
	if strings.Contains(text, "GIT binary patch") || strings.Contains(text, "Binary files ") {
		return nil
	}
	lines := strings.Split(text, "\n")
	var header []string
	var hunks [][]string
	var current []string
	started := false
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			if started {
				hunks = append(hunks, current)
			}
			current, started = []string{line}, true
			continue
		}
		if started {
			current = append(current, line)
			continue
		}
		header = append(header, line)
	}
	if started {
		hunks = append(hunks, current)
	}
	if len(hunks) < 2 {
		return nil
	}
	headerText := strings.TrimRight(strings.Join(header, "\n"), "\n")
	var output []PatchUnit
	for hunkIndex, hunk := range hunks {
		hunkBody := strings.Join(hunk, "\n")
		trailing := "\n"
		if strings.HasSuffix(hunkBody, "\n") {
			trailing = ""
		}
		b := []byte(jsstr.ToUTF8(headerText + "\n" + hunkBody + trailing))
		patchDigest := sha256BytesDigest(b)
		id := digestJSON(jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(SchemaVersion),
			"kind", jsjson.MakeString("hunk"),
			"parentPathDigest", jsjson.MakeString(unit.PathDigest),
			"hunkIndex", jsjson.MakeNumber(float64(hunkIndex)),
			"patchDigest", jsjson.MakeString(patchDigest),
		))
		output = append(output, PatchUnit{
			ID:              id,
			Ordinal:         unit.Ordinal*1000 + hunkIndex,
			ChangeKind:      "MODIFY",
			Path:            unit.Path,
			PathBytesBase64: unit.PathBytesBase64,
			PathDigest:      unit.PathDigest,
			PatchDigest:     patchDigest,
			PatchBytes:      len(b),
			Bytes:           b,
		})
	}
	return output
}

// verifyAppliedPatchScope ports src/git-minimization.ts:784. The returned
// string is TS's message; "" is its null.
func verifyAppliedPatchScope(materialized gitinv.MaterializedTree, base counterfactualState, selectedPaths []string) string {
	changed := materialized.RunGit([]string{"diff", "--cached", "--name-only", "-z", "--no-renames", base.commit})
	if changed.ExitCode == nil || *changed.ExitCode != 0 || changed.Error != "" {
		return jsexc.Concat("Could not verify applied patch scope: ", changed.Detail("Git failed."))
	}
	parts, err := nulParts(changed.Stdout)
	var paths []string
	if err == nil {
		for _, part := range parts {
			path, pathErr := assertUtf8Path(part)
			if pathErr != nil {
				err = pathErr
				break
			}
			paths = append(paths, path)
		}
	}
	if err != nil {
		return jsexc.Concat("Could not verify applied patch scope: ", jsexc.Message(err))
	}
	allowed := map[string]bool{}
	for _, path := range selectedPaths {
		allowed[path] = true
	}
	var unexpected []string
	for _, path := range paths {
		if !allowed[path] {
			unexpected = append(unexpected, path)
		}
	}
	if len(unexpected) > 0 {
		return jsexc.Concat("Git apply changed paths outside the selected patch units: ", jsexc.Join(unexpected, ", "))
	}
	return ""
}

// witnessSummary ports src/git-minimization.ts:810.
func witnessSummary(frozenWitness jsjson.Value, verification gitproof.WitnessVerification) jsjson.Value {
	frozenDigest := jsjson.MakeNull()
	if verification.FrozenDigest != nil {
		frozenDigest = jsjson.MakeString(*verification.FrozenDigest)
	}
	witnessDigest := jsjson.MakeNull()
	if verification.Valid {
		witnessDigest = frozenWitness.Get("witnessDigest")
	}
	return jsjson.MustObject(
		"valid", jsjson.MakeBool(verification.Valid),
		"errors", verification.ErrorsValue(),
		"frozenDigest", frozenDigest,
		"witnessDigest", witnessDigest,
		"externalDigestStatus", jsjson.MakeString(verification.ExternalDigestStatus),
		"approval", verification.ApprovalValue(),
	)
}

// emptyCertificate ports src/git-minimization.ts:821.
func emptyCertificate(direction, expectedOutcome, note string) jsjson.Value {
	return jsjson.MustObject(
		"direction", jsjson.MakeString(direction),
		"expectedOutcome", jsjson.MakeString(expectedOutcome),
		"requiredExecutions", jsjson.MakeNumber(CertificationExecutions),
		"runIds", jsjson.MakeArray(nil),
		"executionIds", jsjson.MakeArray(nil),
		"status", jsjson.MakeString("NOT_RUN"),
		"note", jsjson.MakeString(note),
	)
}

func defaultSufficiency() jsjson.Value {
	return emptyCertificate("FORWARD_FROM_BEFORE", "FAIL", "No sufficiency certification was run.")
}

func defaultNecessity() jsjson.Value {
	return emptyCertificate("REVERSE_FROM_AFTER", "PASS", "No necessity certification was run.")
}

// executionTrustFor ports src/git-minimization.ts:832 for DOCKER_ISOLATED,
// the only sandbox mode a request can carry.
func executionTrustFor(hasInjectedRunner bool) string {
	if hasInjectedRunner {
		return "INJECTED_RUNNER"
	}
	return "NATIVE_DOCKER"
}

// emptyProof ports src/git-minimization.ts:837.
func emptyProof(reason, executionTrust string) jsjson.Value {
	return jsjson.MustObject(
		"requiresDockerIsolation", jsjson.MakeBool(true),
		"dockerIsolated", jsjson.MakeBool(executionTrust == "NATIVE_DOCKER"),
		"executionTrust", jsjson.MakeString(executionTrust),
		"sufficiencyCertified", jsjson.MakeBool(false),
		"necessityCertified", jsjson.MakeBool(false),
		"isProof", jsjson.MakeBool(false),
		"reason", jsjson.MakeString(reason),
	)
}

// resultFields are the fields baseResult takes from its caller.
type resultFields struct{ repository, before, after, witness jsjson.Value }

// baseResult ports src/git-minimization.ts:853.
func baseResult(status string, maxExecutions float64, errs []string, f resultFields, executionTrust string) (jsjson.Value, error) {
	reason := "Minimization did not produce proof."
	if len(errs) > 0 {
		reason = errs[0]
	}
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"recorder", jsjson.MakeString("git-diff-counterfactual-minimization"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", f.repository,
		"before", f.before,
		"after", f.after,
		"witness", f.witness,
		"budget", jsjson.MustObject("usedExecutions", jsjson.MakeNumber(0), "maxExecutions", jsjson.MakeNumber(maxExecutions)),
		"patchUnits", jsjson.MakeArray(nil),
		"candidateUnitIds", jsjson.MakeArray(nil),
		"attempts", jsjson.MakeArray(nil),
		"runs", jsjson.MakeArray(nil),
		"minimality", minimalityRecord(false, "No candidate was established.",
			emptyEnumeration("Enumeration did not run."), emptyInteraction("No subset outcome matrix was retained.")),
		"certification", jsjson.MustObject("sufficiency", defaultSufficiency(), "necessity", defaultNecessity()),
		"proof", emptyProof(reason, executionTrust),
		"errors", stringArray(errs),
	), ResultSchema)
}

// MinimizeGitDiff ports src/git-minimization.ts:899 minimizeGitDiff.
// request is the JSON form of GitMinimizationRequest without its runner;
// runner nil is the native Docker runner, and injected marks a
// caller-supplied runner (request.runner !== undefined). An error is what
// the TS promise rejects with.
func MinimizeGitDiff(request jsjson.Value, runner sandbox.Runner, injected bool) (result jsjson.Value, err error) {
	if thrown := jsexc.Try(func() { result, err = minimizeGitDiff(request, runner, injected) }); thrown != nil {
		return jsjson.Value{}, thrown
	}
	return result, err
}

// run holds the mutable state of one minimizeGitDiff call (TS closures).
type run struct {
	input         jsjson.Value
	frozenWitness jsjson.Value
	repository    string
	before, after counterfactualState
	runner        sandbox.Runner
	injected      bool
	units         []PatchUnit

	runs           []jsjson.Value
	attempts       []jsjson.Value
	errs           []string
	usedExecutions int
	maxExecutions  int
	roleAttempts   map[string]int
	summary        jsjson.Value
}

// nextRoleAttempt ports src/git-minimization.ts:977.
func (r *run) nextRoleAttempt(role string) int {
	next := r.roleAttempts[role] + 1
	r.roleAttempts[role] = next
	return next
}

// executeCandidate ports src/git-minimization.ts:983.
func (r *run) executeCandidate(direction string, candidateIDs []string, role string) probe {
	if r.usedExecutions >= r.maxExecutions {
		return probe{outcome: "NOT_RUN", note: "Execution budget is exhausted."}
	}
	roleAttempt := r.nextRoleAttempt(role)
	base := r.before
	if direction != "FORWARD_FROM_BEFORE" {
		base = r.after
	}
	patchUnits, patchBytes, err := selectedPatch(r.units, candidateIDs)
	if err != nil {
		jsexc.Throw(err)
	}
	startedEpoch := clock.NowMillis()
	startedAt := isoMillis(startedEpoch)
	tempRoot := jsexc.Must(nodefs.MkdtempAsync(nodefs.Join(nodefs.Tmpdir(), "faultline-git-minimization-")))
	materializationName := "worktree-" + clock.NewUUID()
	worktree := nodefs.Join(tempRoot, "worktrees", materializationName)
	worktreeDigest := sha256Digest(worktree)
	var (
		materialized *gitinv.MaterializedTree
		overlays     = jsjson.MakeArray(nil)
		application  *jsjson.Value
		sandboxAudit = jsjson.MakeNull()
		execResult   = jsjson.MakeNull()
		outcome      = "UNRESOLVED"
		note         string
	)
	bodyErr := attempt(func() error {
		tree, err := gitinv.MaterializeGitTree(r.repository, base.commit, tempRoot, materializationName)
		if err != nil {
			return err
		}
		materialized = &tree
		if len(patchBytes) == 0 {
			fact := applicationFact(direction, base, nil, patchBytes, "NO_PATCHES", nil, nil)
			application = &fact
		} else {
			args := []string{"apply"}
			if direction == "REVERSE_FROM_AFTER" {
				args = append(args, "--reverse")
			}
			// The external temporary index starts at the exact base commit.
			// Requiring index application prevents Git from silently resolving
			// a selected path's file/directory obstruction outside the subset.
			args = append(args, "--binary", "--index", "--whitespace=nowarn", "--")
			applied := tree.RunGitStdin(args, patchBytes)
			selectedIDs := unitIDs(patchUnits)
			if applied.ExitCode == nil || *applied.ExitCode != 0 || applied.Error != "" {
				detail := applied.Detail("Git apply failed.")
				fact := applicationFact(direction, base, selectedIDs, patchBytes, "CONFLICT", &applied, &detail)
				application = &fact
				note = jsexc.Concat("Patch application is unresolved: ", detail)
			} else {
				selectedPaths := make([]string, len(patchUnits))
				for i, unit := range patchUnits {
					selectedPaths[i] = unit.Path
				}
				if scopeError := verifyAppliedPatchScope(tree, base, selectedPaths); scopeError != "" {
					fact := applicationFact(direction, base, selectedIDs, patchBytes, "CONFLICT", &applied, &scopeError)
					application = &fact
					note = jsexc.Concat("Patch application is unresolved: ", scopeError)
				} else {
					fact := applicationFact(direction, base, selectedIDs, patchBytes, "APPLIED", &applied, nil)
					application = &fact
				}
			}
		}
		if status := application.Get("status").Str(); status == "APPLIED" || status == "NO_PATCHES" {
			// A failure leaves TS's `overlays` at [] for the unresolved run.
			materializedOverlays, err := gitinv.MaterializeFrozenOverlays(worktree, r.frozenWitness, *materialized)
			if err != nil {
				return err
			}
			overlays = materializedOverlays
			var plan sandbox.Plan
			planErr := attempt(func() error {
				var err error
				plan, err = sandbox.CreatePlan(planRequestForWorktree(r.input.Get("sandbox"), worktree, r.frozenWitness))
				return err
			})
			if planErr != nil {
				note = jsexc.Concat("Sandbox plan could not be created: ", jsexc.Message(planErr))
			} else {
				sandboxAudit = sandbox.AuditPlan(plan)
				r.usedExecutions++
				execution := sandbox.Execute(plan, r.runner, r.injected)
				execResult = persistedResult(execution)
				outcome = outcomeForSandbox(execution)
				note = outcomeNote(execution)
			}
		}
		return nil
	})
	if bodyErr != nil {
		message := jsexc.Message(bodyErr)
		if application == nil {
			fact := applicationFact(direction, base, unitIDs(patchUnits), patchBytes, "ERROR", nil, &message)
			application = &fact
		}
		note = message
	}
	// finally
	if materialized != nil {
		if cleanupError := materialized.Cleanup(); cleanupError != nil {
			r.errs = append(r.errs, jsexc.Message(cleanupError))
		}
	}
	if err := nodefs.RemoveAll(tempRoot); err != nil {
		jsexc.Throw(err)
	}
	finishedEpoch := clock.NowMillis()
	finishedAt := isoMillis(finishedEpoch)
	executionNonce := clock.NewUUID()
	candidateValues := stringArray(unitIDs(patchUnits))
	executionID := digestJSON(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"nonce", jsjson.MakeString(executionNonce),
		"role", jsjson.MakeString(role),
		"roleAttempt", jsjson.MakeNumber(float64(roleAttempt)),
		"direction", jsjson.MakeString(direction),
		"base", base.value(),
		"candidateUnitIds", candidateValues,
		"frozenDigest", r.frozenWitness.Get("frozenDigest"),
	))
	if application == nil {
		message := "FaultLine did not produce a patch application result."
		fact := applicationFact(direction, base, unitIDs(patchUnits), patchBytes, "ERROR", nil, &message)
		application = &fact
	}
	if note == "" {
		note = "No sandbox execution was produced."
	}
	unsigned := jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"executionId", jsjson.MakeString(executionID),
		"executionNonce", jsjson.MakeString(executionNonce),
		"role", jsjson.MakeString(role),
		"roleAttempt", jsjson.MakeNumber(float64(roleAttempt)),
		"candidateUnitIds", candidateValues,
		"worktreeDigest", jsjson.MakeString(worktreeDigest),
		"frozenDigest", r.frozenWitness.Get("frozenDigest"),
		"witnessDigest", r.frozenWitness.Get("witnessDigest"),
		"startedAt", jsjson.MakeString(startedAt),
		"finishedAt", jsjson.MakeString(finishedAt),
		"durationMs", jsjson.MakeNumber(math.Max(0, float64(finishedEpoch-startedEpoch))),
		"overlays", overlays,
		"application", *application,
		"sandbox", sandboxAudit,
		"result", execResult,
		"outcome", jsjson.MakeString(outcome),
		"note", jsjson.MakeString(note),
	)
	signed := jsjson.NewObj()
	for _, key := range unsigned.Obj().Keys() {
		signed.Set(key, unsigned.Obj().Field(key))
	}
	signed.Set("runId", jsjson.MakeString(digestJSON(unsigned)))
	fact := jsexc.Must(bundle.ParseValue(jsjson.MakeObject(signed), RunFactSchema))
	r.runs = append(r.runs, fact)
	return probe{outcome: outcome, run: &fact, note: fact.Get("note").Str()}
}

// recordProbe ports src/git-minimization.ts:1113.
func (r *run) recordProbe(phase, direction string, candidateIDs []string, role string) probe {
	p := r.executeCandidate(direction, candidateIDs, role)
	runID := jsjson.MakeNull()
	if p.run != nil {
		runID = p.run.Get("runId")
	}
	r.attempts = append(r.attempts, jsexc.Must(bundle.ParseValue(jsjson.MustObject(
		"ordinal", jsjson.MakeNumber(float64(len(r.attempts)+1)),
		"phase", jsjson.MakeString(phase),
		"candidateUnitIds", stringArray(candidateIDs),
		"outcome", jsjson.MakeString(p.outcome),
		"runId", runID,
		"note", jsjson.MakeString(p.note),
	), AttemptSchema)))
	return p
}

// statusFromProbe ports src/git-minimization.ts:1130. "" is TS's null.
func statusFromProbe(p probe, expected string) string {
	var result jsjson.Value
	if p.run != nil {
		result = p.run.Get("result")
	}
	if result.Kind() == jsjson.Object {
		if result.Get("kind").Str() == "UNSAFE_LOCAL" {
			return "UNSAFE_LOCAL_INAPPLICABLE"
		}
		if result.Get("reason").Str() == "SANDBOX_UNAVAILABLE" {
			return "SANDBOX_UNAVAILABLE"
		}
		if result.Get("verdict").Str() == "ERROR" {
			return "EXECUTION_ERROR"
		}
	}
	if p.outcome == "UNRESOLVED" {
		return "EXECUTION_ERROR"
	}
	if p.outcome == "NOT_RUN" {
		return "BUDGET_EXHAUSTED"
	}
	if expected == "PASS" {
		return "BASELINE_NOT_PASS"
	}
	return "FULL_PATCH_NOT_FAIL"
}

// finalize ports src/git-minimization.ts:1139.
func (r *run) finalize(status string, candidateIDs []string, minimality, sufficiency, necessity jsjson.Value, reason string) (jsjson.Value, error) {
	sufficiencyCertified := sufficiency.Get("status").Str() == "CERTIFIED"
	necessityCertified := necessity.Get("status").Str() == "CERTIFIED"
	executionTrust := executionTrustFor(r.injected)
	proof := jsjson.MustObject(
		"requiresDockerIsolation", jsjson.MakeBool(true),
		"dockerIsolated", jsjson.MakeBool(executionTrust == "NATIVE_DOCKER"),
		"executionTrust", jsjson.MakeString(executionTrust),
		"sufficiencyCertified", jsjson.MakeBool(sufficiencyCertified),
		"necessityCertified", jsjson.MakeBool(necessityCertified),
		"isProof", jsjson.MakeBool(executionTrust == "NATIVE_DOCKER" && minimality.Get("oneMinimal").Bool() &&
			sufficiencyCertified && necessityCertified && len(r.errs) == 0),
		"reason", jsjson.MakeString(reason),
	)
	patchUnits := make([]jsjson.Value, len(r.units))
	for i, unit := range r.units {
		patchUnits[i] = unit.Value()
	}
	return bundle.ParseValue(jsjson.MustObject(
		"schemaVersion", jsjson.MakeString(SchemaVersion),
		"recorder", jsjson.MakeString("git-diff-counterfactual-minimization"),
		"nativeCodexInterception", jsjson.MakeBool(false),
		"status", jsjson.MakeString(status),
		"repository", jsjson.MakeString(r.repository),
		"before", r.before.value(),
		"after", r.after.value(),
		"witness", r.summary,
		"budget", jsjson.MustObject("usedExecutions", jsjson.MakeNumber(float64(r.usedExecutions)),
			"maxExecutions", jsjson.MakeNumber(float64(r.maxExecutions))),
		"patchUnits", jsjson.MakeArray(patchUnits),
		"candidateUnitIds", stringArray(candidateIDs),
		"attempts", jsjson.MakeArray(r.attempts),
		"runs", jsjson.MakeArray(r.runs),
		"minimality", minimality,
		"certification", jsjson.MustObject("sufficiency", sufficiency, "necessity", necessity),
		"proof", proof,
		"errors", stringArray(r.errs),
	), ResultSchema)
}

// certify ports src/git-minimization.ts:1434.
func (r *run) certify(phase, direction, expectedOutcome, role string, candidate []string) jsjson.Value {
	var runIDs, executionIDs []string
	for attemptIndex := 1; attemptIndex <= CertificationExecutions; attemptIndex++ {
		p := r.recordProbe(phase, direction, candidate, role)
		if p.run != nil {
			runIDs = append(runIDs, p.run.Get("runId").Str())
			executionIDs = append(executionIDs, p.run.Get("executionId").Str())
		}
		result := jsjson.Value{}
		if p.run != nil {
			result = p.run.Get("result")
		}
		if p.outcome != expectedOutcome || result.Kind() != jsjson.Object || result.Get("kind").Str() != "DOCKER_ISOLATED" ||
			result.Get("executor").Str() != "NATIVE_DOCKER" {
			return jsjson.MustObject(
				"direction", jsjson.MakeString(direction),
				"expectedOutcome", jsjson.MakeString(expectedOutcome),
				"requiredExecutions", jsjson.MakeNumber(CertificationExecutions),
				"runIds", stringArray(runIDs),
				"executionIds", stringArray(executionIDs),
				"status", jsjson.MakeString("NOT_CERTIFIED"),
				"note", jsjson.MakeString(jsexc.Concat("Attempt ", strconv.Itoa(attemptIndex), " was ", p.outcome,
					"; required Docker ", expectedOutcome, ". ", p.note)),
			)
		}
	}
	distinct := bundle.DistinctCount(executionIDs) == CertificationExecutions
	status, note := "NOT_CERTIFIED", "Certification executions were not distinct."
	if distinct {
		note = "Three distinct Docker-isolated executions produced the required outcome."
		if len(runIDs) == CertificationExecutions {
			status = "CERTIFIED"
		}
	}
	return jsjson.MustObject(
		"direction", jsjson.MakeString(direction),
		"expectedOutcome", jsjson.MakeString(expectedOutcome),
		"requiredExecutions", jsjson.MakeNumber(CertificationExecutions),
		"runIds", stringArray(runIDs),
		"executionIds", stringArray(executionIDs),
		"status", jsjson.MakeString(status),
		"note", jsjson.MakeString(note),
	)
}

// isoMillis is new Date(ms).toISOString().
func isoMillis(ms int64) string { return clock.ISO(time.UnixMilli(ms)) }

func minimizeGitDiff(request jsjson.Value, runner sandbox.Runner, injected bool) (jsjson.Value, error) {
	if runner == nil {
		runner = sandbox.NativeRunner()
	}
	requestedExecutionTrust := executionTrustFor(injected)
	raw := jsjson.NewObj()
	for _, key := range []string{"repository", "before", "after", "frozenWitness", "expectedFrozenDigest", "sandbox", "budget"} {
		if v := request.Get(key); v.Kind() != jsjson.Undefined {
			raw.Set(key, v)
		}
	}
	input, issues, ok := schema.Parse(RequestSchema, jsjson.MakeObject(raw))
	if !ok {
		null := jsjson.MakeNull()
		return baseResult("INVALID_REQUEST", 128, []string{schema.ErrorMessage(issues)},
			resultFields{null, null, null, null}, requestedExecutionTrust)
	}
	frozenWitness := input.Get("frozenWitness")
	maxExecutions := input.Get("budget", "maxExecutions").Num()
	verification := gitproof.VerifyFrozenWitnessRecord(frozenWitness, input.Get("expectedFrozenDigest").Str(), true)
	summary := witnessSummary(frozenWitness, verification)
	if !verification.Valid || verification.ExternalDigestStatus != "MATCH" {
		errs := verification.Errors
		if len(errs) == 0 {
			errs = []string{"A separately supplied frozen witness digest did not match."}
		}
		null := jsjson.MakeNull()
		return baseResult("INVALID_WITNESS", maxExecutions, errs, resultFields{null, null, null, summary}, requestedExecutionTrust)
	}

	var repository string
	var before, after counterfactualState
	if err := attempt(func() error {
		var err error
		if repository, err = resolveRepositoryRoot(input.Get("repository").Str()); err != nil {
			return err
		}
		if before, err = resolveState(repository, input.Get("before").Str()); err != nil {
			return err
		}
		if after, err = resolveState(repository, input.Get("after").Str()); err != nil {
			return err
		}
		return gitinv.AssertSafeGitMaterialization(repository, []string{before.commit, after.commit})
	}); err != nil {
		null := jsjson.MakeNull()
		return baseResult("RANGE_ERROR", maxExecutions, []string{jsexc.Message(err)},
			resultFields{null, null, null, summary}, requestedExecutionTrust)
	}

	var units []PatchUnit
	if err := attempt(func() error {
		var err error
		units, err = derivePatchUnits(repository, before, after)
		return err
	}); err != nil {
		return baseResult("PATCH_ERROR", maxExecutions, []string{jsexc.Message(err)},
			resultFields{jsjson.MakeString(repository), before.value(), after.value(), summary}, requestedExecutionTrust)
	}
	if len(units) == 0 {
		return baseResult("NO_PATCHES", maxExecutions, []string{"The supplied Git states have no file-level diff units."},
			resultFields{jsjson.MakeString(repository), before.value(), after.value(), summary}, executionTrustFor(false))
	}

	r := &run{
		input: input, frozenWitness: frozenWitness, repository: repository, before: before, after: after,
		runner: runner, injected: injected, units: units, maxExecutions: int(maxExecutions),
		roleAttempts: map[string]int{}, summary: summary,
		attempts: []jsjson.Value{}, runs: []jsjson.Value{}, errs: []string{},
	}

	allIDs := unitIDs(units)
	enumeration := emptyEnumeration("Enumeration did not run because no valid full-range outcome exists yet.")
	interaction := emptyInteraction("No subset outcome matrix was retained.")
	budgetStopped := false

	baseline := r.recordProbe("BASELINE", "FORWARD_FROM_BEFORE", nil, "BASELINE_BEFORE")
	if baseline.outcome != "PASS" {
		return r.finalize(statusFromProbe(baseline, "PASS"), nil,
			minimalityRecord(false, "The before state did not produce a Docker PASS.", enumeration, interaction),
			defaultSufficiency(), defaultNecessity(), baseline.note)
	}

	full := r.recordProbe("FULL_RANGE", "FORWARD_FROM_BEFORE", allIDs, "FULL_PATCH")
	if full.outcome != "FAIL" {
		return r.finalize(statusFromProbe(full, "FAIL"), allIDs,
			minimalityRecord(false, "The full Git diff did not produce a Docker FAIL from the before state.", enumeration, interaction),
			defaultSufficiency(), defaultNecessity(), full.note)
	}

	// Enumerate every proper subset only while the candidate universe is small
	// enough to bound the work. These runs are retained separately from ddmin so
	// the result can report multiple 1-minimal sets rather than implying the
	// first one found is unique.
	if len(allIDs) > MaxEnumerationPatchUnits {
		enumeration = emptyEnumeration(jsexc.Concat("Enumeration skipped: ", strconv.Itoa(len(allIDs)), " patch units exceed the ",
			strconv.Itoa(MaxEnumerationPatchUnits), "-unit bound."))
		enumeration.Obj().Set("status", jsjson.MakeString("SKIPPED_MAX_UNITS"))
		interaction = emptyInteraction("Subset enumeration was skipped by the patch-unit bound.")
	} else {
		recorded := newOutcomes()
		recorded.set(candidateKey(nil), baseline.outcome)
		recorded.set(candidateKey(allIDs), full.outcome)
		subsets := boundedProperSubsets(allIDs)
		searchedCandidateSets := 0
		for _, subset := range subsets {
			p := r.recordProbe("ENUMERATION", "FORWARD_FROM_BEFORE", subset, "ENUMERATION")
			recorded.set(candidateKey(subset), p.outcome)
			searchedCandidateSets++
			if p.outcome == "NOT_RUN" {
				budgetStopped = true
				break
			}
		}
		distinctMinimalSets := []jsjson.Value{}
		if !budgetStopped {
			for _, subset := range subsets {
				if recorded.get(candidateKey(subset)) != "FAIL" {
					continue
				}
				isOneMinimal := true
				for _, removed := range subset {
					if recorded.get(candidateKey(without(subset, removed))) != "PASS" {
						isOneMinimal = false
						break
					}
				}
				if !isOneMinimal {
					continue
				}
				distinctMinimalSets = append(distinctMinimalSets, stringArray(subset))
				if len(distinctMinimalSets) == MaxEnumeratedMinimalSets {
					break
				}
			}
		}
		status, note := "COMPLETED", jsexc.Concat("Found ", strconv.Itoa(len(distinctMinimalSets)),
			" distinct 1-minimal set(s) after executing ", strconv.Itoa(searchedCandidateSets), " proper subset(s).")
		switch {
		case budgetStopped:
			status, note = "BUDGET_EXHAUSTED", "The global execution budget was exhausted during bounded subset enumeration."
		case len(distinctMinimalSets) == MaxEnumeratedMinimalSets:
			status = "SET_LIMIT_REACHED"
			note = jsexc.Concat("Found ", strconv.Itoa(MaxEnumeratedMinimalSets), " distinct 1-minimal sets; the retained-set bound was reached.")
		}
		enumeration = jsjson.MustObject(
			"status", jsjson.MakeString(status),
			"searchedCandidateSets", jsjson.MakeNumber(float64(searchedCandidateSets)),
			"maxPatchUnits", jsjson.MakeNumber(MaxEnumerationPatchUnits),
			"maxDistinctMinimalSets", jsjson.MakeNumber(MaxEnumeratedMinimalSets),
			"distinctMinimalSets", jsjson.MakeArray(distinctMinimalSets),
			"note", jsjson.MakeString(note),
		)
		if budgetStopped {
			interaction = emptyInteraction("Subset enumeration did not finish, so monotonicity is inconclusive.")
		} else {
			interaction = observedNonMonotonicInteraction(allIDs, recorded)
		}
	}

	candidate := append([]string(nil), allIDs...)
	granularity := 2
	for len(candidate) >= 2 && !budgetStopped {
		partitions := chunk(candidate, min(granularity, len(candidate)))
		reduced := false
		for _, partition := range partitions {
			p := r.recordProbe("DELTA_SUBSET", "FORWARD_FROM_BEFORE", partition, "DELTA_SUBSET")
			if p.outcome == "NOT_RUN" {
				budgetStopped = true
				break
			}
			if p.outcome == "FAIL" {
				candidate = partition
				granularity = max(2, granularity-1)
				reduced = true
				break
			}
		}
		if budgetStopped || reduced {
			continue
		}
		for _, partition := range partitions {
			partitionSet := map[string]bool{}
			for _, id := range partition {
				partitionSet[id] = true
			}
			var complement []string
			for _, id := range candidate {
				if !partitionSet[id] {
					complement = append(complement, id)
				}
			}
			p := r.recordProbe("DELTA_COMPLEMENT", "FORWARD_FROM_BEFORE", complement, "DELTA_COMPLEMENT")
			if p.outcome == "NOT_RUN" {
				budgetStopped = true
				break
			}
			if p.outcome == "FAIL" {
				candidate = complement
				granularity = max(2, granularity-1)
				reduced = true
				break
			}
		}
		if budgetStopped || reduced {
			continue
		}
		if granularity >= len(candidate) {
			break
		}
		granularity = min(len(candidate), granularity*2)
	}

	oneMinimal := !budgetStopped
	minimalityReason := "Every single-unit removal was tested."
	if budgetStopped {
		minimalityReason = "The execution budget was exhausted before one-minimality could be established."
	}
	for index := 0; index < len(candidate) && !budgetStopped; {
		removed := candidate[index]
		p := r.recordProbe("ONE_MINIMAL", "FORWARD_FROM_BEFORE", without(candidate, removed), "ONE_MINIMAL")
		if p.outcome == "NOT_RUN" {
			budgetStopped = true
			oneMinimal = false
			minimalityReason = "The execution budget was exhausted before one-minimality could be established."
			break
		}
		if p.outcome == "FAIL" {
			candidate = without(candidate, removed)
			index = 0
			// The smaller candidate needs a fresh complete removal pass. Earlier
			// removal outcomes belonged to a different candidate and must not keep
			// a genuinely one-minimal reduced candidate from being certified.
			oneMinimal = true
			minimalityReason = "A smaller failing candidate was found; retesting its removals."
			continue
		}
		if p.outcome == "UNRESOLVED" {
			oneMinimal = false
			minimalityReason = "At least one single-unit removal could not be applied or executed, so one-minimality is unproven."
		}
		index++
	}
	if !budgetStopped && oneMinimal {
		minimalityReason = "Every single-unit removal produced a Docker PASS."
	}

	// Hunk refinement: after file-level one-minimality, try splitting surviving
	// MODIFY text files into @@ hunks and re-running one-minimal over those units.
	// Fall back to the file-level candidate on apply failure or budget exhaustion.
	fileLevelCandidate := append([]string(nil), candidate...)
	fileLevelOneMinimal := oneMinimal
	fileLevelMinimalityReason := minimalityReason
	if !budgetStopped && oneMinimal && len(candidate) > 0 {
		var refinedUnits []PatchUnit
		splittable := false
		for _, unit := range r.units {
			if !contains(candidate, unit.ID) {
				continue
			}
			hunks := SplitModifyUnitIntoHunks(unit)
			if hunks == nil {
				refinedUnits = append(refinedUnits, unit)
				continue
			}
			splittable = true
			refinedUnits = append(refinedUnits, hunks...)
		}
		if splittable && len(refinedUnits) > len(candidate) {
			r.units = refinedUnits
			candidate = unitIDs(refinedUnits)
			hunkIndex := 0
			hunkOneMinimal := true
			hunkAbandoned := false
			hunkReason := "Every single-hunk removal was tested."
			for hunkIndex < len(candidate) && !budgetStopped {
				removed := candidate[hunkIndex]
				p := r.recordProbe("HUNK_ONE_MINIMAL", "FORWARD_FROM_BEFORE", without(candidate, removed), "HUNK_ONE_MINIMAL")
				if p.outcome == "NOT_RUN" {
					budgetStopped = true
					hunkOneMinimal = false
					hunkReason = "The execution budget was exhausted during hunk refinement; retaining the file-level one-minimal candidate."
					break
				}
				if p.outcome == "FAIL" {
					candidate = without(candidate, removed)
					hunkIndex = 0
					hunkOneMinimal = true
					hunkReason = "A smaller failing hunk candidate was found; retesting its removals."
					continue
				}
				if p.outcome == "UNRESOLVED" {
					hunkAbandoned = true
					hunkReason = jsexc.Concat(fileLevelMinimalityReason, " Hunk refinement was abandoned after an unresolved hunk apply.")
					break
				}
				hunkIndex++
			}
			if budgetStopped || hunkAbandoned {
				derived, err := derivePatchUnits(r.repository, before, after)
				if err != nil {
					return jsjson.Value{}, err
				}
				r.units = derived
				candidate = fileLevelCandidate
				oneMinimal = !budgetStopped && fileLevelOneMinimal
				minimalityReason = hunkReason
			} else if hunkOneMinimal {
				oneMinimal = true
				minimalityReason = "Every single-hunk removal produced a Docker PASS after file-level refinement."
			}
		}
	}

	if budgetStopped || r.usedExecutions >= r.maxExecutions {
		return r.finalize("BUDGET_EXHAUSTED", candidate,
			minimalityRecord(oneMinimal, minimalityReason, enumeration, interaction),
			defaultSufficiency(), defaultNecessity(),
			"The minimization execution budget was exhausted before bidirectional certification.")
	}

	sufficiency := r.certify("SUFFICIENCY_CERTIFICATION", "FORWARD_FROM_BEFORE", "FAIL", "SUFFICIENCY_CERTIFICATION", candidate)
	necessity := emptyCertificate("REVERSE_FROM_AFTER", "PASS", "Execution budget was exhausted before necessity certification.")
	if r.usedExecutions < r.maxExecutions {
		necessity = r.certify("NECESSITY_CERTIFICATION", "REVERSE_FROM_AFTER", "PASS", "NECESSITY_CERTIFICATION", candidate)
	}
	certificationsComplete := sufficiency.Get("status").Str() == "CERTIFIED" && necessity.Get("status").Str() == "CERTIFIED"
	terminal := jsjson.Value{}
	for i := len(r.runs) - 1; i >= 0; i-- {
		if role := r.runs[i].Get("role").Str(); role == "SUFFICIENCY_CERTIFICATION" || role == "NECESSITY_CERTIFICATION" {
			terminal = r.runs[i].Get("result")
			break
		}
	}
	completedProof := certificationsComplete && oneMinimal && len(r.errs) == 0
	var status string
	switch {
	case r.usedExecutions >= r.maxExecutions && !certificationsComplete:
		status = "BUDGET_EXHAUSTED"
	case terminal.Kind() == jsjson.Object && terminal.Get("kind").Str() == "UNSAFE_LOCAL":
		status = "UNSAFE_LOCAL_INAPPLICABLE"
	case terminal.Kind() == jsjson.Object && terminal.Get("reason").Str() == "SANDBOX_UNAVAILABLE":
		status = "SANDBOX_UNAVAILABLE"
	case completedProof:
		status = "COMPLETED"
	default:
		status = "CERTIFICATION_FAILED"
	}
	var reason string
	switch {
	case status == "COMPLETED":
		reason = "One-minimal candidate has independently certified Docker sufficiency and necessity."
	case status == "BUDGET_EXHAUSTED":
		reason = "Execution budget was exhausted before all required certification executions completed."
	case status == "UNSAFE_LOCAL_INAPPLICABLE":
		reason = "Unsafe local execution is explicitly inapplicable and cannot support proof."
	case status == "SANDBOX_UNAVAILABLE":
		reason = "Docker was unavailable; no counterfactual result is proof."
	case oneMinimal:
		reason = "Bidirectional Docker certification did not complete with the required outcomes."
	default:
		reason = minimalityReason
	}
	return r.finalize(status, candidate, minimalityRecord(oneMinimal, minimalityReason, enumeration, interaction),
		sufficiency, necessity, reason)
}

// without is `ids.filter((id) => id !== removed)`.
func without(ids []string, removed string) []string {
	var out []string
	for _, id := range ids {
		if id != removed {
			out = append(out, id)
		}
	}
	return out
}

func contains(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}
