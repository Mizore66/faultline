package cli

// codexUsageError is the codex case's closing throw (src/cli-app.ts:3027).
const codexUsageError = "The current build exposes an observed Codex hook sidecar, quarantined turn-snapshot GC, and a clean Git snapshot. Run: fl codex sidecar install --repo <directory> --cli <built-cli.js> --yes, fl codex snapshot gc --repo <directory>, fl codex --dry-run, or fl codex --snapshot --repo <directory>"

// codexCommand ports the codex case of runCli (src/cli-app.ts:2994-3028),
// whose branches are tried in this order: `fl codex snapshot` without gc
// falls through to the flags, and `fl codex sidecar --dry-run` reaches the
// sidecar.
func codexCommand(e *env, args []string) error {
	switch {
	case len(args) > 0 && args[0] == "record":
		return recordCommand(e, args[1:])
	case len(args) > 0 && args[0] == "sidecar":
		return codexSidecarCommand(e, args[1:])
	case len(args) > 1 && args[0] == "snapshot" && args[1] == "gc":
		return codexSnapshotGCCommand(e, args[2:])
	case hasFlag(args, "--dry-run"):
		return codexDryRunCommand(e)
	case hasFlag(args, "--snapshot"):
		return codexSnapshotCommand(e, args)
	}
	return fail("%s", codexUsageError)
}
