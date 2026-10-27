// Package sidecar ports src/codex-sidecar.ts: the Codex hook sidecar's
// storage under the Git common directory, the hook state machine with its
// lock and stop receipts, and status inspection.
package sidecar // Port of src/codex-sidecar.ts.

import (
	"errors"
	"io/fs"
	"runtime"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
	"github.com/Mizore66/faultline/internal/safegit"
	s "github.com/Mizore66/faultline/internal/schema"
)

// Error ports CodexSidecarError (codex-sidecar.ts:194).
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// fail throws a CodexSidecarError.
func fail(message string) { jsexc.Throw(&Error{Message: message}) }

// IdentifierSchema is codex-sidecar.ts:62.
var IdentifierSchema s.Schema = s.String(s.MinLength(1), s.MaxLength(160),
	s.Regex(s.Pattern(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`), "Identifier contains unsupported characters"))

// filesystemErrorCode ports codex-sidecar.ts:207: a Node error's code, or "".
func filesystemErrorCode(err error) string {
	var e *nodefs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// runHardenedGit ports src/codex-sidecar.ts:233: spawnSync git with
// HOST_COMMAND_SAFE_GIT_CONFIG, -C repository and the hardened environment
// (Node's default 1 MiB maxBuffer, no timeout); its trimmed stdout. A
// failure is a CodexSidecarError, never a LedgerIntegrityError.
func runHardenedGit(repository string, args ...string) (string, error) {
	argv := append(safegit.HostCommand(), "-C", repository)
	argv = append(argv, args...)
	env := gitinv.HardenedGitEnvironment(gitinv.HardenedOptions{})
	if runtime.GOOS != "windows" {
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	r := nodeproc.SpawnSyncWith("git", argv, nodeproc.SpawnSyncOptions{Env: env})
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
		return "", &Error{Message: jsexc.Concat("FaultLine could not safely inspect this Git worktree: ", detail)}
	}
	return jsstr.Trim(jsstr.DecodeUTF8(r.Stdout)), nil
}

// assertSafeDirectory ports src/codex-sidecar.ts:247.
func assertSafeDirectory(path, label string) {
	info, err := nodefs.Lstat(path)
	if err != nil {
		fail(jsexc.Concat(label, " is unavailable: ", jsexc.Message(err)))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		fail(label + " must be a real directory, not a symlink or file.") // coverage gate: race only (safeDirectoryExists or realpathSync accepted the same path just before)
	}
}

// safeDirectoryExists ports src/codex-sidecar.ts:259.
func safeDirectoryExists(path, label string) bool {
	info, err := nodefs.Lstat(path)
	if err != nil {
		if filesystemErrorCode(err) == "ENOENT" {
			return false
		}
		jsexc.Throw(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		fail(label + " must be a real directory, not a symlink or file.")
	}
	return true
}

// ensureSafeChildDirectory ports src/codex-sidecar.ts:272.
func ensureSafeChildDirectory(parent, name, label string, create bool) string {
	assertSafeDirectory(parent, "FaultLine Git metadata directory")
	child := nodefs.Join(parent, name)
	if !safeDirectoryExists(child, label) {
		if !create {
			return child
		}
		if err := nodefs.Mkdir(child, 0o700); err != nil {
			// A second hook/session may initialize the same Git metadata
			// directory concurrently. Accept only a now-verified directory.
			if filesystemErrorCode(err) != "EEXIST" || !safeDirectoryExists(child, label) {
				fail(jsexc.Concat("FaultLine could not create ", label, ": ", jsexc.Message(err)))
			}
		}
	}
	assertSafeDirectory(child, label)
	return child
}

// recordingDirectory ports src/codex-sidecar.ts:297: the recordings
// directory under the actual Git common directory, created (0700) when
// create is set.
func recordingDirectory(cwd string, create bool) string {
	repositoryRoot := nodefs.Resolve(jsexc.Must(runHardenedGit(cwd, "rev-parse", "--show-toplevel")))
	reportedGitDirectory := jsexc.Must(runHardenedGit(repositoryRoot, "rev-parse", "--git-common-dir"))
	if reportedGitDirectory == "" {
		fail("Git did not report a common metadata directory for this worktree.")
	}
	gitDirectory, err := nodefs.RealpathSync(nodefs.Resolve(repositoryRoot, reportedGitDirectory))
	if err != nil {
		fail(jsexc.Concat("FaultLine could not resolve Git metadata storage: ", jsexc.Message(err)))
	}
	assertSafeDirectory(gitDirectory, "FaultLine Git metadata directory")
	faultLinePath := nodefs.Join(gitDirectory, "faultline")
	if !create && !safeDirectoryExists(faultLinePath, "FaultLine sidecar storage") {
		return nodefs.Join(faultLinePath, "recordings")
	}
	faultLineDirectory := ensureSafeChildDirectory(gitDirectory, "faultline", "FaultLine sidecar storage", create)
	recordingsPath := nodefs.Join(faultLineDirectory, "recordings")
	if !create && !safeDirectoryExists(recordingsPath, "FaultLine sidecar recordings") {
		return recordingsPath
	}
	return ensureSafeChildDirectory(faultLineDirectory, "recordings", "FaultLine sidecar recordings", create)
}

// RecordingDirectory ports recordingDirectory (codex-sidecar.ts:297).
func RecordingDirectory(cwd string, create bool) (dir string, err error) {
	defer jsexc.Catch(func(e error) { dir, err = "", e })
	return recordingDirectory(cwd, create), nil
}

// safeRegularFileExists ports src/codex-sidecar.ts:316.
func safeRegularFileExists(path, label string) bool {
	info, err := nodefs.Lstat(path)
	if err != nil {
		if filesystemErrorCode(err) == "ENOENT" {
			return false
		}
		jsexc.Throw(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		fail(label + " must be a regular file, not a symlink or directory.")
	}
	return true
}

// ledgerFileName is `codex-${sha256(sessionId)}.json`.
func ledgerFileName(sessionID string) string {
	return jsexc.Concat("codex-", canonical.SHA256Hex(sessionID), ".json")
}

// LedgerPath ports codexSidecarLedgerPath (codex-sidecar.ts:334): it never
// creates storage.
func LedgerPath(cwd, sessionID string) (path string, err error) {
	defer jsexc.Catch(func(e error) { path, err = "", e })
	bundle.Must(bundle.ParseValue(jsjson.MakeString(sessionID), IdentifierSchema))
	return nodefs.Join(recordingDirectory(cwd, false), ledgerFileName(sessionID)), nil
}
