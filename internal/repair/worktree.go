package repair

import (
	"errors"
	"os"
	"strings"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsre"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// Session ports RepairWorktreeSession (src/repair-worktree.ts:9).
type Session struct {
	RepairID, ManagedRoot, WorktreePath, ProofReadonlyPath  string
	InstructionsPath, ArtifactsPath, BaseCommit, Repository string
}

// Cleanup ports RepairWorktreeCleanupResult (src/repair-worktree.ts:19).
type Cleanup struct {
	Cleaned bool
	Errors  []string
}

// gitObjectID is GIT_OBJECT_ID (src/repair-worktree.ts:7).
var gitObjectID = jsre.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`, "")

// Port of src/repair-worktree.ts:28 assertManagedChild.
func assertManagedChild(root, candidate, label string) (string, error) {
	nested, err := safedir.RelativeTrustedSystemPath(root, candidate)
	if err != nil {
		return "", err
	}
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		return "", errors.New(jsexc.Concat(label, " must be a child of the managed repair root."))
	}
	return nodefs.Resolve(candidate), nil
}

// Port of src/repair-worktree.ts:36 assertNotSymlink.
func assertNotSymlink(path, label string) error {
	if !nodefs.Exists(path) {
		return nil
	}
	stat, err := nodefs.Lstat(path)
	if err != nil {
		return err
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return errors.New(jsexc.Concat(label, " must not be a symbolic link: ", path))
	}
	return nil
}

// Port of src/repair-worktree.ts:48 createRepairWorktree (repairId is always
// generated from the clock's next UUID).
func CreateWorktree(repository, baseCommit, bundleDirectory, outputDirectory string) (Session, error) {
	if !gitObjectID.Test(baseCommit) {
		return Session{}, errors.New(jsexc.Concat("Repair base commit is not a Git object id: ", baseCommit))
	}
	repo := nodefs.Resolve(repository)
	repoRoot, err := gitinv.HardenedGitText(repo, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return Session{}, err
	}
	if !nodefs.IsAbsolute(repoRoot) {
		return Session{}, errors.New("Git did not return an absolute repository root.")
	}
	absoluteRepo := nodefs.Resolve(repoRoot)

	repairID := clock.NewUUID()[:12]
	managedRoot := nodefs.Resolve(outputDirectory)
	parent := nodefs.Dirname(managedRoot)
	if !nodefs.Exists(parent) {
		if err := nodefs.MkdirAll(parent, 0o700); err != nil {
			return Session{}, err
		}
	}
	_, ok, err := safedir.ResolveSafeDirectorySegment(parent)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, errors.New(jsexc.Concat("Repair output parent is not a real directory: ", parent))
	}
	if nodefs.Exists(managedRoot) {
		return Session{}, errors.New(jsexc.Concat("Repair output already exists and will not be replaced: ", managedRoot))
	}
	if err := nodefs.Mkdir(managedRoot, 0o700); err != nil {
		return Session{}, err
	}
	safeRoot, ok, err := safedir.ResolveSafeDirectorySegment(managedRoot)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, errors.New(jsexc.Concat("Repair managed root is not a real directory: ", managedRoot))
	}

	var paths [4]string
	for i, c := range [4][2]string{{"worktree", "repair worktree"}, {"proof-ro", "proof mount"}, {"instructions", "instructions"}, {"artifacts", "artifacts"}} {
		if paths[i], err = assertManagedChild(safeRoot, nodefs.Join(safeRoot, c[0]), c[1]); err != nil {
			return Session{}, err
		}
	}
	worktreePath, proofReadonlyPath, instructionsPath, artifactsPath := paths[0], paths[1], paths[2], paths[3]
	for _, p := range []string{instructionsPath, artifactsPath} {
		if err := nodefs.Mkdir(p, 0o700); err != nil {
			return Session{}, err
		}
	}

	verify := gitinv.RunHardenedGit([]string{"rev-parse", "--verify", "--end-of-options", jsexc.Concat(baseCommit, "^{commit}")}, gitinv.HardenedOptions{Repository: absoluteRepo})
	if verify.Failed() {
		if err := nodefs.RemoveAll(safeRoot); err != nil {
			return Session{}, err
		}
		return Session{}, errors.New(jsexc.Concat("Repair base commit is not available in the repository: ", baseCommit))
	}

	add := gitinv.RunHardenedGit([]string{"worktree", "add", "--detach", gitinv.ToGitPath(worktreePath), baseCommit}, gitinv.HardenedOptions{Repository: absoluteRepo})
	if add.Failed() {
		if err := nodefs.RemoveAll(safeRoot); err != nil {
			return Session{}, err
		}
		return Session{}, errors.New(jsexc.Concat("Could not create detached repair worktree: ", add.Detail("Git failed.")))
	}
	if err := assertNotSymlink(worktreePath, "repair worktree"); err != nil {
		return Session{}, err
	}

	if err := finishWorktree(bundleDirectory, baseCommit, worktreePath, proofReadonlyPath, instructionsPath); err != nil {
		RemoveWorktree(absoluteRepo, worktreePath, safeRoot, false)
		msg := jsexc.Message(err)
		if strings.HasPrefix(msg, "Could not") {
			return Session{}, errors.New(msg)
		}
		return Session{}, errors.New(jsexc.Concat("Could not finish repair worktree setup: ", msg))
	}

	return Session{
		RepairID: repairID, ManagedRoot: safeRoot, WorktreePath: worktreePath,
		ProofReadonlyPath: proofReadonlyPath, InstructionsPath: instructionsPath,
		ArtifactsPath: artifactsPath, BaseCommit: baseCommit, Repository: absoluteRepo,
	}, nil
}

// The try block of src/repair-worktree.ts:111-130.
func finishWorktree(bundleDirectory, baseCommit, worktreePath, proofReadonlyPath, instructionsPath string) error {
	if err := nodefs.CpSyncTree(nodefs.Resolve(bundleDirectory), proofReadonlyPath); err != nil {
		return err
	}
	pointerDir := nodefs.Join(worktreePath, ".faultline-repair")
	if err := nodefs.Mkdir(pointerDir, 0o700); err != nil {
		return err
	}
	readme := strings.Join([]string{
		"# FaultLine isolated repair worktree",
		"",
		jsexc.Concat("Base commit: ", baseCommit),
		jsexc.Concat("Proof (read-only companion): ", proofReadonlyPath),
		jsexc.Concat("Instructions: ", instructionsPath),
		"",
		"Do not commit `.faultline-repair/`. Produce a minimal patch that restores the frozen witness.",
		"",
	}, "\n")
	return nodefs.WriteFileUTF8(nodefs.Join(pointerDir, "README.md"), readme, 0o666, false)
}

// Port of src/repair-worktree.ts:155 removeRepairWorktree. An empty
// managedRoot is TS's undefined (no managed-root cleanup).
func RemoveWorktree(repository, worktreePath, managedRoot string, keepManagedRoot bool) Cleanup {
	errs := []string{}
	repo := nodefs.Resolve(repository)
	worktree := nodefs.Resolve(worktreePath)

	if nodefs.Exists(worktree) {
		removed := gitinv.RunHardenedGit([]string{"worktree", "remove", "--force", gitinv.ToGitPath(worktree)}, gitinv.HardenedOptions{Repository: repo})
		if removed.Failed() {
			errs = append(errs, jsexc.Concat("git worktree remove failed: ", removed.Detail("Git failed.")))
			if err := nodefs.RemoveAll(worktree); err != nil {
				errs = append(errs, jsexc.Concat("Could not delete worktree directory after remove failure: ", jsexc.Message(err)))
			}
		}
	}

	pruned := gitinv.RunHardenedGit([]string{"worktree", "prune"}, gitinv.HardenedOptions{Repository: repo})
	if pruned.Failed() {
		errs = append(errs, jsexc.Concat("git worktree prune failed: ", pruned.Detail("Git failed.")))
	}

	if managedRoot != "" && !keepManagedRoot {
		root := nodefs.Resolve(managedRoot)
		if nodefs.Exists(root) {
			child := nodefs.Join(root, "worktree")
			if nodefs.Exists(child) {
				if err := nodefs.RemoveAll(child); err != nil {
					errs = append(errs, jsexc.Concat("Could not remove leftover worktree directory: ", jsexc.Message(err)))
				}
			}
		}
	}
	return Cleanup{Cleaned: len(errs) == 0, Errors: errs}
}

// Port of src/repair-worktree.ts:204 collectRepairPatch.
func CollectPatch(worktreePath string) (string, error) {
	diff := gitinv.RunHardenedGit([]string{"diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-renames"},
		gitinv.HardenedOptions{Repository: nodefs.Resolve(worktreePath)})
	if diff.Failed() {
		return "", errors.New(jsexc.Concat("Could not collect repair patch: ", diff.Detail("Git failed.")))
	}
	return jsstr.DecodeUTF8(diff.Stdout), nil
}
