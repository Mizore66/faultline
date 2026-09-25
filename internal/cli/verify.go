package cli

// verifyCommand ports the "verify" case in src/cli-app.ts:2903.
func verifyCommand(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fail("Usage: fl verify <proof-bundle-directory>")
	}
	return fail("not yet ported: verify")
}
