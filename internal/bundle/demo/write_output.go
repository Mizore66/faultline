package demo

import (
	"errors"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// proofRootTree is ensureRealDirectoryTree (src/proof-bundle.ts:73); both of
// its messages share one text.
func proofRootTree(directory string) {
	const text = "Proof bundle root cannot traverse a symbolic link or non-directory: "
	safedir.EnsureRealDirectoryTree(directory, text, text)
}

// safeSegment is resolveSafeDirectorySegment with its lstat error thrown.
func safeSegment(path string) (string, bool) {
	resolved, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		bundle.Throw(err)
	}
	return resolved, ok
}

// assertSafeProofOutput ports assertSafeProofOutput (src/proof-bundle.ts:102):
// output must be a child of root, each missing managed segment is created,
// and an existing output must be a link-free, verified FaultLine bundle.
func assertSafeProofOutput(outputDirectory, proofRoot string) string {
	output := nodefs.Resolve(outputDirectory)
	root := nodefs.Resolve(proofRoot)
	nestedPath := bundle.Must(safedir.RelativeTrustedSystemPath(root, output))
	if nestedPath == "" || strings.HasPrefix(nestedPath, "..") || nodefs.IsAbsolute(nestedPath) {
		bundle.Throw(errors.New(jsexc.Concat("Proof bundle output must be a child directory of ", root)))
	}
	proofRootTree(root)
	proofRootTree(nodefs.Dirname(output))
	current := root
	if nodefs.Exists(current) {
		if _, ok := safeSegment(current); !ok {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle root cannot be a symbolic link: ", root)))
		}
	}
	for _, part := range strings.FieldsFunc(nestedPath, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = nodefs.Join(current, part)
		if !nodefs.Exists(current) {
			continue
		}
		stat := bundle.Must(nodefs.Lstat(current))
		if isLink(stat) {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle output cannot traverse a symbolic link: ", current)))
		}
		if current == output && !stat.IsDir() {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle output must be a directory: ", output)))
		}
	}
	if nodefs.Exists(output) {
		assertNoLinksOrSpecialFiles(output)
		if !Verify(output, "", false).Valid {
			bundle.Throw(errors.New(jsexc.Concat("Proof bundle output can only replace a verified FaultLine bundle: ", output)))
		}
	}
	return output
}

// replaceDirectory ports replaceDirectory (src/proof-bundle.ts:322). The
// backup name is allocated even when there is nothing to back up, a restore
// or cleanup failure replaces the earlier error, and the stage is not
// verified before publication.
func replaceDirectory(stage, output string) {
	backup := jsexc.Concat(output, ".backup-", clock.NewUUID())
	hadExisting := nodefs.Exists(output)
	if hadExisting {
		bundle.Must(struct{}{}, nodefs.Rename(output, backup))
	}
	if err := nodefs.Rename(stage, output); err != nil {
		if hadExisting && !nodefs.Exists(output) {
			bundle.Must(struct{}{}, nodefs.Rename(backup, output))
		}
		bundle.Throw(err)
	}
	if hadExisting {
		bundle.Must(struct{}{}, nodefs.RemoveAll(backup))
	}
}

// publishStage writes the bundle into <output>.staging-<UUID> and replaces
// output with it (src/proof-bundle.ts:339-349, 405-413).
func publishStage(output string, files []artifact, hashes, rootDigest string) {
	stage := jsexc.Concat(output, ".staging-", clock.NewUUID())
	if err := bundle.Try(func() {
		bundle.Must(struct{}{}, nodefs.MkdirAll(stage, 0o777))
		for _, f := range files {
			destination := nodefs.Join(stage, f.path)
			bundle.Must(struct{}{}, nodefs.MkdirAll(nodefs.Dirname(destination), 0o777))
			bundle.Must(struct{}{}, nodefs.WriteFileUTF8(destination, f.body, 0o666, false))
		}
		bundle.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(stage, "hashes.txt"), hashes, 0o666, false))
		bundle.Must(struct{}{}, nodefs.WriteFileUTF8(nodefs.Join(stage, "ROOT.sha256"), jsexc.Concat(rootDigest, "\n"), 0o666, false))
		replaceDirectory(stage, output)
	}); err != nil {
		if nodefs.Exists(stage) {
			bundle.Must(struct{}{}, nodefs.RemoveAll(stage))
		}
		bundle.Throw(err)
	}
}
