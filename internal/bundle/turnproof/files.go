package turnproof

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/nodefs"
)

func isLink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

// safeArtifactPath ports turn-proof-bundle.ts:195.
func safeArtifactPath(root, artifact string) string {
	if !IsSafeRelativeArtifactPath(artifact) {
		bundle.Throw(errors.New(jsexc.Concat("Unsafe artifact path: ", artifact)))
	}
	destination := nodefs.Resolve(root, artifact)
	nested := nodefs.Relative(root, destination)
	if nested == "" || strings.HasPrefix(nested, "..") || nodefs.IsAbsolute(nested) {
		bundle.Throw(errors.New(jsexc.Concat("Artifact path escapes its bundle: ", artifact)))
	}
	return destination
}

// assertNoLinksOrSpecialFiles ports turn-proof-bundle.ts:205.
func assertNoLinksOrSpecialFiles(directory string) {
	stat := bundle.Must(nodefs.Lstat(directory))
	if isLink(stat) || !stat.IsDir() {
		bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle must be a real directory: ", directory)))
	}
	for _, name := range bundle.Must(nodefs.ReadDirNames(directory)) {
		child := nodefs.Join(directory, name)
		childStat := bundle.Must(nodefs.Lstat(child))
		if isLink(childStat) || (!childStat.IsDir() && !childStat.Mode().IsRegular()) {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle contains a symbolic link or special file: ", child)))
		}
		if childStat.IsDir() {
			assertNoLinksOrSpecialFiles(child)
		}
	}
}

// assertRegularFile ports turn-proof-bundle.ts:259.
func assertRegularFile(path, label string, maximumBytes int64) int64 {
	stat := bundle.Must(nodefs.Lstat(path))
	if isLink(stat) || !stat.Mode().IsRegular() {
		bundle.Throw(errors.New(jsexc.Concat(label, " must be a regular non-symlink file")))
	}
	if stat.Size() > maximumBytes {
		bundle.Throw(errors.New(jsexc.Concat(label, " exceeds FaultLine's ", strconv.FormatInt(maximumBytes, 10), " byte read limit")))
	}
	return stat.Size()
}

// readBoundedFile ports turn-proof-bundle.ts:266.
func readBoundedFile(path, label string, maximumBytes int64) []byte {
	assertRegularFile(path, label, maximumBytes)
	return bundle.Must(nodefs.ReadBytes(path))
}

type fileBudget struct{ files, bytes int64 }

// collectFiles ports turn-proof-bundle.ts:271.
func collectFiles(root, current string, budget *fileBudget) []string {
	var files []string
	for _, name := range bundle.Must(nodefs.ReadDirNames(current)) {
		child := nodefs.Join(current, name)
		stat := bundle.Must(nodefs.Lstat(child))
		if isLink(stat) || (!stat.IsDir() && !stat.Mode().IsRegular()) {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle contains a symbolic link or special file: ", child)))
		}
		if stat.IsDir() {
			files = append(files, collectFiles(root, child, budget)...)
			continue
		}
		if stat.Size() > MaxSourceArtifactBytes {
			bundle.Throw(errors.New(jsexc.Concat("Turn proof bundle artifact exceeds FaultLine's read limit: ", child)))
		}
		budget.files++
		budget.bytes += stat.Size()
		if budget.files > MaxArtifacts {
			bundle.Throw(errors.New("Turn proof bundle contains too many files to verify safely"))
		}
		if budget.bytes > MaxTotalBytes {
			bundle.Throw(errors.New("Turn proof bundle exceeds FaultLine's total verification read limit"))
		}
		files = append(files, strings.ReplaceAll(nodefs.Relative(root, child), `\`, "/"))
	}
	return files
}

// writeArtifact ports turn-proof-bundle.ts:296.
func writeArtifact(root, artifact string, b []byte) {
	destination := safeArtifactPath(root, artifact)
	if err := nodefs.MkdirAll(nodefs.Dirname(destination), 0o700); err != nil {
		bundle.Throw(err)
	}
	if err := nodefs.WriteFileBytes(destination, b, 0o600, true); err != nil {
		bundle.Throw(err)
	}
	assertRegularFile(destination, jsexc.Concat("artifact ", artifact), MaxSourceArtifactBytes)
}
