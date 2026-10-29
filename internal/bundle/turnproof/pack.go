package turnproof

import (
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// firstField is `line.trim().split(/\s+/)[0]`.
func firstField(line string) string {
	units := jsstr.ToUTF16(jsstr.Trim(line))
	end := 0
	for end < len(units) && !jsstr.IsWhitespace(units[end]) {
		end++
	}
	return jsstr.FromUTF16(units[:end])
}

// PackTurnTrees ports turn-proof-bundle.ts:412 packTurnTrees.
func PackTurnTrees(repository string, treeDigests []string) ([]byte, error) {
	var order []string
	seen := map[string]bool{}
	for _, tree := range treeDigests {
		listed := gitinv.RunHardenedGit([]string{"rev-list", "--objects", tree}, gitinv.HardenedOptions{Repository: repository})
		if listed.ExitCode == nil || *listed.ExitCode != 0 || listed.Error != "" {
			return nil, errors.New(jsexc.Concat("Could not enumerate objects for turn tree ", tree, ": ", listed.Detail("Git failed.")))
		}
		for _, line := range strings.Split(jsstr.DecodeUTF8(listed.Stdout), "\n") {
			if oid := firstField(line); oid != "" && !seen[oid] {
				seen[oid] = true
				order = append(order, oid)
			}
		}
	}
	if len(order) == 0 {
		return nil, errors.New("Turn tree pack enumeration produced no Git objects.") // coverage gate: unreachable (rev-list --objects <tree> lists the tree)
	}
	stdin := []byte(jsstr.ToUTF8(strings.Join(canonical.SortLocale(order), "\n") + "\n"))
	packed := gitinv.RunHardenedGit([]string{"pack-objects", "--stdout", "--compression=9"}, gitinv.HardenedOptions{Repository: repository, Stdin: stdin, HasStdin: true})
	if packed.ExitCode == nil || *packed.ExitCode != 0 || packed.Error != "" || len(packed.Stdout) == 0 {
		return nil, errors.New(jsexc.Concat("Could not pack turn trees: ", packed.Detail("Git failed.")))
	}
	if len(packed.Stdout) > MaxSourceArtifactBytes {
		return nil, errors.New("Turn tree pack exceeds FaultLine's portable artifact limit.") // coverage gate: unreachable (64 MiB output cap)
	}
	return packed.Stdout, nil
}

// removeTemporaryDirectory ports turn-proof-bundle.ts:436.
func removeTemporaryDirectory(path string) error {
	retries, delay := 3, 50*time.Millisecond
	if runtime.GOOS == "windows" {
		retries, delay = 20, 250*time.Millisecond
	}
	err := nodefs.RemoveAllRetry(path, retries, delay)
	if err != nil && runtime.GOOS == "windows" {
		if code := nodefs.ErrnoCode(err); code == "EPERM" || code == "EBUSY" {
			return nil
		}
	}
	return err
}

func gitFailed(r gitinv.GitRun) bool { return r.ExitCode == nil || *r.ExitCode != 0 || r.Error != "" }

// VerifyTreesPack ports turn-proof-bundle.ts:452 verifyTreesPack. It
// appends to errs and returns an error only for what TS throws out of it.
func VerifyTreesPack(pack []byte, treeDigests []string, errs *[]string) (result error) {
	bare, err := nodefs.Mkdtemp(nodefs.Join(nodefs.Tmpdir(), "faultline-turn-proof-pack-"))
	if err != nil {
		return err
	}
	defer func() {
		// finally: a throw from the cleanup replaces the body's outcome.
		if rmErr := removeTemporaryDirectory(bare); rmErr != nil {
			result = rmErr
		}
	}()
	return bundle.Try(func() {
		init := gitinv.RunHardenedGit([]string{"init", "--bare"}, gitinv.HardenedOptions{Repository: bare})
		if gitFailed(init) {
			*errs = append(*errs, jsexc.Concat("Could not create temporary bare repository for pack verification: ", init.Detail("Git failed.")))
			return
		}
		unpacked := gitinv.RunHardenedGit([]string{"unpack-objects", "-q", "--strict"}, gitinv.HardenedOptions{Repository: bare, GitDir: bare, Stdin: pack, HasStdin: true})
		if gitFailed(unpacked) {
			packDir := nodefs.Join(bare, "objects", "pack")
			if err := nodefs.MkdirAll(packDir, 0o700); err != nil {
				bundle.Throw(err)
			}
			packFile := nodefs.Join(packDir, "turn-trees.pack")
			if err := nodefs.WriteFileBytes(packFile, pack, 0o600, false); err != nil {
				bundle.Throw(err)
			}
			indexed := gitinv.RunHardenedGit([]string{"index-pack", "--strict", gitinv.ToGitPath(packFile)}, gitinv.HardenedOptions{Repository: bare, GitDir: bare})
			if gitFailed(indexed) {
				detail := firstNonEmpty(
					jsstr.Trim(jsstr.DecodeUTF8(indexed.Stderr)),
					jsstr.Trim(jsstr.DecodeUTF8(unpacked.Stderr)),
					indexed.Error,
					unpacked.Error,
					"Git failed.")
				*errs = append(*errs, jsexc.Concat("Turn tree pack is not a valid Git pack: ", detail))
				return
			}
		}
		for _, tree := range treeDigests {
			typed := gitinv.RunHardenedGit([]string{"cat-file", "-t", tree}, gitinv.HardenedOptions{Repository: bare, GitDir: bare})
			if gitFailed(typed) || jsstr.Trim(jsstr.DecodeUTF8(typed.Stdout)) != "tree" {
				*errs = append(*errs, jsexc.Concat("Turn tree object missing or wrong type in portable pack: ", tree))
			}
		}
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
