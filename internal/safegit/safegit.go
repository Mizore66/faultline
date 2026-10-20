// Package safegit holds the fixed `-c` overrides FaultLine passes to every
// host Git invocation outside the materialization path: they disable the
// Git features that can run commands from repository-local config (hooks,
// fsmonitor, filters, transports).
package safegit

// hostCommand is src/doctor.ts:26 HOST_COMMAND_SAFE_GIT_CONFIG, in order.
var hostCommand = []string{
	"-c", "core.hooksPath=/nonexistent/faultline-hooks",
	"-c", "core.fsmonitor=false",
	"-c", "core.useBuiltinFSMonitor=false",
	"-c", "core.untrackedCache=false",
	"-c", "core.preloadIndex=false",
	"-c", "filter.lfs.process=",
	"-c", "filter.lfs.smudge=",
	"-c", "filter.lfs.required=false",
	"-c", "diff.external=",
	"-c", "submodule.recurse=false",
	"-c", "fetch.recurseSubmodules=false",
	"-c", "protocol.allow=never",
	"-c", "protocol.file.allow=never",
	"-c", "protocol.ext.allow=never",
	"-c", "protocol.git.allow=never",
	"-c", "protocol.ssh.allow=never",
	"-c", "protocol.http.allow=never",
	"-c", "protocol.https.allow=never",
}

// HostCommand ports src/doctor.ts:26 HOST_COMMAND_SAFE_GIT_CONFIG. It leaves
// core.autocrlf alone, so clean-worktree checks follow the repository's
// line-ending policy. Each call returns a fresh slice.
func HostCommand() []string {
	return append([]string(nil), hostCommand...)
}

// Doctor ports src/doctor.ts:54 DOCTOR_SAFE_GIT_CONFIG: HostCommand plus
// core.autocrlf=false, for deterministic diagnostic text. Each call returns
// a fresh slice.
func Doctor() []string {
	return append(HostCommand(), "-c", "core.autocrlf=false")
}

// TurnSnapshot is the list src/turn-snapshot.ts:259 runSnapshotGit passes:
// DOCTOR_SAFE_GIT_CONFIG plus core.attributesFile pointed at a path that
// does not exist. Each call returns a fresh slice.
func TurnSnapshot() []string {
	return append(Doctor(), "-c", "core.attributesFile=/nonexistent/faultline-attributes")
}
