package cli

import (
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/snapgc"
)

// codexRepository is resolve(option(args, "--repo") ?? process.cwd()):
// an absent or last --repo is the working directory.
func codexRepository(args []string) string {
	repository, ok := option(args, "--repo")
	if !ok {
		repository = jsexc.Must(nodefs.Cwd())
	}
	return nodefs.Resolve(repository)
}

// codexSnapshotGCCommand ports `fl codex snapshot gc` (src/cli-app.ts:3003-3016).
func codexSnapshotGCCommand(e *env, gcArgs []string) error {
	result, err := snapgc.Purge(codexRepository(gcArgs), hasFlag(gcArgs, "--force"))
	if err != nil {
		return err
	}
	e.printJSON(jsjson.MustObject(
		"status", jsjson.MakeString(result.Status),
		"repositoryRoot", jsjson.MakeString(result.RepositoryRoot),
		"objectDirectory", jsjson.MakeString(result.ObjectDirectory),
		"removedEntries", jsjson.MakeNumber(float64(result.RemovedEntries)),
		"referencingPaths", stringList(result.ReferencingPaths...),
		"next", jsjson.MakeString("fl doctor --security"),
	))
	return nil
}

// codexDryRunCommand ports `fl codex --dry-run` (src/cli-app.ts:3017-3020).
func codexDryRunCommand(e *env) error {
	e.printJSON(jsjson.MustObject(
		"adapter", jsjson.MakeString("observed-codex-hook-sidecar"),
		"status", jsjson.MakeString("DRY_RUN"),
		"records", stringList("public session and turn identifiers", "prompt digest only", "clean Git checkpoint when available"),
		"limitation", jsjson.MakeString("Install and trust the emitted hook configuration to observe public lifecycle metadata. FaultLine does not intercept private model state, transcripts, or reasoning."),
	))
	return nil
}

// codexSnapshotCommand ports `fl codex --snapshot` (src/cli-app.ts:3021-3027).
func codexSnapshotCommand(e *env, args []string) error {
	repository := codexRepository(args)
	snapshot, err := snapgc.CaptureCleanGitSnapshot(repository)
	if err != nil {
		return err
	}
	file, err := snapgc.WriteGitSidecarSnapshot(repository, snapshot)
	if err != nil {
		return err
	}
	e.printJSON(jsjson.MustObject(
		"snapshot", snapshot,
		"file", jsjson.MakeString(file),
		"limitation", jsjson.MakeString("This is a clean Git sidecar snapshot, not a live Codex transport event."),
	))
	return nil
}
