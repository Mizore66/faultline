package codexloop

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle/prevention"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/repair"
)

// RepairOptions ports the options of src/codex-loop.ts:320 repairWithCodex.
// Invariant is set only when HasInvariant. VerifyBundle returns
// { valid, errors, rootDigest } (a nil rootDigest is null); a nil Runner and
// a nil VerifyCandidate are TS undefined.
type RepairOptions struct {
	BundleDirectory, ExpectRoot, Repository, OutputDirectory string
	Invariant                                                string
	HasInvariant                                             bool
	WithCodex, InstructionsOnly, KeepWorktree                bool
	Runner                                                   Runner
	VerifyCandidate                                          VerifyCandidate
	VerifyBundle                                             func(dir, expectRoot string) (valid bool, errors []string, rootDigest *string)
}

// SelectRepairBaseCommit ports src/codex-loop.ts:192 selectRepairBaseCommit:
// the first PASS_TO_FAIL transition's after.commit, else the resolved
// descendant's commit when truthy, else the error.
func SelectRepairBaseCommit(investigation jsjson.Value) (string, error) {
	for _, transition := range investigation.Get("transitions").Items() {
		if transition.Get("kind").Str() == "PASS_TO_FAIL" {
			return transition.Get("after", "commit").Str(), nil
		}
	}
	if commit := investigation.Get("resolvedRange", "descendant", "commit"); commit.Kind() == jsjson.String && commit.Str() != "" {
		return commit.Str(), nil
	}
	return "", errors.New("Verified investigation does not identify a repair base commit.")
}

// writeRepairInstructions ports src/codex-loop.ts:275 writeRepairInstructions.
func writeRepairInstructions(instructionPath, rootDigest, baseCommit, invariant, proofReadonlyPath, worktreePath string) error {
	worktree := "Isolated worktree: not created (instructions only)"
	if worktreePath != "" {
		worktree = "Isolated worktree: " + worktreePath
	}
	return nodefs.WriteFileUTF8(instructionPath, strings.Join([]string{
		"# FaultLine repair",
		"",
		"Verified proof root: " + rootDigest,
		"Repair base commit: " + baseCommit,
		"Invariant (inferred / human-supplied): " + invariant,
		worktree,
		"Proof companion (read-only): " + proofReadonlyPath,
		"",
		"Rules:",
		"- Use only verified evidence from the proof bundle.",
		"- Do not invent agent intent.",
		"- After editing, re-run the frozen witness on last-good, first-bad, and repaired states.",
		"- FaultLine never auto-merges a Codex draft.",
		"",
		"Next:",
		"1. Apply a minimal fix in the isolated worktree",
		"2. Re-verify with the same frozen witness digest",
		"3. Confirm three-state prevention: last-good PASS, first-bad FAIL, repaired PASS",
		"",
	}, "\n"), 0o666, false)
}

// repairResult is baseRepairResult (src/codex-loop.ts:306): schemaVersion,
// then cleanupErrors (an object spread keeps the first key's position, so a
// partial's own cleanupErrors only replaces the value), then the partial's
// keys in order, then preventionExport when set.
type repairResult struct {
	status, transport                                      string
	worktreePath, instructionPath, managedRoot, baseCommit *string
	patchPath, codexThreadID                               *string
	nextCommands                                           []string
	note, limitation                                       string
	cleanupErrors                                          []string
	preventionExport                                       *jsjson.Value
}

func optString(s *string) jsjson.Value {
	if s == nil {
		return jsjson.MakeNull()
	}
	return jsjson.MakeString(*s)
}

func stringArray(items []string) jsjson.Value {
	out := make([]jsjson.Value, len(items))
	for i, item := range items {
		out[i] = jsjson.MakeString(item)
	}
	return jsjson.MakeArray(out)
}

func (r repairResult) value() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(SchemaVersion))
	o.Set("cleanupErrors", stringArray(r.cleanupErrors))
	o.Set("status", jsjson.MakeString(r.status))
	o.Set("transport", jsjson.MakeString(r.transport))
	o.Set("worktreePath", optString(r.worktreePath))
	o.Set("instructionPath", optString(r.instructionPath))
	o.Set("managedRoot", optString(r.managedRoot))
	o.Set("baseCommit", optString(r.baseCommit))
	o.Set("patchPath", optString(r.patchPath))
	o.Set("codexThreadId", optString(r.codexThreadID))
	o.Set("nextCommands", stringArray(r.nextCommands))
	o.Set("note", jsjson.MakeString(r.note))
	o.Set("limitation", jsjson.MakeString(r.limitation))
	if r.preventionExport != nil {
		o.Set("preventionExport", *r.preventionExport)
	}
	return jsjson.MakeObject(o)
}

