package cli

// TEMPORARY (Task 6 branch only): Task 4 owns this file and its full
// incidentCommand switch (src/cli-app.ts:573); this minimal router exists so
// the status and continue branches build and run until the merge.
func incidentCommand(e *env, args []string) error {
	var action string
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		return incidentStatusCommand(e, args)
	case "continue":
		return incidentContinueCommand(e, args)
	}
	return fail("not yet ported: incident %s", action)
}
