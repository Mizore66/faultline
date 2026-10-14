package cli

import (
	"errors"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

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

// requiredOption ports src/cli-app.ts:149: an absent, empty or "--"-prefixed
// value is missing.
func requiredOption(args []string, flag string) (string, error) {
	value, ok := option(args, flag)
	if !ok || value == "" || strings.HasPrefix(value, "--") {
		return "", fail("Missing required option: %s", flag)
	}
	return value, nil
}

// witnessStore ports src/cli-app.ts:252. Resolving can throw (jsexc) when
// process.cwd() fails.
func witnessStore(args []string) string {
	store, ok := option(args, "--store")
	if !ok {
		store = ".faultline/witnesses"
	}
	return nodefs.Resolve(store)
}

// readJSONInput ports src/cli-app.ts:244: every failure, resolve's included,
// becomes "Unable to read JSON input <file>: <message>".
func readJSONInput(file string) (jsjson.Value, error) {
	var v jsjson.Value
	if err := jsexc.Try(func() { v = jsexc.Must(bundle.ParseJSONFile(nodefs.Resolve(file))) }); err != nil {
		return jsjson.Value{}, errors.New(jsexc.Concat("Unable to read JSON input ", file, ": ", jsexc.Message(err)))
	}
	return v, nil
}