func str(s string) *string { return &s }

// utf16Prefix is s.slice(0, n) on UTF-16 units; a cut surrogate pair leaves a
// lone half (written as U+FFFD by WriteFileUTF8, as Node does).
func utf16Prefix(s string, n int) string {
	units := jsstr.ToUTF16(s)
	if len(units) > n {
		units = units[:n]
	}
	return jsstr.FromUTF16(units)
}

func exportFailure(reasons []string) *jsjson.Value {
	v := jsjson.MustObject("ok", jsjson.MakeBool(false), "reasons", stringArray(reasons))
	return &v
}

const (
	repairLimitation = "Automated Codex repair is opt-in via --with-codex and never auto-merges."
	fallbackCommand  = "If automatic prevention export failed, collect three repaired NATIVE_DOCKER runs then: fl prevention write --from-bundle <bundle> --expect-root <digest> --patch <repair.patch> --repaired-commit <id> --repaired-tree <id> --repaired-runs <runs.json>"
)

// RepairWithCodex ports src/codex-loop.ts:320 repairWithCodex. A returned
// error is the rejected promise (an escaped read, parse or write, or a throw
// the TS code does not catch).
func RepairWithCodex(o RepairOptions) (result jsjson.Value, err error) {
	defer jsexc.Catch(func(e error) { result, err = jsjson.Value{}, e })
	return repairWithCodex(o)
}

