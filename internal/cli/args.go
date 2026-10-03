package cli

import "slices"

// hasFlag ports src/cli-app.ts:140.
func hasFlag(args []string, flag string) bool { return slices.Contains(args, flag) }

// option ports src/cli-app.ts:144: the element after the first occurrence of
// flag. ok is false when the flag is absent or last (TS `undefined`).
func option(args []string, flag string) (value string, ok bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}
