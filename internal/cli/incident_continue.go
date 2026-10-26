package cli

// Stubs for `fl incident status` and `fl incident continue`; Task 6
// replaces this file with the ports of src/cli-app.ts:461
// incidentStatusCommand and src/cli-app.ts:525 continueIncidentCommand.
// The message reuses cli.go's "not yet ported: %s" literal, which
// difftest's unreachableLiterals already waives.

func incidentStatusCommand(e *env, args []string) error {
	_ = e
	return fail("not yet ported: %s", "incident "+arg(args, 0))
}

func incidentContinueCommand(e *env, args []string) error {
	_ = e
	return fail("not yet ported: %s", "incident "+arg(args, 0))
}