func repairWithCodex(o RepairOptions) (jsjson.Value, error) {
	valid, verificationErrors, verifiedRoot := o.VerifyBundle(o.BundleDirectory, o.ExpectRoot)
	if !valid {
		note := jsexc.Join(verificationErrors, "; ")
		if note == "" {
			note = "Proof bundle verification failed."
		}
		return repairResult{status: "BUNDLE_INVALID", transport: "NONE", note: note,
			limitation: "Repair requires a verified proof bundle and externally retained root digest."}.value(), nil
	}

	var investigation jsjson.Value
	var baseCommit string
	if e := jsexc.Try(func() {
		investigation = jsexc.Must(readInvestigationFromBundle(o.BundleDirectory))
		baseCommit = jsexc.Must(SelectRepairBaseCommit(investigation))
	}); e != nil {
		return repairResult{status: "BUNDLE_INVALID", transport: "NONE", note: jsexc.Message(e),
			limitation: "Repair requires a completed investigation artifact inside the verified proof bundle."}.value(), nil
	}

	outputDirectory := nodefs.Resolve(o.OutputDirectory)
	invariant := "Preserve the frozen witness predicate on the repaired state."
	if o.HasInvariant {
		invariant = o.Invariant
	}
	rootDigest := o.ExpectRoot
	if verifiedRoot != nil {
		rootDigest = *verifiedRoot
	}
	repositoryPath := o.Repository
	if r := investigation.Get("repository"); r.Kind() == jsjson.String {
		repositoryPath = r.Str()
	}
	repository := nodefs.Resolve(repositoryPath)

	if o.InstructionsOnly {
		if err := nodefs.MkdirAll(outputDirectory, 0o700); err != nil {
			return jsjson.Value{}, err
		}
		instructionPath := nodefs.Join(outputDirectory, "repair-instructions.md")
		if err := writeRepairInstructions(instructionPath, rootDigest, baseCommit, invariant, nodefs.Resolve(o.BundleDirectory), ""); err != nil {
			return jsjson.Value{}, err
		}
		return repairResult{status: "REPAIR_INSTRUCTIONS_PREPARED", transport: "NONE", instructionPath: &instructionPath, managedRoot: &outputDirectory,
			baseCommit: &baseCommit,
			nextCommands: []string{
				"Review " + instructionPath,
				"Re-run without --instructions-only to create an isolated Git worktree",
				"Re-verify with the same frozen witness digest",
			},
			note:       "Repair instruction pack written. Codex was not invoked. This is not an isolated Git repair worktree.",
			limitation: repairLimitation}.value(), nil
	}

	var session *repair.Session
	cleanupErrors := []string{}
	status := "REPAIR_WORKTREE_READY"
	transport := "NONE"
	var patchPath *string
	var codexThreadID *string
	note := "Isolated detached Git worktree prepared at the selected repair base; companion proof and instructions retained under the managed repair root."
	var instructionPath *string

	setup := jsexc.Try(func() {
		created := jsexc.Must(repair.CreateWorktree(repository, baseCommit, o.BundleDirectory, outputDirectory))
		session = &created
		instructionPath = str(nodefs.Join(session.InstructionsPath, "repair-instructions.md"))
		jsexc.Must(struct{}{}, writeRepairInstructions(*instructionPath, rootDigest, baseCommit, invariant, session.ProofReadonlyPath, session.WorktreePath))
		meta := jsjson.MustObject(
			"schemaVersion", jsjson.MakeString(SchemaVersion),
			"baseCommit", jsjson.MakeString(baseCommit),
			"rootDigest", jsjson.MakeString(rootDigest),
			"repairId", jsjson.MakeString(session.RepairID),
		)
		jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(session.ArtifactsPath, "repair-meta.json"), jsjson.StringifyIndent(meta, "  ")+"\n", 0o666, false))

		if o.WithCodex && o.Runner != nil {
			transport = "CODEX_CLI"
			if e := jsexc.Try(func() {
				prompt := strings.Join([]string{
					"Using only the verified FaultLine proof companion at " + session.ProofReadonlyPath + ",",
					"implement a minimal repair in this Git worktree that restores the frozen witness.",
					"Do not claim model intent. Prefer the smallest safe change.",
					"When finished, leave the worktree dirty with your edits so FaultLine can collect git diff.",
				}, " ")
				run, runErr := o.Runner([]string{"exec", "--full-auto", prompt}, session.WorktreePath, prompt)
				if runErr != nil {
					jsexc.Throw(runErr)
				}
				codexThreadID = run.ThreadID
				exitCode := jsjson.MakeNull()
				if run.ExitCode != nil {
					exitCode = jsjson.MakeNumber(float64(*run.ExitCode))
				}
				thread := jsjson.MustObject(
					"threadId", optString(codexThreadID),
					"exitCode", exitCode,
					"stdoutPreview", jsjson.MakeString(utf16Prefix(run.Stdout, 4096)),
					"stderrPreview", jsjson.MakeString(utf16Prefix(run.Stderr, 4096)),
				)
				jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(session.ArtifactsPath, "codex-thread.json"), jsjson.StringifyIndent(thread, "  ")+"\n", 0o666, false))

				if run.ExitCode == nil || *run.ExitCode != 0 {
					exit := "null"
					if run.ExitCode != nil {
						exit = strconv.Itoa(*run.ExitCode)
					}
					status = "CODEX_UNAVAILABLE"
					note = "Codex CLI exited " + exit + ". Isolated worktree and instructions remain for manual repair."
					return
				}
				patch := jsexc.Must(repair.CollectPatch(session.WorktreePath))
				patchPath = str(nodefs.Join(session.ArtifactsPath, "repair.patch"))
				jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(*patchPath, patch, 0o666, false))
				status = "CODEX_DRAFT_RECORDED"
				note = "Codex CLI drafted repairs inside the isolated worktree. Patch collected; human review required. Not auto-merged."

				if o.VerifyCandidate != nil {
					verdict := jsexc.Must(o.VerifyCandidate(session.WorktreePath, baseCommit, *patchPath, o.BundleDirectory))
					jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(session.ArtifactsPath, "verification.json"), jsjson.StringifyIndent(verdict.JSON(), "  ")+"\n", 0o666, false))
					if verdict.OK {
						status = "REPAIR_CANDIDATE_VERIFIED"
						note = "Repair candidate verified in the isolated worktree: " + verdict.Detail
					} else {
						status = "REPAIR_VERIFICATION_FAILED"
						note = "Repair candidate failed verification: " + verdict.Detail
					}
				}
			}); e != nil {
				status = "CODEX_UNAVAILABLE"
				note = "Codex CLI could not be started: " + jsexc.Message(e)
				transport = "NONE"
			}
		}
	})
	if setup != nil {
		var managedRoot *string
		if nodefs.Exists(outputDirectory) {
			managedRoot = &outputDirectory
		}
		return repairResult{status: "REPAIR_SETUP_FAILED", transport: "NONE", managedRoot: managedRoot, baseCommit: &baseCommit,
			note: jsexc.Message(setup), limitation: repairLimitation, cleanupErrors: cleanupErrors}.value(), nil
	}

	var preventionExport *jsjson.Value
	var exportOK bool
	var exportDirectory, exportRoot string
	if status == "REPAIR_CANDIDATE_VERIFIED" && session != nil && patchPath != nil && o.VerifyCandidate != nil {
		if e := jsexc.Try(func() {
			repairedState := jsexc.Must(prevention.CommitRepairedWorktreeState(session.WorktreePath, baseCommit))
			runs, reasons, collectErr := prevention.CollectRepairedRuns(session.WorktreePath, o.BundleDirectory, repairedState.Commit, repairedState.Tree,
				func() (prevention.Verdict, error) {
					return o.VerifyCandidate(session.WorktreePath, baseCommit, *patchPath, o.BundleDirectory)
				})
			if collectErr != nil {
				jsexc.Throw(collectErr)
			}
			if reasons != nil {
				preventionExport = exportFailure(reasons)
				note = note + " Prevention export fail-closed: " + jsexc.Join(reasons, "; ")
				return
			}
			cwd := jsexc.Must(nodefs.Cwd())
			preventionOut := nodefs.Join(prevention.DefaultRoot(cwd), "from-repair-"+strconv.FormatInt(clock.NowMillis(), 10))
			artifact := prevention.ArtifactOptions{
				Bundle: o.BundleDirectory, ExpectedRoot: o.ExpectRoot, RepairedRuns: runs,
				RepairedCommit: repairedState.Commit, RepairedTree: repairedState.Tree,
			}
			for _, transition := range investigation.Get("transitions").Items() {
				if transition.Get("kind").Str() == "PASS_TO_FAIL" {
					artifact.RepairBaseTree, artifact.HasRepairBaseTree = transition.Get("after", "tree").Str(), true
					break
				}
			}
			artifact.PatchDigest = jsexc.Must(prevention.DigestRepairPatchFile(*patchPath))
			if codexThreadID != nil {
				artifact.CodexThreadID, artifact.HasThreadID = *codexThreadID, true
			}
			written, exportReasons := prevention.WriteFromArtifacts(artifact, preventionOut)
			if exportReasons != nil {
				preventionExport = exportFailure(exportReasons)
				note = note + " Prevention export fail-closed: " + jsexc.Join(exportReasons, "; ")
				return
			}
			classification := written.Manifest.Get("classification")
			exported := jsjson.MustObject(
				"ok", jsjson.MakeBool(true),
				"directory", jsjson.MakeString(written.Directory),
				"rootDigest", jsjson.MakeString(written.RootDigest),
				"classification", classification,
			)
			preventionExport = &exported
			exportOK, exportDirectory, exportRoot = true, written.Directory, written.RootDigest
			if classification.Str() == "PREVENTION_VERIFIED" {
				// Docs materialization is best-effort; the prevention package remains authoritative.
				_, _ = prevention.UpsertAgentsMd(o.Repository, written.Body)
			}
			jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(session.ArtifactsPath, "prevention-export.json"), jsjson.StringifyIndent(exported, "  ")+"\n", 0o666, false))
			note = note + " Prevention verified package written to " + written.Directory + "."
		}); e != nil {
			exportOK = false
			preventionExport = exportFailure([]string{jsexc.Message(e)})
			note = note + " Prevention export fail-closed: " + jsexc.Message(e)
		}
	}

	if session != nil && !o.KeepWorktree {
		cleanup := repair.RemoveWorktree(session.Repository, session.WorktreePath, session.ManagedRoot, true)
		cleanupErrors = append(cleanupErrors, cleanup.Errors...)
		if !cleanup.Cleaned {
			if status == "REPAIR_WORKTREE_READY" || status == "CODEX_DRAFT_RECORDED" || status == "REPAIR_CANDIDATE_VERIFIED" {
				status = "WORKTREE_CLEANUP_FAILED"
				note = note + " Worktree cleanup reported errors: " + jsexc.Join(cleanup.Errors, "; ")
			}
		}
	}

	var worktreePath, managedRoot *string
	if o.KeepWorktree && session != nil {
		worktreePath = &session.WorktreePath
	}
	if session != nil {
		managedRoot = &session.ManagedRoot
	}
	var next []string
	if instructionPath != nil {
		next = append(next, "Review "+*instructionPath)
	}
	if patchPath != nil {
		next = append(next, "Inspect "+*patchPath)
	}
	next = append(next, "Human-review any Codex draft before merge")
	if exportOK {
		next = append(next, "Prevention verified: fl prevention verify "+exportDirectory+" --expect-root "+exportRoot)
	} else {
		next = append(next, fallbackCommand)
	}
	return repairResult{status: status, transport: transport, worktreePath: worktreePath, instructionPath: instructionPath, managedRoot: managedRoot,
		baseCommit: &baseCommit, patchPath: patchPath, codexThreadID: codexThreadID, nextCommands: next, note: note, limitation: repairLimitation,
		cleanupErrors: cleanupErrors, preventionExport: preventionExport}.value(), nil
}
